-- Per-user song ownership.
--
-- Every queue_songs stub is inserted by the refiller on behalf of a specific
-- user, and must reach only that user's queue when the download completes.
-- Before this migration, onLoadedSong iterated auth_users and attached to any
-- user whose queue was short — fine for single-user, incorrect for multi-user.
--
-- Nullable because:
--   a) rows inserted by the old code path (pre-migration) have no owner;
--   b) external tooling inserting songs for shared catalogs has no natural
--      owner either. The app treats NULL as "don't auto-attach to any queue".
--
-- ON DELETE CASCADE: if the requester is deleted, their still-pending stubs
-- go with them (they'd be unowned otherwise and stay forever).

ALTER TABLE queue_songs
    ADD COLUMN requesting_user_id TEXT REFERENCES auth_users(id) ON DELETE CASCADE;

CREATE INDEX idx_queue_songs_requester ON queue_songs(requesting_user_id);
