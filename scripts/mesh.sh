#!/usr/bin/env bash
set -euo pipefail

# ── source helpers ───────────────────────────────────────────────────

SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPTS_DIR/helpers.sh"

MESH_DIR="${MESH_DIR:-$HOME/.tmux-agent-mesh}"
DB="${DB:-$MESH_DIR/mesh.db}"
NOTIFY_DIR="${NOTIFY_DIR:-$MESH_DIR/notify}"
DELIVERY_LOG="${DELIVERY_LOG:-$MESH_DIR/delivery.log}"

# Reserved session_id for the human participant.
HUMAN_ID="human"

# ── sql helpers ──────────────────────────────────────────────────────

sql() { printf '.timeout 100\n%s\n' "$*" | sqlite3 "$DB"; }
sql_sep() { local s="$1"; shift; printf '.timeout 100\n%s\n' "$*" | sqlite3 -separator "$s" "$DB"; }
sql_json() { printf '.timeout 100\n.mode json\n%s\n' "$*" | sqlite3 "$DB"; }
sql_esc() { local q="'"; printf '%s' "${1//$q/$q$q}"; }
json_esc() {
    local s="$1"
    s="${s//\\/\\\\}"
    s="${s//\"/\\\"}"
    s="${s//$'\n'/\\n}"
    s="${s//$'\t'/\\t}"
    s="${s//$'\r'/}"
    printf '%s' "$s"
}

# Fast JSON value extraction, replaces jq for simple string key lookups
_json_val() {
    local _t="${1#*\"$2\":\"}"
    [[ "$_t" == "$1" ]] && return
    printf '%s' "${_t%%\"*}"
}

_debug_log() {
    [[ "${DEBUG_LOG:-0}" == "1" ]] || return 0
    local _log="$MESH_DIR/debug.log"
    printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >> "$_log"
    local _lc
    _lc=$(wc -l < "$_log" 2>/dev/null) || return 0
    if [[ "${_lc:-0}" -gt 1500 ]]; then
        tail -n 1000 "$_log" > "$_log.tmp" && mv -f "$_log.tmp" "$_log"
    fi
}

_die() { printf '%s\n' "$*" >&2; exit 1; }

# Load config from cache without a freshness check. Hook hot path.
_load_config_fast() {
    local cache="$MESH_DIR/config_cache"
    if [[ -f "$cache" ]]; then
        # shellcheck disable=SC1090
        source "$cache"
    else
        load_config
    fi
}

# ── identity ─────────────────────────────────────────────────────────

# Map a session_id to the file used as its wake flag. Pi watches this.
_notify_flag() {
    local sid="$1" safe
    safe=$(printf '%s' "$sid" | tr -c 'A-Za-z0-9._-' '_')
    printf '%s/%s.flag' "$NOTIFY_DIR" "$safe"
}

# Resolve a user-supplied reference to a session_id.
# Order: alias, exact session_id, %pane, session:window.pane, unambiguous id prefix.
# Exit 2 on ambiguity so callers can distinguish it from "not found".
_resolve_ref() {
    local ref="$1" out esc n
    [[ -z "$ref" ]] && return 1
    esc=$(sql_esc "$ref")

    out=$(sql "SELECT session_id FROM agents WHERE alias='$esc' LIMIT 1;")
    [[ -n "$out" ]] && { printf '%s' "$out"; return 0; }

    out=$(sql "SELECT session_id FROM agents WHERE session_id='$esc' LIMIT 1;")
    [[ -n "$out" ]] && { printf '%s' "$out"; return 0; }

    if [[ "$ref" == %* ]]; then
        out=$(sql "SELECT session_id FROM agents WHERE tmux_pane='$esc' LIMIT 1;")
        [[ -n "$out" ]] && { printf '%s' "$out"; return 0; }
    fi

    out=$(sql "SELECT session_id FROM agents WHERE tmux_target='$esc' LIMIT 1;")
    [[ -n "$out" ]] && { printf '%s' "$out"; return 0; }

    # Prefix match via substr, not LIKE: '_' and '%' are LIKE wildcards.
    n=$(sql "SELECT COUNT(*) FROM agents WHERE substr(session_id,1,length('$esc'))='$esc';")
    if [[ "${n:-0}" -eq 1 ]]; then
        sql "SELECT session_id FROM agents WHERE substr(session_id,1,length('$esc'))='$esc';"
        return 0
    fi
    if [[ "${n:-0}" -gt 1 ]]; then
        printf "ambiguous ref '%s' matches %s agents\n" "$ref" "$n" >&2
        return 2
    fi
    return 1
}

# Identify the caller's own session_id.
# Precedence: explicit --session, then the agent registered to $TMUX_PANE.
_self_session() {
    local explicit="${1:-}"
    if [[ -n "$explicit" ]]; then printf '%s' "$explicit"; return 0; fi
    if [[ -n "${TMUX_PANE:-}" ]]; then
        local out
        out=$(sql "SELECT session_id FROM agents WHERE tmux_pane='$(sql_esc "$TMUX_PANE")' LIMIT 1;")
        [[ -n "$out" ]] && { printf '%s' "$out"; return 0; }
    fi
    return 1
}

_push_capable_for() {
    case "$1" in
        pi) printf '1' ;;
        *)  printf '0' ;;
    esac
}

# ── schema ───────────────────────────────────────────────────────────

_SCHEMA_SQL='
CREATE TABLE IF NOT EXISTS agents (
    session_id    TEXT PRIMARY KEY,
    harness       TEXT NOT NULL,
    alias         TEXT UNIQUE,
    tmux_pane     TEXT,
    tmux_target   TEXT,
    cwd           TEXT,
    project_name  TEXT,
    push_capable  INTEGER NOT NULL DEFAULT 0,
    block_streak  INTEGER NOT NULL DEFAULT 0,
    registered_at INTEGER NOT NULL DEFAULT (unixepoch()),
    last_seen     INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS messages (
    id            INTEGER PRIMARY KEY,
    thread_id     TEXT NOT NULL,
    from_session  TEXT NOT NULL,
    to_session    TEXT NOT NULL,
    body          TEXT NOT NULL,
    hops          INTEGER NOT NULL DEFAULT 0,
    expect_reply  INTEGER NOT NULL DEFAULT 0,
    reply_to_id   INTEGER,
    created_at    INTEGER NOT NULL DEFAULT (unixepoch()),
    delivered_at  INTEGER,
    delivered_via TEXT
);
CREATE INDEX IF NOT EXISTS idx_pending ON messages(to_session, delivered_at);
CREATE INDEX IF NOT EXISTS idx_thread ON messages(thread_id);

CREATE TABLE IF NOT EXISTS threads (
    thread_id      TEXT PRIMARY KEY,
    opener_session TEXT,
    msg_count      INTEGER NOT NULL DEFAULT 0,
    closed_at      INTEGER
);

CREATE TABLE IF NOT EXISTS dispatches (
    id               INTEGER PRIMARY KEY,
    tmux_pane        TEXT,
    harness          TEXT NOT NULL,
    task             TEXT NOT NULL,
    alias            TEXT,
    reply_to_session TEXT,
    worktree_branch  TEXT,
    created_at       INTEGER NOT NULL DEFAULT (unixepoch()),
    claimed_by       TEXT,
    claimed_at       INTEGER
);
'

# ── init ─────────────────────────────────────────────────────────────

# Unlike tracker, init is non-destructive by default: mesh rows are messages,
# not ephemeral session state. --reset is the explicit destructive path.
cmd_init() {
    local reset=0
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --reset) reset=1 ;;
            *) _die "init: unknown flag '$1'" ;;
        esac
        shift
    done

    mkdir -p "$MESH_DIR" "$NOTIFY_DIR"

    if [[ "$reset" -eq 1 ]]; then
        sqlite3 "$DB" <<'SQL'
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=100;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS threads;
DROP TABLE IF EXISTS dispatches;
DROP TABLE IF EXISTS agents;
SQL
    fi

    printf 'PRAGMA journal_mode=WAL;\nPRAGMA busy_timeout=100;\n%s\n' "$_SCHEMA_SQL" \
        | sqlite3 "$DB" >/dev/null

    # The human is a first-class participant, seeded once.
    sql "INSERT OR IGNORE INTO agents (session_id, harness, alias, push_capable)
         VALUES ('$HUMAN_ID', 'human', '$HUMAN_ID', 0);"

    echo "Initialized: $DB"
}

_ensure_schema() {
    [[ -f "$DB" ]] || return 0
    [[ -f "$MESH_DIR/.schema_v1" ]] && return 0
    printf '%s\n' "$_SCHEMA_SQL" | sqlite3 "$DB" >/dev/null 2>&1 || true
    touch "$MESH_DIR/.schema_v1" 2>/dev/null || true
}

# ── register / deregister ────────────────────────────────────────────

cmd_register() {
    local sid="" harness="claude" alias="" pane="${TMUX_PANE:-}" cwd=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --session) sid="${2:-}"; shift ;;
            --harness) harness="${2:-}"; shift ;;
            --alias)   alias="${2:-}"; shift ;;
            --pane)    pane="${2:-}"; shift ;;
            --cwd)     cwd="${2:-}"; shift ;;
            *) _die "register: unknown flag '$1'" ;;
        esac
        shift
    done
    [[ -n "$sid" ]] || _die "register: --session is required"

    case "$harness" in
        claude|codex|gemini|pi|human) ;;
        *) _die "register: unknown harness '$harness'" ;;
    esac

    # tmux answers display-message for a dead pane with empty fields, yielding
    # the degenerate target ":.". Storing that makes ":." a live address that
    # resolves to an arbitrary agent, so echo pane_id back and only trust the
    # target when it identifies the pane we asked about.
    local target="" info=""
    if [[ -n "$pane" ]]; then
        info=$(tmux display-message -p -t "$pane" \
            '#{pane_id}|#{session_name}:#{window_index}.#{pane_index}' 2>/dev/null || true)
        [[ "${info%%|*}" == "$pane" ]] && target="${info#*|}"
    fi
    [[ -n "$cwd" ]] || cwd="$PWD"
    local project
    project=$(basename "$cwd" 2>/dev/null || printf '')

    local esid epane etarget ecwd eproject
    esid=$(sql_esc "$sid"); epane=$(sql_esc "$pane"); etarget=$(sql_esc "$target")
    ecwd=$(sql_esc "$cwd"); eproject=$(sql_esc "$project")

    # A pane can only host one agent. A new session on a known pane evicts
    # the stale row, otherwise pane-based ref resolution goes to a dead agent.
    sql "DELETE FROM agents
         WHERE tmux_pane='$epane' AND tmux_pane<>'' AND session_id<>'$esid';"

    sql "INSERT INTO agents (session_id, harness, tmux_pane, tmux_target, cwd, project_name, push_capable, last_seen)
         VALUES ('$esid', '$(sql_esc "$harness")', '$epane', '$etarget', '$ecwd', '$eproject', $(_push_capable_for "$harness"), unixepoch())
         ON CONFLICT(session_id) DO UPDATE SET
             harness=excluded.harness,
             tmux_pane=CASE WHEN excluded.tmux_pane<>'' THEN excluded.tmux_pane ELSE agents.tmux_pane END,
             tmux_target=CASE WHEN excluded.tmux_target<>'' THEN excluded.tmux_target ELSE agents.tmux_target END,
             cwd=excluded.cwd,
             project_name=excluded.project_name,
             push_capable=excluded.push_capable,
             last_seen=unixepoch();"

    [[ -n "$alias" ]] && _set_alias "$sid" "$alias"
    _debug_log "register sid=$sid harness=$harness pane=$pane alias=$alias"
    return 0
}

cmd_deregister() {
    local sid=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --session) sid="${2:-}"; shift ;;
            *) _die "deregister: unknown flag '$1'" ;;
        esac
        shift
    done
    [[ -n "$sid" ]] || sid=$(_self_session "" || true)
    [[ -n "$sid" ]] || return 0
    [[ "$sid" == "$HUMAN_ID" ]] && _die "deregister: refusing to remove the human participant"

    local esid
    esid=$(sql_esc "$sid")
    sql "UPDATE threads SET closed_at=unixepoch()
         WHERE closed_at IS NULL AND opener_session='$esid';"
    sql "DELETE FROM agents WHERE session_id='$esid';"
    rm -f "$(_notify_flag "$sid")" 2>/dev/null || true
    _debug_log "deregister sid=$sid"
    return 0
}

_set_alias() {
    local sid="$1" alias="$2"
    local esid ealias
    esid=$(sql_esc "$sid"); ealias=$(sql_esc "$alias")
    case "$alias" in
        "$HUMAN_ID") _die "name: '$HUMAN_ID' is reserved" ;;
        *[!A-Za-z0-9_-]*) _die "name: alias must be alphanumeric, dash or underscore" ;;
    esac
    local holder
    holder=$(sql "SELECT session_id FROM agents WHERE alias='$ealias';")
    if [[ -n "$holder" && "$holder" != "$sid" ]]; then
        _die "name: alias '$alias' is already held by $holder"
    fi
    sql "UPDATE agents SET alias='$ealias', last_seen=unixepoch() WHERE session_id='$esid';"
}

cmd_name() {
    local alias="${1:-}" sid
    [[ -n "$alias" ]] || _die "name: usage: name <alias>"
    sid=$(_self_session "" || true)
    [[ -n "$sid" ]] || _die "name: cannot identify this session (no agent registered for pane ${TMUX_PANE:-<unset>})"
    _set_alias "$sid" "$alias"
    printf '%s is now "%s"\n' "$sid" "$alias"
}

# ── roster ───────────────────────────────────────────────────────────

cmd_roster() {
    local as_json=0 remote=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --json)   as_json=1 ;;
            --remote) remote="${2:-}"; shift ;;
            *) _die "roster: unknown flag '$1'" ;;
        esac
        shift
    done

    if [[ -n "$remote" ]]; then
        local args="roster"
        [[ "$as_json" -eq 1 ]] && args="roster --json"
        exec ssh "$remote" "tmux-agent-mesh $args"
    fi

    local q="SELECT a.alias, a.session_id, a.harness, a.project_name, a.push_capable,
                (SELECT COUNT(*) FROM messages m
                  WHERE m.to_session=a.session_id AND m.delivered_at IS NULL) AS pending,
                a.tmux_target
         FROM agents a ORDER BY (a.harness='human') DESC, a.alias IS NULL, a.alias, a.registered_at"

    if [[ "$as_json" -eq 1 ]]; then
        sql_json "$q;"
        return 0
    fi

    printf '%-14s %-8s %-18s %-5s %-8s %s\n' NAME HARNESS PROJECT PUSH PENDING PANE
    local alias sid harness project push pending target
    while IFS='|' read -r alias sid harness project push pending target; do
        [[ -z "$sid" ]] && continue
        [[ -n "$alias" ]] || alias="${sid:0:8}"
        [[ "$push" == "1" ]] && push="yes" || push="no"
        printf '%-14s %-8s %-18s %-5s %-8s %s\n' \
            "$alias" "$harness" "${project:--}" "$push" "$pending" "${target:--}"
    done <<EOF
$(sql_sep '|' "$q;")
EOF
}

# ── cleanup ──────────────────────────────────────────────────────────

# Remove agents whose tmux pane is gone, delivered mail older than 24h,
# and threads with no live participant.
cmd_cleanup() {
    _ensure_schema

    local live=""
    live=$(tmux list-panes -a -F '#{pane_id}' 2>/dev/null || true)

    local sid pane
    while IFS='|' read -r sid pane; do
        [[ -z "$sid" ]] && continue
        [[ "$sid" == "$HUMAN_ID" ]] && continue
        [[ -z "$pane" ]] && continue
        if printf '%s\n' "$live" | grep -qxF "$pane"; then continue; fi
        _debug_log "cleanup reaping sid=$sid pane=$pane (dead pane)"
        sql "DELETE FROM agents WHERE session_id='$(sql_esc "$sid")';"
        rm -f "$(_notify_flag "$sid")" 2>/dev/null || true
    done <<EOF
$(sql_sep '|' "SELECT session_id, COALESCE(tmux_pane,'') FROM agents;")
EOF

    sql "DELETE FROM messages WHERE delivered_at IS NOT NULL AND delivered_at < unixepoch()-86400;"
    sql "DELETE FROM threads WHERE closed_at IS NOT NULL AND closed_at < unixepoch()-86400;"
    sql "DELETE FROM threads
         WHERE thread_id NOT IN (SELECT DISTINCT thread_id FROM messages);"
    return 0
}

# ── hook (Claude Code, Codex, Gemini) ────────────────────────────────

cmd_hook() {
    # No database means mesh is not installed. Never fail a harness hook.
    [[ -f "$DB" ]] || return 0
    _ensure_schema
    _load_config_fast

    local event="${1:-}" harness="${MESH_HARNESS:-claude}"
    [[ -n "$event" ]] || return 0

    local json
    read -r json || true
    [[ -z "$json" ]] && json='{}'

    local sid
    sid=$(_json_val "$json" "session_id")
    [[ -z "$sid" ]] && sid=$(_json_val "$json" "conversationId")
    [[ -z "$sid" ]] && sid=$(_json_val "$json" "conversation_id")
    [[ -z "$sid" ]] && return 0

    local cwd
    cwd=$(_json_val "$json" "cwd")

    _debug_log "HOOK $event harness=$harness sid=$sid"

    case "$event" in
        SessionStart)
            if [[ -n "$cwd" ]]; then
                cmd_register --session "$sid" --harness "$harness" --cwd "$cwd"
            else
                cmd_register --session "$sid" --harness "$harness"
            fi
            ;;
        SessionEnd|session_shutdown)
            cmd_deregister --session "$sid"
            ;;
        *) return 0 ;;
    esac
    return 0
}

# ── doctor ───────────────────────────────────────────────────────────

_DOCTOR_FAIL=0

# Run a probe inside an `if` so `set -e` never aborts the report.
_probe() {
    local label="$1"; shift
    if "$@" >/dev/null 2>&1; then
        printf 'ok    %s\n' "$label"
    else
        printf 'FAIL  %s\n' "$label"
        _DOCTOR_FAIL=1
    fi
}

_human_seeded() {
    local n
    n=$(sql "SELECT COUNT(*) FROM agents WHERE session_id='$HUMAN_ID';" 2>/dev/null) || return 1
    [[ "${n:-0}" -eq 1 ]]
}

_claude_hook_wired() {
    jq -e --arg e "$1" \
        '(.hooks[$e] // []) | map(.hooks[]? | select(.command | test("tmux-agent-mesh"))) | length > 0' \
        "$HOME/.claude/settings.json"
}

cmd_doctor() {
    _DOCTOR_FAIL=0
    _probe "sqlite3 present"        command -v sqlite3
    _probe "tmux present"           command -v tmux
    _probe "jq present"             command -v jq
    _probe "tmux >= 3.0"            check_tmux_version 3.0
    _probe "database exists"        test -f "$DB"
    _probe "notify dir exists"      test -d "$NOTIFY_DIR"

    if [[ -f "$DB" ]]; then
        _probe "human participant seeded" _human_seeded
    fi

    local settings="$HOME/.claude/settings.json"
    if [[ -f "$settings" ]] && command -v jq >/dev/null 2>&1; then
        local ev
        for ev in SessionStart SessionEnd; do
            _probe "claude hook $ev wired" _claude_hook_wired "$ev"
        done
    else
        printf 'skip  claude hook wiring (no %s or no jq)\n' "$settings"
    fi

    return "$_DOCTOR_FAIL"
}

# ── main ─────────────────────────────────────────────────────────────

case "${1:-}" in
    init)        shift; cmd_init "$@" ;;
    register)    shift; cmd_register "$@" ;;
    deregister)  shift; cmd_deregister "$@" ;;
    name)        shift; cmd_name "$@" ;;
    roster)      shift; cmd_roster "$@" ;;
    cleanup)     shift; cmd_cleanup "$@" ;;
    hook)        shift; cmd_hook "$@" ;;
    doctor)      shift; cmd_doctor "$@" ;;
    ""|-h|--help)
        cat <<'USAGE'
tmux-agent-mesh - agent-to-agent messaging for tmux

  init [--reset]                  create the database (--reset drops all data)
  register --session <id> [--harness claude|codex|gemini|pi] [--alias <a>] [--pane <%N>] [--cwd <p>]
  deregister [--session <id>]
  name <alias>                    alias the calling session
  roster [--json] [--remote <host>]
  cleanup                         reap dead panes and old mail
  hook <event>                    harness hook entry point (JSON on stdin)
  doctor                          check dependencies and wiring
USAGE
        ;;
    *) _die "unknown command '$1' (try --help)" ;;
esac
