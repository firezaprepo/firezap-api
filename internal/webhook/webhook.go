// Package webhook delivers instance events to the URL configured by the user.
//
// Every request carries an X-FireZAP-Signature header ("sha256=" + HMAC-SHA256 of the body
// with the webhook secret), so the receiver can confirm it came from this server. Events are
// written to an outbox before they are sent (see outbox.go): failed deliveries are retried with
// a growing delay, and nothing is lost when the server restarts or the receiver is down.
// Delivery is at least once — receivers should ignore an "id" they already processed.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// SignatureHeader is the header that carries the HMAC of the request body.
const SignatureHeader = "X-FireZAP-Signature"

// Event is the envelope sent to webhooks.
type Event struct {
	ID        string    `json:"id"` // unique per event: a repeated delivery has the same id
	Event     string    `json:"event"`
	Instance  string    `json:"instance"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// Target is where an instance sends its events.
type Target struct {
	URL    string
	Secret string
}

// Sign returns the signature header value for a body.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether a signature header matches the body. Receivers can use the same
// logic; it is exported so it can be tested and documented.
func Verify(secret string, body []byte, signature string) bool {
	return hmac.Equal([]byte(Sign(secret, body)), []byte(signature))
}

// Dispatcher sends events asynchronously, so a slow webhook never blocks message handling.
type Dispatcher struct {
	client *http.Client
	policy AddressPolicy
	log    *slog.Logger
	// retryDelays are the waits before each new attempt when there is no outbox (or it is
	// down): delivery from memory, as a fallback.
	retryDelays []time.Duration

	queue     Queue         // nil: deliver from memory only
	retention time.Duration // how long failed events are kept (0 = not kept)
	wake      chan struct{} // nudges Run when an event is written
	// outboxRetries replaces Retries when set (tests).
	outboxRetries []time.Duration
}

// NewDispatcher creates a dispatcher with the given request timeout. Every connection is
// checked against the address policy (see guard.go), and environment proxies are ignored so
// they cannot be used to reach addresses the policy blocks.
func NewDispatcher(timeout time.Duration, log *slog.Logger, policy AddressPolicy) *Dispatcher {
	return &Dispatcher{
		client:      SafeClient(timeout, policy),
		policy:      policy,
		log:         log,
		retryDelays: []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second},
		wake:        make(chan struct{}, 1),
	}
}

// WithQueue makes events go through an outbox; call Run to deliver it. retention is how long
// events that ran out of attempts are kept for a manual retry (0 = not kept).
func (d *Dispatcher) WithQueue(q Queue, retention time.Duration) *Dispatcher {
	d.queue, d.retention = q, retention
	return d
}

// Policy returns the address policy, so the HTTP layer validates URLs with the same rule.
func (d *Dispatcher) Policy() AddressPolicy { return d.policy }

// Send delivers the event in the background. It writes it to the outbox (a quick database
// insert) and returns; if the outbox cannot take it, the event is sent from memory instead, so
// a database hiccup delays nothing.
func (d *Dispatcher) Send(target Target, evt Event) {
	if target.URL == "" {
		return
	}
	if evt.ID == "" {
		evt.ID = newEventID()
	}
	body, err := json.Marshal(evt)
	if err != nil {
		d.log.Error("webhook: encode event", "instance", evt.Instance, "event", evt.Event, "error", err)
		return
	}
	if d.queue != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := d.queue.Enqueue(ctx, evt.Instance, evt.ID, evt.Event, body)
		cancel()
		if err == nil {
			select {
			case d.wake <- struct{}{}:
			default: // Run is already awake
			}
			return
		}
		d.log.Error("webhook: outbox unavailable, sending from memory", "instance", evt.Instance, "event", evt.Event, "error", err)
	}
	go d.deliver(target, evt, body)
}

func (d *Dispatcher) deliver(target Target, evt Event, body []byte) {
	for attempt := 0; ; attempt++ {
		err := d.post(target, body)
		if err == nil {
			return
		}
		if attempt >= len(d.retryDelays) {
			d.log.Warn("webhook: delivery failed, giving up",
				"instance", evt.Instance, "event", evt.Event, "attempts", attempt+1, "error", err)
			return
		}
		time.Sleep(d.retryDelays[attempt])
	}
}

func (d *Dispatcher) post(target Target, body []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), d.client.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "FireZAP-API-Webhook/1")
	if target.Secret != "" {
		req.Header.Set(SignatureHeader, Sign(target.Secret, body))
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
