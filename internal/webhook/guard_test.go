package webhook

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestAddressPolicy(t *testing.T) {
	strict := AddressPolicy{}
	open := AddressPolicy{AllowPrivate: true}
	cases := []struct {
		ip             string
		strict, opened bool
	}{
		{"8.8.8.8", true, true},
		{"2606:4700:4700::1111", true, true},
		{"169.254.169.254", false, false}, // cloud metadata: never
		{"fe80::1", false, false},
		{"0.0.0.0", false, false},
		{"224.0.0.1", false, false},
		{"127.0.0.1", false, true},
		{"::1", false, true},
		{"10.1.2.3", false, true},
		{"172.20.0.5", false, true},
		{"192.168.0.10", false, true},
		{"100.64.0.1", false, true},
		{"::ffff:127.0.0.1", false, true}, // IPv4-mapped loopback
	}
	for _, c := range cases {
		ip := netip.MustParseAddr(c.ip)
		if got := strict.Allowed(ip); got != c.strict {
			t.Errorf("strict %s: got %v, want %v", c.ip, got, c.strict)
		}
		if got := open.Allowed(ip); got != c.opened {
			t.Errorf("allow-private %s: got %v, want %v", c.ip, got, c.opened)
		}
	}
}

func fixedResolver(addrs ...string) Resolver {
	return func(context.Context, string, string) ([]netip.Addr, error) {
		out := make([]netip.Addr, len(addrs))
		for i, a := range addrs {
			out[i] = netip.MustParseAddr(a)
		}
		return out, nil
	}
}

func TestValidateURL(t *testing.T) {
	ctx := context.Background()
	strict := AddressPolicy{}
	public := fixedResolver("93.184.216.34")

	if err := ValidateURL(ctx, "https://hooks.example.com/x", strict, public); err != nil {
		t.Fatalf("public host rejected: %v", err)
	}
	for _, raw := range []string{
		"http://169.254.169.254/latest/meta-data",
		"http://127.0.0.1:8080/",
		"http://[::1]/",
		"http://localhost/",
	} {
		if err := ValidateURL(ctx, raw, strict, public); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: got %v, want ErrBlockedAddress", raw, err)
		}
	}
	// A name that resolves to an internal address is blocked too, even if only one of its
	// addresses is internal.
	if err := ValidateURL(ctx, "https://evil.example.com/", strict, fixedResolver("93.184.216.34", "10.0.0.7")); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("mixed resolution: got %v, want ErrBlockedAddress", err)
	}
	for _, raw := range []string{"ftp://example.com/", "https://", "not a url", "https://user:pass@example.com/"} {
		if err := ValidateURL(ctx, raw, strict, public); err == nil || errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%q: want a format error, got %v", raw, err)
		}
	}
	// Single-tenant setups can deliver to other containers.
	if err := ValidateURL(ctx, "http://receiver:9000/", AddressPolicy{AllowPrivate: true}, fixedResolver("172.20.0.4")); err != nil {
		t.Errorf("allow-private: %v", err)
	}
	// ... but never to the metadata service.
	if err := ValidateURL(ctx, "http://169.254.169.254/", AddressPolicy{AllowPrivate: true}, public); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("metadata with allow-private: got %v", err)
	}
}

// The check also runs at delivery time, on the IP actually dialed (DNS rebinding).
func TestDispatcherRefusesInternalAddressAtDelivery(t *testing.T) {
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit <- struct{}{}
	}))
	defer srv.Close()

	d := NewDispatcher(2*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), AddressPolicy{})
	err := d.post(Target{URL: srv.URL}, []byte(`{}`))
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("got %v, want ErrBlockedAddress", err)
	}
	select {
	case <-hit:
		t.Fatal("request reached the loopback server")
	default:
	}
}
