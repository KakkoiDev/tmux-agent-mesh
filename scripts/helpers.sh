#!/usr/bin/env bash
# helpers.sh - Config loading and tmux helpers for tmux-agent-mesh

# ── Plugin directory resolution ──────────────────────────────────────

if [[ -z "${AGENT_MESH_PLUGIN_DIR:-}" ]]; then
    AGENT_MESH_PLUGIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fi
SCRIPTS_DIR="$AGENT_MESH_PLUGIN_DIR/scripts"

# ── platform helpers ──────────────────────────────────────────────────

_file_mtime() {
    case "$(uname)" in
        Darwin) stat -f %m "$1" ;;
        *)      stat -c %Y "$1" ;;
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
WAKE=""
ICON_MAIL=""
DEBUG_LOG=""
HOOK_ON_MAIL=""

load_config() {
    local cache="${MESH_DIR:-$HOME/.tmux-agent-mesh}/config_cache"

    # Use cache if fresh (< 60s), shared across all hook invocations
    if [[ -f "$cache" ]]; then
        local age now
        now=$(date +%s)
        age=$(( now - $(_file_mtime "$cache" 2>/dev/null || echo 0) ))
        if [[ "$age" -lt 60 ]]; then
            # shellcheck disable=SC1090
            source "$cache"
            return
        fi
    fi

    KEYBINDING=$(get_tmux_option "@agent-mesh-keybinding" "m")
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
    WAKE=$(get_tmux_option "@agent-mesh-wake" "off")
    ICON_MAIL=$(get_tmux_option "@agent-mesh-icon-mail" "@")
    DEBUG_LOG=$(get_tmux_option "@agent-mesh-debug-log" "0")
    HOOK_ON_MAIL=$(get_tmux_option "@agent-mesh-on-mail" "")

    mkdir -p "$(dirname "$cache")"

    # Atomic write, safe for concurrent hook invocations
    cat > "${cache}.tmp" <<EOF
KEYBINDING='$KEYBINDING'
ITEMS_PER_PAGE='$ITEMS_PER_PAGE'
KEY_NEXT='$KEY_NEXT'
KEY_PREV='$KEY_PREV'
KEY_QUIT='$KEY_QUIT'
ENABLED='$ENABLED'
DELIVERY='$DELIVERY'
PI_DELIVERY='$PI_DELIVERY'
MAX_HOPS='$MAX_HOPS'
MAX_THREAD_MSGS='$MAX_THREAD_MSGS'
MAX_BLOCKS='$MAX_BLOCKS'
MAX_BROADCAST='$MAX_BROADCAST'
WAKE='$WAKE'
ICON_MAIL='$ICON_MAIL'
DEBUG_LOG='$DEBUG_LOG'
HOOK_ON_MAIL='$HOOK_ON_MAIL'
EOF
    mv -f "${cache}.tmp" "$cache"
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
