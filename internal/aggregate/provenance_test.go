package aggregate

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// This file watches the one property of a published snapshot that nothing else
// in the artifact can be checked against: the pair that says which revision and
// which run produced it.
//
// The pair cannot be derived from the tree - no partition artifact carries it,
// which is why UpdateManifest preserves it instead of rebuilding it - so the
// only honest options are to record it or to refuse to publish. A snapshot whose
// pair reads as 0/"unknown" is indistinguishable from one whose pair is real to
// every reader that is not this package, which is what the gate exists to stop.

func TestRevisionRecognition(t *testing.T) {
	t.Parallel()

	cases := []struct {
		sha  string
		want bool
	}{
		{"0123456789abcdef0123456789abcdef01234567", true},
		{"0123456", true},
		{"0123456\n", true},
		{"0123456789ABCDEF0123456789ABCDEF01234567", true},
		{"", false},
		{UnknownGitSHA, false},
		{"012345", false},
		{"0123456789abcdef0123456789abcdef012345678", false},
		{"0123456789abcdef0123456789abcdef0123456g", false},
		{"main", false},
		{"v1.2.3", false},
	}

	for _, tc := range cases {
		if got := IsRevision(tc.sha); got != tc.want {
			t.Errorf("IsRevision(%q) = %v, want %v", tc.sha, got, tc.want)
		}
	}
}

func TestProvenanceProblemNamesWhatIsMissing(t *testing.T) {
	t.Parallel()

	const sha = "0123456789abcdef0123456789abcdef01234567"

	cases := []struct {
		name    string
		sha     string
		runID   int64
		want    []string
		missing []string
	}{
		{
			name: "a recorded pair has no problem",
			sha:  sha, runID: 15,
		},
		{
			name: "a zero run id names no row",
			sha:  sha, runID: 0,
			want:    []string{"build_run_id 0"},
			missing: []string{"git_sha"},
		},
		{
			name: "an unrecorded revision names no commit",
			sha:  UnknownGitSHA, runID: 15,
			want:    []string{`git_sha "unknown"`},
			missing: []string{"build_run_id"},
		},
		{
			name: "neither half was recorded",
			sha:  "", runID: 0,
			want: []string{`git_sha ""`, "build_run_id 0"},
		},
	}

	for _, tc := range cases {
		problem := ProvenanceProblem(tc.sha, tc.runID)
		if len(tc.want) == 0 {
			if problem != "" {
				t.Errorf("%s: ProvenanceProblem = %q, want empty", tc.name, problem)
			}
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(problem, want) {
				t.Errorf("%s: ProvenanceProblem = %q, want it to name %q", tc.name, problem, want)
			}
		}
		for _, missing := range tc.missing {
			if strings.Contains(problem, missing) {
				t.Errorf("%s: ProvenanceProblem = %q, want it to stay silent about %q", tc.name, problem, missing)
			}
		}
	}
}

// TestFixtureProvenanceGateRefusesAndAccepts is the gate's end-to-end proof on
// the fixture archive: the half that must stop the build, the half that must
// let it through, and the default that keeps an offline build working.
func TestFixtureProvenanceGateRefusesAndAccepts(t *testing.T) {
	t.Parallel()

	rawRoot := fixtureRawRoot(t, fixtureFiles(fixtureMatches()))

	t.Run("a build with no audit row and no revision publishes nothing", func(t *testing.T) {
		t.Parallel()

		aggRoot := t.TempDir()
		opts := fixtureBuildOptions(t, aggRoot, rawRoot)
		opts.Gates = DefaultGateConfig(opts.MinCellN)
		opts.Gates.RequireProvenance = true
		// fixtureBuildOptions carries no Auditor, which is the state a database
		// outage leaves behind: the run has no id to file its numbers under.
		opts.GitSHA = UnknownGitSHA

		result, err := Build(context.Background(), opts)
		if !errors.Is(err, ErrUnrecordedProvenance) {
			t.Fatalf("Build error = %v, want %v", err, ErrUnrecordedProvenance)
		}
		if got := FailureReason(err); got != "unrecorded_provenance" {
			t.Errorf("FailureReason = %q, want unrecorded_provenance", got)
		}
		for _, want := range []string{`git_sha "unknown"`, "build_run_id 0"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %s", err, want)
			}
		}
		// Nothing was published: the tree is empty, not half-written.
		if _, statErr := os.Stat(filepath.Join(aggRoot, filepath.FromSlash(aggmodel.ManifestPath))); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("stat manifest = %v, want the manifest to be absent", statErr)
		}
		if len(result.Published.Replaced) != 0 {
			t.Errorf("the refused build reported swaps: %v", result.Published.Replaced)
		}
	})

	t.Run("a recorded pair publishes and lands in the manifest", func(t *testing.T) {
		t.Parallel()

		aggRoot := t.TempDir()
		opts := fixtureBuildOptions(t, aggRoot, rawRoot)
		opts.Gates = DefaultGateConfig(opts.MinCellN)
		opts.Gates.RequireProvenance = true
		opts.Auditor = FileAuditor{Root: filepath.Join(t.TempDir(), "build-runs")}

		result, err := Build(context.Background(), opts)
		if err != nil {
			t.Fatalf("a build with provenance failed: %v", err)
		}
		if result.Partition.BuildRunID == 0 {
			t.Fatal("the published partition has no build_run_id")
		}
		if problem := ProvenanceProblem(result.Partition.GitSHA, result.Partition.BuildRunID); problem != "" {
			t.Errorf("the published partition's provenance is incomplete: %s", problem)
		}
		if result.Partition.GitSHA != fixtureGitSHA {
			t.Errorf("published git_sha = %q, want %q", result.Partition.GitSHA, fixtureGitSHA)
		}
		// The pair in the manifest on disk is the pair the build recorded, not
		// a re-derived one: readPartition cannot recover it from the tree.
		manifest, err := ReadManifest(aggRoot)
		if err != nil {
			t.Fatalf("read published manifest: %v", err)
		}
		if manifest.Latest.BuildRunID != result.Partition.BuildRunID {
			t.Errorf("manifest latest build_run_id = %d, want %d",
				manifest.Latest.BuildRunID, result.Partition.BuildRunID)
		}
		if manifest.Latest.GitSHA != fixtureGitSHA {
			t.Errorf("manifest latest git_sha = %q, want %q", manifest.Latest.GitSHA, fixtureGitSHA)
		}
	})

	t.Run("the gate is off unless an operator asks for it", func(t *testing.T) {
		t.Parallel()

		if DefaultGateConfig(100).RequireProvenance {
			t.Error("DefaultGateConfig requires provenance: an offline build with no database would stop")
		}

		aggRoot := t.TempDir()
		opts := fixtureBuildOptions(t, aggRoot, rawRoot)
		opts.GitSHA = UnknownGitSHA
		opts.Gates = DefaultGateConfig(opts.MinCellN)

		result, err := Build(context.Background(), opts)
		if err != nil {
			t.Fatalf("the default gate refused an offline build: %v", err)
		}
		if result.Partition.BuildRunID != 0 {
			t.Errorf("build_run_id = %d, want 0 for a build with no auditor", result.Partition.BuildRunID)
		}
		if !strings.EqualFold(result.Partition.GitSHA, UnknownGitSHA) {
			t.Errorf("git_sha = %q, want %q", result.Partition.GitSHA, UnknownGitSHA)
		}
	})
}
