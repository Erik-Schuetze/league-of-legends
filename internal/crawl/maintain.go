package crawl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
)

// Maintenance defaults. The numbers encode the operating assumption: a worker
// that has not reported a claim in fifteen minutes is dead (the client timeout
// is seconds, not minutes), and a player who has produced nothing new five
// crawls in a row is unlikely to be worth polluting the queue for.
const (
	DefaultClaimGrace         = 15 * time.Minute
	DefaultFrontierRetention  = 30 * 24 * time.Hour
	DefaultMaxConsecutiveMiss = 5
	DefaultMaintainLimit      = 1000
)

// QueueInspector is the read-only queue surface maintenance reports. It is
// satisfied by *store.Store without this package naming it.
type QueueInspector interface {
	QueueDepths(ctx context.Context) (map[contract.JobStatus]int, error)
	QueueOldest(ctx context.Context) (time.Time, bool, error)
}

// MaintenanceStore is the mutating surface maintenance needs beyond the frozen
// contract. Both methods are additive on *store.Store: reclaiming an abandoned
// claim and re-ranking the frontier are recovery operations, not crawl
// progress, so they have no place in the interface the crawler moves work
// through.
type MaintenanceStore interface {
	ResetStuckClaims(ctx context.Context, olderThan time.Time, limit int) (int, error)
	RecomputeFrontierPriority(ctx context.Context) (int, error)
}

// FrontierInspector is the read-only frontier surface maintenance reports.
type FrontierInspector interface {
	FrontierSize(ctx context.Context) (int, error)
	DeadFrontierSize(ctx context.Context) (int, error)
}

// MaintainOptions is one maintenance pass.
type MaintainOptions struct {
	Deps

	// ClaimGrace is how long a claim may be held before it is considered
	// abandoned by a dead worker.
	ClaimGrace time.Duration
	// FrontierRetention is how long a dead or fruitless frontier entry is kept
	// before it is pruned.
	FrontierRetention time.Duration
	// MaxConsecutiveEmpty is the fruitless-crawl threshold for pruning.
	MaxConsecutiveEmpty int
	// Limit bounds each mutating statement.
	Limit int
	// DryRun reports what would change without changing it. The 24h key makes
	// this the difference between "show me" and "spend the budget".
	DryRun bool
}

// MaintainResult is the pass summary.
type MaintainResult struct {
	Pruned           int
	ReclaimedClaims  int
	Reprioritised    int
	FrontierSize     int
	DeadFrontierSize int
	QueueDepths      map[contract.JobStatus]int
	QueueOldest      time.Time
	HasQueueOldest   bool
	NewestFetchedAt  time.Time
	HasFetched       bool
	DryRun           bool
}

// Maintain performs the three jobs that keep a long-running crawl honest:
// reclaim claims abandoned by a dead worker, prune the frontier of players who
// are gone or fruitless, and recompute frontier priority so that the walk
// spends its budget where it is still producing.
//
// It never touches the archive. Everything it does is recoverable: a claim
// returned to the queue is retried, a pruned player is rediscovered through the
// matches of others, and a priority is recomputed from scratch on the next pass.
func Maintain(ctx context.Context, opts MaintainOptions) (MaintainResult, error) {
	opts.normalize()
	if opts.Store == nil {
		return MaintainResult{}, errors.New("crawl: maintain needs a store")
	}
	opts.ClaimGrace = defaultDuration(opts.ClaimGrace, DefaultClaimGrace)
	opts.FrontierRetention = defaultDuration(opts.FrontierRetention, DefaultFrontierRetention)
	opts.MaxConsecutiveEmpty = defaultInt(opts.MaxConsecutiveEmpty, DefaultMaxConsecutiveMiss)
	opts.Limit = defaultInt(opts.Limit, DefaultMaintainLimit)

	now := opts.Now()
	result := MaintainResult{DryRun: opts.DryRun}
	log := opts.Log

	if !opts.DryRun {
		recovery, ok := opts.Store.(MaintenanceStore)
		if !ok {
			return result, errors.New("crawl: store does not support maintenance operations")
		}
		reclaimed, err := recovery.ResetStuckClaims(ctx, now.Add(-opts.ClaimGrace), opts.Limit)
		if err != nil {
			return result, fmt.Errorf("reset stuck claims: %w", err)
		}
		result.ReclaimedClaims = reclaimed
		log.Info("maintain: reclaimed abandoned claims",
			"count", reclaimed, "grace", opts.ClaimGrace.String())

		pruned, err := opts.Store.PruneFrontier(ctx, now.Add(-opts.FrontierRetention), opts.MaxConsecutiveEmpty)
		if err != nil {
			return result, fmt.Errorf("prune frontier: %w", err)
		}
		result.Pruned = pruned
		log.Info("maintain: pruned frontier entries",
			"count", pruned,
			"retention", opts.FrontierRetention.String(),
			"max_consecutive_empty", opts.MaxConsecutiveEmpty)

		reprioritised, err := recovery.RecomputeFrontierPriority(ctx)
		if err != nil {
			return result, fmt.Errorf("recompute frontier priority: %w", err)
		}
		result.Reprioritised = reprioritised
		log.Info("maintain: recomputed frontier priority", "count", reprioritised)
	}

	if err := reportInspection(ctx, opts, &result); err != nil {
		return result, err
	}
	if opts.DryRun {
		log.Info("maintain: dry run, nothing was changed",
			"would_reclaim_claims_before", now.Add(-opts.ClaimGrace).UTC().Format(time.RFC3339),
			"would_prune_before", now.Add(-opts.FrontierRetention).UTC().Format(time.RFC3339),
			"frontier_size", result.FrontierSize,
			"dead_frontier", result.DeadFrontierSize)
	}
	return result, nil
}

// reportInspection reads the state maintenance is judged by. It runs on both
// the real and the dry run, because the numbers are the report.
func reportInspection(ctx context.Context, opts MaintainOptions, result *MaintainResult) error {
	if queue, ok := opts.Store.(QueueInspector); ok {
		depths, err := queue.QueueDepths(ctx)
		if err != nil {
			return fmt.Errorf("queue depths: %w", err)
		}
		result.QueueDepths = depths
		oldest, hasOldest, err := queue.QueueOldest(ctx)
		if err != nil {
			return fmt.Errorf("queue oldest: %w", err)
		}
		result.QueueOldest, result.HasQueueOldest = oldest, hasOldest
	}
	if frontier, ok := opts.Store.(FrontierInspector); ok {
		size, err := frontier.FrontierSize(ctx)
		if err != nil {
			return fmt.Errorf("frontier size: %w", err)
		}
		result.FrontierSize = size
		dead, err := frontier.DeadFrontierSize(ctx)
		if err != nil {
			return fmt.Errorf("dead frontier size: %w", err)
		}
		result.DeadFrontierSize = dead
	}
	opts.Metrics.SetFrontierSize(result.FrontierSize)

	if reporter, ok := opts.Store.(PipelineReporter); ok {
		newest, ok, err := reporter.NewestFetchedAt(ctx)
		if err != nil {
			return fmt.Errorf("newest fetched: %w", err)
		}
		result.NewestFetchedAt, result.HasFetched = newest, ok
		if ok {
			age := opts.Now().Sub(newest).Seconds()
			if age < 0 {
				age = 0
			}
			opts.Metrics.SetPipelineStaleness(obs.StageCrawl, age)
		}
	}
	opts.Log.Info("maintain: pipeline state",
		"frontier_size", result.FrontierSize,
		"dead_frontier", result.DeadFrontierSize,
		"queue_pending", result.QueueDepths[contract.JobPending],
		"queue_retry", result.QueueDepths[contract.JobRetry],
		"queue_claimed", result.QueueDepths[contract.JobClaimed],
		"queue_done", result.QueueDepths[contract.JobDone],
		"queue_dead", result.QueueDepths[contract.JobDead])
	return nil
}
