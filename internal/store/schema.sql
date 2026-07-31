-- tmux-agent-mesh store.
--
-- One recipient mechanism, not four. A message is posted to a channel and every
-- member of that channel is a recipient, so a direct message, a named group, a
-- public room and "everyone" are the same object with different membership. That
-- is what makes read receipts, file scoping and access rules one implementation
-- each instead of one per addressing mode.
--
-- Delivery and reading are separate append-only facts. Delivery is per recipient
-- because a message now has several. Reading is a log rather than a flag, so the
-- same agent reading twice is two rows, which is what a receipt has to show.

PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS agents (
    session_id    TEXT PRIMARY KEY,
    harness       TEXT NOT NULL,
    alias         TEXT UNIQUE,
    model         TEXT,
    -- Which machine the agent runs on. A remote mailbox serves several.
    host          TEXT NOT NULL DEFAULT '',
    tmux_pane     TEXT NOT NULL DEFAULT '',
    tmux_target   TEXT NOT NULL DEFAULT '',
    cwd           TEXT NOT NULL DEFAULT '',
    project_name  TEXT NOT NULL DEFAULT '',
    -- Can the harness be reached while it is idle, without keystrokes.
    push_capable  INTEGER NOT NULL DEFAULT 0,
    -- Consecutive mesh-forced turns, the unattended-token-burn brake.
    block_streak  INTEGER NOT NULL DEFAULT 0,
    -- From this plugin's own turn hooks, not from scraping the pane.
    turn_state    TEXT NOT NULL DEFAULT 'idle'
        CHECK (turn_state IN ('idle', 'working')),
    -- Full path to the agent's conversation transcript, so another agent or
    -- the human can open an old conversation. Empty until the agent reports it.
    transcript_path TEXT NOT NULL DEFAULT '',
    registered_at INTEGER NOT NULL DEFAULT (unixepoch()),
    last_seen     INTEGER NOT NULL DEFAULT (unixepoch())
);

-- kind is what the client shows, not a permission: 'dm' is a channel whose
-- membership happens to be two. visibility is the permission.
CREATE TABLE IF NOT EXISTS channels (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    kind        TEXT NOT NULL DEFAULT 'channel'
        CHECK (kind IN ('channel', 'dm')),
    visibility  TEXT NOT NULL DEFAULT 'public'
        CHECK (visibility IN ('public', 'private')),
    topic       TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    archived_at INTEGER
);

CREATE TABLE IF NOT EXISTS channel_members (
    channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    role       TEXT NOT NULL DEFAULT 'member'
        CHECK (role IN ('member', 'owner')),
    joined_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (channel_id, session_id)
);

-- Who is allowed to join a private channel at all, by harness or by model.
-- Empty means membership is the only gate. Any row makes it an allow-list, so a
-- rule that matches nothing locks the channel rather than opening it: sensitive
-- work must fail closed.
CREATE TABLE IF NOT EXISTS channel_rules (
    channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    subject    TEXT NOT NULL CHECK (subject IN ('harness', 'model')),
    value      TEXT NOT NULL,
    PRIMARY KEY (channel_id, subject, value)
);

CREATE TABLE IF NOT EXISTS messages (
    id           INTEGER PRIMARY KEY,
    channel_id   INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    -- Slack-style threading inside a channel. A top-level post threads on itself.
    thread_id    INTEGER,
    from_session TEXT NOT NULL,
    body         TEXT NOT NULL,
    hops         INTEGER NOT NULL DEFAULT 0,
    expect_reply INTEGER NOT NULL DEFAULT 0,
    reply_to_id  INTEGER REFERENCES messages(id) ON DELETE SET NULL,
    created_at   INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_messages_channel ON messages(channel_id, id);
CREATE INDEX IF NOT EXISTS idx_messages_thread  ON messages(thread_id);

-- Per recipient, because a message has several now. Delivery is at-most-once and
-- stamped at claim time; the row existing is the claim.
CREATE TABLE IF NOT EXISTS deliveries (
    message_id    INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    session_id    TEXT NOT NULL,
    delivered_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    delivered_via TEXT NOT NULL,
    PRIMARY KEY (message_id, session_id)
);

-- Append-only. A second read by the same reader is a second row, because "read
-- three times" is a thing a receipt has to be able to say.
--
-- For an agent, read is exactly the drain: the moment the text entered its
-- context. For a human it is the moment the client displayed it. Both are
-- recorded the same way and distinguished by source.
CREATE TABLE IF NOT EXISTS reads (
    id            INTEGER PRIMARY KEY,
    message_id    INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    reader        TEXT NOT NULL,
    read_at       INTEGER NOT NULL DEFAULT (unixepoch()),
    source        TEXT NOT NULL CHECK (source IN ('drain', 'client'))
);
CREATE INDEX IF NOT EXISTS idx_reads_message ON reads(message_id, read_at);

-- The row is the handle; the bytes live outside the database under a directory
-- only the server can read. An agent cannot open the path, so `file get` through
-- the service is the only way in, and that is where membership is checked.
CREATE TABLE IF NOT EXISTS files (
    id          INTEGER PRIMARY KEY,
    channel_id  INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    message_id  INTEGER REFERENCES messages(id) ON DELETE SET NULL,
    uploaded_by TEXT NOT NULL,
    name        TEXT NOT NULL,
    size        INTEGER NOT NULL,
    sha256      TEXT NOT NULL,
    -- Relative to the server's private store. Never sent to a client.
    stored_path TEXT NOT NULL,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_files_channel ON files(channel_id, id);

-- Every read of a file body, for the same reason reads exists.
CREATE TABLE IF NOT EXISTS file_access (
    id       INTEGER PRIMARY KEY,
    file_id  INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    reader   TEXT NOT NULL,
    at       INTEGER NOT NULL DEFAULT (unixepoch()),
    allowed  INTEGER NOT NULL
);

-- Conversation counters for the per-thread message cap.
CREATE TABLE IF NOT EXISTS threads (
    thread_id      INTEGER PRIMARY KEY,
    channel_id     INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    opener_session TEXT NOT NULL DEFAULT '',
    msg_count      INTEGER NOT NULL DEFAULT 0,
    closed_at      INTEGER
);

CREATE TABLE IF NOT EXISTS dispatches (
    id               INTEGER PRIMARY KEY,
    host             TEXT NOT NULL DEFAULT '',
    tmux_pane        TEXT NOT NULL DEFAULT '',
    harness          TEXT NOT NULL,
    task             TEXT NOT NULL,
    alias            TEXT,
    reply_to_session TEXT NOT NULL DEFAULT '',
    worktree_branch  TEXT,
    created_at       INTEGER NOT NULL DEFAULT (unixepoch()),
    claimed_by       TEXT,
    claimed_at       INTEGER
);

CREATE TABLE IF NOT EXISTS schema_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
