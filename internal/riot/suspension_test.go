package riot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// D3, at the client. When Riot answers 429 with a Retry-After that is further
// away than the call's own deadline, the wait cannot be paid inside this call -
// but the 429 is still the answer, and the caller has to be able to tell it
// apart from a shutdown. Reporting the expired deadline instead is what made
// the worker log "job released before shutdown" for eight rows released
// ten seconds apart in a run where nothing was shutting down, and to retry them
// with no backoff at all: the documented route to a challenged API key.
func TestARetryAfterLongerThanTheCallIsReportedAsARateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(headerRetryAfter, "90")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	const callTimeout = 200 * time.Millisecond
	// A real clock: the point of the test is that the call's deadline expires
	// while the client is waiting out the Retry-After. A fake clock would
	// advance past it instead, which is the behaviour the defect hid behind.
	client := newTestClient(t, srv, RealClock{}, testKeys(), func(o *Options) {
		o.Timeout = callTimeout
	})

	started := time.Now()
	_, err := client.Match(context.Background(), "EUW1_0000000042")
	elapsed := time.Since(started)

	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("err = %v (%T), want a *RateLimitedError: a 429 is not a shutdown", err, err)
	}
	if limited.RetryAfter != 90*time.Second {
		t.Fatalf("RetryAfter = %s, want 90s", limited.RetryAfter)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("the error also reports DeadlineExceeded: the caller would classify it as a shutdown")
	}
	if elapsed < callTimeout {
		t.Fatalf("the call returned after %s, want it to have waited out its own deadline (%s) first", elapsed, callTimeout)
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
