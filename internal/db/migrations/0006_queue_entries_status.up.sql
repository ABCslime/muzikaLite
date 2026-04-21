-- v0.4.1 PR B — search availability probe.
--
-- queue_entries.status distinguishes:
--   'probing' — the search seeder picked (artist, title) and inserted the
--               entry, but the download worker hasn't confirmed Soulseek
--               has peers for the track yet. UI renders it with a spinner
--               and a disabled play button.
--   'ready'   — the file is downloaded and playable.
--
-- Only search-initiated entries ever go through 'probing'. Passive refill
-- inserts entries straight at 'ready' (default) on LoadedSong{Completed}
-- because the user isn't watching — ROADMAP §v0.4 item 6 keeps the
-- passive path silent.

ALTER TABLE queue_entries
    ADD COLUMN status TEXT NOT NULL DEFAULT 'ready';

-- Index helps the "count user's probing entries" query (not used in v0.4.1
-- but the shape of v0.5's similarity engine will want it).
CREATE INDEX idx_queue_entries_user_status ON queue_entries(user_id, status);
