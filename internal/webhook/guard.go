package webhook

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlockedAddress is returned when a webhook or media URL points to an address the server must not
// reach. Without this check, anyone who can set a webhook could make the server send requests
// into its own network (SSRF): databases, admin panels, cloud metadata.
var ErrBlockedAddress = errors.New("address not allowed: it points to a loopback, private, link-local or reserved network")

// AddressPolicy decides which IP addresses webhook requests may reach.
type AddressPolicy struct {
	// AllowPrivate allows loopback and private networks (10/8, 172.16/12, 192.168/16, fc00::/7,
	// 127/8, ::1). Enable it only when every instance belongs to you and the receiver runs on
	// the same host or Docker network. Link-local addresses (including the cloud metadata
	// service at 169.254.169.254) stay blocked even then.
	AllowPrivate bool
}

// carrierNAT is 100.64.0.0/10 (shared address space), reachable only inside a provider network.
var carrierNAT = netip.MustParsePrefix("100.64.0.0/10")

// Allowed reports whether a webhook may connect to ip.
func (p AddressPolicy) Allowed(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if ip.Is4() && ip.As4()[0] == 0 { // 0.0.0.0/8 ("this network")
		return false
	}
	if p.AllowPrivate {
		return true
	}
	return !ip.IsLoopback() && !ip.IsPrivate() && !carrierNAT.Contains(ip)
}

// Resolver looks up the IP addresses of a host name. net.DefaultResolver.LookupNetIP fits.
type Resolver func(ctx context.Context, network, host string) ([]netip.Addr, error)

// ValidateURL checks a webhook URL when it is configured: http or https, a host, and every
// address the host resolves to allowed by the policy. The same rule is enforced again on each
// delivery (see dialControl), because DNS can change after the URL was accepted.
func ValidateURL(ctx context.Context, raw string, p AddressPolicy, resolve Resolver) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return errors.New("url must be an http or https address")
	}
	if u.User != nil {
		return errors.New("url must not contain a user name or password")
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		if !p.Allowed(ip) {
			return ErrBlockedAddress
		}
		return nil
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		if !p.AllowPrivate {
			return ErrBlockedAddress
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := resolve(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("could not resolve the webhook host %q", host)
	}
	for _, a := range addrs {
		if !p.Allowed(a) {
			return ErrBlockedAddress
		}
	}
	return nil
}

// SafeClient returns an HTTP client for addresses chosen by API users (webhooks, media URLs).
// Every connection, redirects included, is checked against the policy at dial time, and
// environment proxies are ignored so they cannot be used to reach blocked addresses.
func SafeClient(timeout time.Duration, p AddressPolicy) *http.Client {
	dialer := &net.Dialer{Timeout: timeout, Control: dialControl(p)}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// dialControl runs right before each connection is opened, with the IP actually chosen. It is
// what closes the DNS-rebinding gap: a host that resolved to a public IP when the webhook was
// saved and to an internal one now is still refused. Redirects go through the same dialer.
func dialControl(p AddressPolicy) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !p.Allowed(ip) {
			return ErrBlockedAddress
		}
		return nil
	}
}
