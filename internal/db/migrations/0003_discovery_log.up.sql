-- v0.4 PR 2: discovery observability + Discogs response cache.
--
-- ROADMAP §v0.4 item 7 promises a discovery_log that captures every
-- attempt (source, strategy, rung, gate outcome, result). Never deleted —
-- historical data becomes valuable in v0.5 for per-user similarity training.
--
-- ROADMAP §v0.4 item 2 promises a 30-day Discogs response cache so we
-- never hit the Discogs API for data we've already fetched. The cache
-- table is ignored (but never deleted) by readers past 30 days; a
-- periodic sweep deletes expired rows.

-- -----------------------------------------------------------------------------
-- discovery_log
-- -----------------------------------------------------------------------------
CREATE TABLE discovery_log (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at   INTEGER NOT NULL DEFAULT (unixepoch()),

    -- Correlation keys. NULL when the log row doesn't correspond to a
    -- specific stub (e.g. a user-initiated search in v0.4 PR 3 before
    -- any queue_songs row is inserted).
    song_id      TEXT,
    user_id      TEXT,

    -- What/where.
    source       TEXT    NOT NULL,         -- "bandcamp" | "discogs" | "download"
    -- strategy is the DiscoveryIntent.Strategy that kicked off this lineage.
    -- NULL at download-stage rows (ladder/gate/picked/failed) because
    -- RequestDownload doesn't carry the originating strategy; aggregations
    -- that need it self-join on song_id to the seed-stage row. Populated
    -- for every seed-stage row.
    strategy     TEXT,
    stage        TEXT    NOT NULL,         -- "seed" | "ladder" | "gate" | "picked" | "failed"
    rung         INTEGER,                  -- 0 (catno), 1 (artist+title), 2 (title); NULL outside download ladder
    query        TEXT,                     -- query string used, if any

    -- Outcome.
    outcome      TEXT    NOT NULL,         -- "ok" | "no_results" | "rejected_strict" | "relaxed" | "error"
    reason       TEXT,                     -- free-form detail, e.g. "bitrate 96 < 192"
    result_count INTEGER,                  -- raw item/peer count seen at this stage

    -- Picked-file detail (stage="picked" only).
    filename     TEXT,
    peer         TEXT,
    bitrate      INTEGER,
    size         INTEGER
);

-- Primary query pattern is per-song forensics.
CREATE INDEX idx_discovery_log_song     ON discovery_log(song_id);
CREATE INDEX idx_discovery_log_created  ON discovery_log(created_at);
-- Aggregations like "rung 0 hit rate over the last N days" scan on these:
CREATE INDEX idx_discovery_log_src_strat ON discovery_log(source, strategy);

-- -----------------------------------------------------------------------------
-- discogs_cache
-- -----------------------------------------------------------------------------
CREATE TABLE discogs_cache (
    cache_key   TEXT    PRIMARY KEY,   -- e.g. "search:genre=electronic:page=3"
    payload     BLOB    NOT NULL,      -- raw JSON response body
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_discogs_cache_created ON discogs_cache(created_at);
