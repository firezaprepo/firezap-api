package instance

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func TestLimiterMinInterval(t *testing.T) {
	l := sendLimiter{limits: SendLimits{MinInterval: time.Second}}
	if got := l.next(t0); !got.Equal(t0) {
		t.Fatalf("first message should go now, got %v", got.Sub(t0))
	}
	l.record(t0)
	if got := l.next(t0.Add(300 * time.Millisecond)); !got.Equal(t0.Add(time.Second)) {
		t.Fatalf("second message should wait for the interval, got %v", got.Sub(t0))
	}
	if got := l.next(t0.Add(2 * time.Second)); !got.Equal(t0.Add(2 * time.Second)) {
		t.Fatalf("after the interval it should go now, got %v", got.Sub(t0))
	}
}

func TestLimiterPerMinute(t *testing.T) {
	l := sendLimiter{limits: SendLimits{PerMinute: 3}}
	for i := 0; i < 3; i++ {
		l.record(t0.Add(time.Duration(i) * 10 * time.Second)) // 0s, 10s, 20s
	}
	// The 4th must wait until the first one (0s) leaves the window: 60s.
	if got := l.next(t0.Add(25 * time.Second)); !got.Equal(t0.Add(time.Minute)) {
		t.Fatalf("4th message: want +60s, got %v", got.Sub(t0))
	}
	if got := l.next(t0.Add(61 * time.Second)); !got.Equal(t0.Add(61 * time.Second)) {
		t.Fatalf("after the window it should go now, got %v", got.Sub(t0))
	}
}

func TestLimiterPerHourAndStats(t *testing.T) {
	l := sendLimiter{limits: SendLimits{PerHour: 2}}
	l.record(t0)
	l.record(t0.Add(30 * time.Minute))
	if got := l.next(t0.Add(40 * time.Minute)); !got.Equal(t0.Add(time.Hour)) {
		t.Fatalf("3rd message in the hour: want +60m, got %v", got.Sub(t0))
	}
	if m, h := l.stats(t0.Add(30*time.Minute + 30*time.Second)); m != 1 || h != 2 {
		t.Fatalf("stats: want 1 in the minute and 2 in the hour, got %d and %d", m, h)
	}
	// After 80 minutes the first send (0m) is forgotten and the second (30m) is kept.
	if _, h := l.stats(t0.Add(80 * time.Minute)); h != 1 {
		t.Fatalf("old sends should be pruned, got %d in the hour", h)
	}
}

func TestLimiterTakesTheStrictestLimit(t *testing.T) {
	l := sendLimiter{limits: SendLimits{MinInterval: 5 * time.Second, PerMinute: 2}}
	l.record(t0)
	l.record(t0.Add(50 * time.Second))
	// Interval says +55s, per-minute says +60s: the later one wins.
	if got := l.next(t0.Add(51 * time.Second)); !got.Equal(t0.Add(time.Minute)) {
		t.Fatalf("want +60s, got %v", got.Sub(t0))
	}
}

func TestLimiterNoLimits(t *testing.T) {
	l := sendLimiter{}
	for i := 0; i < 100; i++ {
		l.record(t0)
	}
	if got := l.next(t0); !got.Equal(t0) {
		t.Fatalf("without limits it should always go now, got %v", got.Sub(t0))
	}
}

func TestLimiterUnits(t *testing.T) {
	l := sendLimiter{limits: SendLimits{PerMinute: 10}}
	l.recordN(t0, 6)
	// 4 more fit now; 5 must wait until the 6 sent at t0 leave the window.
	if got := l.nextFor(t0.Add(time.Second), 4); !got.Equal(t0.Add(time.Second)) {
		t.Fatalf("4 units should go now, got %v", got.Sub(t0))
	}
	if got := l.nextFor(t0.Add(time.Second), 5); !got.Equal(t0.Add(time.Minute)) {
		t.Fatalf("5 units should wait for the window, got %v", got.Sub(t0))
	}
	if l.fits(11) || !l.fits(10) {
		t.Fatal("a request larger than the per-minute limit can never pass")
	}
	if !(&sendLimiter{}).fits(1000) {
		t.Fatal("without limits everything fits")
	}
}
