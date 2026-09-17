// Package contract is the frozen Go interface surface between the pipeline's
// components.
//
// It exists as its own package, and not as interfaces sprinkled across the
// packages that implement them, for one reason: three people write this
// pipeline against interfaces they cannot see each other's code for. A single
// package that only declares types means a divergence is a compile error in
// one place instead of a runtime surprise in the middle of a nightly run.
//
// Implementations live in internal/riot, internal/store and internal/raw. The
// metrics surface is obs.MetricsRecorder, which is declared next to its
// Prometheus implementation because every component imports obs anyway.
//
// Changing a signature here is a contract change and requires an ADR. See
// docs/contracts.md.
package contract

import (
	"context"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// MatchStatus mirrors matches.status.
type MatchStatus string

const (
	MatchPending MatchStatus = "pending"
	MatchFetched MatchStatus = "fetched"
	MatchParsed  MatchStatus = "parsed"
	MatchFailed  MatchStatus = "failed"
)

// JobStatus mirrors fetch_queue.status.
type JobStatus string

const (
	JobPending JobStatus = "pending"
	JobClaimed JobStatus = "claimed"
	JobDone    JobStatus = "done"
	JobRetry   JobStatus = "retry"
	JobDead    JobStatus = "dead"
)

// MatchMeta is the provenance the raw archive records next to a payload, and
// the fields the control plane needs to describe it. Everything here is known
// before the payload is parsed, which is what lets the archive be written
// first and the database second: a crash between the two loses no data.
type MatchMeta struct {
	MatchID        string
	Region         string
	QueueID        int
	Patch          string
	GameVersion    string
	GameCreation   time.Time
	GameDurationS  int
	PayloadVersion string
	FetchedAt      time.Time
}

// PartitionDate is the dt= directory component. It is the fetch date rather
// than the game date so that a late-arriving match lands in the partition of
// the run that fetched it, which keeps a partition append-only and complete.
func (m MatchMeta) PartitionDate() string { return m.FetchedAt.UTC().Format(time.DateOnly) }

// LeagueMeta is the provenance for a LEAGUE-V4 seeding response.
type LeagueMeta struct {
	Region    string
	QueueType string
	Tier      string
	Division  string
	FetchedAt time.Time
}

// MatchRecord is one row of the `matches` table: dedupe, provenance and parse
// state. Bulk statistics never live here - they are Parquet, read by DuckDB.
type MatchRecord struct {
	MatchID        string
	Region         string
	QueueID        int
	Patch          string
	GameVersion    string
	GameCreation   time.Time
	GameDurationS  int
	PayloadVersion string
	RawURI         string
	Status         MatchStatus
	FetchedAt      time.Time
	ParsedAt       time.Time
	// Err is the last failure cause, cleared on success. It is a short
	// operator-facing string, not a formatted error chain.
	Err string
}

// QueueItem is one row of `fetch_queue`.
type QueueItem struct {
	ID        int64
	MatchID   string
	Priority  int
	Attempts  int
	NotBefore time.Time
	ClaimedAt time.Time
	Status    JobStatus
}

// FrontierEntry is one row of `crawl_frontier`: a puuid the crawler may walk,
// with the seed rank it was discovered at.
//
// SeedTier and SeedDivision are a snapshot of the ladder position at discovery
// time, not a live property of the player. That limitation is disclosed on the
// site, and it is the reason per-rank pages are gated behind a Phase 0 gate.
type FrontierEntry struct {
	PUUID            string
	Region           string
	SeedTier         string
	SeedDivision     string
	LastSeenAt       time.Time
	LastFetchedAt    time.Time
	ConsecutiveEmpty int
	Priority         int
	Dead             bool
}

// SeedRun is one row of `crawl_seeds`: an audit of a single seeding pass, so a
// thin frontier can be traced back to the ladder page that produced it.
type SeedRun struct {
	ID           int64
	Tier         string
	Division     string
	Queue        string
	Region       string
	StartedAt    time.Time
	FinishedAt   time.Time
	EntriesFound int
}

// BuildRun is the opening row of a `build_runs` entry, written before the
// build starts so that a build killed mid-run is visible as a stuck row rather
// than as nothing at all.
type BuildRun struct {
	ID        int64
	Patch     string
	Region    string
	Queue     int
	Bracket   string
	StartedAt time.Time
	GitSHA    string
}

// BuildResult closes a build_runs row.
type BuildResult struct {
	// Status is "ok", "failed" or "quarantined". A failed build leaves the
	// previous artifacts live: publishing nothing beats publishing garbage.
	Status          string
	FinishedAt      time.Time
	CellsTotal      int
	CellsPublished  int
	CellsSuppressed int
	ArtifactURI     string
	Err             string
}

// SourceToggle is one row of `source_toggles`. Optional sources are off unless
// a row says otherwise, with a name and a review date attached, because the
// compliance checkpoint is "who enabled this and when does it get looked at
// again" rather than a boolean in a config file.
type SourceToggle struct {
	Source      string
	Enabled     bool
	DecidedBy   string
	DecidedAt   time.Time
	ReviewDueAt time.Time
	Notes       string
}

// MatchListQuery parameterises MATCH-V5
// GET /lol/match/v5/matches/by-puuid/{puuid}/ids. A zero StartTime or EndTime
// means "unbounded on that side"; a zero Queue means "any queue".
type MatchListQuery struct {
	PUUID     string
	Count     int
	Start     int
	StartTime time.Time
	EndTime   time.Time
	Queue     int
}

// LeagueQuery parameterises LEAGUE-V4
// GET /lol/league/v4/entries/{queue}/{tier}/{division}.
type LeagueQuery struct {
	// Queue is Riot's queue name, not a numeric id: "RANKED_SOLO_5x5".
	Queue    string
	Tier     string
	Division string
	Page     int
}

// RiotClient is the Riot API surface v1 needs. It is deliberately three
// methods wide.
//
// There is no Timeline method. Timelines are a second request per match for
// data that only the optional skill-order section uses, and v1 ships without
// that section, so adding the method now would buy a rate-limit cost and no
// product. Skill orders appear when timelines are retained, and that is a
// contract change with an ADR.
type RiotClient interface {
	Match(ctx context.Context, matchID string) (riot.MatchDTO, error)
	MatchIDsByPUUID(ctx context.Context, q MatchListQuery) ([]string, error)
	LeagueEntries(ctx context.Context, q LeagueQuery) ([]riot.LeagueEntryDTO, error)
}

// RawWriter appends payloads to the immutable archive. It is append-only by
// construction: there is no update or delete method, because the archive is
// the copy that cannot be re-fetched - Riot retains match history for two
// years and this project intends to outlive that.
type RawWriter interface {
	WriteMatch(ctx context.Context, match riot.MatchDTO, meta MatchMeta) error
	WriteLeagueEntries(ctx context.Context, entries []riot.LeagueEntryDTO, meta LeagueMeta) error
	// Flush finalises the open part files. A writer that is not flushed by
	// the end of a run has written nothing, which is why it is on the
	// interface rather than left to a deferred Close on a concrete type.
	Flush(ctx context.Context) error
}

// Store is the Postgres control plane. Every method takes a context and
// returns an error; none of them returns a partial result silently.
//
// The crawler is required to be safe to run concurrently with itself, so
// ClaimJobs and ClaimFrontier must be implemented with
// `FOR UPDATE SKIP LOCKED` semantics and must never hand the same row to two
// callers.
type Store interface {
	// Matches: dedupe, provenance and parse state.
	UpsertMatch(ctx context.Context, rec MatchRecord) (inserted bool, err error)
	MarkMatchParsed(ctx context.Context, matchID string, parsedAt time.Time) error
	MarkMatchFailed(ctx context.Context, matchID string, cause string) error

	// fetch_queue.
	EnqueueMatches(ctx context.Context, items []QueueItem) (enqueued int, err error)
	ClaimJobs(ctx context.Context, limit int, now time.Time) ([]QueueItem, error)
	CompleteJob(ctx context.Context, id int64) error
	RetryJob(ctx context.Context, id int64, notBefore time.Time, cause string) error
	DeadLetterJob(ctx context.Context, id int64, cause string) error

	// crawl_frontier.
	UpsertFrontier(ctx context.Context, entries []FrontierEntry) (added int, err error)
	ClaimFrontier(ctx context.Context, limit int, now time.Time) ([]FrontierEntry, error)
	MarkFrontierFetched(ctx context.Context, puuid string, at time.Time, empty bool) error
	MarkFrontierDead(ctx context.Context, puuid string, cause string) error
	PruneFrontier(ctx context.Context, before time.Time, maxConsecutiveEmpty int) (int, error)
	FrontierSize(ctx context.Context) (int, error)

	// crawl_seeds.
	StartSeedRun(ctx context.Context, run SeedRun) (int64, error)
	FinishSeedRun(ctx context.Context, id int64, finishedAt time.Time, entriesFound int) error

	// build_runs.
	StartBuildRun(ctx context.Context, run BuildRun) (int64, error)
	FinishBuildRun(ctx context.Context, id int64, result BuildResult) error

	// source_toggles.
	SetSourceToggle(ctx context.Context, toggle SourceToggle) error
	SourceToggles(ctx context.Context) ([]SourceToggle, error)

	// Ping is what the readiness probe and the preflight check use.
	Ping(ctx context.Context) error
}
