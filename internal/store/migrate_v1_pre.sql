-- v1 -> channel model, first half. Runs before the schema is created.
--
-- CREATE TABLE IF NOT EXISTS would see the v1 tables and do nothing, leaving the
-- old shape in place, so they are renamed out of the way first. Guarded by the
-- caller: this only runs when messages still has a to_session column.
PRAGMA foreign_keys=OFF;
ALTER TABLE messages RENAME TO messages_v1;
ALTER TABLE threads  RENAME TO threads_v1;
