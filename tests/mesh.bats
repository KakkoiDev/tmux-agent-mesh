#!/usr/bin/env bats

load helpers

setup() {
    setup_test_env
    source_mesh_functions
}

teardown() {
    teardown_test_env
}

# ── init ─────────────────────────────────────────────────────────────

@test "init creates the database" {
    [[ -f "$DB" ]]
}

@test "init seeds the human participant" {
    [[ "$(get_harness human)" == "human" ]]
    [[ "$(get_alias human)" == "human" ]]
}

@test "init creates the notify directory" {
    [[ -d "$NOTIFY_DIR" ]]
}

@test "init is non-destructive: existing messages survive re-init" {
    insert_message human a1 "keep me"
    "$MESH_BIN" init >/dev/null
    [[ "$(count_messages)" == "1" ]]
}

@test "init --reset drops all data" {
    insert_message human a1 "delete me"
    "$MESH_BIN" init --reset >/dev/null
    [[ "$(count_messages)" == "0" ]]
    # human is re-seeded
    [[ "$(get_alias human)" == "human" ]]
}

@test "init rejects unknown flags" {
    run "$MESH_BIN" init --bogus
    [[ "$status" -ne 0 ]]
}

# ── register ─────────────────────────────────────────────────────────

@test "register creates an agent row" {
    cmd_register --session s1 --harness claude
    agent_exists s1
    [[ "$(get_harness s1)" == "claude" ]]
}

@test "register marks pi as push capable" {
    cmd_register --session s1 --harness pi
    [[ "$(get_push s1)" == "1" ]]
}

@test "register marks claude as not push capable" {
    cmd_register --session s1 --harness claude
    [[ "$(get_push s1)" == "0" ]]
}

@test "register accepts codex and gemini" {
    cmd_register --session s1 --harness codex
    cmd_register --session s2 --harness gemini
    [[ "$(get_harness s1)" == "codex" ]]
    [[ "$(get_harness s2)" == "gemini" ]]
}

@test "register rejects an unknown harness" {
    run cmd_register --session s1 --harness borg
    [[ "$status" -ne 0 ]]
    [[ "$output" == *"unknown harness"* ]]
}

@test "register requires a session id" {
    run cmd_register --harness claude
    [[ "$status" -ne 0 ]]
}

@test "register twice on the same session updates without duplicating" {
    cmd_register --session s1 --harness claude
    cmd_register --session s1 --harness pi
    [[ "$(msql "SELECT COUNT(*) FROM agents WHERE session_id='s1';")" == "1" ]]
    [[ "$(get_harness s1)" == "pi" ]]
    [[ "$(get_push s1)" == "1" ]]
}

@test "register evicts a stale agent on the same pane" {
    cmd_register --session old --harness claude --pane %5
    cmd_register --session new --harness claude --pane %5
    ! agent_exists old
    agent_exists new
}

@test "register does not evict agents on other panes" {
    cmd_register --session a --harness claude --pane %1
    cmd_register --session b --harness claude --pane %2
    agent_exists a
    agent_exists b
}

@test "register with an empty pane does not evict paneless agents" {
    cmd_register --session a --harness claude
    cmd_register --session b --harness claude
    agent_exists a
    agent_exists b
}

# Format-aware tmux mock. Real tmux resolves each #{...} independently, so a
# dead pane yields empty fields rather than an error: asking for
# '#{session_name}:#{window_index}.#{pane_index}' returns the literal ":.".
# The mock must reproduce that per-format, otherwise these tests pass against
# the very bug they exist to catch.
_mock_tmux_pane() {
    local live_pane="$1"
    eval '
    tmux() {
        local fmt="${!#}" requested=""
        case "$*" in
            *display-message*) ;;
            *) return 1 ;;
        esac
        # -t <target> is the argument before the format
        requested=$(printf "%s\n" "$@" | grep -m1 "^%" || true)
        if [[ "$requested" == "'"$live_pane"'" ]]; then
            fmt="${fmt//\#\{pane_id\}/$requested}"
            fmt="${fmt//\#\{session_name\}/work}"
            fmt="${fmt//\#\{window_index\}/2}"
            fmt="${fmt//\#\{pane_index\}/1}"
        else
            fmt="${fmt//\#\{pane_id\}/}"
            fmt="${fmt//\#\{session_name\}/}"
            fmt="${fmt//\#\{window_index\}/}"
            fmt="${fmt//\#\{pane_index\}/}"
        fi
        printf "%s\n" "$fmt"
    }'
}

@test "register stores no target for a pane tmux does not know" {
    _mock_tmux_pane %7
    cmd_register --session s1 --harness claude --pane %99
    [[ "$(msql "SELECT COALESCE(tmux_target,'') FROM agents WHERE session_id='s1';")" == "" ]]
}

@test "register stores the target when tmux confirms the pane" {
    _mock_tmux_pane %7
    cmd_register --session s1 --harness claude --pane %7
    [[ "$(msql "SELECT tmux_target FROM agents WHERE session_id='s1';")" == "work:2.1" ]]
}

@test "the degenerate target ':.' resolves to nothing" {
    _mock_tmux_pane %7
    cmd_register --session s1 --harness claude --pane %98
    cmd_register --session s2 --harness claude --pane %99
    run _resolve_ref ":."
    [[ "$status" -eq 1 ]]
}

@test "register applies an alias when given" {
    cmd_register --session s1 --harness claude --alias reviewer
    [[ "$(get_alias s1)" == "reviewer" ]]
}

# ── deregister ───────────────────────────────────────────────────────

@test "deregister removes the agent" {
    cmd_register --session s1 --harness claude
    cmd_deregister --session s1
    ! agent_exists s1
}

@test "deregister refuses to remove the human" {
    run cmd_deregister --session human
    [[ "$status" -ne 0 ]]
    agent_exists human
}

@test "deregister on an unknown session is a no-op" {
    run cmd_deregister --session nope
    [[ "$status" -eq 0 ]]
}

@test "deregister closes threads opened by that session" {
    cmd_register --session s1 --harness claude
    msql "INSERT INTO threads (thread_id, opener_session) VALUES ('t1','s1');"
    cmd_deregister --session s1
    [[ "$(msql "SELECT closed_at IS NOT NULL FROM threads WHERE thread_id='t1';")" == "1" ]]
}

# ── name / alias ─────────────────────────────────────────────────────

@test "name sets the alias for the calling pane" {
    cmd_register --session s1 --harness claude --pane %7
    TMUX_PANE=%7 cmd_name reviewer
    [[ "$(get_alias s1)" == "reviewer" ]]
}

@test "name fails when the pane has no registered agent" {
    TMUX_PANE=%99
    run cmd_name reviewer
    [[ "$status" -ne 0 ]]
    [[ "$output" == *"cannot identify this session"* ]]
}

@test "name rejects the reserved human alias" {
    cmd_register --session s1 --harness claude --pane %7
    TMUX_PANE=%7
    run cmd_name human
    [[ "$status" -ne 0 ]]
    [[ "$output" == *"reserved"* ]]
}

@test "name rejects an alias with invalid characters" {
    cmd_register --session s1 --harness claude --pane %7
    TMUX_PANE=%7
    run cmd_name "bad alias"
    [[ "$status" -ne 0 ]]
}

@test "name rejects an alias held by another session" {
    cmd_register --session s1 --harness claude --alias reviewer
    cmd_register --session s2 --harness claude --pane %8
    TMUX_PANE=%8
    run cmd_name reviewer
    [[ "$status" -ne 0 ]]
    [[ "$output" == *"already held"* ]]
}

@test "name is idempotent for the same session" {
    cmd_register --session s1 --harness claude --pane %7
    TMUX_PANE=%7
    cmd_name reviewer
    cmd_name reviewer
    [[ "$(get_alias s1)" == "reviewer" ]]
}

# ── ref resolution ───────────────────────────────────────────────────

@test "resolve by alias" {
    insert_agent s1 claude reviewer
    run _resolve_ref reviewer
    [[ "$status" -eq 0 ]]
    [[ "$output" == "s1" ]]
}

@test "resolve by exact session id" {
    insert_agent abcdef claude
    run _resolve_ref abcdef
    [[ "$status" -eq 0 ]]
    [[ "$output" == "abcdef" ]]
}

@test "resolve by pane id" {
    insert_agent s1 claude "" %12
    run _resolve_ref %12
    [[ "$status" -eq 0 ]]
    [[ "$output" == "s1" ]]
}

@test "resolve by tmux target" {
    insert_agent s1 claude "" %12 "work:2.1"
    run _resolve_ref "work:2.1"
    [[ "$status" -eq 0 ]]
    [[ "$output" == "s1" ]]
}

@test "resolve by unambiguous session id prefix" {
    insert_agent abcdef123 claude
    run _resolve_ref abcdef
    [[ "$status" -eq 0 ]]
    [[ "$output" == "abcdef123" ]]
}

@test "resolve returns 2 on an ambiguous prefix" {
    insert_agent abc111 claude
    insert_agent abc222 claude
    run _resolve_ref abc
    [[ "$status" -eq 2 ]]
    [[ "$output" == *"ambiguous"* ]]
}

@test "resolve returns 1 for an unknown ref" {
    run _resolve_ref nosuchthing
    [[ "$status" -eq 1 ]]
}

@test "resolve returns 1 for an empty ref" {
    run _resolve_ref ""
    [[ "$status" -eq 1 ]]
}

# Negative boundary: underscore is a LIKE wildcard. Prefix matching must
# use substr so 'abc_' does not also match 'abcXdef'.
@test "resolve does not treat underscore as a wildcard" {
    insert_agent abc_def claude
    insert_agent abcXdef claude
    run _resolve_ref abc_
    [[ "$status" -eq 0 ]]
    [[ "$output" == "abc_def" ]]
}

# Same boundary for percent, which would otherwise match everything.
@test "resolve does not treat percent as a wildcard" {
    insert_agent aaa claude
    insert_agent bbb claude
    run _resolve_ref "a%"
    [[ "$status" -eq 1 ]]
}

@test "resolve prefers alias over a session id prefix" {
    insert_agent zzz111 claude
    insert_agent other claude zzz
    run _resolve_ref zzz
    [[ "$status" -eq 0 ]]
    [[ "$output" == "other" ]]
}

@test "resolve finds the human participant" {
    run _resolve_ref human
    [[ "$status" -eq 0 ]]
    [[ "$output" == "human" ]]
}

# ── roster ───────────────────────────────────────────────────────────

@test "roster lists registered agents" {
    insert_agent s1 claude reviewer %1
    run cmd_roster
    [[ "$status" -eq 0 ]]
    [[ "$output" == *"reviewer"* ]]
    [[ "$output" == *"claude"* ]]
}

@test "roster shows push capability per harness" {
    insert_agent s1 pi builder
    run cmd_roster
    [[ "$output" == *"builder"* ]]
    [[ "$output" == *"yes"* ]]
}

@test "roster falls back to a short session id when unaliased" {
    insert_agent abcdefghij claude
    run cmd_roster
    [[ "$output" == *"abcdefgh"* ]]
}

@test "roster counts pending messages" {
    insert_agent s1 claude reviewer
    insert_message human s1 "hi"
    insert_message human s1 "again"
    run cmd_roster
    [[ "$output" == *"2"* ]]
}

@test "roster does not count delivered messages as pending" {
    insert_agent s1 claude reviewer
    insert_message human s1 "hi"
    msql "UPDATE messages SET delivered_at=unixepoch(), delivered_via='stop-block';"
    run cmd_roster --json
    [[ "$status" -eq 0 ]]
    echo "$output" | jq -e '.[] | select(.alias=="reviewer") | .pending == 0'
}

@test "roster --json emits valid json" {
    insert_agent s1 claude reviewer
    run cmd_roster --json
    [[ "$status" -eq 0 ]]
    echo "$output" | jq -e '.[] | select(.alias=="reviewer") | .harness == "claude"'
}

@test "roster --json lists the human participant" {
    run cmd_roster --json
    echo "$output" | jq -e '.[] | select(.session_id=="human") | .harness == "human"'
}

@test "roster rejects unknown flags" {
    run cmd_roster --bogus
    [[ "$status" -ne 0 ]]
}

# ── cleanup ──────────────────────────────────────────────────────────

@test "cleanup reaps an agent whose pane is gone" {
    insert_agent s1 claude reviewer %99
    tmux() { case "$1" in list-panes) printf '%%1\n%%2\n' ;; *) return 1 ;; esac; }
    cmd_cleanup
    ! agent_exists s1
}

@test "cleanup keeps an agent whose pane is live" {
    insert_agent s1 claude reviewer %1
    tmux() { case "$1" in list-panes) printf '%%1\n%%2\n' ;; *) return 1 ;; esac; }
    cmd_cleanup
    agent_exists s1
}

@test "cleanup never reaps the human" {
    tmux() { case "$1" in list-panes) printf '' ;; *) return 1 ;; esac; }
    cmd_cleanup
    agent_exists human
}

@test "cleanup keeps paneless agents" {
    insert_agent s1 pi builder
    tmux() { case "$1" in list-panes) printf '' ;; *) return 1 ;; esac; }
    cmd_cleanup
    agent_exists s1
}

@test "cleanup drops delivered mail older than 24h" {
    insert_agent s1 claude reviewer %1
    insert_message human s1 "old"
    msql "UPDATE messages SET delivered_at=unixepoch()-90000, delivered_via='stop-block';"
    tmux() { case "$1" in list-panes) printf '%%1\n' ;; *) return 1 ;; esac; }
    cmd_cleanup
    [[ "$(count_messages)" == "0" ]]
}

@test "cleanup keeps pending mail regardless of age" {
    insert_agent s1 claude reviewer %1
    insert_message human s1 "still pending"
    msql "UPDATE messages SET created_at=unixepoch()-90000;"
    tmux() { case "$1" in list-panes) printf '%%1\n' ;; *) return 1 ;; esac; }
    cmd_cleanup
    [[ "$(count_messages)" == "1" ]]
}

# ── hook dispatch ────────────────────────────────────────────────────

@test "hook SessionStart registers the session" {
    echo '{"session_id":"h1","cwd":"/tmp/proj"}' | cmd_hook SessionStart
    agent_exists h1
    [[ "$(msql "SELECT project_name FROM agents WHERE session_id='h1';")" == "proj" ]]
}

@test "hook SessionStart honours MESH_HARNESS" {
    MESH_HARNESS=pi
    echo '{"session_id":"h1","cwd":"/tmp/proj"}' | cmd_hook SessionStart
    [[ "$(get_harness h1)" == "pi" ]]
    [[ "$(get_push h1)" == "1" ]]
}

@test "hook SessionEnd deregisters the session" {
    echo '{"session_id":"h1","cwd":"/tmp/proj"}' | cmd_hook SessionStart
    echo '{"session_id":"h1"}' | cmd_hook SessionEnd
    ! agent_exists h1
}

@test "hook with no session id is a no-op" {
    echo '{}' | cmd_hook SessionStart
    [[ "$(count_agents)" == "1" ]]  # human only
}

@test "hook accepts conversationId as the session key" {
    echo '{"conversationId":"g1","cwd":"/tmp/proj"}' | cmd_hook SessionStart
    agent_exists g1
}

@test "hook with an unknown event is a no-op" {
    echo '{"session_id":"h1"}' | cmd_hook Nonsense
    [[ "$(count_agents)" == "1" ]]
}

@test "hook is a no-op when the database is absent" {
    rm -f "$DB"
    run bash -c "echo '{\"session_id\":\"h1\"}' | MESH_DIR='$MESH_DIR' DB='$MESH_DIR/mesh.db' '$MESH_BIN' hook SessionStart"
    [[ "$status" -eq 0 ]]
}

# ── doctor ───────────────────────────────────────────────────────────

@test "doctor reports on a healthy install" {
    run cmd_doctor
    [[ "$output" == *"database exists"* ]]
    [[ "$output" == *"human participant seeded"* ]]
}

@test "doctor fails when the database is missing" {
    rm -f "$DB"
    run cmd_doctor
    [[ "$status" -ne 0 ]]
    [[ "$output" == *"FAIL"*"database exists"* ]]
}
