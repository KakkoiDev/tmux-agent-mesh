#!/usr/bin/env bats

bats_require_minimum_version 1.5.0
# Every one of these runs mesh.sh as a real subprocess. The in-process suites
# stub load_config away, which is exactly why the CLI paths could ignore the
# whole configuration for as long as they did: seven commands never loaded it,
# so @agent-mesh-enabled off did not stop a send, the configured caps were
# ignored, and @agent-mesh-on-mail never fired.

load helpers

setup() {
    setup_test_env
    source_mesh_functions
    cmd_register --session A --harness claude --alias alpha --cwd /tmp >/dev/null
}

teardown() {
    teardown_test_env
}

# A fake tmux ahead of the real one on PATH, so `refresh` reads known values
# without touching the developer's server. With no arguments every option
# answers empty and mesh falls back to its own defaults.
fake_tmux() {
    mkdir -p "$TEST_TMPDIR/bin"
    cat > "$TEST_TMPDIR/bin/tmux" <<EOF
#!/usr/bin/env bash
if [[ "\$1" == "show-option" && "\$3" == "${1:-}" ]]; then
    printf '%s' "${2:-}"
fi
exit 0
EOF
    chmod +x "$TEST_TMPDIR/bin/tmux"
}

# ── the kill switch and the caps reach the CLI ────────────────────────

@test "a CLI send obeys the kill switch" {
    plant_config "ENABLED='off'"
    run "$MESH_BIN" send --from A --to human --message x
    assert_fail
    assert_contains "$output" "disabled"
    assert_eq "$(count_messages)" "0"
}

@test "a CLI broadcast obeys the kill switch" {
    plant_config "ENABLED='off'"
    run "$MESH_BIN" broadcast --from A --message x
    assert_fail
    assert_contains "$output" "disabled"
}

@test "a CLI send obeys the configured hop cap" {
    plant_config "MAX_HOPS='1'"
    run "$MESH_BIN" send --from A --to human --hops 2 --message x
    assert_fail
    assert_contains "$output" "hop limit"
    assert_eq "$(count_messages)" "0"
}

@test "a CLI send obeys the configured thread cap" {
    plant_config "MAX_THREAD_MSGS='1'"
    "$MESH_BIN" send --from A --to human --thread t-cap --message one >/dev/null
    run "$MESH_BIN" send --from A --to human --thread t-cap --message two
    assert_fail
    assert_contains "$output" "message limit"
    assert_eq "$(count_messages)" "1"
}

@test "a CLI broadcast obeys the configured fan-out cap" {
    cmd_register --session B --harness claude --cwd /tmp >/dev/null
    cmd_register --session C --harness claude --cwd /tmp >/dev/null
    plant_config "MAX_BROADCAST='1'"
    run "$MESH_BIN" broadcast --from A --message x
    assert_fail
    assert_contains "$output" "max-broadcast"
    assert_eq "$(count_messages)" "0"
}

@test "pi-deliver obeys the configured pi delivery mode" {
    cmd_register --session P --harness pi --cwd /tmp >/dev/null
    cmd_send --from A --to P --message "held" >/dev/null
    plant_config "PI_DELIVERY='off'"
    run "$MESH_BIN" pi-deliver --session P --mode push
    assert_ok
    assert_empty "$output"
    assert_eq "$(pending_for P)" "1"
}

# ── the human notification hook ───────────────────────────────────────

# @agent-mesh-on-mail is the documented way to be told an agent wrote to you.
# It never fired: _fire_mail_hook read a HOOK_ON_MAIL that nothing had set.
@test "mail to the human fires the on-mail hook" {
    plant_config "HOOK_ON_MAIL='printf %s fired >> $TEST_TMPDIR/fired'"
    "$MESH_BIN" send --from A --to human --message x >/dev/null
    local waited=0
    while [[ ! -f "$TEST_TMPDIR/fired" && "$waited" -lt 20 ]]; do
        sleep 0.1
        waited=$((waited + 1))
    done
    assert_file "$TEST_TMPDIR/fired"
}

# ── the cache is sourced, so it has to be syntactically valid ─────────

# A single quote in any option value used to produce a syntax-error cache file,
# and the bare `source` under `set -euo pipefail` then killed every hook.
@test "refresh writes a sourceable cache for a value containing a quote" {
    fake_tmux @agent-mesh-icon-mail "it's fine"
    PATH="$TEST_TMPDIR/bin:$PATH" run "$MESH_BIN" refresh
    assert_ok
    run bash -n "$MESH_DIR/config_cache"
    assert_ok
    assert_eq "$(bash -c "source '$MESH_DIR/config_cache'; printf '%s' \"\$ICON_MAIL\"")" "it's fine"
}

# The hook path sources the cache, so a cache built from a quoted option value
# used to abort every hook under `set -euo pipefail` before it delivered
# anything. The hook still exits 0 either way; what breaks is the delivery.
@test "a hook still delivers after a quoted option value is cached" {
    cmd_register --session B --harness claude --cwd /tmp >/dev/null
    cmd_send --from A --to B --message "survives the cache" >/dev/null
    rm -f "$MESH_DIR/config_cache"
    fake_tmux @agent-mesh-icon-mail "it's fine"
    PATH="$TEST_TMPDIR/bin:$PATH" "$MESH_BIN" refresh
    run bash -c "printf '{\"session_id\":\"B\"}' | '$MESH_BIN' hook Stop --harness claude"
    assert_ok
    assert_contains "$output" "survives the cache"
}

@test "refresh rebuilds a stale cache instead of reusing it" {
    plant_config "ENABLED='off'"
    fake_tmux
    PATH="$TEST_TMPDIR/bin:$PATH" "$MESH_BIN" refresh
    assert_eq "$(bash -c "source '$MESH_DIR/config_cache'; printf '%s' \"\$ENABLED\"")" "on"
    run "$MESH_BIN" send --from A --to human --message x
    assert_ok
    assert_eq "$(count_messages)" "1"
}

# ── not installed means inert ─────────────────────────────────────────

@test "loading config does not create the data dir on an uninstalled machine" {
    local absent="$TEST_TMPDIR/never"
    MESH_DIR="$absent" MESH_DB="$absent/mesh.db" run "$MESH_BIN" hook Stop </dev/null
    assert_ok
    refute_dir "$absent"
}
