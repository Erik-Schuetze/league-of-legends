package crawl

import (
	"context"
	"fmt"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
)

// Timeline retention is one year, against two for a match summary, and the
// window is closing the whole time: a match that ages out of it can never be
// fetched again at any price. The default horizon is deliberately short of the
// year - 330 days - because a request inside the last few weeks of the window
// is a request made against a payload Riot may already have dropped, and a 404
// spends the same rate-limit budget as a success.
const DefaultTimelineSince = 330 * 24 * time.Hour

// DefaultTimelineMinDurationS is the duration floor, in seconds. A game that
// ended inside the first frame interval has no participant frames at all, so a
// timeline for it is a payload with an empty body of facts; excluding it by
// default keeps the rate-limit budget for games that have something in them.
// -include-short overrides it.
const DefaultTimelineMinDurationS = 600

// DefaultTimelineLimit caps one backfill pass. A development key allows 100
// requests per two minutes per method per region, so a pass of 1000 is over
// twenty minutes of fetching at the rate limit and is a plan; a number in the
// millions is a wish.
const DefaultTimelineLimit = 1000

// TimelineBackfillOptions is one run of `lolstats-ingest backfill-timelines`.
type TimelineBackfillOptions struct {
	Deps Deps

	// Since is the lower bound on the game's own creation time, not on when it
	// was crawled. Timeline retention is measured from the game, so a match
	// crawled yesterday that was played two years ago is already unfetchable.
	Since time.Time

	Queue        int
	MinDurationS int
	IncludeShort bool

	// Limit is how many candidates may be enqueued. Zero means none, which is
	// what a dry run passes: a dry run has to be able to ask the queue what it
	// would do without asking Riot anything.
	Limit int

	// DryRun reports the selection without enqueuing it.
	DryRun bool
}

// TimelineBackfillResult is what the run found and what it did about it. The
// counts are the point: the number of matches the duration floor removed and
// the number already covered are the two ways a backfill silently does nothing,
// and neither is visible from an "enqueued N" line alone.
type TimelineBackfillResult struct {
	Eligible      int
	Ready         int
	ShortExcluded int
	AlreadyQueued int
	AlreadyDone   int
	// WouldEnqueue is Ready capped by the run's limit: the number of Riot
	// timeline requests the run asks for. It is reported on a dry run, when
	// Enqueued is necessarily zero, which is the whole point of a dry run.
	WouldEnqueue int
	Enqueued     int
	DryRun       bool
}

// BackfillTimelines selects a bounded, reproducible sample of matches and
// enqueues a timeline fetch for each.
//
// The selection rule itself lives in the store, because it is one SQL statement
// over the control plane; this function's job is the two things around it - it
// clamps the horizon, and it refuses to spend a rate-limit budget it cannot
// describe. A run that enqueued nothing because every candidate was already
// covered is reported as that, rather than as a run that fetched nothing.
func BackfillTimelines(ctx context.Context, opts TimelineBackfillOptions) (TimelineBackfillResult, error) {
	deps := opts.Deps
	deps.normalize()
	if deps.Store == nil {
		return TimelineBackfillResult{}, fmt.Errorf("crawl: timeline backfill needs a store")
	}
	if opts.Queue == 0 {
		opts.Queue = 420
	}
	if opts.MinDurationS == 0 {
		opts.MinDurationS = DefaultTimelineMinDurationS
	}
	if opts.Since.IsZero() {
		opts.Since = deps.Now().Add(-DefaultTimelineSince)
	}

	// A dry run asks the control plane what it would do and nothing more: no
	// candidate rows are materialised, because the counts are the answer and
	// the rows would be discarded.
	limit := opts.Limit
	if opts.DryRun {
		limit = 0
	}
	stats, err := deps.Store.TimelineCandidates(ctx, contract.TimelineQuery{
		Region:       deps.Region,
		QueueID:      opts.Queue,
		Since:        opts.Since,
		MinDurationS: opts.MinDurationS,
		IncludeShort: opts.IncludeShort,
		Limit:        limit,
	})
	if err != nil {
		return TimelineBackfillResult{}, err
	}

	result := TimelineBackfillResult{
		Eligible:      stats.Eligible,
		Ready:         stats.Ready,
		ShortExcluded: stats.ShortExcluded,
		AlreadyQueued: stats.AlreadyQueued,
		AlreadyDone:   stats.AlreadyDone,
		DryRun:        opts.DryRun,
	}
	result.WouldEnqueue = stats.Ready
	if opts.Limit > 0 && opts.Limit < result.WouldEnqueue {
		result.WouldEnqueue = opts.Limit
	}
	if opts.DryRun || opts.Limit <= 0 || len(stats.Matches) == 0 {
		return result, nil
	}

	added, err := enqueueTimelines(ctx, deps.Store, stats.Matches, PriorityTimeline)
	result.Enqueued = added
	if err != nil {
		return result, err
	}
	return result, nil
}
