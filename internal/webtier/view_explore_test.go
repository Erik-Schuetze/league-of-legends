package webtier

// The explorer's tests. They exist because /explore is the one route whose
// whole purpose is to publish the artifact, so three things have to be true at
// once and each of them is checked against the files on disk rather than
// against the code that rendered the page:
//
//   - the route answers, and every column the brief names is on the page and
//     carries the artifact's own number;
//   - a rate the artifact does not publish reads "withheld" in markup that says
//     so, and never reads 0 and never reads empty;
//   - the two downloads are the artifact: the JSON byte for byte, the CSV cell
//     for cell.
//
// The tests read the checked-in demo tree directly (manifest, tierlist.json,
// static champions) so that a view-layer regression cannot satisfy them.

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// exploreRequiredColumns are the columns the explorer promises: the champion,
// the role it was played in, the tier, the sample size n and every rate of the
// published cell beside the interval that sample supports.
var exploreRequiredColumns = []string{
	"champion", "role", "tier", "n", "win_rate", "pick_rate", "ban_rate", "ci95",
}

// TestExploreIsServedWithEveryColumn is the load-bearing test of this route. It
// fails if the route is missing (a 404, which is what the nav linked to before
// this page landed), if a column disappears from the header, if a row stops
// carrying a column's value, if the header and the body disagree about how many
// columns there are, or if the numbers stop coming from the artifact.
func TestExploreIsServedWithEveryColumn(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	resp := get(t, live, explorePath)
	if resp.status != 200 {
		t.Fatalf("GET %s = %d, want 200; the nav links to this path, so anything but 200 is a missing route",
			explorePath, resp.status)
	}
	page := resp.text()

	head := exploreBetween(t, page, "<thead", "</thead>")
	headers := regexp.MustCompile(`<th `).FindAllString(head, -1)
	if len(headers) != len(exploreRequiredColumns) {
		t.Errorf("the header carries %d columns, want %d: %s", len(headers), len(exploreRequiredColumns), head)
	}
	for _, column := range exploreRequiredColumns {
		if !strings.Contains(head, `data-col="`+column+`"`) {
			t.Errorf("the header carries no %s column: %s", column, head)
		}
	}

	artifact := exploreReadArtifact(t)
	names, slugs := exploreReadChampions(t)
	rows := exploreTableRows(t, page)
	if len(rows) == 0 {
		t.Fatal("the explorer rendered no rows over a snapshot it publishes cells for")
	}

	for index, row := range rows {
		cells := regexp.MustCompile(`<td[^>]*>`).FindAllString(row, -1)
		if len(cells) != len(headers) {
			t.Fatalf("row %d holds %d cells against %d headers, so the table is misaligned:\n%s",
				index, len(cells), len(headers), row)
		}
		for _, column := range exploreRequiredColumns {
			if !strings.Contains(row, `data-v-`+column+`="`) {
				t.Errorf("row %d carries no %s value:\n%s", index, column, row)
			}
		}
	}

	// The first row is the busiest cell, because the default view sorts by n
	// descending. Its whole content is then checked against the artifact by
	// value, so a column that renders a constant, a blank or a different cell's
	// number fails here.
	busiest := artifact.Cells[0]
	for _, cell := range artifact.Cells {
		if cell.N > busiest.N {
			busiest = cell
		}
	}
	first := rows[0]
	text := exploreCellText(t, first)
	if len(text) != len(exploreRequiredColumns) {
		t.Fatalf("the first row holds %d cells, want %d:\n%s", len(text), len(exploreRequiredColumns), first)
	}
	if want, got := names[busiest.ChampionID], text[0]; got != want {
		t.Errorf("the champion cell reads %q, want %q (champion %d, n = %d)", got, want, busiest.ChampionID, busiest.N)
	}
	if !strings.Contains(first, `href="/champions/`+slugs[busiest.ChampionID]+`"`) {
		t.Errorf("the champion cell does not link to the artifact's champion %d (%s):\n%s",
			busiest.ChampionID, slugs[busiest.ChampionID], first)
	}
	if want, got := RoleLabel(busiest.Role), text[1]; got != want {
		t.Errorf("the role cell reads %q, want %q, the role of the busiest published cell", got, want)
	}
	if want, got := string(busiest.Tier), text[2]; !strings.Contains(got, want) {
		t.Errorf("the tier cell reads %q, want it to carry %q", got, want)
	}
	if want, got := Integer(float64(busiest.N)), text[3]; got != want {
		t.Errorf("the n cell reads %q, want %q games", got, want)
	}
	for index, want := range []string{
		Percent(busiest.WinRate, 2),
		Percent(busiest.PickRate, 2),
		Percent(busiest.BanRate, 2),
		PlusMinus(busiest.CI95HalfWidth, 2),
	} {
		if got := text[4+index]; got != want {
			t.Errorf("rate column %d reads %q, want %q, as the artifact publishes it", index, got, want)
		}
	}
	for _, want := range []string{
		`data-v-champion="` + names[busiest.ChampionID] + `"`,
		`data-v-role="` + strconv.Itoa(roleIndex(busiest.Role)) + `"`,
		`data-v-n="` + strconv.Itoa(busiest.N) + `"`,
		`data-v-win_rate="` + strconv.FormatFloat(busiest.WinRate, 'f', -1, 64) + `"`,
		`data-v-ci95="` + strconv.FormatFloat(busiest.CI95HalfWidth, 'f', -1, 64) + `"`,
	} {
		if !strings.Contains(first, want) {
			t.Errorf("the busiest row does not sort on %s:\n%s", want, first)
		}
	}

	// The page has to say what the numbers are and where they came from, or a
	// reader cannot tell a published rate from a computed one.
	for _, want := range []string{
		"Data explorer, patch " + artifact.Patch,
		`data-state="demo"`,
		"is the number of games the cell",
		"A rate is published only when its cell has at least n = " + Integer(float64(artifact.MinCellN)),
		`href="` + exploreExportCSVPath + `"`,
		`href="` + exploreExportJSONPath + `"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not carry %q", want)
		}
	}
}

// TestExploreSortFilterAndPageChangeTheOutput walks the three URL-driven
// controls the brief asks for and asserts each one changes the rows rather than
// the address bar. Sorting is checked for order, filtering for narrowing, and
// the second page for being disjoint from the first.
func TestExploreSortFilterAndPageChangeTheOutput(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	busiest := exploreTableRows(t, get(t, live, explorePath).text())
	alphabetical := exploreTableRows(t, get(t, live, explorePath+"?sort=champion&dir=asc").text())
	if len(busiest) == 0 || len(alphabetical) == 0 {
		t.Fatal("a sorted view rendered no rows")
	}
	if exploreRowKey(t, busiest[0]) == exploreRowKey(t, alphabetical[0]) {
		t.Errorf("sort=champion&dir=asc opens with the same row as the default sort (%s)",
			exploreRowKey(t, busiest[0]))
	}
	exploreAssertOrdered(t, busiest, "n", false)
	exploreAssertOrdered(t, alphabetical, "champion", true)
	exploreAssertOrdered(t, exploreTableRows(t, get(t, live, explorePath+"?sort=ci95&dir=asc").text()), "ci95", true)

	filtered := exploreTableRows(t, get(t, live, explorePath+"?q=zeri").text())
	if len(filtered) == 0 {
		t.Fatal("q=zeri matched no row of a snapshot that publishes Zeri")
	}
	if len(filtered) >= len(busiest) {
		t.Errorf("q=zeri returned %d rows against %d unfiltered, so the filter did not narrow the table",
			len(filtered), len(busiest))
	}
	for _, row := range filtered {
		if !strings.Contains(exploreRowKey(t, row), "zeri") {
			t.Errorf("a row survived q=zeri without matching it:\n%s", row)
		}
	}

	firstPage := exploreTableRows(t, get(t, live, explorePath+"?per=25").text())
	secondPage := exploreTableRows(t, get(t, live, explorePath+"?per=25&page=2").text())
	if len(firstPage) != 25 || len(secondPage) == 0 {
		t.Fatalf("per=25 page 1 holds %d rows and page 2 holds %d", len(firstPage), len(secondPage))
	}
	seen := make(map[string]bool, len(firstPage))
	for _, row := range firstPage {
		seen[exploreRowKey(t, row)] = true
	}
	for _, row := range secondPage {
		if seen[exploreRowKey(t, row)] {
			t.Errorf("page 2 repeats a row of page 1: %s", exploreRowKey(t, row))
		}
	}
	if got := len(exploreTableRows(t, get(t, live, explorePath+"?role=mid").text())); got >= len(busiest) {
		t.Errorf("?role=mid returned %d rows against %d for every role, so the selection did nothing", got, len(busiest))
	}
}

// TestExploreWithheldCellIsNotZeroAndNotEmpty is the honesty assertion. A cell
// the artifact publishes with too few games must render its count, must render
// the word "withheld" in markup that says it is withheld, must carry no rate in
// the attributes the island sorts on, and the page must name the floor it was
// withheld under. Zero and an empty cell are both failures: one is a number the
// measurement does not support and the other is a hole a reader cannot read.
func TestExploreWithheldCellIsNotZeroAndNotEmpty(t *testing.T) {
	t.Parallel()
	live := sparseTier(t)

	// Xerath mid is published with n = 120, below the snapshot's own floor of
	// 500 games, so the snapshot itself withholds its rates.
	resp := get(t, live, explorePath+"?q=xerath")
	if resp.status != 200 {
		t.Fatalf("GET %s?q=xerath = %d, want 200", explorePath, resp.status)
	}
	page := resp.text()
	row := rowFor(t, page, `data-search="xerath mid"`)

	if !strings.Contains(row, `data-v-n="120"`) {
		t.Errorf("the thin cell's row does not publish its count:\n%s", row)
	}
	withheld := regexp.MustCompile(`<td class="num unavailable"[^>]*>withheld</td>`)
	if got := len(withheld.FindAllString(row, -1)); got != 4 {
		t.Errorf("the thin cell publishes %d withheld rate cells in markup that names them, want 4:\n%s", got, row)
	}
	for _, attribute := range []string{"win_rate", "pick_rate", "ban_rate", "ci95"} {
		if !strings.Contains(row, `data-v-`+attribute+`=""`) {
			t.Errorf("the thin cell sorts on a %s, so a client would rank it against published rates:\n%s",
				attribute, row)
		}
	}
	for _, unsupported := range []string{">0<", ">0.0", ">0.00", ">-<", ">0%"} {
		if strings.Contains(row, unsupported) {
			t.Errorf("the thin cell renders %q, which is a number the sample does not support:\n%s",
				unsupported, row)
		}
	}
	thinCells := exploreCellText(t, row)
	if len(thinCells) != len(exploreRequiredColumns) {
		t.Fatalf("the thin cell's row holds %d cells, want %d:\n%s", len(thinCells), len(exploreRequiredColumns), row)
	}
	if thinCells[3] != "120" {
		t.Errorf("the thin cell's count reads %q, want 120: a withheld cell still shows its count", thinCells[3])
	}
	for index, column := range exploreRequiredColumns[4:] {
		if got := thinCells[4+index]; got != "withheld" {
			t.Errorf("the %s cell of a thin row reads %q, want %q", column, got, "withheld")
		}
	}
	for index, got := range thinCells {
		if strings.TrimSpace(got) == "" {
			t.Errorf("cell %d of the thin row is empty: an unpublished rate is named, not left blank", index)
		}
	}
	if !strings.Contains(page, "at least n = 500 games") {
		t.Error("the page does not name the floor the cell was withheld under")
	}
	if !strings.Contains(page, "withheld") || !strings.Contains(page, "show their count only") {
		t.Error("the page does not say that a withheld cell shows its count only")
	}

	// A raised floor withholds cells the snapshot published: the same markup,
	// with the page explaining that the floor is this view's, not the
	// producer's.
	raised := get(t, live, explorePath+"?min_n=200000&sort=n&dir=desc")
	if raised.status != 200 {
		t.Fatalf("GET %s?min_n=200000 = %d, want 200", explorePath, raised.status)
	}
	raisedRows := exploreTableRows(t, raised.text())
	if len(raisedRows) == 0 {
		t.Fatal("a raised floor emptied the table instead of withholding its rates")
	}
	if !strings.Contains(raisedRows[0], `class="num unavailable"`) ||
		!strings.Contains(raisedRows[0], ">withheld<") {
		t.Errorf("a floor above every published sample did not withhold the rate cells:\n%s", raisedRows[0])
	}
	if strings.Contains(raisedRows[0], "%") {
		t.Errorf("a withheld row still publishes a rate:\n%s", raisedRows[0])
	}
	if !strings.Contains(raised.text(), "This view publishes a rate only above n = 200,000 games") {
		t.Error("the raised floor is not explained, so a withheld cell reads as a missing measurement")
	}
}

// TestExploreExportsAreTheArtifact checks both downloads against the published
// file. The JSON is a byte copy, so equality is the assertion; the CSV is a
// projection, so every cell of every row is compared with the artifact's own
// value.
func TestExploreExportsAreTheArtifact(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	artifactBytes, err := os.ReadFile(exploreArtifactPath(t))
	if err != nil {
		t.Fatalf("read the published artifact: %v", err)
	}
	jsonResult := get(t, live, exploreExportJSONPath)
	if jsonResult.status != 200 {
		t.Fatalf("GET %s = %d, want 200", exploreExportJSONPath, jsonResult.status)
	}
	if !strings.HasPrefix(jsonResult.header.Get("Content-Type"), jsonContentType) {
		t.Errorf("the JSON download is %q, want %q", jsonResult.header.Get("Content-Type"), jsonContentType)
	}
	if !strings.EqualFold(string(jsonResult.body), string(artifactBytes)) {
		t.Errorf("the JSON download is not the artifact: %d bytes against %d",
			len(jsonResult.body), len(artifactBytes))
	}

	csvResult := get(t, live, exploreExportCSVPath)
	if csvResult.status != 200 {
		t.Fatalf("GET %s = %d, want 200", exploreExportCSVPath, csvResult.status)
	}
	if !strings.HasPrefix(csvResult.header.Get("Content-Type"), "text/csv") {
		t.Errorf("the CSV download is %q, want text/csv", csvResult.header.Get("Content-Type"))
	}
	records, err := csv.NewReader(strings.NewReader(string(csvResult.body))).ReadAll()
	if err != nil {
		t.Fatalf("the CSV download does not parse: %v", err)
	}
	artifact := exploreReadArtifact(t)
	if len(records) != len(artifact.Cells)+1 {
		t.Errorf("the CSV holds %d records (header included) against %d published cells",
			len(records), len(artifact.Cells))
	}
	header := exploreCSVHeader()
	if strings.Join(records[0], ",") != strings.Join(header, ",") {
		t.Fatalf("the CSV header is %v, want %v", records[0], header)
	}
	column := make(map[string]int, len(header))
	for index, name := range header {
		column[name] = index
	}
	names, slugs := exploreReadChampions(t)
	for index, cell := range artifact.Cells {
		record := records[index+1]
		for name, want := range map[string]string{
			"champion_id":     strconv.Itoa(cell.ChampionID),
			"champion_name":   names[cell.ChampionID],
			"champion_slug":   slugs[cell.ChampionID],
			"role":            string(cell.Role),
			"n":               strconv.Itoa(cell.N),
			"wins":            strconv.Itoa(cell.Wins),
			"win_rate":        exploreRate(cell.WinRate),
			"pick_rate":       exploreRate(cell.PickRate),
			"ban_rate":        exploreRate(cell.BanRate),
			"ci95_half_width": exploreRate(cell.CI95HalfWidth),
			"tier":            string(cell.Tier),
		} {
			if got := record[column[name]]; got != want {
				t.Errorf("CSV row %d, column %s = %q, want %q", index+1, name, got, want)
			}
		}
	}
}

// TestExploreAddsNoRouteUnderTheSnapshotTree is the hard boundary: the snapshot
// tree is not an HTTP surface of this tier. The two downloads are the only way
// to read it, and no /agg/-shaped path is reachable through the explorer.
func TestExploreAddsNoRouteUnderTheSnapshotTree(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, fixtureOptions())

	for _, path := range []string{
		"/explore/agg/v1/manifest.json",
		"/explore/export.csv/../agg/v1/manifest.json",
		"/explore/export.json/../",
	} {
		if got := get(t, live, path).status; got != 404 {
			t.Errorf("GET %s = %d, want 404: the explorer must not expose the snapshot tree", path, got)
		}
	}
}

// TestExploreWithNoSnapshotIsAFaultAndNotAnEmptyTable holds the honesty line
// under the explorer: with nothing published, "no games met the floor" and "no
// games were measured" are different answers, so the route refuses with a fault
// page that names the state instead of rendering a table with no rows in it. The
// downloads refuse too, because an empty artifact is not the published one.
func TestExploreWithNoSnapshotIsAFaultAndNotAnEmptyTable(t *testing.T) {
	t.Parallel()
	_, live := newTestServer(t, Options{
		AggRoot:      t.TempDir(),
		FixturesMode: FixturesOff,
		DataDir:      fixtureDataDir(),
	})

	for _, path := range []string{explorePath, exploreExportCSVPath, exploreExportJSONPath} {
		resp := get(t, live, path)
		if resp.status != http.StatusServiceUnavailable {
			t.Errorf("GET %s with no snapshot = %d, want %d", path, resp.status, http.StatusServiceUnavailable)
			continue
		}
		page := resp.text()
		if !strings.Contains(page, `data-fault="no-snapshot"`) {
			t.Errorf("GET %s with no snapshot does not name the no-snapshot state", path)
		}
		if !strings.Contains(page, `data-state="no-data"`) {
			t.Errorf("GET %s with no snapshot renders a page that does not declare data-state=\"no-data\"", path)
		}
		if strings.Contains(page, `data-v-champion=`) {
			t.Errorf("GET %s with no snapshot rendered table cells anyway", path)
		}
	}
}

// exploreTableRows returns the table's body rows, whole and in order, so an
// assertion about the table cannot be satisfied by markup elsewhere on the page
// and cannot run on past the row it was handed.
func exploreTableRows(t *testing.T, page string) []string {
	t.Helper()
	body := exploreBetween(t, page, "<tbody", "</tbody>")
	parts := strings.Split(body, `<tr data-search="`)
	rows := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		end := strings.Index(part, "</tr>")
		if end < 0 {
			t.Fatalf("a table row is not closed:\n%s", part)
		}
		rows = append(rows, `<tr data-search="`+part[:end+len("</tr>")])
	}
	return rows
}

// exploreBetween returns the first region between two markers.
func exploreBetween(t *testing.T, page, open, close string) string {
	t.Helper()
	start := strings.Index(page, open)
	if start < 0 {
		t.Fatalf("the page carries no %s", open)
	}
	end := strings.Index(page[start:], close)
	if end < 0 {
		t.Fatalf("the region opened by %s is not closed by %s", open, close)
	}
	return page[start : start+end]
}

// exploreCellText is a row's cells as text, with the markup stripped, so an
// assertion about a column reads the way a reader reads it.
func exploreCellText(t *testing.T, row string) []string {
	t.Helper()
	out := make([]string, 0, len(exploreRequiredColumns))
	for _, cell := range regexp.MustCompile(`<td[^>]*>`).Split(row, -1)[1:] {
		end := strings.Index(cell, "</td>")
		if end < 0 {
			t.Fatalf("a cell of the row is not closed:\n%s", row)
		}
		text := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(cell[:end], "")
		text = strings.ReplaceAll(text, "&#43;", "+")
		text = strings.ReplaceAll(text, "&#34;", `"`)
		text = strings.ReplaceAll(text, "&amp;", "&")
		out = append(out, strings.Join(strings.Fields(text), " "))
	}
	return out
}

// exploreRowKey is a row's identity, which is the champion and the role: the
// pair a page of rows must not repeat.
func exploreRowKey(t *testing.T, row string) string {
	t.Helper()
	match := regexp.MustCompile(`data-search="([^"]*)"`).FindStringSubmatch(row)
	if match == nil {
		t.Fatalf("a table row carries no data-search:\n%s", row)
	}
	return match[1]
}

// exploreAssertOrdered checks that a column really is in the order the URL
// asked for. Rows whose value the artifact does not publish carry an empty
// attribute and sort last by design, so they are skipped.
func exploreAssertOrdered(t *testing.T, rows []string, column string, ascending bool) {
	t.Helper()
	previous := ""
	seen := 0
	for _, row := range rows {
		match := regexp.MustCompile(`data-v-` + column + `="([^"]*)"`).FindStringSubmatch(row)
		if match == nil {
			t.Fatalf("a row of the %s view carries no %s value:\n%s", column, column, row)
		}
		if match[1] == "" {
			continue
		}
		if previous != "" && !exploreInOrder(previous, match[1], ascending) {
			t.Errorf("the %s view is not in the order the URL asked for: %s then %s",
				column, previous, match[1])
		}
		previous = match[1]
		seen++
	}
	if seen == 0 {
		t.Fatalf("the %s view published no value to check", column)
	}
}

// exploreInOrder compares two attribute values the way the island does: as
// numbers when both are numbers, as text otherwise.
func exploreInOrder(left, right string, ascending bool) bool {
	leftNumber, leftErr := strconv.ParseFloat(left, 64)
	rightNumber, rightErr := strconv.ParseFloat(right, 64)
	if leftErr == nil && rightErr == nil {
		if ascending {
			return leftNumber <= rightNumber
		}
		return leftNumber >= rightNumber
	}
	if ascending {
		return left <= right
	}
	return left >= right
}

// exploreArtifact is the published tier list as the file holds it. It is
// decoded straight from the fixture rather than through the view layer, so the
// assertions above compare the page with the artifact and not with itself.
type exploreArtifact struct {
	Patch           string `json:"patch"`
	Region          string `json:"region"`
	Queue           int    `json:"queue"`
	Bracket         string `json:"bracket"`
	MinCellN        int    `json:"min_cell_n"`
	SuppressedCells int    `json:"suppressed_cells"`
	Cells           []struct {
		ChampionID    int           `json:"champion_id"`
		Role          aggmodel.Role `json:"role"`
		N             int           `json:"n"`
		Wins          int           `json:"wins"`
		WinRate       float64       `json:"win_rate"`
		PickRate      float64       `json:"pick_rate"`
		BanRate       float64       `json:"ban_rate"`
		Tier          aggmodel.Tier `json:"tier"`
		CI95HalfWidth float64       `json:"ci95_half_width"`
	} `json:"cells"`
}

func exploreReadArtifact(t *testing.T) exploreArtifact {
	t.Helper()
	raw, err := os.ReadFile(exploreArtifactPath(t))
	if err != nil {
		t.Fatalf("read the published artifact: %v", err)
	}
	var artifact exploreArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		t.Fatalf("decode the published artifact: %v", err)
	}
	if len(artifact.Cells) == 0 {
		t.Fatal("the published artifact holds no cells")
	}
	return artifact
}

// exploreArtifactPath is the demo tree's newest tier list, read from the
// manifest so that republishing the fixture does not silently point the tests
// at a partition the tier no longer serves.
func exploreArtifactPath(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDir(), "v1", "manifest.json"))
	if err != nil {
		t.Fatalf("read the demo manifest: %v", err)
	}
	var manifest struct {
		Latest struct {
			Patch   string `json:"patch"`
			Region  string `json:"region"`
			Queue   int    `json:"queue"`
			Bracket string `json:"bracket"`
		} `json:"latest"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode the demo manifest: %v", err)
	}
	latest := manifest.Latest
	if latest.Patch == "" {
		t.Fatal("the demo manifest names no latest partition")
	}
	return filepath.Join(fixtureDir(), "v1", "p", latest.Patch, latest.Region,
		strconv.Itoa(latest.Queue), latest.Bracket, "tierlist.json")
}

// exploreReadChampions is the static champion projection: the only place a
// champion id becomes the name and the slug the page renders.
func exploreReadChampions(t *testing.T) (map[int]string, map[int]string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(fixtureDir(), "v1", "static", "*", "champions.json"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no static champion projection in the demo tree: %v", err)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read %s: %v", matches[0], err)
	}
	var document struct {
		Champions []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"champions"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode %s: %v", matches[0], err)
	}
	names := make(map[int]string, len(document.Champions))
	slugs := make(map[int]string, len(document.Champions))
	for _, champion := range document.Champions {
		names[champion.ID] = champion.Name
		slugs[champion.ID] = champion.Slug
	}
	if len(names) == 0 {
		t.Fatalf("%s names no champion", matches[0])
	}
	return names, slugs
}
