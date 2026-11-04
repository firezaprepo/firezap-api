// Package instance runs one WhatsApp connection (whatsmeow client) per instance and turns
// WhatsApp events into webhook events.
//
// Each instance is isolated: it has its own client, its own lock and its own send pacing,
// so a stuck or banned number never blocks the others.
package instance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/firezaprepo/firezap-api/internal/auth"
	"github.com/firezaprepo/firezap-api/internal/store"
	"github.com/firezaprepo/firezap-api/internal/webhook"
)

// Status is the connection state of an instance.
type Status string

const (
	StatusDisconnected Status = "disconnected" // not connected (never linked, or connection dropped)
	StatusConnecting   Status = "connecting"   // opening the connection
	StatusQR           Status = "qr"           // waiting for the QR code or pairing code
	StatusConnected    Status = "connected"    // linked and online
	StatusLoggedOut    Status = "logged_out"   // the number was unlinked; a new QR code is needed
)

// Errors returned to the HTTP layer.
var (
	ErrExists        = errors.New("an instance with this id already exists")
	ErrNotConnected  = errors.New("instance is not connected")
	ErrAlreadyLinked = errors.New("instance is already linked to a number")
)

// Info is the public view of an instance.
type Info struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Status     Status    `json:"status"`
	Phone      string    `json:"phone,omitempty"`
	WebhookURL string    `json:"webhookUrl,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// QRInfo is the current QR code of an instance waiting to be linked.
type QRInfo struct {
	Status    Status    `json:"status"`
	Code      string    `json:"code,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

type runtime struct {
	mu        sync.Mutex
	rec       store.Instance
	client    *whatsmeow.Client
	status    Status
	qr        string
	qrExpires time.Time

	sendMu   sync.Mutex
	lastSend time.Time
}

// Manager owns every instance.
type Manager struct {
	store       *store.Store
	container   *sqlstore.Container
	hooks       *webhook.Dispatcher
	log         *slog.Logger
	waLog       waLog.Logger
	minInterval time.Duration

	mu        sync.RWMutex
	instances map[string]*runtime
}

// NewManager creates the manager. Call Restore to reconnect the instances that were linked.
func NewManager(st *store.Store, container *sqlstore.Container, hooks *webhook.Dispatcher,
	log *slog.Logger, wlog waLog.Logger, minInterval time.Duration) *Manager {
	return &Manager{
		store: st, container: container, hooks: hooks, log: log, waLog: wlog,
		minInterval: minInterval, instances: map[string]*runtime{},
	}
}

// Restore loads every instance and reconnects the ones that are linked to a number, so a
// server restart does not require scanning the QR code again.
func (m *Manager) Restore(ctx context.Context) error {
	recs, err := m.store.List(ctx)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		rt := &runtime{rec: rec, status: StatusDisconnected}
		m.mu.Lock()
		m.instances[rec.ID] = rt
		m.mu.Unlock()
		if rec.DeviceJID == "" {
			continue
		}
		jid, err := types.ParseJID(rec.DeviceJID)
		if err != nil {
			m.log.Error("restore: invalid device jid", "instance", rec.ID, "error", err)
			continue
		}
		device, err := m.container.GetDevice(ctx, jid)
		if err != nil || device == nil {
			// The session is gone (for example the number was unlinked while we were offline).
			m.log.Warn("restore: session not found, a new QR code is needed", "instance", rec.ID)
			_ = m.store.SetDevice(ctx, rec.ID, "")
			rt.status = StatusLoggedOut
			continue
		}
		rt.client = m.newClient(rt, device)
		go func(id string, cli *whatsmeow.Client) {
			if err := cli.Connect(); err != nil {
				m.log.Error("restore: connect failed", "instance", id, "error", err)
			}
		}(rec.ID, rt.client)
	}
	m.log.Info("instances restored", "total", len(recs))
	return nil
}

func (m *Manager) get(id string) (*runtime, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rt, ok := m.instances[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return rt, nil
}

// Create registers a new instance and returns its API key. The key is shown only once.
func (m *Manager) Create(ctx context.Context, id, name string) (Info, string, error) {
	if _, err := m.get(id); err == nil {
		return Info{}, "", ErrExists
	}
	key, err := auth.NewKey()
	if err != nil {
		return Info{}, "", err
	}
	rec := store.Instance{ID: id, Name: name, APIKeyHash: auth.HashKey(key), CreatedAt: time.Now()}
	if err := m.store.Create(ctx, rec); err != nil {
		return Info{}, "", err
	}
	rt := &runtime{rec: rec, status: StatusDisconnected}
	m.mu.Lock()
	m.instances[id] = rt
	m.mu.Unlock()
	return rt.info(), key, nil
}

// List returns every instance.
func (m *Manager) List() []Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Info, 0, len(m.instances))
	for _, rt := range m.instances {
		out = append(out, rt.info())
	}
	return out
}

// Get returns one instance.
func (m *Manager) Get(id string) (Info, error) {
	rt, err := m.get(id)
	if err != nil {
		return Info{}, err
	}
	return rt.info(), nil
}

func (rt *runtime) info() Info {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	in := Info{ID: rt.rec.ID, Name: rt.rec.Name, Status: rt.status, WebhookURL: rt.rec.WebhookURL, CreatedAt: rt.rec.CreatedAt}
	if rt.client != nil && rt.client.Store.ID != nil {
		in.Phone = rt.client.Store.ID.User
	}
	return in
}

// Connect opens the connection. If the instance is not linked yet, it starts the QR flow:
// read the code with QR (or use Pair for a pairing code).
func (m *Manager) Connect(id string) error {
	rt, err := m.get(id)
	if err != nil {
		return err
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.client != nil && rt.client.IsConnected() {
		return nil
	}
	if rt.client == nil {
		rt.client = m.newClient(rt, m.container.NewDevice())
	}
	cli := rt.client
	if cli.Store.ID != nil {
		rt.status = StatusConnecting
		return cli.Connect()
	}

	qrChan, err := cli.GetQRChannel(context.Background())
	if err != nil {
		return err
	}
	if err := cli.Connect(); err != nil {
		return err
	}
	rt.status = StatusConnecting
	go m.watchQR(rt, qrChan)
	return nil
}

func (m *Manager) watchQR(rt *runtime, qrChan <-chan whatsmeow.QRChannelItem) {
	for item := range qrChan {
		rt.mu.Lock()
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			rt.status = StatusQR
			rt.qr = item.Code
			rt.qrExpires = time.Now().Add(item.Timeout)
		case whatsmeow.QRChannelSuccess.Event:
			rt.qr = ""
		default: // timeout or error: the code expired without being scanned
			rt.qr = ""
			if rt.status != StatusConnected {
				rt.status = StatusDisconnected
			}
		}
		status, id := rt.status, rt.rec.ID
		rt.mu.Unlock()
		if item.Event == whatsmeow.QRChannelEventCode {
			m.emit(rt, "connection.update", map[string]any{"status": status})
		} else if item.Event != whatsmeow.QRChannelSuccess.Event {
			m.log.Info("qr flow ended without linking", "instance", id, "reason", item.Event)
			m.emit(rt, "connection.update", map[string]any{"status": status, "reason": item.Event})
		}
	}
}

// QR returns the current QR code of an instance waiting to be linked.
func (m *Manager) QR(id string) (QRInfo, error) {
	rt, err := m.get(id)
	if err != nil {
		return QRInfo{}, err
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	q := QRInfo{Status: rt.status}
	if rt.status == StatusQR && rt.qr != "" {
		q.Code, q.ExpiresAt = rt.qr, rt.qrExpires
	}
	return q, nil
}

// Pair links the instance with an 8-character pairing code instead of the QR code. The user
// types the code on the phone: WhatsApp > Linked devices > Link with phone number.
func (m *Manager) Pair(ctx context.Context, id, phone string) (string, error) {
	jid, err := ParseRecipient(phone)
	if err != nil || jid.Server != types.DefaultUserServer {
		return "", ErrInvalidRecipient
	}
	rt, err := m.get(id)
	if err != nil {
		return "", err
	}
	rt.mu.Lock()
	linked := rt.client != nil && rt.client.Store.ID != nil
	rt.mu.Unlock()
	if linked {
		return "", ErrAlreadyLinked
	}
	if err := m.Connect(id); err != nil {
		return "", err
	}
	// WhatsApp only accepts the pairing request once the connection reaches the QR stage.
	deadline := time.Now().Add(15 * time.Second)
	for {
		rt.mu.Lock()
		ready, cli := rt.status == StatusQR, rt.client
		rt.mu.Unlock()
		if ready {
			return cli.PairPhone(ctx, jid.User, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
		}
		if time.Now().After(deadline) {
			return "", errors.New("timed out waiting for the connection to be ready for pairing")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Logout unlinks the number. A new QR code is needed to use the instance again.
func (m *Manager) Logout(ctx context.Context, id string) error {
	rt, err := m.get(id)
	if err != nil {
		return err
	}
	rt.mu.Lock()
	cli := rt.client
	rt.client, rt.status, rt.qr = nil, StatusLoggedOut, ""
	rt.mu.Unlock()
	if cli != nil {
		if cli.Store.ID != nil && cli.IsConnected() {
			if err := cli.Logout(ctx); err != nil {
				m.log.Warn("logout: whatsapp did not confirm, removing the session locally", "instance", id, "error", err)
				_ = cli.Store.Delete(ctx)
			}
		} else if cli.Store.ID != nil {
			_ = cli.Store.Delete(ctx)
		}
		cli.Disconnect()
	}
	if err := m.store.SetDevice(ctx, id, ""); err != nil {
		return err
	}
	m.emit(rt, "connection.update", map[string]any{"status": StatusLoggedOut})
	return nil
}

// Delete unlinks the number and removes the instance.
func (m *Manager) Delete(ctx context.Context, id string) error {
	if err := m.Logout(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := m.store.Delete(ctx, id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.instances, id)
	m.mu.Unlock()
	return nil
}

// SetWebhook changes where the instance sends its events. An empty secret generates one.
func (m *Manager) SetWebhook(ctx context.Context, id, url, secret string) (string, error) {
	rt, err := m.get(id)
	if err != nil {
		return "", err
	}
	if url != "" && secret == "" {
		if secret, err = auth.NewSecret(); err != nil {
			return "", err
		}
	}
	if err := m.store.SetWebhook(ctx, id, url, secret); err != nil {
		return "", err
	}
	rt.mu.Lock()
	rt.rec.WebhookURL, rt.rec.WebhookSecret = url, secret
	rt.mu.Unlock()
	return secret, nil
}

// SendText sends a text message and returns the WhatsApp message ID. Messages from the same
// instance are paced by the configured minimum interval.
func (m *Manager) SendText(ctx context.Context, id, to, text string) (string, error) {
	jid, err := ParseRecipient(to)
	if err != nil {
		return "", err
	}
	rt, err := m.get(id)
	if err != nil {
		return "", err
	}
	rt.mu.Lock()
	cli := rt.client
	rt.mu.Unlock()
	if cli == nil || !cli.IsConnected() || !cli.IsLoggedIn() {
		return "", ErrNotConnected
	}

	rt.sendMu.Lock()
	defer rt.sendMu.Unlock()
	if wait := m.minInterval - time.Since(rt.lastSend); wait > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
	resp, err := cli.SendMessage(ctx, jid, &waE2E.Message{Conversation: proto.String(text)})
	rt.lastSend = time.Now()
	if err != nil {
		return "", fmt.Errorf("send failed: %w", err)
	}
	return resp.ID, nil
}

// Shutdown closes every connection without unlinking the numbers.
func (m *Manager) Shutdown() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, rt := range m.instances {
		rt.mu.Lock()
		if rt.client != nil {
			rt.client.Disconnect()
		}
		rt.mu.Unlock()
	}
}

func (m *Manager) newClient(rt *runtime, device *wastore.Device) *whatsmeow.Client {
	cli := whatsmeow.NewClient(device, m.waLog.Sub(rt.rec.ID))
	cli.AddEventHandler(func(evt any) { m.handle(rt, evt) })
	return cli
}

func (m *Manager) emit(rt *runtime, name string, data any) {
	rt.mu.Lock()
	target := webhook.Target{URL: rt.rec.WebhookURL, Secret: rt.rec.WebhookSecret}
	id := rt.rec.ID
	rt.mu.Unlock()
	m.hooks.Send(target, webhook.Event{Event: name, Instance: id, Timestamp: time.Now().UTC(), Data: data})
}

func (m *Manager) setStatus(rt *runtime, s Status) {
	rt.mu.Lock()
	changed := rt.status != s
	rt.status = s
	rt.mu.Unlock()
	if changed {
		m.emit(rt, "connection.update", map[string]any{"status": s})
	}
}

func (m *Manager) handle(rt *runtime, evt any) {
	switch e := evt.(type) {
	case *events.PairSuccess:
		if err := m.store.SetDevice(context.Background(), rt.rec.ID, e.ID.String()); err != nil {
			m.log.Error("pair: could not save the linked device", "instance", rt.rec.ID, "error", err)
		}
		m.log.Info("instance linked", "instance", rt.rec.ID, "phone", e.ID.User)
	case *events.Connected:
		m.setStatus(rt, StatusConnected)
	case *events.Disconnected:
		// whatsmeow reconnects by itself; the status is updated again on Connected.
		m.setStatus(rt, StatusDisconnected)
	case *events.StreamReplaced:
		m.log.Warn("connection replaced by another session", "instance", rt.rec.ID)
		m.setStatus(rt, StatusDisconnected)
	case *events.LoggedOut:
		m.log.Warn("number unlinked from the phone", "instance", rt.rec.ID, "reason", e.Reason.String())
		rt.mu.Lock()
		rt.client = nil
		rt.mu.Unlock()
		_ = m.store.SetDevice(context.Background(), rt.rec.ID, "")
		m.setStatus(rt, StatusLoggedOut)
	case *events.Message:
		kind, text := MessageContent(e.Message)
		m.emit(rt, "message.received", map[string]any{
			"id":        e.Info.ID,
			"chat":      e.Info.Chat.String(),
			"sender":    e.Info.Sender.String(),
			"fromMe":    e.Info.IsFromMe,
			"isGroup":   e.Info.IsGroup,
			"pushName":  e.Info.PushName,
			"timestamp": e.Info.Timestamp.UTC(),
			"type":      kind,
			"text":      text,
		})
	case *events.Receipt:
		var status string
		switch e.Type {
		case types.ReceiptTypeDelivered:
			status = "delivered"
		case types.ReceiptTypeRead:
			status = "read"
		default:
			return
		}
		m.emit(rt, "message.status", map[string]any{
			"ids":       e.MessageIDs,
			"chat":      e.Chat.String(),
			"status":    status,
			"timestamp": e.Timestamp.UTC(),
		})
	}
}
