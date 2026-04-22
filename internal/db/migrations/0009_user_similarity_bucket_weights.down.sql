-- v0.5 PR D rollback.
-- SQLite supports DROP COLUMN since 3.35.

ALTER TABLE user_similarity_settings
    DROP COLUMN bucket_weights;
