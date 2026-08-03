-- v1 -> channel model, second half. Runs after the schema has been created.
--
-- foreign_keys off for the rebuild: reply_to_id points inside the same
-- INSERT..SELECT, and a row referencing one not yet written would fail even
-- though the finished table is consistent. The caller runs PRAGMA
-- foreign_key_check afterwards, which proves it rather than trusting insert
-- order.
--
-- uid is left NULL here because sqlite3 has no sha256; the caller backfills it
-- row by row.
PRAGMA foreign_keys=OFF;
BEGIN;

-- A DM channel per ordered pair, so either direction resolves to one row.
INSERT OR IGNORE INTO channels (name, kind, visibility, created_by)
SELECT DISTINCT
       'dm:' || MIN(from_session, to_session) || ':' || MAX(from_session, to_session),
       'dm', 'private', from_session
  FROM messages_v1;

INSERT OR IGNORE INTO channel_members (channel_id, session_id)
SELECT c.id, m.from_session
  FROM messages_v1 m
  JOIN channels c
    ON c.name = 'dm:' || MIN(m.from_session, m.to_session) || ':' || MAX(m.from_session, m.to_session);

INSERT OR IGNORE INTO channel_members (channel_id, session_id)
SELECT c.id, m.to_session
  FROM messages_v1 m
  JOIN channels c
    ON c.name = 'dm:' || MIN(m.from_session, m.to_session) || ':' || MAX(m.from_session, m.to_session);

-- One thread row per (channel, old tag). A tag that spanned several pairs was
-- one thread only by accident of having no channel; it becomes one per channel,
-- which is the boundary it should always have had.
INSERT OR IGNORE INTO threads (channel_id, name, opener_session, created_at)
SELECT c.id, m.thread_id, MIN(m.from_session), MIN(m.created_at)
  FROM messages_v1 m
  JOIN channels c
    ON c.name = 'dm:' || MIN(m.from_session, m.to_session) || ':' || MAX(m.from_session, m.to_session)
 GROUP BY c.id, m.thread_id;

INSERT OR IGNORE INTO messages
       (id, channel_id, thread_id, from_session, body, hops, expect_reply, reply_to_id, created_at)
SELECT m.id, c.id, t.id, m.from_session, m.body, m.hops, m.expect_reply, m.reply_to_id, m.created_at
  FROM messages_v1 m
  JOIN channels c
    ON c.name = 'dm:' || MIN(m.from_session, m.to_session) || ':' || MAX(m.from_session, m.to_session)
  JOIN threads t ON t.channel_id = c.id AND t.name = m.thread_id
 ORDER BY m.id;

-- Delivery was a column on the row and is now a fact about one recipient.
INSERT OR IGNORE INTO deliveries (message_id, session_id, delivered_at, delivered_via)
SELECT id, to_session, delivered_at, COALESCE(delivered_via, 'v1')
  FROM messages_v1
 WHERE delivered_at IS NOT NULL;

DROP TABLE messages_v1;
DROP TABLE IF EXISTS threads_v1;
COMMIT;
PRAGMA foreign_keys=ON;
