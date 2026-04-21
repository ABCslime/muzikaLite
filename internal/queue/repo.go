package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macabc/muzika/internal/db"
)

var (
	ErrNotFound  = errors.New("queue: not found")
	ErrDuplicate = errors.New("queue: duplicate")
)

// Repo persists queue_entries, queue_songs, queue_user_songs.
type Repo struct{ db *sql.DB }

func NewRepo(sqlDB *sql.DB) *Repo { return &Repo{db: sqlDB} }

// ListEntries returns all queue entries for userID ordered by position.
// The Relaxed flag (v0.4 PR 3) is filled from queue_entries.relaxed.
func (r *Repo) ListEntries(ctx context.Context, userID uuid.UUID) ([]QueueEntry, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, song_id, position, created_at, relaxed
		 FROM queue_entries WHERE user_id = ? ORDER BY position`,
		userID.String())
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()
	var out []QueueEntry
	for rows.Next() {
		var (
			idStr, uidStr, sidStr string
			pos                   int
			createdAt             int64
			relaxed               int
		)
		if err := rows.Scan(&idStr, &uidStr, &sidStr, &pos, &createdAt, &relaxed); err != nil {
			return nil, err
		}
		id, _ := uuid.Parse(idStr)
		uid, _ := uuid.Parse(uidStr)
		sid, _ := uuid.Parse(sidStr)
		out = append(out, QueueEntry{
			ID: id, UserID: uid, SongID: sid, Position: pos,
			CreatedAt: time.Unix(createdAt, 0).UTC(),
			Relaxed:   relaxed != 0,
		})
	}
	return out, rows.Err()
}

// CountEntries returns how many songs are queued for userID.
func (r *Repo) CountEntries(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM queue_entries WHERE user_id = ?`,
		userID.String()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count entries: %w", err)
	}
	return n, nil
}

// InsertEntry adds a song to the queue. Caller holds the per-user mutex.
func (r *Repo) InsertEntry(ctx context.Context, e QueueEntry) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO queue_entries (id, user_id, song_id, position)
		 VALUES (?, ?, ?, ?)`,
		e.ID.String(), e.UserID.String(), e.SongID.String(), e.Position)
	if err != nil {
		if db.IsUniqueErr(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("insert entry: %w", err)
	}
	return nil
}

// AppendEntry computes MAX(position)+1 then inserts. Caller holds per-user mutex.
// Relaxed=false; use AppendEntryRelaxed when the download worker's relaxed
// pass produced this entry.
func (r *Repo) AppendEntry(ctx context.Context, userID, songID uuid.UUID) error {
	return r.appendEntry(ctx, userID, songID, false)
}

// AppendEntryRelaxed is AppendEntry + sets relaxed=1. v0.4 PR 3 calls this
// only when the originating DiscoveryIntent was user-initiated (Strategy=
// StrategySearch). Passive refill always uses AppendEntry so the flag stays
// 0 per ROADMAP §v0.4 item 6 ("Passive refill relaxes silently.").
func (r *Repo) AppendEntryRelaxed(ctx context.Context, userID, songID uuid.UUID) error {
	return r.appendEntry(ctx, userID, songID, true)
}

func (r *Repo) appendEntry(ctx context.Context, userID, songID uuid.UUID, relaxed bool) error {
	var maxPos sql.NullInt64
	if err := r.db.QueryRowContext(ctx,
		`SELECT MAX(position) FROM queue_entries WHERE user_id = ?`,
		userID.String()).Scan(&maxPos); err != nil {
		return fmt.Errorf("max position: %w", err)
	}
	next := 0
	if maxPos.Valid {
		next = int(maxPos.Int64) + 1
	}
	relaxedVal := 0
	if relaxed {
		relaxedVal = 1
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO queue_entries (id, user_id, song_id, position, relaxed)
		 VALUES (?, ?, ?, ?, ?)`,
		uuid.New().String(), userID.String(), songID.String(), next, relaxedVal)
	if err != nil {
		if db.IsUniqueErr(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("insert entry: %w", err)
	}
	return nil
}

// RemoveEntry deletes a queue entry. Caller holds the per-user mutex.
func (r *Repo) RemoveEntry(ctx context.Context, userID, songID uuid.UUID) error {
	return removeEntryExec(ctx, r.db, userID, songID)
}

// RemoveEntryTx is the tx-scoped variant used by MarkSkipped/MarkFinished to
// pair the queue-entry delete with the listen-stat upsert atomically.
func (r *Repo) RemoveEntryTx(ctx context.Context, tx *sql.Tx, userID, songID uuid.UUID) error {
	return removeEntryExec(ctx, tx, userID, songID)
}

// execer is the common subset of *sql.DB / *sql.Tx we need. Factored so the
// query lives in one place.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func removeEntryExec(ctx context.Context, e execer, userID, songID uuid.UUID) error {
	res, err := e.ExecContext(ctx,
		`DELETE FROM queue_entries WHERE user_id = ? AND song_id = ?`,
		userID.String(), songID.String())
	if err != nil {
		return fmt.Errorf("remove entry: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetSong loads a song by ID.
func (r *Repo) GetSong(ctx context.Context, id uuid.UUID) (Song, error) {
	var (
		idStr                 string
		title, artist, album  sql.NullString
		genre, url            sql.NullString
		duration              sql.NullInt64
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT id, title, artist, album, genre, duration, url
		 FROM queue_songs WHERE id = ?`, id.String()).Scan(
		&idStr, &title, &artist, &album, &genre, &duration, &url)
	if errors.Is(err, sql.ErrNoRows) {
		return Song{}, ErrNotFound
	}
	if err != nil {
		return Song{}, fmt.Errorf("get song: %w", err)
	}
	sid, _ := uuid.Parse(idStr)
	s := Song{ID: sid}
	if title.Valid {
		s.Title = title.String
	}
	if artist.Valid {
		s.Artist = artist.String
	}
	if album.Valid {
		s.Album = album.String
	}
	if genre.Valid {
		s.Genre = genre.String
	}
	if duration.Valid {
		s.Duration = int(duration.Int64)
	}
	if url.Valid {
		s.URL = url.String
	}
	return s, nil
}

// InsertSongStub creates a placeholder song row the refiller can reference.
// Metadata is filled in later by onRequestDownload; url by onLoadedSong.
//
// requestingUserID is stamped on the stub so onLoadedSong knows which user's
// queue to append to when the download completes. Passing uuid.Nil stores
// NULL (used by tests and by any non-refiller path that doesn't have a
// requester).
func (r *Repo) InsertSongStub(ctx context.Context, id uuid.UUID, genre string, requestingUserID uuid.UUID) error {
	var requester any
	if requestingUserID != uuid.Nil {
		requester = requestingUserID.String()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO queue_songs (id, genre, requesting_user_id) VALUES (?, ?, ?)`,
		id.String(), nullString(genre), requester)
	if err != nil {
		return fmt.Errorf("insert stub: %w", err)
	}
	return nil
}

// GetSongRequester returns the requesting_user_id for a stub. The bool is
// false when the column is NULL (pre-migration rows or legacy inserts).
func (r *Repo) GetSongRequester(ctx context.Context, songID uuid.UUID) (uuid.UUID, bool, error) {
	var reqStr sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT requesting_user_id FROM queue_songs WHERE id = ?`,
		songID.String()).Scan(&reqStr)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("get requester: %w", err)
	}
	if !reqStr.Valid {
		return uuid.Nil, false, nil
	}
	uid, err := uuid.Parse(reqStr.String)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("parse requester: %w", err)
	}
	return uid, true, nil
}

// UpdateSongMetadata updates title/artist. Called from onRequestDownload.
func (r *Repo) UpdateSongMetadata(ctx context.Context, id uuid.UUID, title, artist string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE queue_songs SET title = ?, artist = ? WHERE id = ?`,
		nullString(title), nullString(artist), id.String())
	if err != nil {
		return fmt.Errorf("update metadata: %w", err)
	}
	return nil
}

// UpdateSongFile sets the URL (filesystem path) after the download worker reports success.
func (r *Repo) UpdateSongFile(ctx context.Context, id uuid.UUID, filePath string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE queue_songs SET url = ? WHERE id = ?`,
		filePath, id.String())
	if err != nil {
		return fmt.Errorf("update url: %w", err)
	}
	return nil
}

// DeleteSong removes a song (cascades to queue_entries, user_songs, playlist_songs).
func (r *Repo) DeleteSong(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM queue_songs WHERE id = ?`, id.String())
	if err != nil {
		return fmt.Errorf("delete song: %w", err)
	}
	return nil
}

// IncrementListenCount marks a finished play for (userID, songID).
func (r *Repo) IncrementListenCount(ctx context.Context, userID, songID uuid.UUID) error {
	return incrementListenCountExec(ctx, r.db, userID, songID)
}

// IncrementListenCountTx is the tx-scoped variant used by MarkFinished so the
// listen-count upsert and the queue-entry delete land together or not at all.
func (r *Repo) IncrementListenCountTx(ctx context.Context, tx *sql.Tx, userID, songID uuid.UUID) error {
	return incrementListenCountExec(ctx, tx, userID, songID)
}

func incrementListenCountExec(ctx context.Context, e execer, userID, songID uuid.UUID) error {
	_, err := e.ExecContext(ctx,
		`INSERT INTO queue_user_songs (user_id, song_id, listen_count, first_listened_at, last_listened_at)
		 VALUES (?, ?, 1, unixepoch(), unixepoch())
		 ON CONFLICT(user_id, song_id) DO UPDATE SET
			listen_count = queue_user_songs.listen_count + 1,
			last_listened_at = unixepoch()`,
		userID.String(), songID.String())
	if err != nil {
		return fmt.Errorf("increment listen_count: %w", err)
	}
	return nil
}

// MarkSkipped sets the skipped flag.
func (r *Repo) MarkSkipped(ctx context.Context, userID, songID uuid.UUID) error {
	return markSkippedExec(ctx, r.db, userID, songID)
}

// MarkSkippedTx is the tx-scoped variant — pairs with RemoveEntryTx in MarkSkipped.
func (r *Repo) MarkSkippedTx(ctx context.Context, tx *sql.Tx, userID, songID uuid.UUID) error {
	return markSkippedExec(ctx, tx, userID, songID)
}

func markSkippedExec(ctx context.Context, e execer, userID, songID uuid.UUID) error {
	_, err := e.ExecContext(ctx,
		`INSERT INTO queue_user_songs (user_id, song_id, skipped)
		 VALUES (?, ?, 1)
		 ON CONFLICT(user_id, song_id) DO UPDATE SET skipped = 1`,
		userID.String(), songID.String())
	if err != nil {
		return fmt.Errorf("mark skipped: %w", err)
	}
	return nil
}

// SetLiked toggles the liked flag.
func (r *Repo) SetLiked(ctx context.Context, userID, songID uuid.UUID, liked bool) error {
	val := 0
	if liked {
		val = 1
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO queue_user_songs (user_id, song_id, liked)
		 VALUES (?, ?, ?)
		 ON CONFLICT(user_id, song_id) DO UPDATE SET liked = excluded.liked`,
		userID.String(), songID.String(), val)
	if err != nil {
		return fmt.Errorf("set liked: %w", err)
	}
	return nil
}

// GetLiked reads the liked flag. Returns false (no error) if row doesn't exist.
func (r *Repo) GetLiked(ctx context.Context, userID, songID uuid.UUID) (bool, error) {
	var liked int
	err := r.db.QueryRowContext(ctx,
		`SELECT liked FROM queue_user_songs WHERE user_id = ? AND song_id = ?`,
		userID.String(), songID.String()).Scan(&liked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get liked: %w", err)
	}
	return liked != 0, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
