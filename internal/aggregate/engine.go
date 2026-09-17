package aggregate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DuckDBVersion is the pinned DuckDB release the build step is written against.
//
// The plan (section 4-D2) requires an LTS release rather than "latest": DuckDB
// publishes an LTS edition every other minor, and a major (2.0) release with a
// breaking change window is expected in the near future. This pin is mirrored
// in docs/aggregation.md and in the Dockerfile, and is asserted at runtime by
// OpenCLIEngine so a drifted image fails loudly instead of silently producing
// different numbers.
const DuckDBVersion = "1.4.5"

// DuckDBBinEnv overrides the location of the pinned DuckDB CLI.
//
// It exists so that CI, the container image and a developer machine can each
// point at their own copy of the pinned binary without a rebuild. Docker and
// the Makefile are frozen by the contract, so the override has to live in the
// process environment rather than in build configuration.
const DuckDBBinEnv = "LOLSTATS_DUCKDB_BIN"

// duckDBLookupCandidates are the relative paths probed, in order, when no
// explicit binary is configured.
var duckDBLookupCandidates = []string{
	// Local verification copy downloaded by the aggregation engineer; see
	// docs/aggregation.md. It is gitignored, so its absence is normal.
	".agent-artifacts/duckdb/duckdb",
	"bin/duckdb",
}

// Engine is the SQL engine the build step runs against.
//
// It is an interface so that tests can observe the statements the build
// compiles without a DuckDB binary on the machine: most of this package is
// policy, and the policy must be testable on a machine with no DuckDB at all.
type Engine interface {
	// Exec runs a SQL script and returns an error if DuckDB reports one.
	Exec(ctx context.Context, sql string) error
	// Version reports the engine version, for assertions and logging.
	Version(ctx context.Context) (string, error)
	// Close releases any resources held by the engine.
	Close() error
}

// CLIEngine runs SQL through the pinned DuckDB command line client.
//
// The client is invoked with -batch (never interactive) and -init /dev/null so
// that a developer's ~/.duckdbrc cannot change the numbers a build produces.
// SQL arrives on stdin; results are read back from files the SQL writes, which
// avoids any dependency on how a given DuckDB version formats a result set.
type CLIEngine struct {
	bin     string
	version string
	log     *slog.Logger

	// allowVersionMismatch permits a different engine version. It is only set
	// from an explicit operator flag, never implicitly.
	allowVersionMismatch bool
}

var _ Engine = (*CLIEngine)(nil)

// ErrNoDuckDBBinary reports that no pinned DuckDB client could be located.
var ErrNoDuckDBBinary = errors.New("no duckdb binary found: set " + DuckDBBinEnv + " or install the pinned client")

// FindDuckDBBin locates the DuckDB client to use.
//
// Resolution order is explicit configuration (argument, then environment),
// then the known local download, then PATH. An explicit setting that does not
// exist is an error rather than a silent fallback, because a typo in CI must
// not degrade into "ran a different engine".
func FindDuckDBBin(configured string) (string, error) {
	for _, candidate := range []string{configured, os.Getenv(DuckDBBinEnv)} {
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve duckdb binary %q: %w", candidate, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("configured duckdb binary %q: %w", candidate, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("configured duckdb binary %q is a directory", candidate)
		}
		return abs, nil
	}
	for _, candidate := range duckDBLookupCandidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			abs, err := filepath.Abs(candidate)
			if err != nil {
				return "", fmt.Errorf("resolve duckdb binary %q: %w", candidate, err)
			}
			return abs, nil
		}
	}
	if found, err := exec.LookPath("duckdb"); err == nil {
		return found, nil
	}
	return "", ErrNoDuckDBBinary
}

// OpenCLIEngine resolves and validates the pinned DuckDB client.
//
// The version is asserted up front rather than trusted: every published number
// depends on the engine version, so a mismatch must stop the build before it
// reads a single row.
func OpenCLIEngine(ctx context.Context, configuredBin string, allowMismatch bool, log *slog.Logger) (*CLIEngine, error) {
	bin, err := FindDuckDBBin(configuredBin)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	engine := &CLIEngine{bin: bin, log: log, allowVersionMismatch: allowMismatch}
	version, err := engine.Version(ctx)
	if err != nil {
		return nil, err
	}
	if version != DuckDBVersion && !allowMismatch {
		return nil, fmt.Errorf("duckdb version mismatch: found %s, pinned %s (set --duckdb-allow-mismatch to override)", version, DuckDBVersion)
	}
	if version != DuckDBVersion {
		log.Warn("duckdb version differs from the pinned release", "found", version, "pinned", DuckDBVersion)
	}
	log.Info("duckdb engine ready", "bin", bin, "version", version, "pinned", DuckDBVersion)
	return engine, nil
}

// Bin reports the resolved client path.
func (e *CLIEngine) Bin() string { return e.bin }

// Version runs `duckdb --version` and returns the bare version string.
func (e *CLIEngine) Version(ctx context.Context) (string, error) {
	if e.version != "" {
		return e.version, nil
	}
	cmd := exec.CommandContext(ctx, e.bin, "--version")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("run %s --version: %w: %s", e.bin, err, strings.TrimSpace(stderr.String()))
	}
	// Observed shape: "v1.4.5 (Andium) f31be57c18".
	fields := strings.Fields(strings.TrimSpace(stdout.String()))
	if len(fields) == 0 {
		return "", fmt.Errorf("run %s --version: empty output", e.bin)
	}
	e.version = strings.TrimPrefix(fields[0], "v")
	return e.version, nil
}

// Exec runs a SQL script through the pinned client.
//
// stderr is echoed on failure: DuckDB reports parse and bind errors there and
// a bare exit status is useless for diagnosis.
func (e *CLIEngine) Exec(ctx context.Context, sql string) error {
	if strings.TrimSpace(sql) == "" {
		return errors.New("refusing to run an empty sql script")
	}
	cmd := exec.CommandContext(ctx, e.bin, "-batch", "-init", os.DevNull)
	cmd.Stdin = strings.NewReader(sql)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("duckdb exec: %w: %s", err, truncate(strings.TrimSpace(stderr.String()), 2000))
	}
	if out := strings.TrimSpace(stderr.String()); out != "" {
		e.log.Debug("duckdb warning", "stderr", truncate(out, 2000))
	}
	return nil
}

// Close is a no-op: each Exec is its own short-lived process. It exists so the
// build has a single shutdown path regardless of engine implementation.
func (e *CLIEngine) Close() error { return nil }

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + " ...[truncated]"
}
