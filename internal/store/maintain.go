package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
)

// The methods in this file are not part of contract.Store. The frozen interface
// covers everything the crawler needs to make progress; `maintain` needs three
// more things - to reclaim claims abandoned by a dead worker, to re-rank the
// frontier, and to report queue depth - and none of them belongs in the
// interface's vocabulary, which is about moving work rather than about
// inspecting it. They are additive, so the contract stays satisfied.

// ResetStuckClaims returns claims abandoned by a worker that died to the queue.
//
// The grace period is the whole safety argument: a job is only reclaimed when
// its claim is older than the caller's threshold, which must be longer than any
// legitimate fetch (the Riot client's own timeout plus the archive write). A
// shorter threshold would have two workers fetching the same match, which wastes
// rate-limit budget and is exactly what the queue exists to prevent.
//
// The job returns as 'retry' rather than as 'pending' because it has been
// attempted: attempts has already been incremented, and the attempt ceiling must
// keep counting towards a dead letter instead of starting over.
const resetStuckClaimsSQL = `
UPDATE fetch_queue
SET status = $2, claimed_at = NULL, not_before = $3, last_cause = $4
WHERE id IN (
    SELECT id FROM fetch_queue
    WHERE status = $5 AND claimed_at IS NOT NULL AND claimed_at < $1
    ORDER BY claimed_at
    LIMIT $6
)`

// ResetStuckClaims reclaims at most limit claims older than olderThan.
func (s *Store) ResetStuckClaims(ctx context.Context, olderThan time.Time, limit int) (int, error) {
	limit = clampLimit(limit)
	if limit == 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, resetStuckClaimsSQL, olderThan.UTC(), string("retry"),
		s.now().UTC(), claimTimeoutCause, string("claimed"), limit)
	if err != nil {
		return 0, fmt.Errorf("store: ResetStuckClaims: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: ResetStuckClaims: %w", err)
	}
	return int(n), nil
}

// claimTimeoutCause is the last_cause written on a reclaimed claim. It is a
// constant because it is read by an operator grepping for "why is this job being
// retried", and a per-call message would be ungreppable.
const claimTimeoutCause = "claim expired: worker did not finish or report"

// recomputeFrontierPrioritySQL re-ranks the frontier from what the crawl has
// observed.
//
// Priority is derived, never stored by hand: a player who keeps yielding matches
// is worth re-walking sooner (lower priority number) than a player whose history
// has been empty several times in a row. Deriving it in one statement means the
// ordering cannot drift away from the evidence, which is what a hand-maintained
// priority column always eventually does.
//
// The clamp keeps a pathological consecutive_empty (a player who deleted their
// account, say) from pushing a row so far back that it is never picked again
// while still being counted in the frontier size.
const recomputeFrontierPrioritySQL = `
UPDATE crawl_frontier
SET priority = LEAST(300, 50 + 25 * consecutive_empty)
WHERE dead = false
  AND priority IS DISTINCT FROM LEAST(300, 50 + 25 * consecutive_empty)`

// RecomputeFrontierPriority re-ranks live frontier rows and returns how many
// changed.
func (s *Store) RecomputeFrontierPriority(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, recomputeFrontierPrioritySQL)
	if err != nil {
		return 0, fmt.Errorf("store: RecomputeFrontierPriority: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: RecomputeFrontierPriority: %w", err)
	}
	return int(n), nil
}

// QueueDepths is a snapshot of the fetch queue: how many rows are in each
// state. It is a report for a human or a metric, never a decision input - the
// crawler decides from ClaimJobs - and the map key is the contract's own
// JobStatus so that the crawl package can consume it without importing SQL.
//
// 'done' and 'dead' rows are included on purpose: a queue that is growing
// because rows are piling up in 'dead' looks identical to a working one unless
// both are visible.
const queueDepthsSQL = `
SELECT status, count(*) FROM fetch_queue GROUP BY status`

// QueueDepths reads the queue in one pass.
func (s *Store) QueueDepths(ctx context.Context) (map[contract.JobStatus]int, error) {
	rows, err := s.db.QueryContext(ctx, queueDepthsSQL)
	if err != nil {
		return nil, fmt.Errorf("store: QueueDepths: %w", err)
	}
	defer func() { _ = rows.Close() }()

	depths := make(map[contract.JobStatus]int, 5)
	for rows.Next() {
		var (
			status string
			count  int
		)
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("store: QueueDepths: scan: %w", err)
		}
		depths[contract.JobStatus(status)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: QueueDepths: %w", err)
	}
	return depths, nil
}

const queueOldestSQL = `SELECT min(not_before) FROM fetch_queue WHERE status IN ('pending', 'retry')`

// QueueOldest is the due time of the oldest work the queue is still holding.
// It is the number an operator looks at to answer "is the crawler behind?".
func (s *Store) QueueOldest(ctx context.Context) (time.Time, bool, error) {
	var oldest *time.Time
	err := s.db.QueryRowContext(ctx, queueOldestSQL).Scan(&oldest)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("store: QueueOldest: %w", err)
	}
	if oldest == nil {
		return time.Time{}, false, nil
	}
	return *oldest, true, nil
}

const newestFetchedAtSQL = `SELECT max(fetched_at) FROM matches`

// NewestFetchedAt reports the newest match the crawler has archived. The bool is
// false when nothing has ever been fetched, which the staleness metric needs to
// tell apart from "fetched a long time ago": an empty pipeline is a setup
// problem, a stalled one is an incident.
func (s *Store) NewestFetchedAt(ctx context.Context) (time.Time, bool, error) {
	var newest *time.Time
	err := s.db.QueryRowContext(ctx, newestFetchedAtSQL).Scan(&newest)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("store: NewestFetchedAt: %w", err)
	}
	if newest == nil {
		return time.Time{}, false, nil
	}
	return *newest, true, nil
}
