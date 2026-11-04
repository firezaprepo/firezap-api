package webhook

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSignAndVerify(t *testing.T) {
	body := []byte(`{"event":"message.received"}`)
	sig := Sign("secret", body)
	if !Verify("secret", body, sig) {
		t.Fatal("valid signature rejected")
	}
	if Verify("other", body, sig) {
		t.Fatal("signature with the wrong secret accepted")
	}
	if Verify("secret", []byte(`{"event":"tampered"}`), sig) {
		t.Fatal("signature of a different body accepted")
	}
}

func TestDispatcherRetriesUntilSuccess(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !Verify("s3cr3t", body, r.Header.Get(SignatureHeader)) {
			t.Error("request arrived without a valid signature")
		}
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		close(done)
	}))
	defer srv.Close()

	d := NewDispatcher(2*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.retryDelays = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	d.Send(Target{URL: srv.URL, Secret: "s3cr3t"}, Event{Event: "message.received", Instance: "test"})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("webhook not delivered after retries (calls: %d)", calls.Load())
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected 3 calls, got %d", got)
	}
}
