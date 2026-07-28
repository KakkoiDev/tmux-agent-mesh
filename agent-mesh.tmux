#!/usr/bin/env bash
# TPM entry point for tmux-agent-mesh

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export AGENT_MESH_PLUGIN_DIR="$CURRENT_DIR"
SCRIPTS_DIR="$CURRENT_DIR/scripts"

source "$SCRIPTS_DIR/helpers.sh"

ensure_tmux_version || exit 1
load_config

# Create the DB if absent. Non-destructive, safe on every tmux reload.
"$SCRIPTS_DIR/mesh.sh" init >/dev/null 2>&1

# ── Idempotent CLI + skill setup (TPM auto-provisioning) ─────────────

_link_if_stale() {
    local target="$1" link="$2"
    [[ "$(readlink "$link" 2>/dev/null)" == "$target" ]] && return
    mkdir -p "$(dirname "$link")"
    ln -sf "$target" "$link"
}

_link_if_stale "$CURRENT_DIR/bin/tmux-agent-mesh" "$HOME/.local/bin/tmux-agent-mesh"

_sync_skill_bundle() {
    local src_dir="$1" skills_root="$2" skill_name dest_dir
    [[ -d "$src_dir" ]] || return
    skill_name="$(basename "$src_dir")"
    dest_dir="$skills_root/$skill_name"
    mkdir -p "$dest_dir"
    cp -Rf "$src_dir/." "$dest_dir/"
}

for src_dir in "$CURRENT_DIR"/.claude/skills/tmux-agent-mesh*; do
    [[ -d "$src_dir" ]] || continue
    # A skill with no SKILL.md is a scaffold, not a skill. Do not publish it.
    [[ -f "$src_dir/SKILL.md" ]] || continue
    _sync_skill_bundle "$src_dir" "$HOME/.claude/skills"
done

# Pi loads extensions by auto-discovery from ~/.pi/agent/extensions.
if [[ -f "$CURRENT_DIR/pi-extension/index.ts" ]]; then
    _link_if_stale "$CURRENT_DIR/pi-extension" "$HOME/.pi/agent/extensions/tmux-agent-mesh"
fi

# Initialize the status option so #{@agent-mesh-status} is never unset, then
# populate it from the current mailbox.
# Placement in status-right is left to the user: tmux-agent-tracker already
# rewrites that string, and two plugins editing it is a clobber. Opt in with
# ./install.sh --status-bar.
tmux set -gq @agent-mesh-status ""
"$SCRIPTS_DIR/mesh.sh" refresh >/dev/null 2>&1 || true

# prefix + m: roster with pending counts, jump to a pane.
tmux bind-key "${KEYBINDING:-m}" run-shell "$SCRIPTS_DIR/mesh.sh menu"

# Reap agents whose pane died. session_shutdown does not fire when a pane is
# killed, so for every harness this is the only cleanup path.
#
# -ga, not -g: a plain set replaces whatever else is on pane-died, and this
# plugin has three siblings plus whatever the user wrote. The existence check is
# what keeps -ga idempotent, since tmux reloads this file on every source-file
# and an unconditional append accumulates one duplicate per reload.
if ! tmux show-hooks -g pane-died 2>/dev/null | grep -qF "$SCRIPTS_DIR/mesh.sh cleanup"; then
    tmux set-hook -ga pane-died "run-shell -b '$SCRIPTS_DIR/mesh.sh cleanup'"
fi
