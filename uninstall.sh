#!/usr/bin/env bash
set -euo pipefail

# Reverses install.sh. Leaves the database alone unless --purge is given:
# undelivered mail is data, not an install artifact.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_LINK="$HOME/.local/bin/tmux-agent-mesh"
TUI_LINK="$HOME/.local/bin/mesh"
MESH_DIR="${MESH_DIR:-$HOME/.tmux-agent-mesh}"
PI_EXT_DIR="$HOME/.pi/agent/extensions/tmux-agent-mesh"

PURGE=0
[[ "${1:-}" == "--purge" ]] && PURGE=1
[[ "${1:-}" == "-h" || "${1:-}" == "--help" ]] && {
    printf 'usage: ./uninstall.sh [--purge]\n  --purge also deletes %s\n' "$MESH_DIR"; exit 0; }

say() { printf '%s\n' "$*"; }

# Same reason as install.sh: cat > writes through a symlink, mv replaces it.
write_through() { cat "$2" > "$1"; rm -f "$2"; }

remove_hooks() {
    local file="$1"
    [[ -f "$file" ]] || return 0
    command -v jq >/dev/null 2>&1 || { say "  jq missing, skipping $file"; return 0; }
    local tmp="${file}.mesh-tmp.$$"
    # Both transforms have to sit inside `.value |= (...)`. Chaining the second
    # one outside applies it to the {key,value} entry object, and map over an
    # object iterates its values, so jq tries to index the key string and dies
    # with 'Cannot index string with string "hooks"'.
    jq '
        if .hooks then
          .hooks |= with_entries(
            .value |= (
                map(.hooks |= map(select((.command // "") | test("tmux-agent-mesh") | not)))
              | map(select((.hooks | length) > 0))
            )
          )
          | .hooks |= with_entries(select((.value | length) > 0))
        else . end
    ' "$file" > "$tmp"
    write_through "$file" "$tmp"
    say "  cleaned $file"
}

say "tmux-agent-mesh uninstall"
say ""

if [[ -L "$BIN_LINK" ]]; then rm -f "$BIN_LINK"; say "cli: removed $BIN_LINK"; fi

# Only when it points here. `mesh` is a common enough name that someone else's
# binary could be under it, and removing that is not this script's business.
if [[ -L "$TUI_LINK" && "$(readlink "$TUI_LINK")" == "$HERE/bin/mesh" ]]; then
    rm -f "$TUI_LINK"
    say "tui: removed $TUI_LINK"
fi
if [[ -L "$PI_EXT_DIR" || -d "$PI_EXT_DIR" ]]; then rm -rf "$PI_EXT_DIR"; say "pi: removed $PI_EXT_DIR"; fi

say "hooks:"
remove_hooks "$HOME/.claude/settings.json"
remove_hooks "$HOME/.codex/hooks.json"
remove_hooks "$HOME/.gemini/settings.json"

skill="$HOME/.claude/skills/tmux-agent-mesh"
if [[ -d "$skill" ]]; then rm -rf "$skill"; say "skill: removed $skill"; fi

if command -v tmux >/dev/null 2>&1 && tmux info >/dev/null 2>&1; then
    cur=$(tmux show-option -gqv status-right 2>/dev/null || true)
    case "$cur" in
        *"@agent-mesh-status"*)
            new="${cur//#\{@agent-mesh-status\} /}"
            new="${new//#\{@agent-mesh-status\}/}"
            tmux set -g status-right "$new"
            say "status-bar: removed the mesh segment" ;;
    esac
    tmux set -gu @agent-mesh-status 2>/dev/null || true
fi

conf="$HOME/.tmux.conf"
if [[ -f "$conf" ]] && grep -qF "agent-mesh.tmux" "$conf"; then
    say ""
    say "tmux.conf still references agent-mesh.tmux; remove this line yourself:"
    grep -nF "agent-mesh.tmux" "$conf" | sed 's/^/  /'
fi

say ""
if [[ "$PURGE" -eq 1 ]]; then
    rm -rf "$MESH_DIR"
    say "data: removed $MESH_DIR"
else
    say "data kept at $MESH_DIR (re-run with --purge to delete it)"
fi
say "Config backups (*.mesh-backup) were left in place."
