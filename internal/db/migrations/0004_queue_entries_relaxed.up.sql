-- v0.4 PR 3: surface gate-relaxation to user-initiated search.
--
-- ROADMAP §v0.4 item 6: "User-initiated search surfaces the relaxation
-- ('no high-quality matches; showing best available'). Never ship 128 kbps
-- tracks without the user knowing."
--
-- A stub whose acquisition required the relaxed gate pass gets relaxed=1
-- on its queue_entries row. The GET /api/queue DTO exposes this as a
-- per-entry flag; the frontend renders a one-shot toast the first time
-- the user sees it.
--
-- Default 0: passive refill's relax path silently lands relaxed=0 because
-- queue.onLoadedSong only sets the flag when the upstream origin warrants
-- surfacing. See queue/service.go.

ALTER TABLE queue_entries
    ADD COLUMN relaxed INTEGER NOT NULL DEFAULT 0;
