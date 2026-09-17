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

// D3, at the client. When Riot answers 429 with a Retry-After that is further
// away than a whole call may wait, the wait cannot be paid inside this call -
// but the 429 is still the answer, and the caller has to be able to tell it
// apart from a shutdown. Reporting the expired deadline instead is what made
// the worker log "job released before shutdown" for eight rows released
// ten seconds apart in a run where nothing was shutting down, and to retry them
// with no backoff at all: the documented route to a challenged API key.
//
// The wait that cannot be paid is measured against the retry wait budget, not
// against one round trip: ten seconds is what a single request may take, and a
// client that treated the Retry-After as a request timeout could never pay the
// fifteen seconds a development key asks for. See
// TestDevelopmentKeyRetryAfterIsWaitedOutInsideTheCall for the other half.
func TestARetryAfterLongerThanTheCallIsReportedAsARateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(headerRetryAfter, "600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	clock := NewFakeClock(testStart)
	const budget = 60 * time.Second
	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		o.Timeout = 10 * time.Second
		o.RetryWaitBudget = budget
	})

	_, err := client.Match(context.Background(), "EUW1_0000000042")

	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("err = %v (%T), want a *RateLimitedError: a 429 is not a shutdown", err, err)
	}
	if limited.RetryAfter != 600*time.Second {
		t.Fatalf("RetryAfter = %s, want the value Riot sent (600s)", limited.RetryAfter)
	}
	if !limited.Suspended {
		t.Fatal("Suspended = false: the caller cannot tell it must schedule the row past the suspension")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("the error also reports DeadlineExceeded: the caller would classify it as a shutdown")
	}
	if got := clock.Sleeps(); len(got) != 0 {
		t.Fatalf("sleeps = %v, want none: a wait past the budget must be released at once rather than "+
			"burning the call's deadline first", got)
	}
	if blocked := clock.Now().Sub(testStart); blocked != 0 {
		t.Fatalf("the call waited %s, want it to release immediately", blocked)
	}
	// The suspension is on the limiter, which is what the crawler reads to
	// schedule the row: a released call that left nothing behind would be
	// retried straight back into the same 429.
	if _, ok := client.Blocked(); !ok {
		t.Fatal("the limiter is not blocked: the Retry-After was reported but not penalised")
	}
}

// The measured defect, as a test.
//
// The owner's development key answers 429 with `Retry-After: 15-16s` while the
// deployed per-attempt deadline is ten seconds, so every throttled call was
// released with "retry-after outlasts the call's own deadline", the row was
// requeued at the retry base, and no pass ever converged. The wait belongs to
// the retry budget rather than to the round trip, and then the call simply
// finishes.
func TestDevelopmentKeyRetryAfterIsWaitedOutInsideTheCall(t *testing.T) {
	const retryAfter = 16 * time.Second
	clock := NewFakeClock(testStart)
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			// The observed development-key behaviour: 1s-window throttling
			// with the wait in the header.
			w.Header().Set(headerRetryAfter, "16")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		setRateHeaders(w, "20:1,100:120", "1:1,1:120", "20:1,100:120", "1:1,1:120")
		writeBody(w, `{"metadata":{"matchId":"EUW1_0000000042"}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), func(o *Options) {
		// The deployed values, unchanged: ten seconds per attempt is correct,
		// and the fix is not to make it longer.
		o.Timeout = 10 * time.Second
	})

	dto, err := client.Match(context.Background(), "EUW1_0000000042")
	if err != nil {
		t.Fatalf("a 16s Retry-After must be waited out, not released: %v", err)
	}
	if dto.Metadata.MatchID != "EUW1_0000000042" {
		t.Fatalf("match id = %q, want the fetched match", dto.Metadata.MatchID)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("requests = %d, want the 429 and one retry", n)
	}
	if got := clock.Total(); got < retryAfter {
		t.Fatalf("waited %s, want at least the Retry-After Riot asked for (%s)", got, retryAfter)
	}
}

// The negative control: a Retry-After the call can afford is waited out and
// retried, which is the ordinary 429 path.
func TestARetryAfterInsideTheCallIsWaitedOutAndRetried(t *testing.T) {
	clock := NewFakeClock(testStart)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set(headerRetryAfter, "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		setRateHeaders(w, "20:1,100:120", "1:1,1:120", "20:1,100:120", "1:1,1:120")
		writeBody(w, `{"metadata":{"matchId":"EUW1_0000000042"}}`)
	}))
	defer srv.Close()

	client := newTestClient(t, srv, clock, testKeys(), nil)
	if _, err := client.Match(context.Background(), "EUW1_0000000042"); err != nil {
		t.Fatalf("Match: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want the 429 retried once", calls)
	}
	if got := clock.Sleeps(); len(got) == 0 || got[0] < 2*time.Second {
		t.Fatalf("sleeps = %v, want a wait of at least the Retry-After", got)
	}
}
