package instance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// Limits that span days, on top of the per-minute and per-hour pace of limit.go:
//
//   - a daily allowance that grows with the age of the link (SEND_DAILY_SCHEDULE): a number
//     linked yesterday that sends like an old one is what WhatsApp's anti-abuse looks for;
//   - a separate daily allowance of first messages to private chats this number never talked
//     to (SEND_NEW_CHAT_SCHEDULE): messaging strangers is the strongest spam signal;
//   - a pause after several failed sends in a row (SEND_PAUSE_AFTER_FAILURES): a list of
//     numbers that are not on WhatsApp, or a number WhatsApp started refusing, should stop the
//     sending instead of insisting.
//
// They protect the number; they do not hide it. Nothing here makes automated traffic look like
// a person — that is out of scope for this project (see "Responsible use" in the README).

// DailyLimits configures the limits above. Empty schedules and a zero threshold turn them off.
type DailyLimits struct {
	Schedule        []int // messages per day, by days since the number was linked
	NewChatSchedule []int // first messages to unknown private chats per day, same indexing
	PauseAfter      int   // failed sends in a row that pause sending (0 = never)
	PauseFor        time.Duration
}

// pacingStore keeps the day's counters and the chats each number knows (store.Store).
type pacingStore interface {
	Daily(ctx context.Context, id string, t time.Time) (sent, newChats int, err error)
	AddDaily(ctx context.Context, id string, t time.Time, sent, newChats int) error
	KnownChat(ctx context.Context, id string, chats ...string) (bool, error)
	MarkChats(ctx context.Context, id string, chats ...string) error
}

// allowance returns the day's limit for a link that is age days old, or -1 for no limit.
func allowance(schedule []int, age int) int {
	if len(schedule) == 0 {
		return -1
	}
	if age < 0 {
		age = 0
	}
	if age >= len(schedule) {
		return schedule[len(schedule)-1]
	}
	return schedule[age]
}

// ageDays is how many whole days ago the number was linked (0 on the first day, and when the
// date is unknown: the cautious answer).
func ageDays(linkedAt *time.Time, now time.Time) int {
	if linkedAt == nil || now.Before(*linkedAt) {
		return 0
	}
	return int(now.Sub(*linkedAt) / (24 * time.Hour))
}

// nextMidnight is when the day of now ends, in now's location (the server's TZ).
func nextMidnight(now time.Time) time.Time {
	y, mo, d := now.Date()
	return time.Date(y, mo, d+1, 0, 0, 0, 0, now.Location())
}

// dayCount is what an instance sent today. day is empty until loaded from the database.
type dayCount struct {
	day      string
	sent     int
	newChats int
}

// pause is the failure breaker of an instance.
type pause struct {
	failures int       // failed sends in a row
	until    time.Time // sending is refused until then
}

// DayStats is the day's sending of an instance, as shown by GET /instances/{id}.
type DayStats struct {
	Sent         int        `json:"sent"`
	SentLimit    *int       `json:"sentLimit,omitempty"` // absent: no daily limit
	NewChats     int        `json:"newChats"`
	NewChatLimit *int       `json:"newChatLimit,omitempty"`
	LinkedDays   *int       `json:"linkedDays,omitempty"` // whole days since the number was linked
	PausedUntil  *time.Time `json:"pausedUntil,omitempty"`
	Failures     int        `json:"failuresInARow"`
}

// ensureDay loads today's counters when the day changed (or on the first use after a start).
// Two loads may race (a send and the instance view): the second one finds the day already set
// and keeps what is in memory, which may include sends counted after the first load.
func (m *Manager) ensureDay(ctx context.Context, rt *runtime, now time.Time) {
	day := now.Format("2006-01-02")
	rt.limMu.Lock()
	current := rt.today.day == day
	rt.limMu.Unlock()
	if current || m.pace == nil {
		return
	}
	sent, newChats, err := m.pace.Daily(ctx, rt.rec.ID, now)
	if err != nil {
		// Counting from zero is the lesser evil: refusing every send because the database
		// blinked would stop a working number.
		m.log.Error("daily limits: read today's counters", "instance", rt.rec.ID, "error", err)
		sent, newChats = 0, 0
	}
	rt.limMu.Lock()
	if rt.today.day != day {
		rt.today = dayCount{day: day, sent: sent, newChats: newChats}
	}
	rt.limMu.Unlock()
}

// dailyCheck refuses a request that would pass today's limits, or that comes while sending is
// paused. The caller holds rt.limMu.
func (m *Manager) dailyCheck(rt *runtime, now time.Time, units int, newChat bool) error {
	if now.Before(rt.brk.until) {
		return &RateLimitError{
			Reason:     fmt.Sprintf("sending is paused after %d failed sends in a row (SEND_PAUSE_AFTER_FAILURES)", m.daily.PauseAfter),
			RetryAfter: rt.brk.until.Sub(now),
		}
	}
	age := ageDays(rt.linkedAt(), now)
	if limit := allowance(m.daily.Schedule, age); limit >= 0 && rt.today.sent+units > limit {
		return &RateLimitError{
			Reason:     fmt.Sprintf("the daily sending limit of this number was reached (%d today, day %d since it was linked — SEND_DAILY_SCHEDULE)", limit, age),
			RetryAfter: nextMidnight(now).Sub(now),
		}
	}
	if newChat {
		if limit := allowance(m.daily.NewChatSchedule, age); limit >= 0 && rt.today.newChats+1 > limit {
			return &RateLimitError{
				Reason:     fmt.Sprintf("the daily limit of first messages to new chats was reached (%d today — SEND_NEW_CHAT_SCHEDULE)", limit),
				RetryAfter: nextMidnight(now).Sub(now),
			}
		}
	}
	return nil
}

// countSent adds a request to today's counters, in memory and in the database.
func (m *Manager) countSent(ctx context.Context, rt *runtime, now time.Time, units int, newChat bool) {
	nc := 0
	if newChat {
		nc = 1
	}
	rt.limMu.Lock()
	rt.today.sent += units
	rt.today.newChats += nc
	rt.limMu.Unlock()
	if m.pace == nil {
		return
	}
	// A short context of its own: the request's may already be ending, and the count must stick.
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := m.pace.AddDaily(dbCtx, rt.rec.ID, now, units, nc); err != nil {
		m.log.Error("daily limits: save today's counters", "instance", rt.rec.ID, "error", err)
	}
}

// pausedError is dailyCheck's breaker part alone, for checks made before the send slot.
func (m *Manager) pausedError(rt *runtime, now time.Time) error {
	rt.limMu.Lock()
	defer rt.limMu.Unlock()
	if now.Before(rt.brk.until) {
		return &RateLimitError{
			Reason:     fmt.Sprintf("sending is paused after %d failed sends in a row (SEND_PAUSE_AFTER_FAILURES)", m.daily.PauseAfter),
			RetryAfter: rt.brk.until.Sub(now),
		}
	}
	return nil
}

// countsAsFailure tells which errors feed the breaker: WhatsApp refused the message, or the
// number is not on WhatsApp. Our own validation errors and rate limits do not.
func countsAsFailure(err error) bool {
	return errors.Is(err, ErrSendFailed) || errors.Is(err, ErrNotOnWhatsApp)
}

// noteResult feeds the breaker with the outcome of a send. It pauses the instance when
// PauseAfter failures come in a row, and tells the webhook.
func (m *Manager) noteResult(rt *runtime, err error) {
	if m.daily.PauseAfter <= 0 {
		return
	}
	now := time.Now()
	rt.limMu.Lock()
	tripped := false
	switch {
	case err == nil:
		rt.brk.failures = 0
	case countsAsFailure(err):
		rt.brk.failures++
		if rt.brk.failures >= m.daily.PauseAfter {
			rt.brk = pause{until: now.Add(m.daily.PauseFor)}
			tripped = true
		}
	}
	until := rt.brk.until
	rt.limMu.Unlock()
	if tripped {
		m.log.Warn("sending paused after failed sends in a row", "instance", rt.rec.ID,
			"failures", m.daily.PauseAfter, "until", until, "lastError", err)
		m.emit(rt, "send.paused", map[string]any{
			"failures":  m.daily.PauseAfter,
			"until":     until.UTC(),
			"lastError": err.Error(),
		})
	}
}

// dayStats is the public view of the day's counters.
func (m *Manager) dayStats(rt *runtime, now time.Time) DayStats {
	rt.limMu.Lock()
	defer rt.limMu.Unlock()
	st := DayStats{Failures: rt.brk.failures}
	if rt.today.day == now.Format("2006-01-02") {
		st.Sent, st.NewChats = rt.today.sent, rt.today.newChats
	}
	linked := rt.linkedAt()
	if linked != nil {
		age := ageDays(linked, now)
		st.LinkedDays = &age
	}
	age := ageDays(linked, now)
	if l := allowance(m.daily.Schedule, age); l >= 0 {
		st.SentLimit = &l
	}
	if l := allowance(m.daily.NewChatSchedule, age); l >= 0 {
		st.NewChatLimit = &l
	}
	if now.Before(rt.brk.until) {
		u := rt.brk.until.UTC()
		st.PausedUntil = &u
	}
	return st
}

// chatForms returns the ways a private chat may be recorded: the phone-number JID and the LID
// of the same person, when WhatsApp told us the pair. Other chats return nil.
func chatForms(ctx context.Context, cli *whatsmeow.Client, jid types.JID) []string {
	jid = jid.ToNonAD()
	switch jid.Server {
	case types.DefaultUserServer, types.HiddenUserServer:
	default:
		return nil
	}
	out := []string{jid.String()}
	if cli == nil || cli.Store == nil || cli.Store.LIDs == nil {
		return out
	}
	if jid.Server == types.DefaultUserServer {
		if lid, err := cli.Store.LIDs.GetLIDForPN(ctx, jid); err == nil && !lid.IsEmpty() {
			out = append(out, lid.ToNonAD().String())
		}
	} else if pn, err := cli.Store.LIDs.GetPNForLID(ctx, jid); err == nil && !pn.IsEmpty() {
		out = append(out, pn.ToNonAD().String())
	}
	return out
}

// isNewChat tells whether a message to jid would be the first one in a private chat this number
// never talked to. It only asks when the new-chat limit is on.
func (m *Manager) isNewChat(ctx context.Context, rt *runtime, cli *whatsmeow.Client, jid types.JID) bool {
	if len(m.daily.NewChatSchedule) == 0 || m.pace == nil {
		return false
	}
	forms := chatForms(ctx, cli, jid)
	if len(forms) == 0 {
		return false // groups, channels, broadcast lists
	}
	known, err := m.pace.KnownChat(ctx, rt.rec.ID, forms...)
	if err != nil {
		m.log.Error("daily limits: check known chats", "instance", rt.rec.ID, "error", err)
		return false // unsure: do not hold back a message to someone who may be a customer
	}
	return !known
}

// markChats records private chats the number talked to, in the background.
func (m *Manager) markChats(rt *runtime, chats ...string) {
	if m.pace == nil || len(chats) == 0 {
		return
	}
	id := rt.rec.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := m.pace.MarkChats(ctx, id, chats...); err != nil {
			m.log.Error("daily limits: record known chats", "instance", id, "error", err)
		}
	}()
}
