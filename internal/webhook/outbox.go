package webhook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Item is an event waiting in the outbox, with where it goes now.
type Item struct {
	ID       int64
	Instance string
	Event    string
	Body     []byte
	Attempts int
	Target   Target
}

// Counts is how many events of an instance wait for delivery and how many gave up.
type Counts struct {
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
}

// Failure is an event that ran out of attempts. Its body stays on the server.
type Failure struct {
	ID        int64      `json:"id"`
	EventID   string     `json:"eventId"`
	Event     string     `json:"event"`
	Attempts  int        `json:"attempts"`
	LastError string     `json:"lastError"`
	CreatedAt time.Time  `json:"createdAt"`
	FailedAt  *time.Time `json:"failedAt"`
}

// Queue keeps events until their webhook accepts them (store.Outbox in production).
type Queue interface {
	Enqueue(ctx context.Context, instanceID, eventID, event string, body []byte) error
	Claim(ctx context.Context, limit int, lease time.Duration) ([]Item, error)
	Delivered(ctx context.Context, id int64) error
	Retry(ctx context.Context, id int64, attempts int, at time.Time, lastErr string) error
	Fail(ctx context.Context, id int64, attempts int, lastErr string, keep bool) error
	Purge(ctx context.Context, retention time.Duration) (int64, error)
}

// Retries are the waits before each new attempt after a failed one: about 1 h 40 min in all,
// enough for a receiver to come back from a deploy or a short outage.
var Retries = []time.Duration{
	5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour,
}

const (
	claimBatch = 20
	workers    = 4
	pollEvery  = 2 * time.Second
	purgeEvery = time.Hour
)

// newEventID returns a random id for an event, so receivers can drop a repeated delivery.
// Delivery is at least once: an event whose answer was lost in a crash is sent again.
func newEventID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Run delivers the outbox until ctx ends. Without a queue it returns at once (events then go
// straight out, as before the outbox).
func (d *Dispatcher) Run(ctx context.Context) {
	if d.queue == nil {
		return
	}
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	lastPurge := time.Time{}
	sem := make(chan struct{}, workers)
	for {
		if time.Since(lastPurge) >= purgeEvery {
			lastPurge = time.Now()
			if d.retention > 0 {
				if n, err := d.queue.Purge(ctx, d.retention); err != nil {
					d.log.Error("webhook: purge failed events", "error", err)
				} else if n > 0 {
					d.log.Info("webhook: dropped old failed events", "count", n)
				}
			}
		}
		d.drain(ctx, sem)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-d.wake:
		}
	}
}

// drain delivers every due event, a few at a time.
func (d *Dispatcher) drain(ctx context.Context, sem chan struct{}) {
	for ctx.Err() == nil {
		items, err := d.queue.Claim(ctx, claimBatch, d.lease())
		if err != nil {
			d.log.Error("webhook: read the outbox", "error", err)
			return
		}
		if len(items) == 0 {
			return
		}
		done := make(chan struct{}, len(items))
		for _, it := range items {
			sem <- struct{}{}
			go func(it Item) {
				defer func() { <-sem; done <- struct{}{} }()
				d.attempt(ctx, it)
			}(it)
		}
		for range items {
			<-done
		}
	}
}

// retries returns the outbox retry waits (tests shorten them).
func (d *Dispatcher) retries() []time.Duration {
	if d.outboxRetries != nil {
		return d.outboxRetries
	}
	return Retries
}

// lease is how long a claimed event stays hidden: longer than one delivery can take.
func (d *Dispatcher) lease() time.Duration { return 2*d.client.Timeout + 30*time.Second }

// attempt delivers one event and records the outcome.
func (d *Dispatcher) attempt(ctx context.Context, it Item) {
	// The webhook was removed after the event was written: there is nowhere to send it.
	if it.Target.URL == "" {
		if err := d.queue.Delivered(ctx, it.ID); err != nil {
			d.log.Error("webhook: drop event without webhook", "id", it.ID, "error", err)
		}
		return
	}
	err := d.post(it.Target, it.Body)
	if err == nil {
		if err := d.queue.Delivered(ctx, it.ID); err != nil {
			// It will be sent again when the lease ends; receivers dedupe by event id.
			d.log.Error("webhook: delivered but not removed from the outbox", "id", it.ID, "error", err)
		}
		return
	}
	attempts := it.Attempts + 1
	retries := d.retries()
	if attempts > len(retries) {
		d.log.Warn("webhook: delivery failed, giving up", "instance", it.Instance, "event", it.Event,
			"attempts", attempts, "error", err)
		if ferr := d.queue.Fail(ctx, it.ID, attempts, err.Error(), d.retention > 0); ferr != nil {
			d.log.Error("webhook: mark event as failed", "id", it.ID, "error", ferr)
		}
		return
	}
	if rerr := d.queue.Retry(ctx, it.ID, attempts, time.Now().Add(retries[attempts-1]), err.Error()); rerr != nil {
		d.log.Error("webhook: schedule retry", "id", it.ID, "error", rerr)
	}
}
