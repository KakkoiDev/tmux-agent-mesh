#!/usr/bin/env bash
# helpers.sh - Config loading and tmux helpers for tmux-agent-mesh

# ── Plugin directory resolution ──────────────────────────────────────

if [[ -z "${AGENT_MESH_PLUGIN_DIR:-}" ]]; then
    AGENT_MESH_PLUGIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fi
# Read by mesh.sh and agent-mesh.tmux, which source this file.
# shellcheck disable=SC2034
SCRIPTS_DIR="$AGENT_MESH_PLUGIN_DIR/scripts"

# ── platform helpers ──────────────────────────────────────────────────

_file_mtime() {
    case "$(uname)" in
        Darwin) stat -f %m "$1" ;;
        *)      stat -c %Y "$1" ;;
    esac
}

# `date -r <epoch>` is BSD-only, so watch printed blank timestamps on Linux.
_fmt_time() {
    case "$(uname)" in
        Darwin) date -r "$1" '+%H:%M:%S' ;;
        *)      date -d "@$1" '+%H:%M:%S' ;;
    esac
}

# ── tmux option helpers ──────────────────────────────────────────────

get_tmux_option() {
    local option="$1" default="${2:-}"
    local value
    value=$(tmux show-option -gqv "$option" 2>/dev/null) || true
    printf '%s' "${value:-$default}"
}

# ── config loading ───────────────────────────────────────────────────

KEYBINDING=""
ITEMS_PER_PAGE=""
KEY_NEXT=""
KEY_PREV=""
KEY_QUIT=""
ENABLED=""
DELIVERY=""
PI_DELIVERY=""
MAX_HOPS=""
MAX_THREAD_MSGS=""
MAX_BLOCKS=""
MAX_BROADCAST=""
ICON_MAIL=""
DEBUG_LOG=""
HOOK_ON_MAIL=""

# Quote a value for the cache file, which is sourced. @agent-mesh-on-mail is a
# shell snippet, so single quotes are expected there; unescaped they produced a
# syntax-error cache and the bare `source` under `set -euo pipefail` then killed
# every hook, menu, watch and refresh at once.
_cq() {
    local q="'" esc
    esc="${1//$q/$q\\$q$q}"
    printf "'%s'" "$esc"
}

load_config() {
    local dir="${MESH_DIR:-$HOME/.tmux-agent-mesh}"
    local cache="$dir/config_cache"

    # Use cache if fresh (< 60s), shared across all hook invocations
    if [[ -f "$cache" ]]; then
        local age now
        now=$(date +%s)
        age=$(( now - $(_file_mtime "$cache" 2>/dev/null || echo 0) ))
        if [[ "$age" -lt 60 ]]; then
            # A cache that will not parse is worse than no cache: `source` under
            # `set -euo pipefail` aborts the caller, which would take down every
            # hook and every command including the one meant to diagnose it. So
            # check first and rebuild rather than report.
            if bash -n "$cache" 2>/dev/null; then
                # shellcheck disable=SC1090
                source "$cache"
                return
            fi
            rm -f "$cache" 2>/dev/null || true
        fi
    fi

    # Not `m`: that is tmux's own select-pane -m, so the old default silently
    # took a built-in away from everyone who installed this.
    KEYBINDING=$(get_tmux_option "@agent-mesh-keybinding" "g")
    ITEMS_PER_PAGE=$(get_tmux_option "@agent-mesh-items-per-page" "10")
    KEY_NEXT=$(get_tmux_option "@agent-mesh-key-next" "i")
    KEY_PREV=$(get_tmux_option "@agent-mesh-key-prev" "o")
    KEY_QUIT=$(get_tmux_option "@agent-mesh-key-quit" "q")
    ENABLED=$(get_tmux_option "@agent-mesh-enabled" "on")
    DELIVERY=$(get_tmux_option "@agent-mesh-delivery" "stop-block")
    PI_DELIVERY=$(get_tmux_option "@agent-mesh-pi-delivery" "push")
    MAX_HOPS=$(get_tmux_option "@agent-mesh-max-hops" "4")
    MAX_THREAD_MSGS=$(get_tmux_option "@agent-mesh-max-thread-msgs" "12")
    MAX_BLOCKS=$(get_tmux_option "@agent-mesh-max-blocks" "3")
    MAX_BROADCAST=$(get_tmux_option "@agent-mesh-max-broadcast" "8")
    ICON_MAIL=$(get_tmux_option "@agent-mesh-icon-mail" "@")
    DEBUG_LOG=$(get_tmux_option "@agent-mesh-debug-log" "0")
    HOOK_ON_MAIL=$(get_tmux_option "@agent-mesh-on-mail" "")

    # No data dir means mesh is not installed here. Read the options, but do not
    # create anything: a harness hook on an uninstalled machine must be inert.
    [[ -d "$dir" ]] || return 0

    # Atomic write, safe for concurrent hook invocations. Cosmetic, so an
    # unwritable dir must not fail a hook.
    {
        {
            printf 'KEYBINDING=%s\n'      "$(_cq "$KEYBINDING")"
            printf 'ITEMS_PER_PAGE=%s\n'  "$(_cq "$ITEMS_PER_PAGE")"
            printf 'KEY_NEXT=%s\n'        "$(_cq "$KEY_NEXT")"
            printf 'KEY_PREV=%s\n'        "$(_cq "$KEY_PREV")"
            printf 'KEY_QUIT=%s\n'        "$(_cq "$KEY_QUIT")"
            printf 'ENABLED=%s\n'         "$(_cq "$ENABLED")"
            printf 'DELIVERY=%s\n'        "$(_cq "$DELIVERY")"
            printf 'PI_DELIVERY=%s\n'     "$(_cq "$PI_DELIVERY")"
            printf 'MAX_HOPS=%s\n'        "$(_cq "$MAX_HOPS")"
            printf 'MAX_THREAD_MSGS=%s\n' "$(_cq "$MAX_THREAD_MSGS")"
            printf 'MAX_BLOCKS=%s\n'      "$(_cq "$MAX_BLOCKS")"
            printf 'MAX_BROADCAST=%s\n'   "$(_cq "$MAX_BROADCAST")"
            printf 'ICON_MAIL=%s\n'       "$(_cq "$ICON_MAIL")"
            printf 'DEBUG_LOG=%s\n'       "$(_cq "$DEBUG_LOG")"
            printf 'HOOK_ON_MAIL=%s\n'    "$(_cq "$HOOK_ON_MAIL")"
        } > "${cache}.tmp" && mv -f "${cache}.tmp" "$cache"
    } 2>/dev/null || true
    return 0
}

# ── version check ────────────────────────────────────────────────────

check_tmux_version() {
    local required="${1:-3.0}"
    local current
    current=$(tmux -V 2>/dev/null | sed 's/[^0-9.]//g') || return 1
    [[ -z "$current" ]] && return 1

    local cur_major cur_minor req_major req_minor
    cur_major="${current%%.*}"
    cur_minor="${current#*.}"; cur_minor="${cur_minor%%.*}"
    req_major="${required%%.*}"
    req_minor="${required#*.}"; req_minor="${req_minor%%.*}"

    if [[ "$cur_major" -gt "$req_major" ]]; then return 0; fi
    if [[ "$cur_major" -eq "$req_major" && "$cur_minor" -ge "$req_minor" ]]; then return 0; fi
    return 1
}

ensure_tmux_version() {
    if ! check_tmux_version "3.0"; then
        echo "tmux-agent-mesh requires tmux 3.0+" >&2
        return 1
    fi
}
