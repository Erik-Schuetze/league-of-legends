package aggregate

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The engine's resource bounds are asserted here against DuckDB's own view of
// them, not against the constants that produced them. A test that only re-read
// DefaultDuckDBMemoryLimit would keep passing on the day the limit stopped being
// applied, which is the exact defect this file exists to catch: DuckDB's default
// comes from the host's RAM, so "the setting was dropped" looks like a working
// build right up to the moment the kernel kills the pod.

// engineBoundsQuery asks DuckDB what it is actually running with.
//
// current_setting is the engine's own answer, so it cannot be satisfied by a
// constant in this package: these are the values in force in the process the
// build would run.
const engineBoundsQuery = `SELECT
  current_setting('memory_limit') AS memory_limit,
  current_setting('threads') AS threads,
  current_setting('temp_directory') AS temp_directory,
  current_setting('max_temp_directory_size') AS max_temp_directory_size`

// engineBounds is the shape of the JSON document above.
type engineBounds struct {
	MemoryLimit        string `json:"memory_limit"`
	Threads            int64  `json:"threads"`
	TempDirectory      string `json:"temp_directory"`
	MaxTempDirectoryIn string `json:"max_temp_directory_size"`
}

// readEngineBounds runs one statement through the engine and reads back what
// DuckDB reports about itself.
func readEngineBounds(t *testing.T, engine *CLIEngine) engineBounds {
	t.Helper()

	report := filepath.Join(t.TempDir(), "engine_bounds.json")
	if err := engine.Exec(context.Background(), copyJSON(engineBoundsQuery, report)); err != nil {
		t.Fatalf("ask the engine for its own bounds: %v", err)
	}
	rows, err := readJSONArray[engineBounds](report)
	if err != nil {
		t.Fatalf("read the reported bounds: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("reported bounds has %d rows, want 1", len(rows))
	}
	return rows[0]
}

// TestEngineBoundsAreInForce checks that every bound the settings name is the
// bound DuckDB is running under.
//
// It fails if the preamble stops being prepended to a script, if -bail is
// dropped (a rejected SET then leaves DuckDB at its host-sized default), if a
// value is passed through unquoted, or if the spill directory is not the one the
// engine created and proved writable.
func TestEngineBoundsAreInForce(t *testing.T) {
	t.Parallel()

	bin := duckDBBin(t)
	spillParent := t.TempDir()

	engine, err := OpenCLIEngine(context.Background(), bin, false, DuckDBSettings{
		MemoryLimit: "384MiB",
		Threads:     3,
		TempDir:     spillParent,
		MaxTempSize: "512MiB",
	}, nil)
	if err != nil {
		t.Fatalf("open the pinned client: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	got := readEngineBounds(t, engine)
	want := engine.Settings()

	for _, tc := range []struct{ name, want, got string }{
		{"memory_limit", want.MemoryLimit, got.MemoryLimit},
		{"max_temp_directory_size", want.MaxTempSize, got.MaxTempDirectoryIn},
	} {
		wantBytes, err := parseDuckDBSize(tc.want)
		if err != nil {
			t.Fatalf("parse the configured %s %q: %v", tc.name, tc.want, err)
		}
		gotBytes, err := parseDuckDBSize(tc.got)
		if err != nil {
			t.Fatalf("parse the reported %s %q: %v", tc.name, tc.got, err)
		}
		if gotBytes != wantBytes {
			t.Errorf("DuckDB reports %s = %q (%d bytes), configured %q (%d bytes)",
				tc.name, tc.got, gotBytes, tc.want, wantBytes)
		}
	}

	if got.Threads != int64(want.Threads) {
		t.Errorf("DuckDB reports threads = %d, configured %d", got.Threads, want.Threads)
	}
	if got.TempDirectory != want.TempDir {
		t.Errorf("DuckDB reports temp_directory = %q, configured %q", got.TempDirectory, want.TempDir)
	}
	if expected := filepath.Join(spillParent, spillDirName); want.TempDir != expected {
		t.Errorf("spill directory = %q, want %q", want.TempDir, expected)
	}
	// The engine proved the directory writable at open, so this is a directory
	// DuckDB can spill into rather than the read-only root filesystem's /.tmp
	// that its own default resolves to.
	if !filepath.IsAbs(got.TempDirectory) {
		t.Errorf("spill directory %q is not absolute", got.TempDirectory)
	}
}

// TestDuckDBDefaultsFitInsideThePodLimit pins the default against the pod this
// repository deploys rather than against a number copied into the test.
//
// The manifest is read, not hard-coded: lowering the aggregate Job's memory
// limit without lowering the engine default has to fail here, because a default
// that equals the pod limit is still an OOM kill - the Go runtime, the page
// cache and DuckDB's own non-buffer allocations are in the same cgroup.
func TestDuckDBDefaultsFitInsideThePodLimit(t *testing.T) {
	t.Parallel()

	limits := podContainerLimits(t, filepath.Join("..", "..", "deploy", "base", "jobs", "aggregate.yaml"))

	podBytes, err := parseDuckDBSize(kubernetesQuantity(t, limits["memory"]))
	if err != nil {
		t.Fatalf("parse the pod memory limit %q: %v", limits["memory"], err)
	}
	cpus, err := strconv.Atoi(limits["cpu"])
	if err != nil {
		t.Fatalf("parse the pod cpu limit %q: %v", limits["cpu"], err)
	}

	defaultBytes, err := parseDuckDBSize(DefaultDuckDBMemoryLimit)
	if err != nil {
		t.Fatalf("parse DefaultDuckDBMemoryLimit %q: %v", DefaultDuckDBMemoryLimit, err)
	}
	// Half, not "just under": the engine is one of several claimants on the
	// cgroup, and the largest of the others (the Go heap) is not bounded by the
	// engine's limit at all.
	if defaultBytes > podBytes/2 {
		t.Errorf("default DuckDB memory limit %d bytes is more than half the pod limit %d bytes; "+
			"a statement over the limit would still be an OOM kill", defaultBytes, podBytes)
	}
	if DefaultDuckDBThreads > cpus {
		t.Errorf("default DuckDB threads %d exceeds the pod's %d CPUs", DefaultDuckDBThreads, cpus)
	}

	// And the zero value an unconfigured process carries resolves to exactly
	// those defaults rather than to DuckDB's host-derived ones.
	resolved, err := DuckDBSettings{TempDir: t.TempDir()}.resolve()
	if err != nil {
		t.Fatalf("resolve the default settings: %v", err)
	}
	if resolved.MemoryLimit != DefaultDuckDBMemoryLimit {
		t.Errorf("resolved memory limit = %q, want %q", resolved.MemoryLimit, DefaultDuckDBMemoryLimit)
	}
	if resolved.Threads != DefaultDuckDBThreads {
		t.Errorf("resolved threads = %d, want %d", resolved.Threads, DefaultDuckDBThreads)
	}
	if resolved.MaxTempSize != DefaultDuckDBMaxTempSize {
		t.Errorf("resolved maximum spill size = %q, want %q", resolved.MaxTempSize, DefaultDuckDBMaxTempSize)
	}

	// An unset temp directory means the system one, which is the emptyDir the
	// Job mounts at /tmp. The engine creates and probes the directory and
	// removes the probe file, so this leaves the directory a real build uses.
	byDefault, err := DuckDBSettings{}.resolve()
	if err != nil {
		t.Fatalf("resolve settings with no temp directory: %v", err)
	}
	if want := filepath.Join(os.TempDir(), spillDirName); byDefault.TempDir != want {
		t.Errorf("default spill directory = %q, want %q", byDefault.TempDir, want)
	}
}

// TestDuckDBBoundsFailClosedOnBadConfiguration checks that an unusable bound is
// an error at open rather than a silently unapplied setting.
//
// The failure mode being covered is not hypothetical: DuckDB's batch client
// prints "Parser Error: Memory limit must have a number" and then runs the rest
// of the script at its host-sized default, so nothing downstream would notice.
func TestDuckDBBoundsFailClosedOnBadConfiguration(t *testing.T) {
	t.Parallel()

	openPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(openPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write a file where the spill directory would go: %v", err)
	}

	for _, tc := range []struct {
		name     string
		settings DuckDBSettings
		wants    string
	}{
		{"unparsable memory limit", DuckDBSettings{MemoryLimit: "lots"}, "unknown unit"},
		{"unqualified memory limit", DuckDBSettings{MemoryLimit: "1073741824"}, "unknown unit"},
		{"kibibyte without the i", DuckDBSettings{MemoryLimit: "1Gi"}, "unknown unit"},
		{"negative memory limit", DuckDBSettings{MemoryLimit: "-1GiB"}, "positive"},
		{"unparsable spill bound", DuckDBSettings{MaxTempSize: "as much as it likes"}, "unknown unit"},
		{"unwritable spill directory", DuckDBSettings{TempDir: openPath}, "create duckdb spill directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			settings := tc.settings
			if settings.TempDir == "" {
				settings.TempDir = t.TempDir()
			}
			_, err := settings.resolve()
			if err == nil {
				t.Fatalf("resolve(%+v) succeeded, want an error mentioning %q", tc.settings, tc.wants)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("resolve(%+v) error = %v, want it to mention %q", tc.settings, err, tc.wants)
			}
		})
	}
}

// TestParseDuckDBSize is the parser's own table: the byte value it reports has
// to be the one DuckDB applies, because the test above compares the two.
func TestParseDuckDBSize(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"1GiB", 1 << 30},
		{"512MiB", 512 << 20},
		{"1024KiB", 1024 << 10},
		{"2G", 2e9},
		{"1 gb", 1e9},
		{"0.5GiB", 512 << 20},
		{"10GiB", 10 << 30},
	} {
		got, err := parseDuckDBSize(tc.in)
		if err != nil {
			t.Errorf("parseDuckDBSize(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseDuckDBSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
	for _, in := range []string{"", "  ", "1", "1Gi", "unlimited", "1XB"} {
		if got, err := parseDuckDBSize(in); err == nil {
			t.Errorf("parseDuckDBSize(%q) = %d, want an error", in, got)
		}
	}
}

// podContainerLimits reads the container resource limits out of a Job manifest.
//
// Parsed rather than hard-coded so that the assertion is about the pod this
// repository deploys. Only the `limits:` block is read: `requests:` is the
// scheduler's number, and a request is not an upper bound.
func podContainerLimits(t *testing.T, path string) map[string]string {
	t.Helper()

	body, err := os.ReadFile(path) //nolint:gosec // the manifest is a file in this repository, named by the test.
	if err != nil {
		t.Fatalf("read the aggregate Job manifest: %v", err)
	}
	out := map[string]string{}
	inLimits, limitsIndent := false, 0
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if trimmed == "limits:" {
			inLimits, limitsIndent = true, indent
			continue
		}
		if !inLimits {
			continue
		}
		if indent <= limitsIndent {
			inLimits = false
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	if out["memory"] == "" || out["cpu"] == "" {
		t.Fatalf("no memory and cpu limits in %s: %v", path, out)
	}
	return out
}

// kubernetesQuantity turns a Kubernetes memory quantity into the DuckDB literal
// that means the same number of bytes: Kubernetes spells the binary suffixes
// without the trailing B ("3Gi") and DuckDB spells them with it ("3GiB").
func kubernetesQuantity(t *testing.T, value string) string {
	t.Helper()

	for _, suffix := range []string{"Ei", "Pi", "Ti", "Gi", "Mi", "Ki"} {
		if strings.HasSuffix(value, suffix) {
			return value + "B"
		}
	}
	// Decimal quantities (3G, 3000M) and exponent forms mean the same thing in
	// both spellings, so they are parsed as written.
	return value
}
