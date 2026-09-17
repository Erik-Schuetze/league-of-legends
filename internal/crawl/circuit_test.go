package crawl

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// A key that Riot refuses is a statement about the crawl, not about the matches
// the crawler happened to be holding when the key broke. The breaker has to say
// so out loud - ErrCircuitOpen - so the worker hands the rows back without
// charging them for it. Before the fix the client slept the cooldown out and
// called Riot again, which meant every row in the backlog reached its own
// attempt ceiling during one outage and was dead-lettered permanently.
//
// This is the end-to-end half of finding D: a real client, a real 403 server
// and the real worker loop, wired together in fake time. It needs no Riot key
// and makes no outbound request: the fake server is localhost.
func TestSustainedForbiddenKeyPausesTheCrawlInsteadOfRetiringTheBacklog(t *testing.T) {
	clock := riot.NewFakeClock(testBaseTime())
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"status":{"message":"Forbidden","status_code":403}}`)
	}))
	defer srv.Close()

	client, err := riot.NewClient(riot.Options{
		PlatformBaseURL:  srv.URL,
		RegionalBaseURL:  srv.URL,
		Clock:            clock,
		KeyProvider:      riot.NewKeyProviderFrom("RGAPI-fixture-key", ""),
		MaxAttempts:      1,
		HTTPClient:       srv.Client(),
		UserAgent:        "lolstats-ingest-test/1.0",
		BreakerThreshold: 2,
		BreakerCooldown:  30 * time.Second,
		BreakerMaxWait:   2 * time.Minute,
		BackoffBase:      time.Millisecond,
		BackoffMax:       4 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	const rows = 6
	store := newFakeStore()
	writer := newFakeWriter()
	worker, err := NewWorker(WorkerOptions{
		Deps:        testDeps(store, client, writer, clock),
		JobBatch:    rows,
		MaxAttempts: 2,
		RetryBase:   time.Second,
		RetryMax:    5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	ids := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		id := fmt.Sprintf("EUW1_%010d", i)
		ids = append(ids, id)
		store.forceEnqueue(contract.QueueItem{MatchID: id, Priority: PriorityPlayer})
	}

	// Twelve passes, which is six times the attempt budget of every row: with
	// the outage charged to the rows, none of them could survive this.
	const passes = 12
	for i := 0; i < passes; i++ {
		if _, err := worker.Step(context.Background()); err != nil {
			t.Fatalf("pass %d: Step: %v", i+1, err)
		}
		clock.Advance(time.Minute)
	}

	for _, id := range ids {
		if status := store.jobStatus(id); status == "dead" {
			t.Fatalf("row %s was dead-lettered by someone else's outage (cause %q); passes=%d requests=%d",
				id, store.jobCause(id), passes, requests.Load())
		}
		job := store.jobFor(id)
		if job == nil {
			t.Fatalf("row %s vanished from the queue", id)
		}
		// Two rows paid for the requests that tripped the breaker; the rest
		// must have been handed back untouched.
		if job.item.Attempts >= 2 {
			t.Fatalf("row %s spent %d of its 2 attempts on a refused key", id, job.item.Attempts)
		}
	}
	if jobs := store.deadJobs(); jobs != 0 {
		t.Fatalf("dead rows = %d, want 0", jobs)
	}
	if got := requests.Load(); got > int64(passes+2) {
		t.Fatalf("Riot was called %d times over %d passes with a refused key; at most one probe per pass is expected",
			got, passes)
	}
}
