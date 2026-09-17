package riot

import (
	"context"
	"sync"
	"time"
)

// Clock is the client's view of time and of waiting. Rate limiting is the one
// place in the pipeline where correctness is a function of wall-clock time, so
// it is the one place where the clock is injected: a test can then assert the
// exact spacing the limiter demands without sleeping for it, and the few tests
// whose subject really is the wait can use the real clock and say so.
type Clock interface {
	Now() time.Time
	// Sleep returns ctx.Err() if the wait was cut short, and must not return
	// before the duration has elapsed on a cancelled context.
	Sleep(ctx context.Context, d time.Duration) error
}

// RealClock is the production clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// FakeClock is a clock that advances only when something sleeps on it. It is
// exported because the crawler's tests need the same time base as the client's.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	slept  time.Duration
	sleeps []time.Duration
}

// NewFakeClock starts at a fixed instant. A zero time.Time is a poor test base
// because every arithmetic mistake on it looks like a plausible date.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep advances virtual time by d. A test that wants to observe "the crawler
// asked to wait" rather than "the crawler waited" reads Total or Sleeps.
func (c *FakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.now = c.now.Add(d)
		c.slept += d
		c.sleeps = append(c.sleeps, d)
	}
	return nil
}

// Advance moves the clock without recording a sleep, for tests that need time
// to pass between two observations.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Total is the accumulated sleep time.
func (c *FakeClock) Total() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.slept
}

// Sleeps is a copy of the individual sleep durations, in order.
func (c *FakeClock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.sleeps))
	copy(out, c.sleeps)
	return out
}
