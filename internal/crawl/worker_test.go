package crawl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// harness is the fixture set every worker test starts from: fake time, an
// in-memory control plane, a fixture-backed fetcher and a recording archive.
type harness struct {
	worker  *Worker
	store   *fakeStore
	fetcher *fakeFetcher
	writer  *fakeWriter
	clock   *riot.FakeClock
	opts    WorkerOptions
}

func newHarness(t *testing.T, mutate func(*WorkerOptions)) *harness {
	t.Helper()
	clock := riot.NewFakeClock(testBaseTime())
	store := newFakeStore()
	fetcher := newFakeFetcher("RGAPI-test-key")
	writer := newFakeWriter()

	opts := WorkerOptions{
		Deps: testDeps(store, fetcher, writer, clock),
	}
	if mutate != nil {
		mutate(&opts)
	}
	w, err := NewWorker(opts)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	return &harness{worker: w, store: store, fetcher: fetcher, writer: writer, clock: clock, opts: opts}
}

// serveFixture registers the synthetic match under id, exactly as the client
// would hand it over: decoded value plus verbatim body.
func (h *harness) serveFixture(t *testing.T, id string) riot.MatchDTO {
	t.Helper()
	body := fixture(t, "match-v5/synthetic-ranked-solo.json")
	dto := fixtureMatch(t)
	h.fetcher.serve(id, dto, body)
	return dto
}

func TestNewWorkerRequiresCollaborators(t *testing.T) {
	clock := riot.NewFakeClock(testBaseTime())
	store := newFakeStore()
	fetcher := newFakeFetcher("k")
	writer := newFakeWriter()

	tests := []struct {
		name string
		deps Deps
	}{
		{name: "no store", deps: Deps{Fetcher: fetcher, Writer: writer, Clock: clock}},
		{name: "no fetcher", deps: Deps{Store: store, Writer: writer, Clock: clock}},
		{name: "no writer", deps: Deps{Store: store, Fetcher: fetcher, Clock: clock}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewWorker(WorkerOptions{Deps: tc.deps}); err == nil {
				t.Fatal("NewWorker accepted an incomplete collaborator set")
			}
		})
	}
}

// The zero value of every option must be valid: the commands pass a partial
// struct and rely on the defaults.
func TestNewWorkerFillsDefaults(t *testing.T) {
	h := newHarness(t, nil)
	if h.worker.opts.JobBatch != DefaultJobBatch ||
		h.worker.opts.FrontierBatch != DefaultFrontierBatch ||
		h.worker.opts.HistoryCount != DefaultHistoryCount ||
		h.worker.opts.MaxAttempts != DefaultMaxAttempts ||
		h.worker.opts.JobTimeout != DefaultJobTimeout ||
		h.worker.opts.RetryBase != DefaultRetryBase ||
		h.worker.opts.RetryMax != DefaultRetryMax ||
		h.worker.opts.PollInterval != DefaultPollInterval ||
		h.worker.opts.Queue != 420 {
		t.Fatalf("defaults not applied: %+v", h.worker.opts)
	}
	if h.worker.deps.Region != DefaultRegion {
		t.Fatalf("region = %q", h.worker.deps.Region)
	}
	if h.worker.pacer == nil || h.worker.keys == nil {
		t.Fatal("the optional fetcher surfaces were not discovered")
	}
}

// The job deadline has to outlast the slowest Riot call the client will make on
// a row's behalf. One attempt may take riot.DefaultTimeout, and the client is
// allowed to sit out riot.DefaultRetryWaitBudget of Riot's own backpressure
// inside that same call; a job timeout shorter than the two together would cut
// a throttled fetch off in the middle of a wait Riot asked for, requeue the row
// and leave the wait unpaid - the measured defect, one level up. Both sides
// live in different packages, so the relationship is pinned here.
func TestJobTimeoutOutlastsTheClientsSlowestCall(t *testing.T) {
	slowest := riot.DefaultTimeout + riot.DefaultRetryWaitBudget
	if DefaultJobTimeout <= slowest {
		t.Fatalf("DefaultJobTimeout = %s, want more than the client's slowest call (%s)",
			DefaultJobTimeout, slowest)
	}
}

// Step drains the queue before walking the frontier, which is what makes a
// restart with a backlog finish what it already claimed.
func TestStepDrainsTheQueueBeforeWalkingTheFrontier(t *testing.T) {
	h := newHarness(t, nil)
	h.serveFixture(t, "EUW1_0000000000")
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_0000000000"})
	h.store.seedFrontier(contract.FrontierEntry{PUUID: "someone", Priority: PriorityPlayer})

	processed, err := h.worker.Step(context.Background())
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if h.store.logCount("claim-frontier") != 0 {
		t.Fatalf("frontier was walked while the queue had work: %v", h.store.calls)
	}
	if h.fetcher.fetchCount("history:") != 0 {
		t.Fatalf("history fetched while the queue had work: %v", h.fetcher.allCalls())
	}
}

func TestProcessJobArchivesRecordsAndWidens(t *testing.T) {
	h := newHarness(t, nil)
	dto := h.serveFixture(t, "EUW1_0000000000")
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_0000000000", Priority: PriorityPlayer})

	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}

	if got := h.writer.matchIDs(); len(got) != 1 || got[0] != "EUW1_0000000000" {
		t.Fatalf("archive holds %v, want the fetched match", got)
	}
	// The verbatim-byte promise is proved against the real client in
	// client_http_test.go; here the archive write itself is what is checked.
	if len(h.writer.matchBodies["EUW1_0000000000"]) == 0 {
		t.Fatal("the archive received no payload for the fetched match")
	}
	meta := h.writer.metas[0]
	if meta.Region != DefaultRegion || meta.Patch != PatchFromGameVersion(dto.Info.GameVersion) {
		t.Fatalf("archived provenance = %+v", meta)
	}
	if h.store.matchCount() != 1 {
		t.Fatalf("matches = %d, want 1", h.store.matchCount())
	}
	if h.store.jobStatus("EUW1_0000000000") != "done" {
		t.Fatalf("job status = %q, want done", h.store.jobStatus("EUW1_0000000000"))
	}
	if h.writer.flushCount() == 0 {
		t.Fatal("the archive was never flushed; a crash would lose the payload")
	}

	puuids := ParticipantPUUIDs(dto)
	if len(puuids) != 10 {
		t.Fatalf("fixture has %d participants, want 10", len(puuids))
	}
	for _, puuid := range puuids {
		entry := h.store.frontierEntry(puuid)
		if entry.Priority != PriorityParticipant {
			t.Fatalf("frontier entry %s priority = %d, want %d", puuid, entry.Priority, PriorityParticipant)
		}
		if entry.Region != DefaultRegion {
			t.Fatalf("frontier entry %s region = %q", puuid, entry.Region)
		}
	}
	if got := h.store.FrontierSizeLocked(); got != 10 {
		t.Fatalf("frontier size = %d, want 10", got)
	}
}

// Re-crawling a player's history must be a no-op: the match id is fetched once,
// the archive keeps one record of it, and the control plane stays at one row.
//
// The archive half of that is not free. `matches` is idempotent because match_id
// is its primary key, but the archive is append-only parquet with no key at all,
// so a second walk used to append a second verbatim copy of the payload - the
// duplicate the build counts as extra participants for that match. The crawl now
// asks the control plane before it spends a fetch, and closes the row when the
// answer is that the match is already stored.
func TestRecrawlingAMatchIsANoOp(t *testing.T) {
	h := newHarness(t, nil)
	h.serveFixture(t, "EUW1_0000000000")

	for i := 0; i < 2; i++ {
		h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_0000000000", Priority: PriorityPlayer})
		if _, err := h.worker.Step(context.Background()); err != nil {
			t.Fatalf("Step %d: %v", i, err)
		}
	}

	if got := h.fetcher.fetchCount("match:"); got != 1 {
		t.Fatalf("fetches = %d, want 1: a match the control plane already holds must not be re-fetched", got)
	}
	if got := h.writer.writeCount(); got != 1 {
		t.Fatalf("archive records = %d, want 1: the archive has no key, so a second walk appends a second copy of a payload that is already stored", got)
	}
	if h.store.matchCount() != 1 {
		t.Fatalf("matches = %d, want 1: match_id is the idempotency key", h.store.matchCount())
	}
	if got := h.store.FrontierSizeLocked(); got != 10 {
		t.Fatalf("frontier size = %d, want 10: a re-crawl must not duplicate puuids", got)
	}
}

func TestProcessJobFailureHandling(t *testing.T) {
	tests := []struct {
		name       string
		fetchErr   error
		attempts   int
		maxAttem   int
		wantStatus string
		wantCause  bool
		// checkAttempts asserts the attempt count after the failure, which is
		// how a global condition is told apart from this row's own failure.
		checkAttempts bool
		wantAttempts  int
	}{
		{
			name:       "transient failure requeues",
			fetchErr:   &riot.StatusError{Method: "match", Status: 500},
			wantStatus: "retry",
			wantCause:  true,
		},
		{
			name:       "rate limit requeues",
			fetchErr:   &riot.RateLimitedError{Method: "match", Attempts: 5, RetryAfter: time.Second},
			wantStatus: "retry",
			wantCause:  true,
		},
		{
			name:          "circuit breaker open hands the row back without burning it",
			fetchErr:      riot.ErrCircuitOpen,
			attempts:      2,
			wantStatus:    "retry",
			wantCause:     true,
			checkAttempts: true,
			wantAttempts:  2,
		},
		{
			// A refused key is a global condition. Before the fix this row
			// reached the ceiling and was retired for an outage that was not
			// its fault.
			name:          "a refused key does not dead-letter at the attempt ceiling",
			fetchErr:      &riot.StatusError{Method: "match", Status: 403},
			attempts:      1,
			maxAttem:      2,
			wantStatus:    "retry",
			wantCause:     true,
			checkAttempts: true,
			wantAttempts:  1,
		},
		{
			name:          "401 is treated like 403",
			fetchErr:      &riot.StatusError{Method: "match", Status: 401},
			attempts:      1,
			maxAttem:      2,
			wantStatus:    "retry",
			wantCause:     true,
			checkAttempts: true,
			wantAttempts:  1,
		},
		{
			name:       "a purge is permanent",
			fetchErr:   &riot.StatusError{Method: "match", Status: 404},
			wantStatus: "dead",
			wantCause:  true,
		},
		{
			name:       "a gone match is permanent",
			fetchErr:   &riot.StatusError{Method: "match", Status: 410},
			wantStatus: "dead",
			wantCause:  true,
		},
		{
			name:       "our own bad request is permanent",
			fetchErr:   &riot.StatusError{Method: "match", Status: 400},
			wantStatus: "dead",
			wantCause:  true,
		},
		{
			name:       "the attempt ceiling dead-letters a poison key",
			fetchErr:   &riot.StatusError{Method: "match", Status: 503},
			attempts:   1,
			maxAttem:   2,
			wantStatus: "dead",
			wantCause:  true,
		},
		{
			name:       "below the ceiling it still retries",
			fetchErr:   &riot.StatusError{Method: "match", Status: 503},
			attempts:   0,
			maxAttem:   2,
			wantStatus: "retry",
			wantCause:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, func(opts *WorkerOptions) {
				opts.MaxAttempts = defaultInt(tc.maxAttem, DefaultMaxAttempts)
			})
			h.fetcher.errors["match:EUW1_1"] = tc.fetchErr
			h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1", Attempts: tc.attempts})

			if _, err := h.worker.Step(context.Background()); err != nil {
				t.Fatalf("Step: %v", err)
			}
			if got := h.store.jobStatus("EUW1_1"); got != tc.wantStatus {
				t.Fatalf("job status = %q, want %q", got, tc.wantStatus)
			}
			if tc.wantCause && h.store.jobCause("EUW1_1") == "" {
				t.Fatal("no cause recorded on the row")
			}
			if tc.checkAttempts {
				job := h.store.jobFor("EUW1_1")
				if job == nil {
					t.Fatal("the row vanished from the queue")
				}
				if job.item.Attempts != tc.wantAttempts {
					t.Fatalf("attempts = %d, want %d: the row paid for someone else's failure",
						job.item.Attempts, tc.wantAttempts)
				}
			}
			if h.store.matchCount() != 0 {
				t.Fatal("a failed fetch must not be recorded as retained")
			}
			if h.writer.writeCount() != 0 {
				t.Fatal("a failed fetch must not reach the archive")
			}
		})
	}
}

// A transient failure is scheduled, not immediate: a Riot outage must not turn
// into a hot loop across every worker.
func TestTransientFailureSchedulesTheRetryInTheFuture(t *testing.T) {
	h := newHarness(t, nil)
	h.fetcher.errors["match:EUW1_1"] = &riot.StatusError{Method: "match", Status: 500}
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}
	notBefore := h.store.notBefore("EUW1_1")
	if !notBefore.After(testBaseTime()) {
		t.Fatalf("not_before = %s, want a future instant", notBefore)
	}
	delay := notBefore.Sub(testBaseTime())
	if delay < DefaultRetryBase/2 || delay > DefaultRetryBase {
		t.Fatalf("retry delay = %s, want within half-jitter of %s", delay, DefaultRetryBase)
	}

	// The row is not claimed again before its deadline.
	items, err := h.store.ClaimJobs(context.Background(), 10, testBaseTime())
	if err != nil {
		t.Fatalf("ClaimJobs: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("claimed %d rows before not_before", len(items))
	}
}

// A shutdown is not a verdict on the row: the claim is released immediately so
// the next process does not wait out the claim window. A cancellation from the
// row's own call, with the run still going, is a different thing and has to be
// scheduled like the retry it is.
func TestCancelledFetchReleasesTheRowImmediatelyWhenTheRunIsStopping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newHarness(t, nil)
	h.fetcher.errors["match:EUW1_1"] = fmt.Errorf("fetch: %w", context.Canceled)
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})
	h.fetcher.onFetch = func(string) { cancel() }

	if _, err := h.worker.Step(ctx); err != nil && ctx.Err() == nil {
		t.Fatalf("Step: %v", err)
	}
	if got := h.store.jobStatus("EUW1_1"); got != "retry" {
		t.Fatalf("job status = %q, want retry: a cancelled row must not stay claimed", got)
	}
	if got := h.store.notBefore("EUW1_1"); !got.Equal(testBaseTime()) {
		t.Fatalf("not_before = %s, want the instant the run stopped", got)
	}
}

// The negative control for the test above: the same cancelled call in a run
// that is still going - a per-call timeout, which the client reports as its own
// deadline - must be pulled back on the retry schedule. Releasing it at "now"
// is what turned eight rows of the verification run into an immediate retry
// loop against a suspended key.
func TestCancelledFetchInALiveRunIsScheduled(t *testing.T) {
	h := newHarness(t, nil)
	h.fetcher.errors["match:EUW1_1"] = fmt.Errorf("fetch: %w", context.Canceled)
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got := h.store.jobStatus("EUW1_1"); got != "retry" {
		t.Fatalf("job status = %q, want retry", got)
	}
	if got := h.store.notBefore("EUW1_1"); !got.After(testBaseTime()) {
		t.Fatalf("not_before = %s, want a future instant: nothing is shutting down", got)
	}
}

// An archive failure is local (disk, permissions) and the payload is still
// fetchable, so the row goes back rather than being dropped.
func TestArchiveFailureRequeuesWithoutRecordingTheMatch(t *testing.T) {
	h := newHarness(t, nil)
	h.serveFixture(t, "EUW1_1")
	h.writer.failMatch = errors.New("no space left on device")
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got := h.store.jobStatus("EUW1_1"); got != "retry" {
		t.Fatalf("job status = %q, want retry", got)
	}
	if cause := h.store.jobCause("EUW1_1"); cause != "archive" {
		t.Fatalf("cause = %q, want archive", cause)
	}
	if h.store.matchCount() != 0 {
		t.Fatal("a match was recorded even though its payload was not archived")
	}
}

// The reverse failure is deliberately not handled the same way: the payload is
// already durable, so leaving the row claimed means the retry re-fetches and
// the DO NOTHING insert collapses it to one row.
func TestUpsertFailureLeavesTheRowClaimed(t *testing.T) {
	h := newHarness(t, nil)
	h.serveFixture(t, "EUW1_1")
	h.store.failUpsert = errTestStoreDown
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	if _, err := h.worker.Step(context.Background()); err == nil {
		t.Fatal("Step swallowed a control-plane failure")
	}
	if got := h.store.jobStatus("EUW1_1"); got != "claimed" {
		t.Fatalf("job status = %q, want claimed: the payload is already archived", got)
	}
	if h.writer.writeCount() != 1 {
		t.Fatal("the payload should have reached the archive before the upsert")
	}
}

func TestFlushFailureSurfacesAsAStepError(t *testing.T) {
	h := newHarness(t, nil)
	h.serveFixture(t, "EUW1_1")
	h.writer.failFlush = errors.New("read-only file system")
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	if _, err := h.worker.Step(context.Background()); err == nil {
		t.Fatal("Step swallowed an archive flush failure")
	}
}

// A row that reads 'done' is never offered again, so it may only be closed
// once its payload is a renamed part on disk. Completing the batch before the
// flush would turn a crash or a failed flush into silent data loss: the queue
// would look healthy while the archive holds nothing.
func TestFlushFailureLeavesEveryBatchedRowUnDone(t *testing.T) {
	h := newHarness(t, func(o *WorkerOptions) { o.JobBatch = 5 })
	ids := []string{"EUW1_1", "EUW1_2", "EUW1_3"}
	for _, id := range ids {
		h.serveFixture(t, id)
		h.store.forceEnqueue(contract.QueueItem{MatchID: id})
	}
	h.writer.failFlush = errors.New("read-only file system")

	if _, err := h.worker.Step(context.Background()); err == nil {
		t.Fatal("Step swallowed an archive flush failure")
	}

	if h.writer.writeCount() != len(ids) {
		t.Fatalf("archive writes = %d, want %d: the batch must be written before it is flushed",
			h.writer.writeCount(), len(ids))
	}
	for _, id := range ids {
		if got := h.store.jobStatus(id); got != "retry" {
			t.Fatalf("job %s status = %q, want retry: a row whose payload never became durable must not be closed (done would lose it forever)", id, got)
		}
		if got := h.store.jobCause(id); got != "archive flush: read-only file system" {
			t.Fatalf("job %s cause = %q, want the flush failure", id, got)
		}
		if got := h.store.notBefore(id); !got.After(testBaseTime()) {
			t.Fatalf("job %s not_before = %s, want a future instant", id, got)
		}
	}
}

// The happy path keeps one flush per batch: the fix must not turn a batch into
// a flush per row.
func TestBatchFlushesOnceAndThenCompletesEveryRow(t *testing.T) {
	h := newHarness(t, func(o *WorkerOptions) { o.JobBatch = 5 })
	ids := []string{"EUW1_1", "EUW1_2", "EUW1_3"}
	for _, id := range ids {
		h.serveFixture(t, id)
		h.store.forceEnqueue(contract.QueueItem{MatchID: id})
	}

	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got := h.writer.flushCount(); got != 1 {
		t.Fatalf("flushes = %d, want 1 for a batch of %d", got, len(ids))
	}
	for _, id := range ids {
		if got := h.store.jobStatus(id); got != "done" {
			t.Fatalf("job %s status = %q, want done once the flush published the part", id, got)
		}
		if got := h.store.logCount("complete "); got != len(ids) {
			t.Fatalf("CompleteJob calls = %d, want one per row (%d)", got, len(ids))
		}
	}
}

// A completion failure is surfaced, not swallowed: the payload is durable by
// then, so the next pass re-fetches and the DO NOTHING insert collapses it.
func TestCompleteJobFailureAfterTheFlushIsSurfaced(t *testing.T) {
	h := newHarness(t, nil)
	h.serveFixture(t, "EUW1_1")
	h.store.failComplete = errTestStoreDown
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	if _, err := h.worker.Step(context.Background()); err == nil {
		t.Fatal("Step swallowed a control-plane failure while completing a durable row")
	}
	if h.writer.flushCount() != 1 {
		t.Fatal("the row was completed before the archive was flushed")
	}
}

func TestWalkFrontierEnqueuesHistoryAndStampsTheClaim(t *testing.T) {
	h := newHarness(t, nil)
	h.fetcher.history["p1"] = []string{"EUW1_1", "EUW1_2"}
	h.store.seedFrontier(contract.FrontierEntry{PUUID: "p1", Priority: PriorityPlayer})

	touched, err := h.worker.Step(context.Background())
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if touched != 1 {
		t.Fatalf("touched = %d, want 1", touched)
	}
	if got := h.store.queueLen(); got != 2 {
		t.Fatalf("queue length = %d, want 2", got)
	}
	entry := h.store.frontierEntry("p1")
	if !entry.LastFetchedAt.Equal(testBaseTime()) {
		t.Fatalf("last fetched = %s, want the claim instant", entry.LastFetchedAt)
	}
	if entry.ConsecutiveEmpty != 0 {
		t.Fatalf("consecutive empty = %d, want 0: the history was not empty", entry.ConsecutiveEmpty)
	}
	if got := h.store.logCount("mark-fetched"); got != 1 {
		t.Fatalf("mark-fetched calls = %d, want 1", got)
	}
}

// An empty history is what a fruitless player looks like, and it is counted so
// that pruning has something to go on.
func TestWalkFrontierCountsAnEmptyHistory(t *testing.T) {
	h := newHarness(t, nil)
	h.store.seedFrontier(contract.FrontierEntry{PUUID: "p1", Priority: PriorityPlayer})

	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got := h.store.frontierEntry("p1").ConsecutiveEmpty; got != 1 {
		t.Fatalf("consecutive empty = %d, want 1", got)
	}
}

// A 404 on a player's history means the account is gone: Riot pruning an
// account is permanent, so the entry is marked dead rather than retried.
func TestWalkFrontierMarksAGonePlayerDead(t *testing.T) {
	h := newHarness(t, nil)
	h.fetcher.errors["history:p1"] = &riot.StatusError{Method: "match-ids", Status: 404}
	h.store.seedFrontier(contract.FrontierEntry{PUUID: "p1", Priority: PriorityPlayer})

	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if !h.store.frontierEntry("p1").Dead {
		t.Fatal("a 404 on history left the entry live")
	}
	if got := h.store.logCount("mark-dead"); got != 1 {
		t.Fatalf("mark-dead calls = %d, want 1", got)
	}
}

// Riot being unavailable must not penalise the player: the claim is left to
// expire instead of the entry being marked dead.
func TestWalkFrontierLeavesATransientFailureClaimed(t *testing.T) {
	h := newHarness(t, nil)
	h.fetcher.errors["history:p1"] = &riot.StatusError{Method: "match-ids", Status: 503}
	h.store.seedFrontier(contract.FrontierEntry{PUUID: "p1", Priority: PriorityPlayer})

	if _, err := h.worker.Step(context.Background()); err == nil {
		t.Fatal("Step swallowed a transient frontier failure")
	}
	entry := h.store.frontierEntry("p1")
	if entry.Dead {
		t.Fatal("a transient failure marked the player dead")
	}
	if entry.ConsecutiveEmpty != 0 {
		t.Fatalf("consecutive empty = %d, want 0", entry.ConsecutiveEmpty)
	}
}

// A history page full of known matches must not grow the queue: this is the
// property that makes re-crawling a player cheap.
func TestEnqueueingHistoryTwiceAddsNothing(t *testing.T) {
	h := newHarness(t, nil)
	h.fetcher.history["p1"] = []string{"EUW1_1", "EUW1_2"}

	for pass := 0; pass < 2; pass++ {
		h.store.seedFrontier(contract.FrontierEntry{PUUID: "p1", Priority: PriorityPlayer})
		if _, err := h.worker.Step(context.Background()); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if got := h.store.queueLen(); got != 2 {
		t.Fatalf("queue length = %d, want 2: match ids are unique", got)
	}
}

// Killing a worker mid-batch leaves rows claimed. A new worker must not lose
// them, must not duplicate them, and must finish them once maintenance
// reclaims the abandoned claims.
func TestKillAndRestartResumesWithoutLossOrDuplication(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, func(opts *WorkerOptions) { opts.JobBatch = 1 })
	for _, id := range []string{"EUW1_1", "EUW1_2", "EUW1_3"} {
		h.serveFixture(t, id)
		h.store.forceEnqueue(contract.QueueItem{MatchID: id, Priority: PriorityPlayer})
	}

	if processed, err := h.worker.Step(ctx); err != nil || processed != 1 {
		t.Fatalf("first Step = (%d, %v), want (1, nil)", processed, err)
	}

	// The process dies here: two rows were claimed and never completed.
	claimed, err := h.store.ClaimJobs(ctx, 10, testBaseTime())
	if err != nil {
		t.Fatalf("ClaimJobs: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed = %d, want 2", len(claimed))
	}
	if got := h.store.logCount("upsert-match"); got != 1 {
		t.Fatalf("upserts = %d, want 1", got)
	}

	// A fresh worker cannot see the abandoned rows, so it does not re-fetch
	// them: the claim is still held, and the walk it does instead is the
	// frontier work the dead worker never got to.
	restarted := &harness{worker: mustWorker(t, WorkerOptions{Deps: h.opts.Deps, JobBatch: 1}),
		store: h.store, fetcher: h.fetcher, writer: h.writer, clock: h.clock}
	if _, err := restarted.worker.Step(ctx); err != nil {
		t.Fatalf("restarted Step: %v", err)
	}
	if got := h.fetcher.fetchCount("match:"); got != 1 {
		t.Fatalf("fetches = %d, want 1: a claimed row must not be handed out twice", got)
	}

	// The claim grace is what distinguishes "abandoned" from "in flight", so
	// time has to pass before maintenance is allowed to touch the rows.
	h.clock.Advance(DefaultClaimGrace + time.Minute)

	maintain, err := Maintain(ctx, MaintainOptions{
		Deps:       h.opts.Deps,
		ClaimGrace: DefaultClaimGrace,
		DryRun:     false,
	})
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if maintain.ReclaimedClaims != 2 {
		t.Fatalf("reclaimed = %d, want 2", maintain.ReclaimedClaims)
	}

	for i := 0; i < 3; i++ {
		if _, err := restarted.worker.Step(ctx); err != nil {
			t.Fatalf("restart Step %d: %v", i, err)
		}
	}

	for _, id := range []string{"EUW1_1", "EUW1_2", "EUW1_3"} {
		if got := h.store.jobStatus(id); got != "done" {
			t.Fatalf("job %s status = %q, want done", id, got)
		}
	}
	if h.store.matchCount() != 3 {
		t.Fatalf("matches = %d, want 3", h.store.matchCount())
	}
	// Three keys, three fetches: the two reclaimed rows are fetched once more
	// because a claim that was lost carries no payload, and the extra INSERT
	// collapses on match_id rather than producing a second row.
	if got := h.fetcher.fetchCount("match:"); got != 3 {
		t.Fatalf("fetches = %d, want 3", got)
	}
	if got := h.store.logCount("upsert-match"); got != 3 {
		t.Fatalf("upserts = %d, want 3: one row per key, never a duplicate", got)
	}
}

// mustWorker is newHarness without the harness wrapper, for tests that build a
// second worker over the same collaborators.
func mustWorker(t *testing.T, opts WorkerOptions) *Worker {
	t.Helper()
	w, err := NewWorker(opts)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	return w
}

func TestRunIdlesWithoutAKeyAndStartsWhenOneAppears(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clock := newStopClock(testBaseTime(), 0, cancel)
	store := newFakeStore()
	fetcher := newFakeFetcher("")
	writer := newFakeWriter()
	store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})
	// The key arrives on the second poll, which is what a rotated key file
	// looks like to a worker that is already running.
	clock.onSleep = func(n int) {
		if n == 2 {
			fetcher.mu.Lock()
			fetcher.key = "RGAPI-rotated"
			fetcher.mu.Unlock()
		}
	}
	clock.after = 3

	w := mustWorker(t, WorkerOptions{Deps: testDeps(store, fetcher, writer, clock)})
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v, want nil: a cancelled context is a normal shutdown", err)
	}
	if got := store.logCount("claim-jobs"); got == 0 {
		t.Fatal("the worker never claimed work after the key appeared")
	}
	if got := fetcher.fetchCount("match:"); got == 0 {
		t.Fatal("the worker never fetched after the key appeared")
	}
	if got := writer.flushCount(); got == 0 {
		t.Fatal("shutdown did not flush the archive")
	}
}

// A declared expiry in the past must stop the crawl and exit non-zero rather
// than spend the day's remaining budget discovering the same fact one 401 at a
// time. The context here is never cancelled, so the run has to stop on its own.
func TestRunRefusesToCrawlPastADeclaredKeyExpiry(t *testing.T) {
	clock := riot.NewFakeClock(testBaseTime())
	store := newFakeStore()
	fetcher := newFakeFetcher("RGAPI-test-key")
	writer := newFakeWriter()
	store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	expires := testBaseTime().Add(-90 * time.Minute)
	deps := testDeps(store, fetcher, writer, clock)
	deps.KeyExpiry = riot.NewKeyExpiry(expires)

	err := mustWorker(t, WorkerOptions{Deps: deps}).Run(context.Background())
	if !errors.Is(err, riot.ErrKeyExpired) {
		t.Fatalf("Run = %v, want ErrKeyExpired: an expired key is a failure, not a shutdown", err)
	}
	if got := fetcher.fetchCount(""); got != 0 {
		t.Fatalf("the worker made %d Riot calls with an expired key", got)
	}
	if got := store.logCount("claim-jobs"); got != 0 {
		t.Fatalf("the worker claimed %d batches with an expired key", got)
	}
	if got := store.logCount("complete-job"); got != 0 {
		t.Fatalf("the worker completed %d jobs with an expired key", got)
	}
	// Stopping must not cost the payloads already in the open part file.
	if writer.flushCount() == 0 {
		t.Fatal("the expired-key exit did not flush the archive")
	}
}

// The other half: a deadline in the future is not a reason to stop, and an
// undeclared deadline is not a reason either. Both are the zero-risk cases that
// would make the check above dangerous if it were written the other way round.
func TestRunCrawlsUntilTheDeclaredExpiryArrives(t *testing.T) {
	store := newFakeStore()
	fetcher := newFakeFetcher("RGAPI-test-key")
	writer := newFakeWriter()
	store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := newStopClock(testBaseTime(), 2, cancel)

	deps := testDeps(store, fetcher, writer, clock)
	deps.KeyExpiry = riot.NewKeyExpiry(testBaseTime().Add(time.Hour))

	if err := mustWorker(t, WorkerOptions{Deps: deps}).Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil: a deadline an hour away must not stop the crawl", err)
	}
	if got := fetcher.fetchCount("match:"); got == 0 {
		t.Fatal("the worker never fetched while the declared key was still valid")
	}
}

// A keyless worker must stay up and do nothing, not crash-loop.
func TestRunWithoutAKeyDoesNoWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clock := newStopClock(testBaseTime(), 0, cancel)
	clock.after = 3
	store := newFakeStore()
	fetcher := newFakeFetcher("")
	writer := newFakeWriter()
	store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	w := mustWorker(t, WorkerOptions{Deps: testDeps(store, fetcher, writer, clock)})
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v, want nil", err)
	}
	if got := fetcher.fetchCount(""); got != 0 {
		t.Fatalf("a keyless worker made %d calls", got)
	}
	if got := store.logCount("claim-jobs"); got != 0 {
		t.Fatalf("a keyless worker claimed %d batches", got)
	}
	if got := clock.Total(); got < 2*DefaultPollInterval {
		t.Fatalf("idle sleep total = %s, want at least two poll intervals", got)
	}
}

// The pacer and key surfaces are discovered by type assertion, which no
// signature change would break loudly. This wires the real client exactly as
// main.go does, so a client that stopped exposing either one is a test failure
// rather than a keyless worker claiming rows it cannot fetch or a rate-limited
// worker claiming rows it cannot afford.
func TestRealRiotClientExposesTheOptionalWorkerSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	client, err := riot.NewClient(riot.Options{
		PlatformBaseURL: srv.URL,
		RegionalBaseURL: srv.URL,
		KeyProvider:     riot.NewKeyProviderFrom("", ""),
		HTTPClient:      srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)

	w, err := NewWorker(WorkerOptions{
		Deps: testDeps(newFakeStore(), client, newFakeWriter(), riot.NewFakeClock(testBaseTime())),
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if w.pacer == nil || w.keys == nil {
		t.Fatal("the real client does not expose both optional worker surfaces")
	}
	if _, ok := client.Key(); ok {
		t.Fatal("a keyless client reported a key")
	}
	if wait, blocked := client.Blocked(); blocked || wait != 0 {
		t.Fatalf("a fresh client is blocked for %s, want no wait", wait)
	}
	if client.Advertised() {
		t.Fatal("a fresh client claims to have seen rate headers")
	}
	if rate := client.EffectiveRate(); rate <= 0 {
		t.Fatalf("effective rate = %v, want the conservative default", rate)
	}
}

// A control-plane outage backs the loop off instead of hot-looping, and it
// never exits: the crawler is a long-running process, not a batch job.
func TestRunBacksOffOnAPassFailureAndKeepsGoing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clock := newStopClock(testBaseTime(), 0, cancel)
	clock.after = 3
	store := newFakeStore()
	store.failClaim = errTestStoreDown
	fetcher := newFakeFetcher("RGAPI-test-key")
	writer := newFakeWriter()

	w := mustWorker(t, WorkerOptions{Deps: testDeps(store, fetcher, writer, clock)})
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v, want nil", err)
	}
	if got := store.logCount("claim-jobs"); got < 2 {
		t.Fatalf("claim attempts = %d, want repeated attempts", got)
	}
	if got := clock.sleeps; got < 2 {
		t.Fatalf("slept %d times, want a backoff between attempts", got)
	}
}

func TestPauseHonoursTheLimiterAndCapsTheWait(t *testing.T) {
	tests := []struct {
		name      string
		blocked   time.Duration
		wantPause bool
		wantWait  time.Duration
	}{
		{name: "not blocked", blocked: 0, wantPause: false},
		{name: "blocked briefly", blocked: 2 * time.Second, wantPause: true, wantWait: 2 * time.Second},
		{name: "a long suspension is capped so the context is re-checked", blocked: 10 * time.Minute, wantPause: true, wantWait: maxRateLimitPause},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			h.fetcher.blocked = tc.blocked

			paused, err := h.worker.pause(context.Background())
			if err != nil {
				t.Fatalf("pause: %v", err)
			}
			if paused != tc.wantPause {
				t.Fatalf("paused = %v, want %v", paused, tc.wantPause)
			}
			if got := h.clock.Total(); got != tc.wantWait {
				t.Fatalf("waited %s, want %s", got, tc.wantWait)
			}
		})
	}
}

func TestIdleWaitIsJitteredWithinHalfAPollInterval(t *testing.T) {
	h := newHarness(t, nil)
	half := h.worker.opts.PollInterval / 2
	seen := map[time.Duration]bool{}
	for i := 0; i < 40; i++ {
		got := h.worker.idleWait()
		if got < half || got > h.worker.opts.PollInterval {
			t.Fatalf("idleWait = %s, want within [%s, %s]", got, half, h.worker.opts.PollInterval)
		}
		seen[got] = true
	}
	if len(seen) == 1 {
		t.Fatal("idleWait is not jittered; several workers would claim in a thundering herd")
	}
}

func TestRetryDelayGrowsAndCaps(t *testing.T) {
	h := newHarness(t, func(opts *WorkerOptions) {
		opts.RetryBase = time.Minute
		opts.RetryMax = 8 * time.Minute
	})
	tests := []struct {
		attempts int
		min      time.Duration
		max      time.Duration
	}{
		{attempts: 1, min: 30 * time.Second, max: time.Minute},
		{attempts: 2, min: time.Minute, max: 2 * time.Minute},
		{attempts: 4, min: 4 * time.Minute, max: 8 * time.Minute},
		{attempts: 20, min: 4 * time.Minute, max: 8 * time.Minute},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("attempts=%d", tc.attempts), func(t *testing.T) {
			for i := 0; i < 20; i++ {
				got := h.worker.retryDelay(tc.attempts)
				if got < tc.min || got > tc.max {
					t.Fatalf("retryDelay(%d) = %s, want within [%s, %s]", tc.attempts, got, tc.min, tc.max)
				}
			}
		})
	}
}

// The pipeline staleness gauge is the alert that catches a crawl that has
// silently stopped moving.
func TestReportPublishesStalenessFrontierSizeAndKeyAge(t *testing.T) {
	h := newHarness(t, nil)
	rec := newRecorder()
	h.worker.deps.Metrics = rec
	h.fetcher.age, h.fetcher.haveAge = 13*time.Hour, true
	h.store.matches["EUW1_1"] = contract.MatchRecord{MatchID: "EUW1_1", FetchedAt: testBaseTime()}
	h.store.seedFrontier(contract.FrontierEntry{PUUID: "p1"}, contract.FrontierEntry{PUUID: "p2", Dead: true})
	h.clock.Advance(5 * time.Minute)

	h.worker.report(context.Background(), true)

	if got := rec.count("key-age"); got != 1 {
		t.Fatalf("key age was not published")
	}
	if got, want := fmt.Sprint(rec.calls), ""; got == want {
		t.Fatal("no metrics were recorded")
	}
	if got := rec.count("frontier-size 1"); got != 1 {
		t.Fatalf("frontier size not published: %v", rec.calls)
	}
	if got := rec.count("staleness crawl 300"); got != 1 {
		t.Fatalf("staleness not published: %v", rec.calls)
	}
}

// A store without the reporting surface must not stop the loop: the metrics
// are a diagnostic, not control flow.
func TestReportToleratesAStoreWithoutTheReportingSurface(t *testing.T) {
	h := newHarness(t, nil)
	h.worker.deps.Store = contractOnlyStore{Store: h.store}
	h.worker.reporter = nil

	h.worker.report(context.Background(), true)
}

// A healthy crawl has to be readable from the log, not only from Prometheus.
// The measured defect this pins: a full hour of ingest output that was 100%
// WARN "riot rate limited" - one line a minute, every one of them attempt 1 -
// on a crawler that was in fact fetching about forty-four matches a minute.
// Nothing in that hour said the loop was alive, so an hour of healthy
// throttling and an hour of genuine stall were the same bytes, which is the
// silent staleness the pipeline is required to report instead of hiding. The
// report interval's line names the numbers that freeze when the crawl stops,
// and it is promoted to a warning once they have.
func TestReportHeartbeatMakesAHealthyCrawlReadableAndAStalledOneLoud(t *testing.T) {
	var buf bytes.Buffer
	h := newHarness(t, nil)
	h.worker.deps.Log = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h.serveFixture(t, "EUW1_0000000000")
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_0000000000"})
	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}

	h.worker.report(context.Background(), true)
	healthy := buf.String()
	if got := strings.Count(healthy, "\n"); got != 1 {
		t.Fatalf("a healthy crawl wrote %d report lines, want 1: %s", got, healthy)
	}
	for _, want := range []string{
		`"level":"INFO"`,
		`"msg":"crawl pipeline status"`,
		`"matches_retained":1`,
		`"staleness":"0s"`,
	} {
		if !strings.Contains(healthy, want) {
			t.Fatalf("the status line does not carry %s: %s", want, healthy)
		}
	}

	// The same line, once the newest fetch is older than the staleness alert
	// holds for, is the warning: the counter and the age on it are the only
	// things that move when the queue empties and the frontier stops
	// yielding matches.
	buf.Reset()
	h.clock.Advance(StaleWarnAge + time.Minute)
	h.worker.report(context.Background(), true)
	stalled := buf.String()
	if got := strings.Count(stalled, "\n"); got != 1 {
		t.Fatalf("a stalled crawl wrote %d report lines, want 1: %s", got, stalled)
	}
	for _, want := range []string{`"level":"WARN"`, "crawl is not fetching", `"staleness":"1h1m0s"`} {
		if !strings.Contains(stalled, want) {
			t.Fatalf("a stalled crawl was not reported as one (%s missing): %s", want, stalled)
		}
	}
}
