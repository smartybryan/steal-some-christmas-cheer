// Package store persists duck registrations and owner settings in SQLite.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Status is the moderation state of a registration.
type Status string

const (
	StatusPending       Status = "pending"        // waiting for the owner to vet it
	StatusApproved      Status = "approved"       // entry and photo are public (once the duck moves on)
	StatusPhotoRejected Status = "photo_rejected" // entry is public, photo is not
)

// ErrNotFound is returned when a registration does not exist.
var ErrNotFound = errors.New("not found")

// Registration is one stop on the duck's journey.
type Registration struct {
	ID             int64     `json:"id"`
	CreatedAt      time.Time `json:"createdAt"`
	Name           string    `json:"name"`
	Street         string    `json:"street"`
	City           string    `json:"city"`
	PublicLocation string    `json:"publicLocation"`
	Message        string    `json:"message"`
	Status         Status    `json:"status"`
	HasPhoto       bool      `json:"hasPhoto"`
}

// Settings are configured by the duck's owner. Everything except NotifyEmail
// is shown publicly; use Public to get the shareable subset.
type Settings struct {
	OwnerName     string `json:"ownerName"`
	HomeLocation  string `json:"homeLocation"`
	ReturnAddress string `json:"returnAddress"`
	ReturnBy      string `json:"returnBy"`
	Instructions  string `json:"instructions"`
	RevealAll     bool   `json:"revealAll"`
	NotifyEmail   string `json:"notifyEmail,omitempty"` // private: where new-registration alerts go
}

// Public returns the settings with private fields cleared.
func (s Settings) Public() Settings {
	s.NotifyEmail = ""
	return s
}

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS registrations (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at      INTEGER NOT NULL,
	name            TEXT NOT NULL,
	street          TEXT NOT NULL,
	city            TEXT NOT NULL,
	public_location TEXT NOT NULL,
	message         TEXT NOT NULL DEFAULT '',
	status          TEXT NOT NULL,
	photo           BLOB
);
CREATE TABLE IF NOT EXISTS settings (
	id   INTEGER PRIMARY KEY CHECK (id = 1),
	data TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS images (
	name       TEXT PRIMARY KEY,
	updated_at INTEGER NOT NULL,
	data       BLOB NOT NULL
);`

// HomePhotoName is the image shown at the start of the trail (Duck HQ).
const HomePhotoName = "home"

// Open opens (and if needed creates) the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite allows a single writer; keep it simple and safe.
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Create stores a new pending registration with its photo and returns its ID.
func (s *Store) Create(ctx context.Context, r Registration, photo []byte) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO registrations (created_at, name, street, city, public_location, message, status, photo)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.CreatedAt.Unix(), r.Name, r.Street, r.City, r.PublicLocation, r.Message, StatusPending, photo)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const selectColumns = `id, created_at, name, street, city, public_location, message, status, length(photo) > 0`

func scan(row interface{ Scan(...any) error }) (Registration, error) {
	var r Registration
	var created int64
	var hasPhoto sql.NullBool
	err := row.Scan(&r.ID, &created, &r.Name, &r.Street, &r.City, &r.PublicLocation, &r.Message, &r.Status, &hasPhoto)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	r.CreatedAt = time.Unix(created, 0).UTC()
	r.HasPhoto = hasPhoto.Bool
	return r, err
}

func (s *Store) query(ctx context.Context, q string, args ...any) ([]Registration, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Registration
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns one registration.
func (s *Store) Get(ctx context.Context, id int64) (Registration, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+selectColumns+` FROM registrations WHERE id = ?`, id))
}

// All returns every registration, newest first (for the owner).
func (s *Store) All(ctx context.Context) ([]Registration, error) {
	return s.query(ctx, `SELECT `+selectColumns+` FROM registrations ORDER BY id DESC`)
}

// visibleClause selects registrations the public may see. The newest
// registration (whatever its status) is always hidden so the duck's current
// location stays secret, unless the owner has chosen to reveal everything.
const visibleClause = `status IN ('approved', 'photo_rejected')
	AND (? OR id < (SELECT MAX(id) FROM registrations))`

// PublicTrail returns the stops the public may see, oldest first, along with
// the total number of times the duck has been stolen.
func (s *Store) PublicTrail(ctx context.Context, revealAll bool) ([]Registration, int, error) {
	trail, err := s.query(ctx, `SELECT `+selectColumns+` FROM registrations WHERE `+visibleClause+` ORDER BY id`, revealAll)
	if err != nil {
		return nil, 0, err
	}
	var total int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM registrations`).Scan(&total)
	return trail, total, err
}

// Photo returns the stored photo for a registration.
func (s *Store) Photo(ctx context.Context, id int64) ([]byte, error) {
	var photo []byte
	err := s.db.QueryRowContext(ctx, `SELECT photo FROM registrations WHERE id = ?`, id).Scan(&photo)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && len(photo) == 0) {
		return nil, ErrNotFound
	}
	return photo, err
}

// PublicPhoto returns a photo only if the public is allowed to see it.
func (s *Store) PublicPhoto(ctx context.Context, id int64, revealAll bool) ([]byte, error) {
	var photo []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT photo FROM registrations WHERE id = ? AND status = 'approved' AND `+visibleClause,
		id, revealAll).Scan(&photo)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && len(photo) == 0) {
		return nil, ErrNotFound
	}
	return photo, err
}

// Review sets the moderation status and public-facing fields of a registration.
func (s *Store) Review(ctx context.Context, id int64, status Status, name, publicLocation string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE registrations SET status = ?, name = ?, public_location = ? WHERE id = ?`,
		status, name, publicLocation, id)
	return mustAffect(res, err)
}

// Delete permanently removes a registration (for spam).
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM registrations WHERE id = ?`, id)
	return mustAffect(res, err)
}

func mustAffect(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Settings returns the owner settings (zero value if never saved).
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var settings Settings
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM settings WHERE id = 1`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	return settings, json.Unmarshal([]byte(data), &settings)
}

// SaveImage stores (or replaces) a named image.
func (s *Store) SaveImage(ctx context.Context, name string, data []byte, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO images (name, updated_at, data) VALUES (?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET updated_at = excluded.updated_at, data = excluded.data`,
		name, now.UnixNano(), data)
	return err
}

// Image returns a named image.
func (s *Store) Image(ctx context.Context, name string) ([]byte, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM images WHERE name = ?`, name).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return data, err
}

// ImageVersion returns a value that changes whenever the image changes, for
// cache-busting URLs. ok is false if the image doesn't exist.
func (s *Store) ImageVersion(ctx context.Context, name string) (version int64, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT updated_at FROM images WHERE name = ?`, name).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return version, err == nil, err
}

// DeleteImage removes a named image (no error if it doesn't exist).
func (s *Store) DeleteImage(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM images WHERE name = ?`, name)
	return err
}

// SaveSettings replaces the owner settings.
func (s *Store) SaveSettings(ctx context.Context, settings Settings) error {
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO settings (id, data) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, string(data))
	return err
}
