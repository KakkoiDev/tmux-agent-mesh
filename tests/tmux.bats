#!/usr/bin/env bats

bats_require_minimum_version 1.5.0

# Everything here runs against a tmux server on a private socket, because these
# are the parts of mesh that only exist as tmux state: hooks, key bindings, the
# menu, and the commands that create panes. None of it was covered before, which
# is how a global `set-hook` that silently replaced any other pane-died hook
# survived alongside a README claiming coexistence with the sibling plugins.

load helpers

setup() {
    setup_test_env
    start_test_tmux
}

teardown() {
    stop_test_tmux
    teardown_test_env
}

# pane-died is a pane hook, so a bare `show-hooks -g` does not list it. It has
# to be asked for by name.
hooks_for() { tmux show-hooks -g "$1" 2>/dev/null || true; }

# ── pane-died ────────────────────────────────────────────────────────

@test "the plugin registers cleanup on pane-died" {
    run_plugin
    run hooks_for pane-died
    assert_contains "$output" "mesh.sh cleanup"
}

@test "the plugin keeps a pane-died hook somebody else registered" {
    tmux set-hook -g pane-died "run-shell -b 'echo other'"
    run_plugin
    run hooks_for pane-died
    assert_contains "$output" "mesh.sh cleanup"
    assert_contains "$output" "echo other"
}

# tmux reloads the config on every `source-file`, so an append that does not
# check first accumulates one duplicate hook per reload.
@test "loading the plugin twice does not duplicate the hook" {
    run_plugin
    run_plugin
    assert_eq "$(hooks_for pane-died | grep -c 'mesh.sh cleanup')" "1"
}

# ── key binding ──────────────────────────────────────────────────────

@test "the plugin binds the menu key" {
    run_plugin
    run bash -c "tmux list-keys -T prefix | grep 'mesh.sh menu'"
    assert_ok
}

@test "the menu key is configurable" {
    tmux set -g @agent-mesh-keybinding X
    rm -f "$MESH_DIR/config_cache"
    run_plugin
    run bash -c "tmux list-keys -T prefix | grep 'mesh.sh menu'"
    assert_contains "$output" " X "
}

# The shipped default was `m`, which is tmux's own select-pane -m, so installing
# mesh silently took a built-in away from every user.
@test "the shipped default menu key does not replace a tmux built-in" {
    rm -f "$MESH_DIR/config_cache"
    run_plugin
    run bash -c "tmux list-keys -T prefix | grep -E '^bind-key +-T prefix +m '"
    assert_contains "$output" "select-pane"
}

# ── status option ────────────────────────────────────────────────────

@test "the plugin initialises the status option" {
    run_plugin
    run bash -c "tmux show-options -gqv @agent-mesh-status"
    assert_ok
}

# ── doctor ───────────────────────────────────────────────────────────
#
# doctor is what a user runs to decide whether to believe mesh works, so its
# coverage matters more than average. It used to check dependencies and hook
# wiring and nothing else: not the PATH the hooks depend on, not the tmux state
# that proves the plugin loaded, not the config cache whose corruption kills
# every hook at once.

doctor_in_fake_home() { HOME="$FAKE_HOME" "$MESH_BIN" doctor; }

@test "doctor fails when the database is missing" {
    rm -f "$DB"
    run doctor_in_fake_home
    assert_fail
    assert_contains "$output" "FAIL  database exists"
}

# Wiring is opt-in, so "not wired" is a choice. A missing codex hooks.json used
# to be a hard failure while a missing claude settings.json was a skip.
@test "doctor treats an unwired harness as a choice, not a failure" {
    printf '#!/bin/sh\nexit 0\n' > "$TEST_TMPDIR/bin/codex"
    chmod +x "$TEST_TMPDIR/bin/codex"
    run doctor_in_fake_home
    assert_contains "$output" "info  codex not wired"
    refute_contains "$output" "FAIL  codex"
}

# Half-wired is the only genuinely broken harness state, and the only one
# nobody notices.
@test "doctor fails a half-wired harness" {
    printf '#!/bin/sh\nexit 0\n' > "$TEST_TMPDIR/bin/codex"
    chmod +x "$TEST_TMPDIR/bin/codex"
    mkdir -p "$FAKE_HOME/.codex"
    jq -n '{hooks:{Stop:[{matcher:"",hooks:[{type:"command",command:"tmux-agent-mesh hook Stop --harness codex"}]}]}}' \
        > "$FAKE_HOME/.codex/hooks.json"
    run doctor_in_fake_home
    assert_fail
    assert_contains "$output" "codex half-wired: 1 of 4"
}

@test "doctor sees the tmux state the plugin creates" {
    run_plugin
    run doctor_in_fake_home
    assert_contains "$output" "ok    cleanup registered on pane-died"
    assert_contains "$output" "ok    menu key bound"
    assert_contains "$output" "ok    symlink points at this checkout"
}

@test "doctor reports the tmux state as missing before the plugin loads" {
    run doctor_in_fake_home
    assert_contains "$output" "FAIL  cleanup registered on pane-died"
    assert_contains "$output" "FAIL  menu key bound"
}
