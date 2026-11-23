package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// memQueue is an in-memory Queue that behaves like store.Outbox.
type memQueue struct {
	mu         sync.Mutex
	next       int64
	items      map[int64]*memItem
	targets    map[string]Target
	enqueueErr error
}

type memItem struct {
	Item
	due    time.Time
	failed bool
	errMsg string
}

func newMemQueue(targets map[string]Target) *memQueue {
	return &memQueue{items: map[int64]*memItem{}, targets: targets}
}

func (q *memQueue) Enqueue(_ context.Context, inst, _, event string, body []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.enqueueErr != nil {
		return q.enqueueErr
	}
	q.next++
	q.items[q.next] = &memItem{Item: Item{ID: q.next, Instance: inst, Event: event, Body: body}, due: time.Now()}
	return nil
}

func (q *memQueue) Claim(_ context.Context, limit int, lease time.Duration) ([]Item, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Item
	for id := int64(1); id <= q.next && len(out) < limit; id++ {
		it, ok := q.items[id]
		if !ok || it.failed || it.due.After(time.Now()) {
			continue
		}
		it.due = time.Now().Add(lease)
		c := it.Item
		c.Target = q.targets[it.Instance]
		out = append(out, c)
	}
	return out, nil
}

func (q *memQueue) Delivered(_ context.Context, id int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.items, id)
	return nil
}

func (q *memQueue) Retry(_ context.Context, id int64, attempts int, at time.Time, lastErr string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	it := q.items[id]
	it.Attempts, it.due, it.errMsg = attempts, at, lastErr
	return nil
}

func (q *memQueue) Fail(_ context.Context, id int64, attempts int, lastErr string, keep bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !keep {
		delete(q.items, id)
		return nil
	}
	it := q.items[id]
	it.Attempts, it.failed, it.errMsg = attempts, true, lastErr
	return nil
}

func (q *memQueue) Purge(context.Context, time.Duration) (int64, error) { return 0, nil }

func (q *memQueue) snapshot() []memItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []memItem
	for _, it := range q.items {
		out = append(out, *it)
	}
	return out
}

func testDispatcher(q *memQueue, retention time.Duration) *Dispatcher {
	d := NewDispatcher(2*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), AddressPolicy{AllowPrivate: true}).
		WithQueue(q, retention)
	d.outboxRetries = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond}
	return d
}

// waitFor polls cond for up to 5 seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOutboxDeliversAndForgets(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.Store(b)
	}))
	defer srv.Close()
	q := newMemQueue(map[string]Target{"a": {URL: srv.URL}})
	d := testDispatcher(q, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	d.Send(Target{URL: srv.URL}, Event{Event: "message.received", Instance: "a", Data: "hi"})
	waitFor(t, "delivery", func() bool { return got.Load() != nil && len(q.snapshot()) == 0 })

	var evt Event
	if err := json.Unmarshal(got.Load().([]byte), &evt); err != nil || evt.ID == "" || evt.Event != "message.received" {
		t.Fatalf("delivered event must carry an id: %s (%v)", got.Load(), err)
	}
}

func TestOutboxRetriesWithTheSameIDThenKeepsTheFailure(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	q := newMemQueue(map[string]Target{"a": {URL: srv.URL}})
	d := testDispatcher(q, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	d.Send(Target{URL: srv.URL}, Event{Event: "message.status", Instance: "a"})
	waitFor(t, "the event to give up", func() bool {
		s := q.snapshot()
		return len(s) == 1 && s[0].failed
	})
	s := q.snapshot()[0]
	if s.Attempts != 3 || s.errMsg == "" {
		t.Fatalf("want 3 attempts (1 + 2 retries) and the last error, got %d %q", s.Attempts, s.errMsg)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 || string(bodies[0]) != string(bodies[2]) {
		t.Fatalf("every attempt must send the same body (same event id), got %d bodies", len(bodies))
	}
}

func TestOutboxWithoutRetentionDropsTheFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	q := newMemQueue(map[string]Target{"a": {URL: srv.URL}})
	d := testDispatcher(q, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	d.Send(Target{URL: srv.URL}, Event{Event: "x", Instance: "a"})
	time.Sleep(50 * time.Millisecond) // let it be enqueued and tried at least once
	waitFor(t, "the failed event to be dropped", func() bool { return len(q.snapshot()) == 0 })
}

func TestOutboxDropsEventsWhoseWebhookWasRemoved(t *testing.T) {
	q := newMemQueue(map[string]Target{"a": {URL: ""}}) // webhook removed after the event was written
	d := testDispatcher(q, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	d.Send(Target{URL: "https://example.invalid/hook"}, Event{Event: "x", Instance: "a"})
	waitFor(t, "the event to be dropped", func() bool { return len(q.snapshot()) == 0 })
}

func TestOutboxDownFallsBackToMemory(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(done) }))
	defer srv.Close()
	q := newMemQueue(nil)
	q.enqueueErr = errors.New("database is down")
	d := testDispatcher(q, time.Hour)
	d.Send(Target{URL: srv.URL}, Event{Event: "x", Instance: "a"})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("with the outbox down the event must still be delivered from memory")
	}
}
