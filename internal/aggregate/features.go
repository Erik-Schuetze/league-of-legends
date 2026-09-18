package aggregate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The feature dataset build.
//
// It is a second build, and a deliberately independent one. The nightly
// tier-list build publishes agg/v1, a frozen reader contract whose whole point
// is that every cell in it was measured on at least min_cell_n games; the
// feature dataset answers questions about individual matches and individual
// minutes, so it must not be suppressed and must not be folded into that
// contract. Nothing here reads or writes the agg/v1 tree, and a failed run
// leaves the live dataset untouched rather than failing the tier list. See
// docs/decisions/ADR-012-ingest-match-timelines.md.
//
// What it shares with the nightly build is the plumbing and nothing else: the
// engine, the spill-and-batch pattern, the publishing discipline (directories,
// then files, then the manifest last) and the gate style. The two builds read
// different archives - this one reads both the summaries and the timelines -
// and produce different shapes of output.

const (
	// FeatureDatasetDir is the dataset's directory under the dataset root. The
	// name carries the version because the schema is not frozen: a future
	// reshape publishes timeline-v2 beside it rather than reinterpreting
	// timeline-v1 for readers that already have it.
	FeatureDatasetDir = "timeline-v1"

	// DefaultFeatureMinDurationS is the floor below which a game is called too
	// short to hold a timeline worth analysing, and it is the same floor the
	// crawl used to choose the sample. It is an argument to the build rather
	// than a constant in the SQL so that a build can only ever disagree with
	// the crawl if an operator says so.
	DefaultFeatureMinDurationS = 600

	// featureTimelineBatch names the timeline payload spill, and it is the same
	// string the batch files are named with, so a batch file's name says which
	// spill it came from. The summary half needs no constant: it is the only
	// other kind.
	featureTimelineBatch = "timeline"
)

// featureTables lists the dataset's tables in the order they are built, which
// is also the order they are published in. Every one of them is a directory
// holding parquet parts, so a reader globs a table rather than opening a file
// whose name depends on how many batches the build happened to use.
var featureTables = []string{
	"match_index",
	"participant_minutes",
	"events",
	"lane_matchups",
	"participant_early",
	"match_objectives",
}

var (
	// ErrFeatureOrphans reports timeline payloads whose match has no summary
	// row. It is a hard failure rather than a skip because the two archives are
	// written by the same crawler and the same backfill: a timeline without a
	// summary means the archives disagree about a match, and silently dropping
	// it would quietly shrink the dataset by an unknown amount.
	ErrFeatureOrphans = errors.New("timeline archive holds matches with no summary row")

	// ErrFeatureNoEligibleMatches reports a dataset whose every match was
	// excluded. Nothing is published: an empty dataset is not a dataset.
	ErrFeatureNoEligibleMatches = errors.New("no match in the dataset is eligible")

	// ErrFeatureReconciliation reports a per-matchup table whose row count is
	// not one row per participant. It is the gate that catches a mis-join, and
	// a mis-join here would publish CS differences against the wrong opponent.
	ErrFeatureReconciliation = errors.New("lane matchup rows do not reconcile with the participant count")

	// ErrFeatureSchemaDrift reports a built table whose columns are not the
	// columns the schema document describes. The schema is generated from the
	// SQL, so drift means the two were written apart - and a documented column
	// that does not exist is worse than an undocumented one.
	ErrFeatureSchemaDrift = errors.New("built table does not match its schema document")
)

// FeatureOptions is the operator input for one dataset build.
type FeatureOptions struct {
	// DatasetRoot is the dataset parent directory, e.g. ./data/datasets. The
	// dataset itself is written to <DatasetRoot>/timeline-v1.
	DatasetRoot string
	// RawRoot is the raw archive root, the same one the nightly build reads.
	RawRoot string
	// Region, Platform and Queue narrow the dataset. They are empty and zero by
	// default, because the crawl already chose the sample: see
	// featureScopeFilter.
	Region   string
	Platform string
	Queue    int
	// MinDurationS is the floor applied when the ledger explains an exclusion.
	// Zero means DefaultFeatureMinDurationS.
	MinDurationS int
	// DuckDBBin overrides the pinned DuckDB binary; empty searches the usual
	// places.
	DuckDBBin string
	// AllowVersionMismatch continues on a DuckDB release other than the pinned
	// one. It is a development escape hatch, as in the nightly build.
	AllowVersionMismatch bool
	// DuckDB carries the engine's memory and temp settings. The zero value
	// resolves to engine defaults - 1 GiB and one temp directory - and it is
	// low on purpose: this extraction unnests two arrays out of payloads ten
	// times the size the nightly build handles.
	DuckDB DuckDBSettings
	// Engine, when set, is used instead of spawning the pinned binary. It is
	// how the tests drive the build without a DuckDB client.
	Engine Engine
	// GitSHA is the revision the build ran from. It is recorded, never
	// validated: a dataset is reproducible from its inputs, not from its
	// checkout.
	GitSHA string
	Now    func() time.Time
	Log    *slog.Logger
}

// FeatureResult is what one dataset build produced.
//
// It is returned even when the build fails - with Dir and StagingDir naming the
// trees involved and Counts holding whatever was counted before the failure -
// because the counts are the operator's message and they are not recoverable
// from the error alone.
type FeatureResult struct {
	Dir        string
	StagingDir string
	Manifest   FeatureManifest
	Counts     FeatureCounts
	Tables     []FeatureTableRows
	Published  PublishResult
}

// FeatureManifest is the dataset's build receipt.
//
// It is explicitly not a reader contract, and it says so in its own notice,
// because the temptation to treat a file called manifest.json as one is exactly
// the mistake the agg/v1 contract exists to avoid. It records what this run
// read, what it excluded and what it wrote; nothing about its keys is promised
// to a future reader, and schema.json is where the columns are described.
type FeatureManifest struct {
	Dataset       string             `json:"dataset"`
	GeneratedAt   string             `json:"generated_at"`
	GitSHA        string             `json:"git_sha,omitempty"`
	DuckDBVersion string             `json:"duckdb_version"`
	Sources       []string           `json:"sources"`
	Scope         FeatureScope       `json:"scope"`
	MinDurationS  int                `json:"min_duration_s"`
	BatchParts    int                `json:"batch_parts"`
	RawParts      int                `json:"raw_parts"`
	TimelineParts int                `json:"timeline_parts"`
	Matches       int                `json:"matches"`
	Eligible      int                `json:"eligible"`
	Excluded      map[string]int     `json:"excluded"`
	Tables        []FeatureTableRows `json:"tables"`
	Notice        string             `json:"notice"`
}

// FeatureScope records the narrowing an operator asked for, so a dataset that
// looks small can be explained without reading the invocation.
type FeatureScope struct {
	Region   string `json:"region,omitempty"`
	Platform string `json:"platform,omitempty"`
	Queue    int    `json:"queue,omitempty"`
}

// FeatureTableRows is one row of the manifest's table list.
type FeatureTableRows struct {
	Name string `json:"name"`
	Rows int    `json:"rows"`
}

// FeatureCounts is what the ledger and the tables added up to.
type FeatureCounts struct {
	ArchiveRows     int            `json:"archive_rows"`
	TimelineRows    int            `json:"timeline_rows"`
	Matches         int            `json:"matches"`
	TimelinePresent int            `json:"timeline_present"`
	Eligible        int            `json:"eligible"`
	Excluded        map[string]int `json:"excluded"`
	Malformed       int            `json:"malformed_payloads"`
	Orphans         int            `json:"orphans"`
	TableRows       map[string]int `json:"table_rows"`
}

// featureNotice is written into every manifest.
const featureNotice = "This file is a build receipt, not a reader contract. It records what one " +
	"run read, excluded and wrote, and nothing in it is promised to a future reader. The dataset's " +
	"columns are described in schema.json and its caveats and query recipes are in README.md."

// featureState carries one build.
type featureState struct {
	opts        FeatureOptions
	generatedAt time.Time
	staging     string
	scratch     string
	datasetDir  string

	engine        Engine
	summaryParts  []string
	timelineParts []string

	summaryEnvelope    string
	timelineEnvelope   string
	summaryPayloadDir  string
	summaryPayload     string
	timelinePayloadDir string
	timelinePayload    string
	framesDir          string
	framesPath         string
	eventsDir          string
	eventsPath         string
	actorsPath         string
	killPointsPath     string
	checkpointsPath    string
	participantsPath   string
	matchesPath        string

	counts   FeatureCounts
	tables   []FeatureTableRows
	manifest FeatureManifest
}

// featureScratchPaths resolves every spill path from the scratch directory.
//
// They are files rather than sub-queries because the engine runs one process
// per statement: nothing survives between two statements except what one of
// them wrote to disk. See the header of features_sql.go.
func (s *featureState) path(name string) string { return filepath.Join(s.scratch, name) }

// Features builds the dataset and publishes it.
//
// The order is the nightly build's order, for the nightly build's reason: plan,
// stage, spill, build the tables, gate, document, then publish with the manifest
// last. Every failure before the publish leaves the live dataset exactly as it
// was, and staging is removed on every path out.
func Features(ctx context.Context, opts FeatureOptions) (result FeatureResult, err error) {
	startedAt := time.Now()
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MinDurationS == 0 {
		opts.MinDurationS = DefaultFeatureMinDurationS
	}
	if opts.MinDurationS < 0 {
		return result, fmt.Errorf("min_duration_s must not be negative, got %d", opts.MinDurationS)
	}
	if opts.DatasetRoot == "" || opts.RawRoot == "" {
		return result, errors.New("dataset root and raw root are both required")
	}
	// The same guard the nightly build applies to its own root, for the same
	// reason: the demo subcommand writes a dataset-shaped tree too, and a real
	// build must not publish measured numbers into a tree that announces
	// simulated ones.
	if err := refuseDemoTree(opts.DatasetRoot); err != nil {
		return result, err
	}
	generatedAt := opts.Now().UTC()
	if err := os.MkdirAll(opts.DatasetRoot, publishedDirPerm); err != nil { //nolint:gosec // G301: a published tree; see perms.go.
		return result, fmt.Errorf("create dataset root: %w", err)
	}
	staging := filepath.Join(opts.DatasetRoot, fmt.Sprintf("%s%d-%d", stagingPrefix, os.Getpid(), generatedAt.UnixNano()))
	// Private, like every other staging tree: only this process reads a
	// half-built dataset. See perms.go.
	if err := os.MkdirAll(staging, privateDirPerm); err != nil {
		return result, fmt.Errorf("create staging directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(staging); removeErr != nil {
			opts.Log.Warn("could not remove staging directory", "path", staging, "error", removeErr)
		}
	}()

	result = FeatureResult{StagingDir: staging, Dir: filepath.Join(opts.DatasetRoot, FeatureDatasetDir)}
	state, err := newFeatureState(ctx, opts, generatedAt, staging)
	if err != nil {
		return result, err
	}
	defer func() {
		opts.Log.Info("feature dataset build finished", "duration", time.Since(startedAt).Round(time.Millisecond).String(), "error", err)
	}()
	if err := state.run(ctx); err != nil {
		result.Counts = state.counts
		result.Tables = state.tables
		return result, err
	}
	if err := state.publish(&result); err != nil {
		result.Counts = state.counts
		result.Tables = state.tables
		return result, err
	}
	result.Manifest = state.manifest
	result.Counts = state.counts
	result.Tables = state.tables
	return result, nil
}

// newFeatureState plans both archives and lays out the scratch tree.
func newFeatureState(ctx context.Context, opts FeatureOptions, generatedAt time.Time, staging string) (*featureState, error) {
	summary, err := RawArchive{Root: opts.RawRoot}.Parts()
	if err != nil {
		return nil, err
	}
	// The timeline archive is not merely an extra input: it is the input. Its
	// absence is ErrNoTimelineArchive and not ErrArchiveEmpty, because the two
	// tell an operator opposite things - a missing summary archive is a crawler
	// that never ran, a missing timeline archive is a backfill that never did.
	timeline, err := RawArchive{Root: opts.RawRoot, Source: RawSourceTimeline}.Parts()
	if err != nil {
		return nil, err
	}
	opts.Log.Info("raw archives planned",
		"summary_parts", len(summary), "timeline_parts", len(timeline),
		"region", opts.Region, "platform", opts.Platform, "queue", opts.Queue)

	scratch := filepath.Join(staging, ".scratch")
	stagedSummary, err := RawArchive{Root: opts.RawRoot}.StageParts(ctx, summary, filepath.Join(scratch, "raw-summary"))
	if err != nil {
		return nil, err
	}
	stagedTimeline, err := RawArchive{Root: opts.RawRoot, Source: RawSourceTimeline}.
		StageParts(ctx, timeline, filepath.Join(scratch, "raw-timeline"))
	if err != nil {
		return nil, err
	}
	// DuckDB will not create a COPY target's directory, and a glob over an
	// empty directory is an error rather than an empty relation, so every
	// spill directory is created up front - the same constraint the nightly
	// extraction documents.
	summaryPayloadDir, err := makeSpillDir(scratch, "summary-payload")
	if err != nil {
		return nil, err
	}
	timelinePayloadDir, err := makeSpillDir(scratch, "timeline-payload")
	if err != nil {
		return nil, err
	}
	framesDir, err := makeSpillDir(scratch, "frames")
	if err != nil {
		return nil, err
	}
	eventsDir, err := makeSpillDir(scratch, "events")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(staging, FeatureDatasetDir), privateDirPerm); err != nil {
		return nil, fmt.Errorf("create staged dataset: %w", err)
	}
	// The table directories are created served rather than private, unlike the
	// scratch below them: their mode is what a reader gets after the rename,
	// and a dataset nobody but the builder can open is not a dataset. They are
	// unreachable while staged - the staging root above them is private.
	for _, table := range featureTables {
		if err := os.MkdirAll(filepath.Join(staging, FeatureDatasetDir, table), publishedDirPerm); err != nil { //nolint:gosec // G301: a published tree; see perms.go.
			return nil, fmt.Errorf("create staged table %s: %w", table, err)
		}
	}
	return &featureState{
		opts:               opts,
		generatedAt:        generatedAt,
		staging:            staging,
		scratch:            scratch,
		datasetDir:         filepath.Join(staging, FeatureDatasetDir),
		summaryParts:       stagedSummary,
		timelineParts:      stagedTimeline,
		summaryEnvelope:    filepath.Join(scratch, "summary-envelope.parquet"),
		timelineEnvelope:   filepath.Join(scratch, "timeline-envelope.parquet"),
		summaryPayloadDir:  summaryPayloadDir,
		summaryPayload:     filepath.Join(summaryPayloadDir, "*.parquet"),
		timelinePayloadDir: timelinePayloadDir,
		timelinePayload:    filepath.Join(timelinePayloadDir, "*.parquet"),
		framesDir:          framesDir,
		framesPath:         filepath.Join(framesDir, "*.parquet"),
		eventsDir:          eventsDir,
		eventsPath:         filepath.Join(eventsDir, "*.parquet"),
		participantsPath:   filepath.Join(scratch, "participants.parquet"),
		actorsPath:         filepath.Join(scratch, "event-actors.parquet"),
		killPointsPath:     filepath.Join(scratch, "kill-points.parquet"),
		checkpointsPath:    filepath.Join(scratch, "checkpoints.parquet"),
		matchesPath:        filepath.Join(scratch, "matches.parquet"),
		counts: FeatureCounts{
			Excluded:  map[string]int{},
			TableRows: map[string]int{},
		},
	}, nil
}

// tablePath is the staged path of one table's directory.
func (s *featureState) tablePath(table string) string {
	return filepath.Join(s.datasetDir, table)
}

// tableGlob is the path handed to DuckDB to read a table back.
func (s *featureState) tableGlob(table string) string {
	return filepath.Join(s.tablePath(table), "*.parquet")
}

// featureRowCount is one row of a table's row count, read back from a spilled
// JSON file the way every other count in this build is.
type featureRowCount struct {
	Table string `json:"table"`
	Rows  int    `json:"rows"`
}

// featureLedgerCounts is the ledger's arithmetic, read back after it is written.
type featureLedgerCounts struct {
	SummaryRows       int `json:"summary_rows"`
	TimelineRows      int `json:"timeline_rows"`
	Matches           int `json:"matches"`
	TimelinePresent   int `json:"timeline_present"`
	Eligible          int `json:"eligible"`
	MalformedSummary  int `json:"malformed_summary"`
	MalformedTimeline int `json:"malformed_timeline"`
	Orphans           int `json:"orphans"`
}

// featureExclusion is one row of the exclusion breakdown.
type featureExclusion struct {
	Reason string `json:"reason"`
	Rows   int    `json:"rows"`
}

// tablePart is the single part file of one table.
//
// One part per table, not one per batch: a derived table is a streaming
// relation and the batching that the payload statements need would only add
// files here. The reader globs the directory either way, so the part count is
// an implementation detail of the build - which is why the tables are
// directories rather than files with names a reader would have to know.
func (s *featureState) tablePart(table string) string {
	return filepath.Join(s.tablePath(table), "part-00000.parquet")
}

// writeTables writes the five tables that are derived from the ledger and the
// two unnestings.
//
// The order is the dependency order: the kill points come from the events, the
// minutes need the kill points, the checkpoints need the minutes, and the three
// per-participant and per-match tables need the checkpoints. Each statement
// reads the spill the previous one wrote, which is why they are separate
// statements at all - the engine runs one process per statement and keeps
// nothing in memory between them.
func (s *featureState) writeTables(ctx context.Context) error {
	ledger := s.tableGlob("match_index")
	return s.engine.Exec(ctx, joinStatements(
		copyParquet(killPointsSQL(s.eventsPath), s.killPointsPath),
		copyParquet(participantMinutesSQL(ledger, s.participantsPath, s.framesPath, s.killPointsPath),
			s.tablePart("participant_minutes")),
		copyParquet(checkpointPicksSQL(s.tableGlob("participant_minutes")), s.checkpointsPath),
		copyParquet(eventsTableSQL(ledger, s.eventsPath), s.tablePart("events")),
		copyParquet(eventActorsSQL(s.tableGlob("events")), s.actorsPath),
		copyParquet(laneMatchupsSQL(ledger, s.participantsPath, s.checkpointsPath),
			s.tablePart("lane_matchups")),
		copyParquet(participantEarlySQL(ledger, s.participantsPath, s.actorsPath, s.checkpointsPath),
			s.tablePart("participant_early")),
		copyParquet(matchObjectivesSQL(ledger, s.tableGlob("events"), s.participantsPath),
			s.tablePart("match_objectives")),
	))
}

// countRows reads back what the build wrote, for the manifest, the gates and
// the operator's log line.
func (s *featureState) countRows(ctx context.Context) error {
	rowCount := func(table string) string {
		return fmt.Sprintf("SELECT %s AS table, CAST(count(*) AS BIGINT) AS rows FROM %s",
			quoteLiteral(table), parquetOf(s.tableGlob(table)))
	}
	var statements []string
	for _, table := range featureTables {
		statements = append(statements, copyJSON(rowCount(table), s.path("rows-"+table+".json")))
	}
	statements = append(statements,
		copyJSON(featureLedgerCountsSQL(s.summaryEnvelope, s.timelineEnvelope, s.tableGlob("match_index")),
			s.path("ledger-counts.json")),
		copyJSON(featureExclusionsSQL(s.tableGlob("match_index")), s.path("exclusions.json")))
	if err := s.engine.Exec(ctx, joinStatements(statements...)); err != nil {
		return err
	}
	ledger, err := readJSONArray[featureLedgerCounts](s.path("ledger-counts.json"))
	if err != nil {
		return err
	}
	if len(ledger) != 1 {
		return fmt.Errorf("ledger counts: expected one row, got %d", len(ledger))
	}
	counts := ledger[0]
	s.counts.ArchiveRows = counts.SummaryRows
	s.counts.TimelineRows = counts.TimelineRows
	s.counts.Matches = counts.Matches
	s.counts.TimelinePresent = counts.TimelinePresent
	s.counts.Eligible = counts.Eligible
	s.counts.Malformed = counts.MalformedSummary + counts.MalformedTimeline
	s.counts.Orphans = counts.Orphans

	exclusions, err := readJSONArray[featureExclusion](s.path("exclusions.json"))
	if err != nil {
		return err
	}
	excluded := map[string]int{}
	for _, exclusion := range exclusions {
		excluded[exclusion.Reason] = exclusion.Rows
	}
	s.counts.Excluded = excluded

	tables := make([]FeatureTableRows, 0, len(featureTables))
	for _, table := range featureTables {
		rows, err := readJSONArray[featureRowCount](s.path("rows-" + table + ".json"))
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return fmt.Errorf("row count for %s: expected one row, got %d", table, len(rows))
		}
		s.counts.TableRows[table] = rows[0].Rows
		tables = append(tables, FeatureTableRows{Name: table, Rows: rows[0].Rows})
	}
	s.tables = tables
	s.opts.Log.Info("feature dataset counted",
		"matches", counts.Matches, "timeline_present", counts.TimelinePresent,
		"eligible", counts.Eligible, "excluded", len(excluded))
	return nil
}

// featureLedgerCountsSQL is the single statement behind every gate count.
//
// One statement rather than six, because the six figures have to describe one
// moment of one dataset: reading coverage, orphans and malformed payloads in
// three separate passes over a building tree is how a gate ends up comparing
// numbers that were never true together.
func featureLedgerCountsSQL(summaryEnvelope, timelineEnvelope, ledger string) string {
	summary, timeline := parquetOf(summaryEnvelope), parquetOf(timelineEnvelope)
	index := parquetOf(ledger)
	return fmt.Sprintf(`SELECT
  CAST((SELECT count(*) FROM %s) AS BIGINT) AS summary_rows,
  CAST((SELECT count(*) FROM %s) AS BIGINT) AS timeline_rows,
  CAST((SELECT count(*) FROM %s) AS BIGINT) AS matches,
  CAST((SELECT count(*) FROM %s WHERE timeline_present) AS BIGINT) AS timeline_present,
  CAST((SELECT count(*) FROM %s WHERE timeline_eligible) AS BIGINT) AS eligible,
  CAST((SELECT count(*) FROM %s WHERE NOT payload_valid OR match_id IS NULL) AS BIGINT) AS malformed_summary,
  CAST((SELECT count(*) FROM %s WHERE NOT payload_valid OR match_id IS NULL) AS BIGINT) AS malformed_timeline,
  CAST((SELECT count(*) FROM %s t
        LEFT JOIN %s s ON s.match_id = t.match_id
        WHERE s.match_id IS NULL) AS BIGINT) AS orphans`,
		summary, timeline, index, index, index, summary, timeline, timeline, summary)
}

// featureExclusionsSQL is the ledger's exclusion breakdown.
//
// NULL is grouped as its own reason rather than dropped: a match whose
// exclusion_reason is NULL means the ledger has a row it cannot explain, and
// the quality gate has to be able to see that instead of a count that quietly
// leaves it out.
func featureExclusionsSQL(ledger string) string {
	return fmt.Sprintf(`SELECT COALESCE(exclusion_reason, 'unexplained') AS reason, CAST(count(*) AS BIGINT) AS rows
FROM %s
GROUP BY 1
ORDER BY 1`, parquetOf(ledger))
}

// run is the build, in the order the dependencies require.
//
// The two envelopes come first because the ledger is built from them and
// because a corrupt payload has to become a countable row before anything tries
// to read a field out of it. The ledger is written before the payload batches
// on purpose: coverage is worth knowing even if the expensive half of the build
// is the half that fails, and an operator who sees "1,000 matches, 12 excluded
// as too short" beside a failure knows what to fix.
func (s *featureState) run(ctx context.Context) error {
	engine := s.opts.Engine
	if engine == nil {
		cli, err := OpenCLIEngine(ctx, s.opts.DuckDBBin, s.opts.AllowVersionMismatch, s.opts.DuckDB, s.opts.Log)
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()
		engine = cli
	}
	s.engine = engine
	version, err := engine.Version(ctx)
	if err != nil {
		return err
	}
	s.manifest.DuckDBVersion = version

	if err := s.spillEnvelopes(ctx); err != nil {
		return err
	}
	if err := s.writeLedger(ctx); err != nil {
		return err
	}
	if err := s.spillPayloads(ctx); err != nil {
		return err
	}
	if err := s.selectParticipants(ctx); err != nil {
		return err
	}
	if err := s.spillFrames(ctx); err != nil {
		return err
	}
	if err := s.spillEvents(ctx); err != nil {
		return err
	}
	if err := s.writeTables(ctx); err != nil {
		return err
	}
	if err := s.countRows(ctx); err != nil {
		return err
	}
	if err := s.checkGates(ctx); err != nil {
		return err
	}
	if err := s.writeDocs(ctx); err != nil {
		return err
	}
	return s.writeManifest()
}

// scope is the optional narrowing, rendered once.
func (s *featureState) scope(prefix ...string) string {
	alias := ""
	if len(prefix) > 0 {
		alias = prefix[0]
	}
	return featureScopeFilter(alias, s.opts.Region, s.opts.Platform, s.opts.Queue)
}

// spillEnvelopes reads the per-match keys of both archives without carrying a
// payload.
//
// It is the same two-statement split the nightly build documents and for the
// same measured reason: one statement that projects fields out of a payload and
// also carries that payload exceeds the engine's memory budget, and one that
// projects fields while dropping the payload does not.
func (s *featureState) spillEnvelopes(ctx context.Context) error {
	return s.engine.Exec(ctx, joinStatements(
		copyParquet(summaryEnvelopeSQL(s.summaryParts), s.summaryEnvelope),
		copyParquet(timelineEnvelopeSQL(s.timelineParts), s.timelineEnvelope),
	))
}

// writeLedger writes match_index, the eligibility ledger, from the two
// envelopes alone.
func (s *featureState) writeLedger(ctx context.Context) error {
	return s.engine.Exec(ctx, copyParquet(
		matchIndexSQL(s.summaryEnvelope, s.timelineEnvelope, s.scope(), s.opts.MinDurationS),
		filepath.Join(s.tablePath("match_index"), "part-00000.parquet")))
}

// spillPayloads carries both archives' payloads, one batch of raw parts at a
// time.
//
// This is the statement whose batch size is a memory decision rather than a
// style choice: a timeline is roughly ten times a match summary, and the
// batch size of the nightly extraction was measured against summaries. The
// batches are appended to their own spill directories, so the boundary is
// invisible to everything downstream - the glob below reads one relation.
func (s *featureState) spillPayloads(ctx context.Context) error {
	if err := s.spillInBatches(ctx, "summary", s.summaryParts, s.summaryPayloadDir,
		func(envelopePath string, parts []string) string {
			return carriedPayloadSQL(envelopePath, parts, s.scope("e"),
				"match_id", "region", "patch", "game_version", "queue_id",
				"game_creation_ms", "game_duration_s", "platform_id", "payload_valid")
		}); err != nil {
		return err
	}
	return s.spillInBatches(ctx, featureTimelineBatch, s.timelineParts, s.timelinePayloadDir,
		func(envelopePath string, parts []string) string {
			return carriedPayloadSQL(envelopePath, parts, s.scope("e"),
				"match_id", "region", "patch", "game_version", "queue_id",
				"game_creation_ms", "game_duration_s", "frame_interval_ms", "frame_count", "payload_valid")
		})
}

// spillInBatches runs one payload-carrying statement per batch of parts.
//
// The batch size is featureBatchParts and it is deliberately not derived: the
// nightly extraction's value was measured on a payload class ten times smaller,
// and inheriting it would be inheriting an estimate.
func (s *featureState) spillInBatches(ctx context.Context, kind string, parts []string, dir string, statement func(string, []string) string) error {
	envelope := s.summaryEnvelope
	if kind == featureTimelineBatch {
		envelope = s.timelineEnvelope
	}
	batches := 0
	for i := 0; i*featureBatchParts < len(parts); i++ {
		batch := parts[i*featureBatchParts:]
		if len(batch) > featureBatchParts {
			batch = batch[:featureBatchParts]
		}
		target := filepath.Join(dir, batchFileName(kind, i))
		if err := s.engine.Exec(ctx, copyParquet(statement(envelope, batch), target)); err != nil {
			return err
		}
		batches++
	}
	s.opts.Log.Info("payload batches spilled",
		"kind", kind, "parts", len(parts), "batches", batches, "parts_per_batch", featureBatchParts)
	return nil
}

// selectParticipants unnests the summary payload's participant array, which is
// the identity half of every per-participant feature.
func (s *featureState) selectParticipants(ctx context.Context) error {
	return s.engine.Exec(ctx, copyParquet(summaryParticipantSQL(s.summaryPayload), s.participantsPath))
}

// spillFrames unnests frames[].participantFrames into one row per
// (match, participant, frame), batched the way the payload batches are.
func (s *featureState) spillFrames(ctx context.Context) error {
	return s.spillUnnest(ctx, "frames", s.framesDir, timelineFramesSQL)
}

// spillEvents unnests frames[].events into the wide event table.
//
// Batching it per payload batch is safe for the two window functions it applies
// - event_index and is_duplicate_skill_level_up both partition by match_id -
// because a match's payload is one row of one raw part, so no match can ever
// straddle two batches. That is a property of the batching, not a coincidence,
// and it is why the batch boundary is by part and never by row.
func (s *featureState) spillEvents(ctx context.Context) error {
	return s.spillUnnest(ctx, "events", s.eventsDir, timelineEventsSQL)
}

// payloadBatch is the path of one payload spill batch.
func (s *featureState) payloadBatch(kind string, i int) string {
	dir := s.summaryPayloadDir
	if kind == featureTimelineBatch {
		dir = s.timelinePayloadDir
	}
	return filepath.Join(dir, batchFileName(kind, i))
}

// spillUnnest runs one unnesting statement per timeline payload batch.
//
// Both unnesting statements read the same payload spill and unfold a different
// array out of it. They are separate statements rather than two branches of one
// because unfolding both arrays in a single statement multiplies them - every
// event of a frame carried beside every participant frame of that frame - and
// the multiplication is what the engine runs out of memory on.
func (s *featureState) spillUnnest(ctx context.Context, kind, dir string, statement func(string) string) error {
	batches := 0
	for i := 0; i*featureBatchParts < len(s.timelineParts); i++ {
		target := filepath.Join(dir, batchFileName(kind, i))
		if err := s.engine.Exec(ctx, copyParquet(statement(s.payloadBatch(featureTimelineBatch, i)), target)); err != nil {
			return err
		}
		batches++
	}
	s.opts.Log.Info("unnested batches spilled", "kind", kind, "batches", batches)
	return nil
}

// featureReconciliationSQL counts the rows a mis-join would change.
//
// The per-matchup table is the one table whose correctness is not visible in
// its own contents: a pairing that matched the wrong opponent produces a
// complete, plausible table whose cs_diff column is simply wrong. Two figures
// catch it - the row count must be exactly ten per match, and every match the
// ledger says has a timeline must appear - and neither can be inferred from the
// table alone, so they are read beside the ledger.
//
// The ledger side of that comparison is deliberately restricted to eligible
// matches, because that is what the matchup table reads. A match the ledger
// graded no_timeline or aborted is a legitimate ledger row, not a missing
// matchup, so counting it here would turn the ordinary case of an unbackfilled
// match into a gate failure on a correct build. The two predicates have to agree
// exactly for the count to mean anything, so they are written to agree.
func featureReconciliationSQL(ledger, matchups string) string {
	return fmt.Sprintf(`SELECT
  CAST((SELECT count(*) FROM %s) AS BIGINT) AS matchup_rows,
  CAST((SELECT count(DISTINCT match_id) FROM %s) AS BIGINT) AS matchup_matches,
  CAST((SELECT count(DISTINCT match_id) FROM %s WHERE timeline_eligible) AS BIGINT) AS ledger_matches`,
		parquetOf(matchups), parquetOf(matchups), parquetOf(ledger))
}

// featureReconciliation is the row read back from the query above.
type featureReconciliation struct {
	MatchupRows    int `json:"matchup_rows"`
	MatchupMatches int `json:"matchup_matches"`
	LedgerMatches  int `json:"ledger_matches"`
}

// checkGates reads the reconciliation figures and refuses a dataset that is not
// internally consistent.
//
// Everything here fails before the publish, so a refused build changes nothing
// a reader can see. The order is the order of the question an operator asks:
// is there anything at all to publish, do the two archives agree about which
// matches exist, does the matchup table pair every participant exactly once,
// and does every table carry the columns it claims to.
func (s *featureState) checkGates(ctx context.Context) error {
	// An archive row the envelope cannot identify is refused before anything
	// else, and it is refused rather than graded because it is not a property
	// of a match: a payload that is valid JSON and carries no match id cannot
	// be attributed to a game at all, and a payload that is not valid JSON was
	// truncated on the way to disk. The nightly build fails closed on the same
	// input for the same reason - a row no reader can reject is a row only a
	// gate can catch - and counting it here, over the unscoped envelope, is
	// what makes the count the whole archive's rather than the scope's.
	if s.counts.Malformed > 0 {
		return fmt.Errorf("%w: %d archive rows carry no readable envelope",
			ErrMalformedArchive, s.counts.Malformed)
	}
	// The ledger must explain every row it holds. A NULL reason is a match the
	// build classified into no category, which means the ledger's own CASE grew
	// a hole; publishing it would hide the hole rather than show it.
	if unexplained := s.counts.Excluded["unexplained"]; unexplained > 0 {
		return fmt.Errorf("%w: %d ledger rows have no exclusion reason", ErrMalformedArchive, unexplained)
	}
	// The two archives are written by the same crawler and the same backfill,
	// so a timeline without a summary means they disagree about a match.
	if s.counts.Orphans > 0 {
		return fmt.Errorf("%w: %d timeline payloads have no summary row", ErrFeatureOrphans, s.counts.Orphans)
	}
	if s.counts.Matches == 0 {
		return fmt.Errorf("%w: the summary archive holds no matches in scope", ErrEmptyWindow)
	}
	if s.counts.TimelinePresent == 0 {
		return fmt.Errorf("%w: no match in scope has a timeline", ErrFeatureNoEligibleMatches)
	}
	if err := s.engine.Exec(ctx, copyJSON(
		featureReconciliationSQL(s.tableGlob("match_index"), s.tableGlob("lane_matchups")),
		s.path("reconciliation.json"))); err != nil {
		return err
	}
	rows, err := readJSONArray[featureReconciliation](s.path("reconciliation.json"))
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("reconciliation: expected one row, got %d", len(rows))
	}
	recon := rows[0]
	if recon.MatchupMatches != recon.LedgerMatches {
		return fmt.Errorf("%w: %d matches in lane_matchups, %d in the ledger",
			ErrFeatureReconciliation, recon.MatchupMatches, recon.LedgerMatches)
	}
	if recon.MatchupRows != recon.MatchupMatches*10 {
		return fmt.Errorf("%w: %d rows for %d matches, expected %d",
			ErrFeatureReconciliation, recon.MatchupRows, recon.MatchupMatches, recon.MatchupMatches*10)
	}
	return s.checkSchema(ctx)
}

// checkSchema verifies that every built table carries the columns the schema
// document describes, in the order it describes them.
//
// The schema is generated from a specification rather than from the tables, so
// this is the check that keeps the two from drifting: a column added to the SQL
// and forgotten in the document, or documented and not built, refuses the
// build. The order is compared as well, because the order is what a reader of
// the README's examples sees in a `SELECT *`.
func (s *featureState) checkSchema(ctx context.Context) error {
	var statements []string
	var drift []string
	for _, table := range featureTables {
		statements = append(statements, copyJSON(
			fmt.Sprintf("SELECT column_name AS name, column_type AS type FROM (DESCRIBE SELECT * FROM %s)",
				parquetOf(s.tableGlob(table))),
			s.path("describe-"+table+".json")))
	}
	if err := s.engine.Exec(ctx, joinStatements(statements...)); err != nil {
		return err
	}
	for _, table := range featureTables {
		described, err := readJSONArray[featureColumn](s.path("describe-" + table + ".json"))
		if err != nil {
			return err
		}
		expected, ok := featureSchemaColumns[table]
		if !ok {
			return fmt.Errorf("%w: %s has no schema specification", ErrFeatureSchemaDrift, table)
		}
		if len(described) != len(expected) {
			return fmt.Errorf("%w: %s built %d columns, documented %d",
				ErrFeatureSchemaDrift, table, len(described), len(expected))
		}
		for i, column := range described {
			if column.Name != expected[i].Name {
				drift = append(drift, fmt.Sprintf("%s column %d is named %s, documented %s",
					table, i, column.Name, expected[i].Name))
				continue
			}
			if !featureTypeMatches(column.Type, expected[i].Type) {
				drift = append(drift, fmt.Sprintf("%s.%s built %s, documented %s",
					table, column.Name, column.Type, expected[i].Type))
			}
		}
	}
	// Every mismatch is reported, not just the first: resolving them one build
	// at a time is how a schema document stays wrong for a week.
	if len(drift) > 0 {
		return fmt.Errorf("%w: %s", ErrFeatureSchemaDrift, strings.Join(drift, "; "))
	}
	return nil
}

// featureTypeMatches compares a DuckDB column type against the type the schema
// document states.
//
// It is a comparison of the base type with nullability stripped, which is what
// the document promises: DuckDB reports a nullable column as `BIGINT` and a
// non-nullable one as `BIGINT NOT NULL`, and the document says `BIGINT` for
// both. A difference in the base type is a real drift and is refused.
func featureTypeMatches(built, documented string) bool {
	return strings.EqualFold(baseType(built), baseType(documented))
}

// baseType strips DuckDB's nullability suffix and decimal parameters, keeping
// the type name a reader of schema.json cares about.
func baseType(t string) string {
	t = strings.TrimSpace(t)
	if idx := strings.IndexFunc(t, func(r rune) bool { return r == '(' }); idx >= 0 {
		end := strings.Index(t, ")")
		if end > idx {
			t = strings.TrimSpace(t[:idx] + t[end+1:])
		}
	}
	t = strings.TrimSuffix(t, "NOT NULL")
	return strings.TrimSpace(t)
}

// writeDocs writes the two documents that are the dataset's interface.
//
// They are written from the same specification the schema gate checks against,
// so a column cannot be described in one and omitted from the other, and the
// counts in the README are the counts of this build rather than an example
// somebody once ran.
func (s *featureState) writeDocs(context.Context) error {
	if err := writeDoc(s.datasetDir, "schema.json", featureSchema(s.counts, s.opts)); err != nil {
		return err
	}
	readme := featureReadme(s.counts, s.opts)
	target := filepath.Join(s.datasetDir, "README.md")
	if err := os.WriteFile(target, []byte(readme), publishedFilePerm); err != nil { //nolint:gosec // G306: publishedFilePerm is 0644 on purpose - see perms.go.
		return fmt.Errorf("write dataset README: %w", err)
	}
	// The mode is set explicitly rather than left to the umask, for the same
	// reason the manifest's is: this file is served to a reader and lives on a
	// volume whose umask is not ours to know.
	if err := os.Chmod(target, publishedFilePerm); err != nil {
		return fmt.Errorf("set dataset README mode: %w", err)
	}
	return nil
}

// writeManifest writes the build receipt into the staging tree.
//
// It is written last among the staged documents and published last among the
// published paths: a receipt that arrives before the tree it receipts describes
// is a receipt for the wrong tree.
func (s *featureState) writeManifest() error {
	s.manifest = FeatureManifest{
		Dataset:       FeatureDatasetDir,
		GeneratedAt:   s.generatedAt.Format(time.RFC3339),
		GitSHA:        s.opts.GitSHA,
		DuckDBVersion: s.manifest.DuckDBVersion,
		Sources:       []string{featureSummarySource, featureTimelineSource},
		Scope: FeatureScope{
			Region:   s.opts.Region,
			Platform: s.opts.Platform,
			Queue:    s.opts.Queue,
		},
		MinDurationS:  s.opts.MinDurationS,
		BatchParts:    featureBatchParts,
		RawParts:      len(s.summaryParts),
		TimelineParts: len(s.timelineParts),
		Matches:       s.counts.Matches,
		Eligible:      s.counts.Eligible,
		Excluded:      s.counts.Excluded,
		Tables:        s.tables,
		Notice:        featureNotice,
	}
	return writeDoc(s.datasetDir, "manifest.json", s.manifest)
}

// publish swaps the staged dataset into place: the six table directories, then
// the two documents that describe them, then the manifest.
func (s *featureState) publish(result *FeatureResult) error {
	dirs := make([]string, 0, len(featureTables))
	for _, table := range featureTables {
		dirs = append(dirs, FeatureDatasetDir+"/"+table)
	}
	published, err := Publisher{
		AggRoot: s.opts.DatasetRoot,
		// The documentation is dataset content, so it is published with the
		// tables rather than after them: a reader who finds a table and no
		// README has no way to know what its NULLs mean.
		Files: []string{
			FeatureDatasetDir + "/schema.json",
			FeatureDatasetDir + "/README.md",
		},
		Log: s.opts.Log,
	}.Publish(s.staging, dirs, FeatureDatasetDir+"/manifest.json")
	if err != nil {
		return err
	}
	result.Published = published
	result.Manifest = s.manifest
	s.opts.Log.Info("feature dataset published", "dir", result.Dir,
		"tables", len(featureTables), "matches", s.counts.TimelinePresent,
		"eligible", s.counts.Eligible)
	return nil
}

// sortedExclusions renders the exclusion breakdown in a stable order, so that
// two builds of the same archive produce the same document.
func sortedExclusions(excluded map[string]int) []featureExclusion {
	out := make([]featureExclusion, 0, len(excluded))
	for reason, rows := range excluded {
		out = append(out, featureExclusion{Reason: reason, Rows: rows})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reason < out[j].Reason })
	return out
}
