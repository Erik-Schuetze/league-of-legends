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

// The retry contract: a 429 is a wait, not a failure; the wait is the one Riot
// asked for; a 5xx is retried a bounded number of times; and once the attempt
// ceiling is reached the caller gets an error it can act on - a retryable
// RateLimitedError for throttling, a terminal StatusError otherwise.
//
// Everything here runs on a FakeClock. That is what makes "the client waited
// seven seconds" an assertion rather than a seven-second test, and it is why
// the production clock is injected in the first place.

func TestClientHonoursRetryAfterOn429(t *testing.T) {
	const retryAfter = 7 * time.Second
	clock := NewFakeClock(testStart)
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setRateHeaders(w, "20:1,100:120", "1:1,1:120", "20:1,100:120", "1:1,1:120")
		if requests.Add(1) == 1 {
			w.Header().Set(headerRetryAfter, "7")
			w.WriteHeader(http.StatusTooManyRequests)
			writeBody(w, `{"status":{"message":"Rate limit exceeded","status_code":429}}`)
			return
		}
		writeBody(w, `["EUW1_0000000000"]`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), nil)
	ids, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "fixture-puuid-01"})
	if err != nil {
		t.Fatalf("a 429 with Retry-After must be waited out and retried, got: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("ids = %v", ids)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2 (the throttled call and the retry)", got)
	}

	var waited time.Duration
	for _, d := range clock.Sleeps() {
		if d == retryAfter {
			waited = d
		}
	}
	if waited != retryAfter {
		t.Fatalf("sleeps = %v, want one of exactly %s", clock.Sleeps(), retryAfter)
	}
	if clock.Total() < retryAfter {
		t.Fatalf("total wait = %s, want at least %s", clock.Total(), retryAfter)
	}
}

func TestClientCannotOutrunASecondRetryAfter(t *testing.T) {
	// The limiter is penalised by the penalty, not merely the one in-flight
	// call: otherwise four workers wait out the same 429 and then arrive
	// together.
	clock := NewFakeClock(testStart)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setRateHeaders(w, "20:1,100:120", "1:1,1:120", "", "")
		w.Header().Set(headerRetryAfter, "30")
		w.WriteHeader(http.StatusTooManyRequests)
		writeBody(w, `{"status":{"message":"Rate limit exceeded","status_code":429}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		// Give the limiter runway so the only reason to wait is the penalty.
		ceiling := ConfigWindows(20, 4800)
		o.Limiter = NewLimiter(LimiterOptions{Clock: clock, Ceiling: ceiling, Bootstrap: ceiling})
	})
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); err == nil {
		t.Fatal("expected the attempt ceiling to produce an error")
	}
	if blocked := client.Limiter().Blocked(); blocked <= 0 {
		t.Fatalf("limiter blocked = %s, want the Retry-After to have penalised it", blocked)
	}
}

func TestClientRetryAfterIsCappedByTheLimiter(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		max     time.Duration
		wantCap time.Duration
	}{
		{name: "reasonable delay is honoured", header: "45", max: 5 * time.Minute, wantCap: 45 * time.Second},
		{name: "absurd delay is capped", header: "3600", max: 5 * time.Minute, wantCap: 5 * time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := NewFakeClock(testStart)
			limiter := NewLimiter(LimiterOptions{Clock: clock, MaxRetryAfter: tc.max})
			limiter.Penalize(ParseRetryAfter(tc.header, clock.Now()))
			if got := limiter.Blocked(); got != tc.wantCap {
				t.Fatalf("blocked = %s, want %s", got, tc.wantCap)
			}
		})
	}
}

func TestClientRetryWithoutRetryAfterBacksOff(t *testing.T) {
	clock := NewFakeClock(testStart)
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setRateHeaders(w, "20:1,100:120", "1:1,1:120", "", "")
		if requests.Add(1) == 1 {
			// No Retry-After: the client must fall back to its own backoff
			// rather than retrying immediately.
			w.WriteHeader(http.StatusTooManyRequests)
			writeBody(w, `{}`)
			return
		}
		writeBody(w, `["EUW1_0000000000"]`)
	}))
	defer srv.Close()

	const base = 400 * time.Millisecond
	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.BackoffBase = base
		o.BackoffMax = base
	})
	if _, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"}); err != nil {
		t.Fatalf("MatchIDs: %v", err)
	}
	for _, d := range clock.Sleeps() {
		if d < base/2 || d > base {
			t.Fatalf("sleep %s falls outside the equal-jitter range [%s, %s]", d, base/2, base)
		}
	}
	if clock.Total() <= 0 {
		t.Fatal("the client did not back off at all")
	}
}

func TestClientRetries5xxUpToTheAttemptCeiling(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		maxAttempt int
	}{{
		name:       "service unavailable",
		status:     http.StatusServiceUnavailable,
		maxAttempt: 3,
	}, {
		name:       "internal server error",
		status:     http.StatusInternalServerError,
		maxAttempt: 1,
	}, {
		name:       "bad gateway twice",
		status:     http.StatusBadGateway,
		maxAttempt: 2,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := NewFakeClock(testStart)
			var requests atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(tc.status)
				writeBody(w, `{"status":{"message":"upstream"}}`)
			}))
			defer srv.Close()

			client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
				o.MaxAttempts = tc.maxAttempt
			})
			_, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"})
			if err == nil {
				t.Fatal("expected the 5xx to surface as an error after the ceiling")
			}
			if !IsStatus(err) {
				t.Fatalf("err = %v, want a StatusError", err)
			}
			var se *StatusError
			if !errors.As(err, &se) || se.Status != tc.status {
				t.Fatalf("err = %v, want status %d", err, tc.status)
			}
			if got := requests.Load(); got != int64(tc.maxAttempt) {
				t.Fatalf("requests = %d, want exactly %d attempts", got, tc.maxAttempt)
			}
		})
	}
}

func TestClientStopsRetryingOnATerminalStatus(t *testing.T) {
	// A 404 is not a transient failure: retrying it burns budget on a match
	// Riot has aged out.
	for _, status := range []int{http.StatusNotFound, http.StatusGone, http.StatusUnauthorized} {
		clock := NewFakeClock(testStart)
		var requests atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.WriteHeader(status)
			writeBody(w, `{"status":{"message":"no"}}`)
		}))

		client := newTestClient(t, srv, clock, testKeys(), func(o *Options) { o.MaxAttempts = 5 })
		_, err := client.Match(context.Background(), "EUW1_0000000000")
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: expected an error", status)
		}
		if got := requests.Load(); got != 1 {
			t.Fatalf("status %d: requests = %d, want 1", status, got)
		}
		if status == http.StatusNotFound && !IsNotFound(err) {
			t.Fatalf("status 404: err = %v, want IsNotFound", err)
		}
		if clock.Total() != 0 {
			t.Fatalf("status %d: waited %s before failing a terminal status", status, clock.Total())
		}
	}
}

func TestClientReportsRateLimitedAfterExhaustingAttempts(t *testing.T) {
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

	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) { o.MaxAttempts = 3 })
	_, err := client.MatchIDs(context.Background(), MatchListQuery{PUUID: "p"})
	if err == nil {
		t.Fatal("expected an error once the attempt ceiling was reached")
	}
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("err = %v (%T), want a RateLimitedError so the caller can re-queue", err, err)
	}
	if limited.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3", limited.Attempts)
	}
	if limited.RetryAfter != time.Second {
		t.Fatalf("retry after = %s, want 1s", limited.RetryAfter)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
	// Two waits for three attempts: the last attempt breaks out rather than
	// waiting for a retry it will not make.
	if want := 2 * time.Second; clock.Total() < want {
		t.Fatalf("total wait = %s, want at least %s of honoured Retry-After", clock.Total(), want)
	}
}

func TestBackoffIsExponentialWithEqualJitter(t *testing.T) {
	clock := NewFakeClock(testStart)
	const base = 100 * time.Millisecond
	const max = time.Second
	client := newTestClient(t, httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})), clock, testKeys(), func(o *Options) {
		o.BackoffBase = base
		o.BackoffMax = max
	})

	tests := []struct {
		attempt int
		nominal time.Duration
	}{
		{attempt: 1, nominal: base},
		{attempt: 2, nominal: 2 * base},
		{attempt: 3, nominal: 4 * base},
		{attempt: 4, nominal: 8 * base},
		{attempt: 5, nominal: max},
		{attempt: 9, nominal: max},
	}
	for _, tc := range tests {
		for i := 0; i < 64; i++ {
			got := client.backoff(tc.attempt)
			if got < tc.nominal/2 || got > tc.nominal {
				t.Fatalf("attempt %d: backoff %s outside [%s, %s]", tc.attempt, got, tc.nominal/2, tc.nominal)
			}
		}
	}
}

func TestBackoffJitterActuallyVaries(t *testing.T) {
	clock := NewFakeClock(testStart)
	client := newTestClient(t, httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})), clock, testKeys(), func(o *Options) {
		o.BackoffBase = time.Second
		o.BackoffMax = time.Second
	})
	seen := make(map[time.Duration]struct{})
	for i := 0; i < 64; i++ {
		seen[client.backoff(1)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("64 backoffs produced %d distinct delays, so there is no jitter", len(seen))
	}
}
