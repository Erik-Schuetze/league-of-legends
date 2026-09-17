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

func TestClientBreakerBackoffGrowsAcrossTrips(t *testing.T) {
	// Each further trip doubles what the next probe costs, capped at MaxWait.
	// The cap matters: an unbounded doubling turns a long outage into a
	// crawler that never comes back on its own.
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
		o.BreakerThreshold = 1
		o.BreakerCooldown = time.Minute
		o.BreakerMaxWait = 2 * time.Minute
	})

	for i := 0; i < 3; i++ {
		if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); !IsStatus(err) {
			t.Fatalf("call %d: err = %v, want a probe that reaches Riot and fails", i+1, err)
		}
	}
	if trips := client.BreakerTrips(); trips != 3 {
		t.Fatalf("trips = %d, want 3", trips)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
	sleeps := clock.Sleeps()
	want := []time.Duration{time.Minute, 2 * time.Minute}
	if len(sleeps) != len(want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
	for i := range want {
		if sleeps[i] != want[i] {
			t.Fatalf("sleeps = %v, want %v", sleeps, want)
		}
	}
}

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
