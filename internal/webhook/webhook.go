// Package webhook delivers instance events to the URL configured by the user.
//
// Every request carries an X-FireZAP-Signature header ("sha256=" + HMAC-SHA256 of the body
// with the webhook secret), so the receiver can confirm it came from this server. Failed
// deliveries are retried with a growing delay.
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
	log    *slog.Logger
	// retryDelays are the waits before each new attempt after the first one fails.
	retryDelays []time.Duration
}

// NewDispatcher creates a dispatcher with the given request timeout.
func NewDispatcher(timeout time.Duration, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		client:      &http.Client{Timeout: timeout},
		log:         log,
		retryDelays: []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second},
	}
}

// Send delivers the event in the background. It returns immediately.
func (d *Dispatcher) Send(target Target, evt Event) {
	if target.URL == "" {
		return
	}
	body, err := json.Marshal(evt)
	if err != nil {
		d.log.Error("webhook: encode event", "instance", evt.Instance, "event", evt.Event, "error", err)
		return
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
