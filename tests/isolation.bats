#!/usr/bin/env bats


bats_require_minimum_version 1.5.0
# Guards against breaking the sibling tmux-agent-* plugins, and against
# leaking anything onto a harness's stderr. Both classes of bug are invisible
# to the functional suites and were found only by running a real agent.

load helpers

setup() {
    setup_test_env
    source_mesh_functions
}

teardown() {
    teardown_test_env
}

# ── env namespace ────────────────────────────────────────────────────

# Regression: mesh.sh used to read a bare $DB. tmux-agent-tracker reads the
# same name, so pointing mesh at its database also pointed tracker at it and
# every tracker hook died with "Parse error: no such table: sessions".
# Observed live in a Pi pane.
@test "mesh reads no unprefixed environment overrides" {
    run grep -nE '^[A-Z_]+="\$\{(DB|NOTIFY_DIR|DELIVERY_LOG|CACHE|TRACKER_DIR)' \
        "$PROJECT_ROOT/scripts/mesh.sh"
    assert_fail
}

@test "every environment override is MESH_ prefixed" {
    local v
    for v in $(grep -oE '\$\{[A-Z_]+:-' "$PROJECT_ROOT/scripts/mesh.sh" \
               | tr -d '${:-' | sort -u); do
        case "$v" in
            MESH_*|HOME|PWD|TMUX|TMUX_PANE|DEBUG_LOG|ENABLED|DELIVERY|PI_DELIVERY) ;;
            MAX_*|ICON_MAIL|HOOK_ON_MAIL|KEY_QUIT|CODEX_HOME) ;;
            *) printf 'unprefixed override: %s\n' "$v"; return 1 ;;
        esac
    done
}

@test "setting a bare DB does not redirect the mesh database" {
    local other="$TEST_TMPDIR/other.db"
    DB="$other" MESH_DIR="$MESH_DIR" MESH_DB="$MESH_DB" \
        "$MESH_BIN" register --session envtest --harness claude --cwd /tmp
    refute_file "$other"
    assert_eq "$(msql "SELECT COUNT(*) FROM agents WHERE session_id='envtest';")" "1"
}

@test "tracker env vars are never consumed" {
    run grep -nE 'TRACKER_DIR|tracker\.db' "$PROJECT_ROOT/scripts/mesh.sh"
    assert_fail
}

# ── stderr hygiene ───────────────────────────────────────────────────
#
# Harnesses surface hook stderr to the user, and Claude Code treats a
# non-empty stderr with exit 2 as a blocking error. A cosmetic failure inside
# mesh must never reach it.

@test "status update stays silent when the data dir is unwritable" {
    local ro="$TEST_TMPDIR/ro"
    mkdir -p "$ro"
    chmod 500 "$ro"
    MESH_DIR="$ro" run _update_status
    chmod 700 "$ro"
    assert_ok
    assert_empty "$output"
}

@test "turn-end hook writes nothing to stderr on a clean mailbox" {
    cmd_register --session S1 --harness claude --cwd /tmp >/dev/null
    run --separate-stderr _hook_turn_end claude S1 '{}'
    assert_empty "$stderr"
}

@test "turn-end hook writes nothing to stderr when delivering" {
    cmd_register --session S1 --harness claude --cwd /tmp >/dev/null
    cmd_register --session S2 --harness claude --cwd /tmp >/dev/null
    cmd_send --from S1 --to S2 --message "x" >/dev/null
    run --separate-stderr _hook_turn_end claude S2 '{}'
    assert_empty "$stderr"
}

@test "prompt hook writes nothing to stderr" {
    cmd_register --session S1 --harness claude --cwd /tmp >/dev/null
    run --separate-stderr _hook_prompt claude S1
    assert_empty "$stderr"
}

@test "session start writes nothing to stderr with no peers" {
    msql "DELETE FROM agents;"
    run --separate-stderr _hook_session_start claude solo /tmp
    assert_empty "$stderr"
}

# ── hook robustness ──────────────────────────────────────────────────

@test "hook exits zero on malformed stdin" {
    run bash -c "printf 'not json at all' | MESH_DIR='$MESH_DIR' MESH_DB='$MESH_DB' MESH_NOTIFY_DIR='$NOTIFY_DIR' '$MESH_BIN' hook Stop"
    assert_ok
}

@test "hook exits zero on empty stdin" {
    run bash -c "printf '' | MESH_DIR='$MESH_DIR' MESH_DB='$MESH_DB' MESH_NOTIFY_DIR='$NOTIFY_DIR' '$MESH_BIN' hook Stop"
    assert_ok
}

@test "hook exits zero for an unknown harness name" {
    run bash -c "echo '{\"session_id\":\"x\"}' | MESH_DIR='$MESH_DIR' MESH_DB='$MESH_DB' MESH_NOTIFY_DIR='$NOTIFY_DIR' '$MESH_BIN' hook Stop --harness nonesuch"
    assert_ok
}

@test "hook exits zero when the database is unreadable" {
    chmod 000 "$DB"
    run bash -c "echo '{\"session_id\":\"x\"}' | MESH_DIR='$MESH_DIR' MESH_DB='$MESH_DB' MESH_NOTIFY_DIR='$NOTIFY_DIR' '$MESH_BIN' hook Stop"
    chmod 600 "$DB"
    assert_ok
}
