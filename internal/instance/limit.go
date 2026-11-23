package instance

import (
	"fmt"
	"time"
)

// SendLimits is the sending pace of every instance. Each instance has its own counters: a busy
// number never slows down the others.
type SendLimits struct {
	MinInterval time.Duration // minimum time between two messages
	PerMinute   int           // most messages in any 60-second window (0 = no limit)
	PerHour     int           // most messages in any 60-minute window (0 = no limit)
	QueueMax    int           // most requests waiting to send; more are refused (0 = no limit)
}

// RateLimitError means the message was not sent because of the instance's sending limits.
// Nothing reached WhatsApp: the caller can safely send again after RetryAfter.
type RateLimitError struct {
	Reason     string
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("%s; try again in %s", e.Reason, e.RetryAfter.Round(time.Second))
}

// SendStats is the current sending activity of an instance.
type SendStats struct {
	Queued     int      `json:"queued"`     // requests waiting for their turn
	LastMinute int      `json:"lastMinute"` // messages sent in the last 60 seconds
	LastHour   int      `json:"lastHour"`   // messages sent in the last 60 minutes
	Today      DayStats `json:"today"`      // the day's counters and limits (daily.go)
}

// sendLimiter keeps the send times of one instance (the last hour of them) and answers when
// the next message may go. It is not safe for concurrent use: the caller holds a lock.
type sendLimiter struct {
	limits SendLimits
	sent   []time.Time // oldest first
}

// next returns the earliest time the next message may be sent without breaking any limit.
func (l *sendLimiter) next(now time.Time) time.Time {
	return l.nextFor(now, 1)
}

// nextFor is next for a request that counts as units messages (adding 20 people to a group
// counts as 20). Every limit only gets easier to meet as time passes, so the latest of the
// individual answers satisfies all of them. The caller checks fits first.
func (l *sendLimiter) nextFor(now time.Time, units int) time.Time {
	l.prune(now)
	at := now
	if n := len(l.sent); n > 0 && l.limits.MinInterval > 0 {
		at = later(at, l.sent[n-1].Add(l.limits.MinInterval))
	}
	at = later(at, l.windowFree(now, time.Minute, l.limits.PerMinute, units))
	at = later(at, l.windowFree(now, time.Hour, l.limits.PerHour, units))
	return at
}

// fits reports whether a request of units messages can ever pass the limits.
func (l *sendLimiter) fits(units int) bool {
	return (l.limits.PerMinute <= 0 || units <= l.limits.PerMinute) && (l.limits.PerHour <= 0 || units <= l.limits.PerHour)
}

// windowFree returns when the window ending at that moment has room for units more messages.
func (l *sendLimiter) windowFree(now time.Time, window time.Duration, limit, units int) time.Time {
	if limit <= 0 {
		return now
	}
	first := l.firstAfter(now.Add(-window))
	count := len(l.sent) - first
	if count+units <= limit {
		return now
	}
	// The oldest (count + units - limit) messages must leave the window.
	return l.sent[first+count+units-limit-1].Add(window)
}

// record notes a message sent at t.
func (l *sendLimiter) record(t time.Time) {
	l.recordN(t, 1)
}

// recordN notes a request that counted as units messages.
func (l *sendLimiter) recordN(t time.Time, units int) {
	for i := 0; i < units; i++ {
		l.sent = append(l.sent, t)
	}
}

// stats counts the messages sent in the last minute and hour.
func (l *sendLimiter) stats(now time.Time) (lastMinute, lastHour int) {
	l.prune(now)
	return len(l.sent) - l.firstAfter(now.Add(-time.Minute)), len(l.sent)
}

// prune forgets sends older than one hour, the longest window.
func (l *sendLimiter) prune(now time.Time) {
	if i := l.firstAfter(now.Add(-time.Hour)); i > 0 {
		l.sent = append(l.sent[:0], l.sent[i:]...)
	}
}

// firstAfter returns the index of the first send after t (len(sent) when none).
func (l *sendLimiter) firstAfter(t time.Time) int {
	for i, s := range l.sent {
		if s.After(t) {
			return i
		}
	}
	return len(l.sent)
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
