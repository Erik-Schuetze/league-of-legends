package riot

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Window is one advertised rate limit: at most Limit requests in Period.
//
// Riot sends these as `limit:seconds` pairs in X-App-Rate-Limit and
// X-Method-Rate-Limit, and the counts actually used in the same windows in the
// -Count variants. Both are read on every response, because the published
// documentation is stale and Riot explicitly does not document every limit.
type Window struct {
	Limit  int
	Period time.Duration
}

// DevelopmentKeyWindows is the conservative starting point for a personal or
// development key: 20 requests per second and 100 per two minutes, which makes
// the two-minute window the binding one at roughly 0.83 requests per second.
//
// These values are only what the limiter uses before the first response of a
// run arrives. Once Riot has told us what it actually enforces, the headers win
// - see Limiter.Observe.
func DevelopmentKeyWindows() []Window {
	return []Window{
		{Limit: 20, Period: time.Second},
		{Limit: 100, Period: 2 * time.Minute},
	}
}

// ConfigWindows turns the configured development-key allowances into the
// limiter's ceiling and starting window set.
//
// The ceiling is not the operating rate: it is the largest rate this process
// will ever run at, so a stale or misread header cannot authorise a burst the
// key does not have. Riot's headers still decide the rate below it, which is
// what makes an upgraded key faster without a restart.
//
// A non-positive or non-integral value is dropped rather than rounded: "18.5
// requests per second" is not a limit anyone can honour, and inventing one
// would either overspend the key or silently throttle the crawl.
func ConfigWindows(perSecond float64, per2Min int) []Window {
	out := make([]Window, 0, 2)
	if perSecond >= 1 {
		out = append(out, Window{Limit: int(perSecond), Period: time.Second})
	}
	if per2Min >= 1 {
		out = append(out, Window{Limit: per2Min, Period: 2 * time.Minute})
	}
	if len(out) == 0 {
		return DevelopmentKeyWindows()
	}
	return out
}

// ParseWindows reads a `20:1,100:120` style header. Unparseable pairs are
// dropped rather than failing the request: a header we cannot read must cost us
// throughput, never a crawl. A window with a non-positive limit is dropped for
// the same reason - honouring it literally would wedge the client forever.
func ParseWindows(header string) []Window {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	parts := strings.Split(header, ",")
	out := make([]Window, 0, len(parts))
	for _, p := range parts {
		limitStr, periodStr, ok := strings.Cut(strings.TrimSpace(p), ":")
		if !ok {
			continue
		}
		limit, err := strconv.Atoi(strings.TrimSpace(limitStr))
		if err != nil || limit <= 0 {
			continue
		}
		seconds, err := strconv.Atoi(strings.TrimSpace(periodStr))
		if err != nil || seconds <= 0 {
			continue
		}
		out = append(out, Window{Limit: limit, Period: time.Duration(seconds) * time.Second})
	}
	return out
}

// parseCounts reads a `1:1,1:120` style header into used-per-window. It does
// not go through ParseWindows because zero is a legal count - and the one that
// matters, since it means the window is empty.
func parseCounts(header string) map[time.Duration]int {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	out := make(map[time.Duration]int)
	for _, p := range strings.Split(header, ",") {
		countStr, periodStr, ok := strings.Cut(strings.TrimSpace(p), ":")
		if !ok {
			continue
		}
		count, err := strconv.Atoi(strings.TrimSpace(countStr))
		if err != nil || count < 0 {
			continue
		}
		seconds, err := strconv.Atoi(strings.TrimSpace(periodStr))
		if err != nil || seconds <= 0 {
			continue
		}
		out[time.Duration(seconds)*time.Second] = count
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// rateLimitHeaders is the set read from every response. Method limits are
// per-endpoint, which is why both sets exist: the app limit and the method limit
// can bind independently and the client has to satisfy whichever is tighter.
type rateLimitHeaders struct {
	AppLimit       []Window
	AppCount       map[time.Duration]int
	MethodLimit    []Window
	MethodCount    map[time.Duration]int
	HasApplication bool
	HasMethod      bool
}

const (
	headerAppRateLimit         = "X-App-Rate-Limit"
	headerAppRateLimitCount    = "X-App-Rate-Limit-Count"
	headerMethodRateLimit      = "X-Method-Rate-Limit"
	headerMethodRateLimitCount = "X-Method-Rate-Limit-Count"
	headerRetryAfter           = "Retry-After"
)

func readRateLimitHeaders(h http.Header) rateLimitHeaders {
	var out rateLimitHeaders
	if v := h.Get(headerAppRateLimit); v != "" {
		out.AppLimit = ParseWindows(v)
		out.HasApplication = len(out.AppLimit) > 0
	}
	out.AppCount = parseCounts(h.Get(headerAppRateLimitCount))
	if v := h.Get(headerMethodRateLimit); v != "" {
		out.MethodLimit = ParseWindows(v)
		out.HasMethod = len(out.MethodLimit) > 0
	}
	out.MethodCount = parseCounts(h.Get(headerMethodRateLimitCount))
	return out
}

// ParseRetryAfter reads the Retry-After header in either of its two legal
// forms: a delay in seconds, or an HTTP date. A value we cannot read is
// reported as zero so the caller can fall back to its own backoff rather than
// treating "unreadable" as "no wait".
func ParseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(header); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(header); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
