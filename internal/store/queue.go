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

// EnqueueMatches adds work to the queue and returns how many rows were newly
// created.
//
// The dedupe is in the ON CONFLICT, not in the caller: a PUUID's history
// overlaps every other participant's history, so the same match id arrives
// repeatedly by design. Duplicate ids inside one batch are collapsed here
// instead, because a single statement cannot insert the same key twice.
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
		sb.WriteString("INSERT INTO fetch_queue (match_id, priority, attempts, not_before, status) VALUES ")
		args := make([]any, 0, len(chunk)*5)
		for i, item := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := i * 5
			fmt.Fprintf(&sb, "($%d, $%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4, base+5)
			notBefore := item.NotBefore
			if notBefore.IsZero() {
				notBefore = now
			}
			args = append(args, item.MatchID, item.Priority, item.Attempts, notBefore.UTC(), string(contract.JobPending))
		}
		sb.WriteString(" ON CONFLICT (match_id) DO NOTHING")

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

// dedupeQueueItems collapses repeated match ids, keeping the most urgent
// priority in the batch. Lower priority values are claimed first, so a match
// discovered both as a seed and as another player's participant keeps the
// earlier priority.
func dedupeQueueItems(items []contract.QueueItem) []contract.QueueItem {
	seen := make(map[string]int, len(items))
	out := make([]contract.QueueItem, 0, len(items))
	for _, item := range items {
		if item.MatchID == "" {
			continue
		}
		if idx, ok := seen[item.MatchID]; ok {
			if item.Priority < out[idx].Priority {
				out[idx].Priority = item.Priority
			}
			continue
		}
		seen[item.MatchID] = len(out)
		out = append(out, item)
	}
	return out
}

// claimJobsSQL hands out the most urgent ready work.
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
const claimJobsSQL = `
UPDATE fetch_queue q
SET status = $3, claimed_at = $1, attempts = q.attempts + 1
WHERE q.id IN (
    SELECT id FROM fetch_queue
    WHERE status IN ('pending', 'retry') AND not_before <= $1
    ORDER BY priority, not_before, id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
RETURNING q.id, q.match_id, q.priority, q.attempts, q.not_before, q.claimed_at, q.status`

// ClaimJobs reserves up to limit ready jobs for this worker. The returned items
// are ordered by the same keys the statement ordered by, because the caller's
// rate limiter should spend its budget on the most urgent work first and
// RETURNING does not promise an order.
func (s *Store) ClaimJobs(ctx context.Context, limit int, now time.Time) ([]contract.QueueItem, error) {
	limit = clampLimit(limit)
	if limit == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, claimJobsSQL, now.UTC(), limit, string(contract.JobClaimed))
	if err != nil {
		return nil, fmt.Errorf("store: ClaimJobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]contract.QueueItem, 0, limit)
	for rows.Next() {
		var (
			item      contract.QueueItem
			notBefore sql.NullTime
			claimedAt sql.NullTime
			status    string
		)
		if err := rows.Scan(&item.ID, &item.MatchID, &item.Priority, &item.Attempts, &notBefore, &claimedAt, &status); err != nil {
			return nil, fmt.Errorf("store: ClaimJobs: %w", err)
		}
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
