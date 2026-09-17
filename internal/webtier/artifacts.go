package webtier

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
	"sync"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The aggregate tree reader.
//
// This is a port of web/src/lib/artifacts.ts, which was the Astro build's only
// door into the data. The behaviours it exists to preserve are:
//
//   - an explicitly configured LOLSTATS_AGG_ROOT is honoured as-is, so an empty
//     mount means "no data yet" rather than a silent substitution of fixtures;
//   - an artifact this reader does not understand fails closed, because a page
//     that renders blanks looks like "no games were played";
//   - nothing here writes, so the tier can mount the published tree read-only.
//
// One behaviour is deliberately stricter than the build it replaces. The Astro
// build read the tree once, at build time, so a missing artifact was a build
// error that produced no page at all. This tier reads the tree per request, so
// a missing or corrupt artifact has to become a runtime answer: it is a 503
// with a visible error page, never a truncated 200.

// FixturesMode selects whether the checked-in demo tree may be read.
type FixturesMode string

const (
	// FixturesAuto reads the configured root and falls back to the fixtures
	// only when no root was configured explicitly.
	FixturesAuto FixturesMode = "auto"
	// FixturesOff never reads the fixtures.
	FixturesOff FixturesMode = "off"
	// FixturesOnly ignores the configured root and reads the fixtures, which
	// is how the deployed tier is demonstrating parity while no real snapshot
	// is published yet.
	FixturesOnly FixturesMode = "only"
)

// Options is everything the reader needs to find its inputs.
type Options struct {
	// AggRoot is the aggregate root, i.e. the directory holding v1/.
	AggRoot string
	// FixturesMode defaults to FixturesAuto.
	FixturesMode FixturesMode
	// FixturesDir is the checked-in demo tree (LOLSTATS_FIXTURES_DIR, or
	// <repo>/web/src/fixtures).
	FixturesDir string
	// DataDir is the checked-in Data Dragon projection
	// (LOLSTATS_DATA_DIR, or <repo>/web/src/data).
	DataDir string
}

// Env names the variables the tier reads. They are the ones the deployed
// ConfigMap already sets for the Astro tier, so the new tier inherits its
// configuration instead of needing a parallel set.
const (
	EnvAggRoot      = "LOLSTATS_AGG_ROOT"
	EnvFixtures     = "LOLSTATS_AGG_FIXTURES"
	EnvFixturesDir  = "LOLSTATS_FIXTURES_DIR"
	EnvDataDir      = "LOLSTATS_DATA_DIR"
	EnvSiteURL      = "LOLSTATS_SITE_URL"
	EnvMetricsAddr  = "LOLSTATS_METRICS_ADDR"
	EnvListenAddr   = "LOLSTATS_WEB_ADDR"
	DefaultSiteURL  = "https://lol.erik-schuetze.dev"
	DefaultAddress  = ":8080"
	defaultDDRagon  = "16.18.1"
	championsDataIn = "champions.json"
)

// OptionsFromEnv reads the tier's configuration from the environment, filling
// in the repository-relative defaults for a development run.
func OptionsFromEnv() Options {
	root := discoverRepoRoot()
	opts := Options{
		AggRoot:      envValue(EnvAggRoot),
		FixturesMode: FixturesMode(strings.ToLower(envValue(EnvFixtures))),
		FixturesDir:  envValue(EnvFixturesDir),
		DataDir:      envValue(EnvDataDir),
	}
	switch opts.FixturesMode {
	case FixturesAuto, FixturesOff, FixturesOnly:
	default:
		opts.FixturesMode = FixturesAuto
	}
	if opts.FixturesDir == "" && root != "" {
		opts.FixturesDir = filepath.Join(root, "web", "src", "fixtures")
	}
	if opts.DataDir == "" && root != "" {
		opts.DataDir = filepath.Join(root, "web", "src", "data")
	}
	if opts.AggRoot == "" && root != "" {
		opts.AggRoot = filepath.Join(root, "agg")
	}
	return opts
}

func envValue(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

// discoverRepoRoot walks up from the working directory looking for the
// checked-in fixtures, which is what makes `go test ./internal/webtier` and a
// local `go run` find their fixtures without any environment set. The deployed
// image sets the directory variables explicitly.
func discoverRepoRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for dir := cwd; ; {
		if _, err := os.Stat(filepath.Join(dir, "web", "src", "fixtures")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// RootCandidate is one place the manifest might live.
type RootCandidate struct {
	Dir string
	// Label is how the root is described on /about.
	Label string
	// Explicit is true when the operator configured it rather than the tier
	// defaulting to it. An explicit root never falls back to the fixtures.
	Explicit bool
}

// candidateRoots mirrors artifacts.ts: the order the roots are tried in.
func candidateRoots(opts Options) []RootCandidate {
	primary := RootCandidate{
		Dir:      opts.AggRoot,
		Label:    "lolstats agg root (LOLSTATS_AGG_ROOT)",
		Explicit: envValue(EnvAggRoot) != "",
	}
	if !primary.Explicit {
		primary.Label = "default agg root (../agg)"
	}
	fixtures := RootCandidate{Dir: opts.FixturesDir, Label: "checked-in demo fixtures"}

	switch opts.FixturesMode {
	case FixturesOnly:
		return []RootCandidate{fixtures}
	case FixturesOff:
		return []RootCandidate{primary}
	default:
		if primary.Explicit {
			return []RootCandidate{primary}
		}
		return []RootCandidate{primary, fixtures}
	}
}

// ErrNoSnapshot means no candidate root published a manifest, so the tier has
// nothing to render statistics from. It is not a fault in the data: the pages
// that describe the site itself (the home page intro, /about, the legal pages,
// the feeds) render their real layout with an explicit empty state, and the
// routes that describe the ladder answer 503 with a visible page saying that
// nothing has been published yet.
var ErrNoSnapshot = errors.New("no aggregate snapshot has been published")

// ErrArtifactMissing means the snapshot exists and advertises this artifact but
// the file is not there. Every one of these is answered with 503.
var ErrArtifactMissing = errors.New("artifact is missing")

// fileCache keeps the bytes of the artifacts it has read, keyed by path, size
// and modification time. The published tree is written by rename, so a size or
// mtime change is what "the artifact was republished" looks like from here; a
// reader that cached by path alone would serve the previous patch forever after
// a rollout.
type fileCache struct {
	mu      sync.Mutex
	entries map[string]cachedFile
	bytes   int
}

type cachedFile struct {
	size    int64
	modTime time.Time
	data    []byte
}

// maxCachedEntry bounds what a single cached read may hold. A tier list for a
// large partition is a few hundred kilobytes; anything far beyond that is not
// something to keep in memory per process.
const maxCachedEntry = 8 << 20

// maxCachedEntries and maxCachedBytes bound the cache as a whole. The count
// alone is not a memory bound - 512 entries of the largest artifact a single
// read may cache is several gigabytes, which is more than the Deployment's
// limit - so the byte total is tracked too and the cache is dropped when either
// bound is crossed. Dropping rather than evicting one entry at a time is enough
// at this size: the next read refills what the next request needs.
const (
	maxCachedEntries = 512
	maxCachedBytes   = 64 << 20
)

func newFileCache() *fileCache {
	return &fileCache{entries: map[string]cachedFile{}}
}

// read returns an artifact's bytes, reusing the cached copy while the file's
// size and modification time are unchanged. The loader re-reads the same
// manifest, partitions and static files on every request, and the root is a
// read-only volume whose files only change when a producer publishes a
// snapshot, so a stale hit is not a correctness problem: the schema check that
// follows every read is what decides whether the bytes may be rendered.
//
// path is always the configured artifact root joined with a path this package
// built from the manifest or an aggmodel.*Path builder, never a request value,
// so the read is confined as well as untrusted-by-default.
func (c *fileCache) read(path string) ([]byte, error) {
	info, err := os.Stat(path) // #nosec G304 G703 -- path is built from the configured artifact root plus an aggmodel.*Path segment; no request value reaches it
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if entry, ok := c.entries[path]; ok && entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
		data := entry.data
		c.mu.Unlock()
		return data, nil
	}
	c.mu.Unlock()

	data, err := os.ReadFile(path) // #nosec G304 G703 -- as above: the configured artifact root plus a package-built path, no request value
	if err != nil {
		return nil, err
	}
	if len(data) <= maxCachedEntry {
		c.mu.Lock()
		if len(c.entries) >= maxCachedEntries || c.bytes+len(data) > maxCachedBytes {
			c.entries = map[string]cachedFile{}
			c.bytes = 0
		}
		c.entries[path] = cachedFile{size: info.Size(), modTime: info.ModTime(), data: data}
		c.bytes += len(data)
		c.mu.Unlock()
	}
	return data, nil
}

// Loader resolves a root, and holds the derived site data cached against the
// manifest it was derived from.
type Loader struct {
	opts  Options
	cache *fileCache

	mu        sync.Mutex
	cachedKey string
	cached    *Site

	// builds holds the resolved Data Dragon projection per version, so the
	// per-champion pages share one read and one validation of the game data.
	builds buildLookups
}

// NewLoader returns a Loader for the given options.
func NewLoader(opts Options) *Loader {
	if opts.FixturesMode == "" {
		opts.FixturesMode = FixturesAuto
	}
	return &Loader{opts: opts, cache: newFileCache()}
}

// Options returns the configuration the loader was built with.
func (l *Loader) Options() Options { return l.opts }

// RootDir returns the directory the published tree is being read from, which is
// also the directory /agg serves from, so that a page and the artifact it was
// rendered from are the same origin and a reader can check one against the
// other. It is empty when no candidate root has published a manifest: there is
// no tree to serve, which the caller answers as a state rather than as a fault.
func (l *Loader) RootDir() (string, error) {
	_, root, err := l.resolveRoot()
	if err != nil {
		return "", err
	}
	if root == nil {
		return "", nil
	}
	return root.Dir, nil
}

// Site returns the current view of the aggregate tree. It re-derives the view
// when the manifest it was built from has changed, so a rollout of a new patch
// is picked up without restarting the process.
func (l *Loader) Site() (*Site, error) {
	key, root, err := l.resolveRoot()
	if err != nil {
		return nil, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cached != nil && l.cachedKey == key {
		return l.cached, nil
	}

	site, err := l.buildSite(root)
	if err != nil {
		return nil, err
	}
	l.cached, l.cachedKey = site, key
	return site, nil
}

// resolveRoot picks the first candidate root that has a manifest and returns a
// cache key for its state. A manifest that is present but invalid is a fault
// rather than a reason to try the next candidate: reading fixtures because the
// deployed snapshot is corrupt is the silent substitution this design exists to
// prevent.
func (l *Loader) resolveRoot() (string, *RootCandidate, error) {
	for _, candidate := range candidateRoots(l.opts) {
		if candidate.Dir == "" {
			continue
		}
		path := filepath.Join(candidate.Dir, filepath.FromSlash(aggmodel.ManifestPath))
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", nil, artifactFault(path, "cannot be read: %v", err)
		}
		key := fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())
		candidate := candidate
		return key, &candidate, nil
	}
	return "", nil, nil
}

// buildSite derives everything the templates read from a resolved root.
func (l *Loader) buildSite(root *RootCandidate) (*Site, error) {
	site := &Site{
		loader: l,
		opts:   l.opts,
		state:  StateNoData,
	}

	checkedIn, err := l.loadCheckedInChampions()
	if err != nil {
		return nil, err
	}

	if root != nil {
		site.rootDir = root.Dir
		site.rootLabel = root.Label
		site.rootExplicit = root.Explicit

		manifest, source, declared, err := l.loadManifest(root.Dir)
		if err != nil {
			return nil, err
		}
		site.manifest = manifest
		site.source = source
		site.sourceDeclared = declared
	}

	site.state = dataStateFor(site.manifest, site.source)
	site.sourceRecognised = site.source == string(aggmodel.SourceRiotMatchV5) || site.source == string(aggmodel.SourceDemo)

	if site.manifest != nil {
		site.partitions = site.manifest.Partitions
		latest := site.manifest.Latest
		site.latest = &latest
		site.patches = patchesFromManifest(site.manifest)
		site.partitionsByPatch = map[string]aggmodel.Partition{}
		for _, partition := range site.partitions {
			site.partitionsByPatch[partition.Patch] = partition
		}
		site.partitionsByPatch[latest.Patch] = latest
	}

	version, err := l.staticVersion(site)
	if err != nil {
		return nil, err
	}
	site.ddragonVersion = version

	if site.rootDir != "" {
		artifact, err := l.readStaticChampions(site, version)
		if err != nil {
			return nil, err
		}
		site.artifactChampions = artifact
	}
	site.champions = mergeChampions(checkedIn.Champions, site.artifactChampions)
	site.championByID = make(map[int]aggmodel.StaticChampion, len(site.champions))
	site.championBySlug = make(map[string]aggmodel.StaticChampion, len(site.champions))
	for _, champion := range site.champions {
		site.championByID[champion.ID] = champion
		site.championBySlug[champion.Slug] = champion
	}
	site.rootsPresent = l.rootsPresent()
	return site, nil
}

// loadManifest reads and validates the root's manifest, returning the declared
// source separately from the generated type: declaring where a snapshot came
// from is an additive change, so a manifest that does not say has to render as
// unverified rather than as real match data.
func (l *Loader) loadManifest(dir string) (*aggmodel.Manifest, string, bool, error) {
	path := filepath.Join(dir, filepath.FromSlash(aggmodel.ManifestPath))
	raw, err := l.cache.read(path)
	if err != nil {
		return nil, "", false, artifactFault(path, "cannot be read: %v", err)
	}
	if err := checkEnvelopeSchema("manifest", path, raw); err != nil {
		return nil, "", false, err
	}
	if err := validateArtifact("Manifest", path, raw); err != nil {
		return nil, "", false, err
	}
	var manifest aggmodel.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, "", false, artifactFault(path, "cannot be decoded: %v", err)
	}
	var envelope struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, "", false, artifactFault(path, "cannot be decoded: %v", err)
	}
	source := strings.TrimSpace(envelope.Source)
	return &manifest, source, source != "", nil
}

// loadCheckedInChampions reads the Data Dragon projection that ships with the
// repository. It is the last resort for champion identity, so a snapshot that
// predates a Data Dragon release still renders names instead of ids.
func (l *Loader) loadCheckedInChampions() (*aggmodel.StaticChampions, error) {
	raw, path, err := l.checkedInData(championsDataIn)
	if err != nil {
		return nil, err
	}
	if err := validateArtifact("StaticChampions", path, raw); err != nil {
		return nil, err
	}
	var champions aggmodel.StaticChampions
	if err := json.Unmarshal(raw, &champions); err != nil {
		return nil, artifactFault(path, "cannot be decoded: %v", err)
	}
	return &champions, nil
}

// staticVersion returns the Data Dragon version the tree published static data
// for, falling back to the checked-in projection's version. The tree is synced
// by version directory and there can be more than one during a release overlap,
// so the highest version wins - compared numerically, because a string
// comparison would rank 16.9.1 above 16.18.1.
func (l *Loader) staticVersion(site *Site) (string, error) {
	if site.rootDir != "" {
		dir := filepath.Join(site.rootDir, aggmodel.VersionDir, "static")
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", artifactFault(dir, "cannot be listed: %v", err)
		}
		var versions []string
		for _, entry := range entries {
			if entry.IsDir() {
				versions = append(versions, entry.Name())
			}
		}
		if len(versions) > 0 {
			sort.Slice(versions, func(i, j int) bool { return compareVersions(versions[i], versions[j]) < 0 })
			return versions[len(versions)-1], nil
		}
	}
	if version := l.checkedInVersion(); version != "" {
		return version, nil
	}
	return defaultDDRagon, nil
}

func (l *Loader) checkedInVersion() string {
	raw, _, err := l.checkedInData(championsDataIn)
	if err != nil {
		return ""
	}
	var champions aggmodel.StaticChampions
	if err := json.Unmarshal(raw, &champions); err != nil {
		return ""
	}
	return champions.DDragonVersion
}

// readStaticChampions reads the tree's champion identity artifact, when the
// tree has one. A root with no static data is not a fault: the checked-in
// projection covers identity, and the art is resolved against Riot's CDN.
func (l *Loader) readStaticChampions(site *Site, version string) ([]aggmodel.StaticChampion, error) {
	path := filepath.Join(site.rootDir, filepath.FromSlash(aggmodel.StaticChampionsPath(version)))
	raw, err := l.cache.read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, artifactFault(path, "cannot be read: %v", err)
	}
	if err := validateArtifact("StaticChampions", path, raw); err != nil {
		return nil, err
	}
	var champions aggmodel.StaticChampions
	if err := json.Unmarshal(raw, &champions); err != nil {
		return nil, artifactFault(path, "cannot be decoded: %v", err)
	}
	return champions.Champions, nil
}

// rootsPresent lists the candidate roots that exist on disk at all, for the
// /about provenance note.
func (l *Loader) rootsPresent() []string {
	var present []string
	for _, candidate := range candidateRoots(l.opts) {
		if candidate.Dir == "" {
			continue
		}
		if _, err := os.Stat(candidate.Dir); err == nil {
			present = append(present, candidate.Dir)
		}
	}
	return present
}

// readArtifact reads one artifact of the resolved root, enforcing the schema.
//
// A missing file is ErrArtifactMissing rather than an empty value. The static
// site could not have had this case - it read the tree once, before rendering,
// so a missing artifact meant no page was ever emitted - and inventing a
// partial page here is exactly the failure mode the contract forbids.
func (l *Loader) readArtifact(site *Site, definition string, relative string) ([]byte, error) {
	if site.rootDir == "" {
		return nil, ErrNoSnapshot
	}
	path := filepath.Join(site.rootDir, filepath.FromSlash(relative))
	raw, err := l.cache.read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &ArtifactError{Path: path, Err: ErrArtifactMissing}
	}
	if err != nil {
		return nil, artifactFault(path, "cannot be read: %v", err)
	}
	if err := checkEnvelopeSchema(definition, path, raw); err != nil {
		return nil, err
	}
	if err := validateArtifact(definition, path, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// decodeArtifact reads one artifact and decodes it into out.
func decodeArtifact[T any](l *Loader, site *Site, definition string, relative string) (*T, error) {
	raw, err := l.readArtifact(site, definition, relative)
	if err != nil {
		return nil, err
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, artifactFault(relative, "cannot be decoded: %v", err)
	}
	return &out, nil
}

func compareVersions(a string, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	longer := len(left)
	if len(right) > longer {
		longer = len(right)
	}
	for i := 0; i < longer; i++ {
		diff := versionPart(left, i) - versionPart(right, i)
		if diff != 0 {
			return diff
		}
	}
	return strings.Compare(a, b)
}

func versionPart(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}
	value, err := strconv.Atoi(parts[index])
	if err != nil {
		return 0
	}
	return value
}
