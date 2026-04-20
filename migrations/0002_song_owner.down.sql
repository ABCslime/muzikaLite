DROP INDEX IF EXISTS idx_queue_songs_requester;

-- SQLite supports DROP COLUMN as of 3.35 (2021); modernc.org/sqlite embeds
-- a newer version. This is the symmetric inverse of the .up migration.
ALTER TABLE queue_songs DROP COLUMN requesting_user_id;
