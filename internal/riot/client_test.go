package riot

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
)

// arrivals records the virtual time at which each request reached the fake
// server. Driving the client with a FakeClock makes the limiter's waiting
// observable without waiting for it, which is the only way to assert a rate
// deterministically.
type arrivals struct {
	clock Clock

	mu sync.Mutex
	at []time.Time
}

func (a *arrivals) mark() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.at = append(a.at, a.clock.Now())
}

func (a *arrivals) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.at)
}

func (a *arrivals) gaps() []time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]time.Duration, 0, len(a.at))
	for i := 1; i < len(a.at); i++ {
		out = append(out, a.at[i].Sub(a.at[i-1]))
	}
	return out
}

// setRateHeaders renders the four rate-limit headers the client must read. An
// empty string omits that header, which is a distinct case: it means "Riot said
// nothing about this scope", not "the limit is zero".
func setRateHeaders(w http.ResponseWriter, appLimit, appCount, methodLimit, methodCount string) {
	setHeader(w, headerAppRateLimit, appLimit)
	setHeader(w, headerAppRateLimitCount, appCount)
	setHeader(w, headerMethodRateLimit, methodLimit)
	setHeader(w, headerMethodRateLimitCount, methodCount)
}

func setHeader(w http.ResponseWriter, name, value string) {
	if value != "" {
		w.Header().Set(name, value)
	}
}

func writeBody(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

// testKeys supplies a fake key. It is never a real Riot key: this suite runs
// entirely against a local server.
func testKeys() *KeyProvider {
	return NewKeyProviderFrom("RGAPI-fixture-key", "")
}

func newTestClient(t *testing.T, srv *httptest.Server, clock Clock, keys *KeyProvider, mutate func(*Options)) *Client {
	t.Helper()
	opts := Options{
		PlatformBaseURL: srv.URL,
		RegionalBaseURL: srv.URL,
		Clock:           clock,
		KeyProvider:     keys,
		MaxAttempts:     3,
		BackoffBase:     time.Millisecond,
		BackoffMax:      4 * time.Millisecond,
		HTTPClient:      srv.Client(),
		UserAgent:       "lolstats-ingest-test/1.0",
	}
	if mutate != nil {
		mutate(&opts)
	}
	client, err := NewClient(opts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func TestClientSendsKeyAcceptAndUserAgent(t *testing.T) {
	clock := NewFakeClock(testStart)
	type captured struct {
		token, accept, encoding, agent string
	}
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- captured{
			token:    r.Header.Get("X-Riot-Token"),
			accept:   r.Header.Get("Accept"),
			encoding: r.Header.Get("Accept-Encoding"),
			agent:    r.Header.Get("User-Agent"),
		}
		setRateHeaders(w, "20:1,100:120", "1:1,1:120", "20:1,100:120", "1:1,1:120")
		writeBody(w, `["EUW1_0000000000"]`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), nil)
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "fixture-puuid-01", Count: 5}); err != nil {
		t.Fatalf("MatchIDs: %v", err)
	}
	c := <-got
	if c.token != "RGAPI-fixture-key" {
		t.Fatalf("X-Riot-Token = %q", c.token)
	}
	if c.encoding != "gzip" {
		t.Fatalf("Accept-Encoding = %q, want gzip", c.encoding)
	}
	if !strings.Contains(c.accept, "json") {
		t.Fatalf("Accept = %q", c.accept)
	}
	if !strings.HasPrefix(c.agent, "lolstats-ingest") {
		t.Fatalf("User-Agent = %q", c.agent)
	}
}

func TestClientQueryParameters(t *testing.T) {
	past := testStart.Add(-24 * time.Hour)
	tests := []struct {
		name     string
		query    MatchListQuery
		want     map[string]string
		unwanted []string
	}{{
		name:  "queue and count",
		query: MatchListQuery{PUUID: "fixture-puuid-02", Count: 20, Queue: 420},
		want:  map[string]string{"count": "20", "queue": "420"},
	}, {
		name:  "time window",
		query: MatchListQuery{PUUID: "fixture-puuid-03", StartTime: past, EndTime: testStart},
		want: map[string]string{
			"startTime": fmt.Sprint(past.Unix()),
			"endTime":   fmt.Sprint(testStart.Unix()),
		},
	}, {
		name:     "start offset only",
		query:    MatchListQuery{PUUID: "fixture-puuid-04", Start: 100},
		want:     map[string]string{"start": "100"},
		unwanted: []string{"count", "queue", "startTime", "endTime"},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := NewFakeClock(testStart)
			got := make(chan map[string]string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				params := make(map[string]string)
				for k, v := range r.URL.Query() {
					params[k] = v[0]
				}
				if !strings.Contains(r.URL.Path, "/lol/match/v5/matches/by-puuid/"+tc.query.PUUID+"/ids") {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				got <- params
				writeBody(w, `[]`)
			}))
			defer srv.Close()

			client := newTestClient(t, srv, clock, testKeys(), nil)
			if _, err := client.MatchIDs(context.Background(), tc.query); err != nil {
				t.Fatalf("MatchIDs: %v", err)
			}
			params := <-got
			for k, v := range tc.want {
				if params[k] != v {
					t.Fatalf("query %s = %q, want %q", k, params[k], v)
				}
			}
			for _, k := range tc.unwanted {
				if v, ok := params[k]; ok {
					t.Fatalf("query %s = %q, want it absent", k, v)
				}
			}
		})
	}
}

func TestClientDecodesGzippedResponse(t *testing.T) {
	clock := NewFakeClock(testStart)
	const payload = `{"metadata":{"matchId":"EUW1_0000000000"},"info":{"queueId":420,"participants":[]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "gzip" {
			t.Errorf("request did not ask for gzip")
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		gz := gzip.NewWriter(w)
		defer func() { _ = gz.Close() }()
		if _, err := io.WriteString(gz, payload); err != nil {
			t.Errorf("write gzip body: %v", err)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), nil)
	dto, err := client.Match(context.Background(), "EUW1_0000000000")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if dto.Metadata.MatchID != "EUW1_0000000000" {
		t.Fatalf("decoded match id = %q", dto.Metadata.MatchID)
	}
}

func TestClientRejectsAnOversizedBody(t *testing.T) {
	clock := NewFakeClock(testStart)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, strings.Repeat("x", 4096))
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.MaxBodyBytes = 256
		o.MaxAttempts = 1
	})
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); err == nil {
		t.Fatal("expected an error for a body over MaxBodyBytes")
	}
}

func TestClientWithoutAKeyMakesNoRequest(t *testing.T) {
	clock := NewFakeClock(testStart)
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeBody(w, `[]`)
	}))
	defer srv.Close()

	// Neither the environment nor a file has a key: a supported configuration.
	keys := NewKeyProviderFrom("", "")
	client := newTestClient(t, srv, clock, keys, nil)
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("the client made %d requests without a key", n)
	}
	if _, ok := keys.Key(); ok {
		t.Fatal("key provider reported a key it does not have")
	}
	if keys.Source() != "none" {
		t.Fatalf("source = %q, want none", keys.Source())
	}
}

func TestClientRetriesTransportFailures(t *testing.T) {
	clock := NewFakeClock(testStart)
	var attempts atomic.Int64
	// The flake is in the transport, not in the status: a dropped connection
	// must be retried just like a 503.
	flaky := &flakyTransport{failFirst: 2, inner: http.DefaultTransport, calls: &attempts}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, `["EUW1_0000000000"]`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.HTTPClient = &http.Client{Transport: flaky}
	})
	ids, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"})
	if err != nil {
		t.Fatalf("MatchIDs: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("ids = %v", ids)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("transport calls = %d, want 3 (two flakes then a success)", got)
	}
	if clock.Total() <= 0 {
		t.Fatal("the client retried without backing off")
	}
}

type flakyTransport struct {
	failFirst int64
	inner     http.RoundTripper
	calls     *atomic.Int64
}

func (f *flakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.calls.Add(1) <= f.failFirst {
		return nil, errors.New("fixture: connection reset by peer")
	}
	return f.inner.RoundTrip(req)
}

func TestClientCallsEveryEndpointPath(t *testing.T) {
	clock := NewFakeClock(testStart)
	seen := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.EscapedPath()
		setRateHeaders(w, "20:1,100:120", "1:1,1:120", "", "")
		switch {
		case strings.Contains(r.URL.Path, "/lol/match/v5/matches/"):
			writeBody(w, `{"metadata":{"matchId":"EUW1_0000000000"},"info":{"queueId":420,"participants":[]}}`)
		case strings.Contains(r.URL.Path, "/lol/league/v4/entries/"):
			writeBody(w, `[{"puuid":"fixture-puuid-01","queueType":"RANKED_SOLO_5x5","tier":"GOLD","rank":"I"}]`)
		case strings.Contains(r.URL.Path, "/lol/league/v4/challengerleagues/by-queue/"):
			writeBody(w, `{"name":"challenger","queue":"RANKED_SOLO_5x5","entries":[{"puuid":"fixture-puuid-11","tier":"CHALLENGER","rank":"I","leaguePoints":900}]}`)
		default:
			writeBody(w, `{"puuid":"fixture-puuid-01","gameName":"Fixture","tagLine":"EUW"}`)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), nil)
	ctx := context.Background()
	if _, err := client.Match(ctx, "EUW1_0000000000"); err != nil {
		t.Fatalf("Match: %v", err)
	}
	if _, err := client.LeagueEntries(ctx, LeagueQuery{Queue: "RANKED_SOLO_5x5", Tier: "GOLD", Division: "I", Page: 2}); err != nil {
		t.Fatalf("LeagueEntries: %v", err)
	}
	entries, payload, err := client.ApexLeague(ctx, "RANKED_SOLO_5x5", "CHALLENGER")
	if err != nil {
		t.Fatalf("ApexLeague: %v", err)
	}
	if len(entries) != 1 || entries[0].Tier != "CHALLENGER" {
		t.Fatalf("apex entries = %+v", entries)
	}
	if len(payload) == 0 {
		t.Fatal("apex payload was not retained verbatim")
	}
	if _, err := client.AccountByRiotID(ctx, "Fixture Summoner", "EUW"); err != nil {
		t.Fatalf("AccountByRiotID: %v", err)
	}
	if _, err := client.AccountByPUUID(ctx, "fixture-puuid-01"); err != nil {
		t.Fatalf("AccountByPUUID: %v", err)
	}

	want := []string{
		"/lol/match/v5/matches/EUW1_0000000000",
		"/lol/league/v4/entries/RANKED_SOLO_5x5/GOLD/I",
		"/lol/league/v4/challengerleagues/by-queue/RANKED_SOLO_5x5",
		"/riot/account/v1/accounts/by-riot-id/Fixture%20Summoner/EUW",
		"/riot/account/v1/accounts/by-puuid/fixture-puuid-01",
	}
	close(seen)
	var paths []string
	for p := range seen {
		paths = append(paths, p)
	}
	for _, w := range want {
		if !slicesContains(paths, w) {
			t.Fatalf("no request for %q; saw %v", w, paths)
		}
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestClientRecordsMetrics(t *testing.T) {
	clock := NewFakeClock(testStart)
	metrics := obs.NewMetrics()
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setRateHeaders(w, "20:1,100:120", "1:1,1:120", "20:1,100:120", "2:1,2:120")
		if requests.Add(1) == 1 {
			w.Header().Set(headerRetryAfter, "1")
			w.WriteHeader(http.StatusTooManyRequests)
			writeBody(w, `{"status":{"message":"rate limited"}}`)
			return
		}
		writeBody(w, `["EUW1_0000000000"]`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) { o.Metrics = metrics })
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); err != nil {
		t.Fatalf("MatchIDs: %v", err)
	}

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	text := rec.Body.String()
	for _, want := range []string{
		`lolstats_riot_requests_total{method="match-ids",status="200"} 1`,
		`lolstats_riot_requests_total{method="match-ids",status="429"} 1`,
		`lolstats_riot_retries_total{method="match-ids",reason="429"} 1`,
		`lolstats_riot_request_duration_seconds_count{method="match-ids"} 2`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics output is missing %q", want)
		}
	}
}

func TestClientContextCancellationAbortsTheRequest(t *testing.T) {
	// This test is about the wait itself, so it uses the real clock and a
	// deadline short enough to observe.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	client, err := NewClient(Options{
		PlatformBaseURL: srv.URL,
		RegionalBaseURL: srv.URL,
		Clock:           RealClock{},
		KeyProvider:     testKeys(),
		Timeout:         30 * time.Millisecond,
		MaxAttempts:     5,
		HTTPClient:      srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.CloseIdleConnections()

	started := time.Now()
	_, err = client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"})
	if err == nil {
		t.Fatal("expected the request to fail")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("cancellation took %s, so the timeout did not cut the retry loop short", elapsed)
	}
}

func TestClientStopsOnCancelledCallerContext(t *testing.T) {
	clock := NewFakeClock(testStart)
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeBody(w, `[]`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.MatchIDs(ctx, MatchListQuery{PUUID: "p"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("a cancelled call made %d requests", n)
	}
}

func TestIsNotFoundAndIsStatus(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		notFound bool
		isStatus bool
	}{
		{name: "nil", err: nil},
		{name: "404", err: &StatusError{Method: MethodMatch, Status: 404}, notFound: true, isStatus: true},
		{name: "403", err: &StatusError{Method: MethodMatch, Status: 403}, isStatus: true},
		{name: "500", err: &StatusError{Method: MethodMatch, Status: 500}, isStatus: true},
		{name: "wrapped 404", err: fmt.Errorf("match: %w", &StatusError{Method: MethodMatch, Status: 404}), notFound: true, isStatus: true},
		{name: "rate limited", err: &RateLimitedError{Method: MethodMatch, Attempts: 3}},
		{name: "no key", err: ErrNoAPIKey},
		{name: "plain", err: errors.New("boom")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNotFound(tc.err); got != tc.notFound {
				t.Fatalf("IsNotFound = %v, want %v", got, tc.notFound)
			}
			if got := IsStatus(tc.err); got != tc.isStatus {
				t.Fatalf("IsStatus = %v, want %v", got, tc.isStatus)
			}
		})
	}
}

func TestErrorMessagesNameTheCause(t *testing.T) {
	status := &StatusError{Method: MethodLeagueEntries, Status: 418, Body: "teapot"}
	if msg := status.Error(); !strings.Contains(msg, "league-entries") || !strings.Contains(msg, "418") {
		t.Fatalf("message = %q", msg)
	}
	limited := &RateLimitedError{Method: MethodMatch, Attempts: 4, RetryAfter: 7 * time.Second}
	if msg := limited.Error(); !strings.Contains(msg, "match") || !strings.Contains(msg, "7s") {
		t.Fatalf("message = %q", msg)
	}
}

func TestBaseURLsHonourTheTestOverrides(t *testing.T) {
	t.Setenv(EnvPlatformBaseURL, "http://127.0.0.1:9/")
	t.Setenv(EnvRegionalBaseURL, "http://127.0.0.1:10")
	platform, regional := BaseURLs("EUW1", "EUROPE")
	if platform != "http://127.0.0.1:9" {
		t.Fatalf("platform = %q", platform)
	}
	if regional != "http://127.0.0.1:10" {
		t.Fatalf("regional = %q", regional)
	}

	t.Setenv(EnvPlatformBaseURL, "")
	t.Setenv(EnvRegionalBaseURL, "")
	platform, regional = BaseURLs("EUW1", "EUROPE")
	if platform != "https://euw1.api.riotgames.com" || regional != "https://europe.api.riotgames.com" {
		t.Fatalf("defaults = %q, %q", platform, regional)
	}
}

func TestNewClientRequiresBothHosts(t *testing.T) {
	if _, err := NewClient(Options{PlatformBaseURL: "http://x"}); err == nil {
		t.Fatal("expected an error for a missing regional host")
	}
	if _, err := NewClient(Options{RegionalBaseURL: "http://x"}); err == nil {
		t.Fatal("expected an error for a missing platform host")
	}
}

func TestKeyProviderRotatesTheKeyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "riot-key")
	if err := os.WriteFile(path, []byte("RGAPI-first\n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	keys := NewKeyProviderFrom("RGAPI-environment", path)
	keys.ttl = 0
	now := time.Now()
	keys.now = func() time.Time { return now }

	if got, ok := keys.Key(); !ok || got != "RGAPI-first" {
		t.Fatalf("key = %q, %v, want the file value to win over the environment", got, ok)
	}
	if keys.Source() != "file" || !keys.Rotated() {
		t.Fatalf("source = %q, rotated = %v", keys.Source(), keys.Rotated())
	}

	now = now.Add(time.Hour)
	if err := os.WriteFile(path, []byte("RGAPI-second\n"), 0o600); err != nil {
		t.Fatalf("rewrite key: %v", err)
	}
	got, ok := keys.Key()
	if !ok || got != "RGAPI-second" {
		t.Fatalf("key = %q, %v, want the rotated value with no restart", got, ok)
	}
	if keys.Age() != 0 {
		t.Fatalf("age = %s, want the rotation to reset it", keys.Age())
	}

	// A missing file must keep the last good key rather than dropping the
	// crawl: an operator rotating a key deletes and recreates the file.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	if got, ok := keys.Key(); !ok || got != "RGAPI-second" {
		t.Fatalf("key = %q, %v, want the last good key", got, ok)
	}
}

func TestKeyProviderFallsBackToTheEnvironment(t *testing.T) {
	keys := NewKeyProviderFrom("RGAPI-environment", filepath.Join(t.TempDir(), "absent"))
	got, ok := keys.Key()
	if !ok || got != "RGAPI-environment" {
		t.Fatalf("key = %q, %v", got, ok)
	}
	if keys.Source() != "environment" || keys.Rotated() {
		t.Fatalf("source = %q, rotated = %v", keys.Source(), keys.Rotated())
	}
}

func TestKeyProviderAgeGrowsAndResetsOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := os.WriteFile(path, []byte("RGAPI-one"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	now := testStart
	keys := NewKeyProviderFrom("", path)
	keys.ttl = 0
	keys.now = func() time.Time { return now }
	if _, ok := keys.Key(); !ok {
		t.Fatal("expected a key")
	}
	now = now.Add(6 * time.Hour)
	if age := keys.Age(); age != 6*time.Hour {
		t.Fatalf("age = %s, want 6h", age)
	}
	if err := os.WriteFile(path, []byte("RGAPI-two"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if _, ok := keys.Key(); !ok {
		t.Fatal("expected the rotated key")
	}
	if age := keys.Age(); age != 0 {
		t.Fatalf("age after rotation = %s, want 0", age)
	}
}

// The retry path has to say which 429s are only being waited out and which one
// ended the call. The measured defect this pins: a full hour of ingest output
// that was nothing but WARN "riot rate limited", one line a minute, every one
// of them "attempt":1. Every shed fetch logged at WARN, including the ones the
// client was about to wait a second for and retry into a success, so a crawler
// running correctly at its key's ceiling read exactly like the permanent stall
// it was mistaken for. The bumps are still counted - see
// TestClientRecordsMetrics - because the 429 share is what the rate-limit
// alert reads; only the bump that ends the call is the operator's warning.
func TestAnAbsorbed429IsNotAWarningAndATerminalOneIs(t *testing.T) {
	clock := NewFakeClock(testStart)

	var buf bytes.Buffer
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		setRateHeaders(w, "20:1,100:120", "1:1,1:120", "20:1,100:120", "2:1,2:120")
		if requests.Add(1) == 1 {
			w.Header().Set(headerRetryAfter, "1")
			w.WriteHeader(http.StatusTooManyRequests)
			writeBody(w, `{"status":{"message":"rate limited"}}`)
			return
		}
		writeBody(w, `["EUW1_0000000000"]`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.Logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); err != nil {
		t.Fatalf("MatchIDs: %v", err)
	}
	absorbed := buf.String()
	if strings.Contains(absorbed, `"level":"WARN"`) {
		t.Fatalf("a 429 the call went on to succeed past was logged as a warning: %s", absorbed)
	}
	if !strings.Contains(absorbed, `"msg":"riot rate limited; waiting out Riot's retry-after"`) {
		t.Fatalf("the absorbed 429 left no record of itself: %s", absorbed)
	}

	// The same 429 on the call's last attempt is the warning: that call did not
	// fetch, and the run is the thing that has to hear about it.
	buf.Reset()
	always := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		setRateHeaders(w, "20:1,100:120", "1:1,1:120", "20:1,100:120", "2:1,2:120")
		w.Header().Set(headerRetryAfter, "1")
		w.WriteHeader(http.StatusTooManyRequests)
		writeBody(w, `{"status":{"message":"rate limited"}}`)
	}))
	defer always.Close()

	exhausted := newTestClient(t, always, clock, testKeys(), func(o *Options) {
		o.MaxAttempts = 2
		o.Logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	})
	if _, err := exhausted.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); err == nil {
		t.Fatal("every attempt was refused and the call returned no error")
	}
	terminal := buf.String()
	if !strings.Contains(terminal, `"level":"WARN"`) ||
		!strings.Contains(terminal, `"msg":"riot rate limited"`) {
		t.Fatalf("the 429 that ended the call was not reported as a warning: %s", terminal)
	}
}
