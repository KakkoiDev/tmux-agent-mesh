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

# ── documented surface ───────────────────────────────────────────────

# pi-deliver and reset-streak were dispatched and covered by 33 tests while
# appearing in neither --help nor the README.
@test "every command the dispatcher accepts is in --help" {
    local usage verb
    usage=$(run_mesh_help)
    for verb in $(/usr/bin/sed -n '/^case "\${1:-}" in$/,/^esac$/p' "$SCRIPTS_DIR/mesh.sh" \
                  | grep -oE '^ +[a-z-]+\)' | tr -d ' )'); do
        assert_contains "$usage" "$verb"
    done
}

# The project's whole claim is that it needs no keystroke injection. An
# @agent-mesh-wake option promised an opt-in send-keys path and never
# implemented it; this is the guarantee that replaced it.
@test "no code path sends keystrokes to a pane" {
    refute grep -rn "send-keys" "$SCRIPTS_DIR" "$PROJECT_ROOT/agent-mesh.tmux" \
        "$PROJECT_ROOT/pi-extension"
}

@test "no configuration option is loaded and then never read" {
    local v
    for v in $(grep -oE '^[A-Z_]+=\$\(get_tmux_option' "$SCRIPTS_DIR/helpers.sh" \
               | /usr/bin/sed 's/=.*//'); do
        grep -q "\${$v" "$SCRIPTS_DIR/mesh.sh" \
            || _afail "$v is loaded from a tmux option and never read"
    done
}

# ── portability ──────────────────────────────────────────────────────
#
# helpers.sh is not part of source_mesh_functions (the awk filter strips its
# `source` line), so these load it directly. The uname stub is what makes them
# mean anything: without it both branches would be checked on macOS only.

@test "watch formats a timestamp with BSD date flags on macos" {
    source "$SCRIPTS_DIR/helpers.sh"
    uname() { echo Darwin; }
    date() { printf '%s' "$*"; }
    run _fmt_time 0
    assert_contains "$output" "-r 0"
}

@test "watch formats a timestamp with GNU date flags elsewhere" {
    source "$SCRIPTS_DIR/helpers.sh"
    uname() { echo Linux; }
    date() { printf '%s' "$*"; }
    run _fmt_time 0
    assert_contains "$output" "-d @0"
}

@test "watch prints a real clock time on this platform" {
    source "$SCRIPTS_DIR/helpers.sh"
    run _fmt_time 0
    assert_match "$output" '[0-9][0-9]:[0-9][0-9]:[0-9][0-9]'
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

# The hook payload arrives on stdin from the harness, so only a subprocess run
# exercises how it is read and parsed. Both of these delivered nothing at all:
# `read -r json` took one line, and the string-match parser needed
# "session_id":"x" with no space after the colon.
@test "hook reads a pretty-printed JSON payload" {
    cmd_register --session A --harness claude --cwd /tmp >/dev/null
    cmd_register --session B --harness claude --cwd /tmp >/dev/null
    cmd_send --from A --to B --message "multiline payload" >/dev/null
    run bash -c "printf '{\n  \"session_id\": \"B\",\n  \"cwd\": \"/tmp\"\n}\n' | '$MESH_BIN' hook Stop --harness claude"
    assert_ok
    assert_eq "$(printf '%s' "$output" | jq -r '.decision')" "block"
    assert_contains "$output" "multiline payload"
}

@test "hook tolerates whitespace after a JSON key" {
    cmd_register --session A --harness claude --cwd /tmp >/dev/null
    cmd_register --session B --harness claude --cwd /tmp >/dev/null
    cmd_send --from A --to B --message "spaced payload" >/dev/null
    run bash -c "printf '{\"session_id\": \"B\"}' | '$MESH_BIN' hook Stop --harness claude"
    assert_ok
    assert_eq "$(printf '%s' "$output" | jq -r '.decision')" "block"
}

@test "hook registers from a pretty-printed session start" {
    run bash -c "printf '{\n  \"session_id\": \"PRETTY\",\n  \"cwd\": \"/tmp/proj\"\n}\n' | '$MESH_BIN' hook SessionStart --harness claude"
    assert_ok
    assert_eq "$(msql "SELECT cwd FROM agents WHERE session_id='PRETTY';")" "/tmp/proj"
}

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
