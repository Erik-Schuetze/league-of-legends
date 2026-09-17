package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
	"github.com/Erik-Schuetze/league-of-legends/internal/aggregate"
	"github.com/Erik-Schuetze/league-of-legends/internal/config"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
)

// The binary is the only place the subcommands are wired to flags, so these
// tests drive a whole invocation. The library tests cover the maths and the
// gates; these cover the two things a flag layer can get wrong on its own: a
// subcommand that reports success without writing anything, and one that
// accepts a partition the tree does not hold.

// emptyEnv makes every setting fall back to its default, so a test never
// depends on the shell it is run from.
func emptyEnv(string) (string, bool) { return "", false }

// demoRoot publishes a labelled demo tree, which is the cheapest way to get a
// real, complete partition on disk without a raw archive or DuckDB.
func demoRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if _, err := aggregate.Demo(aggregate.DemoOptions{OutDir: root, MinCellN: 2}); err != nil {
		t.Fatalf("demo: %v", err)
	}
	return root
}

func manifestFile(root string) string {
	return filepath.Join(root, filepath.FromSlash(aggmodel.ManifestPath))
}

func mustStamp(t *testing.T, value string) time.Time {
	t.Helper()

	stamp, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return stamp
}

// TestManifestSubcommandWritesTheManifest covers the defect that a re-index
// printed a success line and changed nothing on disk, so a tree whose manifest
// was lost stayed lost however often the operator ran the repair.
func TestManifestSubcommandWritesTheManifest(t *testing.T) {
	root := demoRoot(t)
	if err := os.Remove(manifestFile(root)); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runEnv([]string{
		"manifest", "--agg", root, "--source", "demo",
		"--generated-at", "2026-09-18T00:00:00Z",
	}, &stdout, &stderr, emptyEnv)
	if code != exitOK {
		t.Fatalf("manifest exit code = %d, want %d\nstdout: %s\nstderr: %s",
			code, exitOK, stdout.String(), stderr.String())
	}

	line := stdout.String()
	for _, want := range []string{"manifest: ok", "partitions=1", "latest=16.18", "source=demo"} {
		if !strings.Contains(line, want) {
			t.Errorf("status line %q does not mention %q", line, want)
		}
	}

	written, err := aggregate.ReadManifest(root)
	if err != nil {
		t.Fatalf("the subcommand reported success but wrote no readable manifest: %v", err)
	}
	if len(written.Partitions) != 1 {
		t.Fatalf("manifest holds %d partitions, want the 1 the tree holds: %+v",
			len(written.Partitions), written.Partitions)
	}
	if written.Partitions[0].Patch != "16.18" || written.Partitions[0].Region != "EUW" {
		t.Errorf("manifest indexed %+v, want the demo partition 16.18/EUW", written.Partitions[0])
	}
	if written.Latest.Patch != "16.18" {
		t.Errorf("latest = %q, want 16.18", written.Latest.Patch)
	}
	if !written.GeneratedAt.Equal(mustStamp(t, "2026-09-18T00:00:00Z")) {
		t.Errorf("generated_at = %s, want the stamp the flag supplied", written.GeneratedAt)
	}
}

// TestManifestSubcommandRefusesAnUnpublishedPartition pins that naming a
// partition the tree does not hold fails without touching the live manifest: an
// index entry a reader cannot follow is worse than the stale one in place.
func TestManifestSubcommandRefusesAnUnpublishedPartition(t *testing.T) {
	root := demoRoot(t)

	before, err := os.ReadFile(manifestFile(root))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runEnv([]string{
		"manifest", "--agg", root, "--source", "demo",
		"--patch", "9.99", "--region", "EUW", "--queue", "420", "--bracket", "all",
	}, &stdout, &stderr, emptyEnv)
	if code != exitFailure {
		t.Fatalf("manifest exit code = %d, want %d for an unindexed partition\nstdout: %s\nstderr: %s",
			code, exitFailure, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "9.99") {
		t.Errorf("failure line %q does not name the partition that was refused", stderr.String())
	}

	after, err := os.ReadFile(manifestFile(root))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a refused re-index rewrote the live manifest")
	}
}

// TestManifestSubcommandFailsOnAnEmptyTree keeps an empty root from being
// published as a manifest that lists nothing, which a consumer would read as
// "this site has no data" rather than "this tree was never built".
func TestManifestSubcommandFailsOnAnEmptyTree(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runEnv([]string{"manifest", "--agg", t.TempDir(), "--source", "demo"},
		&stdout, &stderr, emptyEnv)
	if code != exitFailure {
		t.Fatalf("manifest exit code = %d, want %d\nstdout: %s", code, exitFailure, stdout.String())
	}
}

// TestManifestReindexPreservesWhatItDoesNotKnow covers the defect that a pure
// re-index stamped the manifest with the zero time: with no --generated-at the
// subcommand keeps the generation time the live manifest carries, so a repair
// run that changed no number cannot claim the artifacts were rebuilt in year 1.
//
// The partition fields are asserted alongside it because the --patch segment
// used to be handed to UpdateManifest as the partition itself, which gave four
// flag values precedence over the entry on disk and blanked generated_at,
// min_cell_n, cells_published, champions and matchup_roles for that partition.
func TestManifestReindexPreservesWhatItDoesNotKnow(t *testing.T) {
	root := demoRoot(t)

	live, err := aggregate.ReadManifest(root)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if live.GeneratedAt.IsZero() {
		t.Fatal("the demo manifest has no generation time, so this test cannot detect one being lost")
	}
	if len(live.Partitions) != 1 {
		t.Fatalf("the demo tree holds %d partitions, want 1", len(live.Partitions))
	}
	want := live.Partitions[0]

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"rebuilt from the tree alone", nil},
		{"named patch", []string{"--patch", "16.18", "--region", "EUW", "--queue", "420", "--bracket", "all"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append([]string{"manifest", "--agg", root, "--source", "demo"}, tc.args...)
			if code := runEnv(args, &stdout, &stderr, emptyEnv); code != exitOK {
				t.Fatalf("manifest exit code = %d, want %d\nstdout: %s\nstderr: %s",
					code, exitOK, stdout.String(), stderr.String())
			}

			got, err := aggregate.ReadManifest(root)
			if err != nil {
				t.Fatalf("read manifest: %v", err)
			}
			if !got.GeneratedAt.Equal(live.GeneratedAt) {
				t.Errorf("generated_at = %s, want the preserved %s", got.GeneratedAt, live.GeneratedAt)
			}
			if len(got.Partitions) != 1 {
				t.Fatalf("manifest holds %d partitions, want 1: %+v", len(got.Partitions), got.Partitions)
			}
			entry := got.Partitions[0]
			if entry.GeneratedAt != want.GeneratedAt || entry.MinCellN != want.MinCellN ||
				entry.SuppressedCells != want.SuppressedCells || entry.CellsPublished != want.CellsPublished ||
				entry.BuildRunID != want.BuildRunID || entry.GitSHA != want.GitSHA ||
				entry.SourceWindow != want.SourceWindow || len(entry.Champions) != len(want.Champions) ||
				len(entry.MatchupRoles) != len(want.MatchupRoles) {
				t.Errorf("re-index rewrote the entry it was re-indexing:\n got %+v\nwant %+v", entry, want)
			}
			if got.Latest.Patch != "16.18" {
				t.Errorf("latest = %q, want 16.18", got.Latest.Patch)
			}
		})
	}
}

func TestUsageMentionsEverySubcommand(t *testing.T) {
	for _, sub := range []string{"build", "verify", "manifest", "demo"} {
		if !strings.Contains(usage, "\n  "+sub+" ") {
			t.Errorf("usage does not document the %s subcommand", sub)
		}
	}
}

// TestBuildRefusesToPublishFromAStalledCrawl covers the failure mode the archive
// cannot show on its own: an expired key or a stopped crawler freezes the raw
// tree without changing it, so every later build is a cheap success that
// republishes the same cells under a new generated_at. The site then swaps a
// dated snapshot for a freshly stamped copy of itself, which reads as "the
// pipeline is alive".
//
// The guard is driven here through the environment method directly: the flag
// layer around it is one DurationVar, and the part that can be wrong is which
// way an unanswerable check fails.
func TestBuildRefusesToPublishFromAStalledCrawl(t *testing.T) {
	t.Run("no DSN is a developer build, not a failed one", func(t *testing.T) {
		// A machine with no Postgres cannot check freshness at all. Refusing
		// there would break `make` targets that only want the demo artifacts,
		// so this half is a warning - the deployed posture always has a DSN.
		env := environment{log: obs.NewLogger(config.Config{})}
		if err := env.requireFreshCrawl(context.Background(), 26*time.Hour); err != nil {
			t.Fatalf("requireFreshCrawl without a DSN = %v, want nil", err)
		}
	})

	t.Run("a DSN that cannot answer fails closed", func(t *testing.T) {
		// "The check could not run" must not degrade into "publish anyway":
		// that is exactly the silent stale snapshot the guard exists for.
		cfg := config.Config{}
		cfg.Postgres.DSN = "postgres://nobody@127.0.0.1:1/nothing?sslmode=disable&connect_timeout=1"
		cfg.Postgres.ConnTimeout = time.Second
		env := environment{cfg: cfg, log: obs.NewLogger(cfg)}
		err := env.requireFreshCrawl(context.Background(), 26*time.Hour)
		if err == nil {
			t.Fatal("requireFreshCrawl against an unreachable database returned nil")
		}
		if !strings.Contains(err.Error(), "checking crawl freshness") {
			t.Errorf("error = %q, want it to name the freshness check", err)
		}
	})
}
