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

# pane-exited is a pane hook, so a bare `show-hooks -g` does not list it. It has
# to be asked for by name.
hooks_for() { tmux show-hooks -g "$1" 2>/dev/null || true; }

# Spawn a window whose pane runs `sleep`, and return its pane id. A long-running
# command is required: a pane whose command exits immediately can be gone before
# the caller reads the id back.
victim_pane() {
    local name="$1"
    tmux new-window -d -n "$name" 'sleep 100'
    tmux list-panes -a -F '#{pane_id} #{window_name}' | awk -v n="$name" '$2==n{print $1}'
}

# Scoped to the sessions a test registered. `count_agents` cannot be used here:
# loading the plugin registers the human row, so every absolute count is one
# higher than the test planted and an off-by-one reads as a reap failure.
count_reap_agents() { msql "SELECT COUNT(*) FROM agents WHERE session_id LIKE 'reap%';"; }

# The reap runs from `run-shell -b`, so it is asynchronous by construction and
# the debounce adds a scheduled trailing pass. Poll instead of sleeping a fixed
# amount, and keep the budget above the debounce window.
wait_for_reap() {
    local sid="$1" i=0
    while [[ $i -lt 120 ]]; do
        agent_exists "$sid" || return 0
        sleep 0.1
        i=$((i + 1))
    done
    return 1
}

# ── the teardown hooks ───────────────────────────────────────────────
#
# No single hook covers pane teardown on 3.5a with remain-on-exit off, which is
# the default. Measured: a pane whose process exits fires pane-exited; kill-pane
# fires only after-kill-pane; kill-window fires only window-unlinked;
# kill-session fires session-closed. See the matrix in agent-mesh.tmux.
CLEANUP_HOOKS="pane-exited after-kill-pane window-unlinked session-closed"

@test "the plugin registers cleanup on every teardown hook" {
    run_plugin
    local h
    for h in $CLEANUP_HOOKS; do
        assert_contains "$(hooks_for "$h")" "mesh-wrapper.sh cleanup"
    done
}

@test "the plugin keeps a teardown hook somebody else registered" {
    local h
    for h in $CLEANUP_HOOKS; do
        tmux set-hook -g "$h" "run-shell -b 'echo other'"
    done
    run_plugin
    for h in $CLEANUP_HOOKS; do
        assert_contains "$(hooks_for "$h")" "mesh-wrapper.sh cleanup"
        assert_contains "$(hooks_for "$h")" "echo other"
    done
}

# tmux reloads the config on every `source-file`, so an append that does not
# check first accumulates one duplicate hook per reload.
@test "loading the plugin twice does not duplicate any hook" {
    run_plugin
    run_plugin
    local h
    for h in $CLEANUP_HOOKS; do
        assert_eq "$(hooks_for "$h" | grep -c 'mesh-wrapper.sh cleanup')" "1"
    done
}

# `pane-died` fires only while `remain-on-exit` is on, and it is off by default,
# so binding cleanup there left the plugin with no working cleanup path at all.
# Assert the old hook is gone, or an upgrade keeps reaping twice per pane close.
@test "the plugin does not leave cleanup on pane-died" {
    run_plugin
    run hooks_for pane-died
    refute_contains "$output" "mesh.sh cleanup"
}

@test "loading the plugin removes a cleanup hook left on pane-died" {
    tmux set-hook -ga pane-died "run-shell -b '$SCRIPTS_DIR/mesh.sh cleanup'"
    run_plugin
    assert_empty "$(hooks_for pane-died | grep 'mesh.sh cleanup' || true)"
}

# ── the reap actually happens ────────────────────────────────────────
#
# Every hook test above asserts only that a hook *string* is registered. None of
# them asserted a row was reaped, and that is precisely how V4 survived 319
# tests: the hook was present, plausible, and dead, because `pane-died` needs
# `remain-on-exit` on and nothing turns it on.

@test "an agent is reaped when its pane is killed" {
    run_plugin
    tmux set-option -g remain-on-exit off

    local pane
    pane=$(victim_pane victim)
    assert_not_empty "$pane"

    "$MESH_BIN" register --session reapme --harness claude --pane "$pane" >/dev/null
    assert_eq "$(count_reap_agents)" "1"

    tmux kill-pane -t "$pane"
    wait_for_reap reapme || _afail "reapme survived kill-pane"
    refute agent_exists reapme
}

# The case pane-exited does cover, and the only one it covers: the agent's own
# process ending. Distinct from kill-pane, which fires a different hook.
@test "an agent is reaped when its own process exits" {
    run_plugin
    tmux set-option -g remain-on-exit off

    local pane
    pane=$(victim_pane selfexit)
    assert_not_empty "$pane"

    "$MESH_BIN" register --session reapself --harness claude --pane "$pane" >/dev/null
    assert_eq "$(count_reap_agents)" "1"

    # Kill the command, not the pane, so tmux sees the process exit.
    tmux send-keys -t "$pane" C-c
    wait_for_reap reapself || _afail "reapself survived its process exiting"
    refute agent_exists reapself
}

# kill-window fires neither pane-exited nor after-kill-pane, so an agent in a
# window the user closes was invisible to every earlier version of this hook set.
@test "an agent is reaped when its window is killed" {
    run_plugin
    tmux set-option -g remain-on-exit off

    local pane
    pane=$(victim_pane doomedwin)
    assert_not_empty "$pane"

    "$MESH_BIN" register --session reapwin --harness claude --pane "$pane" >/dev/null
    assert_eq "$(count_reap_agents)" "1"

    tmux kill-window -t doomedwin
    wait_for_reap reapwin || _afail "reapwin survived kill-window"
    refute agent_exists reapwin
}

# kill-session fires session-closed only. tmux-worktree closes a whole session
# per branch, so this is the teardown the sibling plugins actually trigger.
@test "an agent is reaped when its session is killed" {
    run_plugin
    tmux set-option -g remain-on-exit off

    tmux new-session -d -s doomedsess 'sleep 100'
    local pane
    pane=$(tmux list-panes -t doomedsess -F '#{pane_id}' | head -1)
    assert_not_empty "$pane"

    "$MESH_BIN" register --session reapsess --harness claude --pane "$pane" >/dev/null
    assert_eq "$(count_reap_agents)" "1"

    tmux kill-session -t doomedsess
    wait_for_reap reapsess || _afail "reapsess survived kill-session"
    refute agent_exists reapsess
}

# A burst is the case the debounce exists for, and the case it can break: if a
# debounced call returns without scheduling the trailing pass, the last pane to
# die is never reaped.
@test "every agent is reaped when several panes close at once" {
    run_plugin
    tmux set-option -g remain-on-exit off

    local i pane panes=()
    for i in 1 2 3 4; do
        pane=$(victim_pane "victim$i")
        assert_not_empty "$pane"
        "$MESH_BIN" register --session "reap$i" --harness claude --pane "$pane" >/dev/null
        panes+=("$pane")
    done
    assert_eq "$(count_reap_agents)" "4"

    for pane in "${panes[@]}"; do
        tmux kill-pane -t "$pane" 2>/dev/null || true
    done

    for i in 1 2 3 4; do
        wait_for_reap "reap$i" || _afail "reap$i survived its pane closing"
    done
    assert_eq "$(count_reap_agents)" "0"
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
# The TUI is the only way a human can create a channel or manage members, and
# nothing bound it: it existed as a Go subcommand nobody was told about.
@test "the plugin binds the TUI key" {
    run_plugin
    run bash -c "tmux list-keys -T prefix | grep 'mesh-tui.sh'"
    assert_ok
    assert_contains "$output" "new-window"
}

@test "the TUI key is configurable" {
    tmux set -g @agent-mesh-tui-keybinding Y
    rm -f "$MESH_DIR/config_cache"
    run_plugin
    run bash -c "tmux list-keys -T prefix | grep 'mesh-tui.sh'"
    assert_contains "$output" " Y "
}

# Two bindings that both open something, on the same key, is one of them lost.
prefix_key_for() { tmux list-keys -T prefix | grep -F "$1" | sed -n 's/.*-T prefix *\([^ ]*\) .*/\1/p'; }

@test "the TUI key and the menu key are not the same key" {
    run_plugin
    local menu_key tui_key
    menu_key=$(prefix_key_for 'mesh.sh menu')
    tui_key=$(prefix_key_for 'mesh-tui.sh')
    assert_not_empty "$menu_key"
    assert_not_empty "$tui_key"
    assert_ne "$tui_key" "$menu_key"
}

# The launcher runs with no terminal to report to, so a missing binary has to
# say what to do rather than close the window on the way past. Run from a copy,
# because the real checkout may well have bin/mesh built in it.
@test "the TUI launcher explains itself when the binary is not built" {
    mkdir -p "$TEST_TMPDIR/unbuilt/scripts"
    cp "$SCRIPTS_DIR/mesh-tui.sh" "$TEST_TMPDIR/unbuilt/scripts/"
    run env PATH="/usr/bin:/bin" \
        bash -c "'$TEST_TMPDIR/unbuilt/scripts/mesh-tui.sh' < /dev/null"
    assert_fail
    assert_contains "$output" "go build"
}

@test "the menu offers the TUI" {
    record_tmux
    "$MESH_BIN" menu >/dev/null 2>&1 || true
    run grep display-menu "$TEST_TMPDIR/tmux.log"
    assert_contains "$output" "mesh-tui.sh"
}

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
    assert_contains "$output" "ok    cleanup registered on every teardown hook"
    assert_contains "$output" "ok    no stale cleanup on pane-died"
    assert_contains "$output" "ok    menu key bound"
    assert_contains "$output" "ok    symlink points at this checkout"
}

@test "doctor reports the tmux state as missing before the plugin loads" {
    run doctor_in_fake_home
    assert_contains "$output" "FAIL  cleanup registered on every teardown hook"
    assert_contains "$output" "FAIL  menu key bound"
}

# A doctor that only checks the new hook would report a clean bill of health on a
# server still carrying the dead one, which is the state every existing install
# is in until the plugin is reloaded.
@test "doctor fails when cleanup is still on pane-died" {
    run_plugin
    tmux set-hook -ga pane-died "run-shell -b '$SCRIPTS_DIR/mesh.sh cleanup'"
    run doctor_in_fake_home
    assert_fail
    assert_contains "$output" "FAIL  no stale cleanup on pane-died"
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
    # By alias, and stored as the session id it resolves to. --from used to be
    # kept verbatim, so this recorded a requester that was never an agent.
    mesh_in_fake_home register --session R --harness claude --alias requester >/dev/null
    mesh_in_fake_home dispatch --task "audit it" --harness claude --from requester >/dev/null
    assert_eq "$(msql 'SELECT reply_to_session FROM dispatches;')" "R"
}

@test "dispatch refuses a requester that is not registered" {
    fake_harness claude
    run mesh_in_fake_home dispatch --task "audit it" --harness claude --from nobody
    assert_status 3
    assert_eq "$(msql 'SELECT COUNT(*) FROM dispatches;')" "0"
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
