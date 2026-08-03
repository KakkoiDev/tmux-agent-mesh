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

# prefix + g: roster with pending counts, jump to a pane.
tmux bind-key "${KEYBINDING:-g}" run-shell "$SCRIPTS_DIR/mesh.sh menu"

# prefix + G: the TUI, in its own window. Not a popup: display-popup needs tmux
# 3.2 and the published floor for this plugin is 3.0.
tmux bind-key "${TUI_KEYBINDING:-G}" new-window -n mesh "$SCRIPTS_DIR/mesh-tui.sh"

# Reap agents whose pane is gone. session_shutdown does not fire when a pane is
# killed, so for every harness this is the only cleanup path.
#
# Four hooks, because no single one covers pane teardown. Measured on 3.5a with
# remain-on-exit off, which is the default:
#
#   the pane's process exits      pane-exited, window-layout-changed
#   kill-pane, multi-pane window  after-kill-pane, window-layout-changed
#   kill-pane, last pane          after-kill-pane, window-unlinked
#   kill-window                   window-unlinked
#   kill-session                  session-closed, window-unlinked
#   pane-died                     never fires at all
#
# So `pane-exited` alone still misses every kill-*, and `pane-died` (what
# releases up to 0.1.0 used) fires only while remain-on-exit is on: the plugin
# shipped with no working cleanup path whatsoever. window-layout-changed is
# deliberately not in the set - it also fires on every split, resize and layout
# switch, which is a much hotter path than anything it would add coverage for.
#
# All four are debounced into one prune by cmd_cleanup, so overlapping hooks on
# the same event cost one stat, not one prune.
MESH_CLEANUP_HOOKS="pane-exited after-kill-pane window-unlinked session-closed"

# Drop the dead pane-died binding. Left behind it becomes a second reap that does
# fire, for anybody who turns remain-on-exit on.
#
# Unset by index, since only one entry in the array is ours. tmux does not
# renumber the survivors, so the indices read here stay valid across removals.
tmux show-hooks -g pane-died 2>/dev/null \
    | grep -F "$SCRIPTS_DIR/mesh.sh cleanup" \
    | sed -n 's/^pane-died\[\([0-9]*\)\].*/\1/p' \
    | while read -r _i; do
        tmux set-hook -gu "pane-died[$_i]" 2>/dev/null || true
      done

# -ga, not -g: a plain set replaces whatever else is on the hook, and this plugin
# has three siblings plus whatever the user wrote. The existence check is what
# keeps -ga idempotent, since tmux reloads this file on every source-file and an
# unconditional append accumulates one duplicate per reload.
#
# Validated per name: the published floor is tmux 3.0 and an unknown hook name
# makes set-hook fail, which under `set -e` would abort the rest of this file.
# `show-hooks -g <name>` is the only per-name check that works, since a bare
# `show-hooks -g` omits names that are unset.
for _hook in $MESH_CLEANUP_HOOKS; do
    tmux show-hooks -g "$_hook" >/dev/null 2>&1 || continue
    if ! tmux show-hooks -g "$_hook" 2>/dev/null | grep -qF "$SCRIPTS_DIR/mesh-wrapper.sh cleanup"; then
        tmux set-hook -ga "$_hook" "run-shell -b '$SCRIPTS_DIR/mesh-wrapper.sh cleanup'"
    fi
done
