package crawl

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
	"github.com/Erik-Schuetze/league-of-legends/internal/raw"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// Defaults for the worker loop. They are conservative on purpose: the crawler
// runs unattended on a development key, and the cost of an idle pass is one
// query while the cost of a burst is the rest of the day's budget.
const (
	DefaultJobBatch      = 20
	DefaultFrontierBatch = 20
	DefaultHistoryCount  = 20
	DefaultMaxAttempts   = 8
	// DefaultJobTimeout bounds one row's fetch, retries included, and it has to
	// outlast the slowest call the Riot client will make for that row: one
	// attempt (riot.DefaultTimeout) plus the wait the client is willing to pay
	// inside the call when Riot asks to be left alone
	// (riot.DefaultRetryWaitBudget). A deadline shorter than that would cut a
	// throttled row off mid-wait and requeue it without the wait ever being
	// paid, which is the defect the retry budget exists to remove - only moved
	// one level up. The pair is pinned by
	// TestJobTimeoutOutlastsTheClientsSlowestCall.
	DefaultJobTimeout = riot.DefaultTimeout + riot.DefaultRetryWaitBudget + 20*time.Second
	// DefaultRetryBase is how long a released row waits before its next claim.
	// It is deliberately on the order of one Riot rate-limit window: the row
	// left the queue because the key was throttled, so retrying it sooner is
	// only a way to be throttled again.
	DefaultRetryBase      = 15 * time.Second
	DefaultRetryMax       = 30 * time.Minute
	DefaultPollInterval   = 15 * time.Second
	DefaultReportInterval = 60 * time.Second
	// KeyWarnAge is when a key is old enough that an operator should expect
	// the next rotation. A Riot development key expires after 24 hours.
	KeyWarnAge = 12 * time.Hour
	// StaleWarnAge is how old the newest fetch has to be before the report
	// interval's line is a warning rather than a status line. It is the hour
	// the LolstatsCrawlStale rule holds for, so the log and the alert agree
	// about when the pipeline is late rather than merely quiet.
	StaleWarnAge = time.Hour
	// maxRateLimitPause caps a single adaptive pause. Riot's Retry-After is
	// honoured in full by the client's limiter; this cap only bounds how long
	// the worker sleeps in one go before re-checking its context.
	maxRateLimitPause = time.Minute
	// archiveCause is the cause recorded for a row whose payload could not be
	// archived. It is kept short because it is what an operator greps for in
	// the queue and in the dead-letter backlog.
	archiveCause = "archive"
	// archiveFlushCause is the same, for the rows whose payload reached the
	// writer but not the disk. The two are told apart because the second is a
	// whole batch failing at once.
	archiveFlushCause = "archive flush: "
	// claimRecoveryLimit bounds the claims one boot hands back. A stranded
	// backlog is bounded by what the dead process was holding, so this only has
	// to exceed a batch; it is the store's own claim ceiling, so that even a
	// fleet-wide restart costs one bounded statement per worker.
	claimRecoveryLimit = 1000
	// shutdownCloseTimeout bounds the detached write that closes the rows of a
	// batch that was archived and then interrupted. It only has to cover one
	// update per retained row on a healthy database, so it stays small: a
	// shutdown that cannot reach the store must not hang for a whole restart.
	shutdownCloseTimeout = 5 * time.Second
	// shutdownFlushTimeout bounds the detached flush of an interrupted batch.
	// Finalising a part renames a file and writes its footer, so it is disk
	// work rather than network work, but a batch can hold several parts.
	shutdownFlushTimeout = 15 * time.Second
)

// Worker drives one crawl loop: it drains fetch_queue, and when the queue is
// empty it walks the frontier to refill it. The order matters - a restart with
// a backlog must finish work it already claimed before discovering more.
type Worker struct {
	deps Deps
	opts WorkerOptions

	// Optional surfaces, discovered by type assertion so that tests can drive
	// the loop with a fake store that implements only what it needs.
	pacer    Pacer
	keys     KeySource
	reporter PipelineReporter

	lastReport  time.Time
	warnedEmpty bool
	rng         *rand.Rand

	// matchesRetained counts the payloads this process has put in the archive
	// since it started. It is the number the report interval publishes, and it
	// is the one that stops moving when the crawl stops working - which is how
	// a stalled pipeline is told apart from a throttled one that is still
	// fetching.
	matchesRetained int
}

// WorkerOptions configures the loop. The zero value is valid for every field
// except Deps; normalize fills the rest in.
type WorkerOptions struct {
	Deps Deps

	// Queue filters a player's history page. 420 is ranked solo, which is the
	// only queue the site publishes in v1; fetching other queues would cost
	// budget for payloads the build drops.
	Queue int

	JobBatch      int
	FrontierBatch int
	HistoryCount  int

	// MaxAttempts is the claim ceiling for one queue row. Reaching it moves
	// the row to 'dead' instead of leaving a poison key retrying forever.
	MaxAttempts int

	// JobTimeout bounds one Riot fetch, independent of the loop's context.
	JobTimeout time.Duration

	// ClaimGrace is how long a claim may be held before a starting worker
	// treats it as abandoned by a process that died. It is deliberately the
	// same threshold maintenance uses: it has to be longer than any claim a
	// running worker can legitimately be holding, or two workers fetch the
	// same match, which spends the rate-limit budget the queue exists to
	// protect.
	ClaimGrace time.Duration

	RetryBase    time.Duration
	RetryMax     time.Duration
	PollInterval time.Duration

	ReportInterval time.Duration
}

// NewWorker validates the collaborators the loop cannot run without.
func NewWorker(opts WorkerOptions) (*Worker, error) {
	opts.Deps.normalize()
	if opts.Deps.Store == nil {
		return nil, errors.New("crawl: worker needs a store")
	}
	if opts.Deps.Fetcher == nil {
		return nil, errors.New("crawl: worker needs a rioter")
	}
	if opts.Deps.Writer == nil {
		return nil, errors.New("crawl: worker needs a raw writer")
	}
	opts.Queue = defaultInt(opts.Queue, 420)
	opts.JobBatch = defaultInt(opts.JobBatch, DefaultJobBatch)
	opts.FrontierBatch = defaultInt(opts.FrontierBatch, DefaultFrontierBatch)
	opts.HistoryCount = defaultInt(opts.HistoryCount, DefaultHistoryCount)
	opts.MaxAttempts = defaultInt(opts.MaxAttempts, DefaultMaxAttempts)
	opts.JobTimeout = defaultDuration(opts.JobTimeout, DefaultJobTimeout)
	opts.ClaimGrace = defaultDuration(opts.ClaimGrace, DefaultClaimGrace)
	opts.RetryBase = defaultDuration(opts.RetryBase, DefaultRetryBase)
	opts.RetryMax = defaultDuration(opts.RetryMax, DefaultRetryMax)
	opts.PollInterval = defaultDuration(opts.PollInterval, DefaultPollInterval)
	opts.ReportInterval = defaultDuration(opts.ReportInterval, DefaultReportInterval)

	w := &Worker{
		deps: opts.Deps,
		opts: opts,
		rng:  rand.New(rand.NewSource(opts.Deps.Now().UnixNano())),
	}
	if p, ok := opts.Deps.Fetcher.(Pacer); ok {
		w.pacer = p
	}
	if k, ok := opts.Deps.Fetcher.(KeySource); ok {
		w.keys = k
	}
	if r, ok := opts.Deps.Store.(PipelineReporter); ok {
		w.reporter = r
	}
	return w, nil
}

// Pacer is the adaptive-rate surface the worker consults before claiming work.
// *riot.Limiter implements it; the worker only needs to know whether waiting
// would be pointless.
type Pacer interface {
	Blocked() (time.Duration, bool)
	Advertised() bool
	EffectiveRate() float64
}

// KeySource lets the worker idle instead of failing when no key is configured.
// This is what makes `worker` safe to start before the key exists: it stays up,
// logs once, and begins crawling when a key appears (for example when a
// rotating key file is written).
type KeySource interface {
	Key() (string, bool)
}

// KeyAger is the key-age surface the report loop reads, and the reason
// lolstats_riot_key_age_seconds exists at all. It is optional and separate from
// KeySource: a fetcher that cannot tell how old its key is publishes no gauge,
// rather than a zero that would be read as a key rotated a moment ago. The
// consumer asks a question the provider has to be able to decline to answer,
// which is why Age reports whether the duration means anything.
type KeyAger interface {
	Age() (time.Duration, bool)
}

// The keyless idle path is reached through a type assertion, so a fetcher that
// quietly loses these methods would turn "idle" into "claim rows, fail each
// one, burn their attempts" without any test failing. Asserting the wiring at
// compile time is what keeps a keyless worker honest. KeyAger is pinned for the
// same reason, one step further: its only other implementer is the crawl test
// fake, and a fake satisfying an assertion is exactly how the key-age gauge
// stayed dead while CI passed.
var (
	_ KeySource = (*riot.Client)(nil)
	_ KeyAger   = (*riot.Client)(nil)
	_ Pacer     = (*riot.Client)(nil)
)

// PipelineReporter is the optional extra store surface behind the pipeline
// metrics. It is not on the frozen contract because it is observability, not
// control flow.
type PipelineReporter interface {
	NewestFetchedAt(ctx context.Context) (time.Time, bool, error)
	FrontierSize(ctx context.Context) (int, error)
}

// Run drains the queue until the context is cancelled. A cancelled context is
// a normal shutdown, not a failure: the caller (cmd) decides the exit code.
func (w *Worker) Run(ctx context.Context) error {
	w.deps.Log.Info("crawl worker started",
		"region", w.deps.Region,
		"queue", w.opts.Queue,
		"job_batch", w.opts.JobBatch,
		"frontier_batch", w.opts.FrontierBatch,
		"max_attempts", w.opts.MaxAttempts,
		"claim_grace", w.opts.ClaimGrace.String())

	if remaining, declared := w.deps.KeyExpiry.Remaining(w.deps.Now()); declared && remaining > 0 {
		if remaining < KeyWarnAge {
			// The declared deadline is the one expiry the operator told us
			// about, so it is the one worth warning about with hours to spare
			// rather than at 401.
			w.deps.Log.Warn("Riot API key expires soon",
				"expires_at", w.deps.KeyExpiry.At(), "remaining", remaining.String())
		} else {
			w.deps.Log.Info("Riot API key expiry declared",
				"expires_at", w.deps.KeyExpiry.At(), "remaining", remaining.String())
		}
	}

	if err := w.recoverClaims(ctx); err != nil {
		w.deps.Log.Warn("could not reclaim abandoned claims at startup", "err", err)
	}

	var consecutiveFailures int
	for {
		if ctx.Err() != nil {
			return w.shutdown(ctx)
		}
		if err := w.keyExpiryError(); err != nil {
			// A key whose declared deadline has passed is dead: every call
			// from here is a refusal, and every refusal is a request spent on
			// nothing. Stopping is the loud half of "fail loudly on an expired
			// key" - the process exits non-zero with the reason, the supervisor
			// (a Deployment restart loop or a failed CronJob) records it, and
			// the last snapshot on the shelf stays exactly as it was rather
			// than being refreshed from an archive nobody is adding to.
			//
			// This is the one exit the idle-on-a-missing-key rule does not
			// cover, and the difference is what is known. A missing key may
			// appear at any moment in the watched file, so the loop idles for
			// it; a declared expiry is a statement that the key is gone, and
			// idling on it would hide a pipeline that needs an operator.
			w.deps.Log.Error("Riot API key expired; refusing to crawl",
				"err", err,
				"expired_at", w.deps.KeyExpiry.At(),
				"hint", "rotate the key in secret/lolstats-riot or the configured key file, then restart")
			// The archive is flushed on the way out. A stop that skipped the
			// flush would drop the payloads in the open part file, which is
			// the one thing every other exit path in this loop takes care of.
			if serr := w.shutdown(ctx); serr != nil {
				return errors.Join(err, serr)
			}
			return err
		}
		if w.keyless() {
			if err := w.Clock().Sleep(ctx, w.opts.PollInterval); err != nil {
				return w.shutdown(ctx)
			}
			continue
		}
		if paused, err := w.pause(ctx); err != nil {
			return w.shutdown(ctx)
		} else if paused {
			continue
		}

		worked, err := w.Step(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return w.shutdown(ctx)
			}
			consecutiveFailures++
			wait := w.failureBackoff(consecutiveFailures)
			w.deps.Log.Error("crawl pass failed",
				"err", err, "consecutive", consecutiveFailures, "retry_in", wait.String())
			if err := w.Clock().Sleep(ctx, wait); err != nil {
				return w.shutdown(ctx)
			}
			continue
		}
		consecutiveFailures = 0
		w.report(ctx, false)
		if worked == 0 {
			if err := w.Clock().Sleep(ctx, w.idleWait()); err != nil {
				return w.shutdown(ctx)
			}
		}
	}
}

// Step performs at most one unit of work: one batch of queue jobs, or one
// batch of frontier claims. It reports how many matches were retained, which
// is what the loop uses to decide between working and idling. It is exported
// so tests can drive the loop deterministically without goroutines or sleeps.
func (w *Worker) Step(ctx context.Context) (int, error) {
	processed, err := w.drainQueue(ctx)
	if err != nil {
		return processed, err
	}
	if processed > 0 {
		return processed, nil
	}
	return w.walkFrontier(ctx)
}

// recoverClaims hands back claims abandoned by a process that died, so that an
// unassisted restart resumes instead of stalling.
//
// A claim is held for a bounded time, not forever: when the process holding one
// is killed, the rows it had claimed stay 'claimed' and no worker will look at
// them again until something resets them. The only something was the hourly
// maintain job, whose grace is fifteen minutes, which put a crash and restart as
// much as seventy-five minutes behind: a restart recovered nothing in thirty
// seconds, maintain reclaimed nothing at the default grace, and the run only
// finished when maintain's own pass came round.
//
// The reset is the one maintain runs, under the same grace and for the same
// reason, so the two cannot drift apart. It is also the one thing here that a
// peer worker can be hurt by, which is why the grace is far longer than any
// claim a running worker can be holding (the client timeout is seconds): a live
// worker's claims are newer than the window and are left alone.
func (w *Worker) recoverClaims(ctx context.Context) error {
	if ctx.Err() != nil {
		// Already shutting down; there is nothing to recover for.
		return nil
	}
	recoverer, ok := w.deps.Store.(ClaimRecoverer)
	if !ok {
		return nil
	}
	olderThan := w.deps.Now().Add(-w.opts.ClaimGrace)
	reclaimed, err := recoverer.ResetStuckClaims(ctx, olderThan, claimRecoveryLimit)
	if err != nil {
		return fmt.Errorf("reset abandoned claims: %w", err)
	}
	w.deps.Log.Info("reclaimed claims abandoned by a previous process",
		"count", reclaimed,
		"grace", w.opts.ClaimGrace.String(),
		"before", olderThan.UTC().Format(time.RFC3339))
	return nil
}

// drainQueue claims and processes one batch of fetch_queue rows.
func (w *Worker) drainQueue(ctx context.Context) (int, error) {
	items, err := w.deps.Store.ClaimJobs(ctx, w.opts.JobBatch, w.deps.Now())
	if err != nil {
		return 0, fmt.Errorf("claim jobs: %w", err)
	}
	if len(items) == 0 {
		return 0, nil
	}
	w.deps.Log.Debug("claimed jobs", "count", len(items))

	processed := 0
	retained := make([]contract.QueueItem, 0, len(items))
	for i := range items {
		if ctx.Err() != nil {
			// Shutdown mid-batch. The rows that were never started are handed
			// back here rather than left claimed: a plain TERM used to strand
			// nineteen of a twenty-row batch, invisible to every worker until
			// the claim grace expired and maintenance (or a boot) reclaimed
			// them.
			w.releaseUnstarted(ctx, items[i:])
			break
		}
		held, err := w.processJob(ctx, items[i])
		if err != nil {
			return processed, err
		}
		if held {
			retained = append(retained, items[i])
			w.matchesRetained++
		}
		processed++
	}
	// Flush before completing, never after. A row that reads 'done' is never
	// offered again, so it may only be closed once the payload it describes is
	// a renamed part on disk: the archive is written before the database, and
	// "written" here means durable rather than buffered. One flush per batch
	// is still the cost - the parts stay open afterwards.
	//
	// A stop is exactly when this flush matters, and exactly when the context
	// refuses it: the writer rejects every call on a cancelled context, and the
	// payloads of the batch are sitting in its buffers. So the flush of an
	// interrupted batch runs on a context that outlives the stop, and only the
	// flush does - everything else here still stops.
	var flushErr error
	if ctx.Err() != nil {
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownFlushTimeout)
		flushErr = w.deps.Writer.Flush(flushCtx)
		cancel()
	} else {
		flushErr = w.deps.Writer.Flush(ctx)
	}
	if flushErr != nil {
		return processed, w.abandonRetained(ctx, retained, flushErr)
	}
	// Close what was archived even when the stop arrived mid-batch. A row that
	// is left 'claimed' is invisible until the claim grace expires, so the
	// archived half of a batch would otherwise wait out a boot's reclaim: the
	// close is done detached for the same reason the flush is.
	closed := 0
	for _, item := range retained {
		if err := w.completeDetached(ctx, item.ID); err != nil {
			return processed, fmt.Errorf("complete job %d: %w", item.ID, err)
		}
		closed++
	}
	if closed > 0 && ctx.Err() != nil {
		w.deps.Log.Info("closed the archived rows of the interrupted batch", "rows", closed)
	}
	return processed, nil
}

// completeDetached closes one row on a context that outlives the stop.
//
// Both of its callers close a row whose payload is durable in the archive: the
// batch loop closes the rows it has just flushed, and a row that was already
// archived under a previous version of the crawl is closed by the known-match
// path, whose payload the pass that inserted its `matches` row flushed.
func (w *Worker) completeDetached(ctx context.Context, id int64) error {
	if ctx.Err() == nil {
		return w.deps.Store.CompleteJob(ctx, id)
	}
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownCloseTimeout)
	defer cancel()
	return w.deps.Store.CompleteJob(detached, id)
}

// abandonRetained hands back the rows whose payloads went down with a failed
// flush. They must not be completed: the archive holds nothing for them, and a
// row marked done is never offered again, so completing them would be silent
// data loss - the exact failure the write order exists to prevent.
//
// The attempt budget is consulted, for the same reason it is on the write path:
// a flush that keeps failing is a local disk condition that will not fix itself,
// and before it was bounded here too, an unwritable archive produced thousands
// of requeues in minutes instead of retiring the rows.
func (w *Worker) abandonRetained(ctx context.Context, retained []contract.QueueItem, cause error) error {
	if len(retained) == 0 {
		return fmt.Errorf("flush archive: %w", cause)
	}
	if ctx.Err() != nil {
		// Shutdown: the context cannot write to the queue, and the rows are
		// still claimed, so a boot's claim reset (or maintain's) releases them.
		// They must not be released here: the payloads may still be sitting in
		// the open part that shutdown is about to flush, and a row that is
		// offered again while its payload is durable is how a match ends up
		// archived twice.
		return fmt.Errorf("flush archive after %d retained rows: %w", len(retained), cause)
	}
	short := shortCause(cause)
	for _, item := range retained {
		if err := w.handleArchiveFailure(ctx, item, cause, archiveFlushCause+short); err != nil {
			w.deps.Log.Error("could not release a row after a failed archive flush",
				"match_id", item.MatchID, "job_id", item.ID, "err", err)
		}
	}
	return fmt.Errorf("flush archive after %d retained rows: %w", len(retained), cause)
}

// processJob fetches one match, retains it, records it, and widens the crawl
// through its participants. Every exit path either hands the row back to the
// queue or reports that it is ready to be completed - and completion is the
// caller's job, because the row may only be closed once the archive part
// holding the payload has been flushed. A crash can therefore lose at most a
// claim, never a payload.
func (w *Worker) processJob(ctx context.Context, item contract.QueueItem) (bool, error) {
	jobCtx, cancel := context.WithTimeout(ctx, w.opts.JobTimeout)
	defer cancel()

	started := w.deps.Now()

	// A match the control plane already holds is already in the archive, and
	// the archive has no key: walking it again appends a second copy of a
	// payload that is already stored, which the upsert's ON CONFLICT DO NOTHING
	// cannot collapse because it only governs the `matches` row. The row is
	// closed instead, because the work it describes is finished.
	//
	// A `matches` row implies the payload reached a renamed part. The insert
	// happens inside the batch and the flush at the end of it, so the row can
	// exist while its part is still buffered - but only for a row of the same
	// batch, and the batch closes that row only after the flush and hands every
	// row of it back if the flush fails. So a row closed here is never closed
	// ahead of the payload it describes.
	if w.alreadyArchived(ctx, item) {
		if err := w.completeDetached(ctx, item.ID); err != nil {
			return false, fmt.Errorf("complete known match %s: %w", item.MatchID, err)
		}
		w.deps.Log.Debug("match already archived; row closed without a fetch",
			"match_id", item.MatchID, "job_id", item.ID)
		return false, nil
	}

	dto, _, err := w.deps.Fetcher.MatchWithPayload(jobCtx, item.MatchID)
	if err != nil {
		return false, w.handleFetchFailure(ctx, item, err)
	}

	meta := MatchMetaFromDTO(dto, item.MatchID, w.deps.Region, w.deps.Now())
	if err := w.deps.Writer.WriteMatch(ctx, dto, meta); err != nil {
		// An archive failure is local (disk, permissions) and the payload is
		// still fetchable, so the row goes back to the queue: dropping it
		// would lose data the archive exists to keep. It goes back under the
		// attempt ceiling, because "the archive is unwritable" is not a
		// transient condition the crawl can retry its way out of.
		return false, w.handleArchiveFailure(ctx, item, err, archiveCause)
	}
	record := MatchRecordFromMeta(meta, raw.MatchPartitionURI(writerRoot(w.deps.Writer), meta))
	if _, err := w.deps.Store.UpsertMatch(ctx, record); err != nil {
		// The payload is durable, so the row is left claimed rather than
		// closed: a row that reads 'done' is never offered again, and closing
		// it here would leave an archived match with no `matches` row - the
		// record the next walk consults before it decides to fetch. The retry
		// waits for the control plane to come back and pays for it with one
		// re-fetch, which appends a second archive record: the archive has no
		// key, so only the aggregate's de-duplication by match id keeps that
		// copy out of a published number.
		return false, fmt.Errorf("upsert match %s: %w", item.MatchID, err)
	}
	w.widen(ctx, dto, meta)
	w.deps.Log.Debug("match retained",
		"match_id", item.MatchID,
		"queue_id", meta.QueueID,
		"patch", meta.Patch,
		"elapsed", w.deps.Now().Sub(started).String())
	return true, nil
}

// alreadyArchived reports whether the control plane already holds this match.
//
// `matches` is the crawl's own record of what it has finished, and it is only
// inserted after the payload has been written to the archive, so a row in it
// means the payload is stored. The archive itself cannot be asked: it is
// append-only parquet with no key, which is why the duplicate this check
// prevents had nothing to collapse it.
//
// The interface is optional, like the other store surfaces the loop discovers
// by assertion, so a test or a caller can drive the worker with a store that
// does not implement it. A store that cannot answer is treated as "no": the
// question decides whether to skip a fetch, and answering it wrongly in that
// direction costs one duplicate archive record, while treating a failure as
// "yes" would drop the fetch of a match that may not be stored at all.
func (w *Worker) alreadyArchived(ctx context.Context, item contract.QueueItem) bool {
	known, ok := w.deps.Store.(KnownMatchChecker)
	if !ok {
		return false
	}
	exists, err := known.MatchExists(ctx, item.MatchID)
	if err != nil {
		w.deps.Log.Warn("could not check whether the match is already archived; fetching it",
			"match_id", item.MatchID, "job_id", item.ID, "err", err)
		return false
	}
	return exists
}

// handleFetchFailure decides between retrying and giving up on a queue row.
func (w *Worker) handleFetchFailure(ctx context.Context, item contract.QueueItem, err error) error {
	class := classify(err)
	cause := shortCause(err)

	if errors.Is(err, riot.ErrCircuitOpen) {
		// The client refused to make the call: the key was refused or the API
		// is closed to us. That is a statement about the crawl, not about this
		// match, so the row goes back without paying for it - otherwise an
		// outage longer than a row's attempt budget retires the whole backlog
		// one row at a time.
		return w.release(ctx, item, cause)
	}

	switch class {
	case failureGone, failureBadRequest:
		// Riot says this key cannot ever work: a match that was purged, or a
		// request we are building wrong. Retrying spends budget to learn the
		// same thing.
		w.deps.Log.Warn("dropping unfetchable match", "match_id", item.MatchID, "cause", cause)
		return w.deps.Store.DeadLetterJob(ctx, item.ID, cause)
	case failureKeyRefused:
		// The refusals that tripped the breaker are charged to no one row.
		return w.release(ctx, item, cause)
	case failureCancelled:
		if ctx.Err() != nil {
			// Shutdown. Hand the row back immediately so the next process
			// does not wait for the claim window; the attempt is already
			// counted.
			return w.requeueDetached(ctx, item, cause)
		}
		// A deadline that is not the run's own - the client's per-call timeout,
		// or the job's - is not a shutdown and must not be reported as one.
		// The client classifies its own timeouts before they reach here; this
		// is the backstop, and it schedules a real retry rather than handing
		// the row back with no delay at all.
	}

	if ctx.Err() != nil {
		// The run is stopping. An attempt that was cut short by the shutdown
		// has not really been spent, so the row goes back on the queue rather
		// than consuming the last attempt on a dead letter: the next process
		// is the one that gets to decide whether it is hopeless.
		return w.requeueDetached(ctx, item, cause)
	}

	if item.Attempts >= w.opts.MaxAttempts {
		w.deps.Log.Error("match dead-lettered",
			"match_id", item.MatchID, "attempts", item.Attempts, "cause", cause)
		return w.deps.Store.DeadLetterJob(ctx, item.ID, cause)
	}
	return w.requeueAfter(ctx, item, err, cause, w.rateLimitDelay(w.retryDelay(item.Attempts)))
}

// handleArchiveFailure decides between retrying and retiring a row whose payload
// could not be stored, under the same attempt ceiling a failed fetch is held to.
//
// The archive is the first half of the write order, so a row that fails here has
// no `matches` row and nothing durable on disk: retiring it loses nothing, since
// the payload is still fetchable and the row is still visible in the queue. What
// must not happen is what used to happen instead. `requeue` consults no ceiling,
// so an unwritable archive - a read-only mount, a full volume, a wrong fsGroup
// on a PVC - became an unbounded hot loop that re-fetched payloads it could not
// store: with -max-attempts 3 in force, rows reached 34 and 35 attempts and the
// queue took 6860 requeues in two minutes. The ceiling that bounds a failing
// fetch bounds this too; reaching it retires the row as a dead letter that
// carries the archive error, which an operator can read, fix the volume for, and
// replay with `maintain -replay-dead-letters`.
func (w *Worker) handleArchiveFailure(ctx context.Context, item contract.QueueItem, err error, cause string) error {
	if item.Attempts >= w.opts.MaxAttempts {
		w.deps.Log.Error("match dead-lettered by a failed archive write",
			"match_id", item.MatchID, "job_id", item.ID, "attempts", item.Attempts, "cause", cause)
		return w.deps.Store.DeadLetterJob(ctx, item.ID, cause)
	}
	return w.requeue(ctx, item, err, cause)
}

// rateLimitDelay stretches a retry delay to clear the suspension the limiter is
// holding.
//
// Without it, a row released because of a 429 comes back at RetryBase - seconds
// - while the penalty runs for minutes: the row is re-fetched into a limiter
// that will not let the call through, spends an attempt, and is re-released. The
// delay clears the penalty instead, which is the only schedule that describes
// when the call can succeed.
//
// It applies to every retry, not only to a row Riot answered 429 for. A call
// that ran out of its own deadline while the limiter was holding a suspension
// failed for the same reason, and it must not be offered again inside it either.
func (w *Worker) rateLimitDelay(base time.Duration) time.Duration {
	if w.pacer == nil {
		return base
	}
	wait, blocked := w.pacer.Blocked()
	if !blocked || wait <= 0 {
		return base
	}
	if base <= 0 {
		return wait
	}
	// Jitter on top, so the rows that were all blocked behind one penalty do
	// not become due at the same instant.
	return wait + time.Duration(w.rng.Int63n(int64(base)))
}

// jobReleaser returns a claim without charging the row for it. It is additive
// on *store.Store and discovered by type assertion, the way the pacer and the
// maintenance surface are, because contract.Store is frozen and nothing in the
// crawler's normal vocabulary needs it.
type jobReleaser interface {
	ReleaseJob(ctx context.Context, id int64, notBefore time.Time, cause string) error
}

// release hands a row back for a failure that is not this row's fault. The
// attempt is refunded when the store can do it, so that a global condition
// cannot exhaust a row's budget; without the refund the row is retried exactly
// as before, which is the pre-existing behaviour rather than a new hazard.
func (w *Worker) release(ctx context.Context, item contract.QueueItem, cause string) error {
	notBefore := w.deps.Now().Add(w.retryDelay(item.Attempts))
	releaser, ok := w.deps.Store.(jobReleaser)
	if !ok {
		w.deps.Log.Warn("store cannot refund an attempt; the row is retried as if it had failed",
			"match_id", item.MatchID, "cause", cause)
		return w.requeue(ctx, item, nil, cause)
	}
	if err := releaser.ReleaseJob(ctx, item.ID, notBefore, cause); err != nil {
		return fmt.Errorf("release job %d after %q: %w", item.ID, cause, err)
	}
	w.deps.Log.Warn("match released without spending an attempt",
		"match_id", item.MatchID,
		"attempts", item.Attempts,
		"cause", cause,
		"not_before", notBefore.UTC().Format(time.RFC3339))
	return nil
}

// requeue puts a row back with a jittered exponential delay. The retry is
// scheduled rather than immediate so that a Riot outage or a disk problem does
// not turn into a hot loop across every worker.
func (w *Worker) requeue(ctx context.Context, item contract.QueueItem, err error, cause string) error {
	return w.requeueAfter(ctx, item, err, cause, w.retryDelay(item.Attempts))
}

// requeueAfter is requeue with the delay chosen by the caller.
func (w *Worker) requeueAfter(ctx context.Context, item contract.QueueItem, err error, cause string, delay time.Duration) error {
	notBefore := w.deps.Now().Add(delay)
	if ctx.Err() != nil {
		// The context used to reach the store is already gone; the retry is
		// still owed to the queue, and the attempt it costs has already been
		// spent.
		return w.retryDetached(ctx, item, cause)
	}
	if retryErr := w.deps.Store.RetryJob(ctx, item.ID, notBefore, cause); retryErr != nil {
		return fmt.Errorf("retry job %d after %q: %w", item.ID, cause, retryErr)
	}
	w.deps.Log.Warn("match requeued",
		"match_id", item.MatchID,
		"attempts", item.Attempts,
		"cause", cause,
		"not_before", notBefore.UTC().Format(time.RFC3339),
		"err", err)
	return nil
}

// requeueDetached is requeue for a shutdown path, where the caller's context
// is already cancelled but the bookkeeping must still happen: dropping the row
// back to 'claimed' would hide it for the whole claim window.
func (w *Worker) requeueDetached(ctx context.Context, item contract.QueueItem, cause string) error {
	return w.retryDetached(ctx, item, cause)
}

// retryDetached is the shutdown-safe half of requeue: the attempt is already
// spent, so the row goes back to 'pending' even though the context that carried
// it is cancelled. Waiting for a claim reclaim instead would hide the row for
// the whole grace period.
func (w *Worker) retryDetached(ctx context.Context, item contract.QueueItem, cause string) error {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	notBefore := w.deps.Now()
	if err := w.deps.Store.RetryJob(detached, item.ID, notBefore, cause); err != nil {
		w.deps.Log.Error("could not release job during shutdown",
			"match_id", item.MatchID, "job_id", item.ID, "err", err)
		return nil
	}
	w.deps.Log.Info("job released before shutdown", "match_id", item.MatchID, "job_id", item.ID)
	return nil
}

// releaseUnstarted hands back rows that were claimed but never fetched.
//
// On SIGTERM the worker stops claiming work while it drains, which leaves the
// unstarted tail of the batch claimed by a process that is about to exit: the
// row is invisible to every worker until the claim grace expires, which is what
// made a graceful TERM strand nineteen of a twenty-row batch. These rows were
// never fetched, so nothing about them is in flight and nothing has to be
// preserved - the only cost of giving them back is the fetch the next worker
// does again.
func (w *Worker) releaseUnstarted(ctx context.Context, rows []contract.QueueItem) {
	if len(rows) == 0 {
		return
	}
	released := 0
	for _, item := range rows {
		// Detached, because this runs on the shutdown path: the context that
		// carried the batch is cancelled by the time the tail is released, and
		// a refund that is refused for that reason leaves the row claimed by a
		// process that is exiting - the very strand this exists to prevent.
		detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := w.release(detached, item, "")
		cancel()
		if err != nil {
			w.deps.Log.Error("could not release an unstarted row at shutdown",
				"match_id", item.MatchID, "job_id", item.ID, "err", err)
			continue
		}
		released++
	}
	w.deps.Log.Info("released unstarted rows at shutdown",
		"rows", len(rows), "released", released)
}

// walkFrontier claims puiuds whose history is due and turns their recent
// matches into queue rows. This is the breath of the walk; processJob is the
// breadth.
func (w *Worker) walkFrontier(ctx context.Context) (int, error) {
	entries, err := w.deps.Store.ClaimFrontier(ctx, w.opts.FrontierBatch, w.deps.Now())
	if err != nil {
		return 0, fmt.Errorf("claim frontier: %w", err)
	}
	if len(entries) == 0 {
		return 0, nil
	}
	w.deps.Log.Debug("claimed frontier entries", "count", len(entries))

	touched := 0
	for i := range entries {
		if ctx.Err() != nil {
			break
		}
		entry := entries[i]
		ids, err := w.deps.Fetcher.MatchIDs(ctx, riot.MatchListQuery{
			PUUID: entry.PUUID,
			Count: w.opts.HistoryCount,
			Queue: w.opts.Queue,
		})
		if err != nil {
			if classify(err) == failureGone {
				if deadErr := w.deps.Store.MarkFrontierDead(ctx, entry.PUUID, shortCause(err)); deadErr != nil {
					return touched, fmt.Errorf("mark frontier dead: %w", deadErr)
				}
				w.deps.Log.Warn("frontier entry marked dead", "puuid", entry.PUUID, "cause", shortCause(err))
				continue
			}
			// A transient failure leaves the claim to expire; the player is
			// not penalised for Riot being unavailable.
			return touched, fmt.Errorf("match ids for %s: %w", entry.PUUID, err)
		}

		items := make([]contract.QueueItem, 0, len(ids))
		for _, id := range ids {
			items = append(items, contract.QueueItem{MatchID: id, Priority: entry.Priority})
		}
		if len(items) > 0 {
			// Duplicates are expected and free: EnqueueMatches collapses them
			// on match_id, which is what makes re-crawling a player's history
			// a no-op rather than a second copy of every match.
			added, err := w.deps.Store.EnqueueMatches(ctx, items)
			if err != nil {
				return touched, fmt.Errorf("enqueue matches for %s: %w", entry.PUUID, err)
			}
			w.deps.Log.Debug("frontier entry walked",
				"puuid", entry.PUUID, "matches", len(items), "new", added)
		}
		if err := w.deps.Store.MarkFrontierFetched(ctx, entry.PUUID, w.deps.Now(), len(ids) == 0); err != nil {
			return touched, fmt.Errorf("mark frontier fetched: %w", err)
		}
		touched++
	}
	return touched, nil
}

// widen adds the participants of a retained match to the frontier. It is the
// mechanism that lets a single seed spread: the walk gains ten players per
// match, deduplicated by puuid, and each of those players contributes their own
// history on a later pass.
func (w *Worker) widen(ctx context.Context, dto riot.MatchDTO, meta contract.MatchMeta) {
	puuids := ParticipantPUUIDs(dto)
	if len(puuids) == 0 {
		w.deps.Log.Warn("match has no participants; crawl cannot widen", "match_id", meta.MatchID)
		return
	}
	entries := make([]contract.FrontierEntry, 0, len(puuids))
	for _, puuid := range puuids {
		entries = append(entries, contract.FrontierEntry{
			PUUID:        puuid,
			Region:       w.deps.Region,
			LastSeenAt:   meta.FetchedAt,
			Priority:     PriorityParticipant,
			SeedTier:     "",
			SeedDivision: "",
		})
	}
	added, err := w.deps.Store.UpsertFrontier(ctx, entries)
	if err != nil {
		// Widening is best effort by design: the match is retained, and the
		// next pass over this player discovers the same participants again.
		w.deps.Log.Warn("widening frontier failed", "match_id", meta.MatchID, "err", err)
		return
	}
	w.deps.Log.Debug("frontier widened", "match_id", meta.MatchID, "participants", len(entries), "new", added)
}

// report publishes the pipeline metrics and one heartbeat line. Staleness is
// the alert that matters: if nothing has been fetched for longer than a poll
// interval, the operator wants to know before the frontier silently empties.
//
// The heartbeat exists because a throttled crawler and a stopped one used to
// write the same thing to this log - nothing, or a warning about a rate limit -
// and a reader could not tell them apart. The measured case is a development
// key ridden at its ceiling: Riot answers about one request in fifty with a
// 429, the client absorbs it on the next attempt, and the log for a full hour
// was 72 lines of WARN and no INFO at all while the crawl was in fact fetching
// forty-four matches a minute. One line per interval carrying the numbers that
// freeze in a stall makes the healthy case legible and turns the stalled case
// into a number that stops moving, in the log as well as in Prometheus.
func (w *Worker) report(ctx context.Context, force bool) {
	if !force && w.deps.Now().Sub(w.lastReport) < w.opts.ReportInterval {
		return
	}
	w.lastReport = w.deps.Now()

	attrs := []any{"matches_retained", w.matchesRetained}

	if w.pacer != nil {
		attrs = append(attrs,
			"advertised", w.pacer.Advertised(),
			"effective_rps", w.pacer.EffectiveRate())
	}
	if k, ok := w.deps.Fetcher.(KeyAger); ok {
		if age, known := k.Age(); known {
			w.deps.Metrics.SetRiotKeyAge(age.Seconds())
			if age > KeyWarnAge {
				w.deps.Log.Warn("Riot API key is old; rotate it before it expires", "age", age.String())
			}
		}
	}

	var stale bool
	if w.reporter != nil {
		size, err := w.reporter.FrontierSize(ctx)
		if err != nil {
			w.deps.Log.Warn("frontier size unavailable", "err", err)
		} else {
			w.deps.Metrics.SetFrontierSize(size)
			attrs = append(attrs, "frontier", size)
		}
		if newest, ok, err := w.reporter.NewestFetchedAt(ctx); err != nil {
			w.deps.Log.Warn("pipeline staleness unavailable", "err", err)
		} else if ok {
			age := w.deps.Now().Sub(newest)
			if age < 0 {
				age = 0
			}
			w.deps.Metrics.SetPipelineStaleness(obs.StageCrawl, age.Seconds())
			attrs = append(attrs, "staleness", age.Round(time.Second).String())
			// Past the age the staleness alert fires at, the heartbeat is
			// itself the loud failure: a crawl that has stopped fetching is
			// what the pipeline is required to report rather than re-serve
			// stale numbers quietly.
			stale = age >= StaleWarnAge
		}
	}
	if stale {
		w.deps.Log.Warn("crawl is not fetching: the newest match is older than the staleness alert holds for",
			attrs...)
		return
	}
	w.deps.Log.Info("crawl pipeline status", attrs...)
}

// shutdown flushes the archive so that a cancelled run leaves the part files
// it claimed to have written. It is safe to call with a cancelled context.
func (w *Worker) shutdown(ctx context.Context) error {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	w.report(detached, true)
	if err := w.deps.Writer.Flush(detached); err != nil {
		w.deps.Log.Error("final archive flush failed; payloads in the open part may be lost", "err", err)
		return err
	}
	w.deps.Log.Info("crawl worker stopped")
	return nil
}

// keyExpiryError reports the declared key deadline once it has passed. The zero
// value declares nothing and always returns nil.
func (w *Worker) keyExpiryError() error {
	return w.deps.KeyExpiry.Check(w.deps.Now())
}

// keyless reports whether the crawler should idle because there is no key.
// Idling rather than exiting is deliberate: the key may be written to the
// watched file later, and a supervisor restarting a keyless worker would be
// noise, not a fix.
func (w *Worker) keyless() bool {
	if w.keys == nil {
		return false
	}
	if _, ok := w.keys.Key(); ok {
		// A key that came back (or was rotated in) re-arms the notice, so a
		// key that expires later is reported again rather than staying quiet.
		w.warnedEmpty = false
		return false
	}
	if !w.warnedEmpty {
		w.warnedEmpty = true
		// The process owner logs the operator-facing warning; this one is the
		// loop's own note, at info level so that a keyless run has exactly one
		// warning in its log.
		w.deps.Log.Info("crawler idle: no Riot API key yet",
			"key_env", "LOLSTATS_RIOT_API_KEY",
			"key_file_env", "LOLSTATS_RIOT_API_KEY_FILE")
	}
	return true
}

// pause honours an adaptive suspension (429 Retry-After or a tripped breaker)
// before claiming a batch that would fail row by row.
func (w *Worker) pause(ctx context.Context) (bool, error) {
	if w.pacer == nil {
		return false, nil
	}
	wait, blocked := w.pacer.Blocked()
	if !blocked || wait <= 0 {
		return false, nil
	}
	if wait > maxRateLimitPause {
		wait = maxRateLimitPause
	}
	w.deps.Log.Debug("pausing before claiming work", "wait", wait.String())
	if err := w.Clock().Sleep(ctx, wait); err != nil {
		return true, err
	}
	return true, nil
}

// idleWait is the poll interval with jitter. Jitter matters when several
// workers share a database: unjittered polls arrive together and claim the same
// rows in a thundering herd, which SKIP LOCKED survives but at a needless cost.
func (w *Worker) idleWait() time.Duration {
	half := w.opts.PollInterval / 2
	return half + time.Duration(w.rng.Int63n(int64(half)+1))
}

// retryDelay is exponential in the attempt number with equal jitter, capped by
// RetryMax. It mirrors the client's HTTP backoff so that a Riot outage is not
// retried aggressively at the row level once the request level has given up.
func (w *Worker) retryDelay(attempts int) time.Duration {
	delay := w.opts.RetryBase
	for i := 1; i < attempts && delay < w.opts.RetryMax; i++ {
		delay *= 2
	}
	if delay > w.opts.RetryMax {
		delay = w.opts.RetryMax
	}
	half := delay / 2
	return half + time.Duration(w.rng.Int63n(int64(half)+1))
}

// failureBackoff grows the pause after consecutive loop-level failures (a
// database that is down, a disk that is full) without ever giving up.
func (w *Worker) failureBackoff(consecutive int) time.Duration {
	delay := w.opts.RetryBase
	for i := 1; i < consecutive && delay < w.opts.RetryMax; i++ {
		delay *= 2
	}
	if delay > w.opts.RetryMax {
		delay = w.opts.RetryMax
	}
	return delay
}

// Clock returns the injected clock; the loop never calls time.Sleep directly so
// tests can run the whole state machine in fake time.
func (w *Worker) Clock() riot.Clock { return w.deps.Clock }

// failureClass is what the worker decides to do about a failed fetch.
type failureClass int

const (
	// failureTransient: worth another attempt (5xx, transport, breaker open,
	// no key, a local archive problem).
	failureTransient failureClass = iota
	// failureRateLimited: Riot's budget, not ours. Worth another attempt, but
	// with a longer delay.
	failureRateLimited
	// failureKeyRefused: Riot refused the key (401/403). It is a fact about
	// the crawl, not about the match, so the row goes back without spending an
	// attempt - otherwise an outage longer than a row's budget retires the
	// backlog one row per probe, permanently.
	failureKeyRefused
	// failureGone: Riot cannot serve this key (404/410). Never worth retrying.
	failureGone
	// failureBadRequest: our request is wrong (400/422) and will stay wrong.
	failureBadRequest
	// failureCancelled: shutdown, not a verdict on the row.
	failureCancelled
)

// classify maps a client error to the worker's reaction. It reads status codes
// rather than error strings, and anything it does not recognise is transient,
// because dropping work is worse than retrying it.
func classify(err error) failureClass {
	if err == nil {
		return failureTransient
	}
	var limited *riot.RateLimitedError
	if errors.As(err, &limited) {
		// Checked before the deadline, and before anything else: the client
		// reports a 429 by its Retry-After, so that a wait longer than the
		// call's own deadline is not mistaken for a shutdown. The check is by
		// type, not by flag: a bare RateLimitedError classifies the same way it
		// always did.
		return failureRateLimited
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return failureCancelled
	}
	var status *riot.StatusError
	if errors.As(err, &status) {
		switch status.Status {
		case http.StatusNotFound, http.StatusGone:
			return failureGone
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			return failureBadRequest
		case http.StatusUnauthorized, http.StatusForbidden:
			// A key problem is not a match problem. The client's breaker
			// notices the run of refusals and stops calling; the row must not
			// be charged for the calls it took to notice.
			return failureKeyRefused
		case http.StatusTooManyRequests:
			return failureRateLimited
		default:
			return failureTransient
		}
	}
	return failureTransient
}

// shortCause is the operator-facing one-liner stored on the row. It is bounded
// so that a pathological body cannot bloat the queue table.
func shortCause(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

func defaultInt(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}

func defaultDuration(v, fallback time.Duration) time.Duration {
	if v <= 0 {
		return fallback
	}
	return v
}
