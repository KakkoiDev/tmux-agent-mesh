#!/usr/bin/env bash
# Test helpers: fresh DB and mocked externals for each test

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPTS_DIR="$PROJECT_ROOT/scripts"
MESH_BIN="$SCRIPTS_DIR/mesh.sh"

setup_test_env() {
    TEST_TMPDIR=$(mktemp -d)
    # MESH_-prefixed on purpose: a bare DB would also be read by
    # tmux-agent-tracker. See the note at the top of mesh.sh.
    export MESH_DIR="$TEST_TMPDIR"
    export MESH_DB="$MESH_DIR/mesh.db"
    export MESH_NOTIFY_DIR="$MESH_DIR/notify"
    export MESH_DELIVERY_LOG="$MESH_DIR/delivery.log"
    # Short aliases for test assertions only, never read by mesh.sh.
    export DB="$MESH_DB"
    export NOTIFY_DIR="$MESH_NOTIFY_DIR"
    export DELIVERY_LOG="$MESH_DELIVERY_LOG"
    export TMUX_PANE=""

    # Config defaults, normally sourced from the tmux option cache
    export ENABLED="on"
    export DELIVERY="stop-block"
    export PI_DELIVERY="push"
    export MAX_HOPS="4"
    export MAX_THREAD_MSGS="12"
    export MAX_BLOCKS="3"
    export MAX_BROADCAST="8"
    export WAKE="off"
    export ICON_MAIL="@"
    export DEBUG_LOG="0"
    export HOOK_ON_MAIL=""

    # Build the schema through the real init path so tests never drift
    # from production DDL.
    "$MESH_BIN" init >/dev/null
}

teardown_test_env() {
    [[ -d "${TEST_TMPDIR:-}" ]] && rm -rf "$TEST_TMPDIR"
    return 0
}

# Direct SQL helper (named msql to avoid clashing with mesh.sh's own sql())
msql() { printf '.timeout 100\n%s\n' "$*" | sqlite3 "$DB"; }

# Source mesh functions without executing the main dispatcher
source_mesh_functions() {
    load_config() { true; }
    _load_config_fast() { true; }
    get_tmux_option() { echo "${2:-}"; }
    tmux() { return 1; }

    _file_mtime() {
        case "$(uname)" in
            Darwin) stat -f %m "$1" ;;
            *)      stat -c %Y "$1" ;;
        esac
    }

    eval "$(awk '
        /^#!\/usr\/bin\/env bash/ { next }
        /^set -euo pipefail/ { next }
        /^source / { next }
        /^case "\$\{1:-\}"/, /^esac$/ { next }
        { print }
    ' "$SCRIPTS_DIR/mesh.sh")"

    # mesh.sh derives SCRIPTS_DIR from BASH_SOURCE, which under eval points at
    # the .bats file. Left clobbered it breaks a second source_mesh_functions
    # call and anything reading $SCRIPTS_DIR/mesh.sh (cmd_menu).
    SCRIPTS_DIR="$PROJECT_ROOT/scripts"
}

# ── fixtures ─────────────────────────────────────────────────────────

insert_agent() {
    local sid="$1" harness="${2:-claude}" alias="${3:-}" pane="${4:-}" target="${5:-}"
    local push=0
    [[ "$harness" == "pi" ]] && push=1
    msql "INSERT INTO agents (session_id, harness, alias, tmux_pane, tmux_target, cwd, project_name, push_capable)
          VALUES ('$sid', '$harness', $( [[ -n "$alias" ]] && echo "'$alias'" || echo NULL ),
                  '$pane', '$target', '/tmp/test', 'test', $push);"
}

insert_message() {
    local from="$1" to="$2" body="$3" thread="${4:-t1}" hops="${5:-0}"
    msql "INSERT INTO messages (thread_id, from_session, to_session, body, hops)
          VALUES ('$thread', '$from', '$to', '$body', $hops);"
}

count_agents()   { msql "SELECT COUNT(*) FROM agents;"; }
count_messages() { msql "SELECT COUNT(*) FROM messages;"; }
get_alias()      { msql "SELECT COALESCE(alias,'') FROM agents WHERE session_id='$1';"; }
get_harness()    { msql "SELECT harness FROM agents WHERE session_id='$1';"; }
get_push()       { msql "SELECT push_capable FROM agents WHERE session_id='$1';"; }
agent_exists()   { [[ "$(msql "SELECT COUNT(*) FROM agents WHERE session_id='$1';")" == "1" ]]; }
