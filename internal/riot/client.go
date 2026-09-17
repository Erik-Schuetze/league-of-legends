package riot

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
)

// Method labels. They are the `method` label on the Riot metrics, so they must
// stay low-cardinality and match the API names the rest of the project uses.
const (
	MethodMatch         = "match"
	MethodMatchIDs      = "match-ids"
	MethodLeagueEntries = "league-entries"
	MethodAccount       = "account"
)

// Environment overrides for the API hosts. The production values are the real
// Riot routes; the overrides exist so the entire crawler can be pointed at a
// local fake Riot server, which is the only way this pipeline can be verified
// without a key. They are read here rather than in internal/config because the
// config contract is frozen and this is a testing seam, not a product setting.
const (
	EnvPlatformBaseURL = "LOLSTATS_RIOT_PLATFORM_BASE_URL"
	EnvRegionalBaseURL = "LOLSTATS_RIOT_REGIONAL_BASE_URL"
)

// BaseURLs resolves the platform (euw1) and regional (europe) hosts for the
// configured routes, honouring the environment overrides.
func BaseURLs(platformRoute, regionalRoute string) (platform, regional string) {
	platform = "https://" + strings.ToLower(strings.TrimSpace(platformRoute)) + ".api.riotgames.com"
	regional = "https://" + strings.ToLower(strings.TrimSpace(regionalRoute)) + ".api.riotgames.com"
	if v := strings.TrimSpace(os.Getenv(EnvPlatformBaseURL)); v != "" {
		platform = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(os.Getenv(EnvRegionalBaseURL)); v != "" {
		regional = strings.TrimRight(v, "/")
	}
	return platform, regional
}

// Options configures the client. Zero values are replaced by the defaults
// documented on each field.
type Options struct {
	PlatformBaseURL string
	RegionalBaseURL string

	// Timeout bounds one HTTP round trip, including reading the body. A match
	// summary is around 100 KB, so ten seconds is generous; a request that
	// has not finished by then is a request that is not going to.
	//
	// It deliberately does not bound the whole call. Riot's Retry-After is an
	// instruction not to send yet, not a slow round trip, and a call whose
	// entire budget was ten seconds could never pay it: see RetryWaitBudget.
	Timeout time.Duration

	// RetryWaitBudget bounds the waiting one call may do on top of Timeout:
	// Riot's own Retry-After, which is the number that actually arrives on a
	// development key.
	//
	// The two clocks are different and conflating them was a real defect. A
	// development key answers 429 with `Retry-After: 15-16s`, while the
	// deployed per-call deadline is ten seconds, so every throttled call was
	// released with "retry-after outlasts the call's own deadline", every row
	// was requeued, and no pass ever converged. Sixty seconds clears the
	// measured figure several times over and matches the crawler's own
	// maxRateLimitPause. A Retry-After longer than this is Riot disciplining
	// the key, and the call is released with the suspension attached so its
	// caller can schedule the row past it instead of shortening the wait until
	// the key is refused outright.
	RetryWaitBudget time.Duration
	// MaxAttempts counts the first try. It bounds 5xx retries, 429 waits and
	// transport failures alike.
	MaxAttempts int
	// MaxBodyBytes caps what one response may decompress to. Untrusted
	// length combined with gzip is a memory-exhaustion vector, and the
	// whole point of the queue is that a bad response must not take the
	// worker with it.
	MaxBodyBytes int64

	Clock       Clock
	Limiter     *Limiter
	KeyProvider *KeyProvider
	Metrics     obs.MetricsRecorder
	Logger      *slog.Logger
	HTTPClient  *http.Client

	BreakerThreshold int
	BreakerCooldown  time.Duration
	BreakerMaxWait   time.Duration

	BackoffBase time.Duration
	BackoffMax  time.Duration

	UserAgent string
}

const (
	defaultTimeout      = 10 * time.Second
	defaultMaxAttempts  = 5
	defaultMaxBodyBytes = 64 << 20
	defaultBackoffBase  = 500 * time.Millisecond
	defaultBackoffMax   = 30 * time.Second
	defaultUserAgent    = "lolstats-ingest/1.0 (+https://github.com/Erik-Schuetze/league-of-legends)"
)

// DefaultRetryWaitBudget is the exported form of the retry wait budget, so a
// caller that owns the surrounding deadline - the crawl worker's job timeout -
// can size itself against it instead of guessing. See Options.RetryWaitBudget.
const DefaultRetryWaitBudget = 60 * time.Second

// DefaultTimeout is the exported per-attempt deadline, for the same reason.
const DefaultTimeout = defaultTimeout

// Client is the Riot HTTP client. It satisfies contract.RiotClient through the
// adapter in internal/crawl, which exists because internal/contract already
// imports this package for the DTOs and could not be imported back.
type Client struct {
	opts    Options
	http    *http.Client
	limiter *Limiter
	keys    *KeyProvider
	breaker *breaker
	log     *slog.Logger
	metrics obs.MetricsRecorder
}

// NewClient validates the options and returns a client with its own limiter and
// breaker unless the caller supplied them.
func NewClient(opts Options) (*Client, error) {
	var missing []string
	if strings.TrimSpace(opts.PlatformBaseURL) == "" {
		missing = append(missing, "platform base URL")
	}
	if strings.TrimSpace(opts.RegionalBaseURL) == "" {
		missing = append(missing, "regional base URL")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("riot: client needs %s", strings.Join(missing, ", "))
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.RetryWaitBudget <= 0 {
		opts.RetryWaitBudget = DefaultRetryWaitBudget
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaultMaxAttempts
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = defaultMaxBodyBytes
	}
	if opts.BackoffBase <= 0 {
		opts.BackoffBase = defaultBackoffBase
	}
	if opts.BackoffMax <= 0 {
		opts.BackoffMax = defaultBackoffMax
	}
	if opts.UserAgent == "" {
		opts.UserAgent = defaultUserAgent
	}
	if opts.Clock == nil {
		opts.Clock = RealClock{}
	}
	if opts.Limiter == nil {
		opts.Limiter = NewLimiter(LimiterOptions{Clock: opts.Clock})
	}
	if opts.KeyProvider == nil {
		opts.KeyProvider = NewKeyProvider()
	}
	if opts.Metrics == nil {
		opts.Metrics = obs.NopRecorder{}
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DisableCompression = true // the client decodes gzip itself
		httpClient = &http.Client{Transport: transport}
	}
	httpClient = &http.Client{
		Transport: httpClient.Transport,
		// No global client Timeout: the per-request context carries the
		// deadline so a long backfill can set its own.
		CheckRedirect: httpClient.CheckRedirect,
		Jar:           httpClient.Jar,
	}

	return &Client{
		opts:    opts,
		http:    httpClient,
		limiter: opts.Limiter,
		keys:    opts.KeyProvider,
		breaker: newBreaker(opts.Clock, opts.BreakerThreshold, opts.BreakerCooldown, opts.BreakerMaxWait),
		log:     log,
		metrics: opts.Metrics,
	}, nil
}

// Limiter exposes the adaptive limiter so the worker can log and react to what
// the client has learned from Riot.
func (c *Client) Limiter() *Limiter { return c.limiter }

// Keys exposes the key provider for age reporting.
func (c *Client) Keys() *KeyProvider { return c.keys }

// Key reports the key to send and whether one exists yet. It is the surface the
// crawl worker probes to decide whether crawling is possible at all: a worker
// that cannot see this method claims queue rows it is unable to fetch and
// spends their attempts, which is how a keyless process slowly dead-letters a
// backlog it was supposed to leave alone.
func (c *Client) Key() (string, bool) {
	if c.keys == nil {
		return "", false
	}
	return c.keys.Key()
}

// BreakerTrips is how many times the circuit breaker has opened.
func (c *Client) BreakerTrips() int { return c.breaker.Trips() }

// Blocked implements the crawl worker's Pacer: it reports how long the client
// intends to wait before sending anything, and whether it is waiting at all.
// Both suspensions count, because a worker that keeps claiming rows during
// either one converts a rate limit into a burst of requeues.
func (c *Client) Blocked() (time.Duration, bool) {
	wait := time.Duration(0)
	if c.limiter != nil {
		wait = c.limiter.Blocked()
	}
	if c.breaker != nil {
		if d := c.breaker.remaining(); d > wait {
			wait = d
		}
	}
	return wait, wait > 0
}

// Advertised implements the crawl worker's Pacer.
func (c *Client) Advertised() bool {
	if c.limiter == nil {
		return false
	}
	return c.limiter.Advertised()
}

// EffectiveRate implements the crawl worker's Pacer.
func (c *Client) EffectiveRate() float64 {
	if c.limiter == nil {
		return 0
	}
	return c.limiter.EffectiveRate()
}

func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

// endpoint is one Riot API call.
type endpoint struct {
	method string // metrics/log label
	base   string // platform or regional host
	path   string
	query  url.Values
}

func (e endpoint) url() string {
	u := strings.TrimRight(e.base, "/") + e.path
	if len(e.query) > 0 {
		u += "?" + e.query.Encode()
	}
	return u
}

// do performs one API call, applying the limiter, the breaker, retries and the
// body limit.
//
// Two deadlines are in play and they mean different things. Every HTTP round
// trip is bounded by Timeout on its own; the deliberate waiting between
// attempts - Riot's Retry-After, which is an instruction not to send yet - is
// charged against RetryWaitBudget instead. The outer deadline below is only
// their sum, so that a call can pay a 429 without either spending a job's whole
// time budget on it or giving up on the wait entirely.
func (c *Client) do(ctx context.Context, e endpoint) ([]byte, error) {
	requestURL := e.url()
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, c.opts.Timeout+c.opts.RetryWaitBudget)
	defer cancel()

	// paid is the deliberate waiting this call has already done. A Retry-After
	// is only payable while adding it stays inside the budget.
	var paid time.Duration

	var lastErr error
	for attempt := 1; attempt <= c.opts.MaxAttempts; attempt++ {
		if err := c.breaker.allow(ctx); err != nil {
			return nil, fmt.Errorf("riot %s: %w", e.method, err)
		}
		key, ok := c.keys.Key()
		if !ok {
			return nil, ErrNoAPIKey
		}
		// One attempt gets one round trip's worth of time, not the whole call's
		// budget: a per-call deadline on the HTTP request would let a stalled
		// connection sit there for the entire retry budget.
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, c.opts.Timeout)
		if err := c.limiter.Wait(attemptCtx); err != nil {
			// The limiter holds the call until the next advertised slot. If
			// that is further away than this call's deadline, the wait ends
			// here rather than being paid for; the row is told which of the two
			// deadlines expired so it can wait out the limiter instead of
			// coming back into it.
			cancelAttempt()
			return nil, c.ownDeadline(parent, err, e.method)
		}

		body, status, header, err := c.attempt(attemptCtx, requestURL, e.method, key)
		// Read the cause before cancelling: cancel() would overwrite it with
		// context.Canceled and every transport failure would then be reported
		// as an expired deadline.
		cause := context.Cause(attemptCtx)
		cancelAttempt()
		if err != nil {
			if cause != nil {
				return nil, c.ownDeadline(parent, cause, e.method)
			}
			c.metrics.IncRiotRetry(e.method, "transport")
			lastErr = err
			if attempt == c.opts.MaxAttempts {
				break
			}
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return nil, err
			}
			continue
		}

		switch {
		case status >= 200 && status < 300:
			c.breaker.succeed()
			return body, nil

		case status == http.StatusTooManyRequests:
			// Retry-After is the contract with Riot: honour it, and by
			// penalising the shared limiter, honour it for every other
			// worker too. Without that, four concurrent workers each wait
			// out the same 429 and then arrive together.
			wait, suspended := c.rateLimitedWait(
				ParseRetryAfter(header.Get(headerRetryAfter), c.opts.Clock.Now()),
				c.backoff(attempt),
				c.opts.RetryWaitBudget-paid)
			c.limiter.Penalize(wait)
			c.metrics.IncRiotRetry(e.method, "429")
			c.breaker.fail()
			limited := &RateLimitedError{
				Method:     e.method,
				Attempts:   attempt,
				RetryAfter: wait,
				Suspended:  suspended,
			}
			lastErr = limited
			c.log.Warn("riot rate limited",
				"method", e.method, "attempt", attempt, "retry_after", wait.String())
			if suspended {
				// The wait is longer than a single call may spend. Sleeping
				// until the call's own deadline and then reporting the deadline
				// is what turned a 429 into a shutdown, and the deadline is
				// only why the answer arrived early. The suspension is
				// recorded on the limiter, which is what the caller reads to
				// schedule the row past it, so the call ends here.
				c.log.Warn("retry-after is longer than this call's retry wait budget; the call is released",
					"method", e.method, "retry_after", wait.String(),
					"budget", c.opts.RetryWaitBudget.String(), "already_waited", paid.String())
				return nil, limited
			}
			if attempt == c.opts.MaxAttempts {
				break
			}
			paid += wait
			if err := c.sleep(ctx, wait); err != nil {
				if context.Cause(parent) != nil {
					// The run itself is going away: the caller has to hear
					// that, because it decides between handing the row back
					// and retrying it on a schedule.
					return nil, c.ownDeadline(parent, err, e.method)
				}
				// This call ran out of budget while sleeping. The 429 is
				// still the answer, and it still carries the suspension.
				limited.Suspended = true
				return nil, limited
			}
			continue

		case status >= 500:
			c.metrics.IncRiotRetry(e.method, "5xx")
			lastErr = &StatusError{Method: e.method, Status: status, Body: snippet(body)}
			if attempt == c.opts.MaxAttempts {
				break
			}
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return nil, err
			}
			continue

		default:
			// 403 is the one client error worth tripping on: it is what
			// Riot returns for a revoked, banned or wrong key, and
			// continuing to call with it is how access is lost.
			if status == http.StatusForbidden {
				if c.breaker.failAuth() {
					c.log.Error("riot API refused the key repeatedly; backing off",
						"method", e.method, "status", status, "trips", c.breaker.Trips())
				}
			}
			return nil, &StatusError{Method: e.method, Status: status, Body: snippet(body)}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("riot %s: no attempt was made", e.method)
	}
	var limited *RateLimitedError
	if errors.As(lastErr, &limited) && !limited.Suspended && ctx.Err() != nil {
		// The call's budget expired while this call was still waiting out a
		// Retry-After. The sleep returns the context's error rather than the
		// deadline itself, so the mark is made here as well, where every exit
		// from the loop passes.
		limited.Suspended = true
	}
	return nil, lastErr
}

// rateLimitedWait decides how long to wait after a 429, and whether that wait
// fits in what is left of the call's retry wait budget.
//
// Riot's Retry-After is not capped here: a long one is how a key is
// disciplined, and the client's job is to report it (the limiter caps what it
// will hold, and the crawler caps what it will pause for) rather than to shorten
// it until the key is refused outright. What is capped is how long *this* call
// will sit on it: past the budget the wait cannot be paid here, so the call is
// released with the suspension attached and the caller schedules around it.
func (c *Client) rateLimitedWait(after, fallback, remaining time.Duration) (time.Duration, bool) {
	wait := after
	if wait <= 0 {
		wait = fallback
	}
	return wait, wait > remaining
}

// ownDeadline decides what to return when a call failed for a reason one of the
// two deadlines on its context explains.
//
// Two deadlines are on that context and they mean different things to the queue:
// the per-call timeout says "this row ran out of time, retry it on schedule",
// while the run's cancellation says "this process is going away, hand the row
// back at once". Returning the bare deadline for both made the worker report a
// shutdown that never happened. The run's context is checked first, so a real
// shutdown still reads as one.
func (c *Client) ownDeadline(parent context.Context, err error, method string) error {
	if parentErr := context.Cause(parent); parentErr != nil {
		return parentErr
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("riot %s: call deadline exceeded: %w", method, err)
	}
	return err
}

// attempt performs a single HTTP round trip and normalises its outcome into
// body, status and headers. A non-nil error means "no usable response"; the
// caller decides whether that is worth another attempt.
func (c *Client) attempt(ctx context.Context, requestURL, method, key string) ([]byte, int, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("riot %s: build request: %w", method, err)
	}
	req.Header.Set("X-Riot-Token", key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("User-Agent", c.opts.UserAgent)

	started := c.opts.Clock.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("riot %s: %w", method, err)
	}
	body, readErr := readBody(resp, c.opts.MaxBodyBytes)
	status := resp.StatusCode
	header := resp.Header.Clone()
	closeErr := resp.Body.Close()
	c.metrics.ObserveRiotRequest(method, status, c.opts.Clock.Now().Sub(started).Seconds())

	// The limiter is updated from every response, success or failure. A 429
	// is precisely the response whose headers matter most.
	c.limiter.Observe(readRateLimitHeaders(header))

	if readErr != nil {
		return nil, status, header, fmt.Errorf("riot %s: read body: %w", method, readErr)
	}
	if closeErr != nil {
		return nil, status, header, fmt.Errorf("riot %s: close body: %w", method, closeErr)
	}
	return body, status, header, nil
}

// readBody reads at most max+1 bytes so that exceeding the limit is detectable
// rather than silently truncating a payload we would then archive as if it were
// complete.
func readBody(resp *http.Response, max int64) ([]byte, error) {
	var reader io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		defer func() { _ = zr.Close() }()
		reader = zr
	}
	body, err := io.ReadAll(io.LimitReader(reader, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("response body larger than %d bytes", max)
	}
	return body, nil
}

// backoff is exponential in the attempt number with equal jitter: the delay is
// between half and all of the nominal value. Full jitter can produce near-zero
// waits, which makes a "did it back off" test flaky and lets a fleet of workers
// re-synchronise.
func (c *Client) backoff(attempt int) time.Duration {
	d := c.opts.BackoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= c.opts.BackoffMax {
			d = c.opts.BackoffMax
			break
		}
	}
	if d > c.opts.BackoffMax {
		d = c.opts.BackoffMax
	}
	half := d / 2
	return half + time.Duration(rand.Float64()*float64(half))
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	return c.opts.Clock.Sleep(ctx, d)
}

func (c *Client) get(ctx context.Context, e endpoint, out any) ([]byte, error) {
	body, err := c.do(ctx, e)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return body, nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("riot %s: decode %s: %w", e.method, e.path, err)
	}
	return body, nil
}

// MatchListQuery parameterises MATCH-V5 by-puuid ids. It mirrors the frozen
// contract.MatchListQuery; the adapter in internal/crawl converts between them.
type MatchListQuery struct {
	PUUID     string
	Count     int
	Start     int
	StartTime time.Time
	EndTime   time.Time
	Queue     int
}

// LeagueQuery parameterises LEAGUE-V4 entries.
type LeagueQuery struct {
	Queue    string
	Tier     string
	Division string
	Page     int
}

// Match fetches a match summary.
func (c *Client) Match(ctx context.Context, matchID string) (MatchDTO, error) {
	dto, _, err := c.MatchWithPayload(ctx, matchID)
	return dto, err
}

// MatchWithPayload fetches a match summary and returns the response body
// verbatim. The archive stores the bytes Riot sent, not a re-encoding of the
// fields this version happens to model - that is what makes a later transform
// version able to read more of the payload without a re-crawl.
func (c *Client) MatchWithPayload(ctx context.Context, matchID string) (MatchDTO, []byte, error) {
	var dto MatchDTO
	body, err := c.get(ctx, endpoint{
		method: MethodMatch,
		base:   c.opts.RegionalBaseURL,
		path:   "/lol/match/v5/matches/" + url.PathEscape(matchID),
	}, &dto)
	if err != nil {
		return MatchDTO{}, nil, err
	}
	if dto.Metadata.MatchID == "" {
		dto.Metadata.MatchID = matchID
	}
	dto.retainRaw(body)
	return dto, body, nil
}

// MatchIDs lists recent match ids for a puuid, newest first.
func (c *Client) MatchIDs(ctx context.Context, q MatchListQuery) ([]string, error) {
	query := url.Values{}
	if q.Count > 0 {
		query.Set("count", strconv.Itoa(q.Count))
	}
	if q.Start > 0 {
		query.Set("start", strconv.Itoa(q.Start))
	}
	if !q.StartTime.IsZero() {
		query.Set("startTime", strconv.FormatInt(q.StartTime.Unix(), 10))
	}
	if !q.EndTime.IsZero() {
		query.Set("endTime", strconv.FormatInt(q.EndTime.Unix(), 10))
	}
	if q.Queue > 0 {
		query.Set("queue", strconv.Itoa(q.Queue))
	}
	var ids []string
	_, err := c.get(ctx, endpoint{
		method: MethodMatchIDs,
		base:   c.opts.RegionalBaseURL,
		path:   "/lol/match/v5/matches/by-puuid/" + url.PathEscape(q.PUUID) + "/ids",
		query:  query,
	}, &ids)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// LeagueEntries fetches one page of a LEAGUE-V4 ladder.
func (c *Client) LeagueEntries(ctx context.Context, q LeagueQuery) ([]LeagueEntryDTO, error) {
	entries, _, err := c.LeagueEntriesWithPayload(ctx, q)
	return entries, err
}

// LeagueEntriesWithPayload also returns the verbatim response body.
func (c *Client) LeagueEntriesWithPayload(ctx context.Context, q LeagueQuery) ([]LeagueEntryDTO, []byte, error) {
	query := url.Values{}
	if q.Page > 0 {
		query.Set("page", strconv.Itoa(q.Page))
	}
	var entries []LeagueEntryDTO
	body, err := c.get(ctx, endpoint{
		method: MethodLeagueEntries,
		base:   c.opts.PlatformBaseURL,
		path: "/lol/league/v4/entries/" + url.PathEscape(q.Queue) + "/" +
			url.PathEscape(q.Tier) + "/" + url.PathEscape(q.Division),
		query: query,
	}, nil)
	if err != nil {
		return nil, nil, err
	}
	entries, err = decodeLeagueEntries(body)
	if err != nil {
		return nil, nil, fmt.Errorf("riot %s: decode entries: %w", MethodLeagueEntries, err)
	}
	for i := range entries {
		if entries[i].Tier == "" {
			entries[i].Tier = q.Tier
		}
		if entries[i].Rank == "" {
			entries[i].Rank = q.Division
		}
	}
	return entries, body, nil
}

// ApexLeague fetches a whole apex league (challenger, grandmaster or master),
// which is the only way to seed from the top of the ladder: those tiers have no
// divisions to page through.
func (c *Client) ApexLeague(ctx context.Context, queue, tier string) ([]LeagueEntryDTO, []byte, error) {
	var payload struct {
		Name    string            `json:"name"`
		Tier    string            `json:"tier"`
		Queue   string            `json:"queue"`
		Entries []json.RawMessage `json:"entries"`
	}
	body, err := c.get(ctx, endpoint{
		method: MethodLeagueEntries,
		base:   c.opts.PlatformBaseURL,
		path:   "/lol/league/v4/" + strings.ToLower(tier) + "leagues/by-queue/" + url.PathEscape(queue),
	}, &payload)
	if err != nil {
		return nil, nil, err
	}
	entries := make([]LeagueEntryDTO, 0, len(payload.Entries))
	for _, element := range payload.Entries {
		var entry LeagueEntryDTO
		if err := json.Unmarshal(element, &entry); err != nil {
			return nil, nil, fmt.Errorf("riot %s: decode apex entry: %w", MethodLeagueEntries, err)
		}
		if entry.Tier == "" {
			entry.Tier = tier
		}
		if entry.QueueType == "" {
			entry.QueueType = queue
		}
		entry.raw = element
		entries = append(entries, entry)
	}
	return entries, body, nil
}

// AccountByRiotID resolves a Riot ID to a puuid. It is the only identity step
// that can turn operator-supplied input into a crawl seed.
func (c *Client) AccountByRiotID(ctx context.Context, gameName, tagLine string) (AccountDTO, error) {
	var dto AccountDTO
	_, err := c.get(ctx, endpoint{
		method: MethodAccount,
		base:   c.opts.RegionalBaseURL,
		path: "/riot/account/v1/accounts/by-riot-id/" +
			url.PathEscape(gameName) + "/" + url.PathEscape(tagLine),
	}, &dto)
	if err != nil {
		return AccountDTO{}, err
	}
	return dto, nil
}

// AccountByPUUID resolves a puuid to its current Riot ID.
func (c *Client) AccountByPUUID(ctx context.Context, puuid string) (AccountDTO, error) {
	var dto AccountDTO
	_, err := c.get(ctx, endpoint{
		method: MethodAccount,
		base:   c.opts.RegionalBaseURL,
		path:   "/riot/account/v1/accounts/by-puuid/" + url.PathEscape(puuid),
	}, &dto)
	if err != nil {
		return AccountDTO{}, err
	}
	return dto, nil
}

// snippet keeps a rejection reason short. The body of an error response is for
// an operator reading a log line, and an unbounded one turns a log into a
// payload dump.
func snippet(body []byte) string {
	const limit = 200
	s := strings.TrimSpace(string(body))
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}

// IsNotFound reports whether an error is a 404, which for a match id means
// "Riot has aged this out" rather than "try again".
func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == http.StatusNotFound
}

// IsStatus reports whether an error is a non-retryable HTTP status error.
func IsStatus(err error) bool {
	var se *StatusError
	return errors.As(err, &se)
}
