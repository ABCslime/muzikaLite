-- v0.4.3 PR B — album art for queue rows.
--
-- queue_songs gains image_url: the Discogs thumbnail URL surfaced at
-- acquire time. Populated by the search-acquire path in internal/queue
-- when a Discogs pick includes a Thumb; left NULL for songs added via
-- paths that can't infer cover art (Bandcamp tags path, manual entry).
--
-- Frontend consumers (SongItem, PlayerBar) already have <img> tags
-- with error-fallback to an SVG gradient; NULL / empty image_url just
-- trips that fallback. No existing row needs backfill — the column
-- defaults to NULL and the UI degrades gracefully.

ALTER TABLE queue_songs
    ADD COLUMN image_url TEXT;
