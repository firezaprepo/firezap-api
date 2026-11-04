// Package store keeps the instance records (name, API key hash, linked device, webhook).
//
// WhatsApp session data itself lives in the whatsmeow tables of the same database; this
// package only stores what FireZAP API adds on top of it.
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNotFound is returned when an instance does not exist.
var ErrNotFound = errors.New("instance not found")

// Instance is a WhatsApp number managed by the API.
type Instance struct {
	ID            string
	Name          string
	APIKeyHash    string
	DeviceJID     string // empty until the number is linked
	WebhookURL    string
	WebhookSecret string
	CreatedAt     time.Time
}

// Store reads and writes instance records.
type Store struct {
	db *sql.DB
}

// New wraps an open database connection.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

const schema = `
CREATE TABLE IF NOT EXISTS fz_instances (
	id             TEXT PRIMARY KEY,
	name           TEXT NOT NULL,
	api_key_hash   TEXT NOT NULL,
	device_jid     TEXT,
	webhook_url    TEXT NOT NULL DEFAULT '',
	webhook_secret TEXT NOT NULL DEFAULT '',
	created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS fz_instances_api_key_hash_idx ON fz_instances (api_key_hash);
`

// Migrate creates the tables this package needs. It is safe to run on every start.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

const columns = `id, name, api_key_hash, COALESCE(device_jid, ''), webhook_url, webhook_secret, created_at`

func scan(row interface{ Scan(...any) error }) (Instance, error) {
	var in Instance
	err := row.Scan(&in.ID, &in.Name, &in.APIKeyHash, &in.DeviceJID, &in.WebhookURL, &in.WebhookSecret, &in.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return in, ErrNotFound
	}
	return in, err
}

// Create inserts a new instance.
func (s *Store) Create(ctx context.Context, in Instance) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO fz_instances (id, name, api_key_hash) VALUES ($1, $2, $3)`,
		in.ID, in.Name, in.APIKeyHash)
	return err
}

// Get returns one instance.
func (s *Store) Get(ctx context.Context, id string) (Instance, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM fz_instances WHERE id = $1`, id))
}

// GetByKeyHash returns the instance that owns an API key.
func (s *Store) GetByKeyHash(ctx context.Context, hash string) (Instance, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM fz_instances WHERE api_key_hash = $1`, hash))
}

// List returns every instance, oldest first.
func (s *Store) List(ctx context.Context) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM fz_instances ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		in, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// exec runs an update that must touch exactly one instance.
func (s *Store) exec(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
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

// SetDevice records the WhatsApp device linked to the instance ("" clears it).
func (s *Store) SetDevice(ctx context.Context, id, jid string) error {
	var value any
	if jid != "" {
		value = jid
	}
	return s.exec(ctx, `UPDATE fz_instances SET device_jid = $2 WHERE id = $1`, id, value)
}

// SetWebhook stores where the instance sends its events.
func (s *Store) SetWebhook(ctx context.Context, id, url, secret string) error {
	return s.exec(ctx, `UPDATE fz_instances SET webhook_url = $2, webhook_secret = $3 WHERE id = $1`, id, url, secret)
}

// Delete removes the instance record.
func (s *Store) Delete(ctx context.Context, id string) error {
	return s.exec(ctx, `DELETE FROM fz_instances WHERE id = $1`, id)
}
