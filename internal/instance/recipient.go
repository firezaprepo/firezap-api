package instance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// ErrNotOnWhatsApp means WhatsApp has no account for the phone number.
var ErrNotOnWhatsApp = errors.New("this number is not on WhatsApp")

// ErrSendFailed wraps an error WhatsApp returned while sending.
var ErrSendFailed = errors.New("WhatsApp did not accept the message")

const (
	maxCheckNumbers = 20 // numbers per contacts/check request
	knownTTL        = 24 * time.Hour
	unknownTTL      = time.Hour
)

// directory caches what WhatsApp answered about phone numbers, so a conversation does not
// query WhatsApp before every message.
type directory struct {
	entries map[string]dirEntry // digits as typed → answer
}

type dirEntry struct {
	jid types.JID // empty when the number is not on WhatsApp
	at  time.Time
}

func (d *directory) get(digits string, now time.Time) (dirEntry, bool) {
	e, ok := d.entries[digits]
	if !ok {
		return e, false
	}
	ttl := knownTTL
	if e.jid.IsEmpty() {
		ttl = unknownTTL
	}
	return e, now.Sub(e.at) < ttl
}

func (d *directory) put(digits string, jid types.JID, now time.Time) {
	if d.entries == nil {
		d.entries = map[string]dirEntry{}
	}
	if len(d.entries) > 10000 { // bounded memory: start over rather than grow forever
		d.entries = map[string]dirEntry{}
	}
	d.entries[digits] = dirEntry{jid: jid, at: now}
}

// phoneVariants returns the forms in which a number may exist on WhatsApp. Brazilian mobile
// numbers gained a ninth digit, but many WhatsApp accounts still use the eight-digit form
// (it depends on the area code: in the 2026-11-14 test 55 85 9 9154-xxxx only exists as
// 55 85 9154-xxxx), so both are tried. Landlines (starting with 2 to 5) have no variant.
func phoneVariants(digits string) []string {
	out := []string{digits}
	if !strings.HasPrefix(digits, "55") {
		return out
	}
	switch {
	case len(digits) == 13 && digits[4] == '9': // 55 + area + 9 + 8 digits
		out = append(out, digits[:4]+digits[5:])
	case len(digits) == 12 && digits[4] >= '6': // 55 + area + 8 digits (mobile)
		out = append(out, digits[:4]+"9"+digits[4:])
	}
	return out
}

func onlyDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// account is how WhatsApp knows a phone number: the phone-number JID in the form that exists
// (with or without the ninth digit) and, when WhatsApp tells it, the LID.
type account struct {
	pn, lid types.JID
}

// lookup asks WhatsApp which form of each number exists. Numbers that are not on WhatsApp are
// left out of the result.
func lookup(ctx context.Context, cli *whatsmeow.Client, numbers []string) (map[string]account, error) {
	var queries []string
	for _, n := range numbers {
		for _, v := range phoneVariants(n) {
			queries = append(queries, "+"+v)
		}
	}
	resp, err := cli.IsOnWhatsApp(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("could not check the number on WhatsApp: %w", err)
	}
	found := map[string]types.IsOnWhatsAppResponse{}
	for _, r := range resp {
		if r.IsIn {
			found[onlyDigits(r.Query)] = r
		}
	}
	out := make(map[string]account, len(numbers))
	for _, n := range numbers {
		for _, v := range phoneVariants(n) { // the number as typed wins over its variant
			r, ok := found[v]
			if !ok {
				continue
			}
			// WhatsApp may say a form "is in" and answer with the account's LID plus the
			// canonical phone number (pn_jid). That canonical number is where messages go: in
			// the 2026-11-14 test 55 85 9 9154-xxxx was "in", but sending to it failed with "no
			// LID found", while the canonical 55 85 9154-xxxx worked.
			a := account{pn: types.NewJID(v, types.DefaultUserServer)}
			if r.PhoneNumber.Server == types.DefaultUserServer {
				a.pn = r.PhoneNumber.ToNonAD()
			}
			if r.JID.Server == types.HiddenUserServer {
				a.lid = r.JID.ToNonAD()
			}
			out[n] = a
			break
		}
	}
	return out, nil
}

// recipient turns a phone-number JID into the account WhatsApp actually has. Groups, LIDs and
// other JIDs are returned unchanged.
func (m *Manager) recipient(ctx context.Context, rt *runtime, cli *whatsmeow.Client, jid types.JID) (types.JID, error) {
	if jid.Server != types.DefaultUserServer {
		return jid, nil
	}
	now := time.Now()
	// A paused instance does not even look numbers up: the lookups are traffic too.
	if err := m.pausedError(rt, now); err != nil {
		return jid, err
	}
	rt.dirMu.Lock()
	e, fresh := rt.dir.get(jid.User, now)
	rt.dirMu.Unlock()
	if !fresh {
		res, err := lookup(ctx, cli, []string{jid.User})
		if err != nil {
			return jid, err
		}
		e = dirEntry{jid: res[jid.User].pn, at: now}
		rt.dirMu.Lock()
		rt.dir.put(jid.User, e.jid, now)
		rt.dirMu.Unlock()
	}
	if e.jid.IsEmpty() {
		err := fmt.Errorf("%w: %s (checked with and without the ninth digit)", ErrNotOnWhatsApp, "+"+jid.User)
		m.noteResult(rt, err) // a run of numbers that are not on WhatsApp is a bad list: pause
		return jid, err
	}
	return e.jid, nil
}

// NumberCheck is the answer for one number of contacts/check.
type NumberCheck struct {
	Number string `json:"number"`
	Exists bool   `json:"exists"`
	JID    string `json:"jid,omitempty"` // phone-number JID in the form WhatsApp has
	LID    string `json:"lid,omitempty"` // WhatsApp's LID for the account, when it tells it
}

// CheckNumbers tells which phone numbers have a WhatsApp account and under which JID. It is
// limited to a few numbers per request: bulk lookups are how spam lists are built.
func (m *Manager) CheckNumbers(ctx context.Context, id string, numbers []string) ([]NumberCheck, error) {
	if len(numbers) == 0 || len(numbers) > maxCheckNumbers {
		return nil, fmt.Errorf("%w: send 1 to %d numbers", ErrInvalidRecipient, maxCheckNumbers)
	}
	clean := make([]string, len(numbers))
	for i, n := range numbers {
		jid, err := ParseRecipient(n)
		if err != nil || jid.Server != types.DefaultUserServer {
			return nil, fmt.Errorf("%w: %q", ErrInvalidRecipient, n)
		}
		clean[i] = jid.User
	}
	rt, cli, err := m.connected(id)
	if err != nil {
		return nil, err
	}
	var res map[string]account
	if err := m.throttled(ctx, rt, 1, func() error {
		res, err = lookup(ctx, cli, clean)
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]NumberCheck, len(clean))
	now := time.Now()
	rt.dirMu.Lock()
	for i, n := range clean {
		a, ok := res[n]
		rt.dir.put(n, a.pn, now)
		out[i] = NumberCheck{Number: n, Exists: ok}
		if ok {
			out[i].JID = a.pn.String()
			if !a.lid.IsEmpty() {
				out[i].LID = a.lid.String()
			}
		}
	}
	rt.dirMu.Unlock()
	return out, nil
}
