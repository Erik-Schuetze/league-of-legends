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
	"strconv"
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

// The engine's resource bounds: memory, threads, and where an oversized
// statement spills.
//
// DuckDB does not read the cgroup its process is in. Left alone it sizes its own
// buffer manager from the HOST's physical memory - 12.7 GiB on the machine this
// was measured on - so an unbounded build allocates as if it owned the node, and
// the kernel OOM-kills the container long before DuckDB considers itself under
// pressure. deploy/base/jobs/aggregate.yaml gives the pod 3 GiB, and the Go
// runtime, the page cache and every allocation DuckDB makes outside its buffer
// manager all share that one cgroup, so DuckDB's ceiling has to be a fraction of
// the pod limit, never the whole of it.
//
// A limit does not make a build fit in the pod. What it changes is the failure
// mode: an oversized statement either spills into the temp directory - slower,
// but it publishes - or raises "Out of Memory Error" from DuckDB itself. Both
// outcomes are better than the kernel killing the container, but only the spill
// produces artifacts, and the second is what a window too large for the limit
// actually does, so a build that dies here is a signal to tune the window or the
// limit rather than proof that the limit is wrong. That is also why the temp
// directory has to be writable and why a spill that runs away from it is
// bounded too.
const (
	// DefaultDuckDBMemoryLimit is the ceiling applied when configuration names
	// none: a third of the aggregate Job's 3 GiB pod limit. The remaining two
	// thirds are the Go runtime's heap (the Job sets GOMEMLIMIT to 2 GiB), the
	// page cache and DuckDB's own non-buffer allocations - a pass whose limit
	// equals the pod limit is still killed. The fraction is not folklore: it is
	// asserted against the manifest by
	// TestDuckDBDefaultsFitInsideThePodLimit, so lowering the pod limit without
	// lowering this default fails the suite.
	//
	// The fraction is generous on purpose. The buffer manager is not the whole
	// process - measured engine RSS is 2.2 GiB for the demo-scale build with this
	// limit in force - so the ceiling has to leave the pod room for everything
	// DuckDB does not count, not just for the Go reader.
	DefaultDuckDBMemoryLimit = "1GiB"

	// DefaultDuckDBThreads matches the Job's 2-CPU limit. DuckDB sizes its
	// default pool from the host's core count - 10 on the measured node, so ten
	// times the container's share - and every extra thread carries its own
	// operator state, which is memory the pod does not have.
	DefaultDuckDBThreads = 2

	// DefaultDuckDBMaxTempSize bounds the spill directory. DuckDB's own default
	// is "90% of available disk space", which here is the whole node's disk: a
	// bound is what keeps the fix for one outage (a killed pod) from becoming
	// the cause of a worse one (a node with no free space, shared with every
	// other workload).
	DefaultDuckDBMaxTempSize = "10GiB"

	// spillDirName is the directory the engine owns inside the configured temp
	// directory. It is a subdirectory rather than the temp directory itself so
	// that one job's spill can be identified, bounded and removed without
	// touching whatever else shares /tmp.
	spillDirName = "duckdb-spill"
)

// duckDBSizeUnits are the suffixes DuckDB accepts on a byte-size setting. IEC
// and SI spellings are both valid there and mean different things (1GiB is
// 1<<30 bytes, 1GB is 1e9), so they are kept distinct here rather than
// normalised: reading a limit as smaller or larger than the operator wrote it is
// the same class of defect as the limit not being applied at all.
var duckDBSizeUnits = map[string]int64{
	// DuckDB accepts these spellings. The single letters and the two-letter SI
	// forms are decimal; only the "i" forms are powers of two.
	"b":   1,
	"k":   1e3,
	"m":   1e6,
	"g":   1e9,
	"t":   1e12,
	"kb":  1e3,
	"mb":  1e6,
	"gb":  1e9,
	"tb":  1e12,
	"kib": 1 << 10,
	"mib": 1 << 20,
	"gib": 1 << 30,
	"tib": 1 << 40,
}

// parseDuckDBSize parses a DuckDB byte-size literal into bytes.
//
// It exists because DuckDB does not fail a build when a SET is rejected: the
// batch client prints "Parser Error: Memory limit must have a number" and
// carries on with its own default, which is the host's RAM. A typo in the
// environment would therefore turn a bounded engine into an unbounded one and
// nothing would say so, so the value is checked here before it is also sent to
// DuckDB.
func parseDuckDBSize(value string) (int64, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return 0, errors.New("is empty")
	}
	// The sign is separated first so that "-1GiB" is rejected as a non-positive
	// size rather than as an unknown unit "-1GiB".
	sign, body := "", trimmed
	if body[0] == '+' || body[0] == '-' {
		sign, body = body[:1], body[1:]
	}
	end := 0
	for end < len(body) && (body[end] == '.' || (body[end] >= '0' && body[end] <= '9')) {
		end++
	}
	number, unit := strings.TrimSpace(sign+body[:end]), strings.TrimSpace(body[end:])
	multiplier, ok := duckDBSizeUnits[unit]
	if !ok {
		return 0, fmt.Errorf("has an unknown unit %q, want a suffix such as MiB or GiB", unit)
	}
	quantity, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0, fmt.Errorf("is not a byte size such as 1GiB: %w", err)
	}
	if quantity <= 0 {
		return 0, errors.New("must be positive")
	}
	return int64(quantity * float64(multiplier)), nil
}

// DuckDBSettings are the hard bounds the engine applies to every DuckDB process
// it starts.
//
// A zero field means "use the default named above". The bounds are applied per
// process rather than once per build because each Exec is its own short-lived
// client: a setting that is not repeated in every script is not in force in any
// of them.
type DuckDBSettings struct {
	// MemoryLimit is DuckDB's own ceiling, as a DuckDB size literal such as
	// 1GiB. It is a hard limit: a statement that would exceed it spills instead.
	MemoryLimit string
	// Threads caps DuckDB's thread pool. Zero or less means
	// DefaultDuckDBThreads, because "let DuckDB decide" would mean the host's
	// core count.
	Threads int
	// TempDir is the parent of the spill directory. Empty means the process's
	// system temporary directory, which the aggregate Job satisfies with the
	// emptyDir it mounts at /tmp.
	TempDir string
	// MaxTempSize bounds the spill directory, as a DuckDB size literal.
	MaxTempSize string
}

// resolve fills in the defaults, picks the spill directory and proves it is
// writable.
//
// The check is at open rather than discovered mid-build because an unusable
// spill directory does not stop DuckDB from starting: the build would run until
// the first statement that does not fit in memory and then fail deep inside the
// nightly window with a message about a temporary file. Doing it once, up front,
// means a misconfigured volume fails before the archive has been read.
func (s DuckDBSettings) resolve() (DuckDBSettings, error) {
	if s.MemoryLimit == "" {
		s.MemoryLimit = DefaultDuckDBMemoryLimit
	}
	if s.Threads <= 0 {
		s.Threads = DefaultDuckDBThreads
	}
	if s.MaxTempSize == "" {
		s.MaxTempSize = DefaultDuckDBMaxTempSize
	}
	if _, err := parseDuckDBSize(s.MemoryLimit); err != nil {
		return s, fmt.Errorf("duckdb memory limit %q: %w", s.MemoryLimit, err)
	}
	if _, err := parseDuckDBSize(s.MaxTempSize); err != nil {
		return s, fmt.Errorf("duckdb maximum spill size %q: %w", s.MaxTempSize, err)
	}
	if s.TempDir == "" {
		s.TempDir = os.TempDir()
	}
	parent, err := filepath.Abs(s.TempDir)
	if err != nil {
		return s, fmt.Errorf("resolve duckdb temp directory %q: %w", s.TempDir, err)
	}
	spill := filepath.Join(parent, spillDirName)
	if err := os.MkdirAll(spill, 0o700); err != nil {
		return s, fmt.Errorf("create duckdb spill directory %q: %w", spill, err)
	}
	// MkdirAll succeeds on a directory that already exists but is not writable
	// by this user, which is exactly the read-only-root-filesystem case, so the
	// directory is probed rather than assumed.
	probe, err := os.CreateTemp(spill, ".probe-*")
	if err != nil {
		return s, fmt.Errorf("write to duckdb spill directory %q: %w", spill, err)
	}
	name := probe.Name()
	_ = probe.Close()
	if err := os.Remove(name); err != nil {
		return s, fmt.Errorf("remove probe from duckdb spill directory %q: %w", spill, err)
	}
	s.TempDir = spill
	return s, nil
}

// preamble renders the settings as the statements every script starts with.
//
// Every value is a quoted literal or an integer, so nothing an operator can put
// in the environment - a stray quote, a semicolon - can become a second
// statement.
func (s DuckDBSettings) preamble() string {
	return strings.Join([]string{
		"SET memory_limit = " + quoteLiteral(s.MemoryLimit) + ";",
		fmt.Sprintf("SET threads = %d;", s.Threads),
		"SET temp_directory = " + quoteLiteral(s.TempDir) + ";",
		"SET max_temp_directory_size = " + quoteLiteral(s.MaxTempSize) + ";",
	}, "\n") + "\n"
}

// String renders the settings for a log line.
func (s DuckDBSettings) String() string {
	return fmt.Sprintf("memory_limit=%s threads=%d temp_directory=%s max_temp_directory_size=%s",
		s.MemoryLimit, s.Threads, s.TempDir, s.MaxTempSize)
}

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
	bin      string
	version  string
	settings DuckDBSettings
	log      *slog.Logger

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
// reads a single row. The resource bounds are resolved and the spill directory
// created here for the same reason: a limit the engine does not actually apply
// is the defect this exists to prevent.
func OpenCLIEngine(ctx context.Context, configuredBin string, allowMismatch bool, settings DuckDBSettings, log *slog.Logger) (*CLIEngine, error) {
	bin, err := FindDuckDBBin(configuredBin)
	if err != nil {
		return nil, err
	}
	resolved, err := settings.resolve()
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	engine := &CLIEngine{bin: bin, settings: resolved, log: log, allowVersionMismatch: allowMismatch}
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
	log.Info("duckdb engine ready", "bin", bin, "version", version, "pinned", DuckDBVersion,
		"bounds", resolved.String())
	return engine, nil
}

// Settings reports the bounds the engine applied, defaults filled in.
func (e *CLIEngine) Settings() DuckDBSettings { return e.settings }

// Bin reports the resolved client path.
func (e *CLIEngine) Bin() string { return e.bin }

// Version runs `duckdb --version` and returns the bare version string.
func (e *CLIEngine) Version(ctx context.Context) (string, error) {
	if e.version != "" {
		return e.version, nil
	}
	// e.bin is the pinned engine path from configuration or PATH and the
	// argument list is constant; nothing from the archive reaches the command
	// line, because the SQL goes in on stdin.
	cmd := exec.CommandContext(ctx, e.bin, "--version") //nolint:gosec // G204: no archive data in the command.
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
// The script is prefixed with the resource bounds, because each Exec starts a
// fresh DuckDB process that knows nothing about the previous one: a memory limit
// applied once at open would be gone by the time a statement that needs it runs.
// -bail is what makes that prefix load-bearing rather than decorative: without
// it DuckDB prints a rejected SET and continues with its own host-sized default.
//
// stderr is echoed on failure: DuckDB reports parse and bind errors there and
// a bare exit status is useless for diagnosis.
func (e *CLIEngine) Exec(ctx context.Context, sql string) error {
	if strings.TrimSpace(sql) == "" {
		return errors.New("refusing to run an empty sql script")
	}
	cmd := exec.CommandContext(ctx, e.bin, "-batch", "-bail", "-init", os.DevNull) //nolint:gosec // G204: only the pinned engine binary and fixed flags.
	cmd.Stdin = strings.NewReader(e.settings.preamble() + sql)
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
