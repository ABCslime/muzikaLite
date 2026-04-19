package queue

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrNotFound  = errors.New("queue: not found")
	ErrDuplicate = errors.New("queue: duplicate")
)

// Repo persists queue_entries, queue_songs, queue_user_songs.
type Repo struct{ db *sql.DB }

func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

// ListEntries returns all queue entries for userID ordered by position.
// TODO(port): Phase 6.
func (r *Repo) ListEntries(ctx context.Context, userID uuid.UUID) ([]QueueEntry, error) {
	return nil, errors.New("queue.Repo.ListEntries: not implemented")
}

// CountEntries returns how many songs are queued for userID. Used by the refiller.
// TODO(port): Phase 6.
func (r *Repo) CountEntries(ctx context.Context, userID uuid.UUID) (int, error) {
	return 0, errors.New("queue.Repo.CountEntries: not implemented")
}

// InsertEntry adds a song at a position. Must be called inside the per-user mutex.
// TODO(port): Phase 6.
func (r *Repo) InsertEntry(ctx context.Context, e QueueEntry) error {
	return errors.New("queue.Repo.InsertEntry: not implemented")
}

// RemoveEntry deletes a queue entry. Must be called inside the per-user mutex.
// TODO(port): Phase 6.
func (r *Repo) RemoveEntry(ctx context.Context, userID, songID uuid.UUID) error {
	return errors.New("queue.Repo.RemoveEntry: not implemented")
}

// GetSong loads a song by ID. TODO(port): Phase 6.
func (r *Repo) GetSong(ctx context.Context, id uuid.UUID) (Song, error) {
	return Song{}, ErrNotFound
}

// InsertSongStub creates a row with only an ID, to be filled in later when
// Bandcamp/slskd return metadata. TODO(port): Phase 6.
func (r *Repo) InsertSongStub(ctx context.Context, id uuid.UUID, genre string) error {
	return errors.New("queue.Repo.InsertSongStub: not implemented")
}

// UpdateSongMetadata is called from the RequestSlskdSong consumer.
// TODO(port): Phase 6.
func (r *Repo) UpdateSongMetadata(ctx context.Context, id uuid.UUID, title, artist string) error {
	return errors.New("queue.Repo.UpdateSongMetadata: not implemented")
}

// UpdateSongFile is called from the LoadedSong consumer.
// TODO(port): Phase 6.
func (r *Repo) UpdateSongFile(ctx context.Context, id uuid.UUID, filePath string) error {
	return errors.New("queue.Repo.UpdateSongFile: not implemented")
}

// DeleteSong removes a song by ID (used on LoadedSong with status=error).
// TODO(port): Phase 6.
func (r *Repo) DeleteSong(ctx context.Context, id uuid.UUID) error {
	return errors.New("queue.Repo.DeleteSong: not implemented")
}

// IncrementListenCount records a finished play. TODO(port): Phase 6.
func (r *Repo) IncrementListenCount(ctx context.Context, userID, songID uuid.UUID) error {
	return errors.New("queue.Repo.IncrementListenCount: not implemented")
}

// MarkSkipped flips the skipped flag. TODO(port): Phase 6.
func (r *Repo) MarkSkipped(ctx context.Context, userID, songID uuid.UUID) error {
	return errors.New("queue.Repo.MarkSkipped: not implemented")
}

// SetLiked toggles the liked flag. TODO(port): Phase 6.
func (r *Repo) SetLiked(ctx context.Context, userID, songID uuid.UUID, liked bool) error {
	return errors.New("queue.Repo.SetLiked: not implemented")
}

// GetLiked reads the liked flag. TODO(port): Phase 6.
func (r *Repo) GetLiked(ctx context.Context, userID, songID uuid.UUID) (bool, error) {
	return false, ErrNotFound
}
