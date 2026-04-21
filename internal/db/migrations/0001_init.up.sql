-- Muzika schema — one linear migration stream.
-- Table prefixes replace schemas (SQLite has none):
--   auth_*      identity
--   playlist_*  playlists + songs-in-playlists
--   queue_*     per-user queue, song catalog, listen stats
--   outbox      transactional outbox (§4 of ARCHITECTURE.md)

-- -----------------------------------------------------------------------------
-- Outbox
-- -----------------------------------------------------------------------------
CREATE TABLE outbox (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT    NOT NULL,
    payload    BLOB    NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_outbox_created ON outbox(created_at);

-- -----------------------------------------------------------------------------
-- Auth
-- -----------------------------------------------------------------------------
CREATE TABLE auth_users (
    id             TEXT    PRIMARY KEY,
    username       TEXT    NOT NULL UNIQUE,
    password       TEXT    NOT NULL,
    email          TEXT,
    token_version  INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at     INTEGER
);
CREATE INDEX idx_auth_users_username ON auth_users(username);

-- -----------------------------------------------------------------------------
-- Songs catalog (FK target for queue_entries, queue_user_songs, playlist_songs)
-- -----------------------------------------------------------------------------
CREATE TABLE queue_songs (
    id        TEXT    PRIMARY KEY,
    title     TEXT,
    artist    TEXT,
    album     TEXT,
    genre     TEXT,
    duration  INTEGER,
    url       TEXT
);

-- -----------------------------------------------------------------------------
-- Queue
-- -----------------------------------------------------------------------------
CREATE TABLE queue_entries (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES auth_users(id)  ON DELETE CASCADE,
    song_id    TEXT    NOT NULL REFERENCES queue_songs(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE (user_id, song_id)
);
CREATE INDEX idx_queue_entries_user_position ON queue_entries(user_id, position);

CREATE TABLE queue_user_songs (
    user_id           TEXT    NOT NULL REFERENCES auth_users(id)  ON DELETE CASCADE,
    song_id           TEXT    NOT NULL REFERENCES queue_songs(id) ON DELETE CASCADE,
    listen_count      INTEGER NOT NULL DEFAULT 0,
    first_listened_at INTEGER,
    last_listened_at  INTEGER,
    liked             INTEGER NOT NULL DEFAULT 0,
    skipped           INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, song_id)
);

-- -----------------------------------------------------------------------------
-- Playlists
-- -----------------------------------------------------------------------------
CREATE TABLE playlist_playlists (
    id               TEXT    PRIMARY KEY,
    user_id          TEXT    NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
    name             TEXT    NOT NULL,
    description      TEXT,
    is_system_liked  INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at       INTEGER
);
CREATE INDEX idx_playlists_user ON playlist_playlists(user_id);

-- At most one system-liked playlist per user. Partial unique index (SQLite 3.8+).
CREATE UNIQUE INDEX uniq_system_liked_per_user
    ON playlist_playlists(user_id) WHERE is_system_liked = 1;

CREATE TABLE playlist_songs (
    playlist_id TEXT    NOT NULL REFERENCES playlist_playlists(id) ON DELETE CASCADE,
    song_id     TEXT    NOT NULL REFERENCES queue_songs(id)        ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    added_at    INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (playlist_id, song_id)
);
CREATE INDEX idx_playlist_songs_position ON playlist_songs(playlist_id, position);
