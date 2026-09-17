package crawl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// These are the regression tests for the defects an end-to-end verification run
// against a fake Riot origin found in this worker. Each one describes the
// failure it prevents, so a future reader can tell whether the fix is still
// there without reading the verification log.

// D1. An archive that cannot be written used to be retried forever: the failure
// path requeued the row without ever consulting the attempt ceiling, so a
// read-only or mis-mounted volume produced thousands of re-fetches of the same
// match per minute - and, once a key exists, thousands of calls to Riot.
func TestAnUnwritableArchiveGivesUpAtTheAttemptCeiling(t *testing.T) {
	h := newHarness(t, func(opts *WorkerOptions) { opts.MaxAttempts = 3 })
	h.serveFixture(t, "EUW1_1")
	h.writer.failMatch = errors.New("read-only file system")
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	// Drive far past the ceiling. The loop must stop on its own, not be
	// stopped by the test.
	for i := 0; i < 50; i++ {
		if _, err := h.worker.Step(context.Background()); err != nil {
			t.Fatalf("Step %d: %v", i, err)
		}
		h.clock.Advance(24 * time.Hour)
	}

	if got := h.fetcher.fetchCount("match:"); got > 3 {
		t.Fatalf("fetches = %d, want at most the 3 allowed attempts: an unwritable archive is looping", got)
	}
	if got := h.store.jobStatus("EUW1_1"); got != "dead" {
		t.Fatalf("job status = %q, want dead: a row that cannot be archived must be retired, not retried forever", got)
	}
	if got := h.writer.writeCount(); got != 0 {
		t.Fatalf("archive writes = %d, want 0: the write is failing", got)
	}
	// The durability invariant, straight from the verification run: with the
	// archive unwritable the matches table stayed empty, because the archive
	// write comes first and a failure stops the row short of the database.
	if got := h.store.matchCount(); got != 0 {
		t.Fatalf("matches = %d, want 0: nothing may reach the database before its archive write", got)
	}
}

// The same ceiling applies when it is the *flush* that fails, which is the
// shape a read-only volume takes once the batch has already been buffered: the
// rows were archived into the writer's memory and the whole flush bounced.
func TestAnUnwritableArchiveFlushGivesUpAtTheAttemptCeiling(t *testing.T) {
	h := newHarness(t, func(opts *WorkerOptions) { opts.MaxAttempts = 3 })
	h.serveFixture(t, "EUW1_1")
	h.writer.failFlush = errors.New("read-only file system")
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1", Attempts: 3})

	// The pass is reported as failed - the batch really did not land - but the
	// rows are accounted for before the error is handed back.
	if _, err := h.worker.Step(context.Background()); err == nil {
		t.Fatal("Step = nil, want the failed flush reported as a failed pass")
	}

	if got := h.store.jobStatus("EUW1_1"); got != "dead" {
		t.Fatalf("job status = %q, want dead: a failed flush at the ceiling must not be requeued again", got)
	}
	if got := h.store.jobCause("EUW1_1"); !strings.Contains(got, "archive flush") {
		t.Fatalf("cause = %q, want it to name the flush", got)
	}
	// The write order is the durability invariant: the flush that would make
	// the payload durable never happened, so the row is not treated as archived
	// and is not closed.
	if got := h.writer.flushCount(); got != 0 {
		t.Fatalf("flushes = %d, want 0: there is no durable archive", got)
	}
}

// D2. Recovery used to belong to maintenance alone: its grace is fifteen
// minutes and its cron is hourly, so a killed worker's claims could sit behind
// seventy-five minutes of stall. A restart now reclaims them before it claims
// anything new.
func TestABootReclaimsClaimsAbandonedByAKilledWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newHarness(t, nil)
	h.serveFixture(t, "EUW1_1")
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})
	// The previous process claimed the row and was killed before it could
	// finish: the row is claimed, with nothing running to release it.
	if _, err := h.store.ClaimJobs(context.Background(), 1, testBaseTime()); err != nil {
		t.Fatalf("ClaimJobs: %v", err)
	}

	// The replacement starts well after the claim grace.
	clock := newStopClock(testBaseTime().Add(DefaultClaimGrace+time.Minute), 1, cancel)
	w := mustWorker(t, WorkerOptions{Deps: testDeps(h.store, h.fetcher, h.writer, clock)})
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := h.store.jobStatus("EUW1_1"); got != "done" {
		t.Fatalf("job status = %q, want done: a boot must reclaim what a killed process left claimed", got)
	}
	if got := h.fetcher.fetchCount("match:"); got != 1 {
		t.Fatalf("fetches = %d, want 1: the reclaimed row is fetched once, not once per holder", got)
	}
	if got := h.store.matchCount(); got != 1 {
		t.Fatalf("matches = %d, want 1", got)
	}
}

// The other side of D2: a claim that a live worker could still be holding must
// be left alone, or two workers would fetch the same match and write it twice.
func TestABootLeavesAClaimYoungerThanTheGraceAlone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newHarness(t, nil)
	h.serveFixture(t, "EUW1_1")
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})
	if _, err := h.store.ClaimJobs(context.Background(), 1, testBaseTime()); err != nil {
		t.Fatalf("ClaimJobs: %v", err)
	}

	clock := newStopClock(testBaseTime().Add(time.Minute), 1, cancel)
	w := mustWorker(t, WorkerOptions{Deps: testDeps(h.store, h.fetcher, h.writer, clock)})
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := h.store.jobStatus("EUW1_1"); got != "claimed" {
		t.Fatalf("job status = %q, want claimed: a claim inside the grace may belong to a live worker", got)
	}
	if got := h.fetcher.fetchCount("match:"); got != 0 {
		t.Fatalf("fetches = %d, want 0: a live claim must not be handed out twice", got)
	}
}

// D3. A per-call timeout is not a shutdown. The client reports it as a wrapped
// deadline, which used to be classified as a cancellation and released with no
// delay at all: the verification run freed eight rows exactly ten seconds
// apart, in a run where nothing was shutting down.
func TestACallDeadlineIsScheduledNotReleasedAtOnce(t *testing.T) {
	h := newHarness(t, nil)
	h.fetcher.errors["match:EUW1_1"] = fmt.Errorf("riot match EUW1_1: call deadline exceeded: %w", context.DeadlineExceeded)
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}

	if got := h.store.jobStatus("EUW1_1"); got != "retry" {
		t.Fatalf("job status = %q, want retry", got)
	}
	if got := h.store.notBefore("EUW1_1"); !got.After(testBaseTime()) {
		t.Fatalf("not_before = %s, want a future instant: a call that ran out of its own time is not a shutdown", got)
	}
}

// D3, the other half: when the wait a 429 asks for outlasts the call, the row
// has to be scheduled past that wait, not released immediately. Retrying into
// a suspended key is the documented route to having it challenged.
func TestARateLimitIsWaitedOutBeforeTheRowIsRetried(t *testing.T) {
	h := newHarness(t, nil)
	const suspension = 90 * time.Second
	h.fetcher.blocked = suspension
	h.fetcher.errors["match:EUW1_1"] = &riot.RateLimitedError{Method: "match", Attempts: 1, RetryAfter: suspension}
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}

	delay := h.store.notBefore("EUW1_1").Sub(testBaseTime())
	if delay < suspension {
		t.Fatalf("retry delay = %s, want at least the %s the limiter is holding the call back for", delay, suspension)
	}
}

// A 429 that arrives with a deadline in its chain is still a rate limit: the
// deadline explains when the answer came back, not what went wrong.
func TestClassifyPrefersTheRateLimitOverTheDeadlineInItsChain(t *testing.T) {
	err := fmt.Errorf("riot match EUW1_1: %w",
		fmt.Errorf("%w: %w", &riot.RateLimitedError{Method: "match", Attempts: 1, RetryAfter: 90 * time.Second}, context.DeadlineExceeded))

	if got := classify(err); got != failureRateLimited {
		t.Fatalf("classify = %v, want failureRateLimited: a suspended key is not a shutdown", got)
	}
	// The negative control: a deadline with no rate limit behind it is still a
	// cancellation, and must keep being released at once.
	if got := classify(fmt.Errorf("riot match EUW1_1: %w", context.DeadlineExceeded)); got != failureCancelled {
		t.Fatalf("classify = %v, want failureCancelled", got)
	}
}

// D4, the other half: the row that had already been fetched and archived when
// the TERM landed. Its payload is in the open part and the final flush makes it
// durable, so the row can be closed on the way out; leaving it claimed hid an
// archived match from every worker for the length of the claim grace. This is
// the last claimed row of the twenty-row batch in the verification run.
func TestAGracefulStopClosesTheRowsItHadAlreadyArchived(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newHarness(t, nil)
	h.serveFixture(t, "EUW1_1")
	h.serveFixture(t, "EUW1_2")
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_2"})
	// The TERM lands after the first payload is archived and before the batch
	// that holds it is closed.
	h.writer.onWrite = func(string) { cancel() }

	if _, err := h.worker.Step(ctx); err != nil && ctx.Err() == nil {
		t.Fatalf("Step: %v", err)
	}

	if got := h.store.jobStatus("EUW1_1"); got != "done" {
		t.Fatalf("job status = %q, want done: the payload was archived before the stop, so the row may not be left claimed", got)
	}
	// The negative control: the row that had not started belongs to the queue,
	// not to the process that is exiting.
	if got := h.store.jobStatus("EUW1_2"); got == "claimed" {
		t.Fatalf("job EUW1_2 is still claimed after a graceful stop: the next process cannot see it")
	}
	if got := h.store.logCount("complete "); got != 1 {
		t.Fatalf("completed rows = %d, want 1", got)
	}
	// The durable half: the fake writer holds the payload and the control plane
	// holds exactly one row for it, so closing the row on the way out cannot
	// have written an archive entry for something that was never archived.
	if _, ok := h.writer.matchBodies["EUW1_1"]; !ok {
		t.Fatal("the archive holds no payload for EUW1_1: the row was closed without its archive write")
	}
	if got := h.store.matchCount(); got != 1 {
		t.Fatalf("matches rows = %d, want 1: the stop must not double-write the match", got)
	}
}

// The control for the test above: a row whose payload is not durable must stay
// claimed on the way out. Completing it would mark a match done that the
// archive does not hold - the silent loss the write order exists to prevent.
func TestAGracefulStopKeepsRowsWhoseFinalFlushFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newHarness(t, nil)
	h.serveFixture(t, "EUW1_1")
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})
	h.writer.onWrite = func(string) { cancel() }
	h.writer.failFlush = errors.New("raw: create part: permission denied")

	if _, err := h.worker.Step(ctx); err != nil && ctx.Err() == nil {
		t.Fatalf("Step: %v", err)
	}

	if got := h.store.jobStatus("EUW1_1"); got != "claimed" {
		t.Fatalf("job status = %q, want claimed: a row whose payload is not durable must not be closed", got)
	}
	if got := h.store.logCount("complete "); got != 0 {
		t.Fatalf("completed rows = %d, want 0", got)
	}
	if got := h.store.deadJobs(); got != 0 {
		t.Fatalf("dead letters = %d, want 0: a stop is not a failure of the row", got)
	}
	if got := h.store.jobCount(); got != 1 {
		t.Fatalf("queue holds %d rows, want 1: nothing may be dropped on shutdown", got)
	}
}

// A shutdown must never spend the last attempt on a dead letter. A row at the
// ceiling that is interrupted by a TERM has not failed; it has not been tried
// in the run that will pick it up next.
func TestAShutdownAtTheCeilingReleasesInsteadOfRetiring(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newHarness(t, func(opts *WorkerOptions) { opts.MaxAttempts = 3 })
	h.fetcher.errors["match:EUW1_1"] = &riot.StatusError{Method: "match", Status: 500}
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1", Attempts: 3})
	h.fetcher.onFetch = func(string) { cancel() }

	if _, err := h.worker.Step(ctx); err != nil && ctx.Err() == nil {
		t.Fatalf("Step: %v", err)
	}

	if got := h.store.jobStatus("EUW1_1"); got != "retry" {
		t.Fatalf("job status = %q, want retry: a graceful stop must not retire a row that still has a chance", got)
	}
	if got := h.store.deadJobs(); got != 0 {
		t.Fatalf("dead letters = %d, want 0", got)
	}
}

// D4. A graceful TERM used to strand the unstarted tail of the batch: the rows
// were claimed by a process that was exiting, so nothing could pick them up
// until the claim grace expired - nineteen of a twenty-row batch in the
// verification run.
func TestAGracefulStopReleasesTheUnstartedRowsOfTheBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newHarness(t, nil)
	ids := []string{"EUW1_1", "EUW1_2", "EUW1_3", "EUW1_4", "EUW1_5"}
	for _, id := range ids {
		h.serveFixture(t, id)
		h.store.forceEnqueue(contract.QueueItem{MatchID: id})
	}
	// The TERM lands while the first match of the batch is in flight.
	var fetched int
	h.fetcher.onFetch = func(string) {
		fetched++
		if fetched == 1 {
			cancel()
		}
	}

	if _, err := h.worker.Step(ctx); err != nil && ctx.Err() == nil {
		t.Fatalf("Step: %v", err)
	}

	released := 0
	for _, id := range ids {
		switch got := h.store.jobStatus(id); got {
		case "claimed":
			t.Fatalf("job %s is still claimed after a graceful stop: the next process cannot see it", id)
		case "retry", "pending":
			released++
		case "dead":
			t.Fatalf("job %s was retired by a shutdown", id)
		}
	}
	if released == 0 {
		t.Fatal("no row was released: the unstarted tail of the batch is still invisible")
	}
	// The negative control for the durability invariant: releasing is not
	// dropping. Every row is still in the queue, waiting to be tried again.
	if got := h.store.jobCount(); got != len(ids) {
		t.Fatalf("queue holds %d rows, want %d: nothing may be dropped on shutdown", got, len(ids))
	}
}
