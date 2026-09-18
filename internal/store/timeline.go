package store

import (
	"context"
	"fmt"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
)

// candidateStatsSQL summarises the timeline backlog for one selection window.
//
// It counts rather than lists so that a dry run can report what it would spend
// before spending it, and so that "the duration floor removed N games" is a
// number an operator reads rather than a behaviour they have to infer.
//
// done and dead are counted together as AlreadyDone because they mean the same
// thing to a backfill: do not offer this match again. A dead timeline row is a
// 404 from Riot - the timeline retention window is a year and the summary's is
// two - and no later run can change that, so offering it again would spend
// rate-limit budget on a request that cannot succeed.
const candidateStatsSQL = `
WITH eligible AS (
    SELECT m.match_id, m.game_duration_s
    FROM matches m
    WHERE m.region = $1 AND m.queue_id = $2 AND m.game_creation >= $3
), tagged AS (
    SELECT e.game_duration_s,
           (e.game_duration_s < $4 AND $4 > 0) AS is_short,
           q.id IS NOT NULL AS has_job,
           COALESCE(q.status, '') AS job_status
    FROM eligible e
    LEFT JOIN fetch_queue q ON q.match_id = e.match_id AND q.kind = $5
)
SELECT
    count(*)                                                                         AS eligible,
    count(*) FILTER (WHERE is_short)                                                 AS short_excluded,
    count(*) FILTER (WHERE NOT is_short AND has_job AND job_status IN ('pending', 'claimed', 'retry')) AS already_queued,
    count(*) FILTER (WHERE NOT is_short AND has_job AND job_status IN ('done', 'dead'))                AS already_done,
    count(*) FILTER (WHERE NOT is_short AND NOT has_job)                             AS ready
FROM tagged`

// timelineCandidatesSQL selects the matches a backfill should fetch.
//
// The ordering is the load-bearing part. `game_creation DESC` would sample the
// current patch and the champions being played in it, which produces a dataset
// that silently answers every question about two weeks of one patch while
// looking like a random sample of the year. `md5(match_id)` is instead a stable
// pseudo-random order over the whole eligible window: the same match ids always
// select the same sample, whatever order they were crawled in, and raising the
// limit only adds matches rather than replacing them.
//
// The tie-break on match_id exists because md5 collisions are possible in
// principle, and a sample that changes between runs because two rows swapped
// places is not reproducible.
const timelineCandidatesSQL = `
SELECT m.match_id, m.region, m.queue_id, m.patch, m.game_version, m.game_creation, m.game_duration_s
FROM matches m
LEFT JOIN fetch_queue q ON q.match_id = m.match_id AND q.kind = $5
WHERE m.region = $1
  AND m.queue_id = $2
  AND m.game_creation >= $3
  AND q.id IS NULL
  AND ($4 <= 0 OR m.game_duration_s >= $4)
ORDER BY md5(m.match_id), m.match_id
LIMIT $6`

// maxCandidateLimit bounds one backfill pass. The bound is not about database
// cost - the query is a sort over one region's recent matches - but about the
// rate limiter: a limit is a promise about how many Riot calls will be made,
// and a number that exceeds a development key's daily capacity is not a plan.
const maxCandidateLimit = 100000

// defaultSoloQueueID is ranked solo/duo, 420. It is the queue the crawl is
// scoped to and the only one a per-lane, per-minute comparison means anything
// in, so a candidate query with no queue asks for it.
const defaultSoloQueueID = 420

// TimelineCandidates selects matches that could still yield a timeline, with
// the counts that make the selection rule visible.
//
// Only matches the control plane already holds are considered: a timeline for a
// match whose summary was never archived cannot be joined to anything, so
// fetching one would produce an orphan payload and no row.
func (s *Store) TimelineCandidates(ctx context.Context, q contract.TimelineQuery) (contract.TimelineCandidates, error) {
	region := q.Region
	if region == "" {
		region = s.opts.Region
	}
	queueID := q.QueueID
	if queueID == 0 {
		queueID = defaultSoloQueueID
	}
	floor := q.MinDurationS
	if q.IncludeShort {
		floor = 0
	}
	kind := contract.KindTimeline.Stored()

	var stats contract.TimelineCandidates
	err := s.db.QueryRowContext(ctx, candidateStatsSQL, region, queueID, q.Since.UTC(), floor, kind).
		Scan(&stats.Eligible, &stats.ShortExcluded, &stats.AlreadyQueued, &stats.AlreadyDone, &stats.Ready)
	if err != nil {
		return contract.TimelineCandidates{}, fmt.Errorf("store: TimelineCandidates: %w", err)
	}

	limit := q.Limit
	switch {
	case limit <= 0:
		limit = 0
	case limit > maxCandidateLimit:
		limit = maxCandidateLimit
	}
	if limit == 0 {
		return stats, nil
	}

	rows, err := s.db.QueryContext(ctx, timelineCandidatesSQL, region, queueID, q.Since.UTC(), floor, kind, limit)
	if err != nil {
		return contract.TimelineCandidates{}, fmt.Errorf("store: TimelineCandidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var c contract.TimelineCandidate
		if err := rows.Scan(&c.MatchID, &c.Region, &c.QueueID, &c.Patch, &c.GameVersion, &c.GameCreation, &c.GameDurationS); err != nil {
			return contract.TimelineCandidates{}, fmt.Errorf("store: TimelineCandidates: %w", err)
		}
		stats.Matches = append(stats.Matches, c)
	}
	if err := rows.Err(); err != nil {
		return contract.TimelineCandidates{}, fmt.Errorf("store: TimelineCandidates: %w", err)
	}
	return stats, nil
}
