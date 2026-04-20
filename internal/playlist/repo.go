package playlist

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
	ErrNotFound = errors.New("playlist: not found")
	// ErrForbidden is reserved for an authorized caller hitting a protected
	// resource they legitimately cannot mutate — currently only deleting the
	// system-liked playlist. Cross-user access uses ErrNotFound so ID
	// enumeration yields 404 instead of 403.
	ErrForbidden = errors.New("playlist: operation not allowed")
	ErrDuplicate = errors.New("playlist: duplicate")
)

// Repo persists playlists and their song lists.
type Repo struct{ db *sql.DB }

// NewRepo constructs a Repo.
func NewRepo(sqlDB *sql.DB) *Repo { return &Repo{db: sqlDB} }

// ListByUser returns every playlist owned by userID, newest first.
func (r *Repo) ListByUser(ctx context.Context, userID uuid.UUID) ([]Playlist, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, name, description, is_system_liked, created_at, updated_at
		 FROM playlist_playlists
		 WHERE user_id = ?
		 ORDER BY created_at DESC, id`,
		userID.String())
	if err != nil {
		return nil, fmt.Errorf("query playlists: %w", err)
	}
	defer rows.Close()
	var out []Playlist
	for rows.Next() {
		p, err := scanPlaylist(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get returns a playlist by ID. Caller checks ownership against returned UserID.
func (r *Repo) Get(ctx context.Context, playlistID uuid.UUID) (Playlist, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, name, description, is_system_liked, created_at, updated_at
		 FROM playlist_playlists WHERE id = ?`,
		playlistID.String())
	return scanPlaylist(row)
}

// Create inserts a playlist row.
//
// created_at: when p.CreatedAt is non-zero we write it verbatim so the
// service layer's clock reading is authoritative and the caller can skip a
// post-commit read-back. When zero we fall back to the column's DEFAULT
// (unixepoch()) so in-repo callers (tests, GetOrCreateSystemLiked) stay
// unchanged.
func (r *Repo) Create(ctx context.Context, p Playlist) error {
	var err error
	if p.CreatedAt.IsZero() {
		_, err = r.db.ExecContext(ctx,
			`INSERT INTO playlist_playlists (id, user_id, name, description, is_system_liked)
			 VALUES (?, ?, ?, ?, ?)`,
			p.ID.String(), p.UserID.String(), p.Name, nullString(p.Description), boolInt(p.IsSystemLiked))
	} else {
		_, err = r.db.ExecContext(ctx,
			`INSERT INTO playlist_playlists (id, user_id, name, description, is_system_liked, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			p.ID.String(), p.UserID.String(), p.Name, nullString(p.Description), boolInt(p.IsSystemLiked), p.CreatedAt.Unix())
	}
	if err != nil {
		if db.IsUniqueErr(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("insert playlist: %w", err)
	}
	return nil
}

// Delete removes a playlist (and cascades to playlist_songs via FK).
func (r *Repo) Delete(ctx context.Context, playlistID uuid.UUID) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM playlist_playlists WHERE id = ?`, playlistID.String())
	if err != nil {
		return fmt.Errorf("delete playlist: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddSong appends a song to a playlist at the next position.
// SetMaxOpenConns(1) on the pool serializes writes so the MAX(position)+1
// read-then-write is race-free.
func (r *Repo) AddSong(ctx context.Context, playlistID, songID uuid.UUID) error {
	var maxPos sql.NullInt64
	err := r.db.QueryRowContext(ctx,
		`SELECT MAX(position) FROM playlist_songs WHERE playlist_id = ?`,
		playlistID.String()).Scan(&maxPos)
	if err != nil {
		return fmt.Errorf("max position: %w", err)
	}
	next := 0
	if maxPos.Valid {
		next = int(maxPos.Int64) + 1
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO playlist_songs (playlist_id, song_id, position)
		 VALUES (?, ?, ?)`,
		playlistID.String(), songID.String(), next)
	if err != nil {
		if db.IsUniqueErr(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("insert playlist_songs: %w", err)
	}
	return nil
}

// RemoveSong deletes a playlist_songs row.
func (r *Repo) RemoveSong(ctx context.Context, playlistID, songID uuid.UUID) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM playlist_songs WHERE playlist_id = ? AND song_id = ?`,
		playlistID.String(), songID.String())
	if err != nil {
		return fmt.Errorf("delete playlist_songs: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetOrCreateSystemLiked returns the user's system-liked playlist, creating
// it if absent. Uses the partial-unique index uniq_system_liked_per_user to
// tolerate concurrent create attempts.
func (r *Repo) GetOrCreateSystemLiked(ctx context.Context, userID uuid.UUID) (Playlist, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, name, description, is_system_liked, created_at, updated_at
		 FROM playlist_playlists
		 WHERE user_id = ? AND is_system_liked = 1`,
		userID.String())
	p, err := scanPlaylist(row)
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Playlist{}, err
	}
	newP := Playlist{
		ID:            uuid.New(),
		UserID:        userID,
		Name:          "Liked",
		IsSystemLiked: true,
	}
	if err := r.Create(ctx, newP); err != nil {
		if errors.Is(err, ErrDuplicate) {
			// Race: another caller created it — reload and return.
			row := r.db.QueryRowContext(ctx,
				`SELECT id, user_id, name, description, is_system_liked, created_at, updated_at
				 FROM playlist_playlists WHERE user_id = ? AND is_system_liked = 1`,
				userID.String())
			return scanPlaylist(row)
		}
		return Playlist{}, err
	}
	return r.Get(ctx, newP.ID)
}

// Songs returns the songs in a playlist ordered by position.
func (r *Repo) Songs(ctx context.Context, playlistID uuid.UUID) ([]PlaylistSong, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT playlist_id, song_id, position, added_at
		 FROM playlist_songs
		 WHERE playlist_id = ?
		 ORDER BY position`,
		playlistID.String())
	if err != nil {
		return nil, fmt.Errorf("query playlist_songs: %w", err)
	}
	defer rows.Close()
	var out []PlaylistSong
	for rows.Next() {
		var (
			plID, sID string
			pos       int
			addedAt   int64
		)
		if err := rows.Scan(&plID, &sID, &pos, &addedAt); err != nil {
			return nil, err
		}
		plUUID, _ := uuid.Parse(plID)
		sUUID, _ := uuid.Parse(sID)
		out = append(out, PlaylistSong{
			PlaylistID: plUUID,
			SongID:     sUUID,
			Position:   pos,
			AddedAt:    time.Unix(addedAt, 0).UTC(),
		})
	}
	return out, rows.Err()
}

// --- scanning helpers ---

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPlaylist(r rowScanner) (Playlist, error) {
	var (
		idStr, userStr string
		name           string
		description    sql.NullString
		isLiked        int
		createdAt      int64
		updatedAt      sql.NullInt64
	)
	err := r.Scan(&idStr, &userStr, &name, &description, &isLiked, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, ErrNotFound
	}
	if err != nil {
		return Playlist{}, fmt.Errorf("scan playlist: %w", err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return Playlist{}, fmt.Errorf("parse id: %w", err)
	}
	uid, err := uuid.Parse(userStr)
	if err != nil {
		return Playlist{}, fmt.Errorf("parse user_id: %w", err)
	}
	p := Playlist{
		ID:            id,
		UserID:        uid,
		Name:          name,
		IsSystemLiked: isLiked != 0,
		CreatedAt:     time.Unix(createdAt, 0).UTC(),
	}
	if description.Valid {
		p.Description = description.String
	}
	if updatedAt.Valid {
		p.UpdatedAt = time.Unix(updatedAt.Int64, 0).UTC()
	}
	return p, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
