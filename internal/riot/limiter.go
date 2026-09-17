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
	//
	// The ceiling is authoritative across window periods. A request takes a
	// token from every window, so the effective rate is the tightest of the
	// windows present; the ceiling's windows are always among them. An
	// advertised window for a period the ceiling also configures is
	// clamped to the tighter of the two, and a period the ceiling does not
	// configure is kept as advertised but can only ever lower the rate. An
	// advertisement for a period this config has never seen - Riot's
	// production keys send `3000:10,180000:600` where the development key
	// says `20:1,100:120` - therefore cannot raise the rate above the
	// ceiling, and a header that omits a period altogether cannot drop the
	// ceiling window that covers it. See clampTo and Limiter.resolve.
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

// maxWait is the longest wait the limiter will ever ask a caller to sleep.
//
// Every other number in this file is derived from a response header, and a
// wait is the one output whose cost is unbounded wall-clock time: a balance
// written from a -Count header, or a window whose period a header chose, can
// otherwise produce a sleep measured in years. The crawl then stops without
// crashing and without an error to alert on, which is worse than a crash
// because nothing fires.
//
// The number is deliberate. A floored balance can only ask for one window's
// worth of tokens plus the one being reserved, and Riot's longest advertised
// window is 600s - the `180000:600` pair a production key sends - so no honest
// header asks for much more than ten minutes. 30 minutes clears that three
// times over, while a polluted header is still only made to pause: the wait
// ends, the reservation is taken again, and the crawl resumes.
const maxWait = 30 * time.Minute

// Limiter is a token-bucket limiter whose bucket sizes come from Riot's own
// response headers.
//
// The design point is that there is no rate constant anywhere in the request
// path: the buckets are resized from X-App-Rate-Limit and X-Method-Rate-Limit on
// every response, and the -Count headers are used to remove tokens the server
// says are already spent. A key that is upgraded mid-run therefore goes faster
// without a restart, and a key that is throttled slower without one either.
//
// The one constant is the ceiling, which is never dropped from the bucket set:
// Riot's headers decide the rate below it, never above it.
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

	// Defence in depth. The balances waitFor reads are floored where they are
	// written, so a sane window set cannot reach this, but the caller's sleep
	// is the one place a header could still cost unbounded real time.
	if wait > maxWait {
		wait = maxWait
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
		l.app = l.resolve(l.app, headers.AppLimit, headers.AppCount, now)
	}
	if headers.HasMethod {
		l.method = l.resolve(l.method, headers.MethodLimit, headers.MethodCount, now)
	}
	if headers.HasApplication || headers.HasMethod {
		l.advertised = true
	}
}

// resolve builds one scope's bucket set from what Riot advertised plus the
// configured ceiling, seeded from the counts the server reported.
//
// The set is the union of the two, which is what makes the ceiling
// authoritative. Every request takes a token from every bucket, so a window
// added to the set can only ever lower the effective rate - it can never raise
// it, however large the allowance it advertises. Two consequences are the point:
//
//   - a window Riot advertises for a period the ceiling does not configure
//     (a production key's `3000:10`) is kept, so it may tighten, but the
//     ceiling's own windows stay in the set, so the rate stays at or below the
//     ceiling instead of jumping to 300/s;
//   - a window Riot omits is not a window to forget. A response that mentions
//     only a one-second period leaves the ceiling's longer window in place.
//
// current is the scope's previous set; balances are carried over per period so
// that a window Riot reports no count for does not refill on every response.
func (l *Limiter) resolve(current []*bucket, advertised []Window, counts map[time.Duration]int, now time.Time) []*bucket {
	out := make([]*bucket, 0, len(advertised)+len(l.ceiling))
	added := make(map[time.Duration]bool, len(advertised)+len(l.ceiling))
	add := func(w Window) {
		if w.Limit <= 0 || w.Period <= 0 || added[w.Period] {
			return
		}
		added[w.Period] = true
		b := resize(current, w, now)
		if used, ok := counts[w.Period]; ok {
			// The server counts what was used in the window that is
			// already running; treating that as spent from now on is
			// pessimistic by up to one period and never optimistic,
			// which is the right direction to be wrong in.
			//
			// The count is floored at the debt reserve() itself allows,
			// because these headers report the key's *global* usage: any
			// other consumer of the key makes used exceed limit without
			// anyone behaving badly, and an unfloored balance would be
			// spent as years of wait rather than as one window of debt.
			// Bounding it here means the very first reservation after the
			// header is already bounded, not just later ones.
			observed := math.Max(float64(w.Limit-used), -b.limit)
			b.tokens = math.Min(b.tokens, observed)
		}
		out = append(out, b)
	}
	// Advertised windows first: for a period both sides configure, clampTo has
	// already reduced the advertisement to the tighter of the two, so the
	// ceiling's duplicate of that period must not be added again.
	for _, w := range advertised {
		if limit, ok := clampTo(w, l.ceiling); ok {
			w.Limit = limit
		}
		add(w)
	}
	for _, c := range l.ceiling {
		add(c)
	}
	return out
}

// resize returns the bucket for w, reusing the balance of the current set's
// bucket for the same period when there is one. Resizing must not refill: a
// window the server reports no count for would otherwise be handed back its
// whole allowance on every single response, which is how a ceiling window ends
// up inert even though it is in the set.
func resize(current []*bucket, w Window, now time.Time) *bucket {
	for _, b := range current {
		if b.period != w.Period {
			continue
		}
		b.refill(now)
		b.limit = float64(w.Limit)
		b.tokens = math.Min(b.tokens, b.limit)
		return b
	}
	return newBucket(w, now)
}

// clampTo applies the ceiling to one advertised window. A window whose period
// matches a ceiling window is reduced to the ceiling's limit. A window with a
// period no ceiling window shares is returned unchanged: it is still added to
// the bucket set, where it can only lower the rate, so trusting the number Riot
// sent for a period this config does not know about cannot spend more than the
// ceiling allows.
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
