package webtier

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The honesty tests close the one gap the shipped snapshot cannot close. Every
// checked-in fixture cell is above the floor, so the two states the site exists
// to be honest about - a cell that is too thin to publish and a cell with no
// games at all - are rendered by no route and asserted by no test. These tests
// build the snapshot such a cell needs: they copy the demo tree, lower one
// published cell below `min_cell_n`, empty another to n = 0, and then ask the
// real server, over HTTP, what it serves.
//
// What must hold: a thin cell still publishes its count and reads "withheld"
// instead of a rate, a cell with no games is not ranked and the page says so in
// words, and no number is invented for either. The floor comes from the
// artifact, so it is asserted as prose the page has to carry, not as a
// hard-coded behaviour.

// sparseSnapshot copies the demo tree into a temporary directory and edits two
// cells in it: Xerath mid (champion 101) is lowered from a healthy sample to
// short of the floor, and Kayn mid (champion 141) is emptied to n = 0. The
// champion file for Xerath is edited the same way, so the champion pages see a
// thin cell and an empty one too.
func sparseSnapshot(t *testing.T) string {
	t.Helper()
	src := fixtureDir()
	dst := filepath.Join(t.TempDir(), "agg")
	copyTree(t, src, dst)

	tierList := filepath.Join(dst, "v1", "p", "16.18", "EUW", "420", "all", "tierlist.json")
	document := readJSONDocument(t, tierList)
	setCellN(t, document, tierList, 101, "MID", 120)
	setCellN(t, document, tierList, 141, "MID", 0)
	writeJSONDocument(t, tierList, document)

	champion := filepath.Join(dst, "v1", "p", "16.18", "EUW", "420", "all", "champions", "101.json")
	document = readJSONDocument(t, champion)
	setStatsN(t, document, champion, "MID", 120)
	setStatsN(t, document, champion, "BOTTOM", 0)
	writeJSONDocument(t, champion, document)
	return dst
}

// copyTree copies every file under src into dst, preserving relative paths.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s to %s: %v", src, dst, err)
	}
}

// readJSONDocument decodes a partition artifact without touching its numbers,
// so re-encoding it cannot round a rate or a count.
func readJSONDocument(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return document
}

func writeJSONDocument(t *testing.T, path string, document map[string]any) {
	t.Helper()
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// setCellN edits one tier-list cell's games count, addressed by champion and
// role so the test cannot quietly edit the wrong row.
func setCellN(t *testing.T, document map[string]any, path string, champion int, role string, n int) {
	t.Helper()
	cells, ok := document["cells"].([]any)
	if !ok {
		t.Fatalf("%s carries no cells array", path)
	}
	for _, entry := range cells {
		cell, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if !matchesChampionRole(cell, champion, role) {
			continue
		}
		cell["n"] = json.Number(strconv.Itoa(n))
		return
	}
	t.Fatalf("%s carries no cell for champion %d in %s", path, champion, role)
}

// setStatsN edits one champion partition's role sample size.
func setStatsN(t *testing.T, document map[string]any, path string, role string, n int) {
	t.Helper()
	roles, ok := document["roles"].([]any)
	if !ok {
		t.Fatalf("%s carries no roles array", path)
	}
	for _, entry := range roles {
		roleEntry, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if roleEntry["role"] != role {
			continue
		}
		stats, ok := roleEntry["stats"].(map[string]any)
		if !ok {
			t.Fatalf("%s role %s carries no stats object", path, role)
		}
		stats["n"] = json.Number(strconv.Itoa(n))
		return
	}
	t.Fatalf("%s carries no %s role", path, role)
}

func matchesChampionRole(entry map[string]any, champion int, role string) bool {
	id, ok := entry["champion_id"].(json.Number)
	if !ok || id.String() != strconv.Itoa(champion) {
		return false
	}
	return entry["role"] == role
}

// sparseTier is the server under test: the real handler, reading the edited
// snapshot with fixtures switched off, so nothing can fall back to the demo
// tree behind the test's back.
func sparseTier(t *testing.T) *httptest.Server {
	t.Helper()
	opts := OptionsFromEnv()
	opts.AggRoot = sparseSnapshot(t)
	opts.FixturesMode = FixturesOff
	_, live := newTestServer(t, opts)
	return live
}

// TestBelowFloorCellPublishesItsCountAndNotARate is the core honesty assertion:
// a cell the artifact publishes with too few games must render its count, must
// render "withheld" in every rate column, must carry no rate in its island
// attributes, and the page must name the floor it was withheld under.
func TestBelowFloorCellPublishesItsCountAndNotARate(t *testing.T) {
	t.Parallel()
	live := sparseTier(t)

	result := get(t, live, "/tier-list/mid")
	if result.status != 200 {
		t.Fatalf("GET /tier-list/mid = %d, want 200", result.status)
	}
	page := result.text()

	row := rowFor(t, page, `data-search="xerath mid"`)
	if !strings.Contains(row, `data-v-n="120"`) {
		t.Errorf("the thin cell's row does not publish its count:\n%s", row)
	}
	if got := strings.Count(row, ">withheld<"); got != 4 {
		t.Errorf("the thin cell publishes %d withheld rate cells, want 4:\n%s", got, row)
	}
	for _, attribute := range []string{"win_rate", "pick_rate", "ban_rate", "ci95"} {
		if !strings.Contains(row, `data-v-`+attribute+`=""`) {
			t.Errorf("the thin cell carries a %s in its island attributes, want none:\n%s", attribute, row)
		}
	}
	if !strings.Contains(page, `A rate is published only when its cell has at least n = 500 games; thinner cells read`) {
		t.Error("the page does not name the floor the cell was withheld under")
	}
	if !strings.Contains(page, "withheld&quot; and show their count only.") && !strings.Contains(page, `withheld" and show their count only.`) {
		t.Error("the page does not say that a withheld cell shows its count only")
	}
}

// TestCellWithNoGamesIsNotRankedAndThePageSaysSo covers the other state: a cell
// with n = 0. It must not be ranked, must not be given a grade, and the page
// must say in words why it is absent rather than leaving a hole.
func TestCellWithNoGamesIsNotRankedAndThePageSaysSo(t *testing.T) {
	t.Parallel()
	live := sparseTier(t)

	result := get(t, live, "/tier-list/mid")
	if result.status != 200 {
		t.Fatalf("GET /tier-list/mid = %d, want 200", result.status)
	}
	page := result.text()

	if strings.Contains(page, ">Kayn</a>") {
		t.Error("a cell with no games is ranked in the tier list, want it left out")
	}
	if !strings.Contains(page, "One cell in this snapshot holds no games yet (n = 0), so it is not ranked here") {
		t.Error("the page does not say why the n = 0 cell is missing from the table")
	}
}

// TestChampionPageStatesNoSampleRatherThanARate checks the same two states on
// the champion pages, where the sample belongs to the role rather than to a
// single cell: a thin role must name its own count and the floor, and a role
// with no games must state that no cell was published at all.
func TestChampionPageStatesNoSampleRatherThanARate(t *testing.T) {
	t.Parallel()
	live := sparseTier(t)

	thin := get(t, live, "/champions/xerath/mid")
	if thin.status != 200 {
		t.Fatalf("GET /champions/xerath/mid = %d, want 200", thin.status)
	}
	if !strings.Contains(thin.text(), "n = 120") || !strings.Contains(thin.text(), "at least n = 500 games") {
		t.Error("the thin champion page does not publish its count beside the floor")
	}
	// The role's own stat block is withheld rather than rated: it shows the
	// dashes and the reason, which is the champion page's spelling of the same
	// rule the tier list spells "withheld".
	if !strings.Contains(thin.text(), `fallback-stat__value--unavailable">-</span>`) {
		t.Error("the thin champion page publishes a stat instead of withholding it")
	}
	if !strings.Contains(thin.text(), "not published: the sample is below the threshold for this cell") {
		t.Error("the thin champion page does not say why the stat is withheld")
	}

	empty := get(t, live, "/champions/xerath/bottom")
	if empty.status != 200 {
		t.Fatalf("GET /champions/xerath/bottom = %d, want 200", empty.status)
	}
	if !strings.Contains(empty.text(), "has no published cell in bottom in this snapshot") {
		t.Error("the champion page for a role with no games does not state that it published no cell")
	}
	if !strings.Contains(empty.text(), "No sample yet in Bottom") {
		t.Error("the champion page for a role with no games does not say there is no sample")
	}
}

// rowFor returns the table row containing the given marker, so an assertion
// about one cell cannot be satisfied by another row on the same page.
func rowFor(t *testing.T, page string, marker string) string {
	t.Helper()
	start := strings.Index(page, "<tr "+marker)
	if start < 0 {
		start = strings.Index(page, marker)
	}
	if start < 0 {
		t.Fatalf("no row carries %s", marker)
	}
	end := strings.Index(page[start:], "</tr>")
	if end < 0 {
		t.Fatalf("the row carrying %s is not closed", marker)
	}
	return page[start : start+end]
}
