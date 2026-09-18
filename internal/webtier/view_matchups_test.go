package webtier

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The windowed-matrix tests. The matrix route used to render one cell per
// ordered pair of champions in the artifact's pool, whatever the artifact
// stored: the demo tree holds 179 cells for its 28 champions, and the live
// artifact holds one cell for its 164. Rendering the frame rather than the data
// made the live page 2.7 MB of HTML against a 150 KiB ceiling
// (measured out of band), which is the defect these tests hold shut.
//
// They are the only thing that does. The byte-parity gate that used to compare
// this page against the reference build was retired with web/ (docs/contracts.md
// section 5), and no Go test enforces a byte budget - the budget is the external
// harness's - so nothing else would fail if the grid went back to pool x pool.
// The assertions are therefore about the shape of what is served: the number of
// cells, the pager that has to reach the rest of the axis by URL alone, the links
// that have to cover the whole pool, and the note that has to say what is left
// out. A dash is still a dash, and no cell may be invented for a pair the
// artifact does not carry.

var (
	matrixCellPattern    = regexp.MustCompile(`<td class="cell`)
	matrixRowHeadPattern = regexp.MustCompile(`<th scope="row" class="row-head"`)
	matrixColumnPattern  = regexp.MustCompile(`<th scope="col" class="column"`)
	matrixLinkPattern    = regexp.MustCompile(`<li><a href="/champions/`)
)

func matrixCells(page string) int { return len(matrixCellPattern.FindAllString(page, -1)) }

func matrixRowHeads(page string) int { return len(matrixRowHeadPattern.FindAllString(page, -1)) }

func matrixColumns(page string) int { return len(matrixColumnPattern.FindAllString(page, -1)) }

func matrixLinks(page string) int { return len(matrixLinkPattern.FindAllString(page, -1)) }

// matrixTier is the real handler over the checked-in demo tree, which is the
// only published data this repository carries.
func matrixTier(t *testing.T) *httptest.Server {
	t.Helper()
	_, live := newTestServer(t, fixtureOptions())
	return live
}

// sparseMatrixSnapshot rewrites the demo tree's mid matrix into the shape the
// live artifact has: the whole champion pool, and one stored cell. The page used
// to answer that with 28 x 28 cells and %d dashes for one real pairing.
func sparseMatrixSnapshot(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "agg")
	copyTree(t, fixtureDir(), dst)
	path := filepath.Join(dst, "v1", "p", "16.18", "EUW", "420", "all", "matchups", "mid.json")
	document := readJSONDocument(t, path)
	document["cells"] = []any{
		map[string]any{
			"champion_id":     1,
			"opponent_id":     55,
			"n":               900,
			"wins":            500,
			"win_rate":        0.5556,
			"ci95_half_width": 0.03,
		},
	}
	document["suppressed_cells"] = json.Number("177")
	writeJSONDocument(t, path, document)
	return dst
}

// denseMatrixSnapshot rewrites the demo tree's mid matrix into the artifact the
// role would carry if its whole pool were published over the threshold: every
// ordered pair, and nothing suppressed. It is the shape /matchups/top takes when
// its lane fills in, and the shape that made this route 2.7 MB. The page may not
// grow with the artifact's cell count, only with the window.
func denseMatrixSnapshot(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "agg")
	copyTree(t, fixtureDir(), dst)
	path := filepath.Join(dst, "v1", "p", "16.18", "EUW", "420", "all", "matchups", "mid.json")
	document := readJSONDocument(t, path)
	pool, ok := document["champions"].([]any)
	if !ok || len(pool) < 2 {
		t.Fatalf("the demo tree's mid matrix lists %d champions, want a pool to square", len(pool))
	}
	cells := make([]any, 0, len(pool)*(len(pool)-1))
	for _, row := range pool {
		for _, column := range pool {
			if row == column {
				continue // a champion is not its own opponent
			}
			cells = append(cells, map[string]any{
				"champion_id":     row,
				"opponent_id":     column,
				"n":               json.Number("900"),
				"wins":            json.Number("500"),
				"win_rate":        0.5556,
				"ci95_half_width": 0.0352,
			})
		}
	}
	document["cells"] = cells
	document["suppressed_cells"] = json.Number("0")
	writeJSONDocument(t, path, document)
	return dst
}

// TestDenseMatrixStaysInsideTheBudget is the scaling assertion, and the one that
// would have caught the defect before a harness had to: the demo tree's mid role
// with its whole pool squared - 756 published pairs where the checked-in artifact
// carries 179 - still has to serve a window of the axis and not the matrix. A
// grid tuned to today's numbers passes every other test in this file and still
// serves 2.7 MB the day a role's data arrives.
func TestDenseMatrixStaysInsideTheBudget(t *testing.T) {
	t.Parallel()
	opts := OptionsFromEnv()
	opts.AggRoot = denseMatrixSnapshot(t)
	opts.FixturesMode = FixturesOff
	_, live := newTestServer(t, opts)

	for _, test := range []struct {
		path   string
		budget int
	}{
		{"/matchups/mid/", defaultViewGoal},
		{"/matchups/mid/?page=3", planHTMLBudget},
		{"/matchups/mid/?per=30", planHTMLBudget},
		{"/matchups/mid/?q=a", planHTMLBudget},
	} {
		page := get(t, live, test.path).text()
		if got := len(page); got > test.budget {
			t.Errorf("GET %s serves %d B of HTML over a fully published artifact, over its %d B budget (cells: %d)",
				test.path, got, test.budget, matrixCells(page))
		}
		if got := matrixCells(page); got > MaxMatrixCells {
			t.Errorf("GET %s renders %d cells over a fully published artifact, over the %d cell budget",
				test.path, got, MaxMatrixCells)
		}
	}

	page := get(t, live, "/matchups/mid/").text()
	t.Logf("the default view over 756 published pairs is %d B of HTML in %d cells", len(page), matrixCells(page))
	if strings.Contains(page, `class="cell missing"`) {
		t.Error("a window of a fully published artifact renders dashes, so the grid is not the artifact's cells")
	}
	if got, want := matrixLinks(page), 28; got != want {
		t.Errorf("the page carries %d champion links, want %d (one per champion in the pool)", got, want)
	}
}

// emptyMatrixSnapshot rewrites the demo tree's mid matrix into the shape a role
// has before its lane publishes anything: the pool is listed, no cell is stored,
// and the document's envelope carries a suppressed count. It is the state
// /matchups/top served at 33,881 B until its lane filled in with two pairings,
// and the state /matchups/jungle serves live now, so it has to be an honest empty
// state rather than a frame.
func emptyMatrixSnapshot(t *testing.T) string {
	t.Helper()
	return matrixSnapshotWithCells(t, []any{}, 177)
}

// thinMatrixSnapshot is the same artifact one step later: a stored pairing whose
// sample is under the artifact's own min_cell_n (500 in the demo tree), so the
// artifact has a cell and still has nothing to publish.
func thinMatrixSnapshot(t *testing.T) string {
	t.Helper()
	return matrixSnapshotWithCells(t, []any{map[string]any{
		"champion_id":     1,
		"opponent_id":     55,
		"n":               json.Number("120"),
		"wins":            json.Number("61"),
		"win_rate":        0.5083,
		"ci95_half_width": 0.09,
	}}, 0)
}

// thinStoredMatrixSnapshot is the third shape: an artifact with three stored
// pairs whose samples are below its own min_cell_n (500 in the demo tree), so the
// artifact has cells and still has nothing to publish.
func thinStoredMatrixSnapshot(t *testing.T) string {
	t.Helper()
	cells := make([]any, 0, 3)
	for _, opponent := range []int{55, 77, 99} {
		cells = append(cells, map[string]any{
			"champion_id":     1,
			"opponent_id":     opponent,
			"n":               json.Number("50"),
			"wins":            json.Number("26"),
			"win_rate":        0.52,
			"ci95_half_width": 0.14,
		})
	}
	return matrixSnapshotWithCells(t, cells, 0)
}

// matrixSnapshotWithCells rewrites the stored cells and the envelope's suppressed
// count, leaving the champion pool the fixture names so the pool clause the empty
// state carries is checkable. The count is the document's own field, which the
// live build copies from the partition's tier-list cells into every role, so the
// body must not quote it as a matchup count.
func matrixSnapshotWithCells(t *testing.T, cells []any, suppressedCells int) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "agg")
	copyTree(t, fixtureDir(), dst)
	path := filepath.Join(dst, "v1", "p", "16.18", "EUW", "420", "all", "matchups", "mid.json")
	document := readJSONDocument(t, path)
	document["cells"] = cells
	document["suppressed_cells"] = json.Number(IntegerAny(float64(suppressedCells)))
	writeJSONDocument(t, path, document)
	return dst
}

// TestEmptyMatrixSaysNothingIsPublishedInsteadOfDrawingTheFrame holds the empty
// state. The defect these tests exist for was a frame sized to the pool, and the
// frame is at its worst when nothing is stored: the pool's dashes with no data in
// them at all. The live top role served this page at 33,881 B, which is the
// measurement the frame would have replaced with 784 dashes, and /matchups/jungle
// serves it live now.
//
// The empty state also has to be true about why it is empty. Every shape here
// carries an artifact - one with no stored cell at all, one with a stored pair
// that is itself too thin, one with three - and none of them is a missing
// artifact, so none may be described as one. Only the stored cells and the pool
// the document names are quoted: the document's suppressed_cells is the
// partition's tier-list suppression count copied into every role, and this route
// may not present it as a count of matchup pairs.
func TestEmptyMatrixSaysNothingIsPublishedInsteadOfDrawingTheFrame(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		root    func(*testing.T) string
		because string
	}{
		{
			name:    "no stored cell",
			root:    emptyMatrixSnapshot,
			because: "publishes no pairing for this role at or above n = 500 games",
		},
		{
			name:    "a stored pair below the threshold",
			root:    thinMatrixSnapshot,
			because: "holds one stored pairing for this role, below the n = 500 games floor",
		},
		{
			name:    "stored pairs under the threshold",
			root:    thinStoredMatrixSnapshot,
			because: "holds 3 stored pairings for this role, all below the n = 500 games floor",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			opts := OptionsFromEnv()
			opts.AggRoot = test.root(t)
			opts.FixturesMode = FixturesOff
			_, live := newTestServer(t, opts)

			page := get(t, live, "/matchups/mid/").text()
			if got := matrixCells(page); got != 0 {
				t.Errorf("a role with nothing published renders %d cells, want the empty state and no frame", got)
			}
			if got := matrixRowHeads(page) + matrixColumns(page); got != 0 {
				t.Errorf("a role with nothing published renders %d axis headers, want none", got)
			}
			if strings.Contains(page, `rel="next"`) || strings.Contains(page, "pager") {
				t.Error("a role with nothing published pages an empty axis")
			}
			if got, want := len(page), planHTMLBudget; got > want {
				t.Errorf("the empty state is %d B of HTML, over the %d B ceiling", got, want)
			}
			if strings.Contains(page, "has no matchup artifact for this role") {
				t.Errorf("the empty state calls a present artifact a missing one:\n%s", emptyOf(page))
			}
			if strings.Contains(emptyOf(page), "withheld in total") {
				t.Error("the empty state reads the document's partition-wide suppressed count as a count of matchup pairs")
			}
			if !strings.Contains(emptyOf(page), "champions appear in the pairings it counted") {
				t.Errorf("the empty state does not report the pool the document counted:\n%s", emptyOf(page))
			}
			if !strings.Contains(page, "No mid matchup cells published") {
				t.Error("the empty state does not say no cell is published")
			}
			if !strings.Contains(page, test.because) {
				t.Errorf("the empty state does not carry %q:\n%s", test.because, emptyOf(page))
			}
			if !strings.Contains(page, "A rate is published only for a cell with at least n = 500 games") {
				t.Error("the empty state does not carry the threshold the artifact uses")
			}
			t.Logf("%s: the empty state is %d B of HTML in %d cells", test.name, len(page), matrixCells(page))
		})
	}
}

// TestMatchupMatrixRendersAWindowOfTheAxis is the core assertion: a stored axis
// longer than the window is served as the window, with the pager carrying the
// rest, and the champion list under the grid still describing the whole pool.
func TestMatchupMatrixRendersAWindowOfTheAxis(t *testing.T) {
	t.Parallel()
	live := matrixTier(t)
	page := get(t, live, "/matchups/mid/").text()
	if !strings.Contains(page, `class="island" data-island="heatmap"`) {
		t.Fatalf("GET /matchups/mid/ is not the matrix page:\n%s", page)
	}
	if got, want := matrixCells(page), DefaultMatrixPer*DefaultMatrixPer; got != want {
		t.Errorf("the grid renders %d cells, want %d (a %dx%d window)", got, want, DefaultMatrixPer, DefaultMatrixPer)
	}
	if got, want := matrixRowHeads(page), DefaultMatrixPer; got != want {
		t.Errorf("the grid renders %d rows, want %d", got, want)
	}
	if got, want := matrixColumns(page), DefaultMatrixPer; got != want {
		t.Errorf("the grid renders %d columns, want %d", got, want)
	}
	if !strings.Contains(page, `rel="next" href="?page=2"`) {
		t.Error("the windowed grid carries no working next-page link: the rest of the axis is unreachable by URL alone")
	}
	if strings.Contains(page, `href=""`) {
		t.Error("a link on the page points at the empty string, which is not a page a reader can reach")
	}
	for _, sentence := range []string{
		"window of the matrix rather than the whole of it",
		"the pager under the grid carries the rest of them",
	} {
		if !strings.Contains(page, sentence) {
			t.Errorf("the note does not disclose the window: %q is missing", sentence)
		}
	}
	// The window is one page of the role; the filter is how a reader reaches any
	// champion's complete row without JavaScript, so the control has to be there.
	// The class is asserted as the form's own attribute, not as the bare string:
	// the inlined stylesheet carries the selector too, and matching that would
	// pass even with no form on the page.
	if !strings.Contains(page, `<form class="ds-filter-bar `) || !strings.Contains(page, `name="q"`) {
		t.Error("the page carries no filter form, so the complete-row view has no no-JavaScript control")
	}
	// The window's own sentence has to name the filter's width as the axis the
	// artifact measures (23 champions in the demo role) rather than as the
	// window's 12: a filter keeps every column, so promising 12 would understate
	// what the row carries and matching the window here would hide the mistake.
	if !strings.Contains(page, "across all 23 columns the artifact measures") {
		t.Errorf("the window's note does not name the filter's real width:\n%s", noteOf(page))
	}
	// The links are the whole pool, not the window: they are how a reader reaches
	// a champion the grid left out, so a windowed list would defeat the point.
	if links, cells := matrixLinks(page), matrixCells(page); links <= DefaultMatrixPer {
		t.Errorf("the page carries %d champion links, want the whole pool (more than the %d in the window; the grid has %d cells)",
			links, DefaultMatrixPer, cells)
	}
}

// TestMatchupMatrixLastPageIsReachableAndSmall asserts the second page renders
// the remainder rather than a second window, and that its back link is real.
func TestMatchupMatrixLastPageIsReachableAndSmall(t *testing.T) {
	t.Parallel()
	live := matrixTier(t)
	page := get(t, live, "/matchups/mid/?page=2").text()
	cells := matrixCells(page)
	if cells == 0 || cells >= DefaultMatrixPer*DefaultMatrixPer {
		t.Errorf("page 2 renders %d cells, want a remainder smaller than the %dx%d window", cells, DefaultMatrixPer, DefaultMatrixPer)
	}
	if got := matrixRowHeads(page); got*matrixColumns(page) != cells {
		t.Errorf("page 2 renders %d rows and %d columns for %d cells: the window is not square", matrixRowHeads(page), matrixColumns(page), cells)
	}
	if !strings.Contains(page, `rel="prev" href="?page=1"`) {
		t.Error("page 2 carries no working previous-page link")
	}
	if strings.Contains(page, `href=""`) {
		t.Error("a link on page 2 points at the empty string")
	}
}

// TestSparseMatrixDoesNotRenderTheFrame is the live artifact's shape: one stored
// cell, a full pool. The page must be the pairing that exists and nothing else -
// a 1 x 1 pool frame would be 784 cells of dashes - while still naming every
// champion the role has a page for.
func TestSparseMatrixDoesNotRenderTheFrame(t *testing.T) {
	t.Parallel()
	opts := OptionsFromEnv()
	opts.AggRoot = sparseMatrixSnapshot(t)
	opts.FixturesMode = FixturesOff
	_, live := newTestServer(t, opts)

	page := get(t, live, "/matchups/mid/").text()
	// Two stored-axis champions, so a 2 x 2 grid including the self cell.
	if got, want := matrixCells(page), 4; got != want {
		t.Errorf("the sparse grid renders %d cells, want %d", got, want)
	}
	if got, want := matrixRowHeads(page), 2; got != want {
		t.Errorf("the sparse grid renders %d rows, want %d", got, want)
	}
	if strings.Contains(page, `rel="next"`) {
		t.Error("a two-champion axis is paged, which it should fit on one page")
	}
	if got, want := matrixLinks(page), 28; got != want {
		t.Errorf("the page carries %d champion links, want %d (one per champion in the pool)", got, want)
	}
	// The note has to name what the frame leaves out, in the page's own words, or
	// a bounded grid reads as a complete one. The pairing counts are directed: one
	// stored cell is two ordered pairs, and the denominator is the pool squared,
	// which is the same unit the grid is drawn in and the one the page's "shown
	// from both sides, so a cell and its mirror are complements" sentence names.
	for _, sentence := range []string{
		"2 of 784 pairings are published for this role",
		"Only the 2 champions the artifact stores a cell for are rows and columns here",
		"out of the 28 it lists for this role",
	} {
		if !strings.Contains(page, sentence) {
			t.Errorf("the sparse page's note does not carry %q:\n%s", sentence, noteOf(page))
		}
	}
	// The one stored pair is still published with its rate: bounding the frame
	// must not cost a real pairing.
	if !strings.Contains(page, `data-n="900"`) {
		t.Errorf("the stored pair is not published in the grid:\n%s", page)
	}
}

// TestMatrixFilterCarriesACompleteRowAndSaysSo covers the filter the route's
// form carries. A filter is a question about a champion, so the answer is that
// champion's whole row across the role's full column axis - and the grid's own
// window must not be able to truncate it. It was inert before the window; a
// control that does nothing is the same failure as a frame that renders nothing,
// so it is asserted to work, to stay inside the page's cell budget, and to
// disclose what it kept.
func TestMatrixFilterCarriesACompleteRowAndSaysSo(t *testing.T) {
	t.Parallel()
	live := matrixTier(t)

	window := matrixColumns(get(t, live, "/matchups/mid/").text())

	filtered := get(t, live, "/matchups/mid/?q=riven").text()
	if got, want := matrixRowHeads(filtered), 1; got != want {
		t.Errorf("the filter renders %d rows, want %d: it selects champions, not pairs", got, want)
	}
	if got := matrixColumns(filtered); got <= window {
		t.Errorf("the filtered grid renders %d columns, want more than the %d-column window: a filtered row has to be the champion's complete row", got, window)
	}
	if got, want := matrixCells(filtered), matrixRowHeads(filtered)*matrixColumns(filtered); got != want {
		t.Errorf("the filtered grid renders %d cells, want %d (a rectangle of its rows and columns)", got, want)
	}
	if !strings.Contains(filtered, `data-champion="92"`) {
		t.Error("the champion filtered for is not a row of the filtered grid")
	}
	for _, sentence := range []string{
		`whose name or slug contains "riven"`,
		"every row shown is a champion's complete row",
	} {
		if !strings.Contains(filtered, sentence) {
			t.Errorf("the filtered page does not disclose what the filter kept: %q is missing:\n%s", sentence, noteOf(filtered))
		}
	}

	// A filter cannot breach the page's budget. The columns are the role's whole
	// axis, so the rows one page carries are what is left of the budget: "a"
	// matches most of the demo role, which is the shape that used to be the
	// largest page on the site.
	wide := get(t, live, "/matchups/mid/?q=a").text()
	if got := matrixCells(wide); got > MaxMatrixCells {
		t.Errorf("filtering for a substring of most of the roster renders %d cells, over the %d-cell budget", got, MaxMatrixCells)
	}
	if rows, columns := matrixRowHeads(wide), matrixColumns(wide); rows*columns != matrixCells(wide) {
		t.Errorf("the wide filter renders %d rows and %d columns for %d cells: not a rectangle", rows, columns, matrixCells(wide))
	}
	if rows := matrixRowHeads(wide); rows < 2 {
		t.Errorf("filtering for %q renders %d rows, want the many champions whose name contains it", "a", rows)
	}

	empty := get(t, live, "/matchups/mid/?q=zzzzzz").text()
	if got := matrixCells(empty); got != 0 {
		t.Errorf("a filter matching nothing renders %d cells, want none", got)
	}
	if !strings.Contains(empty, "matches that filter, so no row is shown") {
		t.Errorf("a filter matching nothing does not say so:\n%s", noteOf(empty))
	}
}

// TestMatrixBudgetHoldsWhateverTheQueryAsks holds the arithmetic the page's size
// rests on, since no harness in this repository can measure the served bytes: a
// window cannot exceed MaxMatrixPer, and a page cannot exceed MaxMatrixCells
// however many rows the axis has. It is the only thing between ?per= and a
// 2.7 MB page.
func TestMatrixBudgetHoldsWhateverTheQueryAsks(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		columns, per, want int
	}{
		{165, DefaultMatrixPer, MaxMatrixCells / 165},               // a filtered row over the live role's axis
		{23, DefaultMatrixPer, MaxMatrixCells / 23},                 // the demo role's axis is longer than the window
		{MaxMatrixPer, MaxMatrixPer, MaxMatrixCells / MaxMatrixPer}, // the widest window the URL can ask for
		{MaxMatrixCells, DefaultMatrixPer, 1},
		{MaxMatrixCells + 1, DefaultMatrixPer, 1}, // over budget, still a row rather than none
		{0, DefaultMatrixPer, DefaultMatrixPer},
	} {
		if got := matrixFit(test.columns, test.per); got != test.want {
			t.Errorf("matrixFit(%d, %d) = %d, want %d", test.columns, test.per, got, test.want)
		}
	}
	for _, test := range []struct {
		requested, want int
	}{
		{0, DefaultMatrixPer},
		{MinPer - 1, DefaultMatrixPer},
		{MinPer, MinPer},
		{MaxMatrixPer, MaxMatrixPer},
		{MaxPer, MaxMatrixPer},
	} {
		if got := matrixPer(test.requested); got != test.want {
			t.Errorf("matrixPer(%d) = %d, want %d", test.requested, got, test.want)
		}
	}
	// No role has more than the roster's worth of champions, but the budget has to
	// hold for the axes the artifact could carry as well as the ones it does.
	for _, columns := range []int{1, 12, MaxMatrixPer, 165} {
		if got := matrixFit(columns, MaxMatrixPer) * columns; got > MaxMatrixCells {
			t.Errorf("%d columns at %d per page renders %d cells, over the %d budget", columns, MaxMatrixPer, got, MaxMatrixCells)
		}
	}
}

// TestMatrixPageKeepsTheFilterAndClampsThePage covers the pager's own address
// arithmetic: Query.Href drops ?page= unless ?per= is set, and this route's
// default window sets neither, so a hand-built link is the only one that works.
func TestMatrixPageKeepsTheFilterAndClampsThePage(t *testing.T) {
	t.Parallel()
	axis := make([]matrixChampion, 0, 40)
	for id := 1; id <= 40; id++ {
		axis = append(axis, matrixChampion{ID: id, Name: "Champion " + IntegerAny(float64(id)), Slug: "c"})
	}
	rows, nav := matrixPage(axis, 10, Query{Filter: "riven", Page: 99}, "champions")
	if nav == nil {
		t.Fatal("a 40-row axis at 10 rows a page carries no pager")
	}
	if len(rows) != 10 || rows[0].ID != 31 {
		t.Errorf("page 99 of 4 carries rows %d-%d, want the last page 31-40", rows[0].ID, rows[len(rows)-1].ID)
	}
	if !strings.Contains(nav.Label, "4 of 4") || !strings.Contains(nav.Label, "31-40 of 40") {
		t.Errorf("the pager's label is %q, want it to name the page it actually rendered", nav.Label)
	}
	if nav.HasNext {
		t.Error("the last page carries a next link")
	}
	if !nav.HasPrev || !strings.Contains(nav.Prev, "page=3") || !strings.Contains(nav.Prev, "q=riven") {
		t.Errorf("the previous link is %q, want it to carry both the page and the filter", nav.Prev)
	}
	if _, nav := matrixPage(axis[:5], 10, Query{}, "champions"); nav != nil {
		t.Error("a five-row axis at ten rows a page is paged, which it should fit on one page")
	}
}

// TestMatrixAxisAndFilterAreScopedToTheData covers the two halves of the frame:
// the axis is what the artifact's cells name, ordered by the games they measure,
// and the filter selects rows rather than the champions those rows face.
func TestMatrixAxisAndFilterAreScopedToTheData(t *testing.T) {
	t.Parallel()
	pool := []matrixChampion{
		{ID: 1, Name: "Annie", Slug: "annie"},
		{ID: 2, Name: "Riven", Slug: "riven"},
		{ID: 3, Name: "Garen", Slug: "garen"},
	}
	matchups := &aggmodel.Matchups{Cells: []aggmodel.MatchupCell{
		{ChampionID: 1, OpponentID: 2, N: 10},
		{ChampionID: 2, OpponentID: 3, N: 500},
	}}
	axis := matrixAxis(pool, matchups)
	if len(axis) != 3 {
		t.Fatalf("the axis carries %d champions, want the 3 the cells name", len(axis))
	}
	if axis[0].ID != 2 {
		t.Errorf("the axis starts at champion %d, want the one with the most measured games", axis[0].ID)
	}
	if got := matrixMatch(axis, "  RIVEN "); len(got) != 1 || got[0].ID != 2 {
		t.Errorf("matrixMatch returned %v, want only Riven: a filter selects rows, not the champions they face", got)
	}
	if got := matrixMatch(axis, "riven"); len(got) != 1 {
		t.Errorf("matrixMatch returned %d rows, want 1", len(got))
	}
}

// noteOf returns the note the page carries, so a failure prints what the page
// actually said rather than the whole document.
func noteOf(page string) string {
	start := strings.Index(page, `<p class="note`)
	if start < 0 {
		return "(the page carries no note)"
	}
	end := strings.Index(page[start:], "</p>")
	if end < 0 {
		return page[start:]
	}
	return page[start : start+end+4]
}

// emptyOf returns the empty state the page carries, for the same reason: an
// empty page has no note to print.
func emptyOf(page string) string {
	start := strings.Index(page, `class="fallback-empty"`)
	if start < 0 {
		return noteOf(page)
	}
	if open := strings.Index(page[start:], ">"); open >= 0 {
		start += open + 1
	}
	end := strings.Index(page[start:], `</div>`)
	if end < 0 {
		return page[start:]
	}
	return page[start : start+end]
}

// The page HTML ceiling, and the tighter target the default view is held to.
// They are written as bytes because that is the unit the budget was measured in.
const (
	planHTMLBudget  = 150_000
	defaultViewGoal = 100_000
)

// TestServedMatrixFitsThePlanBudgetByMeasurement is the only assertion in this
// repository that reads the served bytes of this route, and it exists because
// nothing else would notice the difference: the tests above hold the *shape* of
// the grid, and a shape is not a budget - a page of published pairings costs 224 B
// a cell where the same page of dashes costs 99 B, so the same 276 cells are 67 KB
// or 27 KB depending on the artifact. Every query shape the route answers has to
// come in under the plan's ceiling, and the default view - the one the budget
// was measured on - under the target for it.
// A filtered view is held to the plan's ceiling rather than the target: the target
// is stated for the default view, and a complete row across the whole axis is a
// deliberately larger answer than the window. The numbers here are the demo
// tree's, which is the only published data a test can serve; the live page carries
// a heavier chrome (a banner, and the whole pool's links), which is why the window
// is sized well inside the ceiling rather than at it.
func TestServedMatrixFitsThePlanBudgetByMeasurement(t *testing.T) {
	t.Parallel()
	live := matrixTier(t)
	for _, test := range []struct {
		path   string
		budget int
	}{
		{"/matchups/mid/", defaultViewGoal},
		{"/matchups/bottom/", defaultViewGoal},
		{"/matchups/top/", defaultViewGoal},
		{"/matchups/mid/?q=a", planHTMLBudget},
		{"/matchups/mid/?page=2", planHTMLBudget},
		{"/matchups/mid/?per=30&page=2", planHTMLBudget},
		{"/matchups/mid/?q=riven&page=2", planHTMLBudget},
	} {
		page := get(t, live, test.path).text()
		if got := len(page); got > test.budget {
			t.Errorf("GET %s serves %d B of HTML, over its %d B budget (cells: %d)",
				test.path, got, test.budget, matrixCells(page))
		}
	}
}
