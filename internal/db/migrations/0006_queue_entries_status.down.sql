DROP INDEX IF EXISTS idx_queue_entries_user_status;
ALTER TABLE queue_entries DROP COLUMN status;
