package aggregate

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// Publishing.
//
// The rule is that a build either replaces a whole partition or leaves it
// exactly as it was, and that a reader never sees a half-written directory. The
// mechanism is a staging tree inside the aggregate root plus directory renames:
// rename(2) is atomic within a filesystem, so a reader sees either the old
// directory or the new one and never a mixture.
//
// The aggregate root is served by Caddy as static files, so there is no
// application layer that could serve a request from two directories at once and
// no way to roll back a bad publish other than a re-run. That makes the order of
// the two renames matter:
//
//  1. the partition directory is swapped in;
//  2. manifest.json is swapped in last, so the landing page only ever advertises
//     a partition whose files are already in place.
//
// If the process dies between the two, the new partition is on disk and the
// manifest does not mention it. That is the safe direction: an unreferenced
// directory is invisible, whereas a manifest entry written first would point at
// files that do not exist yet, which breaks the site build.

// Publisher swaps staged artifacts into the live aggregate tree.
type Publisher struct {
	// AggRoot is the aggregate root, e.g. ./data/agg.
	AggRoot string
	Log     *slog.Logger
}

// PublishResult reports what was replaced, for logging and for the audit row's
// artifact_uri.
type PublishResult struct {
	// Replaced lists the relative paths that were swapped in.
	Replaced []string
	// ReplacedExisting is true when a previous version of the partition was
	// displaced, which is false for the first build of a partition.
	ReplacedExisting bool
}

// Publish swaps the staged tree into place.
//
// relativeDirs are directories relative to both roots, e.g.
// "v1/p/16.18/EUW/420/all". manifestRel is the manifest, published last. Any
// error leaves the live tree as it was: every step either completes or is
// undone before Publish returns.
func (p Publisher) Publish(stagingRoot string, relativeDirs []string, manifestRel string) (PublishResult, error) {
	if p.Log == nil {
		p.Log = slog.New(slog.DiscardHandler)
	}
	if len(relativeDirs) == 0 {
		return PublishResult{}, errors.New("publish: nothing to publish")
	}
	staging, err := filepath.Abs(stagingRoot)
	if err != nil {
		return PublishResult{}, fmt.Errorf("publish: resolve staging root: %w", err)
	}
	for _, rel := range relativeDirs {
		source := filepath.Join(staging, filepath.FromSlash(rel))
		if info, err := os.Stat(source); err != nil || !info.IsDir() {
			return PublishResult{}, fmt.Errorf("publish: staged directory %s is missing", rel)
		}
	}
	manifestSource := filepath.Join(staging, filepath.FromSlash(manifestRel))
	if info, err := os.Stat(manifestSource); err != nil || info.IsDir() {
		return PublishResult{}, fmt.Errorf("publish: staged manifest %s is missing", manifestRel)
	}

	trashRoot := filepath.Join(p.AggRoot, trashDirName())
	if err := os.MkdirAll(trashRoot, 0o755); err != nil {
		return PublishResult{}, fmt.Errorf("publish: create trash directory: %w", err)
	}
	// The trash directory is removed on every path out of this function; a
	// leftover one is harmless but confusing, and it is the evidence a failed
	// build was rolled back cleanly.
	defer func() {
		if err := os.RemoveAll(trashRoot); err != nil {
			p.Log.Warn("publish: could not remove trash directory", "path", trashRoot, "error", err)
		}
	}()

	var result PublishResult
	for i, rel := range relativeDirs {
		live := filepath.Join(p.AggRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
			return result, p.rollback(fmt.Errorf("publish: create parent of %s: %w", rel, err), result, trashRoot, i)
		}
		moved, err := displace(live, filepath.Join(trashRoot, fmt.Sprintf("old-%d", i)))
		if err != nil {
			return result, p.rollback(err, result, trashRoot, i)
		}
		result.ReplacedExisting = result.ReplacedExisting || moved
		if err := os.Rename(filepath.Join(staging, filepath.FromSlash(rel)), live); err != nil {
			// Put the old directory back before reporting, so the tree the
			// operator inspects after a failure is the tree that was live
			// before it.
			if moved {
				if restoreErr := os.Rename(filepath.Join(trashRoot, fmt.Sprintf("old-%d", i)), live); restoreErr != nil {
					p.Log.Error("publish: could not restore displaced directory", "path", live, "error", restoreErr)
				}
			}
			return result, fmt.Errorf("publish: move %s into place: %w", rel, err)
		}
		result.Replaced = append(result.Replaced, rel)
		p.Log.Info("published partition", "path", rel, "replaced_existing", moved)
	}

	if err := os.Rename(manifestSource, filepath.Join(p.AggRoot, filepath.FromSlash(manifestRel))); err != nil {
		return result, p.rollback(fmt.Errorf("publish: swap manifest: %w", err), result, trashRoot, len(relativeDirs))
	}
	result.Replaced = append(result.Replaced, manifestRel)
	p.Log.Info("published manifest", "path", manifestRel)
	return result, nil
}

// rollback undoes the swaps that already happened, newest first.
//
// It is best effort by design: it reports every problem it hits and returns the
// original error, because the original error is the one that explains why the
// build failed.
func (p Publisher) rollback(cause error, result PublishResult, trashRoot string, displaced int) error {
	// result.Replaced[i] was swapped in over trash slot old-i, so walking it
	// backwards both removes what this run published and restores what it
	// displaced. The manifest is never left half-swapped: rename(2) is atomic,
	// and it is only attempted once every directory is in place.
	for i := len(result.Replaced) - 1; i >= 0; i-- {
		rel := result.Replaced[i]
		if rel == aggmodel.ManifestPath {
			continue
		}
		live := filepath.Join(p.AggRoot, filepath.FromSlash(rel))
		if err := os.RemoveAll(live); err != nil {
			p.Log.Error("publish: rollback could not remove partially published directory", "path", rel, "error", err)
		}
		old := filepath.Join(trashRoot, fmt.Sprintf("old-%d", i))
		if _, err := os.Stat(old); err == nil {
			if err := os.Rename(old, live); err != nil {
				p.Log.Error("publish: rollback could not restore directory", "path", rel, "error", err)
			}
		}
	}
	if displaced > len(result.Replaced) {
		p.Log.Error("publish: rollback state is inconsistent", "displaced", displaced, "published", len(result.Replaced))
	}
	return cause
}

// displace moves the live directory into the trash and reports whether there was
// one to move.
func displace(live, trash string) (bool, error) {
	info, err := os.Stat(live)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("publish: stat %s: %w", live, err)
	case !info.IsDir():
		return false, fmt.Errorf("publish: %s exists and is not a directory", live)
	}
	if err := os.RemoveAll(trash); err != nil {
		return false, fmt.Errorf("publish: clear trash slot: %w", err)
	}
	if err := os.Rename(live, trash); err != nil {
		return false, fmt.Errorf("publish: displace %s: %w", live, err)
	}
	return true, nil
}

// trashDirName names the transient directory that holds displaced artifacts.
//
// It sits beside v1 rather than inside it so that a crash cannot leave anything
// under the served path, and it carries the pid and the wall clock so that two
// builds racing on the same aggregate root cannot delete each other's backup.
func trashDirName() string {
	return fmt.Sprintf(".trash-%d-%d", os.Getpid(), time.Now().UnixNano())
}
