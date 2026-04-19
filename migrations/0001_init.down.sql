DROP INDEX IF EXISTS idx_playlist_songs_position;
DROP TABLE IF EXISTS playlist_songs;

DROP INDEX IF EXISTS uniq_system_liked_per_user;
DROP INDEX IF EXISTS idx_playlists_user;
DROP TABLE IF EXISTS playlist_playlists;

DROP TABLE IF EXISTS queue_user_songs;

DROP INDEX IF EXISTS idx_queue_entries_user_position;
DROP TABLE IF EXISTS queue_entries;

DROP TABLE IF EXISTS queue_songs;

DROP INDEX IF EXISTS idx_auth_users_username;
DROP TABLE IF EXISTS auth_users;

DROP INDEX IF EXISTS idx_outbox_created;
DROP TABLE IF EXISTS outbox;
