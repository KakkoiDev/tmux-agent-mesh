#!/usr/bin/env bats


bats_require_minimum_version 1.5.0
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
    assert_file "$DB"
}

@test "init seeds the human participant" {
    assert_eq "$(get_harness human)" "human"
    assert_eq "$(get_alias human)" "human"
}

@test "init creates the notify directory" {
    assert_dir "$NOTIFY_DIR"
}

@test "init is non-destructive: existing messages survive re-init" {
    insert_message human a1 "keep me"
    "$MESH_BIN" init >/dev/null
    assert_eq "$(count_messages)" "1"
}

@test "init --reset drops all data" {
    insert_message human a1 "delete me"
    "$MESH_BIN" init --reset >/dev/null
    assert_eq "$(count_messages)" "0"
    # human is re-seeded
    assert_eq "$(get_alias human)" "human"
}

@test "init rejects unknown flags" {
    run "$MESH_BIN" init --bogus
    assert_fail
}

@test "init --reset drops channel and read state, not just messages" {
    local cid
    cid=$(msql "SELECT id FROM channels WHERE name='general';")
    insert_agent a1 claude
    msql "INSERT INTO channel_members (channel_id, session_id) VALUES ($cid, 'a1');"
    insert_message human a1 "gone"
    msql "INSERT INTO reads (message_id, reader, source)
          VALUES ((SELECT id FROM messages), 'a1', 'drain');"

    "$MESH_BIN" init --reset >/dev/null

    # Dropping only messages/agents left these pointing at rows that no longer
    # exist, and bash never enabled foreign_keys, so they became orphans rather
    # than errors.
    assert_num_eq "$(msql "SELECT COUNT(*) FROM reads;")" 0
    assert_num_eq "$(msql "SELECT COUNT(*) FROM channel_members WHERE session_id='a1';")" 0
}

# ── v1 -> channel model ──────────────────────────────────────────────

# The v1 shape, verbatim, as a fixture rather than read out of git history: this
# has to keep testing the schema people actually have on disk even after that
# commit is far behind.
_build_v1_db() {
    rm -f "$DB"
    sqlite3 "$DB" <<'SQL'
PRAGMA journal_mode=WAL;
CREATE TABLE agents (
    session_id TEXT PRIMARY KEY, harness TEXT NOT NULL, alias TEXT UNIQUE,
    tmux_pane TEXT, tmux_target TEXT, cwd TEXT, project_name TEXT,
    push_capable INTEGER NOT NULL DEFAULT 0, block_streak INTEGER NOT NULL DEFAULT 0,
    turn_state TEXT, model TEXT, transcript_path TEXT NOT NULL DEFAULT '',
    registered_at INTEGER NOT NULL DEFAULT (unixepoch()),
    last_seen INTEGER NOT NULL DEFAULT (unixepoch()));
CREATE TABLE messages (
    id INTEGER PRIMARY KEY, thread_id TEXT NOT NULL, from_session TEXT NOT NULL,
    to_session TEXT NOT NULL, body TEXT NOT NULL, hops INTEGER NOT NULL DEFAULT 0,
    expect_reply INTEGER NOT NULL DEFAULT 0, reply_to_id INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    delivered_at INTEGER, delivered_via TEXT);
CREATE TABLE threads (
    thread_id TEXT PRIMARY KEY, opener_session TEXT,
    msg_count INTEGER NOT NULL DEFAULT 0, closed_at INTEGER);
CREATE TABLE dispatches (
    id INTEGER PRIMARY KEY, tmux_pane TEXT, harness TEXT NOT NULL, task TEXT NOT NULL,
    alias TEXT, reply_to_session TEXT, worktree_branch TEXT,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()), claimed_by TEXT, claimed_at INTEGER);
INSERT INTO agents (session_id, harness, alias) VALUES ('a1','claude','alice'),('b2','codex','bob');
INSERT INTO messages (thread_id, from_session, to_session, body, hops, delivered_at, delivered_via)
  VALUES ('shared','a1','b2','one',0,NULL,NULL),
         ('shared','b2','a1','two',1,1700000000,'stop-block'),
         ('other','a1','human','three',0,NULL,NULL);
INSERT INTO threads (thread_id, opener_session, msg_count) VALUES ('shared','a1',2),('other','a1',1);
SQL
}

# What an intermediate build left behind: every table present, messages already
# addressed by channel, and a `threads` that still has no name. Not v1, not
# current, and not convertible: the thread names were never written down.
_build_half_db() {
    rm -f "$DB"
    sqlite3 "$DB" <<'SQL'
PRAGMA journal_mode=WAL;
CREATE TABLE agents (
    session_id TEXT PRIMARY KEY, harness TEXT NOT NULL, alias TEXT UNIQUE,
    tmux_pane TEXT, tmux_target TEXT, cwd TEXT, project_name TEXT,
    push_capable INTEGER NOT NULL DEFAULT 0, block_streak INTEGER NOT NULL DEFAULT 0,
    turn_state TEXT, model TEXT, transcript_path TEXT NOT NULL DEFAULT '',
    registered_at INTEGER NOT NULL DEFAULT (unixepoch()),
    last_seen INTEGER NOT NULL DEFAULT (unixepoch()));
CREATE TABLE channels (
    id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL DEFAULT 'channel', visibility TEXT NOT NULL DEFAULT 'public',
    topic TEXT NOT NULL DEFAULT '', created_by TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT (unixepoch()), archived_at INTEGER);
CREATE TABLE threads (
    thread_id INTEGER PRIMARY KEY, channel_id INTEGER NOT NULL,
    opener_session TEXT NOT NULL DEFAULT '', msg_count INTEGER NOT NULL DEFAULT 0,
    closed_at INTEGER);
CREATE TABLE messages (
    id INTEGER PRIMARY KEY, channel_id INTEGER NOT NULL, thread_id INTEGER NOT NULL,
    from_session TEXT NOT NULL, body TEXT NOT NULL, hops INTEGER NOT NULL DEFAULT 0,
    expect_reply INTEGER NOT NULL DEFAULT 0, reply_to_id INTEGER,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()));
CREATE TABLE dispatches (
    id INTEGER PRIMARY KEY, tmux_pane TEXT, harness TEXT NOT NULL, task TEXT NOT NULL,
    alias TEXT, reply_to_session TEXT, worktree_branch TEXT,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()), claimed_by TEXT, claimed_at INTEGER);
INSERT INTO agents (session_id, harness, alias) VALUES ('a1','claude','alice');
INSERT INTO channels (id, name) VALUES (1,'general');
INSERT INTO threads (thread_id, channel_id) VALUES (1,1);
INSERT INTO messages (channel_id, thread_id, from_session, body) VALUES (1,1,'a1','stranded');
SQL
}

@test "a v1 database migrates to the channel model" {
    _build_v1_db
    "$MESH_BIN" init >/dev/null

    # Every message survives, addressed by channel instead of to_session.
    assert_num_eq "$(msql "SELECT COUNT(*) FROM messages;")" 3
    assert_num_eq "$(msql "SELECT COUNT(*) FROM messages WHERE channel_id IS NULL;")" 0
    # One DM channel per ordered pair, either direction resolving to the same row.
    assert_num_eq "$(msql "SELECT COUNT(*) FROM channels WHERE kind='dm';")" 2
    assert_not_empty "$(msql "SELECT id FROM channels WHERE name='dm:a1:b2';")"
    # The v1 tables are gone, not left shadowing the new ones.
    assert_num_eq "$(msql "SELECT COUNT(*) FROM sqlite_master WHERE name LIKE '%_v1';")" 0
}

@test "v1 delivery columns become delivery rows" {
    _build_v1_db
    "$MESH_BIN" init >/dev/null

    # Exactly the one message that carried delivered_at, attributed to the agent
    # it was addressed to.
    assert_num_eq "$(msql "SELECT COUNT(*) FROM deliveries;")" 1
    assert_eq "$(msql "SELECT session_id FROM deliveries;")" "a1"
    assert_eq "$(msql "SELECT delivered_via FROM deliveries;")" "stop-block"
}

@test "a v1 thread tag becomes one thread row per channel it spanned" {
    _build_v1_db
    "$MESH_BIN" init >/dev/null

    assert_num_eq "$(msql "SELECT COUNT(*) FROM threads;")" 2
    # Scoped, which is what a bare tag could not be: the tag lives in the channel
    # its messages landed in.
    assert_eq "$(msql "SELECT c.name FROM threads t JOIN channels c ON c.id=t.channel_id
                       WHERE t.name='shared';")" "dm:a1:b2"
    assert_num_eq "$(msql "SELECT COUNT(*) FROM messages m JOIN threads t ON t.id=m.thread_id
                           WHERE t.name='shared';")" 2
}

@test "the migration gives every carried-over message a content address" {
    _build_v1_db
    "$MESH_BIN" init >/dev/null
    assert_num_eq "$(msql "SELECT COUNT(*) FROM messages WHERE uid IS NULL;")" 0
    assert_num_eq "$(msql "SELECT COUNT(DISTINCT uid) FROM messages;")" 3
    assert_num_eq "$(msql "SELECT length(uid) FROM messages WHERE id=1;")" 64
}

# A reply's uid has to fold in its parent's, or a reference stops meaning
# anything once either row leaves this machine. The backfill runs in id order for
# exactly this reason.
@test "a migrated reply hashes its parent's uid" {
    _build_v1_db
    msql "INSERT INTO messages (thread_id, from_session, to_session, body, reply_to_id)
               VALUES ('shared','b2','a1','a reply',1);"
    "$MESH_BIN" init >/dev/null

    local parent child nonce created
    parent=$(msql "SELECT uid FROM messages WHERE id=1;")
    child=$(msql "SELECT uid FROM messages WHERE body='a reply';")
    nonce=$(msql "SELECT nonce FROM messages WHERE body='a reply';")
    created=$(msql "SELECT created_at FROM messages WHERE body='a reply';")
    assert_not_empty "$parent"
    assert_eq "$child" \
        "$(_msg_uid "$nonce" "dm:a1:b2" "shared" "b2" "a reply" "$created" "$parent")"
}

@test "the migration leaves no dangling references" {
    _build_v1_db
    "$MESH_BIN" init >/dev/null
    assert_empty "$(msql "PRAGMA foreign_key_check;")"
    refute_file "$MESH_DIR/migration.log"
}

@test "migrating twice changes nothing" {
    _build_v1_db
    "$MESH_BIN" init >/dev/null
    local before
    before=$(msql "SELECT (SELECT COUNT(*) FROM messages)||'/'||(SELECT COUNT(*) FROM channels)
                        ||'/'||(SELECT COUNT(*) FROM threads)||'/'||(SELECT COUNT(*) FROM deliveries);")
    msql "PRAGMA user_version=0;"
    "$MESH_BIN" init >/dev/null
    assert_eq "$(msql "SELECT (SELECT COUNT(*) FROM messages)||'/'||(SELECT COUNT(*) FROM channels)
                            ||'/'||(SELECT COUNT(*) FROM threads)||'/'||(SELECT COUNT(*) FROM deliveries);")" "$before"
}

# The version used to live in a .schema_vN file beside the database, so it made
# a claim about whatever mesh.db happened to be in that directory. Restore a
# backup, copy a mailbox in from another machine, or delete the database and
# leave the marker, and the commands that go through _ensure_schema -- hook,
# cleanup and import -- would never migrate it again.
@test "a stale schema marker does not stop the migration" {
    _build_v1_db
    touch "$MESH_DIR/.schema_v5"
    "$MESH_BIN" cleanup --forced >/dev/null
    assert_num_eq "$(msql "SELECT COUNT(*) FROM pragma_table_info('threads') WHERE name='name';")" 1
    assert_num_eq "$(msql "SELECT COUNT(*) FROM messages WHERE channel_id IS NULL;")" 0
    refute_file "$MESH_DIR/.schema_v5"
}

@test "the schema version travels in the database, not beside it" {
    assert_num_eq "$(msql 'PRAGMA user_version;')" 5
    assert_num_eq "$(ls -1 "$MESH_DIR" | grep -c '^.schema_v' || true)" 0
}

@test "a mailbox that cannot be converted is not stamped as current" {
    _build_half_db
    # cleanup itself goes on to fail against the old shape, which is the point:
    # the version must not say "current" about a database this build cannot use.
    run "$MESH_BIN" cleanup --forced
    assert_num_eq "$(msql 'PRAGMA user_version;')" 0
    assert_contains "$(cat "$MESH_DIR/migration.log")" "cannot convert"
}

@test "doctor names the columns a half-converted mailbox is missing" {
    _build_half_db
    run "$MESH_BIN" doctor
    assert_fail
    assert_contains "$output" "schema has every column this build writes"
    assert_contains "$output" "threads.name"
    assert_contains "$output" "init --reset"
}

@test "the generated schema in mesh.sh matches the canonical file" {
    run "$SCRIPTS_DIR/gen-schema.sh" --check
    assert_ok
}

# ── migrations ───────────────────────────────────────────────────────

@test "every migration statement is one line" {
    # _apply_migrations feeds sqlite3 one line at a time, so a statement spanning
    # lines is delivered as fragments and every fragment is a parse error. Four
    # CREATE TABLE blocks sat here and had never executed once.
    local open_parens
    open_parens=$(printf '%s\n' "$_MIGRATIONS_SQL" | grep -c '([[:space:]]*$') || true
    assert_num_eq "${open_parens:-0}" 0
}

@test "a failed migration is recorded" {
    _MIGRATIONS_SQL='ALTER TABLE nosuchtable ADD COLUMN x TEXT;'
    _apply_migrations
    assert_file "$MESH_DIR/migration.log"
    assert_contains "$(cat "$MESH_DIR/migration.log")" "no such table"
}

@test "an already-applied migration is not recorded as a failure" {
    # Every ALTER in the real list is a duplicate column on a database init just
    # built, which is the steady state on every later invocation.
    _apply_migrations
    refute_file "$MESH_DIR/migration.log"
}

@test "foreign keys are enforced" {
    # Per connection, and sql() opens a new one per call, so the pragma has to
    # ride in the helper rather than being set once at init.
    assert_eq "$(sql 'PRAGMA foreign_keys;')" "1"
}

@test "deleting a message takes its read receipts with it" {
    insert_message human a1 "gone"
    msql "INSERT INTO reads (message_id, reader, source)
          VALUES ((SELECT id FROM messages), 'a1', 'drain');"
    sql "DELETE FROM messages;"
    assert_num_eq "$(msql "SELECT COUNT(*) FROM reads;")" 0
}

# ── register ─────────────────────────────────────────────────────────

@test "register creates an agent row" {
    cmd_register --session s1 --harness claude
    agent_exists s1
    assert_eq "$(get_harness s1)" "claude"
}

@test "register marks pi as push capable" {
    cmd_register --session s1 --harness pi
    assert_eq "$(get_push s1)" "1"
}

@test "register marks claude as not push capable" {
    cmd_register --session s1 --harness claude
    assert_eq "$(get_push s1)" "0"
}

@test "register accepts codex and gemini" {
    cmd_register --session s1 --harness codex
    cmd_register --session s2 --harness gemini
    assert_eq "$(get_harness s1)" "codex"
    assert_eq "$(get_harness s2)" "gemini"
}

@test "register rejects an unknown harness" {
    run cmd_register --session s1 --harness borg
    assert_fail
    assert_contains "$output" "unknown harness"
}

@test "register requires a session id" {
    run cmd_register --harness claude
    assert_fail
}

@test "register twice on the same session updates without duplicating" {
    cmd_register --session s1 --harness claude
    cmd_register --session s1 --harness pi
    assert_eq "$(msql "SELECT COUNT(*) FROM agents WHERE session_id='s1';")" "1"
    assert_eq "$(get_harness s1)" "pi"
    assert_eq "$(get_push s1)" "1"
}

@test "register evicts a stale agent on the same pane" {
    cmd_register --session old --harness claude --pane %5
    cmd_register --session new --harness claude --pane %5
    refute agent_exists old
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
    assert_eq "$(msql "SELECT COALESCE(tmux_target,'') FROM agents WHERE session_id='s1';")" ""
}

@test "register stores the target when tmux confirms the pane" {
    _mock_tmux_pane %7
    cmd_register --session s1 --harness claude --pane %7
    assert_eq "$(msql "SELECT tmux_target FROM agents WHERE session_id='s1';")" "work:2.1"
}

@test "the degenerate target ':.' resolves to nothing" {
    _mock_tmux_pane %7
    cmd_register --session s1 --harness claude --pane %98
    cmd_register --session s2 --harness claude --pane %99
    run _resolve_ref ":."
    assert_status 1
}

@test "register applies an alias when given" {
    cmd_register --session s1 --harness claude --alias reviewer
    assert_eq "$(get_alias s1)" "reviewer"
}

@test "register auto-names an agent without an alias" {
    cmd_register --session 019fb695aaaa --harness pi
    assert_eq "$(get_alias 019fb695aaaa)" "pi-019fb695"
}

@test "auto-names collide with a counter suffix" {
    cmd_register --session 019fb695aaaa --harness pi
    cmd_register --session 019fb695bbbb --harness pi
    assert_eq "$(get_alias 019fb695aaaa)" "pi-019fb695"
    assert_eq "$(get_alias 019fb695bbbb)" "pi-019fb695-2"
}

@test "re-register keeps the auto-name" {
    cmd_register --session 019fb695aaaa --harness pi
    cmd_register --session 019fb695aaaa --harness pi
    assert_eq "$(get_alias 019fb695aaaa)" "pi-019fb695"
}

@test "re-register keeps a chosen alias, auto-name does not clobber it" {
    cmd_register --session s1 --harness claude --alias reviewer
    cmd_register --session s1 --harness claude
    assert_eq "$(get_alias s1)" "reviewer"
}

# ── transcript ───────────────────────────────────────────────────────

@test "set-transcript records the path and transcript prints it" {
    cmd_register --session s1 --harness claude
    run cmd_set_transcript --session s1 "/x/.claude/projects/slug/s1.jsonl"
    assert_ok
    assert_eq "$(msql "SELECT transcript_path FROM agents WHERE session_id='s1';")" "/x/.claude/projects/slug/s1.jsonl"
    run cmd_transcript s1
    assert_ok
    assert_eq "$output" "/x/.claude/projects/slug/s1.jsonl"
}

@test "transcript prints (none) for an agent without one" {
    cmd_register --session s1 --harness claude
    run cmd_transcript s1
    assert_eq "$output" "(none)"
}

@test "transcript resolves by alias" {
    cmd_register --session s1 --harness claude --alias reviewer
    run cmd_transcript reviewer
    assert_ok
    assert_eq "$output" "(none)"
}

@test "set-transcript requires a path" {
    run cmd_set_transcript
    assert_fail
}

# ── deregister ───────────────────────────────────────────────────────

@test "deregister removes the agent" {
    cmd_register --session s1 --harness claude
    cmd_deregister --session s1
    refute agent_exists s1
}

@test "deregister refuses to remove the human" {
    run cmd_deregister --session human
    assert_fail
    agent_exists human
}

@test "deregister on an unknown session is a no-op" {
    run cmd_deregister --session nope
    assert_ok
}

@test "deregister closes threads opened by that session" {
    cmd_register --session s1 --harness claude
    cmd_register --session s2 --harness claude
    cmd_send --from s1 --to s2 --message hi --thread t1
    cmd_deregister --session s1
    assert_eq "$(msql "SELECT closed_at IS NOT NULL FROM threads WHERE name='t1';")" "1"
}

# ── name / alias ─────────────────────────────────────────────────────

@test "name sets the alias for the calling pane" {
    cmd_register --session s1 --harness claude --pane %7
    TMUX_PANE=%7 cmd_name reviewer
    assert_eq "$(get_alias s1)" "reviewer"
}

@test "name fails when the pane has no registered agent" {
    TMUX_PANE=%99
    run cmd_name reviewer
    assert_fail
    assert_contains "$output" "no agent registered for pane"
}

@test "alias labels an agent other than the caller" {
    insert_agent s1 claude "" %42
    run cmd_alias %42 scout
    assert_ok
    assert_eq "$(get_alias s1)" "scout"
}

@test "alias with one argument names the calling session" {
    cmd_register --session s1 --harness claude --pane %7
    TMUX_PANE=%7 run cmd_alias scout
    assert_ok
    assert_eq "$(get_alias s1)" "scout"
}

@test "alias with one argument fails when the pane has no agent" {
    TMUX_PANE=%99
    run cmd_alias scout
    assert_fail
    assert_contains "$output" "no agent registered for pane"
}

@test "alias fails on an unknown ref" {
    run cmd_alias nosuch scout
    assert_fail
}

@test "alias exits 2 on an ambiguous ref" {
    insert_agent abc111 claude
    insert_agent abc222 claude
    run cmd_alias abc scout
    assert_status 2
}

@test "name rejects the reserved human alias" {
    cmd_register --session s1 --harness claude --pane %7
    TMUX_PANE=%7
    run cmd_name human
    assert_fail
    assert_contains "$output" "reserved"
}

@test "name rejects an alias with invalid characters" {
    cmd_register --session s1 --harness claude --pane %7
    TMUX_PANE=%7
    run cmd_name "bad alias"
    assert_fail
}

@test "name rejects an alias held by another session" {
    cmd_register --session s1 --harness claude --alias reviewer
    cmd_register --session s2 --harness claude --pane %8
    TMUX_PANE=%8
    run cmd_name reviewer
    assert_fail
    assert_contains "$output" "already held"
}

@test "name is idempotent for the same session" {
    cmd_register --session s1 --harness claude --pane %7
    TMUX_PANE=%7
    cmd_name reviewer
    cmd_name reviewer
    assert_eq "$(get_alias s1)" "reviewer"
}

# ── ref resolution ───────────────────────────────────────────────────

@test "resolve by alias" {
    insert_agent s1 claude reviewer
    run _resolve_ref reviewer
    assert_ok
    assert_eq "$output" "s1"
}

@test "resolve by exact session id" {
    insert_agent abcdef claude
    run _resolve_ref abcdef
    assert_ok
    assert_eq "$output" "abcdef"
}

@test "resolve by pane id" {
    insert_agent s1 claude "" %12
    run _resolve_ref %12
    assert_ok
    assert_eq "$output" "s1"
}

@test "resolve by tmux target" {
    insert_agent s1 claude "" %12 "work:2.1"
    run _resolve_ref "work:2.1"
    assert_ok
    assert_eq "$output" "s1"
}

@test "resolve by unambiguous session id prefix" {
    insert_agent abcdef123 claude
    run _resolve_ref abcdef
    assert_ok
    assert_eq "$output" "abcdef123"
}

@test "resolve returns 2 on an ambiguous prefix" {
    insert_agent abc111 claude
    insert_agent abc222 claude
    run _resolve_ref abc
    assert_status 2
    assert_contains "$output" "ambiguous"
}

@test "resolve returns 1 for an unknown ref" {
    run _resolve_ref nosuchthing
    assert_status 1
}

@test "resolve returns 1 for an empty ref" {
    run _resolve_ref ""
    assert_status 1
}

# Negative boundary: underscore is a LIKE wildcard. Prefix matching must
# use substr so 'abc_' does not also match 'abcXdef'.
@test "resolve does not treat underscore as a wildcard" {
    insert_agent abc_def claude
    insert_agent abcXdef claude
    run _resolve_ref abc_
    assert_ok
    assert_eq "$output" "abc_def"
}

# Same boundary for percent, which would otherwise match everything.
@test "resolve does not treat percent as a wildcard" {
    insert_agent aaa claude
    insert_agent bbb claude
    run _resolve_ref "a%"
    assert_status 1
}

@test "resolve prefers alias over a session id prefix" {
    insert_agent zzz111 claude
    insert_agent other claude zzz
    run _resolve_ref zzz
    assert_ok
    assert_eq "$output" "other"
}

@test "resolve finds the human participant" {
    run _resolve_ref human
    assert_ok
    assert_eq "$output" "human"
}

# ── roster ───────────────────────────────────────────────────────────

@test "roster lists registered agents" {
    insert_agent s1 claude reviewer %1
    run cmd_roster
    assert_ok
    assert_contains "$output" "reviewer"
    assert_contains "$output" "claude"
}

@test "roster shows push capability per harness" {
    insert_agent s1 pi builder
    run cmd_roster
    assert_contains "$output" "builder"
    assert_contains "$output" "yes"
}

@test "roster falls back to a short session id when unaliased" {
    insert_agent abcdefghij claude
    run cmd_roster
    assert_contains "$output" "abcdefgh"
}

@test "roster counts pending messages" {
    insert_agent s1 claude reviewer
    insert_message human s1 "hi"
    insert_message human s1 "again"
    run cmd_roster
    assert_contains "$output" "2"
}

@test "roster does not count delivered messages as pending" {
    insert_agent s1 claude reviewer
    insert_message human s1 "hi"
    msql "INSERT INTO deliveries (message_id, session_id, delivered_via)
               SELECT id, 's1', 'stop-block' FROM messages;"
    run cmd_roster --json
    assert_ok
    echo "$output" | jq -e '.[] | select(.alias=="reviewer") | .pending == 0'
}

@test "roster --json emits valid json" {
    insert_agent s1 claude reviewer
    run cmd_roster --json
    assert_ok
    echo "$output" | jq -e '.[] | select(.alias=="reviewer") | .harness == "claude"'
}

@test "roster --json lists the human participant" {
    run cmd_roster --json
    echo "$output" | jq -e '.[] | select(.session_id=="human") | .harness == "human"'
}

@test "roster rejects unknown flags" {
    run cmd_roster --bogus
    assert_fail
}

# ── cleanup ──────────────────────────────────────────────────────────

@test "cleanup reaps an agent whose pane is gone" {
    insert_agent s1 claude reviewer %99
    tmux() { case "$1" in list-panes) printf '%%1\n%%2\n' ;; *) return 1 ;; esac; }
    cmd_cleanup
    refute agent_exists s1
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
    msql "UPDATE messages SET created_at=unixepoch()-90000;
          INSERT INTO deliveries (message_id, session_id, delivered_at, delivered_via)
               SELECT id, 's1', unixepoch()-90000, 'stop-block' FROM messages;"
    tmux() { case "$1" in list-panes) printf '%%1\n' ;; *) return 1 ;; esac; }
    cmd_cleanup
    assert_eq "$(count_messages)" "0"
}

@test "cleanup keeps pending mail regardless of age" {
    insert_agent s1 claude reviewer %1
    insert_message human s1 "still pending"
    msql "UPDATE messages SET created_at=unixepoch()-90000;"
    tmux() { case "$1" in list-panes) printf '%%1\n' ;; *) return 1 ;; esac; }
    cmd_cleanup
    assert_eq "$(count_messages)" "1"
}

# _update_status counts every undelivered row, so mail for an agent that will
# never come back kept a mail badge on the status bar forever. cleanup only
# purged *delivered* mail, so nothing ever removed it.
@test "cleanup drops pending mail addressed to an agent that is gone" {
    insert_message human ghost "nobody will ever read this"
    tmux() { case "$1" in list-panes) printf '' ;; *) return 1 ;; esac; }
    cmd_cleanup
    assert_eq "$(count_messages)" "0"
}

@test "cleanup keeps pending mail addressed to a registered agent" {
    insert_agent s1 claude reviewer %1
    insert_message human s1 "still wanted"
    tmux() { case "$1" in list-panes) printf '%%1\n' ;; *) return 1 ;; esac; }
    cmd_cleanup
    assert_eq "$(count_messages)" "1"
}

# An unclaimed dispatch was never reaped. Pane ids restart at %0 after a tmux
# server restart, so a stale row could be claimed by an unrelated fresh agent.
@test "cleanup reaps an unclaimed dispatch whose pane is gone" {
    msql "INSERT INTO dispatches (tmux_pane, harness, task) VALUES ('%99','claude','stale');"
    tmux() { case "$1" in list-panes) printf '%%1\n' ;; *) return 1 ;; esac; }
    cmd_cleanup
    assert_eq "$(msql 'SELECT COUNT(*) FROM dispatches;')" "0"
}

@test "cleanup keeps an unclaimed dispatch whose pane is still live" {
    msql "INSERT INTO dispatches (tmux_pane, harness, task) VALUES ('%1','claude','waiting');"
    tmux() { case "$1" in list-panes) printf '%%1\n' ;; *) return 1 ;; esac; }
    cmd_cleanup
    assert_eq "$(msql 'SELECT COUNT(*) FROM dispatches;')" "1"
}

# ── deregister leaves nothing behind ─────────────────────────────────

@test "deregister drops the agent's undelivered mail" {
    insert_agent s1 claude reviewer %1
    insert_message human s1 "never read"
    cmd_deregister --session s1
    assert_eq "$(count_messages)" "0"
}

@test "deregister logs the mail it could not deliver" {
    insert_agent s1 claude reviewer %1
    insert_message human s1 "never read"
    cmd_deregister --session s1
    assert_file "$DELIVERY_LOG"
    jq -e 'select(.via == "undeliverable") | .to == "s1"' < "$DELIVERY_LOG"
}

@test "deregister leaves other agents' mail alone" {
    insert_agent s1 claude reviewer %1
    insert_agent s2 claude builder %2
    insert_message human s1 "for s1"
    insert_message human s2 "for s2"
    cmd_deregister --session s1
    assert_eq "$(count_messages)" "1"
    assert_eq "$(msql "SELECT body FROM messages;")" "for s2"
}

# A message with more than one recipient is not the departing agent's to take
# away. Only the mail nobody else could read goes.
@test "deregister keeps mail another member has not read yet" {
    insert_agent s1 claude reviewer %1
    insert_agent s2 claude builder %2
    cmd_register --session s3 --harness claude --pane %3 >/dev/null
    msql "INSERT INTO channels (name, kind, visibility, created_by)
               VALUES ('team', 'channel', 'public', 's3');
          INSERT INTO channel_members (channel_id, session_id)
               SELECT id, 's1' FROM channels WHERE name='team';
          INSERT INTO channel_members (channel_id, session_id)
               SELECT id, 's2' FROM channels WHERE name='team';
          INSERT INTO channel_members (channel_id, session_id)
               SELECT id, 's3' FROM channels WHERE name='team';"
    cmd_send --from s3 --channel team --message "for the team"
    cmd_deregister --session s1
    assert_eq "$(count_messages)" "1"
    assert_eq "$(pending_for s2)" "1"
}

# ── turn state ───────────────────────────────────────────────────────
#
# Recorded from mesh's own hooks. Everything else has to scrape the pane for
# words like "tokens" to guess whether an agent is mid-turn; this is the answer,
# and it is what decides whether waking an idle agent is safe to attempt.

@test "a new session starts idle" {
    echo '{"session_id":"t1","cwd":"/tmp/p"}' | cmd_hook SessionStart
    assert_eq "$(_turn_state t1)" "idle"
}

@test "a prompt marks the agent working" {
    echo '{"session_id":"t1","cwd":"/tmp/p"}' | cmd_hook SessionStart
    echo '{"session_id":"t1"}' | cmd_hook UserPromptSubmit
    assert_eq "$(_turn_state t1)" "working"
}

@test "a turn end marks the agent idle again" {
    echo '{"session_id":"t1","cwd":"/tmp/p"}' | cmd_hook SessionStart
    echo '{"session_id":"t1"}' | cmd_hook UserPromptSubmit
    echo '{"session_id":"t1"}' | cmd_hook Stop
    assert_eq "$(_turn_state t1)" "idle"
}

@test "gemini turn boundaries move the same state" {
    echo '{"session_id":"t1","cwd":"/tmp/p"}' | cmd_hook SessionStart
    echo '{"session_id":"t1"}' | cmd_hook BeforeAgent
    assert_eq "$(_turn_state t1)" "working"
    echo '{"session_id":"t1"}' | cmd_hook AfterAgent
    assert_eq "$(_turn_state t1)" "idle"
}

# The state has to be right even when delivery is off, because the wake path
# reads it and delivery mode has nothing to do with whether a turn is running.
@test "turn state is recorded even with delivery off" {
    DELIVERY=off
    echo '{"session_id":"t1","cwd":"/tmp/p"}' | cmd_hook SessionStart
    echo '{"session_id":"t1"}' | cmd_hook UserPromptSubmit
    assert_eq "$(_turn_state t1)" "working"
    echo '{"session_id":"t1"}' | cmd_hook Stop
    assert_eq "$(_turn_state t1)" "idle"
}

@test "an unknown session has no state to report" {
    assert_eq "$(_turn_state nosuch)" "idle"
}

@test "the roster shows turn state" {
    insert_agent s1 claude reviewer %1
    msql "UPDATE agents SET turn_state='working' WHERE session_id='s1';"
    run cmd_roster
    assert_contains "$output" "STATE"
    assert_contains "$output" "working"
}

# ── model ────────────────────────────────────────────────────────────

@test "session start records a model given as a string" {
    echo '{"session_id":"t1","cwd":"/tmp/p","model":"claude-opus-5"}' | cmd_hook SessionStart
    assert_eq "$(msql "SELECT model FROM agents WHERE session_id='t1';")" "claude-opus-5"
}

# Harnesses disagree on the shape, so both are accepted.
@test "session start records a model given as an object" {
    echo '{"session_id":"t1","cwd":"/tmp/p","model":{"id":"claude-opus-5","display_name":"Opus"}}' \
        | cmd_hook SessionStart
    assert_eq "$(msql "SELECT model FROM agents WHERE session_id='t1';")" "claude-opus-5"
}

@test "a payload with no model leaves it null" {
    echo '{"session_id":"t1","cwd":"/tmp/p"}' | cmd_hook SessionStart
    assert_eq "$(msql "SELECT COALESCE(model,'none') FROM agents WHERE session_id='t1';")" "none"
}

# ── schema migration ─────────────────────────────────────────────────

# CREATE TABLE IF NOT EXISTS cannot add a column to a table that already exists,
# so an install that predates these columns needs the ALTERs to run.
@test "init adds the new columns to a database that predates them" {
    msql "DROP TABLE agents;"
    msql "CREATE TABLE agents (session_id TEXT PRIMARY KEY, harness TEXT NOT NULL,
          alias TEXT UNIQUE, tmux_pane TEXT, tmux_target TEXT, cwd TEXT,
          project_name TEXT, push_capable INTEGER NOT NULL DEFAULT 0,
          block_streak INTEGER NOT NULL DEFAULT 0,
          registered_at INTEGER NOT NULL DEFAULT (unixepoch()),
          last_seen INTEGER NOT NULL DEFAULT (unixepoch()));"
    "$MESH_BIN" init >/dev/null
    assert_contains "$(msql '.schema agents')" "turn_state"
    assert_contains "$(msql '.schema agents')" "model"
}

@test "init is safe to run again once the columns exist" {
    run "$MESH_BIN" init
    assert_ok
    run "$MESH_BIN" init
    assert_ok
    assert_eq "$(msql "SELECT COUNT(*) FROM agents WHERE session_id='human';")" "1"
}

# ── selftest ─────────────────────────────────────────────────────────
#
# selftest is what a user runs to be shown that mesh works, so it has to be
# worth believing. It used to check five things and register two agents on the
# same pane, where the second evicted the first and nothing noticed.

@test "selftest passes against a fresh database" {
    run "$MESH_BIN" selftest
    assert_ok
    refute_contains "$output" "FAIL"
}

@test "selftest leaves the database as it found it" {
    insert_agent bystander claude watcher %1
    insert_message human bystander "pre-existing"
    run "$MESH_BIN" selftest
    assert_ok
    assert_eq "$(count_agents)" "2"
    assert_eq "$(count_messages)" "1"
}

# broadcast excludes only the human and the sender, so an unscoped fan-out in
# selftest would message every real agent registered on the machine.
@test "selftest does not reach agents outside its own run" {
    insert_agent bystander claude watcher %1
    run "$MESH_BIN" selftest
    assert_ok
    assert_eq "$(pending_for bystander)" "0"
}

# ── hook dispatch ────────────────────────────────────────────────────

@test "hook SessionStart registers the session" {
    echo '{"session_id":"h1","cwd":"/tmp/proj"}' | cmd_hook SessionStart
    agent_exists h1
    assert_eq "$(msql "SELECT project_name FROM agents WHERE session_id='h1';")" "proj"
}

@test "hook SessionStart honours MESH_HARNESS" {
    MESH_HARNESS=pi
    echo '{"session_id":"h1","cwd":"/tmp/proj"}' | cmd_hook SessionStart
    assert_eq "$(get_harness h1)" "pi"
    assert_eq "$(get_push h1)" "1"
}

@test "hook SessionEnd deregisters the session" {
    echo '{"session_id":"h1","cwd":"/tmp/proj"}' | cmd_hook SessionStart
    echo '{"session_id":"h1"}' | cmd_hook SessionEnd
    refute agent_exists h1
}

@test "hook with no session id is a no-op" {
    echo '{}' | cmd_hook SessionStart
    assert_eq "$(count_agents)" "1"  # human only
}

@test "hook accepts conversationId as the session key" {
    echo '{"conversationId":"g1","cwd":"/tmp/proj"}' | cmd_hook SessionStart
    agent_exists g1
}

@test "hook with an unknown event is a no-op" {
    echo '{"session_id":"h1"}' | cmd_hook Nonsense
    assert_eq "$(count_agents)" "1"
}

@test "hook is a no-op when the database is absent" {
    rm -f "$DB"
    run bash -c "echo '{\"session_id\":\"h1\"}' | MESH_DIR='$MESH_DIR' DB='$MESH_DIR/mesh.db' '$MESH_BIN' hook SessionStart"
    assert_ok
}

# ── doctor ───────────────────────────────────────────────────────────

@test "doctor reports on a healthy install" {
    run cmd_doctor
    assert_contains "$output" "database exists"
    assert_contains "$output" "human participant seeded"
}

@test "doctor fails when the database is missing" {
    rm -f "$DB"
    run cmd_doctor
    assert_fail
    assert_match "$output" '*FAIL*database exists*'
}
