-- v0.6 PR D — multi-seed similar mode.
--
-- Old shape: user_similarity_settings.seed_song_id was a single
-- FK column holding "the" seed. v0.6 lets a user stack multiple
-- songs as a combined seed set.
--
-- New shape: a join table user_similarity_seeds(user_id, song_id)
-- with FK cascade on BOTH ends. Deleting the user cascades the
-- set; deleting a seeded song cascades that song out of the set
-- without affecting the user's other seeds. Primary key is the
-- composite — a user can't add the same song twice.
--
-- Migration strategy:
--   1. CREATE the new join table.
--   2. INSERT any existing single-seed rows from the old column
--      into the new table (migrating v0.5 state cleanly).
--   3. DROP the now-redundant seed_song_id column.
--
-- After this migration, user_similarity_settings keeps only
-- bucket_weights (PR D of v0.5) + user_id. Seed state lives
-- entirely in user_similarity_seeds. The handler derives the
-- singular seedSongId API field (backward-compat) as "first
-- element of the seed set" via a stable ordering.

CREATE TABLE user_similarity_seeds (
    user_id TEXT NOT NULL REFERENCES auth_users(id)  ON DELETE CASCADE,
    song_id TEXT NOT NULL REFERENCES queue_songs(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, song_id)
);

-- Migrate v0.5 single seeds into the new table. NULL seed_song_id
-- rows don't match the WHERE clause and are skipped — the user's
-- (empty) settings row is preserved for the bucket_weights JSON.
INSERT INTO user_similarity_seeds (user_id, song_id)
SELECT user_id, seed_song_id
FROM   user_similarity_settings
WHERE  seed_song_id IS NOT NULL;

ALTER TABLE user_similarity_settings
    DROP COLUMN seed_song_id;
