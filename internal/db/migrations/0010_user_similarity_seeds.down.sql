-- v0.6 PR D rollback. Reinstates the singular seed_song_id
-- column and copies back the first seed from the join table.
-- Drops the multi-seed table. Lossy: users who had multiple
-- seeds at rollback time keep only one (the arbitrary "first").

ALTER TABLE user_similarity_settings
    ADD COLUMN seed_song_id TEXT REFERENCES queue_songs(id) ON DELETE SET NULL;

-- SQLite UPDATE ... FROM works since 3.33 (Ubuntu 22.04+, Pi
-- OS bookworm ships 3.40). Copy back one seed per user,
-- arbitrary pick.
UPDATE user_similarity_settings
SET    seed_song_id = (
    SELECT song_id
    FROM   user_similarity_seeds
    WHERE  user_similarity_seeds.user_id = user_similarity_settings.user_id
    LIMIT  1
);

DROP TABLE IF EXISTS user_similarity_seeds;
