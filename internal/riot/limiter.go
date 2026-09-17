package riot

import (
	"context"
	"math"
	"sync"
	"time"
)

// LimiterOptions configures the adaptive token-bucket limiter.
type LimiterOptions struct {
	// Clock is the injected time base. Nil means the real clock.
	Clock Clock

	// Ceiling is the highest rate the limiter will ever run at, whatever a
	// response advertises. It is a safety cap, not the operating rate: a
	// stale, shared or misread header that claims a larger allowance must
	// not be able to spend the key's real budget. Zero means the
	// development-key defaults.
	Ceiling []Window

	// Bootstrap is the window set used before the first response of a run
	// arrives. Zero means "same as the ceiling", which is the conservative
	// choice: a fresh process starts slow and speeds up once Riot has told
	// it what it may do.
	Bootstrap []Window

	// MaxRetryAfter caps what a single Retry-After is allowed to cost. A
	// response asking for an hour of silence is a bug or a revocation, and
	// neither is served by a worker that quietly stalls for an hour.
	MaxRetryAfter time.Duration
}

const defaultMaxRetryAfter = 5 * time.Minute

// Limiter is a token-bucket limiter whose bucket sizes come from Riot's own
// response headers.
//
// The design point is that there is no rate constant anywhere in the request
// path: the buckets are resized from X-App-Rate-Limit and X-Method-Rate-Limit on
// every response, and the -Count headers are used to remove tokens the server
// says are already spent. A key that is upgraded mid-run therefore goes faster
// without a restart, and a key that is throttled slower without one either.
type Limiter struct {
	clock         Clock
	ceiling       []Window
	maxRetryAfter time.Duration

	mu           sync.Mutex
	app          []*bucket
	method       []*bucket
	blockedUntil time.Time
	advertised   bool
}

// bucket is one token bucket. Tokens are fractional so a limit that does not
// divide evenly into a second (Riot's 100-per-2-minutes, for instance) is
// represented exactly rather than rounded into a burst.
type bucket struct {
	limit   float64
	period  time.Duration
	tokens  float64
	lastSet time.Time
}

func newBucket(w Window, now time.Time) *bucket {
	return &bucket{limit: float64(w.Limit), period: w.Period, tokens: float64(w.Limit), lastSet: now}
}

func (b *bucket) rate() float64 { return b.limit / b.period.Seconds() }

func (b *bucket) refill(at time.Time) {
	elapsed := at.Sub(b.lastSet)
	if elapsed <= 0 {
		return
	}
	b.lastSet = at
	b.tokens = math.Min(b.limit, b.tokens+elapsed.Seconds()*b.rate())
}

// waitFor is how long until this bucket has a whole token.
func (b *bucket) waitFor() time.Duration {
	if b.tokens >= 1 {
		return 0
	}
	seconds := (1 - b.tokens) / b.rate()
	return time.Duration(math.Ceil(seconds * float64(time.Second)))
}

// NewLimiter builds a limiter that starts at the bootstrap windows.
func NewLimiter(opts LimiterOptions) *Limiter {
	clock := opts.Clock
	if clock == nil {
		clock = RealClock{}
	}
	ceiling := opts.Ceiling
	if len(ceiling) == 0 {
		ceiling = DevelopmentKeyWindows()
	}
	bootstrap := opts.Bootstrap
	if len(bootstrap) == 0 {
		bootstrap = ceiling
	}
	maxRetryAfter := opts.MaxRetryAfter
	if maxRetryAfter <= 0 {
		maxRetryAfter = defaultMaxRetryAfter
	}
	now := clock.Now()
	return &Limiter{
		clock:         clock,
		ceiling:       ceiling,
		maxRetryAfter: maxRetryAfter,
		app:           newBuckets(bootstrap, now),
	}
}

func newBuckets(windows []Window, now time.Time) []*bucket {
	out := make([]*bucket, 0, len(windows))
	for _, w := range windows {
		if w.Limit <= 0 || w.Period <= 0 {
			continue
		}
		out = append(out, newBucket(w, now))
	}
	return out
}

// Wait blocks until the limiter will allow one more request. It returns the
// context error if the caller gave up first.
func (l *Limiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	wait := l.reserve()
	if wait <= 0 {
		return nil
	}
	return l.clock.Sleep(ctx, wait)
}

// reserve consumes a token in every window and returns how long the caller must
// wait first. Tokens for future instants are reserved by advancing the bucket to
// the time of the call, which is what keeps two concurrent workers from both
// deciding that the same token is free.
func (l *Limiter) reserve() time.Duration {
	now := l.clock.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	wait := time.Duration(0)
	if l.blockedUntil.After(now) {
		wait = l.blockedUntil.Sub(now)
	}
	for _, b := range l.allBuckets() {
		b.refill(now)
		if w := b.waitFor(); w > wait {
			wait = w
		}
	}

	at := now.Add(wait)
	for _, b := range l.allBuckets() {
		b.refill(at)
		// Negative balances are allowed and expected: they are how the
		// bucket says "you have already borrowed the next token, wait
		// longer". They are floored at one window's worth so a single
		// burst cannot push the fleet several windows into debt.
		b.tokens = math.Max(b.tokens-1, -b.limit)
	}
	return wait
}

func (l *Limiter) allBuckets() []*bucket {
	out := make([]*bucket, 0, len(l.app)+len(l.method))
	out = append(out, l.app...)
	out = append(out, l.method...)
	return out
}

// Observe resizes the limiter from a response's headers. It is called on every
// response, including failures: a 429 is exactly when the advertised numbers
// matter most.
func (l *Limiter) Observe(headers rateLimitHeaders) {
	now := l.clock.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	// A missing header means "Riot said nothing about this scope on this
	// response", so the previous knowledge for that scope stands. Replacing
	// it with nothing would silently remove the only limit we know about.
	if headers.HasApplication {
		l.app = l.resolve(headers.AppLimit, headers.AppCount, now)
	}
	if headers.HasMethod {
		l.method = l.resolve(headers.MethodLimit, headers.MethodCount, now)
	}
	if headers.HasApplication || headers.HasMethod {
		l.advertised = true
	}
}

// resolve clamps the advertised windows to the ceiling and seeds each bucket's
// balance from the count the server reported.
func (l *Limiter) resolve(advertised []Window, counts map[time.Duration]int, now time.Time) []*bucket {
	out := make([]*bucket, 0, len(advertised))
	for _, w := range advertised {
		if limit, ok := clampTo(w, l.ceiling); ok {
			w.Limit = limit
		}
		b := newBucket(w, now)
		if used, ok := counts[w.Period]; ok {
			// The server counts what was used in the window that is
			// already running; treating that as spent from now on is
			// pessimistic by up to one period and never optimistic,
			// which is the right direction to be wrong in.
			b.tokens = math.Min(b.tokens, float64(w.Limit-used))
		}
		out = append(out, b)
	}
	return out
}

// clampTo applies the ceiling to one advertised window. Only a window with the
// same period as a ceiling window is clamped; an unknown period is trusted as
// advertised, because the alternative - inventing a limit Riot did not send -
// would throttle a key that is entitled to more.
func clampTo(w Window, ceiling []Window) (int, bool) {
	for _, c := range ceiling {
		if c.Period == w.Period && w.Limit > c.Limit {
			return c.Limit, true
		}
	}
	return w.Limit, false
}

// Penalize suspends all requests for d, which is how a 429's Retry-After is
// turned into actual silence rather than into a retry storm.
func (l *Limiter) Penalize(d time.Duration) {
	if d <= 0 {
		return
	}
	if d > l.maxRetryAfter {
		d = l.maxRetryAfter
	}
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if until := now.Add(d); until.After(l.blockedUntil) {
		l.blockedUntil = until
	}
}

// Blocked reports how much longer requests are suspended, and whether any
// advertised limit has ever been seen. Both are used for logging: a worker that
// is silent and says why is debuggable, one that is silent and does not is not.
func (l *Limiter) Blocked() time.Duration {
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.blockedUntil.After(now) {
		return 0
	}
	return l.blockedUntil.Sub(now)
}

// Advertised reports whether the limiter has been driven by real headers yet.
func (l *Limiter) Advertised() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.advertised
}

// EffectiveRate is the number of requests per second the tightest window
// allows. It is what the worker logs so an operator can see the crawler react.
func (l *Limiter) EffectiveRate() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	rate := math.Inf(1)
	buckets := l.allBuckets()
	if len(buckets) == 0 {
		return 0
	}
	for _, b := range buckets {
		rate = math.Min(rate, b.rate())
	}
	return rate
}
