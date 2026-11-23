package store

import (
	"context"
	"time"

	"github.com/firezaprepo/firezap-api/internal/webhook"
)

// The webhook outbox (see webhook/outbox.go). An event is written here before it is sent and
// deleted as soon as the receiver accepts it, so a crash or a receiver that is down loses
// nothing. Events that run out of attempts stay as "failed" for WEBHOOK_FAILED_RETENTION_HOURS,
// to be sent again from the dashboard or the API, and are then deleted.
const outboxSchema = `
CREATE TABLE IF NOT EXISTS fz_webhook_outbox (
	id              BIGSERIAL PRIMARY KEY,
	instance_id     TEXT NOT NULL REFERENCES fz_instances(id) ON DELETE CASCADE,
	event_id        TEXT NOT NULL,
	event           TEXT NOT NULL,
	body            BYTEA NOT NULL,
	status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'failed')),
	attempts        INTEGER NOT NULL DEFAULT 0,
	next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	last_error      TEXT NOT NULL DEFAULT '',
	created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
	failed_at       TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS fz_webhook_outbox_due_idx ON fz_webhook_outbox (next_attempt_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS fz_webhook_outbox_instance_idx ON fz_webhook_outbox (instance_id, status);
`

// Outbox is the PostgreSQL side of webhook.Queue.
type Outbox struct{ s *Store }

// Outbox returns the webhook outbox kept in the same database.
func (s *Store) Outbox() *Outbox { return &Outbox{s: s} }

// Enqueue stores an event to be delivered.
func (o *Outbox) Enqueue(ctx context.Context, instanceID, eventID, event string, body []byte) error {
	_, err := o.s.db.ExecContext(ctx,
		`INSERT INTO fz_webhook_outbox (instance_id, event_id, event, body) VALUES ($1, $2, $3, $4)`,
		instanceID, eventID, event, body)
	return err
}

// Claim takes up to limit events that are due and hides them from other claims for lease, so
// a delivery that hangs or a crash in the middle makes the event due again instead of losing
// it. The webhook URL and secret are read now, not when the event was written: after the user
// fixes a wrong URL, the retries go to the right one.
func (o *Outbox) Claim(ctx context.Context, limit int, lease time.Duration) ([]webhook.Item, error) {
	rows, err := o.s.db.QueryContext(ctx, `
		WITH due AS (
			SELECT id FROM fz_webhook_outbox
			WHERE status = 'pending' AND next_attempt_at <= now()
			ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE fz_webhook_outbox o SET next_attempt_at = now() + make_interval(secs => $2)
			FROM due WHERE o.id = due.id
			RETURNING o.id, o.instance_id, o.event, o.body, o.attempts
		)
		SELECT c.id, c.instance_id, c.event, c.body, c.attempts, i.webhook_url, i.webhook_secret
		FROM claimed c JOIN fz_instances i ON i.id = c.instance_id
		ORDER BY c.id`, limit, lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []webhook.Item
	for rows.Next() {
		var it webhook.Item
		if err := rows.Scan(&it.ID, &it.Instance, &it.Event, &it.Body, &it.Attempts, &it.Target.URL, &it.Target.Secret); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Delivered removes an event the receiver accepted (or that has nowhere to go any more).
func (o *Outbox) Delivered(ctx context.Context, id int64) error {
	_, err := o.s.db.ExecContext(ctx, `DELETE FROM fz_webhook_outbox WHERE id = $1`, id)
	return err
}

// Retry schedules another attempt.
func (o *Outbox) Retry(ctx context.Context, id int64, attempts int, at time.Time, lastErr string) error {
	_, err := o.s.db.ExecContext(ctx,
		`UPDATE fz_webhook_outbox SET attempts = $2, next_attempt_at = $3, last_error = $4 WHERE id = $1`,
		id, attempts, at, lastErr)
	return err
}

// Fail marks an event that ran out of attempts. keep = false deletes it instead.
func (o *Outbox) Fail(ctx context.Context, id int64, attempts int, lastErr string, keep bool) error {
	if !keep {
		return o.Delivered(ctx, id)
	}
	_, err := o.s.db.ExecContext(ctx,
		`UPDATE fz_webhook_outbox SET status = 'failed', attempts = $2, last_error = $3, failed_at = now() WHERE id = $1`,
		id, attempts, lastErr)
	return err
}

// Purge deletes failed events older than the retention.
func (o *Outbox) Purge(ctx context.Context, retention time.Duration) (int64, error) {
	res, err := o.s.db.ExecContext(ctx,
		`DELETE FROM fz_webhook_outbox WHERE status = 'failed' AND failed_at < now() - make_interval(secs => $1)`,
		retention.Seconds())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Counts returns how many events wait and how many failed, per instance.
func (o *Outbox) Counts(ctx context.Context) (map[string]webhook.Counts, error) {
	rows, err := o.s.db.QueryContext(ctx, `
		SELECT instance_id,
		       COUNT(*) FILTER (WHERE status = 'pending'),
		       COUNT(*) FILTER (WHERE status = 'failed')
		FROM fz_webhook_outbox GROUP BY instance_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]webhook.Counts{}
	for rows.Next() {
		var id string
		var c webhook.Counts
		if err := rows.Scan(&id, &c.Pending, &c.Failed); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// Failed lists an instance's failed events, newest first. The body is left out: the list is
// for deciding what to do, and the content stays on the server.
func (o *Outbox) Failed(ctx context.Context, instanceID string, limit int) ([]webhook.Failure, error) {
	rows, err := o.s.db.QueryContext(ctx, `
		SELECT id, event_id, event, attempts, last_error, created_at, failed_at
		FROM fz_webhook_outbox WHERE instance_id = $1 AND status = 'failed'
		ORDER BY id DESC LIMIT $2`, instanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []webhook.Failure{}
	for rows.Next() {
		var f webhook.Failure
		if err := rows.Scan(&f.ID, &f.EventID, &f.Event, &f.Attempts, &f.LastError, &f.CreatedAt, &f.FailedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RetryFailed puts failed events of an instance back in line (all of them when ids is empty)
// with a fresh set of attempts. It returns how many.
func (o *Outbox) RetryFailed(ctx context.Context, instanceID string, ids []int64) (int64, error) {
	if ids == nil {
		ids = []int64{} // nil would be sent as NULL, and cardinality(NULL) matches nothing
	}
	res, err := o.s.db.ExecContext(ctx, `
		UPDATE fz_webhook_outbox SET status = 'pending', attempts = 0, next_attempt_at = now(), failed_at = NULL
		WHERE instance_id = $1 AND status = 'failed' AND (cardinality($2::bigint[]) = 0 OR id = ANY($2))`,
		instanceID, ids)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DiscardFailed deletes failed events of an instance (all of them when ids is empty).
func (o *Outbox) DiscardFailed(ctx context.Context, instanceID string, ids []int64) (int64, error) {
	if ids == nil {
		ids = []int64{} // nil would be sent as NULL, and cardinality(NULL) matches nothing
	}
	res, err := o.s.db.ExecContext(ctx, `
		DELETE FROM fz_webhook_outbox
		WHERE instance_id = $1 AND status = 'failed' AND (cardinality($2::bigint[]) = 0 OR id = ANY($2))`,
		instanceID, ids)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
