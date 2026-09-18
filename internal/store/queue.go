package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
)

// enqueueBatch bounds one INSERT. A history page is at most 100 ids and a
// backfill chunk is a few thousand, so the chunking exists to keep the
// statement's parameter count well inside Postgres' limit rather than to
// optimise anything.
const enqueueBatch = 500

// revivalBudget is how many times the crawl will hand a dead letter a fresh
// attempt budget before leaving it dead.
//
// The revival below exists because a dead letter is otherwise unreachable: a
// global condition - a revoked key, a ban - retires every match it touches, and
// a later discovery of that match has to be able to put it back. Unbounded,
// though, it is a loop with no end: revive, claim, fail, dead, rediscover, and
// the cost of each turn is a Riot call the limiter has to pay for. The
// verification run showed exactly that: a poison row came back to 'dead' with
// its attempt budget reset to the same two on every walk, ten fetches per walk,
// forever.
//
// Three is enough for the condition the revival is for - an outage that has
// passed - and small enough that a row which is failing for its own reasons
// stops costing calls. Past the budget the row stays visible as a dead letter
// with its cause intact, and `maintain -replay-dead-letters` is deliberately not
// bounded by this, because an operator replaying dead letters is asserting that
// the global condition has passed, which is a claim the crawl cannot make.
const revivalBudget = 3

// reviveDeadRowSet is the SET half of the conflict clause that revives a dead
// letter: the budget is restored (the attempt budget that retired the row was
// spent on a global condition rather than on anything wrong with the match) and
// the revival itself is counted, which is what bounds the loop.
const reviveDeadRowSet = `status = EXCLUDED.status, attempts = 0, revivals = fetch_queue.revivals + 1,
    not_before = EXCLUDED.not_before, claimed_at = NULL, last_cause = NULL`

// reviveDeadRowWhere is the predicate half: only a dead row, and only one that
// still has revival budget left.
func reviveDeadRowWhere(statusArg, budgetArg int) string {
	return fmt.Sprintf("fetch_queue.status = $%d AND fetch_queue.revivals < $%d", statusArg, budgetArg)
}

// EnqueueMatches adds work to the queue and returns how many rows are now
// waiting to be claimed - the rows it created plus the dead letters it
// revived.
//
// The dedupe is in the ON CONFLICT, not in the caller: a PUUID's history
// overlaps every other participant's history, so the same match id arrives
// repeatedly by design. Duplicate ids inside one batch are collapsed here
// instead, because a single statement cannot insert the same key twice.
//
// The conflict clause is a DO UPDATE ... WHERE status = 'dead', not a DO
// NOTHING. A dead letter is normally terminal, so DO NOTHING made a retired
// match id unreachable forever: a key outage that outlived one row's attempt
// budget retired every match it touched, and no later discovery of that match
// - by any player, in any crawl - could put it back. Re-queueing a dead row
// that the crawl has just rediscovered is the cheap half of the replay path
// (maintain's ReplayDeadLettered is the explicit half) and it restarts the
// attempt budget, because the budget that retired the row was spent on a
// global condition rather than on anything wrong with the match.
//
// The revival is bounded by revivalBudget revivals per row. Without that bound
// the clause is an infinite loop rather than a recovery path: the row is
// rediscovered by every crawl of every player who played the match, so a row
// that fails for its own reasons would be re-fetched forever, each revival
// restarting the attempt budget that exists to retire it.
func (s *Store) EnqueueMatches(ctx context.Context, items []contract.QueueItem) (int, error) {
	batch := dedupeQueueItems(items)
	if len(batch) == 0 {
		return 0, nil
	}
	now := s.now()
	added := 0
	for start := 0; start < len(batch); start += enqueueBatch {
		end := min(start+enqueueBatch, len(batch))
		chunk := batch[start:end]

		var sb strings.Builder
		sb.WriteString("INSERT INTO fetch_queue (match_id, kind, priority, attempts, not_before, status) VALUES ")
		args := make([]any, 0, len(chunk)*6+1)
		for i, item := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := i * 6
			fmt.Fprintf(&sb, "($%d, $%d, $%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4, base+5, base+6)
			notBefore := item.NotBefore
			if notBefore.IsZero() {
				notBefore = now
			}
			args = append(args, item.MatchID, item.Kind.Stored(), item.Priority, item.Attempts, notBefore.UTC(), string(contract.JobPending))
		}
		statusArg := len(chunk)*6 + 1
		budgetArg := statusArg + 1
		fmt.Fprintf(&sb, " ON CONFLICT (match_id, kind) DO UPDATE\nSET %s\nWHERE %s",
			reviveDeadRowSet, reviveDeadRowWhere(statusArg, budgetArg))
		args = append(args, string(contract.JobDead), revivalBudget)

		res, err := s.db.ExecContext(ctx, sb.String(), args...)
		if err != nil {
			return added, fmt.Errorf("store: EnqueueMatches: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return added, fmt.Errorf("store: EnqueueMatches: %w", err)
		}
		added += int(n)
	}
	return added, nil
}

// dedupeQueueItems collapses repeated (match id, kind) pairs, keeping the most
// urgent priority in the batch. Lower priority values are claimed first, so a
// match discovered both as a seed and as another player's participant keeps the
// earlier priority.
//
// The kind is part of the key: a summary and a timeline for one match are two
// different jobs, and collapsing them would silently drop one of the two
// requests the caller asked for.
func dedupeQueueItems(items []contract.QueueItem) []contract.QueueItem {
	seen := make(map[string]int, len(items))
	out := make([]contract.QueueItem, 0, len(items))
	for _, item := range items {
		if item.MatchID == "" {
			continue
		}
		key := item.MatchID + "\x00" + item.Kind.Stored()
		if idx, ok := seen[key]; ok {
			if item.Priority < out[idx].Priority {
				out[idx].Priority = item.Priority
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, item)
	}
	return out
}

// claimJobsSQL hands out the most urgent ready work of one kind.
//
// `FOR UPDATE SKIP LOCKED` is what makes two workers safe: a row another worker
// has already locked is skipped rather than waited on, so the queue never
// blocks and never duplicates. 'retry' is claimed alongside 'pending' because a
// retry is a ready row with an earlier deadline, and 'not_before <= $1' is the
// whole backoff policy.
//
// The attempt counter increments at claim time, not at failure time: a worker
// that dies mid-fetch must still consume an attempt, otherwise a payload that
// reliably kills the process would be retried forever.
//
// `kind = $4` is not an optimisation. A worker can only fetch one payload type,
// so a summary worker that claimed a timeline row would have no handler for it
// and would have to put it back, and a timeline worker that claimed a summary
// row would fetch the wrong thing under the wrong method label - which is also
// what the rate limiter keys on.
const claimJobsSQL = `
UPDATE fetch_queue q
SET status = $3, claimed_at = $1, attempts = q.attempts + 1
WHERE q.id IN (
    SELECT id FROM fetch_queue
    WHERE kind = $4 AND status IN ('pending', 'retry') AND not_before <= $1
    ORDER BY priority, not_before, id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
RETURNING q.id, q.match_id, q.kind, q.priority, q.attempts, q.not_before, q.claimed_at, q.status`

// ClaimJobs reserves up to limit ready match-summary jobs for this worker.
//
// It is ClaimJobsOfKind(ctx, contract.KindMatch, ...) rather than a
// kind-agnostic claim, because a kind-agnostic claim is the bug: whichever
// worker ran first would take the other's backlog.
func (s *Store) ClaimJobs(ctx context.Context, limit int, now time.Time) ([]contract.QueueItem, error) {
	return s.ClaimJobsOfKind(ctx, contract.KindMatch, limit, now)
}

// ClaimJobsOfKind reserves up to limit ready jobs of one kind. The returned
// items are ordered by the same keys the statement ordered by, because the
// caller's rate limiter should spend its budget on the most urgent work first
// and RETURNING does not promise an order.
func (s *Store) ClaimJobsOfKind(ctx context.Context, kind contract.QueueKind, limit int, now time.Time) ([]contract.QueueItem, error) {
	limit = clampLimit(limit)
	if limit == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, claimJobsSQL, now.UTC(), limit, string(contract.JobClaimed), kind.Stored())
	if err != nil {
		return nil, fmt.Errorf("store: ClaimJobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]contract.QueueItem, 0, limit)
	for rows.Next() {
		var (
			item      contract.QueueItem
			jobKind   string
			notBefore sql.NullTime
			claimedAt sql.NullTime
			status    string
		)
		if err := rows.Scan(&item.ID, &item.MatchID, &jobKind, &item.Priority, &item.Attempts, &notBefore, &claimedAt, &status); err != nil {
			return nil, fmt.Errorf("store: ClaimJobs: %w", err)
		}
		item.Kind = contract.ParseQueueKind(jobKind)
		item.NotBefore = notBefore.Time
		item.ClaimedAt = claimedAt.Time
		item.Status = contract.JobStatus(status)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: ClaimJobs: %w", err)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		if !items[i].NotBefore.Equal(items[j].NotBefore) {
			return items[i].NotBefore.Before(items[j].NotBefore)
		}
		return items[i].ID < items[j].ID
	})
	s.metrics.AddQueueClaimed(len(items))
	return items, nil
}

// completeJobSQL closes a job. The status guard means a job whose claim was
// reset by maintenance (and possibly taken by another worker) is not closed out
// from under its new owner; zero rows affected is therefore a normal outcome,
// not an error.
const completeJobSQL = `
UPDATE fetch_queue SET status = $2, claimed_at = NULL, last_cause = NULL
WHERE id = $1 AND status = $3`

// CompleteJob marks a claimed job as done.
func (s *Store) CompleteJob(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, completeJobSQL, id, string(contract.JobDone), string(contract.JobClaimed)); err != nil {
		return fmt.Errorf("store: CompleteJob %d: %w", id, err)
	}
	return nil
}

// retryJobSQL puts a failed job back in the queue behind a backoff. The cause is
// kept on the row because the alternative - a queue of jobs that each failed for
// one of six reasons and no record of which - is undiagnosable.
const retryJobSQL = `
UPDATE fetch_queue SET status = $2, not_before = $3, claimed_at = NULL, last_cause = $4
WHERE id = $1 AND status = $5`

// RetryJob schedules another attempt at notBefore.
func (s *Store) RetryJob(ctx context.Context, id int64, notBefore time.Time, cause string) error {
	_, err := s.db.ExecContext(ctx, retryJobSQL, id, string(contract.JobRetry), notBefore.UTC(), nullText(cause), string(contract.JobClaimed))
	if err != nil {
		return fmt.Errorf("store: RetryJob %d: %w", id, err)
	}
	return nil
}

// deadLetterJobSQL retires a job. claimed_at is overwritten with the time the
// letter was written rather than left at the claim time, so the dead-letter
// index answers "what stopped moving, and when" in one ordered scan.
const deadLetterJobSQL = `
UPDATE fetch_queue SET status = $2, claimed_at = $3, last_cause = $4
WHERE id = $1 AND status = $5`

// DeadLetterJob retires a job that must not be attempted again.
func (s *Store) DeadLetterJob(ctx context.Context, id int64, cause string) error {
	_, err := s.db.ExecContext(ctx, deadLetterJobSQL, id, string(contract.JobDead), s.now().UTC(), nullText(cause), string(contract.JobClaimed))
	if err != nil {
		return fmt.Errorf("store: DeadLetterJob %d: %w", id, err)
	}
	return nil
}

// releaseJobSQL hands a claim back without charging the row for it.
//
// ClaimJobs increments attempts because a worker that dies mid-fetch must still
// consume one. The opposite case needs the opposite treatment: a failure that
// says nothing about this match - the key was refused, the API is closed to us -
// must not be paid for out of the row's budget, or an outage that outlives a
// row's budget retires the whole backlog. GREATEST keeps the counter at zero for
// a row that was never charged, which is the case for a claim made by another
// code path.
const releaseJobSQL = `
UPDATE fetch_queue
SET status = $2, not_before = $3, claimed_at = NULL, last_cause = $4,
    attempts = GREATEST(attempts - 1, 0)
WHERE id = $1 AND status = $5`

// ReleaseJob returns a claimed job to the queue without spending an attempt.
// It is not part of contract.Store: the crawler only needs it for failures it
// can prove are not about the row, so it is discovered by type assertion.
func (s *Store) ReleaseJob(ctx context.Context, id int64, notBefore time.Time, cause string) error {
	_, err := s.db.ExecContext(ctx, releaseJobSQL, id, string(contract.JobRetry), notBefore.UTC(), nullText(cause), string(contract.JobClaimed))
	if err != nil {
		return fmt.Errorf("store: ReleaseJob %d: %w", id, err)
	}
	return nil
}
