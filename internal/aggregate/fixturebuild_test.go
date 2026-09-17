package aggregate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
)

// The tests in this file run the real build end to end against the fixture
// archive in fixtures/agg: DuckDB reads the payloads, the SQL extracts the
// features, the Go policy computes the cells, and the publisher writes the
// frozen paths.
//
// The archive is checked in as newline-delimited JSON so that a reviewer can
// read it in a diff. The build reads parquet, which is the format the crawler
// writes, so the tests materialise the parts with the same pinned DuckDB client
// the build uses. That conversion is also the assertion that the pinned version
// is the one doing the work: no binary parquet is committed, so a machine with a
// different DuckDB changes nothing on disk.
//
// Tests that need the client skip rather than fail when it is not on the
// machine: a contributor without it can still run everything that does not
// touch SQL, and CI has the pinned release in the image.

// duckDBBin returns the pinned client or skips the test.
func duckDBBin(t *testing.T) string {
	t.Helper()

	bin, err := FindDuckDBBin(os.Getenv(DuckDBBinEnv))
	if err != nil {
		t.Skipf("pinned DuckDB client unavailable (%v); set %s to run this test",
			err, DuckDBBinEnv)
	}
	return bin
}

// runDuckDB runs one statement through the client, the same way the engine
// does: batch mode, no init file, statement on standard input.
func runDuckDB(t *testing.T, bin, statement string) {
	t.Helper()

	cmd := exec.Command(bin, "-batch", "-init", "/dev/null")
	cmd.Stdin = strings.NewReader(statement + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("duckdb: %v\nstatement:\n%s\nstderr:\n%s", err, statement, stderr.String())
	}
}

// jsonlToParquetSQL converts a newline-delimited JSON file into a one-column
// parquet part whose column is `payload`.
//
// `read_blob` plus a split on newlines is the conversion that survives every
// payload the fixture holds, including the empty object the corrupt fixture
// carries: DuckDB's JSON reader would rather fail the statement than hand back a
// payload it cannot type, and the build has to be able to count such a row and
// fail closed on its own terms.
func jsonlToParquetSQL(jsonl, out string) string {
	return fmt.Sprintf(`COPY (
  SELECT payload
  FROM (SELECT unnest(string_split(decode(content), chr(10))) AS payload
        FROM read_blob(%s))
  WHERE length(payload) > 0
) TO %s (FORMAT PARQUET, COMPRESSION ZSTD);`, quoteLiteral(jsonl), quoteLiteral(out))
}

// fixtureRawRoot materialises a fixture set as the archive layout the build
// reads and returns the raw root.
func fixtureRawRoot(t *testing.T, files map[string]string) string {
	t.Helper()

	bin := duckDBBin(t)
	root := t.TempDir()
	for partition, body := range files {
		dir := filepath.Join(root, "riot", rawSourceDir, rawPartitionPr+partition)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create partition: %v", err)
		}
		jsonl := filepath.Join(dir, "matches.jsonl")
		if err := os.WriteFile(jsonl, []byte(body), 0o644); err != nil {
			t.Fatalf("write fixture jsonl: %v", err)
		}
		out := filepath.Join(dir, "part-00001.parquet")
		runDuckDB(t, bin, jsonlToParquetSQL(jsonl, out))
		if err := os.Remove(jsonl); err != nil {
			t.Fatalf("remove fixture jsonl: %v", err)
		}
	}
	return root
}

// fixtureBuildOptions is the build under test: the fixture window, the fixture
// floor, and a fixed clock so that generated_at is reproducible.
func fixtureBuildOptions(t *testing.T, aggRoot, rawRoot string) BuildOptions {
	t.Helper()

	return BuildOptions{
		AggRoot:   aggRoot,
		RawRoot:   rawRoot,
		Region:    "EUW",
		Queue:     aggmodel.QueueIDRankedSolo5x5,
		Bracket:   aggmodel.BracketAll,
		WindowEnd: fixtureWindowEnd,
		// The window is given as an end date plus a length, which is how the
		// CLI takes it, so the test exercises the same date arithmetic.
		WindowDays: fixtureWindowDays,
		MinCellN:   fixtureMinCellN,
		DuckDBBin:  duckDBBin(t),
		GitSHA:     fixtureGitSHA,
		Now:        func() time.Time { return fixtureNow },
		Metrics:    obs.NopRecorder{},
	}
}

const (
	fixtureGitSHA = "0123456789abcdef0123456789abcdef01234567"
)

var fixtureNow = time.Date(2026, 9, 14, 23, 30, 0, 0, time.UTC)

// fixtureExpectedCells is the hand-computed tier list for the fixture archive.
//
// The nine matches F01..F08 and F13 are the ones the build may use. They all
// satisfy EUW1/420, patch 16.18 and the window 2026-09-01..2026-09-14, so the
// denominator of pick_rate and ban_rate is 9, and every rate below is a
// fraction with a 9, a 6 or an 18 in the denominator.
//
// The team 100 composition is 24/64/134/202/111 except in F04 (top lane 11) and
// F06, F07 (top lane 13). The team 200 composition is 86/121/1/145/412 except in
// F08, whose top lane is 12. Team 100 won F01..F05 and team 200 won F06, F07,
// F08 and F13.
//
// That gives, per champion and role:
//
//	24  top      n=6 (F04 and F06/F07 displaced it)  wins=4 (F01,F02,F03,F05)
//	86  top      n=8 (F08's top lane is 12)          wins=3 (F06,F07,F13)
//	13  top      n=2 (F06,F07)                       wins=0
//	11  top      n=1 (F04)                           below the floor
//	12  top      n=1 (F08)                           below the floor
//	64  134  202  111   n=9 each                     wins=5 each
//	121  1  145  412    n=9 each                     wins=4 each
//
// The baseline the tier bands are measured against is the win rate over every
// participant row, which is exactly one win per two players: 45/90 = 0.50. So
// 0.6667 and 0.5556 are far above the +2.0pp band and score S+, while 0.4444 is
// 5.6pp below the baseline and 0.375 is 12.5pp below it, so both score D.
//
// pick_rate is n/(2*9) and ban_rate is the number of the nine matches the
// champion was banned in over 9: champion 24 is banned in F04, F06 and F07, and
// champion 86 in F08. A champion can only be banned in a match it did not play,
// which is why no other published champion has a ban.
//
// The half width is 0.98/sqrt(n) rounded to four places.
var fixtureExpectedCells = []aggmodel.Cell{
	{ChampionID: 24, Role: aggmodel.RoleTop, N: 6, Wins: 4,
		WinRate: 0.6667, PickRate: 0.3333, BanRate: 0.3333,
		Tier: aggmodel.TierSPlus, CI95HalfWidth: 0.4001},
	{ChampionID: 86, Role: aggmodel.RoleTop, N: 8, Wins: 3,
		WinRate: 0.375, PickRate: 0.4444, BanRate: 0.1111,
		Tier: aggmodel.TierD, CI95HalfWidth: 0.3465},
	{ChampionID: 13, Role: aggmodel.RoleTop, N: 2, Wins: 0,
		WinRate: 0, PickRate: 0.1111, BanRate: 0,
		Tier: aggmodel.TierD, CI95HalfWidth: 0.693},
	{ChampionID: 64, Role: aggmodel.RoleJungle, N: 9, Wins: 5,
		WinRate: 0.5556, PickRate: 0.5, BanRate: 0,
		Tier: aggmodel.TierSPlus, CI95HalfWidth: 0.3267},
	{ChampionID: 121, Role: aggmodel.RoleJungle, N: 9, Wins: 4,
		WinRate: 0.4444, PickRate: 0.5, BanRate: 0,
		Tier: aggmodel.TierD, CI95HalfWidth: 0.3267},
	{ChampionID: 134, Role: aggmodel.RoleMid, N: 9, Wins: 5,
		WinRate: 0.5556, PickRate: 0.5, BanRate: 0,
		Tier: aggmodel.TierSPlus, CI95HalfWidth: 0.3267},
	{ChampionID: 1, Role: aggmodel.RoleMid, N: 9, Wins: 4,
		WinRate: 0.4444, PickRate: 0.5, BanRate: 0,
		Tier: aggmodel.TierD, CI95HalfWidth: 0.3267},
	{ChampionID: 202, Role: aggmodel.RoleBottom, N: 9, Wins: 5,
		WinRate: 0.5556, PickRate: 0.5, BanRate: 0,
		Tier: aggmodel.TierSPlus, CI95HalfWidth: 0.3267},
	{ChampionID: 145, Role: aggmodel.RoleBottom, N: 9, Wins: 4,
		WinRate: 0.4444, PickRate: 0.5, BanRate: 0,
		Tier: aggmodel.TierD, CI95HalfWidth: 0.3267},
	{ChampionID: 111, Role: aggmodel.RoleSupport, N: 9, Wins: 5,
		WinRate: 0.5556, PickRate: 0.5, BanRate: 0,
		Tier: aggmodel.TierSPlus, CI95HalfWidth: 0.3267},
	{ChampionID: 412, Role: aggmodel.RoleSupport, N: 9, Wins: 4,
		WinRate: 0.4444, PickRate: 0.5, BanRate: 0,
		Tier: aggmodel.TierD, CI95HalfWidth: 0.3267},
}

// fixtureExpectedCounts is the gate evidence the hand computation predicts.
var fixtureExpectedCounts = GateCounts{
	ArchiveRows:     14,
	MalformedRows:   0,
	MatchesUsed:     9,
	ParticipantRows: 90,
	RejectedRows:    0,
	ClassifiedRows:  90,
	CellsTotal:      13,
	CellsPublished:  11,
	CellsSuppressed: 2,
	// SumN counts the suppressed cells too: two lanes of one game each.
	SumN: 90,
}

// fixtureExpectedChampions is every champion the window saw, ascending,
// including the two whose only cells were suppressed.
var fixtureExpectedChampions = []int{1, 11, 12, 13, 24, 64, 86, 111, 121, 134, 145, 202, 412}

// fixtureExpectedWindow is the window WindowEnd plus WindowDays resolves to.
// The window is inclusive at both ends, so fourteen days ending on 2026-09-14
// start on 2026-09-01.
var fixtureExpectedWindow = aggmodel.Window{From: "2026-09-01", To: "2026-09-14"}

// requireCells compares a published tier list with the hand computation.
func requireCells(t *testing.T, label string, got, want []aggmodel.Cell) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s: published %d cells, want %d\ngot:  %s\nwant: %s",
			label, len(got), len(want), formatCells(got), formatCells(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: cell %d is %s, want %s", label, i, formatCell(got[i]), formatCell(want[i]))
		}
	}
}

func formatCells(cells []aggmodel.Cell) string {
	parts := make([]string, 0, len(cells))
	for _, cell := range cells {
		parts = append(parts, formatCell(cell))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatCell(cell aggmodel.Cell) string {
	return fmt.Sprintf("{champion_id:%d role:%s n:%d wins:%d win_rate:%g pick_rate:%g ban_rate:%g tier:%s ci95_half_width:%g}",
		cell.ChampionID, cell.Role, cell.N, cell.Wins, cell.WinRate, cell.PickRate,
		cell.BanRate, cell.Tier, cell.CI95HalfWidth)
}

// TestBuildAgainstHandComputedFixture runs the whole build against the fixture
// archive and compares every published number with the table above.
//
// This is the test that makes the rest meaningful: if the SQL picked up the
// wrong denominator, the wrong team or the wrong patch, one of these cells would
// move. It also pins the artifact paths, the champions the manifest lists for
// prerendering, the matchup matrices and the item, rune and spell groups.
func TestBuildAgainstHandComputedFixture(t *testing.T) {
	t.Parallel()

	rawRoot := fixtureRawRoot(t, fixtureFiles(fixtureMatches()))
	aggRoot := t.TempDir()
	auditRoot := filepath.Join(t.TempDir(), "build-runs")

	opts := fixtureBuildOptions(t, aggRoot, rawRoot)
	opts.Auditor = FileAuditor{Root: auditRoot}

	result, err := Build(context.Background(), opts)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	if want := (aggmodel.Seg{
		Patch: "16.18", Region: "EUW",
		Queue: aggmodel.QueueIDRankedSolo5x5, Bracket: aggmodel.BracketAll,
	}); result.Seg != want {
		t.Errorf("segment is %+v, want %+v", result.Seg, want)
	}
	if result.Counts != fixtureExpectedCounts {
		t.Errorf("gate counts are %+v, want %+v", result.Counts, fixtureExpectedCounts)
	}
	requireCells(t, "tier list", result.Cells.Cells, fixtureExpectedCells)

	if got := result.Cells.ChampionsAscending; !equalInts(got, fixtureExpectedChampions) {
		t.Errorf("champions ascending are %v, want %v", got, fixtureExpectedChampions)
	}

	checkPartitionDocument(t, result, aggRoot)
	checkTierListDocument(t, result, aggRoot)
	checkChampionDocuments(t, result, aggRoot)
	checkMatchupDocuments(t, result, aggRoot)
	checkAuditRow(t, result, auditRoot)
}

// checkPartitionDocument pins the manifest and the partition it describes.
func checkPartitionDocument(t *testing.T, result BuildResult, aggRoot string) {
	t.Helper()

	manifest := result.Manifest
	if manifest.Source != aggmodel.SourceRiotMatchV5 {
		t.Errorf("manifest source is %q, want %q", manifest.Source, aggmodel.SourceRiotMatchV5)
	}
	if manifest.Schema != aggmodel.SchemaVersion {
		t.Errorf("manifest schema is %d, want %d", manifest.Schema, aggmodel.SchemaVersion)
	}
	if want := fixtureNow; !manifest.GeneratedAt.Equal(want) {
		t.Errorf("manifest generated_at is %s, want %s", manifest.GeneratedAt, want)
	}
	if len(manifest.Partitions) != 1 {
		t.Fatalf("manifest lists %d partitions, want 1", len(manifest.Partitions))
	}
	if got, want := manifest.Latest, manifest.Partitions[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("manifest latest is %+v, want the only partition %+v", got, want)
	}

	partition := manifest.Partitions[0]
	if partition.SourceWindow != fixtureExpectedWindow {
		t.Errorf("source window is %+v, want %+v", partition.SourceWindow, fixtureExpectedWindow)
	}
	if partition.CellsPublished != 11 || partition.SuppressedCells != 2 {
		t.Errorf("partition publishes %d cells and suppresses %d, want 11 and 2",
			partition.CellsPublished, partition.SuppressedCells)
	}
	if partition.MinCellN != fixtureMinCellN {
		t.Errorf("partition min_cell_n is %d, want %d", partition.MinCellN, fixtureMinCellN)
	}
	if partition.GitSHA != fixtureGitSHA {
		t.Errorf("partition git_sha is %q, want %q", partition.GitSHA, fixtureGitSHA)
	}
	if partition.BuildRunID != result.BuildRunID || partition.BuildRunID == 0 {
		t.Errorf("partition build_run_id is %d, want the audit row %d", partition.BuildRunID, result.BuildRunID)
	}
	if got := partition.Champions; !equalInts(got, fixtureExpectedChampions) {
		t.Errorf("partition champions are %v, want %v", got, fixtureExpectedChampions)
	}
	roles := make([]string, 0, len(partition.MatchupRoles))
	for _, role := range partition.MatchupRoles {
		roles = append(roles, string(role))
	}
	wantRoles := make([]string, 0, len(aggmodel.Roles))
	for _, role := range aggmodel.Roles {
		wantRoles = append(wantRoles, string(role))
	}
	if !equalStrings(roles, wantRoles) {
		t.Errorf("partition matchup roles are %v, want %v", roles, wantRoles)
	}

	// The published tree holds exactly the frozen paths and nothing else: a
	// stray file inside the served directory is a path the contract does not
	// describe.
	want := []string{aggmodel.ManifestPath}
	want = append(want, result.Seg.TierListPath())
	for _, championID := range fixtureExpectedChampions {
		want = append(want, result.Seg.ChampionPath(championID))
	}
	for _, role := range aggmodel.Roles {
		want = append(want, result.Seg.MatchupsPath(role))
	}
	if got := relativeFiles(t, aggRoot); !equalStrings(got, sortedCopy(want)) {
		t.Errorf("published tree holds:\n%s\nwant:\n%s", strings.Join(got, "\n"),
			strings.Join(sortedCopy(want), "\n"))
	}
}

// checkTierListDocument re-reads the tier list off disk and compares it with the
// cells the build reported, so the published file is what was audited.
func checkTierListDocument(t *testing.T, result BuildResult, aggRoot string) {
	t.Helper()

	var tierList aggmodel.TierList
	readDocument(t, filepath.Join(aggRoot, filepath.FromSlash(result.Seg.TierListPath())), &tierList)
	requireCells(t, "published tierlist.json", tierList.Cells, fixtureExpectedCells)

	if tierList.Patch != result.Seg.Patch || tierList.Region != result.Seg.Region {
		t.Errorf("tierlist envelope is %s/%s, want %s/%s",
			tierList.Patch, tierList.Region, result.Seg.Patch, result.Seg.Region)
	}
	if tierList.Queue != aggmodel.QueueIDRankedSolo5x5 || tierList.Bracket != aggmodel.BracketAll {
		t.Errorf("tierlist queue/bracket are %d/%s, want 420/all", tierList.Queue, tierList.Bracket)
	}
	if tierList.Source != aggmodel.SourceRiotMatchV5 {
		t.Errorf("tierlist source is %q, want %q", tierList.Source, aggmodel.SourceRiotMatchV5)
	}
	if tierList.MinCellN != fixtureMinCellN || tierList.SuppressedCells != 2 {
		t.Errorf("tierlist min_cell_n/suppressed_cells are %d/%d, want %d/2",
			tierList.MinCellN, tierList.SuppressedCells, fixtureMinCellN)
	}
	if tierList.SourceWindow != fixtureExpectedWindow {
		t.Errorf("tierlist source window is %+v, want %+v", tierList.SourceWindow, fixtureExpectedWindow)
	}
}

// readDocument decodes one published artifact.
func readDocument(t *testing.T, path string, target any) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

// relativeFiles lists every file under root, slash-separated and sorted.
func relativeFiles(t *testing.T, root string) []string {
	t.Helper()

	var out []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// fixtureBuildGroupSizes is the sample size of each item, rune and spell group
// on each published champion page.
//
// Two of them are smaller than the cell they belong to, which is the point:
//
//   - champion 24 in the top lane has six participations, and five rune builds.
//     The sixth is F05, whose perk styles are not in the documented order, so
//     the build refuses the key rather than guessing which tree is primary.
//   - champion 121 in the jungle has nine participations and eight spell builds.
//     The ninth is F07, whose second summoner spell id is zero.
//   - champion 202 in the bottom lane has nine participations and eight item
//     builds. The ninth is F04, whose item slots are all zero, which is what an
//     unparsed or truncated participant row looks like.
var fixtureBuildGroupSizes = map[[2]any][3]int{
	{24, aggmodel.RoleTop}:      {6, 5, 6},
	{86, aggmodel.RoleTop}:      {8, 8, 8},
	{13, aggmodel.RoleTop}:      {2, 2, 2},
	{64, aggmodel.RoleJungle}:   {9, 9, 9},
	{121, aggmodel.RoleJungle}:  {9, 9, 8},
	{134, aggmodel.RoleMid}:     {9, 9, 9},
	{1, aggmodel.RoleMid}:       {9, 9, 9},
	{202, aggmodel.RoleBottom}:  {8, 9, 9},
	{145, aggmodel.RoleBottom}:  {9, 9, 9},
	{111, aggmodel.RoleSupport}: {9, 9, 9},
	{412, aggmodel.RoleSupport}: {9, 9, 9},
}

// checkChampionDocuments reads every champion page and checks the roles, the
// stats and the three build groups on it.
func checkChampionDocuments(t *testing.T, result BuildResult, aggRoot string) {
	t.Helper()

	published := map[[2]any]aggmodel.Cell{}
	for _, cell := range fixtureExpectedCells {
		published[[2]any{cell.ChampionID, cell.Role}] = cell
	}

	for _, championID := range fixtureExpectedChampions {
		var champion aggmodel.Champion
		readDocument(t, filepath.Join(aggRoot, filepath.FromSlash(result.Seg.ChampionPath(championID))), &champion)

		if champion.ChampionID != championID {
			t.Errorf("champion page %d says champion_id %d", championID, champion.ChampionID)
		}
		if champion.Source != aggmodel.SourceRiotMatchV5 {
			t.Errorf("champion page %d carries source %q", championID, champion.Source)
		}
		// Skill orders come from timelines, which this build never reads.
		for _, role := range champion.Roles {
			if len(role.SkillOrders) != 0 {
				t.Errorf("champion %d %s carries %d skill orders, want none without timelines",
					championID, role.Role, len(role.SkillOrders))
			}
		}

		want := 0
		for _, role := range aggmodel.Roles {
			if _, ok := published[[2]any{championID, role}]; ok {
				want++
			}
		}
		if len(champion.Roles) != want {
			t.Errorf("champion page %d lists %d roles, want %d", championID, len(champion.Roles), want)
		}
		for _, role := range champion.Roles {
			cell, ok := published[[2]any{championID, role.Role}]
			if !ok {
				t.Errorf("champion page %d lists role %s, which has no published cell", championID, role.Role)
				continue
			}
			if role.Stats != cell {
				t.Errorf("champion %d %s stats are %s, want %s",
					championID, role.Role, formatCell(role.Stats), formatCell(cell))
			}
			sizes := fixtureBuildGroupSizes[[2]any{championID, role.Role}]
			checkBuildGroups(t, championID, role, sizes)
		}
	}
}

// checkBuildGroups checks the three groups of one champion page against the
// expected sample sizes.
//
// Every fixture participant buys the same items, takes the same runes and
// carries the same spells, so each group holds exactly one build: the check is
// the size of that build, not its membership.
func checkBuildGroups(t *testing.T, championID int, role aggmodel.ChampionRole, want [3]int) {
	t.Helper()

	for i, group := range []struct {
		kind   string
		builds []aggmodel.Build
	}{
		{BuildKindItems, role.Items},
		{BuildKindRunes, role.Runes},
		{BuildKindSpells, role.Spells},
	} {
		label := fmt.Sprintf("champion %d %s %s", championID, role.Role, group.kind)
		if len(group.builds) != 1 {
			t.Errorf("%s: %d builds, want 1 (all fixture participants build alike)",
				label, len(group.builds))
			continue
		}
		build := group.builds[0]
		if build.Kind != group.kind {
			t.Errorf("%s: kind is %q, want %q", label, build.Kind, group.kind)
		}
		if build.N != want[i] {
			t.Errorf("%s: n is %d, want %d", label, build.N, want[i])
		}
		if build.N > role.Stats.N {
			t.Errorf("%s: n is %d, above the cell sample of %d", label, build.N, role.Stats.N)
		}
		if len(build.Key) == 0 {
			t.Errorf("%s: build key is empty", label)
			continue
		}
		if prefix := fmt.Sprintf("%d", build.Key[0]); !strings.HasPrefix(build.Label, prefix) {
			t.Errorf("%s: label %q does not start with the key %v", label, build.Label, build.Key)
		}
	}
}

// fixtureExpectedMatchups is the published matchup matrix for each role.
//
// Only the direction with the lower champion id is published, and only pairs
// that reach the confidence floor survive: the top lane has two pairs above it
// (11 against 86 and 12 against 24 each happened once), and the other four roles
// have exactly one pair each, because both teams field the same composition in
// every counted match.
var fixtureExpectedMatchups = map[aggmodel.Role][]aggmodel.MatchupCell{
	aggmodel.RoleTop: {
		{ChampionID: 13, OpponentID: 86, N: 2, Wins: 0,
			WinRate: 0, CI95HalfWidth: 0.693},
		{ChampionID: 24, OpponentID: 86, N: 5, Wins: 4,
			WinRate: 0.8, CI95HalfWidth: 0.4383},
	},
	aggmodel.RoleJungle: {
		{ChampionID: 64, OpponentID: 121, N: 9, Wins: 5,
			WinRate: 0.5556, CI95HalfWidth: 0.3267},
	},
	aggmodel.RoleMid: {
		{ChampionID: 1, OpponentID: 134, N: 9, Wins: 4,
			WinRate: 0.4444, CI95HalfWidth: 0.3267},
	},
	aggmodel.RoleBottom: {
		{ChampionID: 145, OpponentID: 202, N: 9, Wins: 4,
			WinRate: 0.4444, CI95HalfWidth: 0.3267},
	},
	aggmodel.RoleSupport: {
		{ChampionID: 111, OpponentID: 412, N: 9, Wins: 5,
			WinRate: 0.5556, CI95HalfWidth: 0.3267},
	},
}

// checkMatchupDocuments reads one matrix per role and compares it with the hand
// computation.
func checkMatchupDocuments(t *testing.T, result BuildResult, aggRoot string) {
	t.Helper()

	for _, role := range aggmodel.Roles {
		var matchups aggmodel.Matchups
		readDocument(t, filepath.Join(aggRoot, filepath.FromSlash(result.Seg.MatchupsPath(role))), &matchups)

		if matchups.Role != role {
			t.Errorf("matchups for %s carry role %s", role, matchups.Role)
		}
		want := fixtureExpectedMatchups[role]
		if len(matchups.Cells) != len(want) {
			t.Errorf("matchups for %s hold %d cells, want %d: %+v",
				role, len(matchups.Cells), len(want), matchups.Cells)
			continue
		}
		for i := range want {
			if matchups.Cells[i] != want[i] {
				t.Errorf("matchups for %s cell %d is %+v, want %+v",
					role, i, matchups.Cells[i], want[i])
			}
		}
		for _, cell := range matchups.Cells {
			if cell.N < fixtureMinCellN {
				t.Errorf("matchups for %s publish a cell with n=%d, below min_cell_n=%d",
					role, cell.N, fixtureMinCellN)
			}
		}
		// The champion axis keeps the champions whose pairs were suppressed,
		// so a heatmap does not silently reorder between builds.
		seen := map[int]bool{}
		for _, cell := range matchups.Cells {
			seen[cell.ChampionID] = true
			seen[cell.OpponentID] = true
		}
		for championID := range seen {
			if !containsInt(matchups.Champions, championID) {
				t.Errorf("matchups for %s omit champion %d from the axis", role, championID)
			}
		}
		if !sort.IntsAreSorted(matchups.Champions) {
			t.Errorf("matchups for %s list champions out of order: %v", role, matchups.Champions)
		}
	}
}

// checkAuditRow reads the build_runs record the build wrote and checks it
// describes the run that happened.
func checkAuditRow(t *testing.T, result BuildResult, auditRoot string) {
	t.Helper()

	if result.BuildRunID == 0 {
		t.Fatal("build reported no build run id")
	}
	record := readAuditRecord(t, auditRoot, result.BuildRunID)

	for field, want := range map[string]any{
		"status":           "ok",
		"cells_total":      float64(fixtureExpectedCounts.CellsTotal),
		"cells_published":  float64(fixtureExpectedCounts.CellsPublished),
		"cells_suppressed": float64(fixtureExpectedCounts.CellsSuppressed),
		"git_sha":          fixtureGitSHA,
		"region":           "EUW",
		"queue":            float64(aggmodel.QueueIDRankedSolo5x5),
		"bracket":          "all",
		"artifact_uri":     ArtifactURI(result.Seg.Dir()),
		"error":            "",
	} {
		if got := record[field]; got != want {
			t.Errorf("audit row %s is %v, want %v", field, got, want)
		}
	}
	for _, field := range []string{"started_at", "finished_at"} {
		stamp, ok := record[field].(string)
		if !ok || stamp == "" {
			t.Fatalf("audit row %s is %v, want a timestamp", field, record[field])
		}
		if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
			t.Errorf("audit row %s is %q: %v", field, stamp, err)
		}
	}
}

// readAuditRecord reads one build run record written by the file auditor.
func readAuditRecord(t *testing.T, auditRoot string, id int64) map[string]any {
	t.Helper()

	path := filepath.Join(auditRoot, fmt.Sprintf("build-run-%d.json", id))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit record: %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode audit record %s: %v", path, err)
	}
	return record
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsInt(haystack []int, needle int) bool {
	for _, value := range haystack {
		if value == needle {
			return true
		}
	}
	return false
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

// treeDigest is the content of every file under root, keyed by relative path,
// with a digest of the whole tree. It is how the fail-closed tests prove that a
// rejected build changed nothing rather than changed it back.
type treeDigest struct {
	Files map[string]string
	Tree  string
}

func hashTree(t *testing.T, root string) treeDigest {
	t.Helper()

	digest := treeDigest{Files: map[string]string{}}
	combined := sha256.New()
	for _, rel := range relativeFiles(t, root) {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		sum := sha256.Sum256(raw)
		digest.Files[rel] = hex.EncodeToString(sum[:])
		fmt.Fprintf(combined, "%s %s\n", rel, hex.EncodeToString(sum[:]))
	}
	digest.Tree = hex.EncodeToString(combined.Sum(nil))
	return digest
}

// requireSameTree fails when two digests differ, and names the files that moved.
func requireSameTree(t *testing.T, label string, before, after treeDigest) {
	t.Helper()

	if before.Tree == after.Tree {
		return
	}
	t.Errorf("%s: the published tree changed", label)
	for path, sum := range before.Files {
		other, ok := after.Files[path]
		switch {
		case !ok:
			t.Errorf("%s: %s was removed", label, path)
		case other != sum:
			t.Errorf("%s: %s was rewritten", label, path)
		}
	}
	for path := range after.Files {
		if _, ok := before.Files[path]; !ok {
			t.Errorf("%s: %s appeared", label, path)
		}
	}
}

// TestFixtureSuppressionBoundary walks min_cell_n across the two sample sizes
// the fixture is built around.
//
// The rule is exact and inclusive: n equal to min_cell_n publishes, n one below
// it is suppressed and counted. The fixture carries a cell of exactly two and
// two cells of exactly one, so the boundary can be observed from both sides
// without inventing data: at a floor of two the cell of two is published, and at
// a floor of three it is suppressed and the counters move by exactly one.
func TestFixtureSuppressionBoundary(t *testing.T) {
	t.Parallel()

	rawRoot := fixtureRawRoot(t, fixtureFiles(fixtureMatches()))

	cases := []struct {
		name            string
		minCellN        int
		wantCells       int
		wantSuppressed  int
		wantCellOf1     bool
		wantCellOf2     bool
		wantChampions   int
		wantBuildFails  bool
		wantFailureWrap error
	}{
		{
			// A floor of one suppresses nothing: the two champions played once
			// are published with their sample size of one, which is exactly the
			// "rate without a sample size" the floor exists to prevent.
			name: "floor of one publishes everything", minCellN: 1,
			wantCells: 13, wantSuppressed: 0, wantCellOf1: true, wantCellOf2: true,
			wantChampions: 13,
		},
		{
			// The fixture floor: a sample of one is suppressed and a sample of
			// two is published.
			name: "floor of two publishes the cell of two", minCellN: 2,
			wantCells: 13, wantSuppressed: 2, wantCellOf1: false, wantCellOf2: true,
			wantChampions: 13,
		},
		{
			// One above the fixture floor: the cell of two joins the suppressed
			// ones, and the tree still publishes, because ten of thirteen cells
			// clear the floor.
			name: "floor of three suppresses the cell of two", minCellN: 3,
			wantCells: 13, wantSuppressed: 3, wantCellOf1: false, wantCellOf2: false,
			wantChampions: 13,
		},
		{
			// Ten publishes nothing: nine is the largest sample in the fixture,
			// so the build fails closed rather than publishing an empty tier
			// list.
			name: "floor above every sample fails closed", minCellN: 10,
			wantCells: 13, wantSuppressed: 13, wantChampions: 13,
			wantBuildFails: true, wantFailureWrap: ErrNoPublishedCells,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			aggRoot := t.TempDir()
			opts := fixtureBuildOptions(t, aggRoot, rawRoot)
			opts.MinCellN = tc.minCellN
			opts.Gates = DefaultGateConfig(tc.minCellN)

			result, err := Build(context.Background(), opts)
			if tc.wantBuildFails {
				if err == nil {
					t.Fatalf("build succeeded, want a failure wrapping %v", tc.wantFailureWrap)
				}
				if !errors.Is(err, tc.wantFailureWrap) {
					t.Fatalf("build failed with %v, want a failure wrapping %v", err, tc.wantFailureWrap)
				}
				if got := relativeFiles(t, aggRoot); len(got) != 0 {
					t.Fatalf("a failed build published %v", got)
				}
			} else if err != nil {
				t.Fatalf("build failed: %v", err)
			}

			if result.Counts.CellsTotal != tc.wantCells {
				t.Errorf("cells_total is %d, want %d", result.Counts.CellsTotal, tc.wantCells)
			}
			if result.Counts.CellsSuppressed != tc.wantSuppressed {
				t.Errorf("cells_suppressed is %d, want %d", result.Counts.CellsSuppressed, tc.wantSuppressed)
			}
			if got, want := result.Counts.CellsPublished+result.Counts.CellsSuppressed,
				result.Counts.CellsTotal; got != want {
				t.Errorf("published plus suppressed is %d, want the %d computable cells", got, want)
			}
			if got := len(result.Cells.ChampionsAscending); got != tc.wantChampions {
				t.Errorf("manifest lists %d champions, want %d", got, tc.wantChampions)
			}

			var samples []int
			for _, cell := range result.Cells.Cells {
				samples = append(samples, cell.N)
				if cell.N < tc.minCellN {
					t.Errorf("published cell %+v is below min_cell_n=%d", cell, tc.minCellN)
				}
			}
			if tc.wantCellOf1 && !hasCellInRole(result.Cells.Cells, aggmodel.RoleTop, 11) {
				t.Error("the cell of one is missing at a floor of one")
			}
			if !tc.wantCellOf1 && hasCellInRole(result.Cells.Cells, aggmodel.RoleTop, 11) {
				t.Error("the cell of one is published below its floor")
			}
			if tc.wantCellOf2 && !hasCellInRole(result.Cells.Cells, aggmodel.RoleTop, 13) {
				t.Error("the cell of two is missing at its floor")
			}
			if !tc.wantCellOf2 && hasCellInRole(result.Cells.Cells, aggmodel.RoleTop, 13) {
				t.Error("the cell of two is published below its floor")
			}
			// Reconciliation holds at every floor: suppression moves cells
			// between the published and the suppressed pile, never out of the
			// count.
			if result.Counts.SumN != result.Counts.ClassifiedRows {
				t.Errorf("sum of n is %d, want the %d classified rows",
					result.Counts.SumN, result.Counts.ClassifiedRows)
			}
			for _, sample := range samples {
				if sample > 9 {
					t.Errorf("published cell with n=%d, but the fixture holds at most nine of any cell", sample)
				}
			}
			_ = samples
		})
	}
}

func hasCellInRole(cells []aggmodel.Cell, role aggmodel.Role, championID int) bool {
	for _, cell := range cells {
		if cell.Role == role && cell.ChampionID == championID {
			return true
		}
	}
	return false
}
