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

# ── dispatch ─────────────────────────────────────────────────────────
#
# The whole spawning path had no coverage at all, in a project whose answer to
# "an idle Claude agent will not wake" is "dispatch a fresh one instead".

fake_harness() {
    printf '#!/bin/sh\nexec sleep 30\n' > "$TEST_TMPDIR/bin/$1"
    chmod +x "$TEST_TMPDIR/bin/$1"
}

mesh_in_fake_home() { HOME="$FAKE_HOME" "$MESH_BIN" "$@"; }

@test "dispatch runs the harness as the pane's own process" {
    fake_harness claude
    run mesh_in_fake_home dispatch --task "audit it" --harness claude
    assert_ok
    assert_contains "$output" "dispatched claude in pane"
    assert_eq "$(msql 'SELECT task FROM dispatches;')" "audit it"
}

@test "dispatch records the pane it created" {
    fake_harness claude
    mesh_in_fake_home dispatch --task "audit it" --harness claude >/dev/null
    local pane
    pane=$(msql 'SELECT tmux_pane FROM dispatches;')
    assert_not_empty "$pane"
    run bash -c "tmux list-panes -a -F '#{pane_id}'"
    assert_contains "$output" "$pane"
}

@test "dispatch records who asked for the work" {
    fake_harness claude
    mesh_in_fake_home dispatch --task "audit it" --harness claude --from requester >/dev/null
    assert_eq "$(msql 'SELECT reply_to_session FROM dispatches;')" "requester"
}

@test "dispatch refuses a harness that is not on PATH" {
    PATH="$TEST_TMPDIR/bin:/bin:/usr/bin" run mesh_in_fake_home dispatch --task x --harness gemini
    assert_fail
    assert_contains "$output" "not on PATH"
    assert_eq "$(msql 'SELECT COUNT(*) FROM dispatches;')" "0"
}

# A dispatched pane inherits the tmux server's environment, not your shell's, so
# anything your profile sets or clears is missing. Observed live: a corporate
# NODE_EXTRA_CA_CERTS in the server environment broke every dispatched agent,
# while the same agent started by hand worked, because the user's shell unsets it.
@test "dispatch passes an env override through to the pane" {
    printf '#!/bin/sh\nprintf %%s "$MESH_PROBE" > %s/probe\nexec sleep 30\n' "$TEST_TMPDIR" \
        > "$TEST_TMPDIR/bin/claude"
    chmod +x "$TEST_TMPDIR/bin/claude"
    mesh_in_fake_home dispatch --task x --harness claude --env MESH_PROBE=cleared >/dev/null
    local waited=0
    while [[ ! -f "$TEST_TMPDIR/probe" && "$waited" -lt 30 ]]; do
        sleep 0.1
        waited=$((waited + 1))
    done
    assert_eq "$(cat "$TEST_TMPDIR/probe")" "cleared"
}

@test "dispatch refuses a harness it does not know" {
    run mesh_in_fake_home dispatch --task x --harness emacs
    assert_fail
    assert_contains "$output" "unknown harness"
    assert_eq "$(msql 'SELECT COUNT(*) FROM dispatches;')" "0"
}

@test "dispatch into a worktree creates it under the tmux-worktree root" {
    fake_harness claude
    local repo="$TEST_TMPDIR/repo"
    mkdir -p "$repo"
    git -C "$repo" init -q
    git -C "$repo" -c user.email=t@t -c user.name=t commit -q --allow-empty -m init
    run mesh_in_fake_home dispatch --task x --harness claude --worktree feat/x --cwd "$repo"
    assert_ok
    assert_dir "$FAKE_HOME/.tmux-worktree/repo/feat/x"
    assert_eq "$(msql 'SELECT worktree_branch FROM dispatches;')" "feat/x"
}

@test "claim-dispatch hands the task to whoever starts in that pane" {
    msql "INSERT INTO dispatches (tmux_pane, harness, task) VALUES ('%9','claude','do it');"
    run "$MESH_BIN" claim-dispatch --session D --pane %9
    assert_ok
    echo "$output" | jq -e '.task == "do it"'
}

@test "claim-dispatch returns null when that pane has nothing waiting" {
    run "$MESH_BIN" claim-dispatch --session D --pane %9
    assert_ok
    assert_eq "$output" "null"
}

# ── menu and goto ────────────────────────────────────────────────────

# display-menu needs an attached client, which a headless test cannot provide,
# so the menu is checked by recording what mesh asks tmux to draw.
record_tmux() {
    cat > "$TEST_TMPDIR/bin/tmux" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >> "$TEST_TMPDIR/tmux.log"
exec $REAL_TMUX -L $TMUX_SOCKET "\$@"
EOF
    chmod +x "$TEST_TMPDIR/bin/tmux"
}

@test "the menu lists each agent with its pending count" {
    insert_agent s1 claude reviewer %1 mesh:0.0
    insert_message human s1 "waiting"
    record_tmux
    "$MESH_BIN" menu >/dev/null 2>&1 || true
    run grep display-menu "$TEST_TMPDIR/tmux.log"
    assert_contains "$output" "reviewer"
    assert_contains "$output" "@1"
}

@test "the menu says so when nothing is registered" {
    record_tmux
    "$MESH_BIN" menu >/dev/null 2>&1 || true
    run cat "$TEST_TMPDIR/tmux.log"
    assert_contains "$output" "no agents registered"
}

@test "goto selects the target pane" {
    tmux split-window -d -t mesh
    "$MESH_BIN" goto "$(tmux display-message -p -t mesh:0.1 '#{session_name}:#{window_index}.#{pane_index}')"
    assert_eq "$(tmux display-message -p -t mesh '#{pane_index}')" "1"
}

# ── watch ────────────────────────────────────────────────────────────

@test "watch prints new traffic as it arrives" {
    insert_agent s1 claude alpha %1
    "$MESH_BIN" watch > "$TEST_TMPDIR/watch.out" 2>&1 &
    local pid=$!
    sleep 1
    insert_message s1 human "seen by watch"
    sleep 2
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    run cat "$TEST_TMPDIR/watch.out"
    assert_contains "$output" "seen by watch"
    assert_match "$output" '*[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*'
}
