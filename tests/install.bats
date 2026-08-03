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
    if command -v go >/dev/null 2>&1; then
        REAL_GOCACHE=$(go env GOCACHE)
        REAL_GOMODCACHE=$(go env GOMODCACHE)
    fi
}

teardown() {
    teardown_test_env
}

# GOCACHE and GOMODCACHE are read from the real HOME before it is redirected.
# The fake HOME exists so the installer cannot touch the developer's harness
# configs; go reading an empty cache under it means a full cgo rebuild of the
# sqlite driver on every single test, which is minutes each.
install_into_fake_home() {
    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" MESH_DB="$MESH_DB" \
        MESH_NOTIFY_DIR="$NOTIFY_DIR" \
        GOCACHE="${REAL_GOCACHE:-}" GOMODCACHE="${REAL_GOMODCACHE:-}" \
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

    assert_symlink "$FAKE_HOME/.claude/settings.json"
    assert_eq "$(readlink "$FAKE_HOME/.claude/settings.json")" "$REAL_DOTFILES/settings.json"
}

@test "wiring claude writes through to the symlink target" {
    printf '{}\n' > "$REAL_DOTFILES/settings.json"
    ln -s "$REAL_DOTFILES/settings.json" "$FAKE_HOME/.claude/settings.json"

    install_into_fake_home --claude >/dev/null

    run hook_cmds "$REAL_DOTFILES/settings.json" Stop
    assert_contains "$output" "tmux-agent-mesh hook Stop --harness claude"
}

@test "uninstall keeps a symlinked settings.json a symlink" {
    printf '{}\n' > "$REAL_DOTFILES/settings.json"
    ln -s "$REAL_DOTFILES/settings.json" "$FAKE_HOME/.claude/settings.json"
    install_into_fake_home --claude >/dev/null

    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" bash "$PROJECT_ROOT/uninstall.sh" >/dev/null

    assert_symlink "$FAKE_HOME/.claude/settings.json"
    run hook_cmds "$REAL_DOTFILES/settings.json" Stop
    refute_contains "$output" "tmux-agent-mesh"
}

# ── coexistence with the sibling plugins ─────────────────────────────

@test "wiring preserves an existing tracker hook on the same event" {
    jq -n '{hooks:{Stop:[{matcher:"",hooks:[{type:"command",command:"tmux-agent-tracker hook Stop"}]}]}}' \
        > "$FAKE_HOME/.claude/settings.json"

    install_into_fake_home --claude >/dev/null

    run hook_cmds "$FAKE_HOME/.claude/settings.json" Stop
    assert_contains "$output" "tmux-agent-tracker hook Stop"
    assert_contains "$output" "tmux-agent-mesh hook Stop --harness claude"
}

@test "uninstall removes only the mesh hook" {
    jq -n '{hooks:{Stop:[{matcher:"",hooks:[{type:"command",command:"tmux-agent-tracker hook Stop"}]}]}}' \
        > "$FAKE_HOME/.claude/settings.json"
    install_into_fake_home --claude >/dev/null

    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" bash "$PROJECT_ROOT/uninstall.sh" >/dev/null

    run hook_cmds "$FAKE_HOME/.claude/settings.json" Stop
    assert_contains "$output" "tmux-agent-tracker hook Stop"
    refute_contains "$output" "tmux-agent-mesh"
}

@test "uninstall preserves unrelated settings keys" {
    jq -n '{model:"opus", permissions:{allow:["Bash"]}, hooks:{}}' \
        > "$FAKE_HOME/.claude/settings.json"
    install_into_fake_home --claude >/dev/null
    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" bash "$PROJECT_ROOT/uninstall.sh" >/dev/null

    run jq -r '.model' "$FAKE_HOME/.claude/settings.json"
    assert_eq "$output" "opus"
}

# ── idempotency ──────────────────────────────────────────────────────

@test "running the installer twice adds one hook per event" {
    install_into_fake_home --claude >/dev/null
    install_into_fake_home --claude >/dev/null

    local n
    n=$(jq '[.hooks.Stop[]?.hooks[]? | select(.command | test("tmux-agent-mesh"))] | length' \
        "$FAKE_HOME/.claude/settings.json")
    assert_num_eq "$n" 1
}

@test "the second run reports the hooks as already wired" {
    install_into_fake_home --claude >/dev/null
    run install_into_fake_home --claude
    assert_contains "$output" "already wired"
}

@test "installer creates a backup once, not on every run" {
    printf '{}\n' > "$FAKE_HOME/.claude/settings.json"
    install_into_fake_home --claude >/dev/null
    printf '{"changed":true}\n' > "$FAKE_HOME/.claude/settings.json.mesh-backup"
    install_into_fake_home --claude >/dev/null
    run jq -r '.changed' "$FAKE_HOME/.claude/settings.json.mesh-backup"
    assert_eq "$output" "true"
}

# ── per-harness wiring ───────────────────────────────────────────────

@test "codex wiring lands in hooks.json with the codex harness flag" {
    install_into_fake_home --codex >/dev/null
    run hook_cmds "$FAKE_HOME/.codex/hooks.json" Stop
    assert_contains "$output" "--harness codex"
}

@test "codex wiring does not touch config.toml" {
    printf 'notify = ["tmux-agent-tracker", "codex-notify"]\n' > "$FAKE_HOME/.codex/config.toml"
    install_into_fake_home --codex >/dev/null
    run cat "$FAKE_HOME/.codex/config.toml"
    assert_eq "$output" 'notify = ["tmux-agent-tracker", "codex-notify"]'
}

# Gemini names the turn boundaries differently; wiring Stop there would be inert.
@test "gemini wiring uses BeforeAgent and AfterAgent" {
    install_into_fake_home --gemini >/dev/null
    run hook_cmds "$FAKE_HOME/.gemini/settings.json" AfterAgent
    assert_contains "$output" "--harness gemini"
    run hook_cmds "$FAKE_HOME/.gemini/settings.json" BeforeAgent
    assert_contains "$output" "--harness gemini"
    run hook_cmds "$FAKE_HOME/.gemini/settings.json" Stop
    assert_empty "$output"
}

@test "claude wiring uses Stop and UserPromptSubmit" {
    install_into_fake_home --claude >/dev/null
    run hook_cmds "$FAKE_HOME/.claude/settings.json" UserPromptSubmit
    assert_contains "$output" "--harness claude"
    run hook_cmds "$FAKE_HOME/.claude/settings.json" AfterAgent
    assert_empty "$output"
}

# ── base install ─────────────────────────────────────────────────────

@test "base install wires no harness" {
    run install_into_fake_home
    assert_contains "$output" "No harness wired"
    refute_file "$FAKE_HOME/.codex/hooks.json"
}

@test "base install links the cli and syncs the skill" {
    install_into_fake_home >/dev/null
    assert_symlink "$FAKE_HOME/.local/bin/tmux-agent-mesh"
    assert_file "$FAKE_HOME/.claude/skills/tmux-agent-mesh/SKILL.md"
}

@test "installer rejects unknown flags" {
    run install_into_fake_home --wat
    assert_fail
}

# ── the TUI binary ───────────────────────────────────────────────────
#
# The TUI is the only thing a human can drive, and nothing built it: it was
# reachable only by someone who knew to run `go build ./cmd/mesh` by hand.

@test "base install builds the TUI and links it" {
    command -v go >/dev/null 2>&1 || skip "go is not installed"
    install_into_fake_home >/dev/null
    assert_symlink "$FAKE_HOME/.local/bin/mesh"
    assert_eq "$(readlink "$FAKE_HOME/.local/bin/mesh")" "$PROJECT_ROOT/bin/mesh"
    run "$PROJECT_ROOT/bin/mesh"
    assert_contains "$output" "tui"
}

# A stale `mesh` from an older checkout is a binary built against a schema this
# one has migrated past, and it is what a human runs when they type `mesh`.
@test "base install replaces a mesh link that points somewhere else" {
    command -v go >/dev/null 2>&1 || skip "go is not installed"
    mkdir -p "$FAKE_HOME/.local/bin" "$TEST_TMPDIR/old"
    printf '#!/bin/sh\nexit 0\n' > "$TEST_TMPDIR/old/mesh"
    chmod +x "$TEST_TMPDIR/old/mesh"
    ln -sf "$TEST_TMPDIR/old/mesh" "$FAKE_HOME/.local/bin/mesh"

    run install_into_fake_home
    assert_contains "$output" "replaced a link to $TEST_TMPDIR/old/mesh"
    assert_eq "$(readlink "$FAKE_HOME/.local/bin/mesh")" "$PROJECT_ROOT/bin/mesh"
}

@test "an install without go still works and says the TUI was skipped" {
    # A PATH with no go at all, which is what a server or a fresh box looks like.
    mkdir -p "$TEST_TMPDIR/nogo"
    for t in bash sh env sqlite3 jq tmux uname sed grep awk mkdir ln rm cp cat printf readlink chmod dirname basename; do
        src=$(command -v "$t" 2>/dev/null) || continue
        ln -sf "$src" "$TEST_TMPDIR/nogo/$t"
    done
    run env HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" MESH_DB="$MESH_DB" \
        MESH_NOTIFY_DIR="$NOTIFY_DIR" PATH="$TEST_TMPDIR/nogo" \
        bash "$PROJECT_ROOT/install.sh"
    assert_ok
    assert_contains "$output" "go is not installed"
    assert_symlink "$FAKE_HOME/.local/bin/tmux-agent-mesh"
}

@test "uninstall removes the mesh link only when it points at this checkout" {
    mkdir -p "$FAKE_HOME/.local/bin" "$TEST_TMPDIR/other"
    printf '#!/bin/sh\nexit 0\n' > "$TEST_TMPDIR/other/mesh"
    ln -sf "$TEST_TMPDIR/other/mesh" "$FAKE_HOME/.local/bin/mesh"

    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" bash "$PROJECT_ROOT/uninstall.sh" >/dev/null
    assert_symlink "$FAKE_HOME/.local/bin/mesh"
}

# ── data safety ──────────────────────────────────────────────────────

# Undelivered mail is data, not an install artifact.
@test "uninstall keeps the database by default" {
    install_into_fake_home --claude >/dev/null
    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" bash "$PROJECT_ROOT/uninstall.sh" >/dev/null
    assert_file "$MESH_DB"
}

@test "uninstall --purge removes the data directory" {
    install_into_fake_home --claude >/dev/null
    HOME="$FAKE_HOME" MESH_DIR="$MESH_DIR" bash "$PROJECT_ROOT/uninstall.sh" --purge >/dev/null
    refute_dir "$MESH_DIR"
}
