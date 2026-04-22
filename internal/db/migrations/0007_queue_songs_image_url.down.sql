-- v0.4.3 PR B rollback.
-- SQLite supports DROP COLUMN since 3.35 (2021), so this is safe.

ALTER TABLE queue_songs
    DROP COLUMN image_url;
