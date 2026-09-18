package crawl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/raw"
)

// MatchProvenanceReader is the one control-plane read a timeline fetch needs.
//
// A timeline payload does not carry the queue, the patch, the game duration or
// the creation time - it knows its match id and a frame timeline, nothing else -
// so the provenance columns of a timeline archive row have to come from the
// summary that was archived earlier. Reading them costs one primary-key lookup
// against a payload that is roughly a megabyte, which is not a trade worth
// avoiding.
//
// The read is also the orphan check. A timeline for a match the control plane
// does not hold cannot be joined to anything, so it would be a payload with no
// analysis behind it: the answer is a dead letter, not a fetch.
//
// It is discovered by type assertion rather than added to contract.Store,
// matching the other optional store surfaces the loop uses, but a timeline
// worker refuses to start without it - see NewWorker - because unlike
// MatchExists there is no safe default here.
type MatchProvenanceReader interface {
	MatchProvenance(ctx context.Context, matchID string) (contract.MatchRecord, error)
}

// ErrMatchNotFound is contract.ErrMatchNotFound, named here because a timeline
// job is the only place that reads a missing summary as a terminal condition.
//
// The summary archive is append-only and the control plane only gains rows, so
// a match with no summary will not grow one: a timeline that arrives for it is
// an orphan, not a race.
var ErrMatchNotFound = contract.ErrMatchNotFound

// TimelineMetaFromRecord builds the provenance a timeline row is archived with,
// taking the match-scoped fields from the summary's record.
//
// The payload version is overridden to the timeline's own, because the column
// says which transform a reader should apply and the two payloads have
// different shapes. FetchedAt is the timeline's fetch time rather than the
// summary's: the archive's dt= partition is the date the payload was fetched,
// which is the whole reason a late-arriving timeline lands in its own partition
// instead of being appended to a month-old one.
func TimelineMetaFromRecord(rec contract.MatchRecord, region string, fetchedAt time.Time) contract.MatchMeta {
	if region == "" {
		region = rec.Region
	}
	return contract.MatchMeta{
		MatchID:        rec.MatchID,
		Region:         region,
		QueueID:        rec.QueueID,
		Patch:          rec.Patch,
		GameVersion:    rec.GameVersion,
		GameCreation:   rec.GameCreation,
		GameDurationS:  rec.GameDurationS,
		PayloadVersion: raw.TimelinePayloadVersion,
		FetchedAt:      fetchedAt.UTC(),
	}
}

// processTimelineJob fetches one timeline, retains it, and reports that the row
// is ready to be completed.
//
// It is deliberately much smaller than processJob. There is no widening - a
// timeline yields no new players to walk - no control-plane row to insert, and
// no known-match check, because the row being processed *is* the record that
// this timeline was fetched. The only shared machinery is the failure policy,
// which is the point of putting timelines on the same queue: a 404 for an
// aged-out timeline, a 429, a revoked key and a full disk all take the same
// paths they take for a summary.
func (w *Worker) processTimelineJob(ctx context.Context, item contract.QueueItem) (bool, error) {
	jobCtx, cancel := context.WithTimeout(ctx, w.opts.JobTimeout)
	defer cancel()

	started := w.deps.Now()
	reader := w.matchProvenance
	record, err := reader.MatchProvenance(jobCtx, item.MatchID)
	if err != nil {
		if errors.Is(err, ErrMatchNotFound) {
			// An orphan: no summary, so nothing to join the timeline to. It is
			// retired rather than retried, because the summary is not missing
			// transiently - the archive is append-only and the control plane
			// only gains rows.
			w.deps.Log.Warn("timeline job has no summary to join; dead-lettering",
				"match_id", item.MatchID, "job_id", item.ID)
			return false, w.deps.Store.DeadLetterJob(ctx, item.ID, "no-summary")
		}
		if ctx.Err() != nil {
			return false, w.requeueDetached(ctx, item, shortCause(err))
		}
		return false, w.requeueAfter(ctx, item, err, shortCause(err), w.retryDelay(item.Attempts))
	}

	dto, _, err := w.deps.Fetcher.TimelineWithPayload(jobCtx, item.MatchID)
	if err != nil {
		// The shared policy already does the right thing for the case that
		// matters most here: Riot retains a timeline for one year against the
		// summary's two, so a 404 means the match is permanently out of reach
		// and classify() sends it to the dead-letter path rather than spending
		// the rest of the window's budget retrying it.
		return false, w.handleFetchFailure(ctx, item, err)
	}

	meta := TimelineMetaFromRecord(record, w.deps.Region, w.deps.Now())
	if err := w.deps.Writer.WriteTimeline(ctx, dto, meta); err != nil {
		return false, w.handleArchiveFailure(ctx, item, err, archiveCause)
	}
	w.deps.Log.Debug("timeline retained",
		"match_id", item.MatchID,
		"patch", meta.Patch,
		"elapsed", w.deps.Now().Sub(started).String())
	return true, nil
}

// enqueueTimelines puts timeline jobs on the queue for the matches a candidate
// query selected.
//
// The provenance captured by the query is not written to the queue: the row is
// a request for a payload, not a copy of a `matches` row, and duplicating those
// columns onto it would give the timeline archive two disagreeing sources for
// its provenance. The worker reads the summary's record instead.
func enqueueTimelines(ctx context.Context, st contract.Store, candidates []contract.TimelineCandidate, priority int) (int, error) {
	if len(candidates) == 0 {
		return 0, nil
	}
	items := make([]contract.QueueItem, 0, len(candidates))
	for _, c := range candidates {
		items = append(items, contract.QueueItem{
			MatchID:  c.MatchID,
			Kind:     contract.KindTimeline,
			Priority: priority,
		})
	}
	added, err := st.EnqueueMatches(ctx, items)
	if err != nil {
		return 0, fmt.Errorf("enqueue timeline jobs: %w", err)
	}
	return added, nil
}

// PriorityTimeline is the band timeline jobs are enqueued at.
//
// A timeline is a second request for a match the crawl has already stored, so
// it is never more urgent than discovering new matches: it sits behind the
// participant band and in front of an operator's backfill, which is where a
// derived enrichment of known work belongs.
const PriorityTimeline = 250
