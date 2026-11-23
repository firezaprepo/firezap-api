package store

import (
	"context"
	"time"
)

// Sending limits that span days (see instance/daily.go) need two small things to survive a
// restart: how much each instance sent today, and which private chats each number has already
// talked to. Neither keeps message content.
const pacingSchema = `
CREATE TABLE IF NOT EXISTS fz_send_daily (
	instance_id TEXT NOT NULL REFERENCES fz_instances(id) ON DELETE CASCADE,
	day         DATE NOT NULL,
	sent        INTEGER NOT NULL DEFAULT 0,
	new_chats   INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (instance_id, day)
);
CREATE TABLE IF NOT EXISTS fz_known_chats (
	instance_id TEXT NOT NULL REFERENCES fz_instances(id) ON DELETE CASCADE,
	chat        TEXT NOT NULL,
	first_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY (instance_id, chat)
);
`

// dayKey is the calendar day of t in its own location, as PostgreSQL DATE text.
func dayKey(t time.Time) string { return t.Format("2006-01-02") }

// Daily returns what an instance sent on the day of t.
func (s *Store) Daily(ctx context.Context, id string, t time.Time) (sent, newChats int, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(sent), 0), COALESCE(SUM(new_chats), 0) FROM fz_send_daily WHERE instance_id = $1 AND day = $2`,
		id, dayKey(t)).Scan(&sent, &newChats)
	return sent, newChats, err
}

// AddDaily adds to an instance's counters for the day of t, and forgets days older than a
// week: only today is ever read.
func (s *Store) AddDaily(ctx context.Context, id string, t time.Time, sent, newChats int) error {
	day := dayKey(t)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO fz_send_daily (instance_id, day, sent, new_chats) VALUES ($1, $2, $3, $4)
		ON CONFLICT (instance_id, day) DO UPDATE
		SET sent = fz_send_daily.sent + EXCLUDED.sent, new_chats = fz_send_daily.new_chats + EXCLUDED.new_chats`,
		id, day, sent, newChats); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM fz_send_daily WHERE instance_id = $1 AND day < $2::date - 7`, id, day)
	return err
}

// KnownChat reports whether the number of an instance already talked to any of the chats
// (the same person may be known by phone number or by LID).
func (s *Store) KnownChat(ctx context.Context, id string, chats ...string) (bool, error) {
	if len(chats) == 0 {
		return false, nil
	}
	var known bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM fz_known_chats WHERE instance_id = $1 AND chat = ANY($2))`,
		id, chats).Scan(&known)
	return known, err
}

// MarkChats records chats the number of an instance has talked to. Already known ones are
// left as they are.
func (s *Store) MarkChats(ctx context.Context, id string, chats ...string) error {
	if len(chats) == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO fz_known_chats (instance_id, chat) SELECT $1, unnest($2::text[])
		ON CONFLICT DO NOTHING`, id, chats)
	return err
}
