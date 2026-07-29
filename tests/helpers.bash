#!/usr/bin/env bash
# Test helpers: fresh DB and mocked externals for each test

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPTS_DIR="$PROJECT_ROOT/scripts"
MESH_BIN="$SCRIPTS_DIR/mesh.sh"

# ── assertions ───────────────────────────────────────────────────────
#
# Every assertion goes through a function, and never through a bare
# [[ ]] or ! cmd. bash 3.2 (the system bash on macOS, and the one this
# suite runs under) does not trip `set -e` or the ERR trap for either of
# those when they are not the last statement of the test body:
#
#   bash-3.2 -c 'set -e; f(){ [[ 1 == 2 ]]; echo REACHED; }; f'   -> REACHED
#   bash-3.2 -c 'set -e; f(){ ! true; echo REACHED; }; f'         -> REACHED
#
# A failing *function* call does propagate, on 3.2 as on 5.x, so wrapping
# is what makes an assertion load-bearing. This is not cosmetic: the suite
# was green for 227 tests while one of them asserted a value the code has
# never written.

_afail() { printf 'assertion failed: %s\n' "$*" >&2; return 1; }

assert_ok()   { [[ "$status" -eq 0 ]] || _afail "expected success, got status $status: $output"; }
assert_fail() { [[ "$status" -ne 0 ]] || _afail "expected failure, got status 0: $output"; }
assert_status() { [[ "$status" -eq "$1" ]] || _afail "expected status $1, got $status: $output"; }

assert_eq() { [[ "$1" == "$2" ]] || _afail "expected '$2', got '$1'"; }
assert_ne() { [[ "$1" != "$2" ]] || _afail "expected anything but '$2'"; }
assert_num_eq() { [[ "$1" -eq "$2" ]] || _afail "expected $2, got '$1'"; }

assert_contains() { [[ "$1" == *"$2"* ]] || _afail "'$1' does not contain '$2'"; }
refute_contains() { [[ "$1" != *"$2"* ]] || _afail "'$1' unexpectedly contains '$2'"; }
assert_match()    { [[ "$1" == $2 ]] || _afail "'$1' does not match '$2'"; }
assert_empty()    { [[ -z "$1" ]] || _afail "expected empty, got '$1'"; }
assert_not_empty() { [[ -n "$1" ]] || _afail "expected a value, got empty"; }

assert_file()    { [[ -f "$1" ]] || _afail "no such file: $1"; }
refute_file()    { [[ ! -f "$1" ]] || _afail "file should not exist: $1"; }
assert_dir()     { [[ -d "$1" ]] || _afail "no such directory: $1"; }
refute_dir()     { [[ ! -d "$1" ]] || _afail "directory should not exist: $1"; }
assert_symlink() { [[ -L "$1" ]] || _afail "not a symlink: $1"; }

# `! cmd` has the same bash 3.2 problem as a bare [[ ]].
refute() { if "$@"; then _afail "expected '$*' to fail"; fi; }

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

    # mesh.sh loads config for every command, so a subprocess run would
    # otherwise read the developer's live tmux options and the suite would pass
    # or fail depending on what they had set. A planted cache is also the
    # fixture for testing the config path itself.
    plant_config
}

# Write the config cache mesh.sh reads. Each argument is a VAR='value' line;
# anything not given keeps the default below.
#
# The first line is the format marker the loader checks. A cache without one is
# treated as a foreign or older format and rebuilt from the live tmux options,
# which would silently discard everything planted here. The fixture has to write
# what production writes; it was already coupled to the format, since it
# hardcodes all fifteen variable names.
plant_config() {
    local cache="$MESH_DIR/config_cache"
    mkdir -p "$MESH_DIR"
    cat > "$cache" <<'EOF'
# tk-config v1 agent-mesh
KEYBINDING='g'
ITEMS_PER_PAGE='10'
KEY_NEXT='i'
KEY_PREV='o'
KEY_QUIT='q'
ENABLED='on'
DELIVERY='stop-block'
PI_DELIVERY='push'
MAX_HOPS='4'
MAX_THREAD_MSGS='12'
MAX_BLOCKS='3'
MAX_BROADCAST='8'
ICON_MAIL='@'
DEBUG_LOG='0'
HOOK_ON_MAIL=''
EOF
    local line
    for line in "$@"; do
        printf '%s\n' "$line" >> "$cache"
    done
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

# A tmux server on a private socket, with a `tmux` wrapper first on PATH so
# every call from mesh reaches it and never the developer's own session.
# HOME is redirected too, because agent-mesh.tmux symlinks into it.
start_test_tmux() {
    REAL_TMUX=$(command -v tmux) || return 1
    TMUX_SOCKET="mesh-test-$$-${BATS_TEST_NUMBER:-0}"
    FAKE_HOME="$TEST_TMPDIR/home"
    mkdir -p "$TEST_TMPDIR/bin" "$FAKE_HOME"

    # kill-server ends the server but leaves the socket file behind, so a full
    # run used to drop 25 files into /tmp/tmux-$UID and never collect them. A
    # per-run TMUX_TMPDIR under $TMPDIR makes them disappear with the run.
    #
    # Short path on purpose: a unix socket caps at ~104 bytes and TEST_TMPDIR
    # already sits under a deep mktemp path, so putting the socket there fails
    # with "File name too long" on every call.
    MESH_SOCKDIR="${TMPDIR:-/tmp}/mesh-sock-$$"
    mkdir -p "$MESH_SOCKDIR"
    export TMUX_TMPDIR="$MESH_SOCKDIR"
    printf '#!/usr/bin/env bash\nexec %s -L %s "$@"\n' "$REAL_TMUX" "$TMUX_SOCKET" \
        > "$TEST_TMPDIR/bin/tmux"
    chmod +x "$TEST_TMPDIR/bin/tmux"
    export PATH="$TEST_TMPDIR/bin:$PATH"
    # -f /dev/null: starting a server sources ~/.tmux.conf, which would load the
    # developer's other plugins and let their hooks run against real state.
    tmux -f /dev/null new-session -d -s mesh
}

stop_test_tmux() {
    [[ -n "${TMUX_SOCKET:-}" ]] || return 0
    "${REAL_TMUX:-tmux}" -L "$TMUX_SOCKET" kill-server 2>/dev/null || true
    if [[ -n "${MESH_SOCKDIR:-}" && "$MESH_SOCKDIR" == */mesh-sock-* ]]; then
        rm -rf "$MESH_SOCKDIR"
        MESH_SOCKDIR=""
    fi
    return 0
}

run_mesh_help() { "$MESH_BIN" --help; }

# Load the plugin against the private server and the fake home.
run_plugin() {
    HOME="$FAKE_HOME" bash "$PROJECT_ROOT/agent-mesh.tmux"
}

# Path to the installed pi extension type definitions, for checking the
# extension's calls against the real API instead of against memory. Fails when
# pi is not installed, so the caller can skip.
pi_types_file() {
    local bin f
    bin=$(command -v pi 2>/dev/null) || return 1
    f="$(dirname "$bin")/../lib/node_modules/@earendil-works/pi-coding-agent/dist/core/extensions/types.d.ts"
    [[ -f "$f" ]] || return 1
    printf '%s' "$f"
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

pending_for()    { msql "SELECT COUNT(*) FROM messages WHERE to_session='$1' AND delivered_at IS NULL;"; }
body_of()        { msql "SELECT body FROM messages WHERE id=$1;"; }
count_agents()   { msql "SELECT COUNT(*) FROM agents;"; }
count_messages() { msql "SELECT COUNT(*) FROM messages;"; }
get_alias()      { msql "SELECT COALESCE(alias,'') FROM agents WHERE session_id='$1';"; }
get_harness()    { msql "SELECT harness FROM agents WHERE session_id='$1';"; }
get_push()       { msql "SELECT push_capable FROM agents WHERE session_id='$1';"; }
agent_exists()   { [[ "$(msql "SELECT COUNT(*) FROM agents WHERE session_id='$1';")" == "1" ]]; }
