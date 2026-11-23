package instance

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func TestAllowance(t *testing.T) {
	s := []int{20, 40, 80}
	cases := []struct{ age, want int }{{-1, 20}, {0, 20}, {1, 40}, {2, 80}, {3, 80}, {400, 80}}
	for _, c := range cases {
		if got := allowance(s, c.age); got != c.want {
			t.Errorf("allowance(age %d) = %d, want %d", c.age, got, c.want)
		}
	}
	if got := allowance(nil, 5); got != -1 {
		t.Errorf("empty schedule must mean no limit, got %d", got)
	}
}

func TestAgeDays(t *testing.T) {
	linked := t0
	if got := ageDays(nil, t0); got != 0 {
		t.Errorf("unknown link date must count as day 0, got %d", got)
	}
	if got := ageDays(&linked, t0.Add(23*time.Hour)); got != 0 {
		t.Errorf("23 h after linking is day 0, got %d", got)
	}
	if got := ageDays(&linked, t0.Add(49*time.Hour)); got != 2 {
		t.Errorf("49 h after linking is day 2, got %d", got)
	}
	if got := ageDays(&linked, t0.Add(-time.Hour)); got != 0 {
		t.Errorf("a clock behind the link date must not go negative, got %d", got)
	}
}

func TestNextMidnightUsesTheLocation(t *testing.T) {
	sp := time.FixedZone("BRT", -3*3600)
	now := time.Date(2026, 11, 23, 22, 30, 0, 0, sp)
	want := time.Date(2026, 11, 24, 0, 0, 0, 0, sp)
	if got := nextMidnight(now); !got.Equal(want) {
		t.Fatalf("next midnight = %v, want %v", got, want)
	}
}

// fakePace is an in-memory pacingStore.
type fakePace struct {
	mu        sync.Mutex
	sent, new int
	known     map[string]bool
	readErr   error
}

func (f *fakePace) Daily(context.Context, string, time.Time) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent, f.new, f.readErr
}
func (f *fakePace) AddDaily(_ context.Context, _ string, _ time.Time, s, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent += s
	f.new += n
	return nil
}
func (f *fakePace) KnownChat(_ context.Context, _ string, chats ...string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range chats {
		if f.known[c] {
			return true, nil
		}
	}
	return false, nil
}
func (f *fakePace) MarkChats(_ context.Context, _ string, chats ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range chats {
		f.known[c] = true
	}
	return nil
}

func testManager(daily DailyLimits, pace *fakePace) (*Manager, *runtime) {
	m := &Manager{daily: daily, pace: pace, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	linked := time.Now().Add(-30 * time.Minute) // linked today: day 0
	rt := &runtime{sendSlot: make(chan struct{}, 1), linked: &linked}
	rt.rec.ID = "test"
	return m, rt
}

func ok() error { return nil }

func mustJID(t *testing.T, s string) types.JID {
	t.Helper()
	j, err := types.ParseJID(s)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestDailyLimitRefusesUntilMidnight(t *testing.T) {
	pace := &fakePace{known: map[string]bool{}}
	m, rt := testManager(DailyLimits{Schedule: []int{3, 10}}, pace)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := m.throttled(ctx, rt, 1, ok); err != nil {
			t.Fatalf("send %d: %v", i+1, err)
		}
	}
	err := m.throttled(ctx, rt, 1, ok)
	var lim *RateLimitError
	if !errors.As(err, &lim) {
		t.Fatalf("4th send on day 0 with a limit of 3 must be refused, got %v", err)
	}
	if lim.RetryAfter <= 0 || lim.RetryAfter > 24*time.Hour {
		t.Fatalf("retry must point to the next midnight, got %v", lim.RetryAfter)
	}
	if pace.sent != 3 {
		t.Fatalf("the refused send must not be counted, stored %d", pace.sent)
	}
}

func TestDailyCountersSurviveARestart(t *testing.T) {
	// Another process (before a restart) already sent 2 of the 3 allowed today.
	pace := &fakePace{sent: 2, known: map[string]bool{}}
	m, rt := testManager(DailyLimits{Schedule: []int{3}}, pace)
	ctx := context.Background()
	if err := m.throttled(ctx, rt, 1, ok); err != nil {
		t.Fatalf("3rd message of the day should go: %v", err)
	}
	if err := m.throttled(ctx, rt, 1, ok); err == nil {
		t.Fatal("4th message must be refused: the counter must have been read from the store")
	}
}

func TestNewChatLimitOnlyCountsStrangers(t *testing.T) {
	pace := &fakePace{known: map[string]bool{}}
	m, rt := testManager(DailyLimits{NewChatSchedule: []int{1}}, pace)
	ctx := context.Background()
	if err := m.gate(ctx, rt, 1, true, ok); err != nil {
		t.Fatalf("first new chat of the day should go: %v", err)
	}
	if err := m.gate(ctx, rt, 1, true, ok); err == nil {
		t.Fatal("second new chat must be refused")
	}
	for i := 0; i < 5; i++ { // known chats are not affected
		if err := m.gate(ctx, rt, 1, false, ok); err != nil {
			t.Fatalf("message to a known chat must go: %v", err)
		}
	}
	if pace.new != 1 || pace.sent != 6 {
		t.Fatalf("stored new=%d sent=%d, want 1 and 6", pace.new, pace.sent)
	}
}

func TestIsNewChatOnlyWhenTheLimitIsOn(t *testing.T) {
	pace := &fakePace{known: map[string]bool{}}
	m, rt := testManager(DailyLimits{}, pace)
	if m.isNewChat(context.Background(), rt, nil, mustJID(t, "5511999990000@s.whatsapp.net")) {
		t.Fatal("without SEND_NEW_CHAT_SCHEDULE nothing is a new chat")
	}
	m.daily.NewChatSchedule = []int{5}
	jid := mustJID(t, "5511999990000@s.whatsapp.net")
	if !m.isNewChat(context.Background(), rt, nil, jid) {
		t.Fatal("an unknown private chat is new")
	}
	pace.known[jid.String()] = true
	if m.isNewChat(context.Background(), rt, nil, jid) {
		t.Fatal("a known chat is not new")
	}
	if m.isNewChat(context.Background(), rt, nil, mustJID(t, "120363000000000000@g.us")) {
		t.Fatal("a group is never a new chat")
	}
}

func TestBreakerPausesAfterFailuresInARow(t *testing.T) {
	pace := &fakePace{known: map[string]bool{}}
	m, rt := testManager(DailyLimits{PauseAfter: 3, PauseFor: time.Minute}, pace)
	ctx := context.Background()
	failing := func() error { return ErrSendFailed }
	_ = m.throttled(ctx, rt, 1, failing)
	_ = m.throttled(ctx, rt, 1, failing)
	if err := m.throttled(ctx, rt, 1, ok); err != nil { // a success in between resets the run
		t.Fatalf("send should go: %v", err)
	}
	for i := 0; i < 3; i++ {
		_ = m.throttled(ctx, rt, 1, failing)
	}
	err := m.throttled(ctx, rt, 1, ok)
	var lim *RateLimitError
	if !errors.As(err, &lim) || lim.RetryAfter > time.Minute {
		t.Fatalf("3 failures in a row must pause for 1 minute, got %v", err)
	}
	if err := m.pausedError(rt, time.Now()); err == nil {
		t.Fatal("number lookups must be refused while paused too")
	}
	if st := m.dayStats(rt, time.Now()); st.PausedUntil == nil {
		t.Fatal("the pause must show in the instance stats")
	}
}

func TestBreakerIgnoresOtherErrors(t *testing.T) {
	m, rt := testManager(DailyLimits{PauseAfter: 1, PauseFor: time.Minute}, &fakePace{known: map[string]bool{}})
	_ = m.throttled(context.Background(), rt, 1, func() error { return ErrInvalidGroup })
	if err := m.pausedError(rt, time.Now()); err != nil {
		t.Fatal("a validation error is not a sign of a bad number")
	}
	m.noteResult(rt, ErrNotOnWhatsApp)
	if err := m.pausedError(rt, time.Now()); err == nil {
		t.Fatal("numbers that are not on WhatsApp must count as failures")
	}
}

func TestDayStats(t *testing.T) {
	m, rt := testManager(DailyLimits{Schedule: []int{20, 40}, NewChatSchedule: []int{5}}, &fakePace{known: map[string]bool{}})
	_ = m.throttled(context.Background(), rt, 2, ok)
	st := m.dayStats(rt, time.Now())
	if st.Sent != 2 || st.SentLimit == nil || *st.SentLimit != 20 || st.NewChatLimit == nil || *st.NewChatLimit != 5 {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if st.LinkedDays == nil || *st.LinkedDays != 0 {
		t.Fatalf("linked today must show day 0: %+v", st)
	}
	m2, rt2 := testManager(DailyLimits{}, &fakePace{known: map[string]bool{}})
	if st := m2.dayStats(rt2, time.Now()); st.SentLimit != nil || st.NewChatLimit != nil {
		t.Fatalf("without schedules there are no limits to show: %+v", st)
	}
}
