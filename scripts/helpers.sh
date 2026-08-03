#!/usr/bin/env bash
# helpers.sh - a shim over the vendored tmux-toolkit.
#
# Every function here used to be a local implementation. Three of them
# (get_tmux_option, _file_mtime, check_tmux_version) were byte-identical to the
# copies in tmux-agent-tracker and tmux-agent-resumer, and load_config was the
# same architecture written a fourth time. They now delegate to lib/, so a fix
# lands once instead of four times.
#
# The old names are kept deliberately: mesh.sh and agent-mesh.tmux call them at
# ~90 sites, and renaming those is a separate change from extracting them. Every
# signature and return value is unchanged.

# ── Plugin directory resolution ──────────────────────────────────────

if [[ -z "${AGENT_MESH_PLUGIN_DIR:-}" ]]; then
    AGENT_MESH_PLUGIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fi
# Read by mesh.sh and agent-mesh.tmux, which source this file.
# shellcheck disable=SC2034
SCRIPTS_DIR="$AGENT_MESH_PLUGIN_DIR/scripts"

# shellcheck source=../lib/toolkit.sh
source "$AGENT_MESH_PLUGIN_DIR/lib/toolkit.sh"
tk_require_version 0.1.0

# tk_init is deferred to load_config: mesh.sh sources this file before it
# resolves MESH_DIR, so the data dir is not knowable yet at source time.
_mesh_tk_init() { tk_init agent-mesh "${MESH_DIR:-$HOME/.tmux-agent-mesh}"; }

# ── platform helpers ──────────────────────────────────────────────────

_file_mtime() { tk_mtime "$1"; }

# `date -r <epoch>` is BSD-only, so watch printed blank timestamps on Linux.
_fmt_time() { tk_fmt_time "$1"; }

# ── tmux option helpers ──────────────────────────────────────────────

get_tmux_option() { tk_opt "$1" "${2:-}"; }

# ── config loading ───────────────────────────────────────────────────

# Declared so mesh.sh and agent-mesh.tmux can read them under `set -u` before
# load_config has run. One `declare` rather than fifteen assignments, so that a
# single SC2034 directive covers all of them: the writes are invisible to the
# linter because tk_config_load assigns through tk_opt_into, which has to be an
# eval since bash 3.2 has no namerefs. A file-scope disable would hide real
# findings in the rest of the file.
#
# (A comment line must not begin with the linter's own name, or it is parsed as
# a directive and fails with SC1072.)
# shellcheck disable=SC2034
declare KEYBINDING="" TUI_KEYBINDING="" KEY_QUIT="" \
        ENABLED="" DELIVERY="" PI_DELIVERY="" MAX_HOPS="" \
        MAX_BLOCKS="" MAX_BROADCAST="" ICON_MAIL="" DEBUG_LOG="" HOOK_ON_MAIL=""

# Quote a value for the cache file, which is sourced. @agent-mesh-on-mail is a
# shell snippet, so single quotes are expected there; unescaped they produced a
# syntax-error cache and the bare `source` under `set -euo pipefail` then killed
# every hook, menu, watch and refresh at once.
_cq() { tk_cq "$1"; }

# Not `m`: that is tmux's own select-pane -m, so the old default silently took a
# built-in away from everyone who installed this.
# The TUI is on G rather than g because g is the menu, which has been the
# documented key since the first release. Both are configurable.
_MESH_CONFIG_SPECS=(
    'KEYBINDING:@agent-mesh-keybinding:g'
    'TUI_KEYBINDING:@agent-mesh-tui-keybinding:G'
    'KEY_QUIT:@agent-mesh-key-quit:q'
    'ENABLED:@agent-mesh-enabled:on'
    'DELIVERY:@agent-mesh-delivery:stop-block'
    'PI_DELIVERY:@agent-mesh-pi-delivery:push'
    'MAX_HOPS:@agent-mesh-max-hops:4'
    'MAX_BLOCKS:@agent-mesh-max-blocks:3'
    'MAX_BROADCAST:@agent-mesh-max-broadcast:8'
    'ICON_MAIL:@agent-mesh-icon-mail:@'
    'DEBUG_LOG:@agent-mesh-debug-log:0'
    'HOOK_ON_MAIL:@agent-mesh-on-mail:'
)

load_config() {
    _mesh_tk_init
    tk_config_load agent-mesh 60 "${_MESH_CONFIG_SPECS[@]}"
}

# ── version check ────────────────────────────────────────────────────

check_tmux_version() { tk_vers_ge "${1:-3.0}"; }

ensure_tmux_version() { tk_vers_require 3.0 tmux-agent-mesh; }
