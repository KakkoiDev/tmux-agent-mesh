#!/usr/bin/env bats

bats_require_minimum_version 1.5.0

# The installer edits four live agent configs. These tests pin the two
# properties that matter: it never destroys a symlinked config, and re-running
# it changes nothing.

load helpers

setup() {
    setup_test_env
    FAKE_HOME="$TEST_TMPDIR/home"
    REAL_DOTFILES="$TEST_TMPDIR/dotfiles"
    mkdir -p "$FAKE_HOME/.claude" "$FAKE_HOME/.codex" "$FAKE_HOME/.gemini" "$REAL_DOTFILES"
}

teardown() {
    teardown_test_env
}

install_into_fake_home() {
    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" MESH_DB="$MESH_DB" \
        MESH_NOTIFY_DIR="$NOTIFY_DIR" \
        bash "$PROJECT_ROOT/install.sh" "$@"
}

hook_cmds() {
    jq -r --arg e "$2" '[.hooks[$e][]?.hooks[]?.command] | join(",")' "$1"
}

# ── symlinked configs ────────────────────────────────────────────────

# Regression for the scar this project inherited: `jq ... > tmp && mv tmp file`
# replaces a symlinked settings.json with a regular file, silently detaching it
# from the dotfiles repo that manages it. `cat tmp > file` writes through.
@test "wiring claude keeps a symlinked settings.json a symlink" {
    printf '{}\n' > "$REAL_DOTFILES/settings.json"
    ln -s "$REAL_DOTFILES/settings.json" "$FAKE_HOME/.claude/settings.json"

    install_into_fake_home --claude >/dev/null

    [[ -L "$FAKE_HOME/.claude/settings.json" ]]
    [[ "$(readlink "$FAKE_HOME/.claude/settings.json")" == "$REAL_DOTFILES/settings.json" ]]
}

@test "wiring claude writes through to the symlink target" {
    printf '{}\n' > "$REAL_DOTFILES/settings.json"
    ln -s "$REAL_DOTFILES/settings.json" "$FAKE_HOME/.claude/settings.json"

    install_into_fake_home --claude >/dev/null

    run hook_cmds "$REAL_DOTFILES/settings.json" Stop
    [[ "$output" == *"tmux-agent-mesh hook Stop --harness claude"* ]]
}

@test "uninstall keeps a symlinked settings.json a symlink" {
    printf '{}\n' > "$REAL_DOTFILES/settings.json"
    ln -s "$REAL_DOTFILES/settings.json" "$FAKE_HOME/.claude/settings.json"
    install_into_fake_home --claude >/dev/null

    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" bash "$PROJECT_ROOT/uninstall.sh" >/dev/null

    [[ -L "$FAKE_HOME/.claude/settings.json" ]]
    run hook_cmds "$REAL_DOTFILES/settings.json" Stop
    [[ "$output" != *"tmux-agent-mesh"* ]]
}

# ── coexistence with the sibling plugins ─────────────────────────────

@test "wiring preserves an existing tracker hook on the same event" {
    jq -n '{hooks:{Stop:[{matcher:"",hooks:[{type:"command",command:"tmux-agent-tracker hook Stop"}]}]}}' \
        > "$FAKE_HOME/.claude/settings.json"

    install_into_fake_home --claude >/dev/null

    run hook_cmds "$FAKE_HOME/.claude/settings.json" Stop
    [[ "$output" == *"tmux-agent-tracker hook Stop"* ]]
    [[ "$output" == *"tmux-agent-mesh hook Stop --harness claude"* ]]
}

@test "uninstall removes only the mesh hook" {
    jq -n '{hooks:{Stop:[{matcher:"",hooks:[{type:"command",command:"tmux-agent-tracker hook Stop"}]}]}}' \
        > "$FAKE_HOME/.claude/settings.json"
    install_into_fake_home --claude >/dev/null

    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" bash "$PROJECT_ROOT/uninstall.sh" >/dev/null

    run hook_cmds "$FAKE_HOME/.claude/settings.json" Stop
    [[ "$output" == *"tmux-agent-tracker hook Stop"* ]]
    [[ "$output" != *"tmux-agent-mesh"* ]]
}

@test "uninstall preserves unrelated settings keys" {
    jq -n '{model:"opus", permissions:{allow:["Bash"]}, hooks:{}}' \
        > "$FAKE_HOME/.claude/settings.json"
    install_into_fake_home --claude >/dev/null
    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" bash "$PROJECT_ROOT/uninstall.sh" >/dev/null

    run jq -r '.model' "$FAKE_HOME/.claude/settings.json"
    [[ "$output" == "opus" ]]
}

# ── idempotency ──────────────────────────────────────────────────────

@test "running the installer twice adds one hook per event" {
    install_into_fake_home --claude >/dev/null
    install_into_fake_home --claude >/dev/null

    local n
    n=$(jq '[.hooks.Stop[]?.hooks[]? | select(.command | test("tmux-agent-mesh"))] | length' \
        "$FAKE_HOME/.claude/settings.json")
    [[ "$n" -eq 1 ]]
}

@test "the second run reports the hooks as already wired" {
    install_into_fake_home --claude >/dev/null
    run install_into_fake_home --claude
    [[ "$output" == *"already wired"* ]]
}

@test "installer creates a backup once, not on every run" {
    printf '{}\n' > "$FAKE_HOME/.claude/settings.json"
    install_into_fake_home --claude >/dev/null
    printf '{"changed":true}\n' > "$FAKE_HOME/.claude/settings.json.mesh-backup"
    install_into_fake_home --claude >/dev/null
    run jq -r '.changed' "$FAKE_HOME/.claude/settings.json.mesh-backup"
    [[ "$output" == "true" ]]
}

# ── per-harness wiring ───────────────────────────────────────────────

@test "codex wiring lands in hooks.json with the codex harness flag" {
    install_into_fake_home --codex >/dev/null
    run hook_cmds "$FAKE_HOME/.codex/hooks.json" Stop
    [[ "$output" == *"--harness codex"* ]]
}

@test "codex wiring does not touch config.toml" {
    printf 'notify = ["tmux-agent-tracker", "codex-notify"]\n' > "$FAKE_HOME/.codex/config.toml"
    install_into_fake_home --codex >/dev/null
    run cat "$FAKE_HOME/.codex/config.toml"
    [[ "$output" == 'notify = ["tmux-agent-tracker", "codex-notify"]' ]]
}

# Gemini names the turn boundaries differently; wiring Stop there would be inert.
@test "gemini wiring uses BeforeAgent and AfterAgent" {
    install_into_fake_home --gemini >/dev/null
    run hook_cmds "$FAKE_HOME/.gemini/settings.json" AfterAgent
    [[ "$output" == *"--harness gemini"* ]]
    run hook_cmds "$FAKE_HOME/.gemini/settings.json" BeforeAgent
    [[ "$output" == *"--harness gemini"* ]]
    run hook_cmds "$FAKE_HOME/.gemini/settings.json" Stop
    [[ -z "$output" ]]
}

@test "claude wiring uses Stop and UserPromptSubmit" {
    install_into_fake_home --claude >/dev/null
    run hook_cmds "$FAKE_HOME/.claude/settings.json" UserPromptSubmit
    [[ "$output" == *"--harness claude"* ]]
    run hook_cmds "$FAKE_HOME/.claude/settings.json" AfterAgent
    [[ -z "$output" ]]
}

# ── base install ─────────────────────────────────────────────────────

@test "base install wires no harness" {
    run install_into_fake_home
    [[ "$output" == *"No harness wired"* ]]
    [[ ! -f "$FAKE_HOME/.codex/hooks.json" ]]
}

@test "base install links the cli and syncs the skill" {
    install_into_fake_home >/dev/null
    [[ -L "$FAKE_HOME/.local/bin/tmux-agent-mesh" ]]
    [[ -f "$FAKE_HOME/.claude/skills/tmux-agent-mesh/SKILL.md" ]]
}

@test "installer rejects unknown flags" {
    run install_into_fake_home --wat
    [[ "$status" -ne 0 ]]
}

# ── data safety ──────────────────────────────────────────────────────

# Undelivered mail is data, not an install artifact.
@test "uninstall keeps the database by default" {
    install_into_fake_home --claude >/dev/null
    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" bash "$PROJECT_ROOT/uninstall.sh" >/dev/null
    [[ -f "$MESH_DB" ]]
}

@test "uninstall --purge removes the data directory" {
    install_into_fake_home --claude >/dev/null
    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" bash "$PROJECT_ROOT/uninstall.sh" --purge >/dev/null
    [[ ! -d "$MESH_DIR" ]]
}
