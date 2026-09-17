package crawl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// Seeding defaults. The tier list is walked one division at a time because
// that is the only LEAGUE-V4 shape that pages: challenger, grandmaster and
// master return their whole league in one response.
const (
	DefaultSeedQueue    = "RANKED_SOLO_5x5"
	DefaultSeedTier     = "GOLD"
	DefaultSeedDivision = "I"
	DefaultSeedPages    = 10
)

// SeedOptions is one ladder seeding pass.
type SeedOptions struct {
	Deps

	// Queue is the Riot queue type, e.g. RANKED_SOLO_5x5.
	Queue string
	Tier  string
	// Division is ignored for the apex tiers, which have no divisions.
	Division string
	// MaxPages bounds the walk up a division's ladder. Pages repeat once the
	// ladder is exhausted, so the pass also stops when a page adds nothing new.
	MaxPages int
}

// SeedResult is the audit of one pass, and the summary the command logs.
type SeedResult struct {
	RunID    int64
	Queue    string
	Tier     string
	Division string
	Pages    int
	Entries  int
	Added    int
}

// ApexTier reports whether a tier is one of the three that are returned whole.
func ApexTier(tier string) bool {
	switch strings.ToUpper(strings.TrimSpace(tier)) {
	case "CHALLENGER", "GRANDMASTER", "MASTER":
		return true
	default:
		return false
	}
}

// DiscoverSeeds walks a ladder division and writes the players it finds into
// crawl_frontier with the seed rank recorded.
//
// Two properties matter. First, the pass is auditable: a crawl_seeds row is
// opened before the first request and closed with the entry count, so a thin
// frontier can always be traced to the page that produced it - including when
// the pass fails. Second, the pass is idempotent: seeding the same division
// twice adds nothing, because UpsertFrontier merges on puuid.
func DiscoverSeeds(ctx context.Context, opts SeedOptions) (SeedResult, error) {
	opts.normalize()
	if opts.Store == nil || opts.Fetcher == nil {
		return SeedResult{}, errors.New("crawl: seeding needs a store and a rioter")
	}
	opts.Queue = strings.TrimSpace(opts.Queue)
	if opts.Queue == "" {
		opts.Queue = DefaultSeedQueue
	}
	opts.Tier = strings.ToUpper(strings.TrimSpace(opts.Tier))
	if opts.Tier == "" {
		opts.Tier = DefaultSeedTier
	}
	opts.Division = strings.ToUpper(strings.TrimSpace(opts.Division))
	apex := ApexTier(opts.Tier)
	if !apex && opts.Division == "" {
		opts.Division = DefaultSeedDivision
	}
	if opts.MaxPages <= 0 {
		opts.MaxPages = DefaultSeedPages
	}

	result := SeedResult{Queue: opts.Queue, Tier: opts.Tier, Division: opts.Division}

	runID, err := opts.Store.StartSeedRun(ctx, contract.SeedRun{
		Tier:      opts.Tier,
		Division:  opts.Division,
		Queue:     opts.Queue,
		Region:    opts.Region,
		StartedAt: opts.Now(),
	})
	if err != nil {
		return result, fmt.Errorf("start seed run: %w", err)
	}
	result.RunID = runID

	if apex {
		err = seedApex(ctx, opts, &result)
	} else {
		err = seedDivision(ctx, opts, &result)
	}

	// The run is closed even when the pass failed, so that a failed pass reads
	// as a small run rather than as a stuck one.
	if finishErr := finishSeedRun(ctx, opts, runID, result.Entries); finishErr != nil {
		if err != nil {
			return result, errors.Join(err, finishErr)
		}
		return result, finishErr
	}

	opts.Log.Info("ladder seeding finished",
		"run_id", runID,
		"queue", result.Queue,
		"tier", result.Tier,
		"division", result.Division,
		"pages", result.Pages,
		"entries", result.Entries,
		"added", result.Added)
	return result, err
}

// seedApex seeds one of the three tiers that have no divisions.
func seedApex(ctx context.Context, opts SeedOptions, result *SeedResult) error {
	entries, _, err := opts.Fetcher.ApexLeague(ctx, opts.Queue, opts.Tier)
	if err != nil {
		return fmt.Errorf("apex league %s/%s: %w", opts.Queue, opts.Tier, err)
	}
	result.Pages = 1
	result.Entries = len(entries)

	if err := archiveLeaguePage(ctx, opts, entries); err != nil {
		return err
	}
	added, err := opts.Store.UpsertFrontier(ctx, frontierEntries(entries, opts, result.Division))
	if err != nil {
		return fmt.Errorf("upsert frontier: %w", err)
	}
	result.Added += added
	opts.Log.Debug("apex league seeded", "tier", opts.Tier, "entries", len(entries), "added", added)
	return nil
}

// seedDivision pages down one division of the ladder until the pages repeat.
func seedDivision(ctx context.Context, opts SeedOptions, result *SeedResult) error {
	for page := 1; page <= opts.MaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, _, err := opts.Fetcher.LeagueEntriesWithPayload(ctx, riot.LeagueQuery{
			Queue:    opts.Queue,
			Tier:     opts.Tier,
			Division: opts.Division,
			Page:     page,
		})
		if err != nil {
			return fmt.Errorf("league entries %s/%s/%s page %d: %w",
				opts.Queue, opts.Tier, opts.Division, page, err)
		}
		result.Pages++
		if len(entries) == 0 {
			opts.Log.Debug("ladder page empty; division exhausted", "page", page)
			break
		}
		result.Entries += len(entries)

		if err := archiveLeaguePage(ctx, opts, entries); err != nil {
			return err
		}
		added, err := opts.Store.UpsertFrontier(ctx, frontierEntries(entries, opts, opts.Division))
		if err != nil {
			return fmt.Errorf("upsert frontier: %w", err)
		}
		result.Added += added
		opts.Log.Debug("ladder page seeded",
			"page", page, "entries", len(entries), "added", added)

		// Riot happily serves pages past the end of a ladder by returning a
		// window that overlaps what was already seen. A page that adds no new
		// puuid means the walk has covered this division.
		if added == 0 {
			opts.Log.Debug("ladder page added nothing new; stopping", "page", page)
			break
		}
	}
	return nil
}

// archiveLeaguePage retains the ladder response verbatim. It is a separate
// step from the frontier upsert because the page is the evidence: even if the
// upsert fails, the operator can see what the ladder said at that moment.
//
// The writer rebuilds the page body from the entries' retained bytes, so the
// caller does not pass the raw body through: the response is never re-encoded,
// it is the same bytes the client kept.
func archiveLeaguePage(ctx context.Context, opts SeedOptions, entries []riot.LeagueEntryDTO) error {
	if opts.Writer == nil {
		return nil
	}
	meta := contract.LeagueMeta{
		Region:    opts.Region,
		QueueType: opts.Queue,
		Tier:      opts.Tier,
		Division:  opts.Division,
		FetchedAt: opts.Now().UTC(),
	}
	if err := opts.Writer.WriteLeagueEntries(ctx, entries, meta); err != nil {
		return fmt.Errorf("archive league page: %w", err)
	}
	if err := opts.Writer.Flush(ctx); err != nil {
		return fmt.Errorf("flush archive: %w", err)
	}
	return nil
}

// frontierEntries converts ladder rows into crawl_frontier rows. The seed rank
// is a snapshot of where the player was when they were discovered - the only
// time this project gets to observe it for free.
func frontierEntries(entries []riot.LeagueEntryDTO, opts SeedOptions, division string) []contract.FrontierEntry {
	out := make([]contract.FrontierEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	now := opts.Now()
	for i := range entries {
		puuid := strings.TrimSpace(entries[i].PUUID)
		if puuid == "" {
			continue
		}
		if _, ok := seen[puuid]; ok {
			continue
		}
		seen[puuid] = struct{}{}
		tier := entries[i].Tier
		if tier == "" {
			tier = opts.Tier
		}
		div := entries[i].Rank
		if div == "" {
			div = division
		}
		out = append(out, contract.FrontierEntry{
			PUUID:        puuid,
			Region:       opts.Region,
			SeedTier:     strings.ToUpper(tier),
			SeedDivision: strings.ToUpper(div),
			LastSeenAt:   now,
			Priority:     PrioritySeed,
		})
	}
	return out
}

// finishSeedRun closes the audit row, using a detached context so that a
// cancelled pass still records what it found.
func finishSeedRun(ctx context.Context, opts SeedOptions, runID int64, entries int) error {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := opts.Store.FinishSeedRun(detached, runID, opts.Now(), entries); err != nil {
		return fmt.Errorf("finish seed run %d: %w", runID, err)
	}
	return nil
}
