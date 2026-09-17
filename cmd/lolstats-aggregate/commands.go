package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
	"github.com/Erik-Schuetze/league-of-legends/internal/aggregate"
	"github.com/Erik-Schuetze/league-of-legends/internal/config"
)

// The subcommands. Each one parses its flags, fills the options struct that
// internal/aggregate already validates, runs it and prints one status line.
// Nothing about the artifact shape is decided here: this layer exists so the
// library stays testable without a process and the binary stays testable
// without a database.

func runBuild(args []string, stdout, stderr io.Writer, getenv config.Getenv) int {
	env, err := newEnvironment(getenv)
	if err != nil {
		return fail(stderr, "build", err)
	}
	cfg := env.cfg

	var (
		seg         segFlags
		rawRoot     = cfg.Raw.Root
		aggRoot     = cfg.Aggregate.Root
		windowEnd   string
		windowDays  = cfg.Aggregate.SourceWindowDays
		minCellN    = cfg.Aggregate.MinCellN
		duckdbBin   string
		allowMism   bool
		metricsAddr = cfg.MetricsAddr
	)
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addSegFlags(fs, &seg, cfg)
	fs.StringVar(&rawRoot, "raw", rawRoot, "raw archive root, the parent of riot/match-v5")
	fs.StringVar(&aggRoot, "agg", aggRoot, "aggregate root the artifacts are published under")
	fs.StringVar(&windowEnd, "window-end", "",
		"last day of the source window, empty means the newest date in the archive")
	fs.IntVar(&windowDays, "window-days", windowDays, "length of the source window in days")
	fs.IntVar(&minCellN, "min-cell-n", minCellN,
		"cells with fewer observations are suppressed and counted, never published")
	fs.StringVar(&duckdbBin, "duckdb-bin", "",
		"pinned duckdb client, empty means $LOLSTATS_DUCKDB_BIN or PATH")
	fs.BoolVar(&allowMism, "duckdb-allow-mismatch", false,
		"run even when the client is not the pinned DuckDB release")
	fs.StringVar(&metricsAddr, "metrics-addr", metricsAddr,
		"prometheus listen address, empty disables the endpoint")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		printUsage(stderr, "lolstats-aggregate build: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	ctx, stop := signalContext()
	defer stop()
	defer serveMetrics(ctx, env.log, metricsAddr, env.metrics)()

	auditor, err := env.auditor(aggRoot)
	if err != nil {
		return fail(stderr, "build", err)
	}

	result, buildErr := aggregate.Build(ctx, aggregate.BuildOptions{
		RawRoot:    rawRoot,
		AggRoot:    aggRoot,
		Region:     seg.region,
		Platform:   seg.platform,
		Queue:      seg.queue,
		Bracket:    aggmodel.Bracket(seg.bracket),
		Patch:      seg.patch,
		WindowEnd:  windowEnd,
		WindowDays: windowDays,
		MinCellN:   minCellN,
		GitSHA:     gitSHA(getenv),
		DuckDBBin:  duckdbBin,

		AllowVersionMismatch: allowMism,
		Auditor:              auditor,
		Metrics:              env.metrics,
		Log:                  env.log,
	})
	if buildErr != nil {
		// The counts are reported on the failing path too: "0 of 9000 cells
		// survived the floor" is the diagnosis, and it is not in the error.
		statusLine(stdout, "build", "failed",
			"cells_total", result.Counts.CellsTotal,
			"cells_published", result.Counts.CellsPublished,
			"cells_suppressed", result.Counts.CellsSuppressed,
			"staging", result.StagingDir)
		return fail(stderr, "build", buildErr)
	}

	statusLine(stdout, "build", "ok",
		"partition", result.Seg.Dir(),
		"patch", result.Seg.Patch,
		"cells_total", result.Counts.CellsTotal,
		"cells_published", result.Counts.CellsPublished,
		"cells_suppressed", result.Counts.CellsSuppressed,
		"matches", result.Counts.MatchesUsed,
		"replaced", result.Published.ReplacedExisting,
		"artifact_uri", aggregate.ArtifactURI(result.Seg.Dir()))
	return exitOK
}

func runVerify(args []string, stdout, stderr io.Writer, getenv config.Getenv) int {
	env, err := newEnvironment(getenv)
	if err != nil {
		return fail(stderr, "verify", err)
	}
	cfg := env.cfg

	var (
		seg        segFlags
		aggRoot    = cfg.Aggregate.Root
		schemaPath string
		source     string
		strict     bool
		maxAge     time.Duration
	)
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	// Verification is normally pointed at the whole tree, so its region flag
	// defaults to every region rather than to the configured one.
	seg.region = ""
	addSegFlags(fs, &seg, cfg)
	fs.StringVar(&aggRoot, "agg", aggRoot, "aggregate root to verify")
	fs.StringVar(&schemaPath, "schema", "",
		"JSON Schema document, empty means the schema this build emits")
	fs.StringVar(&source, "source", "",
		"expected source of every artifact, empty accepts any declared source")
	fs.BoolVar(&strict, "strict", false,
		"also fail when the newest partition is older than --max-age")
	fs.DurationVar(&maxAge, "max-age", 0,
		"staleness budget for --strict, zero disables the check")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		printUsage(stderr, "lolstats-aggregate verify: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	result, verifyErr := aggregate.Verify(aggregate.VerifyOptions{
		AggRoot:    aggRoot,
		SchemaPath: schemaPath,
		Source:     aggmodel.Source(source),
		Seg: aggmodel.Seg{
			Patch:   seg.patch,
			Region:  strings.ToUpper(seg.region),
			Queue:   seg.queue,
			Bracket: aggmodel.Bracket(seg.bracket),
		},
		Log: env.log,
	})

	if verifyErr == nil && strict {
		if err := checkFreshness(aggRoot, maxAge, time.Now()); err != nil {
			verifyErr = err
		}
	}
	if verifyErr != nil {
		statusLine(stdout, "verify", "failed",
			"partitions", result.Partitions,
			"documents", result.Documents,
			"problems", len(result.Problems))
		printUsage(stderr, "lolstats-aggregate verify: %v\n", verifyErr)
		return exitFailure
	}
	statusLine(stdout, "verify", "ok",
		"partitions", result.Partitions,
		"documents", result.Documents,
		"cells", result.Cells,
		"source", result.Manifest.Source)
	return exitOK
}

// checkFreshness enforces the --strict staleness budget against the manifest
// the tree carries. It reads the manifest rather than the partition list
// because generated_at is the only stamp that says when the numbers were
// derived, and a partition copied from elsewhere keeps the segment it had.
func checkFreshness(aggRoot string, maxAge time.Duration, now time.Time) error {
	if maxAge <= 0 {
		return nil
	}
	manifest, err := aggregate.ReadManifest(aggRoot)
	if err != nil {
		return err
	}
	age := now.UTC().Sub(manifest.GeneratedAt.UTC())
	if age > maxAge {
		return fmt.Errorf("newest artifacts are %s old, the budget is %s (generated_at %s)",
			age.Round(time.Minute), maxAge, manifest.GeneratedAt.UTC().Format(time.RFC3339))
	}
	return nil
}

func runManifest(args []string, stdout, stderr io.Writer, getenv config.Getenv) int {
	env, err := newEnvironment(getenv)
	if err != nil {
		return fail(stderr, "manifest", err)
	}
	cfg := env.cfg

	var (
		seg         segFlags
		aggRoot     = cfg.Aggregate.Root
		source      string
		generatedAt string
	)
	fs := flag.NewFlagSet("manifest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addSegFlags(fs, &seg, cfg)
	fs.StringVar(&aggRoot, "agg", aggRoot, "aggregate root holding v1/manifest.json")
	fs.StringVar(&source, "source", string(aggmodel.SourceRiotMatchV5),
		"source every partition of the tree is attributed to")
	fs.StringVar(&generatedAt, "generated-at", "",
		"generated_at stamped on the partition, empty means now")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		printUsage(stderr, "lolstats-aggregate manifest: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	stamp, err := parseStamp(generatedAt)
	if err != nil {
		return fail(stderr, "manifest", err)
	}
	manifest, err := aggregate.UpdateManifest(aggRoot, aggmodel.Partition{
		Patch:   seg.patch,
		Region:  strings.ToUpper(seg.region),
		Queue:   seg.queue,
		Bracket: aggmodel.Bracket(seg.bracket),
	}, aggmodel.Source(source), stamp)
	if err != nil {
		return fail(stderr, "manifest", err)
	}
	statusLine(stdout, "manifest", "ok",
		"partitions", len(manifest.Partitions),
		"latest", manifest.Latest.Patch,
		"source", manifest.Source,
		"path", aggmodel.ManifestPath)
	return exitOK
}

func runDemo(args []string, stdout, stderr io.Writer, getenv config.Getenv) int {
	env, err := newEnvironment(getenv)
	if err != nil {
		return fail(stderr, "demo", err)
	}
	cfg := env.cfg

	var (
		seg         segFlags
		outDir      string
		windowEnd   string
		windowDays  = cfg.Aggregate.SourceWindowDays
		minCellN    = cfg.Aggregate.MinCellN
		seed        = aggregate.DemoSeed
		generatedAt string
	)
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	seg.patch = aggregate.DemoPatch
	addSegFlags(fs, &seg, cfg)
	fs.StringVar(&outDir, "out", "",
		"directory the simulated artifact set is written to (required)")
	fs.StringVar(&windowEnd, "window-end", aggregate.DemoWindowEnd,
		"last day of the simulated source window")
	fs.IntVar(&windowDays, "window-days", windowDays, "length of the simulated window in days")
	fs.IntVar(&minCellN, "min-cell-n", minCellN, "suppression floor of the simulated build")
	fs.Int64Var(&seed, "seed", seed,
		"fixed seed of the simulated archive, so reruns are byte-identical")
	fs.StringVar(&generatedAt, "generated-at", "",
		"generated_at stamped on the artifacts, empty means the simulated snapshot instant")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		printUsage(stderr, "lolstats-aggregate demo: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}
	if outDir == "" {
		printUsage(stderr, "lolstats-aggregate demo: --out is required; demo data is never written to the aggregate root\n")
		return exitUsage
	}

	stamp, err := parseStamp(generatedAt)
	if err != nil {
		return fail(stderr, "demo", err)
	}
	result, demoErr := aggregate.Demo(aggregate.DemoOptions{
		OutDir:      outDir,
		Region:      seg.region,
		Queue:       seg.queue,
		Bracket:     aggmodel.Bracket(seg.bracket),
		Patch:       seg.patch,
		WindowEnd:   windowEnd,
		WindowDays:  windowDays,
		MinCellN:    minCellN,
		Seed:        seed,
		GeneratedAt: stamp,
		GitSHA:      gitSHA(getenv),
		Log:         env.log,
	})
	if demoErr != nil {
		return fail(stderr, "demo", demoErr)
	}
	statusLine(stdout, "demo", "ok",
		"out", outDir,
		"partition", result.Seg.Dir(),
		"source", result.Manifest.Source,
		"seed", result.Seed,
		"matches", result.Matches,
		"cells_published", result.Counts.CellsPublished,
		"cells_suppressed", result.Counts.CellsSuppressed,
		"generated_at", result.GeneratedAt.UTC().Format(time.RFC3339))
	return exitOK
}

// parseStamp reads a timestamp flag. An empty value means the caller wants the
// subcommand's own default, which is why it is not an error here.
func parseStamp(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	stamp, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("generated-at must be RFC3339, got %q", value)
	}
	return stamp.UTC(), nil
}
