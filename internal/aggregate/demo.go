package aggregate

import (
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The demo subcommand.
//
// It exists for one reason and the reason is written into the artifacts it
// writes: this repository is developed in an environment with no Riot API key
// and no real match archive, so without it there is no way to run the publish
// path end to end, show a reviewer what the frozen documents look like, or
// exercise a reader against plausible data.
//
// What keeps it from being confused with a real build is not a naming
// convention and not a comment:
//
//   - every document it writes carries envelope.source = "demo", and its
//     manifest carries manifest.source = "demo". A real build can only ever
//     produce "riot-match-v5", because the only thing that sets another value
//     is this file.
//   - it refuses to write into a tree that already holds a manifest from
//     another source, so it cannot quietly overwrite published Riot data.
//   - it writes a notice file next to the tree.
//
// The simulated dataset is generated from a fixed seed, and the demo's
// generated_at defaults to a fixed instant (the close of the simulated window)
// rather than to the wall clock, so two demo runs are byte-for-byte identical.
// See docs/decisions/ADR-005-demo-data-provenance.md.

// Demo constants. The patch is a plausible one and the window is a fixed date
// range rather than "the last 14 days", because a demo whose content changes
// with the calendar is not reproducible.
const (
	// DemoSeed is the fixed seed of the simulated dataset.
	DemoSeed int64 = 20260917
	// DemoMatches is the number of simulated matches behind the demo cells.
	// It is large enough that the simulated pools put most cells comfortably
	// above the confidence floor: the demo's purpose is to exercise the whole
	// publish path, and a demo that suppressed everything would exercise only
	// the failure path.
	DemoMatches = 2000
	// DemoPatch is the simulated patch the demo publishes.
	DemoPatch = "16.18"
	// DemoWindowEnd is the last day of the simulated window. Together with
	// DefaultWindowDays it fixes the window at 2026-09-04 to 2026-09-17.
	DemoWindowEnd = "2026-09-17"
	// DemoNoticeFile is written at the root of the demo output directory, so
	// that a person who finds the directory knows what it is before they open
	// a single JSON file.
	DemoNoticeFile = "README-DEMO.txt"
	// demoRegion is the simulated region segment.
	demoRegion = "EUW"
	// demoQueue is Riot's ranked-solo queue id.
	demoQueue = 420
)

// DemoOptions configures one simulated build.
type DemoOptions struct {
	// OutDir is the aggregate root the demo publishes into. It is not
	// required to exist. It must not already hold artifacts from a real
	// build.
	OutDir string

	Region  string
	Queue   int
	Bracket aggmodel.Bracket
	Patch   string

	WindowEnd  string
	WindowDays int
	MinCellN   int

	// Seed is the fixed seed of the generator. Zero means DemoSeed.
	Seed int64

	// GeneratedAt is stamped into every envelope and into the manifest. The
	// zero value means the simulated snapshot instant, which keeps reruns
	// byte-identical; set it to time.Now() to record a real clock instead.
	GeneratedAt time.Time

	GitSHA string

	Log *slog.Logger
}

// DemoResult is what a demo run produced.
type DemoResult struct {
	Seg         aggmodel.Seg
	Manifest    aggmodel.Manifest
	Partition   aggmodel.Partition
	Counts      GateCounts
	Cells       CellOutput
	Published   PublishResult
	Seed        int64
	Matches     int
	GeneratedAt time.Time
}

// Demo writes a complete, deterministic, clearly labelled simulated artifact
// set.
//
// The generation stage is the only thing that is simulated. Everything after it
// is the production path: the same cell policy, the same gates, the same
// document assembly, the same atomic publish. That is deliberate - a demo built
// by a second, simpler code path would prove nothing about the real one.
func Demo(opts DemoOptions) (DemoResult, error) {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.OutDir == "" {
		return DemoResult{}, fmt.Errorf("demo output directory is required")
	}
	if opts.Region == "" {
		opts.Region = demoRegion
	}
	if opts.Queue == 0 {
		opts.Queue = demoQueue
	}
	if opts.Bracket == "" {
		opts.Bracket = aggmodel.BracketAll
	}
	if opts.Patch == "" {
		opts.Patch = DemoPatch
	}
	if opts.WindowEnd == "" {
		opts.WindowEnd = DemoWindowEnd
	}
	if opts.WindowDays == 0 {
		opts.WindowDays = DefaultWindowDays
	}
	if opts.MinCellN <= 0 {
		opts.MinCellN = 100
	}
	if opts.Seed == 0 {
		opts.Seed = DemoSeed
	}

	window, err := windowForEnd(opts.WindowEnd, opts.WindowDays)
	if err != nil {
		return DemoResult{}, err
	}
	generatedAt := opts.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt, err = simulatedInstant(window)
		if err != nil {
			return DemoResult{}, err
		}
	} else {
		generatedAt = generatedAt.UTC()
	}

	seg := aggmodel.Seg{Patch: opts.Patch, Region: opts.Region, Queue: opts.Queue, Bracket: opts.Bracket}
	if err := seg.Validate(); err != nil {
		return DemoResult{}, fmt.Errorf("demo segment: %w", err)
	}
	if err := refuseRealTree(opts.OutDir); err != nil {
		return DemoResult{}, err
	}

	// The demo root is readable too - the point of the demo tree is that a
	// reader takes it exactly as it takes a published one - so it is created
	// traversable, for the same reason build does it explicitly. See perms.go.
	if err := os.MkdirAll(opts.OutDir, publishedDirPerm); err != nil { //nolint:gosec // G301: the demo tree is read by whatever uid renders or serves it, which is not necessarily the uid that wrote it; see perms.go.
		return DemoResult{}, fmt.Errorf("create demo root: %w", err)
	}
	staging := filepath.Join(opts.OutDir, fmt.Sprintf("%s%d-%d", stagingPrefix, os.Getpid(), generatedAt.UnixNano()))
	// Private like the build's staging directory: nothing under it is read
	// before it is renamed into place. See perms.go.
	if err := os.MkdirAll(staging, privateDirPerm); err != nil {
		return DemoResult{}, fmt.Errorf("create demo staging directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(staging); removeErr != nil {
			opts.Log.Warn("could not remove demo staging directory", "path", staging, "error", removeErr)
		}
	}()

	tallies := simulateDemo(opts.Seed, DemoMatches, opts.MinCellN)
	state := &buildState{
		opts: BuildOptions{
			AggRoot:  opts.OutDir,
			Region:   opts.Region,
			Queue:    opts.Queue,
			Bracket:  opts.Bracket,
			Patch:    opts.Patch,
			MinCellN: opts.MinCellN,
			Gates:    DefaultGateConfig(opts.MinCellN),
			GitSHA:   opts.GitSHA,
			Log:      opts.Log,
		},
		generatedAt:   generatedAt,
		staging:       staging,
		window:        window,
		source:        aggmodel.SourceDemo,
		seg:           seg,
		cellCounts:    tallies.cells,
		banCounts:     tallies.bans,
		itemCounts:    tallies.items,
		runeCounts:    tallies.runes,
		spellCounts:   tallies.spells,
		matchupCounts: tallies.matchups,
		archiveStats:  archiveStatsRow{ArchiveRows: DemoMatches * 10},
		windowStats: windowStatsRow{
			MatchesUsed:     DemoMatches,
			ParticipantRows: tallies.participantRows,
			BanRows:         len(tallies.bans),
		},
	}

	result := DemoResult{Seg: seg, Seed: opts.Seed, Matches: DemoMatches, GeneratedAt: generatedAt}
	if err := state.checkInputGates(); err != nil {
		return result, err
	}
	if err := state.computeCells(); err != nil {
		return result, err
	}
	if err := state.checkOutputGates(); err != nil {
		return result, err
	}
	if err := state.assemble(); err != nil {
		return result, err
	}
	published := BuildResult{StagingDir: staging}
	if err := state.publishLive(&published); err != nil {
		return result, err
	}
	result.Published = published.Published
	result.Counts = state.counts
	result.Cells = state.cells
	result.Partition = state.partition
	result.Manifest = state.manifest

	if err := writeDemoNotice(opts.OutDir, opts.Seed, generatedAt); err != nil {
		return result, err
	}
	opts.Log.Warn("demo artifacts published: these numbers are simulated, not Riot data",
		"out", opts.OutDir, "partition", seg.Dir(), "source", string(aggmodel.SourceDemo),
		"cells_published", len(state.cells.Cells), "cells_suppressed", state.cells.Suppressed)
	return result, nil
}

// simulatedInstant is the instant the simulated window closes, used as
// generated_at when the caller does not supply one.
//
// It is not the wall clock, because a wall clock would make two demo runs
// differ in bytes. It is not a fabricated claim about real time either: the
// demo describes one simulated window, and this is the moment that window ends.
func simulatedInstant(window aggmodel.Window) (time.Time, error) {
	day, err := time.Parse(time.DateOnly, window.To)
	if err != nil {
		return time.Time{}, fmt.Errorf("simulated window end %q: %w", window.To, err)
	}
	return day.UTC(), nil
}

// refuseRealTree stops the demo from writing into a tree that a real build
// published.
//
// The manifest is the test rather than the directory being empty, because the
// demo must be re-runnable into its own output directory: rerunning it is how
// the determinism claim is checked.
func refuseRealTree(outDir string) error {
	manifest, err := readManifestFile(filepath.Join(outDir, aggmodel.ManifestPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("demo output tree: %w", err)
	}
	if manifest.Source != aggmodel.SourceDemo {
		return fmt.Errorf("refusing to write demo artifacts into %s: its manifest has source %q, not %q",
			outDir, manifest.Source, aggmodel.SourceDemo)
	}
	return nil
}

// writeDemoNotice drops a plain-text notice at the root of the output tree.
//
// It sits next to the tree rather than inside it on purpose: the served tree
// under v1/ keeps exactly the paths the contract freezes, with nothing added.
func writeDemoNotice(outDir string, seed int64, generatedAt time.Time) error {
	notice := strings.Join([]string{
		"SIMULATED DATA - NOT RIOT DATA",
		"",
		"This directory was written by `lolstats-aggregate demo`.",
		"Every number under v1/ is generated from a fixed pseudorandom seed; no match",
		"was played, no Riot API was called and no raw archive was read.",
		"",
		fmt.Sprintf("seed         : %d", seed),
		fmt.Sprintf("matches      : %d (simulated)", DemoMatches),
		fmt.Sprintf("generated_at : %s (simulated snapshot instant)", generatedAt.Format(time.RFC3339)),
		fmt.Sprintf("source       : %s", aggmodel.SourceDemo),
		"",
		"Every document under v1/ also carries \"source\": \"demo\" in its envelope,",
		"and v1/manifest.json carries the same value. A real build always writes",
		fmt.Sprintf("%q instead. See docs/decisions/ADR-005-demo-data-provenance.md.", aggmodel.SourceRiotMatchV5),
		"",
	}, "\n")
	return os.WriteFile(filepath.Join(outDir, DemoNoticeFile), []byte(notice), publishedFilePerm) //nolint:gosec // G306: served next to the demo tree so an operator reading the volume cannot miss it; see perms.go.
}

// demoTallies is the simulated replacement for what DuckDB would have returned.
type demoTallies struct {
	cells           []CellCount
	bans            []BanCount
	items           []BuildCount
	runes           []BuildCount
	spells          []BuildCount
	matchups        []MatchupCount
	participantRows int
}

// demoChampion is one entry of the simulated roster. The ids are real champion
// ids because a reader resolves them against the static dataset; everything
// else about the roster is invented.
type demoChampion struct {
	ID        int
	Primary   aggmodel.Role
	Secondary aggmodel.Role
}

// demoRoster is twenty champions with a primary role each and five of them with
// a second role. The second roles exist so that a champion document with more
// than one role, and a matchup matrix whose axis spans every role, are both
// exercised by the demo rather than being dead code paths.
//
// The ids are real champion ids because a reader resolves them against the
// static dataset; everything else about the roster is invented.
var demoRoster = []demoChampion{
	{ID: 86, Primary: aggmodel.RoleTop},
	{ID: 64, Primary: aggmodel.RoleJungle, Secondary: aggmodel.RoleTop},
	{ID: 103, Primary: aggmodel.RoleMid, Secondary: aggmodel.RoleJungle},
	{ID: 222, Primary: aggmodel.RoleBottom, Secondary: aggmodel.RoleSupport},
	{ID: 412, Primary: aggmodel.RoleSupport, Secondary: aggmodel.RoleJungle},
	{ID: 122, Primary: aggmodel.RoleTop},
	{ID: 254, Primary: aggmodel.RoleJungle},
	{ID: 238, Primary: aggmodel.RoleMid},
	{ID: 202, Primary: aggmodel.RoleBottom},
	{ID: 117, Primary: aggmodel.RoleSupport},
	{ID: 58, Primary: aggmodel.RoleTop},
	{ID: 121, Primary: aggmodel.RoleJungle},
	{ID: 105, Primary: aggmodel.RoleMid},
	{ID: 67, Primary: aggmodel.RoleBottom},
	{ID: 89, Primary: aggmodel.RoleSupport},
	{ID: 24, Primary: aggmodel.RoleTop},
	{ID: 203, Primary: aggmodel.RoleJungle},
	{ID: 245, Primary: aggmodel.RoleMid},
	{ID: 145, Primary: aggmodel.RoleBottom},
	{ID: 267, Primary: aggmodel.RoleSupport},
}

// simulateDemo builds the tallies the artifact assembly consumes.
//
// It is deterministic in the seed and in nothing else: no clock, no
// environment, no map iteration order that is not sorted before use. The shape
// of the numbers is chosen so the demo exercises the interesting parts of the
// policy rather than only the easy path - pick rates are spread wide enough
// that cells land on both sides of min_cell_n at a known place in every role,
// and win rates are spread wide enough to produce every tier band.
func simulateDemo(seed int64, matches, minCellN int) demoTallies {
	rng := rand.New(rand.NewSource(seed))

	slots := 2 * matches
	byRole := make(map[aggmodel.Role][]int, len(aggmodel.Roles))
	for _, champion := range demoRoster {
		byRole[champion.Primary] = append(byRole[champion.Primary], champion.ID)
		if champion.Secondary != "" {
			byRole[champion.Secondary] = append(byRole[champion.Secondary], champion.ID)
		}
	}

	var out demoTallies
	rate := map[demoCellKey]float64{}
	// The roles are visited in the canonical order and each list is in roster
	// order, so the generator consumes the same random sequence on every run.
	for _, role := range aggmodel.Roles {
		ids := byRole[role]
		quota := pickQuotas(rng, ids, slots, minCellN)
		for i, championID := range ids {
			n := quota[i]
			// Win probabilities between 0.42 and 0.58 put the tier bands
			// within reach: at n around 150 the bands are a couple of
			// percentage points apart, which is the resolution the scorer is
			// designed for.
			p := 0.42 + 0.16*rng.Float64()
			wins := successes(rng, n, p)
			out.cells = append(out.cells, CellCount{
				ChampionID: championID, Role: string(role), N: n, Wins: wins,
			})
			rate[demoCellKey{championID: championID, role: role}] = p
			out.participantRows += n
			out.items = append(out.items, demoBuildCounts(rng, championID, role, n, demoItemBuilds[role])...)
			out.runes = append(out.runes, demoBuildCounts(rng, championID, role, n, demoRuneBuilds[role])...)
			out.spells = append(out.spells, demoBuildCounts(rng, championID, role, n, demoSpellBuilds[role])...)
		}
	}

	for _, champion := range demoRoster {
		// Ban rates between 1% and 30%: wide enough that the ban column is not
		// a constant.
		bans := int(math.Round(float64(matches) * (0.01 + 0.29*rng.Float64())))
		out.bans = append(out.bans, BanCount{ChampionID: champion.ID, Bans: bans})
	}

	for _, role := range aggmodel.Roles {
		ids := byRole[role]
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				low, high := ids[i], ids[j]
				if low > high {
					low, high = high, low
				}
				// The matchup sample is a slice of the thinner side's games,
				// which is what the real query produces: only matches in which
				// both champions were in the role are pairs.
				thin := quotaOf(out.cells, low, role)
				if other := quotaOf(out.cells, high, role); other < thin {
					thin = other
				}
				n := thin/3 + rng.Intn(4)
				if n < 1 {
					n = 1
				}
				p := (rate[demoCellKey{championID: low, role: role}] +
					1 - rate[demoCellKey{championID: high, role: role}]) / 2
				p = math.Min(0.72, math.Max(0.28, p))
				out.matchups = append(out.matchups, MatchupCount{
					Role:       string(role),
					ChampionID: low,
					OpponentID: high,
					N:          n,
					Wins:       successes(rng, n, p),
				})
			}
		}
	}

	return out
}

// demoCellKey addresses one simulated (champion, role) cell, so the matchup
// generator can read back the win probability the cell was drawn from.
type demoCellKey struct {
	championID int
	role       aggmodel.Role
}

// pickQuotas splits one role's pick slots across its champions.
//
// Each share starts within a narrow band of an even split, which puts the
// majority of cells comfortably above the confidence floor, and one share is
// then moved below it. Both properties are deliberate: the demo has to exercise
// the published path and the suppressed path, and it has to do so at a known
// place rather than wherever a random draw happened to land. The rows the thin
// cell gives up are handed to the role's largest cell, so the pool is preserved
// exactly - a demo that could not reconcile would be a demo that never runs.
func pickQuotas(rng *rand.Rand, ids []int, slots, minCellN int) []int {
	quota := make([]int, len(ids))
	if len(ids) == 1 {
		quota[0] = slots
		return quota
	}

	weights := make([]float64, len(ids))
	total := 0.0
	for i := range weights {
		weights[i] = 0.75 + 0.5*rng.Float64()
		total += weights[i]
	}
	sum := 0
	for i := range ids {
		quota[i] = int(math.Round(float64(slots) * weights[i] / total))
		if quota[i] < 1 {
			quota[i] = 1
		}
		sum += quota[i]
	}

	// The rounding residual goes to one cell, which is then the natural place
	// to absorb the thin cell's rows as well. The thin cell is the last champion
	// of the role's roster, so which cell is thin is a property of the roster
	// rather than of the draw.
	thin := len(ids) - 1
	absorber := largestExcept(quota, thin)
	quota[absorber] += slots - sum

	target := minCellN/4 + 1 + rng.Intn(minCellN/4+1)
	if quota[thin] > target {
		quota[absorber] += quota[thin] - target
		quota[thin] = target
	}
	return quota
}

// largestExcept returns the position of the largest element other than skip,
// always the first such position so the choice is deterministic.
func largestExcept(values []int, skip int) int {
	best := -1
	for i, value := range values {
		if i == skip {
			continue
		}
		if best < 0 || value > values[best] {
			best = i
		}
	}
	if best < 0 {
		return 0
	}
	return best
}

// quotaOf returns the simulated sample size of one champion in one role.
func quotaOf(cells []CellCount, championID int, role aggmodel.Role) int {
	for _, cell := range cells {
		if cell.ChampionID == championID && cell.Role == string(role) {
			return cell.N
		}
	}
	return 1
}

// successes simulates n trials at probability p. It is the only place the
// generator draws more than one number per cell, so the sequence stays cheap and
// stable.
func successes(rng *rand.Rand, n int, p float64) int {
	wins := 0
	for i := 0; i < n; i++ {
		if rng.Float64() < p {
			wins++
		}
	}
	return wins
}

// demoBuildCounts splits a cell's games across the simulated build variants of
// one kind.
//
// The split is deterministic in the random stream and the variants come from a
// fixed per-role table, so a demo champion page always shows the same item set
// for the same champion.
func demoBuildCounts(rng *rand.Rand, championID int, role aggmodel.Role, n int, variants [][]int) []BuildCount {
	if len(variants) == 0 {
		return nil
	}
	weights := make([]float64, len(variants))
	total := 0.0
	for i := range variants {
		weights[i] = 0.4 + 2.6*rng.Float64()
		total += weights[i]
	}
	counts := make([]BuildCount, 0, len(variants))
	used := 0
	for i, variant := range variants {
		share := int(math.Round(float64(n) * weights[i] / total))
		if share < 1 {
			share = 1
		}
		used += share
		counts = append(counts, BuildCount{
			ChampionID: championID,
			Role:       string(role),
			Key:        append([]int{}, variant...),
			N:          share,
			Wins:       successes(rng, share, 0.44+0.12*rng.Float64()),
		})
	}
	// Rounding can make the variants describe a few more games than the cell
	// contains. Scaling the largest one back keeps every variant a subset of
	// its cell, which is the invariant a reader would check first.
	if used > n {
		counts[0].N -= used - n
		if counts[0].N < 1 {
			counts[0].N = 1
		}
	}
	return counts
}

// The simulated build tables. Item ids are the seven item slots in purchase
// order, rune keys are [primary tree, keystone, secondary tree, three shards]
// and spell keys are [first, second].
var (
	demoItemBuilds = map[aggmodel.Role][][]int{
		aggmodel.RoleTop: {
			{3078, 3047, 6333, 3053, 3071, 3068, 3026},
			{3078, 3111, 6333, 3053, 3143, 3068, 3026},
			{3078, 3047, 6333, 3053, 3071, 3135, 3026},
		},
		aggmodel.RoleJungle: {
			{6692, 3047, 6333, 3053, 3071, 3068, 3026},
			{6692, 3111, 6333, 3053, 3143, 3068, 3026},
			{3078, 3047, 6333, 3053, 3071, 3026, 3068},
		},
		aggmodel.RoleMid: {
			{6655, 3020, 4645, 3089, 3135, 3157, 3165},
			{6655, 3020, 4645, 3089, 3157, 3135, 3165},
			{6653, 3020, 3165, 3089, 3135, 3157, 4645},
		},
		aggmodel.RoleBottom: {
			{6672, 3006, 3031, 6675, 3036, 3072, 3026},
			{6672, 3006, 3031, 6675, 3036, 3026, 3072},
			{6672, 3006, 6676, 3031, 6675, 3036, 3072},
		},
		aggmodel.RoleSupport: {
			{3869, 3158, 2065, 3107, 3190, 3222, 4005},
			{3877, 3158, 2065, 3107, 3190, 3222, 4005},
			{3865, 3158, 3870, 3107, 3190, 3222, 4005},
		},
	}
	demoRuneBuilds = map[aggmodel.Role][][]int{
		aggmodel.RoleTop: {
			{8000, 8010, 8400, 5008, 5008, 5001},
			{8000, 8005, 8400, 5005, 5008, 5001},
			{8400, 8437, 8000, 5008, 5001, 5001},
		},
		aggmodel.RoleJungle: {
			{8000, 8010, 8100, 5005, 5008, 5001},
			{8100, 8112, 8000, 5005, 5008, 5001},
			{8000, 8021, 8300, 5005, 5008, 5001},
		},
		aggmodel.RoleMid: {
			{8200, 8229, 8100, 5008, 5008, 5001},
			{8100, 8112, 8200, 5008, 5008, 5001},
			{8200, 8214, 8300, 5008, 5007, 5001},
		},
		aggmodel.RoleBottom: {
			{8000, 8008, 8300, 5005, 5008, 5001},
			{8000, 8021, 8400, 5005, 5008, 5001},
			{8000, 8010, 8200, 5005, 5008, 5001},
		},
		aggmodel.RoleSupport: {
			{8400, 8465, 8300, 5008, 5002, 5001},
			{8300, 8360, 8400, 5008, 5003, 5001},
			{8400, 8439, 8300, 5008, 5002, 5001},
		},
	}
	demoSpellBuilds = map[aggmodel.Role][][]int{
		aggmodel.RoleTop:     {{4, 12}, {4, 14}, {4, 11}},
		aggmodel.RoleJungle:  {{4, 11}, {4, 12}},
		aggmodel.RoleMid:     {{4, 12}, {4, 14}, {4, 21}},
		aggmodel.RoleBottom:  {{4, 7}, {4, 21}, {4, 14}},
		aggmodel.RoleSupport: {{4, 14}, {4, 3}, {4, 7}},
	}
)
