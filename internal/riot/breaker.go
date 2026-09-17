package riot

import (
	"context"
	"sync"
	"time"
)

// breaker is a circuit breaker over repeated authorisation and rate-limit
// failures.
//
// Its job is narrower than "stop calling Riot when things go wrong". A 403 from
// Riot almost always means the key is wrong, revoked, or banned - and a banned
// key that keeps being used is how a project loses its access permanently. So
// the breaker stops the crawler, and every retry afterwards costs a wait rather
// than a request.
type breaker struct {
	clock     Clock
	threshold int
	cooldown  time.Duration
	maxWait   time.Duration

	mu          sync.Mutex
	consecutive int
	openUntil   time.Time
	backoff     time.Duration
	trips       int
}

func newBreaker(clock Clock, threshold int, cooldown, maxWait time.Duration) *breaker {
	if threshold <= 0 {
		threshold = 5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	if maxWait <= 0 {
		maxWait = 2 * time.Minute
	}
	return &breaker{clock: clock, threshold: threshold, cooldown: cooldown, maxWait: maxWait}
}

// allow waits out an open breaker and returns ErrCircuitOpen when the remaining
// wait is longer than the caller agreed to block for.
func (b *breaker) allow(ctx context.Context) error {
	for {
		wait := b.remaining()
		if wait <= 0 {
			return ctx.Err()
		}
		if wait > b.maxWait {
			return ErrCircuitOpen
		}
		if err := b.clock.Sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (b *breaker) remaining() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock.Now()
	if !b.openUntil.After(now) {
		return 0
	}
	return b.openUntil.Sub(now)
}

// fail records a failure and trips the breaker at the threshold. It returns
// whether this call was the one that tripped it, which is what the client logs.
func (b *breaker) fail() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutive++
	if b.consecutive < b.threshold {
		return false
	}
	if b.backoff <= 0 {
		b.backoff = b.cooldown
	} else if b.backoff < b.maxWait {
		// Doubling is what turns "we are being throttled" from a busy
		// loop into a wait, while still probing often enough that a
		// recovered key is noticed within a couple of minutes.
		b.backoff = min(b.backoff*2, b.maxWait)
	}
	tripped := true
	b.openUntil = b.clock.Now().Add(b.backoff)
	b.trips++
	b.consecutive = 0
	return tripped
}

// succeed closes the breaker. One good response is enough: the alternative -
// requiring a run of them - would keep the crawler throttled after a blip.
func (b *breaker) succeed() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutive = 0
	b.openUntil = time.Time{}
	b.backoff = 0
}

// Trips is how many times the breaker has opened, for logging.
func (b *breaker) Trips() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.trips
}
