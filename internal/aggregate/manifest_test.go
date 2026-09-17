package aggregate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The standalone re-index is the repair path for a tree whose manifest was lost
// or left behind by a crash, and it is the only caller that passes the zero
// Partition. These tests pin both halves of the contract that makes it safe:
// the tree drives the document, and a partition nothing backs never enters it.

// demoTree builds a labelled demo tree and returns its root and one partition.
func demoTree(t *testing.T) (string, aggmodel.Partition) {
	t.Helper()

	root := t.TempDir()
	result, err := Demo(DemoOptions{OutDir: root, MinCellN: 2})
	if err != nil {
		t.Fatalf("demo: %v", err)
	}
	if len(result.Manifest.Partitions) != 1 {
		t.Fatalf("demo published %d partitions, want 1", len(result.Manifest.Partitions))
	}
	return root, result.Manifest.Partitions[0]
}

// TestManifestReindexRebuildsFromTheTree is the regression test for a manifest
// that advertised a partition no directory held.
//
// The re-index used to merge its own zero Partition into the document, so a
// tree with one partition came back with two: the real one and an all-zero
// ghost with an empty patch, which then won `latest` because an empty patch
// compares equal to nothing and the tie went to the "build". A consumer
// following `latest` was sent to an empty patch.
func TestManifestReindexRebuildsFromTheTree(t *testing.T) {
	root, want := demoTree(t)

	// The repair case: the manifest is gone, only the artifacts remain.
	manifestPath := filepath.Join(root, filepath.FromSlash(aggmodel.ManifestPath))
	if err := os.Remove(manifestPath); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}

	stamp := time.Date(2026, 9, 18, 1, 2, 3, 0, time.UTC)
	manifest, err := UpdateManifest(root, aggmodel.Partition{}, aggmodel.SourceDemo, stamp)
	if err != nil {
		t.Fatalf("re-index without a manifest: %v", err)
	}

	if len(manifest.Partitions) != 1 {
		t.Fatalf("re-index found %d partitions, want the 1 the tree holds: %+v",
			len(manifest.Partitions), manifest.Partitions)
	}
	for _, p := range manifest.Partitions {
		if p.Patch == "" {
			t.Errorf("partition %+v has an empty patch: a zero partition must never enter the manifest", p)
		}
	}
	if manifest.Latest.Patch != want.Patch {
		t.Errorf("latest = %q, want the only partition %q", manifest.Latest.Patch, want.Patch)
	}
	if manifest.Latest.Region != want.Region || manifest.Latest.Queue != want.Queue ||
		manifest.Latest.Bracket != want.Bracket {
		t.Errorf("latest = %s/%s/%d/%s, want %s/%s/%d/%s",
			manifest.Latest.Patch, manifest.Latest.Region, manifest.Latest.Queue, manifest.Latest.Bracket,
			want.Patch, want.Region, want.Queue, want.Bracket)
	}
	if !manifest.GeneratedAt.Equal(stamp) {
		t.Errorf("generated_at = %s, want the supplied stamp %s", manifest.GeneratedAt, stamp)
	}
	if manifest.Source != aggmodel.SourceDemo {
		t.Errorf("source = %q, want %q carried over from the demo envelopes", manifest.Source, aggmodel.SourceDemo)
	}

	// Everything that lives in an artifact must come back identical, so that a
	// re-index is a repair and not a silent edit of the published numbers.
	got := manifest.Partitions[0]
	wantDerived := want
	wantDerived.GeneratedAt = got.GeneratedAt
	wantDerived.BuildRunID = 0
	wantDerived.GitSHA = ""
	if !reflect.DeepEqual(got, wantDerived) {
		t.Errorf("re-indexed partition\n got %+v\nwant %+v", got, wantDerived)
	}

	// Bookkeeping is deliberately not recovered: no artifact carries the run id
	// or the commit, and inventing them would put a wrong row id in the site's
	// metadata. The re-index says so by leaving them zero.
	if got.BuildRunID != 0 || got.GitSHA != "" {
		t.Errorf("re-indexed partition invented build_run_id %d / git_sha %q",
			got.BuildRunID, got.GitSHA)
	}
}

// TestManifestReindexKeepsLiveEntries covers the other half: a re-index must
// preserve what the manifest on disk already knew, because that is the only
// place a build run id and a commit survive a later repair.
func TestManifestReindexKeepsLiveEntries(t *testing.T) {
	root, partition := demoTree(t)

	live, err := ReadManifest(root)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	live.Partitions[0].BuildRunID = 4242
	live.Partitions[0].GitSHA = strings.Repeat("a", 40)
	if err := WriteManifest(root, live); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	stamp := time.Date(2026, 9, 18, 4, 5, 6, 0, time.UTC)
	manifest, err := UpdateManifest(root, aggmodel.Partition{}, aggmodel.SourceDemo, stamp)
	if err != nil {
		t.Fatalf("re-index: %v", err)
	}
	if len(manifest.Partitions) != 1 {
		t.Fatalf("re-index found %d partitions, want 1", len(manifest.Partitions))
	}
	if manifest.Partitions[0].BuildRunID != 4242 || manifest.Partitions[0].GitSHA != strings.Repeat("a", 40) {
		t.Errorf("re-index dropped the live bookkeeping: build_run_id %d git_sha %q",
			manifest.Partitions[0].BuildRunID, manifest.Partitions[0].GitSHA)
	}
	if manifest.Partitions[0].Patch != partition.Patch {
		t.Errorf("re-index moved the partition to patch %q", manifest.Partitions[0].Patch)
	}
}

// TestWriteManifestSwapsAtomically pins the write path: the document is served,
// so it has to arrive complete, readable by whoever the tree is served to, and
// without leaving a temporary name behind for a directory listing to expose.
func TestWriteManifestSwapsAtomically(t *testing.T) {
	root, _ := demoTree(t)

	original, err := ReadManifest(root)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	updated := original
	updated.GeneratedAt = time.Date(2026, 9, 18, 7, 8, 9, 0, time.UTC)
	if err := WriteManifest(root, updated); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	back, err := ReadManifest(root)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !back.GeneratedAt.Equal(updated.GeneratedAt) {
		t.Errorf("generated_at = %s, want %s", back.GeneratedAt, updated.GeneratedAt)
	}
	if !reflect.DeepEqual(back.Partitions, original.Partitions) {
		t.Errorf("the swap changed the partition list:\n got %+v\nwant %+v", back.Partitions, original.Partitions)
	}

	path := filepath.Join(root, filepath.FromSlash(aggmodel.ManifestPath))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat manifest: %v", err)
	}
	// A rebuild of an already-served file must not narrow who can read it, so
	// the mode is set explicitly rather than inherited from the umask.
	if got := info.Mode().Perm(); got != publishedFilePerm {
		t.Errorf("manifest mode = %04o, want %04o", got, publishedFilePerm)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), manifestTmpPrefix) {
			t.Errorf("temporary manifest %q left behind after the swap", entry.Name())
		}
	}
}

// TestUpdateManifestRefusesAnUnknownSource keeps a typo in --source from being
// published: the manifest is what labels every artifact, so an unrecognised
// source would relabel real numbers as something they are not. The refusal
// happens in UpdateManifest rather than in WriteManifest, which writes whatever
// it is handed; this pins that the guard is reached before anything is written.
func TestUpdateManifestRefusesAnUnknownSource(t *testing.T) {
	root, _ := demoTree(t)

	before, err := ReadManifest(root)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if _, err := UpdateManifest(root, aggmodel.Partition{}, aggmodel.Source("riot-match-v9"), time.Now()); err == nil {
		t.Error("UpdateManifest accepted an unknown source")
	}
	after, err := ReadManifest(root)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Error("a refused re-index modified the manifest")
	}
}
