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

// allow blocks while the breaker cools down and refuses outright once it has
// stopped.
//
// Sleeping the cooldown out is right for a bump - a burst of 429s, a blip at
// Riot - because the caller only needs to be slowed down. It is wrong for a
// sustained failure: fail used to clear the consecutive count on every trip, so
// sleeping meant the client issued exactly `threshold` more requests every
// `maxWait` for as long as the key stayed broken, which is the pattern errors.go
// exists to prevent. Once the backoff has saturated at maxWait - which an auth
// failure reaches on its first trip - allow returns ErrCircuitOpen instead, so
// the caller decides when to come back rather than the breaker sleeping on its
// behalf.
func (b *breaker) allow(ctx context.Context) error {
	for {
		wait, stop := b.state()
		if wait <= 0 {
			return ctx.Err()
		}
		if stop {
			return ErrCircuitOpen
		}
		if err := b.clock.Sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// state reports how long the breaker stays closed to new work and whether it has
// stopped rather than paused. A stopped breaker refuses calls instead of sleeping
// them out: its backoff has nothing left to grow into, so the wait is not a
// cooldown that will end on its own.
func (b *breaker) state() (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock.Now()
	if !b.openUntil.After(now) {
		return 0, false
	}
	return b.openUntil.Sub(now), b.backoff >= b.maxWait
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

// fail records a transient failure - a 429 burst, a transport error - and trips
// the breaker at the threshold. It returns whether this call was the one that
// tripped it, which is what the client logs.
func (b *breaker) fail() bool { return b.record(false) }

// failAuth records a refusal of the key itself. It is separate from fail because
// a 403 almost always means the key is wrong, revoked or banned, and that does
// not clear itself in seconds: calling again with it is how a project loses
// access for good. So the first auth failure that trips the breaker goes
// straight to the longest wait instead of climbing towards it.
func (b *breaker) failAuth() bool { return b.record(true) }

func (b *breaker) record(auth bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutive++
	if b.consecutive < b.threshold {
		return false
	}
	switch {
	case auth:
		b.backoff = b.maxWait
	case b.backoff <= 0:
		b.backoff = min(b.cooldown, b.maxWait)
	case b.backoff < b.maxWait:
		// Doubling is what turns "we are being throttled" from a busy
		// loop into a wait, while still probing often enough that a
		// recovered key is noticed within a couple of minutes.
		b.backoff = min(b.backoff*2, b.maxWait)
	}
	b.openUntil = b.clock.Now().Add(b.backoff)
	b.trips++
	// consecutive is deliberately left alone. Clearing it here was what let each
	// cycle admit another `threshold` requests, so a key that never worked again
	// produced an endless drip of them. Only a successful response clears the
	// count now (succeed), which leaves the brief-bump case as it was: one good
	// reply reopens the crawler, and a run of bad ones stops it.
	return true
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
