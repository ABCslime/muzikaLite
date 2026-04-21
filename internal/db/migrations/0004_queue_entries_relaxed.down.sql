-- SQLite 3.35+ supports ALTER TABLE ... DROP COLUMN. Raspberry Pi OS
-- Bookworm ships 3.40 and modernc.org/sqlite supports it; fine to rely on.
ALTER TABLE queue_entries DROP COLUMN relaxed;
