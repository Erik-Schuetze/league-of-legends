package crawl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/raw"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// Backfill defaults. A backfill is an operator-driven repair, so it is bounded
// by default: an unbounded re-run over a key range is how a crawler wakes up
// having spent the day's rate-limit budget on matches it already has.
const (
	DefaultBackfillLimit = 200
	DefaultBackfillQueue = 420
)

// KnownMatchChecker reports whether a match is already in the control plane.
// It is optional: without it, a backfill re-fetches every key it is given and
// reports how many were already known.
type KnownMatchChecker interface {
	MatchExists(ctx context.Context, matchID string) (bool, error)
}

// BackfillOptions selects the keys to re-run.
//
// Exactly one key source is used, in this order: MatchIDs (an explicit list, or
// a file the caller read), then PUUID with From/To (a window of one player's
// history). The two shapes cover the cases an operator actually has: "these
// fifty ids were in the gap in the archive" and "re-crawl this player's last
// week".
type BackfillOptions struct {
	Deps

	MatchIDs []string
	PUUID    string
	From     time.Time
	To       time.Time

	// Limit bounds the number of keys fetched in one run.
	Limit int
	// Queue filters the history lookup to one queue.
	Queue int
	// HistoryCount bounds how many ids a history lookup asks for per page.
	HistoryCount int
	// Force re-fetches keys the control plane already knows. Without it, a
	// known key is skipped and counted, which is what makes a repeated
	// backfill cheap.
	Force bool
	// JobTimeout bounds one fetch.
	JobTimeout time.Duration
}

// BackfillResult is the run summary. Failures are collected rather than
// aborting the run: a repair that stops at the first 500 is not a repair.
type BackfillResult struct {
	Candidates int
	Fetched    int
	Inserted   int
	Known      int
	Failed     int
	// Failures holds one short line per failed key, capped so that a
	// catastrophic run does not fill memory with error text.
	Failures []string
}

// maxBackfillFailures bounds the reported failure list; the count is exact even
// when the list is not.
const maxBackfillFailures = 20

// Backfill re-runs a bounded key range through the same path the worker uses:
// fetch, archive, record. Nothing about it is special-cased - which is the
// point. If a payload is recoverable by a backfill, it is recoverable by the
// crawler, and a re-run of an already-fetched key is a no-op in the control
// plane because UpsertMatch dedupes on match_id.
func Backfill(ctx context.Context, opts BackfillOptions) (BackfillResult, error) {
	opts.normalize()
	if opts.Store == nil {
		return BackfillResult{}, errors.New("crawl: backfill needs a store")
	}
	if opts.Fetcher == nil {
		return BackfillResult{}, errors.New("crawl: backfill needs a rioter")
	}
	if opts.Writer == nil {
		return BackfillResult{}, errors.New("crawl: backfill needs a raw writer")
	}
	opts.Limit = defaultInt(opts.Limit, DefaultBackfillLimit)
	opts.Queue = defaultInt(opts.Queue, DefaultBackfillQueue)
	opts.HistoryCount = defaultInt(opts.HistoryCount, DefaultHistoryCount)
	opts.JobTimeout = defaultDuration(opts.JobTimeout, DefaultJobTimeout)

	var result BackfillResult
	keys, err := backfillKeys(ctx, opts)
	if err != nil {
		return result, err
	}
	result.Candidates = len(keys)
	if len(keys) == 0 {
		opts.Log.Info("backfill found no keys in the requested range")
		return result, nil
	}

	known, _ := opts.Store.(KnownMatchChecker)
	for _, matchID := range keys {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !opts.Force && known != nil {
			exists, err := known.MatchExists(ctx, matchID)
			if err != nil {
				return result, fmt.Errorf("backfill: match exists %s: %w", matchID, err)
			}
			if exists {
				result.Known++
				continue
			}
		}
		inserted, err := backfillOne(ctx, opts, matchID)
		if err != nil {
			result.Failed++
			if len(result.Failures) < maxBackfillFailures {
				result.Failures = append(result.Failures, fmt.Sprintf("%s: %v", matchID, err))
			}
			opts.Log.Warn("backfill key failed", "match_id", matchID, "err", err)
			continue
		}
		result.Fetched++
		if inserted {
			result.Inserted++
		}
	}

	if err := opts.Writer.Flush(ctx); err != nil {
		return result, fmt.Errorf("flush archive: %w", err)
	}
	opts.Log.Info("backfill finished",
		"candidates", result.Candidates,
		"fetched", result.Fetched,
		"inserted", result.Inserted,
		"known", result.Known,
		"failed", result.Failed)

	if result.Failed > 0 {
		return result, fmt.Errorf("backfill: %d of %d keys failed; first: %s",
			result.Failed, result.Candidates, result.Failures[0])
	}
	return result, nil
}

// backfillKeys resolves the option set into the list of keys to fetch, stopping
// at Limit.
func backfillKeys(ctx context.Context, opts BackfillOptions) ([]string, error) {
	if len(opts.MatchIDs) > 0 {
		return dedupeStrings(opts.MatchIDs, opts.Limit), nil
	}
	if opts.PUUID == "" {
		return nil, errors.New("crawl: backfill needs -match-id, -ids-file or -puuid")
	}

	keys := make([]string, 0, opts.Limit)
	seen := make(map[string]struct{}, opts.Limit)
	start := 0
	for len(keys) < opts.Limit {
		want := opts.HistoryCount
		if remaining := opts.Limit - len(keys); remaining < want {
			want = remaining
		}
		query := riot.MatchListQuery{
			PUUID:     opts.PUUID,
			Count:     want,
			Start:     start,
			StartTime: opts.From,
			EndTime:   opts.To,
			Queue:     opts.Queue,
		}
		ids, err := opts.Fetcher.MatchIDs(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("backfill history %s: %w", opts.PUUID, err)
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			keys = append(keys, id)
			if len(keys) >= opts.Limit {
				break
			}
		}
		start += len(ids)
	}
	opts.Log.Debug("backfill keys resolved",
		"puuid", opts.PUUID, "keys", len(keys), "from", opts.From.UTC(), "to", opts.To.UTC())
	return keys, nil
}

// backfillOne fetches, retains and records one key.
func backfillOne(ctx context.Context, opts BackfillOptions, matchID string) (bool, error) {
	jobCtx, cancel := context.WithTimeout(ctx, opts.JobTimeout)
	defer cancel()

	dto, _, err := opts.Fetcher.MatchWithPayload(jobCtx, matchID)
	if err != nil {
		return false, err
	}
	meta := MatchMetaFromDTO(dto, matchID, opts.Region, opts.Now())
	if err := opts.Writer.WriteMatch(ctx, dto, meta); err != nil {
		return false, fmt.Errorf("archive: %w", err)
	}
	record := MatchRecordFromMeta(meta, raw.MatchPartitionURI(writerRoot(opts.Writer), meta))
	inserted, err := opts.Store.UpsertMatch(ctx, record)
	if err != nil {
		return false, fmt.Errorf("upsert: %w", err)
	}
	return inserted, nil
}

// writerRoot asks the archive where it is rooted, so the recorded raw_uri is
// derived from the writer that holds the payload rather than from a second copy
// of the configuration.
func writerRoot(w contract.RawWriter) string {
	if rooted, ok := w.(interface{ Root() string }); ok {
		return rooted.Root()
	}
	return ""
}

func dedupeStrings(in []string, limit int) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, value := range in {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
