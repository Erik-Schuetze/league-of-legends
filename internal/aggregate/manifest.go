package aggregate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The manifest is the entry point of the tree and the only file a consumer has
// to read to discover what exists. It therefore has to survive a build that
// touches one partition of many, and it has to describe partitions this build
// did not produce: a rebuild of 16.18/EUW must not silently delete the entry
// for 16.17/EUW that is sitting on disk.
//
// The manifest is recomputed in three layers, in this order:
//
//  1. the manifest currently on disk, if it parses;
//  2. every partition directory found on disk, so an entry lost by a crash is
//     restored from the artifacts themselves rather than by hand;
//  3. this build's partition, which wins.
//
// Layer 2 is why the manifest is cheap to repair: the tree is the source of
// truth, and tierlist.json carries the envelope the manifest entry needs.

// ManifestError is returned when the live manifest cannot be read. It is a hard
// failure: overwriting an unreadable manifest with one that knows about a single
// partition would silently drop every other partition the site serves.
type ManifestError struct {
	Path string
	Err  error
}

func (e *ManifestError) Error() string {
	return fmt.Sprintf("manifest %s is unreadable: %v; repair or delete it explicitly before publishing", e.Path, e.Err)
}

func (e *ManifestError) Unwrap() error { return e.Err }

// UpdateManifest merges the partition just published into the manifest on disk
// and returns the document that should be written.
//
// It does not write: publish.go writes it, so that the manifest is swapped in
// only after every partition directory is already in place.
// ReadManifest returns the manifest a published tree carries.
//
// An absent manifest is an error rather than an empty value: a tree without one
// cannot be addressed by a reader, so "missing" and "empty" must not look alike
// to a caller deciding whether to fail.
func ReadManifest(aggRoot string) (aggmodel.Manifest, error) {
	path := filepath.Join(aggRoot, filepath.FromSlash(aggmodel.ManifestPath))
	manifest, err := readManifestFile(path)
	if err != nil {
		return aggmodel.Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	return manifest, nil
}

func UpdateManifest(aggRoot string, partition aggmodel.Partition, source aggmodel.Source, generatedAt time.Time) (aggmodel.Manifest, error) {
	if !source.Valid() {
		return aggmodel.Manifest{}, fmt.Errorf("manifest: unknown source %q", source)
	}
	path := filepath.Join(aggRoot, filepath.FromSlash(aggmodel.ManifestPath))

	known := map[string]aggmodel.Partition{}
	publishedSource := source
	existing, err := readManifestFile(path)
	switch {
	case err == nil:
		if err := mergeSource(&publishedSource, existing.Source, source); err != nil {
			return aggmodel.Manifest{}, err
		}
		for _, p := range existing.Partitions {
			known[partitionKey(p)] = p
		}
	case errors.Is(err, fs.ErrNotExist):
		// First build into this root. Nothing to preserve.
	default:
		return aggmodel.Manifest{}, &ManifestError{Path: path, Err: err}
	}

	found, err := scanPartitions(aggRoot)
	if err != nil {
		return aggmodel.Manifest{}, err
	}
	for _, p := range found {
		if err := mergeSource(&publishedSource, p.source, source); err != nil {
			return aggmodel.Manifest{}, err
		}
		key := partitionKey(p.partition)
		if _, ok := known[key]; !ok {
			known[key] = p.partition
		}
	}

	known[partitionKey(partition)] = partition

	manifest := aggmodel.Manifest{
		Schema:      aggmodel.SchemaVersion,
		Source:      publishedSource,
		GeneratedAt: generatedAt.UTC(),
		Partitions:  make([]aggmodel.Partition, 0, len(known)),
	}
	for _, p := range known {
		manifest.Partitions = append(manifest.Partitions, p)
	}
	sortPartitions(manifest.Partitions)
	manifest.Latest = latestOf(manifest.Partitions, partition)
	return manifest, nil
}

// mergeSource refuses to let a tree hold artifacts from two different sources.
//
// A tree that mixes simulated and real numbers would be indistinguishable from a
// real one at the point of use: both would carry plausible rates, and only the
// manifest would know they came from different places. Refusing is the only
// honest option, and it is what stops `demo` being pointed at the live root by
// accident.
func mergeSource(published *aggmodel.Source, seen, incoming aggmodel.Source) error {
	if seen == "" || seen == *published {
		return nil
	}
	return fmt.Errorf("manifest: refusing to publish %s alongside existing %s artifacts: a tree may hold only one source", incoming, seen)
}

// partitionKey identifies a partition by the path elements that define it.
func partitionKey(p aggmodel.Partition) string {
	return fmt.Sprintf("%s/%s/%d/%s", p.Patch, p.Region, p.Queue, p.Bracket)
}

// latestOf chooses the partition `latest` points at.
//
// `latest` is a single field while the newest patch can exist for several
// regions, so the rule is: the newest patch wins, and this build wins any tie,
// because it is the one whose freshness the caller just established.
func latestOf(partitions []aggmodel.Partition, built aggmodel.Partition) aggmodel.Partition {
	if len(partitions) == 0 {
		return built
	}
	best := partitions[0]
	for _, p := range partitions[1:] {
		if comparePatch(p.Patch, best.Patch) > 0 {
			best = p
		}
	}
	if comparePatch(built.Patch, best.Patch) >= 0 {
		return built
	}
	return best
}

// sortPartitions orders newest patch first, then region, queue and bracket, so
// the manifest is stable across runs and a diff shows only real changes.
func sortPartitions(partitions []aggmodel.Partition) {
	sort.SliceStable(partitions, func(i, j int) bool {
		a, b := partitions[i], partitions[j]
		if c := comparePatch(a.Patch, b.Patch); c != 0 {
			return c > 0
		}
		if a.Region != b.Region {
			return a.Region < b.Region
		}
		if a.Queue != b.Queue {
			return a.Queue < b.Queue
		}
		return a.Bracket < b.Bracket
	})
}

// readManifestFile parses the manifest. A missing file is reported as fs.ErrNotExist
// so the caller can distinguish "first build" from "corrupt".
func readManifestFile(path string) (aggmodel.Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return aggmodel.Manifest{}, err
	}
	var manifest aggmodel.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return aggmodel.Manifest{}, err
	}
	if manifest.Schema != aggmodel.SchemaVersion {
		return aggmodel.Manifest{}, fmt.Errorf("schema %d is not the expected %d", manifest.Schema, aggmodel.SchemaVersion)
	}
	return manifest, nil
}

// discovered is a partition found on disk, complete with the source its
// envelope declares.
type discovered struct {
	partition aggmodel.Partition
	source    aggmodel.Source
}

// scanPartitions walks the published tree and reconstructs a manifest entry for
// every partition directory it finds. It reads only tierlist.json (for the
// envelope), the champions directory listing and the matchups directory
// listing, which is a few kilobytes per partition.
func scanPartitions(aggRoot string) ([]discovered, error) {
	root := filepath.Join(aggRoot, aggmodel.VersionDir, "p")
	patches, err := readDirNames(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("manifest: list patches: %w", err)
	}
	var out []discovered
	for _, patch := range patches {
		regions, err := readDirNames(filepath.Join(root, patch))
		if err != nil {
			return nil, fmt.Errorf("manifest: list regions of %s: %w", patch, err)
		}
		for _, region := range regions {
			queues, err := readDirNames(filepath.Join(root, patch, region))
			if err != nil {
				return nil, fmt.Errorf("manifest: list queues of %s/%s: %w", patch, region, err)
			}
			for _, queue := range queues {
				brackets, err := readDirNames(filepath.Join(root, patch, region, queue))
				if err != nil {
					return nil, fmt.Errorf("manifest: list brackets of %s/%s/%s: %w", patch, region, queue, err)
				}
				for _, bracket := range brackets {
					seg := aggmodel.Seg{Patch: patch, Region: region, Bracket: aggmodel.Bracket(bracket)}
					seg.Queue, err = strconv.Atoi(queue)
					if err != nil || seg.Validate() != nil {
						continue
					}
					entry, err := readPartition(aggRoot, seg)
					if err != nil {
						return nil, err
					}
					if entry != nil {
						out = append(out, *entry)
					}
				}
			}
		}
	}
	return out, nil
}

// readPartition rebuilds one manifest entry from the files on disk. A
// directory without a readable tierlist.json is not a partition: it is either
// a leftover staging directory or damage, and either way it is skipped rather
// than guessed at.
func readPartition(aggRoot string, seg aggmodel.Seg) (*discovered, error) {
	tierList, err := readJSONDoc[aggmodel.TierList](filepath.Join(aggRoot, filepath.FromSlash(seg.TierListPath())))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("manifest: read %s: %w", seg.TierListPath(), err)
	}
	champions, err := readNumericFileNames(filepath.Join(aggRoot, filepath.FromSlash(seg.Dir()), "champions"))
	if err != nil {
		return nil, fmt.Errorf("manifest: list champions of %s: %w", seg.Dir(), err)
	}
	roles, err := readMatchupRoles(filepath.Join(aggRoot, filepath.FromSlash(seg.Dir()), "matchups"))
	if err != nil {
		return nil, fmt.Errorf("manifest: list matchups of %s: %w", seg.Dir(), err)
	}
	return &discovered{
		partition: aggmodel.Partition{
			Patch:           seg.Patch,
			Region:          seg.Region,
			Queue:           seg.Queue,
			Bracket:         seg.Bracket,
			GeneratedAt:     tierList.GeneratedAt,
			SourceWindow:    tierList.SourceWindow,
			MinCellN:        tierList.MinCellN,
			SuppressedCells: tierList.SuppressedCells,
			CellsPublished:  len(tierList.Cells),
			Champions:       champions,
			MatchupRoles:    roles,
			// BuildRunID and GitSHA are deliberately left zero. They are
			// build-bookkeeping, not artifact facts, and inventing them from
			// an artifact would put a wrong row id in the site's metadata.
		},
		source: tierList.Source,
	}, nil
}

// readDirNames lists a directory, returning nil (not an error) when it is
// absent, because every level of the partition tree is optional.
func readDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// readNumericFileNames lists the <id>.json files in a directory as ascending
// integers, which is the order the contract requires for Partition.Champions.
func readNumericFileNames(dir string) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]int, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		id, err := strconv.Atoi(strings.TrimSuffix(name, ".json"))
		if err != nil || !strings.HasSuffix(name, ".json") {
			continue
		}
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids, nil
}

// readMatchupRoles lists the roles that have a matchups artifact, in canonical
// role order so the manifest does not shuffle between runs.
func readMatchupRoles(dir string) ([]aggmodel.Role, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	present := map[aggmodel.Role]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		slug := strings.TrimSuffix(entry.Name(), ".json")
		if role, ok := aggmodel.RoleFromSlug(slug); ok {
			present[role] = true
		}
	}
	roles := make([]aggmodel.Role, 0, len(present))
	for _, role := range aggmodel.Roles {
		if present[role] {
			roles = append(roles, role)
		}
	}
	return roles, nil
}
