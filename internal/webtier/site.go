package webtier

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// DataState is the tier's view of where its numbers come from. It is decided
// from the manifest and nothing else: either there is no manifest, or the
// manifest declares the snapshot as demo data, or it declares it as crawled
// Riot match data. There is no fourth "maybe" state - a manifest that does not
// say where its numbers came from is treated and labelled as unverified, which
// is the only honest reading of it.
type DataState string

const (
	StateNoData DataState = "no-data"
	StateDemo   DataState = "demo"
	StateLive   DataState = "live"
)

// The exact wording is asserted by the verification script, so it lives here
// once rather than in each template.
const (
	PreviewText           = "PREVIEW - illustrative data generated to exercise the layout, not real match statistics"
	UnverifiedPreviewText = "PREVIEW - the manifest does not declare where this snapshot came from, so its numbers are unverified"
	NoDataHeading         = "No sample yet"
)

func dataStateFor(manifest *aggmodel.Manifest, source string) DataState {
	if manifest == nil {
		return StateNoData
	}
	if source == string(aggmodel.SourceRiotMatchV5) {
		return StateLive
	}
	return StateDemo
}

// StateBanner is the one place the three states turn into words. It is rendered
// by the shell on every route, so a preview snapshot cannot be mistaken for real
// statistics on any page, and the deployed no-data state reads as deliberate
// rather than broken.
type StateBanner struct {
	State       DataState
	Heading     string
	Body        string
	Href        string
	PreviewText string
}

// Site is the tier's view of the aggregate tree: which of the three states it
// is in, what it may claim about the numbers, and the champion identity index
// that turns ids into names and icons.
//
// It is rebuilt by Loader.Site when the manifest changes and is read-only
// afterwards, so a request handler can use one value throughout a page without
// the tree shifting underneath it.
type Site struct {
	loader *Loader
	opts   Options

	rootDir      string
	rootLabel    string
	rootExplicit bool
	rootsPresent []string

	manifest         *aggmodel.Manifest
	source           string
	sourceDeclared   bool
	sourceRecognised bool

	state      DataState
	partitions []aggmodel.Partition
	latest     *aggmodel.Partition
	// partitionsByPatch is the patch switcher's index: the newest partition
	// published for a patch.
	partitionsByPatch map[string]aggmodel.Partition
	// patches is the published patches, newest first.
	patches []string

	ddragonVersion string

	checkedInChampions *aggmodel.StaticChampions
	artifactChampions  []aggmodel.StaticChampion
	champions          []aggmodel.StaticChampion
	championByID       map[int]aggmodel.StaticChampion
	championBySlug     map[string]aggmodel.StaticChampion
}

// ---------------------------------------------------------------------------
// Provenance
// ---------------------------------------------------------------------------

func (s *Site) State() DataState { return s.state }

// Source is the manifest's declared source, or "" when it does not declare one.
func (s *Site) Source() string { return s.source }

// SourceRecognised reports whether the declared source is one the build
// understands. An unrecognised source is why the banner stops claiming the
// numbers are preview data and starts calling them unverified.
func (s *Site) SourceRecognised() bool { return s.sourceRecognised }

// SourceAttribute is the value of the body's data-source attribute.
func (s *Site) SourceAttribute() string {
	if s.source == "" {
		return "none"
	}
	return s.source
}

func (s *Site) Manifest() *aggmodel.Manifest { return s.manifest }

// Latest is the newest published partition, or nil when nothing is published.
func (s *Site) Latest() *aggmodel.Partition { return s.latest }

// Partitions is every partition the manifest lists.
func (s *Site) Partitions() []aggmodel.Partition { return s.partitions }

// Patches lists the published patches, newest first.
func (s *Site) Patches() []string { return s.patches }

// RootLabel describes where the data came from, for /about.
func (s *Site) RootLabel() string {
	if s.rootDir == "" {
		return "no aggregate root reachable"
	}
	return s.rootLabel
}

// RootDir is the resolved aggregate root, or "" when none was found.
func (s *Site) RootDir() string { return s.rootDir }

// RootsPresent lists the candidate roots that exist on disk, for /about.
func (s *Site) RootsPresent() []string { return s.rootsPresent }

// GeneratedAt is when the newest partition was generated, ISO formatted, or "".
func (s *Site) GeneratedAt() string {
	if s.latest == nil {
		return ""
	}
	return s.latest.GeneratedAt.Format("2006-01-02T15:04:05Z07:00")
}

// ManifestGeneratedAt is when the manifest was written, ISO formatted, or "".
func (s *Site) ManifestGeneratedAt() string {
	if s.manifest == nil {
		return ""
	}
	return s.manifest.GeneratedAt.Format("2006-01-02T15:04:05Z07:00")
}

// DdragonVersion is the Data Dragon version identities and art resolve against.
func (s *Site) DdragonVersion() string { return s.ddragonVersion }

// MinCellN is the advertised publication floor for the newest partition, or 0
// when nothing is published.
func (s *Site) MinCellN() int {
	if s.latest == nil {
		return 0
	}
	return s.latest.MinCellN
}

// HasMinCellN reports whether a floor is advertised at all, which is what the
// banner needs to decide whether to print the sentence that names it.
func (s *Site) HasMinCellN() bool { return s.latest != nil }

// SuppressedCells is how many cells the producer withheld as too thin.
func (s *Site) SuppressedCells() int {
	if s.latest == nil {
		return 0
	}
	return s.latest.SuppressedCells
}

// CellsPublished is how many cells the producer published.
func (s *Site) CellsPublished() int {
	if s.latest == nil {
		return 0
	}
	return s.latest.CellsPublished
}

// SourceWindow is the inclusive date range the newest partition covers.
func (s *Site) SourceWindow() aggmodel.Window {
	if s.latest == nil {
		return aggmodel.Window{}
	}
	return s.latest.SourceWindow
}

// Banner is the provenance banner rendered on every route.
func (s *Site) Banner() StateBanner {
	switch s.state {
	case StateNoData:
		return StateBanner{
			State:   StateNoData,
			Heading: NoDataHeading,
			Body: "No aggregate snapshot has been published for this site yet, so there are no match statistics to show. " +
				"Every page renders its real layout with an explicit empty state rather than placeholder numbers. " +
				"What will be published, and how it is computed, is described on the provenance page.",
			Href: "/about",
		}
	case StateLive:
		return StateBanner{State: StateLive, Heading: "Published snapshot", Href: "/about"}
	default:
		heading := "Preview build"
		body := PreviewText
		if !s.sourceRecognised {
			heading = "Preview build - snapshot source not declared"
			body = UnverifiedPreviewText
		}
		return StateBanner{State: StateDemo, Heading: heading, Body: body, Href: "/about", PreviewText: body}
	}
}

// ProvenanceLine is the sentence shown in the page body and in structured data.
func (s *Site) ProvenanceLine() string {
	if s.latest == nil {
		return "Nothing published yet: no aggregate snapshot exists."
	}
	window := s.latest.SourceWindow
	return fmt.Sprintf(
		"Patch %s, %s, queue %d, bracket %s. Source window %s, generated %s.",
		s.latest.Patch, s.latest.Region, s.latest.Queue, string(s.latest.Bracket),
		WindowLabel(window.From, window.To), s.latest.GeneratedAt.UTC().Format("2006-01-02T15:04:05Z"),
	)
}

// ---------------------------------------------------------------------------
// Champion identity
// ---------------------------------------------------------------------------

// Champions is the champion identity index, ordered by id.
func (s *Site) Champions() []aggmodel.StaticChampion { return s.champions }

// ChampionByID returns the champion for an id.
func (s *Site) ChampionByID(id int) (aggmodel.StaticChampion, bool) {
	champion, ok := s.championByID[id]
	return champion, ok
}

// ChampionBySlug returns the champion for a URL slug.
func (s *Site) ChampionBySlug(slug string) (aggmodel.StaticChampion, bool) {
	champion, ok := s.championBySlug[slug]
	return champion, ok
}

// ChampionName is the display name for an id, falling back to the id so nothing
// renders blank.
func (s *Site) ChampionName(id int) string {
	if champion, ok := s.championByID[id]; ok {
		return champion.Name
	}
	return fmt.Sprintf("Champion %d", id)
}

// ChampionSlug is the URL segment for an id, or "" when the id is unknown.
func (s *Site) ChampionSlug(id int) string {
	if champion, ok := s.championByID[id]; ok {
		return champion.Slug
	}
	return ""
}

// IconURL resolves an artifact icon path against Riot's Data Dragon CDN. Icons
// are never vendored into the repository, and no request ever goes to the CDN
// at render time: the URL is data for the reader's browser.
func (s *Site) IconURL(icon string) string {
	if icon == "" {
		return ""
	}
	if strings.HasPrefix(icon, "http://") || strings.HasPrefix(icon, "https://") {
		return icon
	}
	path := strings.TrimLeft(icon, "/")
	if !strings.HasPrefix(path, "img/") {
		path = "img/champion/" + path
	}
	return "https://ddragon.leagueoflegends.com/cdn/" + s.ddragonVersion + "/" + path
}

// ---------------------------------------------------------------------------
// Partitions
// ---------------------------------------------------------------------------

// DefaultSeg is the segment every role page defaults to: the newest published
// partition.
func (s *Site) DefaultSeg() (aggmodel.Seg, bool) {
	if s.latest == nil {
		return aggmodel.Seg{}, false
	}
	return SegOf(*s.latest), true
}

// SegForPatch resolves a patch to the partition the tier should render for it.
func (s *Site) SegForPatch(patch string) (aggmodel.Seg, bool) {
	partition, ok := s.partitionsByPatch[patch]
	if !ok {
		return aggmodel.Seg{}, false
	}
	return SegOf(partition), true
}

// PartitionFor returns the partition a segment describes, so a page can print
// the sample sizes of the patch it is actually showing rather than of the
// newest one.
func (s *Site) PartitionFor(seg aggmodel.Seg) (aggmodel.Partition, bool) {
	partition, ok := s.partitionsByPatch[seg.Patch]
	if ok {
		return partition, true
	}
	if s.latest != nil {
		return *s.latest, true
	}
	return aggmodel.Partition{}, false
}

// SegOf turns a partition into the path segment its artifacts live under.
func SegOf(partition aggmodel.Partition) aggmodel.Seg {
	return aggmodel.Seg{
		Patch:   partition.Patch,
		Region:  partition.Region,
		Queue:   partition.Queue,
		Bracket: partition.Bracket,
	}
}

// ---------------------------------------------------------------------------
// Artifacts
// ---------------------------------------------------------------------------

// TierList reads the partition's tier list. It is the artifact every role page
// reads, and the page filters it by role rather than fetching per role, so the
// two views of the same data cannot drift.
func (s *Site) TierList(seg aggmodel.Seg) (*aggmodel.TierList, error) {
	return decodeArtifact[aggmodel.TierList](s.loader, s, "TierList", seg.TierListPath())
}

// ChampionDetail reads one champion's artifact for a partition.
func (s *Site) ChampionDetail(seg aggmodel.Seg, championID int) (*aggmodel.Champion, error) {
	return decodeArtifact[aggmodel.Champion](s.loader, s, "Champion", seg.ChampionPath(championID))
}

// Matchups reads one role's matchup artifact for a partition.
func (s *Site) Matchups(seg aggmodel.Seg, role aggmodel.Role) (*aggmodel.Matchups, error) {
	return decodeArtifact[aggmodel.Matchups](s.loader, s, "Matchups", seg.MatchupsPath(role))
}

// StaticPatches reads the tree's patch list, when it published one.
func (s *Site) StaticPatches() (*aggmodel.StaticPatches, error) {
	if s.rootDir == "" {
		return nil, ErrNoSnapshot
	}
	return decodeArtifact[aggmodel.StaticPatches](s.loader, s, "StaticPatches",
		aggmodel.StaticPatchesPath(s.ddragonVersion))
}

// Items reads the tree's item projection, when it published one.
func (s *Site) Items() (*aggmodel.StaticItems, error) {
	if s.rootDir == "" {
		return nil, ErrNoSnapshot
	}
	return decodeArtifact[aggmodel.StaticItems](s.loader, s, "StaticItems",
		aggmodel.StaticItemsPath(s.ddragonVersion))
}

// Runes reads the tree's rune projection, when it published one.
func (s *Site) Runes() (*aggmodel.StaticRunes, error) {
	if s.rootDir == "" {
		return nil, ErrNoSnapshot
	}
	return decodeArtifact[aggmodel.StaticRunes](s.loader, s, "StaticRunes",
		aggmodel.StaticRunesPath(s.ddragonVersion))
}

// SummonerSpells reads the tree's summoner spell projection, when it published
// one.
func (s *Site) SummonerSpells() (*aggmodel.StaticSummonerSpells, error) {
	if s.rootDir == "" {
		return nil, ErrNoSnapshot
	}
	return decodeArtifact[aggmodel.StaticSummonerSpells](s.loader, s, "StaticSummonerSpells",
		aggmodel.StaticSummonerSpellsPath(s.ddragonVersion))
}

// ---------------------------------------------------------------------------
// Derived helpers
// ---------------------------------------------------------------------------

// patchesFromManifest deduplicates the manifest's partitions by patch, keeping
// the newest generation of each, and returns them newest first. The patch
// switcher is plain links between prerendered snapshots, so this order is the
// order the control presents.
func patchesFromManifest(manifest *aggmodel.Manifest) []string {
	byPatch := map[string]aggmodel.Partition{}
	for _, partition := range append([]aggmodel.Partition{manifest.Latest}, manifest.Partitions...) {
		existing, ok := byPatch[partition.Patch]
		if !ok || existing.GeneratedAt.Before(partition.GeneratedAt) {
			byPatch[partition.Patch] = partition
		}
	}
	patches := make([]string, 0, len(byPatch))
	for patch := range byPatch {
		patches = append(patches, patch)
	}
	sort.Slice(patches, func(i, j int) bool { return patches[i] > patches[j] })
	return patches
}

// mergeChampions merges the checked-in projection with the tree's, which is
// authoritative where it is complete because it is what the aggregate build
// intended this snapshot to be read against. The checked-in projection fills
// any gap, so a champion that predates a Data Dragon release still has a name
// instead of an id.
func mergeChampions(checkedIn []aggmodel.StaticChampion, artifact []aggmodel.StaticChampion) []aggmodel.StaticChampion {
	byID := make(map[int]aggmodel.StaticChampion, len(checkedIn))
	for _, champion := range checkedIn {
		byID[champion.ID] = champion
	}
	for _, champion := range artifact {
		existing, ok := byID[champion.ID]
		if !ok {
			byID[champion.ID] = champion
			continue
		}
		byID[champion.ID] = aggmodel.StaticChampion{
			ID:    champion.ID,
			Key:   firstNonEmpty(champion.Key, existing.Key),
			Slug:  firstNonEmpty(champion.Slug, existing.Slug),
			Name:  firstNonEmpty(champion.Name, existing.Name),
			Icon:  firstNonEmpty(champion.Icon, existing.Icon),
			Roles: firstNonEmptyRoles(champion.Roles, existing.Roles),
		}
	}
	merged := make([]aggmodel.StaticChampion, 0, len(byID))
	for _, champion := range byID {
		merged = append(merged, champion)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].ID < merged[j].ID })
	return merged
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyRoles(values ...[]aggmodel.Role) []aggmodel.Role {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Cell discipline: `n` travels with every rate, and a thin cell is withheld.
// ---------------------------------------------------------------------------

// CellAvailability decides whether a cell's rate may be displayed.
//
// The contract says every cell carries `n` and that cells below min_cell_n are
// suppressed and counted rather than emitted. A cell with n = 0 is legitimate
// in the artifact - the tier list includes champions with no games in the
// window so a reader cannot confuse "absent" with "silent" - but its win rate
// is not a statistic and must not be shown as one. A cell that slipped through
// below the floor is shown as withheld rather than as noise.
type CellAvailability string

const (
	AvailabilityPublished CellAvailability = "published"
	AvailabilityNoSample  CellAvailability = "no-sample"
	AvailabilityWithheld  CellAvailability = "withheld"

	// WithheldLiteral is what a rate column shows for a withheld cell. It is a
	// word rather than an empty cell because the value exists and this site
	// chooses not to publish it.
	WithheldLiteral = "withheld"
	// NoSampleLiteral is what a rate column shows for a cell with no games.
	NoSampleLiteral = "no sample"
)

// CellAvailabilityOf reports whether a cell's rate may be published.
func CellAvailabilityOf(n int, minCellN int) CellAvailability {
	if n <= 0 {
		return AvailabilityNoSample
	}
	if minCellN > 0 && n < minCellN {
		return AvailabilityWithheld
	}
	return AvailabilityPublished
}

// Live reports whether the manifest declares an ingested Riot-derived snapshot.
func (s *Site) Live() bool { return s.state == StateLive }

// Demo reports whether the manifest declares its source as the demo fixtures.
func (s *Site) Demo() bool { return s.state == StateDemo }

// NoData reports whether no snapshot has been published at all.
func (s *Site) NoData() bool { return s.state == StateNoData }
