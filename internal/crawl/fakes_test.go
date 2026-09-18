package crawl

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// fakeStore is an in-memory control plane. It implements the same guarantees
// the crawler depends on from Postgres - claims are exclusive, upserts dedupe,
// and every state change is guarded by the state it expects - so a worker test
// exercises the real loop rather than mocks of it.
type fakeStore struct {
	mu sync.Mutex

	jobs   map[int64]*fakeJob
	nextID int64

	matches    map[string]contract.MatchRecord
	matchOrder []string

	frontier      map[string]*contract.FrontierEntry
	frontierOrder []string

	seedRuns     map[int64]*contract.SeedRun
	buildRuns    map[int64]*contract.BuildRun
	buildResults map[int64]contract.BuildResult
	toggles      map[string]contract.SourceToggle
	nextRunID    int64

	calls     []string
	unhealthy error

	// failUpsert and failClaim inject control-plane failures; the worker's
	// recovery behaviour is the point of those tests.
	failUpsert   error
	failClaim    error
	failComplete error
	// failExists makes the known-match lookup fail, which is the case where the
	// worker must fall back to fetching rather than treating "unknown" as "no".
	failExists error
	// failProvenance makes the provenance read fail, which is the case where a
	// timeline job must not fetch a payload it cannot archive.
	failProvenance error
}

// fakeJob keeps the queue row state the crawler's transitions depend on.
// claimedAt is what ResetStuckClaims looks at, and attempts is what stops the
// worker from retrying a permanently broken match forever.
type fakeJob struct {
	item      contract.QueueItem
	status    string
	claimedAt time.Time
	notBefore time.Time
	cause     string
}

var (
	_ contract.Store        = (*fakeStore)(nil)
	_ QueueInspector        = (*fakeStore)(nil)
	_ MaintenanceStore      = (*fakeStore)(nil)
	_ FrontierInspector     = (*fakeStore)(nil)
	_ KnownMatchChecker     = (*fakeStore)(nil)
	_ MatchProvenanceReader = (*fakeStore)(nil)
)

func newFakeStore() *fakeStore {
	return &fakeStore{
		jobs:      map[int64]*fakeJob{},
		matches:   map[string]contract.MatchRecord{},
		frontier:  map[string]*contract.FrontierEntry{},
		seedRuns:  map[int64]*contract.SeedRun{},
		buildRuns: map[int64]*contract.BuildRun{},
		toggles:   map[string]contract.SourceToggle{},
	}
}

func (s *fakeStore) log(format string, args ...any) {
	s.calls = append(s.calls, fmt.Sprintf(format, args...))
}

func (s *fakeStore) logCount(prefix string) int {
	n := 0
	for _, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (s *fakeStore) UpsertMatch(_ context.Context, rec contract.MatchRecord) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("upsert-match %s", rec.MatchID)
	if s.failUpsert != nil {
		return false, s.failUpsert
	}
	if _, ok := s.matches[rec.MatchID]; ok {
		return false, nil
	}
	s.matches[rec.MatchID] = rec
	s.matchOrder = append(s.matchOrder, rec.MatchID)
	return true, nil
}

func (s *fakeStore) MarkMatchParsed(_ context.Context, matchID string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("mark-parsed %s", matchID)
	return nil
}

func (s *fakeStore) MarkMatchFailed(_ context.Context, matchID string, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("mark-failed %s", matchID)
	return nil
}

func (s *fakeStore) EnqueueMatches(_ context.Context, items []contract.QueueItem) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	added := 0
	for _, item := range items {
		if item.MatchID == "" {
			continue
		}
		// The kind is part of the row's identity, exactly as the composite
		// unique constraint in migration 0004 makes it: a match and its
		// timeline are two jobs, and a duplicate of one is not a duplicate of
		// the other.
		item.Kind = contract.ParseQueueKind(string(item.Kind))
		s.log("enqueue %s %s", item.Kind, item.MatchID)
		duplicate := false
		for _, job := range s.jobs {
			if job.item.MatchID == item.MatchID && job.item.Kind == item.Kind && job.status != "dead" {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		s.nextID++
		item.ID = s.nextID
		s.jobs[s.nextID] = &fakeJob{
			item:      item,
			status:    "pending",
			notBefore: item.NotBefore,
		}
		added++
	}
	return added, nil
}

// ClaimJobs hands out ready match-summary rows, which is what every caller
// before timelines meant.
func (s *fakeStore) ClaimJobs(ctx context.Context, limit int, now time.Time) ([]contract.QueueItem, error) {
	return s.ClaimJobsOfKind(ctx, contract.KindMatch, limit, now)
}

// ClaimJobsOfKind hands out ready rows of one kind in priority order and flips
// them to claimed, mirroring the SELECT ... FOR UPDATE SKIP LOCKED ... ORDER BY
// priority with its `kind = $4` filter.
func (s *fakeStore) ClaimJobsOfKind(_ context.Context, kind contract.QueueKind, limit int, now time.Time) ([]contract.QueueItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kind = contract.ParseQueueKind(string(kind))
	s.log("claim-jobs %s %d", kind, limit)
	if s.failClaim != nil {
		return nil, s.failClaim
	}
	if limit <= 0 {
		return nil, nil
	}
	ready := make([]int64, 0, len(s.jobs))
	for id, job := range s.jobs {
		if job.item.Kind != kind {
			continue
		}
		if job.status != "pending" && job.status != "retry" {
			continue
		}
		if job.notBefore.After(now) {
			continue
		}
		ready = append(ready, id)
	}
	sortIDs(ready)
	out := make([]contract.QueueItem, 0, limit)
	for _, id := range ready {
		if len(out) == limit {
			break
		}
		job := s.jobs[id]
		job.status = "claimed"
		job.claimedAt = now
		job.item.Attempts++
		out = append(out, job.item)
	}
	return out, nil
}

// TimelineCandidates mirrors the store's selection: served matches only, the
// duration floor, and a job already covering a match taken as done.
func (s *fakeStore) TimelineCandidates(_ context.Context, q contract.TimelineQuery) (contract.TimelineCandidates, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("timeline-candidates %d", q.Limit)

	region := q.Region
	if region == "" {
		region = "EUW1"
	}
	queueID := q.QueueID
	if queueID == 0 {
		queueID = 420
	}
	floor := q.MinDurationS
	if q.IncludeShort {
		floor = 0
	}

	var out contract.TimelineCandidates
	for _, id := range s.matchOrder {
		rec := s.matches[id]
		if rec.Region != region || rec.QueueID != queueID || rec.GameCreation.Before(q.Since) {
			continue
		}
		out.Eligible++
		if floor > 0 && rec.GameDurationS < floor {
			out.ShortExcluded++
			continue
		}
		covered := false
		for _, job := range s.jobs {
			if job.item.MatchID == id && job.item.Kind == contract.KindTimeline {
				covered = true
				if job.status == "pending" || job.status == "claimed" || job.status == "retry" {
					out.AlreadyQueued++
				} else {
					out.AlreadyDone++
				}
				break
			}
		}
		if covered {
			continue
		}
		out.Ready++
		if q.Limit > 0 && len(out.Matches) >= q.Limit {
			continue
		}
		out.Matches = append(out.Matches, contract.TimelineCandidate{
			MatchID:       rec.MatchID,
			Region:        rec.Region,
			QueueID:       rec.QueueID,
			Patch:         rec.Patch,
			GameVersion:   rec.GameVersion,
			GameCreation:  rec.GameCreation,
			GameDurationS: rec.GameDurationS,
		})
	}
	sort.Slice(out.Matches, func(i, j int) bool { return out.Matches[i].MatchID < out.Matches[j].MatchID })
	return out, nil
}

// MatchProvenance returns the archived summary's record, or crawl's terminal
// "no such match" answer when the fake's control plane does not hold one.
func (s *fakeStore) MatchProvenance(_ context.Context, matchID string) (contract.MatchRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("provenance %s", matchID)
	if s.failProvenance != nil {
		return contract.MatchRecord{}, s.failProvenance
	}
	rec, ok := s.matches[matchID]
	if !ok {
		return contract.MatchRecord{}, fmt.Errorf("store: MatchProvenance %s: %w", matchID, ErrMatchNotFound)
	}
	return rec, nil
}

func (s *fakeStore) CompleteJob(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		// The real store refuses a statement on a cancelled context; the fake
		// has to as well, or a shutdown path that forgets to detach looks
		// healthy here and strands rows in production.
		return fmt.Errorf("store: CompleteJob %d: %w", id, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("complete %d", id)
	if s.failComplete != nil {
		return s.failComplete
	}
	job, ok := s.jobs[id]
	if !ok || job.status != "claimed" {
		return nil
	}
	job.status = "done"
	return nil
}

func (s *fakeStore) RetryJob(ctx context.Context, id int64, notBefore time.Time, cause string) error {
	if err := ctx.Err(); err != nil {
		// The real store refuses a statement on a cancelled context; the fake
		// has to as well, or a shutdown path that forgets to detach looks
		// healthy here and strands rows in production.
		return fmt.Errorf("store: RetryJob %d: %w", id, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("retry %d", id)
	job, ok := s.jobs[id]
	if !ok || job.status != "claimed" {
		return nil
	}
	job.status, job.notBefore, job.cause = "retry", notBefore, cause
	return nil
}

func (s *fakeStore) ReleaseJob(ctx context.Context, id int64, notBefore time.Time, cause string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store: ReleaseJob %d: %w", id, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("release %d", id)
	job, ok := s.jobs[id]
	if !ok || job.status != "claimed" {
		return nil
	}
	job.status, job.notBefore, job.cause, job.claimedAt = "retry", notBefore, cause, time.Time{}
	if job.item.Attempts > 0 {
		job.item.Attempts--
	}
	return nil
}

func (s *fakeStore) DeadLetterJob(_ context.Context, id int64, cause string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("dead-letter %d", id)
	job, ok := s.jobs[id]
	if !ok || job.status != "claimed" {
		return nil
	}
	job.status, job.cause = "dead", cause
	return nil
}

func (s *fakeStore) UpsertFrontier(_ context.Context, entries []contract.FrontierEntry) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	added := 0
	for _, entry := range entries {
		if entry.PUUID == "" {
			continue
		}
		existing, ok := s.frontier[entry.PUUID]
		if !ok {
			copyEntry := entry
			s.frontier[entry.PUUID] = &copyEntry
			s.frontierOrder = append(s.frontierOrder, entry.PUUID)
			added++
			continue
		}
		// The real upsert keeps the strongest reason a puuid was queued and
		// the most recent observation of its rank.
		if entry.Priority < existing.Priority {
			existing.Priority = entry.Priority
		}
		if entry.Region != "" {
			existing.Region = entry.Region
		}
		if entry.SeedTier != "" {
			existing.SeedTier = entry.SeedTier
		}
		if entry.SeedDivision != "" {
			existing.SeedDivision = entry.SeedDivision
		}
		if entry.LastSeenAt.After(existing.LastSeenAt) {
			existing.LastSeenAt = entry.LastSeenAt
		}
	}
	return added, nil
}

// ClaimFrontier mirrors the cooldown gate and the claim stamp.
func (s *fakeStore) ClaimFrontier(_ context.Context, limit int, now time.Time) ([]contract.FrontierEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("claim-frontier %d", limit)
	if limit <= 0 {
		return nil, nil
	}
	out := make([]contract.FrontierEntry, 0, limit)
	for _, puuid := range s.frontierOrder {
		if len(out) == limit {
			break
		}
		entry := s.frontier[puuid]
		if entry.Dead {
			continue
		}
		if !entry.LastFetchedAt.IsZero() && entry.LastFetchedAt.Add(s.cooldown()).After(now) {
			continue
		}
		entry.LastFetchedAt = now
		if entry.LastSeenAt.IsZero() {
			entry.LastSeenAt = now
		}
		out = append(out, *entry)
	}
	return out, nil
}

// cooldown is a fixed stand-in: the crawler passes its own cooldown to the
// store in the real implementation, so the fake only has to be self-consistent.
func (s *fakeStore) cooldown() time.Duration { return defaultFrontierCooldownTest }

const defaultFrontierCooldownTest = 6 * time.Hour

func (s *fakeStore) MarkFrontierFetched(_ context.Context, puuid string, at time.Time, empty bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("mark-fetched %s", puuid)
	entry, ok := s.frontier[puuid]
	if !ok {
		return nil
	}
	entry.LastFetchedAt = at
	entry.LastSeenAt = at
	if empty {
		entry.ConsecutiveEmpty++
		return nil
	}
	entry.ConsecutiveEmpty = 0
	return nil
}

func (s *fakeStore) MarkFrontierDead(_ context.Context, puuid string, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("mark-dead %s", puuid)
	if entry, ok := s.frontier[puuid]; ok {
		entry.Dead = true
	}
	return nil
}

// PruneFrontier mirrors pruneFrontierSQL: a row goes when it is dead and stale
// since its last sighting, or when it has been walked maxConsecutiveEmpty times
// producing nothing and has not been seen for the whole window. A row that was
// never seen is kept, because "unknown" is not "stale".
func (s *fakeStore) PruneFrontier(_ context.Context, before time.Time, maxConsecutiveEmpty int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("prune-frontier")
	kept := s.frontierOrder[:0]
	pruned := 0
	for _, puuid := range s.frontierOrder {
		entry := s.frontier[puuid]
		sighting := entry.LastFetchedAt
		if sighting.IsZero() {
			sighting = entry.LastSeenAt
		}
		deadAndStale := entry.Dead && !sighting.IsZero() && sighting.Before(before)
		exhausted := entry.ConsecutiveEmpty >= maxConsecutiveEmpty &&
			!entry.LastSeenAt.IsZero() && entry.LastSeenAt.Before(before)
		if deadAndStale || exhausted {
			delete(s.frontier, puuid)
			pruned++
			continue
		}
		kept = append(kept, puuid)
	}
	s.frontierOrder = kept
	return pruned, nil
}

func (s *fakeStore) FrontierSize(_ context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("frontier-size")
	return s.FrontierSizeLocked(), nil
}

// FrontierSizeLocked is the assertion helper: it reads the same map the store
// methods mutate without taking the mutex twice.
func (s *fakeStore) FrontierSizeLocked() int {
	live := 0
	for _, entry := range s.frontier {
		if !entry.Dead {
			live++
		}
	}
	return live
}

func (s *fakeStore) DeadFrontierSize(_ context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dead := 0
	for _, entry := range s.frontier {
		if entry.Dead {
			dead++
		}
	}
	return dead, nil
}

func (s *fakeStore) StartSeedRun(_ context.Context, run contract.SeedRun) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextRunID++
	run.ID = s.nextRunID
	s.seedRuns[s.nextRunID] = &run
	return s.nextRunID, nil
}

func (s *fakeStore) FinishSeedRun(_ context.Context, id int64, finishedAt time.Time, entriesFound int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("finish-seed-run %d", id)
	if run, ok := s.seedRuns[id]; ok {
		run.FinishedAt = finishedAt
		run.EntriesFound = entriesFound
	}
	return nil
}

func (s *fakeStore) StartBuildRun(_ context.Context, run contract.BuildRun) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextRunID++
	run.ID = s.nextRunID
	s.buildRuns[s.nextRunID] = &run
	return s.nextRunID, nil
}

func (s *fakeStore) FinishBuildRun(_ context.Context, id int64, result contract.BuildResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.buildRuns[id]; ok {
		s.buildResults[id] = result
	}
	return nil
}

func (s *fakeStore) SetSourceToggle(_ context.Context, toggle contract.SourceToggle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toggles[toggle.Source] = toggle
	return nil
}

func (s *fakeStore) SourceToggles(_ context.Context) ([]contract.SourceToggle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contract.SourceToggle, 0, len(s.toggles))
	for _, toggle := range s.toggles {
		out = append(out, toggle)
	}
	return out, nil
}

func (s *fakeStore) Ping(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("ping")
	return s.unhealthy
}

func (s *fakeStore) ResetStuckClaims(_ context.Context, olderThan time.Time, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("reset-stuck %d", limit)
	if limit <= 0 {
		return 0, nil
	}
	reset := 0
	for _, id := range s.jobIDsLocked() {
		if reset == limit {
			break
		}
		job := s.jobs[id]
		if job.status != "claimed" || job.claimedAt.After(olderThan) {
			continue
		}
		job.status = "pending"
		job.claimedAt = time.Time{}
		reset++
	}
	return reset, nil
}

func (s *fakeStore) RecomputeFrontierPriority(_ context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("recompute-priority")
	changed := 0
	for _, puuid := range s.frontierOrder {
		entry := s.frontier[puuid]
		if entry.Dead {
			continue
		}
		switch {
		case entry.SeedTier != "" || entry.SeedDivision != "":
			if entry.Priority != PrioritySeed {
				entry.Priority = PrioritySeed
				changed++
			}
		default:
			if entry.Priority != PriorityPlayer {
				entry.Priority = PriorityPlayer
				changed++
			}
		}
	}
	return changed, nil
}

func (s *fakeStore) ReplayDeadLettered(_ context.Context, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("replay-dead %d", limit)
	if limit <= 0 {
		return 0, nil
	}
	replayed := 0
	for _, id := range s.jobIDsLocked() {
		if replayed == limit {
			break
		}
		job := s.jobs[id]
		if job.status != "dead" {
			continue
		}
		job.status, job.item.Attempts, job.cause, job.claimedAt = "pending", 0, "", time.Time{}
		job.notBefore = job.item.NotBefore
		replayed++
	}
	return replayed, nil
}

func (s *fakeStore) QueueDepths(_ context.Context) (map[contract.JobStatus]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	depths := map[contract.JobStatus]int{}
	for _, job := range s.jobs {
		depths[contract.JobStatus(job.status)]++
	}
	return depths, nil
}

func (s *fakeStore) QueueOldest(_ context.Context) (time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var oldest time.Time
	found := false
	for _, id := range s.jobIDsLocked() {
		job := s.jobs[id]
		if job.status != "pending" {
			continue
		}
		if !found || job.notBefore.Before(oldest) {
			oldest, found = job.notBefore, true
		}
	}
	return oldest, found, nil
}

func (s *fakeStore) MatchExists(_ context.Context, matchID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("match-exists %s", matchID)
	if s.failExists != nil {
		return false, s.failExists
	}
	_, ok := s.matches[matchID]
	return ok, nil
}

func (s *fakeStore) NewestFetchedAt(_ context.Context) (time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var newest time.Time
	found := false
	for _, rec := range s.matches {
		if !found || rec.FetchedAt.After(newest) {
			newest, found = rec.FetchedAt, true
		}
	}
	return newest, found, nil
}

// jobIDsLocked returns queue ids in insertion order.
func (s *fakeStore) jobIDsLocked() []int64 {
	ids := make([]int64, 0, len(s.jobs))
	for id := range s.jobs {
		ids = append(ids, id)
	}
	sortIDs(ids)
	return ids
}

// jobFor returns the queue row for a match id, which is how tests address a
// row without assuming an id.
func (s *fakeStore) jobFor(matchID string) *fakeJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.jobIDsLocked() {
		if s.jobs[id].item.MatchID == matchID {
			return s.jobs[id]
		}
	}
	return nil
}

func (s *fakeStore) notBefore(matchID string) time.Time {
	if job := s.jobFor(matchID); job != nil {
		return job.notBefore
	}
	return time.Time{}
}

func (s *fakeStore) jobStatus(matchID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.jobIDsLocked() {
		if s.jobs[id].item.MatchID == matchID {
			return s.jobs[id].status
		}
	}
	return ""
}

// jobCount is how many rows the queue holds, for a test that wants to claim
// everything it has seeded.
func (s *fakeStore) jobCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.jobs)
}

// deadJobs counts retired rows, which is what "the crawl gave up on this match"
// looks like from the outside.
func (s *fakeStore) deadJobs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	dead := 0
	for _, job := range s.jobs {
		if job.status == "dead" {
			dead++
		}
	}
	return dead
}

func (s *fakeStore) jobCause(matchID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.jobIDsLocked() {
		if s.jobs[id].item.MatchID == matchID {
			return s.jobs[id].cause
		}
	}
	return ""
}

// record returns the control-plane row for a match id.
func (s *fakeStore) record(matchID string) contract.MatchRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.matches[matchID]
}

func (s *fakeStore) matchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.matches)
}

func (s *fakeStore) queueLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.jobs)
}

func (s *fakeStore) frontierEntry(puuid string) contract.FrontierEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.frontier[puuid]; ok {
		return *entry
	}
	return contract.FrontierEntry{}
}

func (s *fakeStore) seedRun(id int64) contract.SeedRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run, ok := s.seedRuns[id]; ok {
		return *run
	}
	return contract.SeedRun{}
}

func sortIDs(ids []int64) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}

// contractOnlyStore hides the optional maintenance surfaces behind the frozen
// interface, which is how the crawler sees a store that predates them.
type contractOnlyStore struct{ contract.Store }

var errTestStoreDown = errors.New("test: control plane unavailable")

// fakeFetcher is the crawl layer's view of Riot. It answers from fixtures and
// records what was asked for, so tests can assert on the traversal rather than
// on HTTP.
type fakeFetcher struct {
	mu sync.Mutex

	dtos   map[string]riot.MatchDTO
	bodies map[string][]byte

	timelines      map[string]riot.TimelineDTO
	timelineBodies map[string][]byte

	history map[string][]string
	pages   map[string][]riot.LeagueEntryDTO
	apexes  map[string][]riot.LeagueEntryDTO
	errors  map[string]error
	calls   []string

	// queries records every history lookup, so a test can assert on the window
	// and paging parameters rather than only on the returned ids.
	queries []riot.MatchListQuery

	key string

	blocked  time.Duration
	advert   bool
	rate     float64
	age      time.Duration
	haveAge  bool
	apexCall int

	// onFetch runs after the call is recorded and before the fixture is
	// answered, so a test can make the world change mid-batch - the D4 case,
	// where a TERM arrives between two rows of the same claim. The hook runs
	// while the fetcher is locked: it must not call back into the fetcher.
	onFetch func(matchID string)
}

var (
	_ Fetcher   = (*fakeFetcher)(nil)
	_ Pacer     = (*fakeFetcher)(nil)
	_ KeySource = (*fakeFetcher)(nil)
)

func newFakeFetcher(key string) *fakeFetcher {
	return &fakeFetcher{
		dtos:           map[string]riot.MatchDTO{},
		bodies:         map[string][]byte{},
		timelines:      map[string]riot.TimelineDTO{},
		timelineBodies: map[string][]byte{},
		history:        map[string][]string{},
		pages:          map[string][]riot.LeagueEntryDTO{},
		apexes:         map[string][]riot.LeagueEntryDTO{},
		errors:         map[string]error{},
		key:            key,
	}
}

func (f *fakeFetcher) record(key string) {
	f.calls = append(f.calls, key)
}

func (f *fakeFetcher) fetchCount(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, call := range f.calls {
		if strings.HasPrefix(call, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeFetcher) allCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// serve registers a match exactly as the client would hand it over: decoded
// value plus the verbatim bytes.
// historyQueries returns the recorded history lookups.
func (f *fakeFetcher) historyQueries() []riot.MatchListQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]riot.MatchListQuery, len(f.queries))
	copy(out, f.queries)
	return out
}

// serveLeague registers one ladder page under the query the crawler builds.
func (f *fakeFetcher) serveLeague(key string, entries []riot.LeagueEntryDTO) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages[key] = entries
}

// serveApex registers one apex league response.
func (f *fakeFetcher) serveApex(key string, entries []riot.LeagueEntryDTO) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.apexes[key] = entries
}

func (f *fakeFetcher) serve(id string, dto riot.MatchDTO, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dtos[id] = dto
	f.bodies[id] = body
}

func (f *fakeFetcher) MatchWithPayload(_ context.Context, matchID string) (riot.MatchDTO, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("match:" + matchID)
	if f.onFetch != nil {
		f.onFetch(matchID)
	}
	if err, ok := f.errors["match:"+matchID]; ok {
		return riot.MatchDTO{}, nil, err
	}
	dto, ok := f.dtos[matchID]
	if !ok {
		return riot.MatchDTO{}, nil, &riot.StatusError{Method: "match", Status: 404}
	}
	return dto, f.bodies[matchID], nil
}

// TimelineWithPayload answers from the served timelines and reports a 404 for a
// timeline the fake does not hold, which is how the tests reach the aged-out
// path: Riot keeps a timeline for one year against the summary's two, so a
// missing timeline is the normal terminal outcome rather than an anomaly.
func (f *fakeFetcher) TimelineWithPayload(_ context.Context, matchID string) (riot.TimelineDTO, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("timeline:" + matchID)
	if err, ok := f.errors["timeline:"+matchID]; ok {
		return riot.TimelineDTO{}, nil, err
	}
	dto, ok := f.timelines[matchID]
	if !ok {
		return riot.TimelineDTO{}, nil, &riot.StatusError{Method: "timeline", Status: 404}
	}
	return dto, f.timelineBodies[matchID], nil
}

func (f *fakeFetcher) MatchIDs(_ context.Context, q riot.MatchListQuery) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("history:" + q.PUUID)
	f.queries = append(f.queries, q)
	if err, ok := f.errors["history:"+q.PUUID]; ok {
		return nil, err
	}
	ids := f.history[q.PUUID]
	if q.Start > 0 {
		if q.Start >= len(ids) {
			return nil, nil
		}
		ids = ids[q.Start:]
	}
	if q.Count > 0 && len(ids) > q.Count {
		ids = ids[:q.Count]
	}
	out := make([]string, len(ids))
	copy(out, ids)
	return out, nil
}

func (f *fakeFetcher) LeagueEntriesWithPayload(_ context.Context, q riot.LeagueQuery) ([]riot.LeagueEntryDTO, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := leagueKey(q)
	f.record("league:" + key)
	if err, ok := f.errors["league:"+key]; ok {
		return nil, nil, err
	}
	entries := f.pages[key]
	body, err := riot.LeagueEntriesPayload(entries)
	if err != nil {
		return nil, nil, err
	}
	return entries, body, nil
}

func (f *fakeFetcher) ApexLeague(_ context.Context, queue, tier string) ([]riot.LeagueEntryDTO, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := queue + "/" + tier
	f.record("apex:" + key)
	f.apexCall++
	if err, ok := f.errors["apex:"+key]; ok {
		return nil, nil, err
	}
	entries := f.apexes[key]
	body, err := riot.LeagueEntriesPayload(entries)
	if err != nil {
		return nil, nil, err
	}
	return entries, body, nil
}

func (f *fakeFetcher) Key() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.key, f.key != ""
}

func (f *fakeFetcher) Blocked() (time.Duration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blocked <= 0 {
		return 0, false
	}
	return f.blocked, true
}

func (f *fakeFetcher) Advertised() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.advert
}

func (f *fakeFetcher) EffectiveRate() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rate
}

func (f *fakeFetcher) Age() (time.Duration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.age, f.haveAge
}

func leagueKey(q riot.LeagueQuery) string {
	return fmt.Sprintf("%s/%s/%s/%d", q.Queue, q.Tier, q.Division, q.Page)
}

// fakeWriter records the raw archive writes. It is the proof that a job only
// completes after the payload is durable: the worker flushes between the two.
type fakeWriter struct {
	mu sync.Mutex

	metas       []contract.MatchMeta
	matchBodies map[string][]byte
	pages       []int
	static      []staticWrite
	flushes     int

	timelineMetas  []contract.MatchMeta
	timelineBodies map[string][]byte

	failMatch    error
	failTimeline error
	failLeague   error
	failFlush    error

	// onWrite runs after a payload has been archived, which is the moment a
	// test can land a shutdown between the archive write and the row close.
	onWrite func(matchID string)
}

type staticWrite struct {
	kind    string
	version string
	locale  string
	payload []byte
	at      time.Time
}

var (
	_ contract.RawWriter = (*fakeWriter)(nil)
	_ StaticWriter       = (*fakeWriter)(nil)
)

func newFakeWriter() *fakeWriter {
	return &fakeWriter{matchBodies: map[string][]byte{}, timelineBodies: map[string][]byte{}}
}

// WriteTimeline records a timeline separately from the summaries, because the
// two counts answer different questions: writeCount() is "how many matches did
// this batch retain", and a timeline write is not one of them.
func (w *fakeWriter) WriteTimeline(_ context.Context, timeline riot.TimelineDTO, meta contract.MatchMeta) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failTimeline != nil {
		return w.failTimeline
	}
	if meta.MatchID == "" {
		meta.MatchID = timeline.Metadata.MatchID
	}
	w.timelineMetas = append(w.timelineMetas, meta)
	w.timelineBodies[meta.MatchID] = timeline.TimelineRawPayload()
	return nil
}

func (w *fakeWriter) WriteMatch(_ context.Context, match riot.MatchDTO, meta contract.MatchMeta) error {
	w.mu.Lock()
	fail := w.failMatch
	if fail == nil {
		if meta.MatchID == "" {
			meta.MatchID = match.Metadata.MatchID
		}
		w.metas = append(w.metas, meta)
		w.matchBodies[meta.MatchID] = match.RawPayload()
	}
	w.mu.Unlock()
	if fail != nil {
		return fail
	}
	if w.onWrite != nil {
		w.onWrite(meta.MatchID)
	}
	return nil
}

func (w *fakeWriter) WriteLeagueEntries(_ context.Context, entries []riot.LeagueEntryDTO, _ contract.LeagueMeta) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failLeague != nil {
		return w.failLeague
	}
	w.pages = append(w.pages, len(entries))
	return nil
}

func (w *fakeWriter) WriteStatic(_ context.Context, kind, version, locale string, payload []byte, at time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	copied := make([]byte, len(payload))
	copy(copied, payload)
	w.static = append(w.static, staticWrite{kind: kind, version: version, locale: locale, payload: copied, at: at})
	return nil
}

func (w *fakeWriter) Flush(ctx context.Context) error {
	// The real writer refuses to touch its parts on a cancelled context, and
	// that refusal is what a graceful stop runs into: the payloads are already
	// buffered, so a flush that fails because of the stop is a flush that has
	// to be retried on a context that outlives it.
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failFlush != nil {
		return w.failFlush
	}
	w.flushes++
	return nil
}

func (w *fakeWriter) Root() string { return "/archive" }

func (w *fakeWriter) writeCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.metas)
}

func (w *fakeWriter) staticKinds() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.static))
	for _, s := range w.static {
		out = append(out, s.kind)
	}
	return out
}

// staticWriteFor returns the retained document of one kind.
func (w *fakeWriter) staticWriteFor(kind string) staticWrite {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, s := range w.static {
		if s.kind == kind {
			return s
		}
	}
	return staticWrite{}
}

func (w *fakeWriter) flushCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushes
}

func (w *fakeWriter) matchIDs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.metas))
	for _, m := range w.metas {
		out = append(out, m.MatchID)
	}
	return out
}

// stopClock cancels the run after a fixed number of sleeps. The crawl loop is
// a poll loop, so without this a Run test never returns.
type stopClock struct {
	*riot.FakeClock

	cancel  context.CancelFunc
	after   int
	sleeps  int
	onSleep func(n int)
}

func newStopClock(start time.Time, after int, cancel context.CancelFunc) *stopClock {
	return &stopClock{FakeClock: riot.NewFakeClock(start), after: after, cancel: cancel}
}

func (c *stopClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.sleeps++
	if c.onSleep != nil {
		c.onSleep(c.sleeps)
	}
	if c.after > 0 && c.sleeps >= c.after {
		c.cancel()
		return context.Canceled
	}
	return c.FakeClock.Sleep(ctx, d)
}

func testBaseTime() time.Time {
	return time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
}

// forceEnqueue bypasses the duplicate check so a test can re-present a key the
// control plane already knows, which is what a re-crawl of an old player looks
// like once the queue row has been consumed.
func (s *fakeStore) forceEnqueue(items ...contract.QueueItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		item.Kind = contract.ParseQueueKind(string(item.Kind))
		s.nextID++
		item.ID = s.nextID
		s.jobs[s.nextID] = &fakeJob{item: item, status: "pending", notBefore: item.NotBefore}
	}
}

// seedFrontier inserts entries directly, for the same reason: the walk tests
// care about what the frontier contains, not about how it was filled.
func (s *fakeStore) seedFrontier(entries ...contract.FrontierEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range entries {
		copied := entry
		s.frontier[entry.PUUID] = &copied
		s.frontierOrder = append(s.frontierOrder, entry.PUUID)
	}
}

// recorder records the metric calls the crawl layer makes, so a test can assert
// that a pipeline that stops moving says so.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

var _ obs.MetricsRecorder = (*recorder)(nil)

func newRecorder() *recorder { return &recorder{} }

func (r *recorder) add(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fmt.Sprintf(format, args...))
}

func (r *recorder) count(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, call := range r.calls {
		if strings.HasPrefix(call, prefix) {
			n++
		}
	}
	return n
}

func (r *recorder) ObserveRiotRequest(method string, status int, seconds float64) {
	r.add("request %s %d", method, status)
}

func (r *recorder) IncRiotRetry(method, reason string) { r.add("retry %s %s", method, reason) }
func (r *recorder) SetRiotKeyAge(seconds float64)      { r.add("key-age %.0f", seconds) }
func (r *recorder) AddQueueClaimed(n int)              { r.add("queue-claimed %d", n) }
func (r *recorder) AddMatchesPersisted(n int)          { r.add("matches-persisted %d", n) }
func (r *recorder) AddRawBytesWritten(n int64)         { r.add("raw-bytes %d", n) }
func (r *recorder) SetFrontierSize(n int)              { r.add("frontier-size %d", n) }
func (r *recorder) SetPipelineStaleness(stage string, seconds float64) {
	r.add("staleness %s %.0f", stage, seconds)
}
func (r *recorder) ObserveBuildDuration(seconds float64) { r.add("build-duration %.0f", seconds) }
func (r *recorder) AddCellsPublished(n int)              { r.add("cells-published %d", n) }
func (r *recorder) AddCellsSuppressed(n int)             { r.add("cells-suppressed %d", n) }
func (r *recorder) IncBuildFailure(stage string)         { r.add("build-failure %s", stage) }
