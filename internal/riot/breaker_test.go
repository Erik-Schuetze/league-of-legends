package riot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// The circuit breaker exists because a 403 from Riot normally means the key is
// wrong, revoked or banned, and a key that keeps being used after that is how a
// project loses access for good. So the tests here are about the two things
// that matter operationally: the crawler stops calling, and it starts again by
// itself when the cooldown has passed.

func TestClientBreakerOpensAfterRepeatedForbidden(t *testing.T) {
	clock := NewFakeClock(testStart)
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusForbidden)
		writeBody(w, `{"status":{"message":"Forbidden","status_code":403}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.MaxAttempts = 1
		o.BreakerThreshold = 3
		o.BreakerCooldown = 10 * time.Minute
		o.BreakerMaxWait = 2 * time.Minute
	})

	for i := 0; i < 3; i++ {
		if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); !IsStatus(err) {
			t.Fatalf("call %d: err = %v, want a 403 status error", i+1, err)
		}
	}
	if trips := client.BreakerTrips(); trips != 1 {
		t.Fatalf("trips = %d, want 1 after three consecutive 403s", trips)
	}

	// While open the client must not touch the network at all: the point is
	// to stop spending the key's remaining credibility.
	before := requests.Load()
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err = %v, want ErrCircuitOpen", err)
	}
	if after := requests.Load(); after != before {
		t.Fatalf("an open breaker still made %d requests", after-before)
	}

	// After the cooldown the breaker lets one probe through without operator
	// action, so a key that recovers is picked up on its own.
	clock.Advance(11 * time.Minute)
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); !IsStatus(err) {
		t.Fatalf("err = %v, want the probe to reach Riot and return 403", err)
	}
	if after := requests.Load(); after != before+1 {
		t.Fatalf("requests after the cooldown = %d, want exactly one probe", after-before)
	}
}

func TestClientBreakerClosesOnASuccessfulResponse(t *testing.T) {
	clock := NewFakeClock(testStart)
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 2 {
			writeBody(w, `["EUW1_0000000000"]`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		writeBody(w, `{"status":{"message":"Forbidden","status_code":403}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.MaxAttempts = 1
		o.BreakerThreshold = 2
		o.BreakerCooldown = 10 * time.Minute
		o.BreakerMaxWait = 2 * time.Minute
	})

	// 403, then a success, then a single 403: the success cleared the run, so
	// one failure afterwards must not be enough to open the breaker.
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); err == nil {
		t.Fatal("expected the first call to be refused")
	}
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); err != nil {
		t.Fatalf("the second call should have succeeded: %v", err)
	}
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); err == nil {
		t.Fatal("expected the third call to be refused")
	}
	if trips := client.BreakerTrips(); trips != 0 {
		t.Fatalf("trips = %d, want 0: a success must reset the consecutive count", trips)
	}
	if clock.Total() != 0 {
		t.Fatalf("waited %s with nothing to wait for", clock.Total())
	}
}

func TestClientBreakerIgnoresServerErrors(t *testing.T) {
	// A 5xx is Riot's problem, not the key's. Counting it towards the breaker
	// would turn a bad afternoon at Riot into a crawler that stops itself.
	clock := NewFakeClock(testStart)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		writeBody(w, `{"status":{"message":"upstream"}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.MaxAttempts = 1
		o.BreakerThreshold = 1
		o.BreakerCooldown = time.Hour
		o.BreakerMaxWait = time.Minute
	})
	for i := 0; i < 5; i++ {
		if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); !IsStatus(err) {
			t.Fatalf("call %d: err = %v, want a 502 status error", i+1, err)
		}
	}
	if trips := client.BreakerTrips(); trips != 0 {
		t.Fatalf("trips = %d, want 0 for server errors", trips)
	}
}

// A transient bump - a burst of 429s - is paid for with a growing wait, and the
// growth is what keeps a throttled crawler from turning into a busy loop. The
// wait is capped: once the backoff has saturated the breaker stops admitting
// probes on a schedule of its own.
func TestClientBreakerBackoffGrowsAcrossTrips(t *testing.T) {
	clock := NewFakeClock(testStart)
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setRateHeaders(w, "20:1,100:120", "1:1,1:120", "", "")
		requests.Add(1)
		w.Header().Set(headerRetryAfter, "1")
		w.WriteHeader(http.StatusTooManyRequests)
		writeBody(w, `{"status":{"message":"Rate limit exceeded","status_code":429}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.MaxAttempts = 1
		o.BreakerThreshold = 1
		o.BreakerCooldown = time.Minute
		o.BreakerMaxWait = 8 * time.Minute
	})

	for i := 0; i < 4; i++ {
		if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); err == nil {
			t.Fatalf("call %d: expected the probe to reach Riot and be rate limited", i+1)
		}
	}
	if trips := client.BreakerTrips(); trips != 4 {
		t.Fatalf("trips = %d, want 4", trips)
	}
	if got := requests.Load(); got != 4 {
		t.Fatalf("requests = %d, want 4", got)
	}
	sleeps := clock.Sleeps()
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute}
	if len(sleeps) != len(want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
	for i := range want {
		if sleeps[i] != want[i] {
			t.Fatalf("sleeps = %v, want %v", sleeps, want)
		}
	}

	// The fifth call finds the backoff saturated at MaxWait, and the breaker
	// refuses instead of sleeping: the crawl is told to come back, rather than
	// being drip-fed one probe every MaxWait for as long as the outage lasts.
	before := requests.Load()
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err = %v, want ErrCircuitOpen once the backoff has saturated", err)
	}
	if after := requests.Load(); after != before {
		t.Fatalf("a stopped breaker still made %d requests", after-before)
	}
}

// The regression test for the runaway: a refused key must stop the crawler
// rather than buy a fixed drip of requests with each cooldown. Before the fix
// every cycle admitted exactly `threshold` more 403s, so a permanently refused
// key produced calls forever and burned the queue's attempt budget row by row.
func TestClientBreakerStopsInsteadOfDrippingRequestsWhileTheKeyIsRefused(t *testing.T) {
	clock := NewFakeClock(testStart)
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusForbidden)
		writeBody(w, `{"status":{"message":"Forbidden","status_code":403}}`)
	}))
	defer srv.Close()

	const threshold = 3
	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.MaxAttempts = 1
		o.BreakerThreshold = threshold
		o.BreakerCooldown = 30 * time.Second
		o.BreakerMaxWait = 2 * time.Minute
	})

	for i := 0; i < threshold; i++ {
		if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); !IsStatus(err) {
			t.Fatalf("call %d: err = %v, want the 403 that trips the breaker", i+1, err)
		}
	}
	if trips := client.BreakerTrips(); trips != 1 {
		t.Fatalf("trips = %d, want 1", trips)
	}

	// An auth failure goes straight to the longest wait: the key does not fix
	// itself in a cooldown, and every request with a refused key is another
	// chance to lose access permanently. Between probes the client makes no
	// requests at all - it reports the outage and lets the caller pause.
	for round := 1; round <= 3; round++ {
		before := requests.Load()
		for i := 0; i < threshold*2; i++ {
			if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); !errors.Is(err, ErrCircuitOpen) {
				t.Fatalf("round %d call %d: err = %v, want ErrCircuitOpen from a stopped breaker", round, i+1, err)
			}
		}
		if after := requests.Load(); after != before {
			t.Fatalf("round %d: a stopped breaker made %d requests", round, after-before)
		}
		clock.Advance(defaultBreakerProbeWait + time.Second)
		if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); !IsStatus(err) {
			t.Fatalf("round %d: err = %v, want the one probe per cooldown to reach Riot and be refused", round, err)
		}
	}
	if got := requests.Load(); got != threshold+3 {
		t.Fatalf("requests = %d, want %d: one probe per cooldown after the trip", got, threshold+3)
	}
}

// defaultBreakerProbeWait is the wait the client is configured with in the test
// above, named so the test's arithmetic reads as "after one cooldown".
const defaultBreakerProbeWait = 2 * time.Minute

func TestClientBreakerTripsMidRetryOnRepeated429(t *testing.T) {
	clock := NewFakeClock(testStart)
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setRateHeaders(w, "20:1,100:120", "1:1,1:120", "", "")
		requests.Add(1)
		w.Header().Set(headerRetryAfter, "1")
		w.WriteHeader(http.StatusTooManyRequests)
		writeBody(w, `{"status":{"message":"Rate limit exceeded","status_code":429}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.MaxAttempts = 4
		o.BreakerThreshold = 2
		o.BreakerCooldown = 10 * time.Minute
		o.BreakerMaxWait = 2 * time.Minute
	})
	_, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err = %v, want the retry loop to stop on an open breaker", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2: the third attempt must not reach Riot", got)
	}
	if trips := client.BreakerTrips(); trips != 1 {
		t.Fatalf("trips = %d, want 1", trips)
	}
}

func TestBreakerDefaultsAreConservative(t *testing.T) {
	clock := NewFakeClock(testStart)
	b := newBreaker(clock, 0, 0, 0)
	if b.threshold != 5 {
		t.Fatalf("threshold = %d, want 5", b.threshold)
	}
	if b.cooldown != 30*time.Second {
		t.Fatalf("cooldown = %s, want 30s", b.cooldown)
	}
	if b.maxWait != 2*time.Minute {
		t.Fatalf("maxWait = %s, want 2m", b.maxWait)
	}
	if err := b.allow(context.Background()); err != nil {
		t.Fatalf("a closed breaker refused a call: %v", err)
	}
}
