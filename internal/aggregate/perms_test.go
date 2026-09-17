package aggregate

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
)

// The published tree is read by a uid that is not the one that wrote it.
//
// This is the property the modes in perms.go exist for, and it is the one a
// plausible-looking tidy-up breaks: narrowing 0o755 to 0o750 leaves the build
// green and makes the nightly site build fail with EACCES on a volume where
// fsGroup is not honoured. The tests here pin the property itself - that a
// reader other than the owner can traverse and read everything under the
// published root - rather than the constants, so the modes stay free to change
// as long as that stays true.
//
// The root matters as much as the directories below it, and it is the easy one
// to get wrong: it is created by os.MkdirAll, which gives every directory it
// creates the mode of the call, so creating a private staging directory inside
// a root that does not exist yet creates the root private as a side effect. In
// deployment the root is /var/lib/lolstats/agg, a subdirectory of the shared
// volume rather than a mount point, so it really can be created by the
// aggregate job - and a private root denies the site build everything below it.

// TestDemoPublishesATraversableRoot runs the demo into a root that does not
// exist yet, which is the case that catches an implicitly private root. It
// needs no engine, so it always runs.
func TestDemoPublishesATraversableRoot(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "does", "not", "exist", "agg")
	if _, err := Demo(DemoOptions{OutDir: root, MinCellN: 2}); err != nil {
		t.Fatalf("demo: %v", err)
	}
	requireReadableByOther(t, root)
}

// TestBuildPublishesATraversableRoot is the same property for a real build, so
// it needs the pinned client and skips without it like the other end-to-end
// cases in this package.
func TestBuildPublishesATraversableRoot(t *testing.T) {
	t.Parallel()

	rawRoot := fixtureRawRoot(t, fixtureFiles(fixtureMatches()))
	root := filepath.Join(t.TempDir(), "does", "not", "exist", "agg")
	if _, err := Build(context.Background(), fixtureBuildOptions(t, root, rawRoot)); err != nil {
		t.Fatalf("build the fixture archive: %v", err)
	}
	requireReadableByOther(t, root)
}

// TestPublishedModesAreTheDocumentedOnes pins what the served tree looks like
// on disk, so a change to the constants is a change to this test rather than a
// silent break at deploy time.
func TestPublishedModesAreTheDocumentedOnes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := Demo(DemoOptions{OutDir: root, MinCellN: 2}); err != nil {
		t.Fatalf("demo: %v", err)
	}

	dirs, files := 0, 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if entry.IsDir() {
			dirs++
			if mode != publishedDirPerm {
				t.Errorf("%s is mode %#o, want the published directory mode %#o",
					path, mode, publishedDirPerm)
			}
			return nil
		}
		files++
		if mode != publishedFilePerm {
			t.Errorf("%s is mode %#o, want the published file mode %#o",
				path, mode, publishedFilePerm)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the demo tree: %v", err)
	}
	if dirs == 0 || files == 0 {
		t.Fatalf("walked %d directories and %d files, want both to be non-zero", dirs, files)
	}
}

// TestPublishCreatesATraversableRoot covers the way in that does not go through
// Build or Demo: Publish is exported and creates the root itself.
func TestPublishCreatesATraversableRoot(t *testing.T) {
	t.Parallel()

	// A staged tree is built into one root and published into another that does
	// not exist yet, which is the state Publish has to cope with: it creates
	// the trash directory, and creating it with os.MkdirAll would create the
	// root with the trash directory's mode.
	staged := filepath.Join(t.TempDir(), "staged")
	result, err := Demo(DemoOptions{OutDir: staged, MinCellN: 2})
	if err != nil {
		t.Fatalf("demo: %v", err)
	}
	if result.Seg.Patch == "" {
		t.Fatal("demo published no partition")
	}

	root := filepath.Join(t.TempDir(), "not", "there", "yet")
	pub := Publisher{AggRoot: root}
	if _, err := pub.Publish(staged, []string{result.Seg.Dir()}, aggmodel.ManifestPath); err != nil {
		t.Fatalf("publish: %v", err)
	}

	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat the published root: %v", err)
	}
	if mode := info.Mode().Perm(); mode != publishedDirPerm {
		t.Errorf("published root is mode %#o, want %#o", mode, publishedDirPerm)
	}
	requireReadableByOther(t, root)
}

// TestFileAuditorKeepsItsParentServed covers the breadcrumb directory, which is
// a sibling of the aggregate root rather than a child.
//
// It is private itself - only this process reads it - but the directory that
// contains it also contains the served tree, so creating the parent privately as
// a side effect of os.MkdirAll would deny every reader of the aggregate tree
// below it. That is the same failure as a private aggregate root, one level up.
func TestFileAuditorKeepsItsParentServed(t *testing.T) {
	t.Parallel()

	base := filepath.Join(t.TempDir(), "share", "lolstats")
	root := filepath.Join(base, "build-runs")
	auditor := FileAuditor{Root: root}
	id, err := auditor.StartBuildRun(context.Background(), contract.BuildRun{
		Patch: "16.18", Region: "EUW", Queue: aggmodel.QueueIDRankedSolo5x5,
		Bracket: string(aggmodel.BracketAll), StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("start build run: %v", err)
	}
	if err := auditor.FinishBuildRun(context.Background(), id, contract.BuildResult{
		Status: "ok", FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("finish build run: %v", err)
	}

	cases := []struct {
		path string
		want fs.FileMode
		why  string
	}{
		{base, publishedDirPerm, "the directory that also holds the served aggregate tree"},
		{root, privateDirPerm, "the breadcrumb directory, which only this process reads"},
		{auditor.path(id), privateFilePerm, "the breadcrumb itself"},
	}
	for _, tc := range cases {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tc.path, err)
		}
		if mode := info.Mode().Perm(); mode != tc.want {
			t.Errorf("%s is mode %#o, want %#o: %s", tc.path, mode, tc.want, tc.why)
		}
	}
}

// requireReadableByOther asserts that every directory under root can be
// traversed and every file read by a uid that is not the owner.
func requireReadableByOther(t *testing.T, root string) {
	t.Helper()

	checked := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		checked++
		if entry.IsDir() {
			if mode&0o005 != 0o005 {
				t.Errorf("%s is mode %#o: a reader that is not the owner cannot traverse it",
					path, mode)
			}
			return nil
		}
		if mode&0o004 == 0 {
			t.Errorf("%s is mode %#o: a reader that is not the owner cannot read it", path, mode)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if checked < 3 {
		t.Fatalf("walked %d entries under %s, want a tree", checked, root)
	}
}
