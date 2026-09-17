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
	DefaultJobBatch       = 20
	DefaultFrontierBatch  = 20
	DefaultHistoryCount   = 20
	DefaultMaxAttempts    = 8
	DefaultJobTimeout     = 25 * time.Second
	DefaultRetryBase      = 30 * time.Second
	DefaultRetryMax       = 30 * time.Minute
	DefaultPollInterval   = 15 * time.Second
	DefaultReportInterval = 60 * time.Second
	// KeyWarnAge is when a key is old enough that an operator should expect
	// the next rotation. A Riot development key expires after 24 hours.
	KeyWarnAge = 12 * time.Hour
	// maxRateLimitPause caps a single adaptive pause. Riot's Retry-After is
	// honoured in full by the client's limiter; this cap only bounds how long
	// the worker sleeps in one go before re-checking its context.
	maxRateLimitPause = time.Minute
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

// The keyless idle path is reached through a type assertion, so a fetcher that
// quietly loses these methods would turn "idle" into "claim rows, fail each
// one, burn their attempts" without any test failing. Asserting the wiring at
// compile time is what keeps a keyless worker honest.
var (
	_ KeySource = (*riot.Client)(nil)
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
		"max_attempts", w.opts.MaxAttempts)

	var consecutiveFailures int
	for {
		if ctx.Err() != nil {
			return w.shutdown(ctx)
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
	for i := range items {
		if ctx.Err() != nil {
			// Shutdown mid-batch: the remaining rows stay claimed and are
			// released by maintain (or by this process's shutdown path).
			break
		}
		if err := w.processJob(ctx, items[i]); err != nil {
			return processed, err
		}
		processed++
	}
	// Flush after the batch, not per row: a flush is a part-file boundary, and
	// one part per batch keeps the archive readable without turning every
	// fetch into a file operation.
	if err := w.deps.Writer.Flush(ctx); err != nil {
		return processed, fmt.Errorf("flush archive: %w", err)
	}
	return processed, nil
}

// processJob fetches one match, retains it, records it, and widens the crawl
// through its participants. Every exit path either completes the row or hands
// it back to the queue, so a crash can lose at most a claim, never a payload.
func (w *Worker) processJob(ctx context.Context, item contract.QueueItem) error {
	jobCtx, cancel := context.WithTimeout(ctx, w.opts.JobTimeout)
	defer cancel()

	started := w.deps.Now()
	dto, _, err := w.deps.Fetcher.MatchWithPayload(jobCtx, item.MatchID)
	if err != nil {
		return w.handleFetchFailure(ctx, item, err)
	}

	meta := MatchMetaFromDTO(dto, item.MatchID, w.deps.Region, w.deps.Now())
	if err := w.deps.Writer.WriteMatch(ctx, dto, meta); err != nil {
		// An archive failure is local (disk, permissions) and the payload is
		// still fetchable, so the row goes back to the queue: dropping it
		// would lose data the archive exists to keep.
		return w.requeue(ctx, item, err, "archive")
	}
	record := MatchRecordFromMeta(meta, raw.MatchPartitionURI(writerRoot(w.deps.Writer), meta))
	if _, err := w.deps.Store.UpsertMatch(ctx, record); err != nil {
		// The payload is already durable; leaving the row claimed means the
		// retry re-fetches and the INSERT ... DO NOTHING collapses it to one
		// row, which is the idempotency the design rests on.
		return fmt.Errorf("upsert match %s: %w", item.MatchID, err)
	}
	if err := w.deps.Store.CompleteJob(ctx, item.ID); err != nil {
		return fmt.Errorf("complete job %d: %w", item.ID, err)
	}
	w.widen(ctx, dto, meta)
	w.deps.Log.Debug("match retained",
		"match_id", item.MatchID,
		"queue_id", meta.QueueID,
		"patch", meta.Patch,
		"elapsed", w.deps.Now().Sub(started).String())
	return nil
}

// handleFetchFailure decides between retrying and giving up on a queue row.
func (w *Worker) handleFetchFailure(ctx context.Context, item contract.QueueItem, err error) error {
	class := classify(err)
	cause := shortCause(err)

	switch class {
	case failureGone, failureBadRequest:
		// Riot says this key cannot ever work: a match that was purged, or a
		// request we are building wrong. Retrying spends budget to learn the
		// same thing.
		w.deps.Log.Warn("dropping unfetchable match", "match_id", item.MatchID, "cause", cause)
		return w.deps.Store.DeadLetterJob(ctx, item.ID, cause)
	case failureCancelled:
		// Shutdown. Hand the row back immediately so the next process does
		// not wait for the claim window; the attempt is already counted.
		return w.requeueDetached(ctx, item, cause)
	}

	if item.Attempts >= w.opts.MaxAttempts {
		w.deps.Log.Error("match dead-lettered",
			"match_id", item.MatchID, "attempts", item.Attempts, "cause", cause)
		return w.deps.Store.DeadLetterJob(ctx, item.ID, cause)
	}
	return w.requeue(ctx, item, err, cause)
}

// requeue puts a row back with a jittered exponential delay. The retry is
// scheduled rather than immediate so that a Riot outage or a disk problem does
// not turn into a hot loop across every worker.
func (w *Worker) requeue(ctx context.Context, item contract.QueueItem, err error, cause string) error {
	delay := w.retryDelay(item.Attempts)
	notBefore := w.deps.Now().Add(delay)
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

// report publishes the pipeline metrics. Staleness is the alert that matters:
// if nothing has been fetched for longer than a poll interval, the operator
// wants to know before the frontier silently empties.
func (w *Worker) report(ctx context.Context, force bool) {
	if !force && w.deps.Now().Sub(w.lastReport) < w.opts.ReportInterval {
		return
	}
	w.lastReport = w.deps.Now()

	if w.pacer != nil {
		w.deps.Log.Debug("limiter state",
			"advertised", w.pacer.Advertised(),
			"effective_rps", w.pacer.EffectiveRate())
	}
	if k, ok := w.deps.Fetcher.(interface {
		Age() (time.Duration, bool)
	}); ok {
		if age, known := k.Age(); known {
			w.deps.Metrics.SetRiotKeyAge(age.Seconds())
			if age > KeyWarnAge {
				w.deps.Log.Warn("Riot API key is old; rotate it before it expires", "age", age.String())
			}
		}
	}
	if w.reporter == nil {
		return
	}
	size, err := w.reporter.FrontierSize(ctx)
	if err != nil {
		w.deps.Log.Warn("frontier size unavailable", "err", err)
	} else {
		w.deps.Metrics.SetFrontierSize(size)
	}
	if newest, ok, err := w.reporter.NewestFetchedAt(ctx); err != nil {
		w.deps.Log.Warn("pipeline staleness unavailable", "err", err)
	} else if ok {
		age := w.deps.Now().Sub(newest)
		if age < 0 {
			age = 0
		}
		w.deps.Metrics.SetPipelineStaleness(obs.StageCrawl, age.Seconds())
	}
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
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return failureCancelled
	}
	var limited *riot.RateLimitedError
	if errors.As(err, &limited) {
		return failureRateLimited
	}
	var status *riot.StatusError
	if errors.As(err, &status) {
		switch status.Status {
		case http.StatusNotFound, http.StatusGone:
			return failureGone
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			return failureBadRequest
		case http.StatusUnauthorized, http.StatusForbidden:
			// A key problem is not a match problem: retry, and let the
			// breaker's cooldown keep the retries apart.
			return failureTransient
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
