package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
)

// frontierBatch bounds one INSERT, for the same reason the queue does.
const frontierBatch = 500

// UpsertFrontier records discovered PUUIDs and returns how many were new.
//
// Discovery is not authority over the frontier: a PUUID that was declared dead
// (Riot answers 404 for it, or it is an account the crawl must not widen) is not
// revived by being seen again, because the flag exists precisely to break the
// loop where a dead PUUID is rediscovered through its own matches on every pass.
// Reviving is a deliberate act.
//
// `RETURNING (xmax = 0)` is how Postgres reports which rows were inserted rather
// than updated: a fresh tuple has no overwriting transaction. Counting the added
// PUUIDs any other way would mean a second round trip and a race with the
// crawler's own writes.
func (s *Store) UpsertFrontier(ctx context.Context, entries []contract.FrontierEntry) (int, error) {
	batch := dedupeFrontier(entries)
	if len(batch) == 0 {
		return 0, nil
	}
	added := 0
	for start := 0; start < len(batch); start += frontierBatch {
		end := min(start+frontierBatch, len(batch))
		chunk := batch[start:end]

		var sb strings.Builder
		sb.WriteString("INSERT INTO crawl_frontier (puuid, region, seed_tier, seed_division, last_seen_at, last_fetched_at, consecutive_empty, priority, dead) VALUES ")
		args := make([]any, 0, len(chunk)*6)
		for i, entry := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := i * 6
			fmt.Fprintf(&sb, "($%d, $%d, $%d, $%d, $%d, NULL, 0, $%d, false)", base+1, base+2, base+3, base+4, base+5, base+6)
			region := entry.Region
			if region == "" {
				region = s.opts.Region
			}
			lastSeen := entry.LastSeenAt
			if lastSeen.IsZero() {
				lastSeen = s.now()
			}
			args = append(args, entry.PUUID, region, entry.SeedTier, entry.SeedDivision, lastSeen.UTC(), entry.Priority)
		}
		sb.WriteString(" ON CONFLICT (puuid) DO UPDATE SET")
		sb.WriteString(" region = EXCLUDED.region,")
		sb.WriteString(" seed_tier = EXCLUDED.seed_tier,")
		sb.WriteString(" seed_division = EXCLUDED.seed_division,")
		sb.WriteString(" last_seen_at = GREATEST(crawl_frontier.last_seen_at, EXCLUDED.last_seen_at),")
		sb.WriteString(" priority = LEAST(crawl_frontier.priority, EXCLUDED.priority)")
		sb.WriteString(" RETURNING (xmax = 0)")

		rows, err := s.db.QueryContext(ctx, sb.String(), args...)
		if err != nil {
			return added, fmt.Errorf("store: UpsertFrontier: %w", err)
		}
		for rows.Next() {
			var inserted bool
			if err := rows.Scan(&inserted); err != nil {
				_ = rows.Close()
				return added, fmt.Errorf("store: UpsertFrontier: %w", err)
			}
			if inserted {
				added++
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return added, fmt.Errorf("store: UpsertFrontier: %w", err)
		}
	}
	return added, nil
}

// dedupeFrontier collapses repeated PUUIDs inside one batch, keeping the first
// occurrence. A ladder page and a match participant list routinely name the same
// player, and one statement cannot insert the same key twice.
func dedupeFrontier(entries []contract.FrontierEntry) []contract.FrontierEntry {
	seen := make(map[string]struct{}, len(entries))
	out := make([]contract.FrontierEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.PUUID == "" {
			continue
		}
		if _, ok := seen[entry.PUUID]; ok {
			continue
		}
		seen[entry.PUUID] = struct{}{}
		out = append(out, entry)
	}
	return out
}

// claimFrontierSQL picks the next PUUIDs to walk.
//
// Claiming is marking: there is no 'claimed' column on the frontier because a
// claim that is not durable until the walk finishes is not a claim. Setting
// last_fetched_at here takes the row out of every other worker's reach for the
// cooldown window, so a worker that dies mid-walk costs one cooldown instead of
// causing every other worker to walk the same PUUID immediately. RETURNING hands
// back the pre-update timestamp so the caller can order the batch the way the
// statement ordered it.
//
// The casts on $1 and $3 are load-bearing rather than cosmetic: with an
// untyped parameter on the left of the subtraction, Postgres infers the
// parameter's type from the subtraction itself and resolves the comparison
// against a timestamptz column as `timestamp with time zone <= interval`.
const claimFrontierSQL = `
WITH picked AS (
    SELECT puuid, last_fetched_at FROM crawl_frontier
    WHERE dead = false
      AND (last_fetched_at IS NULL OR last_fetched_at <= $1::timestamptz - make_interval(secs => $3::double precision))
    ORDER BY priority, last_fetched_at ASC NULLS FIRST, puuid
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
UPDATE crawl_frontier f
SET last_fetched_at = $1, last_seen_at = COALESCE(f.last_seen_at, $1)
FROM picked
WHERE f.puuid = picked.puuid
RETURNING f.puuid, f.region, f.seed_tier, f.seed_division, f.consecutive_empty, f.priority, f.dead,
          picked.last_fetched_at, f.last_seen_at`

// ClaimFrontier reserves up to limit PUUIDs to walk.
func (s *Store) ClaimFrontier(ctx context.Context, limit int, now time.Time) ([]contract.FrontierEntry, error) {
	limit = clampLimit(limit)
	if limit == 0 {
		return nil, nil
	}
	cooldown := s.opts.FrontierCooldown.Seconds()
	rows, err := s.db.QueryContext(ctx, claimFrontierSQL, now.UTC(), limit, cooldown)
	if err != nil {
		return nil, fmt.Errorf("store: ClaimFrontier: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type picked struct {
		entry    contract.FrontierEntry
		previous time.Time
	}
	items := make([]picked, 0, limit)
	for rows.Next() {
		var (
			item     picked
			previous *time.Time
		)
		if err := rows.Scan(&item.entry.PUUID, &item.entry.Region, &item.entry.SeedTier, &item.entry.SeedDivision,
			&item.entry.ConsecutiveEmpty, &item.entry.Priority, &item.entry.Dead,
			&previous, &item.entry.LastSeenAt); err != nil {
			return nil, fmt.Errorf("store: ClaimFrontier: %w", err)
		}
		if previous != nil {
			item.previous = *previous
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: ClaimFrontier: %w", err)
	}

	// Least recently fetched first is the crawler's fairness rule, so the order
	// of the claim has to survive RETURNING's unspecified row order.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].entry.Priority != items[j].entry.Priority {
			return items[i].entry.Priority < items[j].entry.Priority
		}
		if !items[i].previous.Equal(items[j].previous) {
			return items[i].previous.Before(items[j].previous)
		}
		return items[i].entry.PUUID < items[j].entry.PUUID
	})

	entries := make([]contract.FrontierEntry, 0, len(items))
	for _, item := range items {
		entries = append(entries, item.entry)
	}
	return entries, nil
}

// markFrontierFetchedSQL records the outcome of a walk.
//
// consecutive_empty is a momentum signal, not a counter to admire: it resets the
// moment a walk yields a match and only grows while a player's history keeps
// coming back empty, which is what maintain uses to decide that a PUUID has
// stopped being useful.
const markFrontierFetchedSQL = `
UPDATE crawl_frontier
SET last_fetched_at = $2,
    last_seen_at = $2,
    consecutive_empty = CASE WHEN $3 THEN consecutive_empty + 1 ELSE 0 END
WHERE puuid = $1`

// MarkFrontierFetched records that a PUUID was walked, and whether it produced
// anything.
func (s *Store) MarkFrontierFetched(ctx context.Context, puuid string, at time.Time, empty bool) error {
	if _, err := s.db.ExecContext(ctx, markFrontierFetchedSQL, puuid, at.UTC(), empty); err != nil {
		return fmt.Errorf("store: MarkFrontierFetched %s: %w", puuid, err)
	}
	return nil
}

// markFrontierDeadSQL retires a PUUID. last_fetched_at is left alone so that
// "when did we last try" survives the retirement, which is what a later audit of
// a dead frontier needs.
const markFrontierDeadSQL = `
UPDATE crawl_frontier SET dead = true, dead_cause = $2 WHERE puuid = $1`

// MarkFrontierDead stops the crawler from widening through this PUUID.
func (s *Store) MarkFrontierDead(ctx context.Context, puuid string, cause string) error {
	if _, err := s.db.ExecContext(ctx, markFrontierDeadSQL, puuid, nullText(cause)); err != nil {
		return fmt.Errorf("store: MarkFrontierDead %s: %w", puuid, err)
	}
	return nil
}

// pruneFrontierSQL drops frontier rows that will not be walked again.
//
// Pruning needs evidence, not suspicion: a row goes only when it is dead, or
// when it has been walked, has produced nothing for maxConsecutiveEmpty attempts
// and has not been seen for the whole window. A row that has never been seen
// (last_seen_at IS NULL) is kept - "unknown" is not "stale", and deleting the
// unknown would erase the only record that a seed was ever discovered.
const pruneFrontierSQL = `
DELETE FROM crawl_frontier
WHERE (dead = true AND COALESCE(last_fetched_at, last_seen_at) < $1)
   OR (consecutive_empty >= $2 AND last_seen_at IS NOT NULL AND last_seen_at < $1)`

// PruneFrontier removes dead and exhausted frontier rows older than before.
func (s *Store) PruneFrontier(ctx context.Context, before time.Time, maxConsecutiveEmpty int) (int, error) {
	res, err := s.db.ExecContext(ctx, pruneFrontierSQL, before.UTC(), maxConsecutiveEmpty)
	if err != nil {
		return 0, fmt.Errorf("store: PruneFrontier: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: PruneFrontier: %w", err)
	}
	return int(n), nil
}

// FrontierSize counts the PUUIDs the crawler may still walk.
//
// Dead entries are excluded because the number feeds the frontier_size metric,
// and an operator reading it is asking "how much crawlable ground is left", not
// "how many rows exist".
func (s *Store) FrontierSize(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM crawl_frontier WHERE dead = false`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: FrontierSize: %w", err)
	}
	s.metrics.SetFrontierSize(n)
	return n, nil
}

// DeadFrontierSize counts retired entries so maintain can report them without an
// ad-hoc query of its own.
func (s *Store) DeadFrontierSize(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM crawl_frontier WHERE dead = true`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: DeadFrontierSize: %w", err)
	}
	return n, nil
}
