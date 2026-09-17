package aggregate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
)

// The build.
//
// The shape of a run, and why it is in this order:
//
//	walk the archive   -> the parts that can hold the window
//	stage the parts    -> plain parquet DuckDB can read
//	spill to parquet   -> one read of the immutable archive
//	extract features   -> one row per participant, one per ban
//	choose the patch   -> newest in the window unless the operator named one
//	reduce             -> cell, ban, build and matchup tallies
//	gates              -> fail closed, before anything is written
//	assemble           -> the frozen documents in a staging tree
//	publish            -> directory renames, manifest last
//	audit              -> a build_runs row, ok or failed
//
// The extraction and the reductions are two phases of SQL rather than one
// because the patch is chosen between them: the extraction has to see every
// patch in the window, and the reductions must see exactly one. The published
// patch cannot be derived before the archive has been read, so the split is the
// honest arrangement rather than a convenience.

// DefaultWindowDays is the trailing source window the aggregate covers when the
// operator does not ask for another one. It matches the config default: long
// enough that the popular champions clear min_cell_n in a small region, which is
// the smallest window that produces a publishable tier list.
const DefaultWindowDays = 14

// stagingPrefix names the build's staging directory inside the aggregate root.
//
// Staging must be inside the aggregate root rather than in the system temporary
// directory: publishing is a directory rename, rename(2) does not cross
// filesystems, and a staging tree on another device would silently turn an
// atomic publish into a copy with a window of inconsistency in it.
const stagingPrefix = ".staging-"

// BuildOptions is everything one build needs.
type BuildOptions struct {
	// AggRoot is the aggregate root the artifacts are published under.
	AggRoot string
	// RawRoot is the raw archive root, i.e. the parent of riot/match-v5.
	RawRoot string

	// Region is the published region and becomes the region path element. It is
	// the region the operator configured, e.g. EUW.
	Region string
	// Platform is the Riot platform id the archive filter matches, e.g. EUW1.
	// Empty means the platform is derived from the region, and the filter still
	// accepts both spellings.
	Platform string
	Queue    int
	Bracket  aggmodel.Bracket

	// Patch pins the patch to publish. Empty means the newest patch found in
	// the window, which is the patch a nightly build is expected to describe.
	Patch string

	// WindowEnd is the last day of the source window. Empty means the newest
	// partition date in the archive, so that an unchanged archive always
	// builds the same window and an offline verification is reproducible.
	WindowEnd  string
	WindowDays int

	MinCellN int
	Gates    GateConfig

	// DuckDBBin is the pinned client. Empty means the resolution order in
	// FindDuckDBBin.
	DuckDBBin            string
	AllowVersionMismatch bool

	// DuckDB carries the engine's hard resource bounds. The zero value is the
	// conservative default rather than "no bound", because DuckDB's own default
	// is derived from the host's RAM and not from the pod's cgroup limit; see
	// DuckDBSettings and DefaultDuckDBMemoryLimit.
	DuckDB DuckDBSettings

	// GitSHA is recorded in the manifest and the audit row so a published
	// number has a revision behind it.
	GitSHA string

	// Metrics records the build's outcome. A nil recorder is a no-op: the
	// counters describe the build rather than drive it, so an operator who has
	// no Prometheus must still get artifacts.
	Metrics obs.MetricsRecorder

	// Engine, Auditor and Now are injection points. Production leaves them
	// zero; tests use them to run the policy with no DuckDB binary, no
	// database and no clock.
	Engine  Engine
	Auditor Auditor
	Now     func() time.Time

	Log *slog.Logger
}

// BuildResult is what a finished build produced, for the CLI to print and for
// tests to assert on.
type BuildResult struct {
	Seg        aggmodel.Seg
	Manifest   aggmodel.Manifest
	Partition  aggmodel.Partition
	Counts     GateCounts
	Cells      CellOutput
	Published  PublishResult
	StagingDir string
	BuildRunID int64
}

// Build runs one build and publishes it.
//
// It returns the result even when it fails, because a failed build's counts are
// what the audit row and the operator message are made of. The returned error is
// the reason nothing was published, and the published tree is untouched whenever
// the error is non-nil.
func Build(ctx context.Context, opts BuildOptions) (result BuildResult, err error) {
	startedAt := time.Now()
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.WindowDays == 0 {
		opts.WindowDays = DefaultWindowDays
	}
	if opts.MinCellN <= 0 {
		return result, fmt.Errorf("min_cell_n must be positive, got %d", opts.MinCellN)
	}
	if opts.Metrics == nil {
		opts.Metrics = obs.NopRecorder{}
	}
	if opts.Gates.MinCellN == 0 {
		opts.Gates = DefaultGateConfig(opts.MinCellN)
	}
	if opts.Bracket == "" {
		opts.Bracket = aggmodel.BracketAll
	}
	if opts.AggRoot == "" || opts.RawRoot == "" {
		return result, errors.New("aggregate root and raw root are both required")
	}
	// A real build must not take over a directory the demo wrote. The demo
	// notice would then describe numbers that were measured, which is the one
	// confusion docs/decisions/ADR-005-demo-data-provenance.md exists to
	// prevent.
	if err := refuseDemoTree(opts.AggRoot); err != nil {
		return result, err
	}
	// Registered before the staging directory exists so that a build rejected
	// by its own arguments is still counted. An operator watching build
	// failures should see a bad flag, not silence.
	defer func() {
		opts.Metrics.ObserveBuildDuration(time.Since(startedAt).Seconds())
		if err != nil {
			opts.Metrics.IncBuildFailure(FailureReason(err))
			return
		}
		opts.Metrics.AddCellsPublished(result.Counts.CellsPublished)
		opts.Metrics.AddCellsSuppressed(result.Counts.CellsSuppressed)
	}()
	if opts.WindowEnd != "" {
		if _, err := parseDate(opts.WindowEnd); err != nil {
			return result, fmt.Errorf("window end: %w", err)
		}
	}

	generatedAt := opts.Now().UTC()
	// The root is created explicitly, and it is created served. In deployment it
	// is /var/lib/lolstats/agg, a subdirectory of the shared PVC rather than a
	// mount point, so whichever job touches the volume first creates it - and
	// creating it private would deny the site-build job (uid 1000) traversal to
	// everything below it, failing the nightly site build while this job passed.
	// os.MkdirAll would otherwise create it with the staging mode as a side
	// effect. The staging directory inside it stays private: only this process
	// reads a partial build. See perms.go.
	if err := os.MkdirAll(opts.AggRoot, publishedDirPerm); err != nil { //nolint:gosec // G301: read by the site-build job as uid 1000 on an NFS volume where fsGroup is not honoured; see perms.go.
		return result, fmt.Errorf("create aggregate root: %w", err)
	}
	staging := filepath.Join(opts.AggRoot, fmt.Sprintf("%s%d-%d", stagingPrefix, os.Getpid(), generatedAt.UnixNano()))
	// Private: the half-built partition is read by this process alone, so the
	// served modes would only widen it for no reader. See perms.go.
	if err := os.MkdirAll(staging, privateDirPerm); err != nil {
		return result, fmt.Errorf("create staging directory: %w", err)
	}
	// Staging is removed on every path out, success included: the published
	// tree is the only output, and a leftover staging directory inside the
	// aggregate root would be served by nothing and understood by nobody. It
	// is private for the same reason the trash directory is - a half-built
	// partition must not be readable at a path a client could guess.
	defer func() {
		if removeErr := os.RemoveAll(staging); removeErr != nil {
			opts.Log.Warn("could not remove staging directory", "path", staging, "error", removeErr)
		}
	}()

	result = BuildResult{StagingDir: staging, Seg: aggmodel.Seg{
		Patch: opts.Patch, Region: opts.Region, Queue: opts.Queue, Bracket: opts.Bracket,
	}}

	state, err := newBuildState(opts, generatedAt, staging)
	if err != nil {
		return result, err
	}

	// The audit row is opened before the first row is read, so that a build
	// killed mid-extraction is visible as a run that never finished. The patch
	// column carries what the operator asked for: the resolved patch is not
	// known yet, and it is recorded in artifact_uri instead.
	if opts.Auditor != nil {
		id, auditErr := opts.Auditor.StartBuildRun(ctx, contract.BuildRun{
			Patch:     opts.Patch,
			Region:    opts.Region,
			Queue:     opts.Queue,
			Bracket:   string(opts.Bracket),
			StartedAt: generatedAt,
			GitSHA:    opts.GitSHA,
		})
		if auditErr != nil {
			// Never fatal: see the note in audit.go. The artifacts are correct
			// without the row, and a database outage must not stop publishing.
			opts.Log.Error("could not open build run", "error", auditErr)
		} else {
			result.BuildRunID = id
			state.buildRunID = id
			defer func() { state.finishAudit(id, err) }()
		}
	}

	if err := state.run(ctx, &result); err != nil {
		// A failed build still reports what it managed to count before it
		// stopped. The counters are what tells an operator whether the input or
		// the floor is wrong - "13 of 13 cells suppressed" is a very different
		// message from "0 rows in the window" - and they must not be lost just
		// because the run was refused.
		result.Seg = state.seg
		result.Counts = state.counts
		result.Cells = state.cells
		result.Partition = state.partition
		return result, err
	}
	result.Seg = state.seg
	result.Counts = state.counts
	result.Cells = state.cells
	result.Partition = state.partition
	return result, nil
}

// refuseDemoTree stops a real build from publishing into a directory the demo
// wrote.
//
// The mirror of demo.refuseRealTree. Overwriting the artifacts while leaving
// the demo notice in place would leave a directory that says its numbers are
// simulated and holds numbers that were measured, so the two guards together
// make the two kinds of tree impossible to mix up in either direction. An
// absent or empty aggregate root is fine: that is the normal first build.
func refuseDemoTree(aggRoot string) error {
	manifest, err := ReadManifest(aggRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("aggregate root %s: %w", aggRoot, err)
	}
	if manifest.Source == aggmodel.SourceDemo {
		return fmt.Errorf("refusing to publish into %s: it holds demo artifacts (source %q); point the build at an empty aggregate root instead",
			aggRoot, manifest.Source)
	}
	return nil
}

// finishAudit closes the build_runs row with the outcome of the run.
//
// It is deferred rather than called at the end because every early return after
// this point must still close the row, and it never returns an error to the
// caller: the publish has already happened and the audit is bookkeeping.
func (s *buildState) finishAudit(id int64, buildErr error) {
	result := contract.BuildResult{
		Status:          auditStatusFor(buildErr),
		FinishedAt:      s.opts.Now().UTC(),
		CellsTotal:      s.counts.CellsTotal,
		CellsPublished:  s.counts.CellsPublished,
		CellsSuppressed: s.counts.CellsSuppressed,
		ArtifactURI:     ArtifactURI(s.seg.Dir()),
	}
	if buildErr != nil {
		result.Err = buildErr.Error()
	}
	if err := s.opts.Auditor.FinishBuildRun(context.WithoutCancel(context.Background()), id, result); err != nil {
		s.opts.Log.Error("could not close build run", "id", id, "error", err)
		return
	}
	s.opts.Log.Info("build run recorded",
		"id", id, "status", result.Status, "cells_total", result.CellsTotal,
		"cells_published", result.CellsPublished, "cells_suppressed", result.CellsSuppressed)
}

// buildState carries what the phases of one run share.
type buildState struct {
	opts        BuildOptions
	generatedAt time.Time
	staging     string
	scratch     string

	engine       Engine
	source       aggmodel.Source
	seg          aggmodel.Seg
	window       aggmodel.Window
	patch        string
	stagedPaths  []string
	matchesPath  string
	featuresPath string
	bansPath     string

	archiveStats archiveStatsRow
	windowStats  windowStatsRow
	counts       GateCounts
	cells        CellOutput

	cellCounts    []CellCount
	banCounts     []BanCount
	itemCounts    []BuildCount
	runeCounts    []BuildCount
	spellCounts   []BuildCount
	matchupCounts []MatchupCount

	buildRunID int64
	manifest   aggmodel.Manifest
	partition  aggmodel.Partition
}

// newBuildState validates the options that can be checked before any I/O and
// resolves the archive view.
func newBuildState(opts BuildOptions, generatedAt time.Time, staging string) (*buildState, error) {
	parts, window, err := planArchive(opts)
	if err != nil {
		return nil, err
	}
	opts.Log.Info("raw archive planned",
		"parts", len(parts), "window_from", window.From, "window_to", window.To,
		"region", opts.Region, "queue", opts.Queue)

	scratch := filepath.Join(staging, ".scratch")
	staged, err := RawArchive{Root: opts.RawRoot}.StageParts(context.Background(), parts, scratch)
	if err != nil {
		return nil, err
	}
	// The staged paths are not needed again: the spill statements name them.
	return &buildState{
		opts:        opts,
		generatedAt: generatedAt,
		staging:     staging,
		scratch:     scratch,
		window:      window,
		// A real build can only ever describe the Riot archive it read. The
		// demo subcommand builds the same documents from simulated tallies and
		// overrides this, which is the one and only way a non-Riot source can
		// appear in an artifact.
		source:       aggmodel.SourceRiotMatchV5,
		matchesPath:  filepath.Join(scratch, "matches.parquet"),
		featuresPath: filepath.Join(scratch, "features.parquet"),
		bansPath:     filepath.Join(scratch, "bans.parquet"),
		stagedPaths:  staged,
	}, nil
}
