-- v0.5 PR B — per-user similar-mode state.
--
-- One row per user. seed_song_id is the queue_songs row anchoring the
-- user's currently active "similar to song" mode; NULL means similar
-- mode is off (refiller falls back to genre-random).
--
-- ON DELETE SET NULL on seed_song_id: when the user removes the
-- seed track from their queue (which cascades to the queue_songs
-- row via existing FKs), this column auto-clears. The frontend's
-- next /api/queue/similar-mode read returns null and the PlayerBar
-- lens icon flips off. No background sweeper needed.
--
-- v0.5 PR D will extend this table with a bucket_weights TEXT
-- (JSON) column for per-user bucket-weight tuning. PR B leaves
-- that out — running on bucket defaults until the settings UI
-- ships gives us cleaner staging.

CREATE TABLE user_similarity_settings (
    user_id      TEXT PRIMARY KEY REFERENCES auth_users(id) ON DELETE CASCADE,
    seed_song_id TEXT REFERENCES queue_songs(id) ON DELETE SET NULL
);
