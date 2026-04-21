-- v0.4.1 PR 4.1.A — per-user genre preferences.
--
-- ROADMAP §v0.4.1 item 1: user preferences tables with genre affinities,
-- normalized, source-scoped. No cross-source mapping yet — that's v0.6.
-- The two vocabularies (Bandcamp's free-form tags, Discogs' closed genre
-- list) are intentionally NOT merged into a single canonical namespace.
-- Empty tables for a user = "no preference" = refiller falls back to the
-- .env defaults (MUZIKA_BANDCAMP_DEFAULT_TAGS, MUZIKA_DISCOGS_DEFAULT_GENRES).

CREATE TABLE user_bandcamp_tags (
    user_id TEXT NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    tag     TEXT NOT NULL,
    PRIMARY KEY (user_id, tag)
);
-- Reverse-lookup index: "show me all users who follow tag X" (unused in
-- v0.4.1, but cheap and useful for v0.5 similarity).
CREATE INDEX idx_user_bandcamp_tags_tag ON user_bandcamp_tags(tag);

CREATE TABLE user_discogs_genres (
    user_id TEXT NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    genre   TEXT NOT NULL,
    PRIMARY KEY (user_id, genre)
);
CREATE INDEX idx_user_discogs_genres_genre ON user_discogs_genres(genre);
