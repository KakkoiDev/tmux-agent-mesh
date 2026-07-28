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
