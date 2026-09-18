package webtier

import (
	"html/template"
	"sort"
	"strconv"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The matchup route: /matchups/<role>.
//
// The artifact stores each unordered pair once, so both orientations are
// materialised here, and the mirror of a stored pair is the complement of its
// win rate. That is arithmetic, not an estimate, and it is the same rule
// lib/artifacts.ts applies.
//
// The rendered grid is a window of that matrix, never the whole of it: the axes
// are the champions the artifact's cells name, the columns are the ones this
// role measured the most games for, and the rows are carried a page at a time by
// the URL. The matrix stays reachable in full through ?q=, which renders one
// champion's complete row against every column.

// matchupCellView is one cell of the heatmap, pre-rendered. The cells are
// assembled in Go rather than in the template because each of the three shapes
// has its own attribute set.
type matchupCellView struct {
	Markup template.HTML
}

// matchupRowView is one row: a champion on the axis, and its row of cells.
type matchupRowView struct {
	ID    int
	Name  string
	Slug  string
	Href  string
	Cells []matchupCellView
}

// matchupColumnView is one column header.
type matchupColumnView struct {
	ID   int
	Name string
}

// heatmapView is HeatmapIsland's data.
type heatmapView struct {
	MinCellN int
	Caption  string
	Columns  []matchupColumnView
	Rows     []matchupRowView
	Note     template.HTML

	// HasNav and Nav are the pager under the grid. The matrix is windowed, so
	// the rest of it is reachable by URL alone, the same contract the tier
	// list's rows keep.
	HasNav bool
	Nav    pagerView
}

// matchupView is the page body.
type matchupView struct {
	base
	Label        string
	LabelLower   string
	Slug         string
	Patch        string
	HasPartition bool
	Intro        string
	Region       string
	Queue        int
	Bracket      string
	Window       string
	Generated    string

	HasSample bool
	Notice    sampleNoticeView
	Median    string
	Island    heatmapView
	Links     []linkView
	MinCellN  int

	ColumnNote template.HTML
	Tail       string
	Empty      emptyView

	// HasBar and Bar are the no-JavaScript control surface, the same addition
	// the tier list carries: a plain GET form that narrows the matrix to a
	// champion, linkable and functional without JavaScript.
	HasBar bool
	Bar    barView
}

// MatchupsPage is the /matchups/<role> route.
func (r *Renderer) MatchupsPage(role string, query Query, interactive bool) (*Page, error) {
	resolved, err := r.Role(role)
	if err != nil {
		return nil, err
	}
	return r.matchupsPage(resolved, query, interactive)
}

func (r *Renderer) matchupsPage(role aggmodel.Role, query Query, interactive bool) (*Page, error) {
	snap, err := r.snapshot("")
	if err != nil {
		return nil, err
	}
	site := snap.Site
	slug := RoleSlugString(role)
	label := RoleLabel(role)

	var matchups *aggmodel.Matchups
	if snap.HasSeg {
		// Same rule as the tier list: a manifest that declares a partition has
		// promised its artifacts exist, so an unreadable matchup artifact is an
		// incomplete publication (a 503), not a page with an empty matrix.
		loaded, err := site.Matchups(snap.Seg, role)
		if err != nil {
			return nil, err
		}
		matchups = loaded
	}

	minCellN := snap.minCellN()
	if matchups != nil {
		minCellN = matchups.MinCellN
	}
	suppressed := snap.suppressedCells()
	if matchups != nil {
		value := matchups.SuppressedCells
		suppressed = &value
	}

	// The published pairs are the role's sample: the median pair is the honest
	// summary of a matrix, because a total across it would count one match once
	// per pair it contains. This is the whole role, not the window the grid
	// renders, so the notice describes the snapshot the page is drawn from.
	var pairCounts []int
	for _, cell := range cellsOf(matchups) {
		if CellAvailabilityOf(cell.N, minCellN) == AvailabilityPublished {
			pairCounts = append(pairCounts, cell.N)
		}
	}
	hasSample := matchups != nil && len(pairCounts) > 0

	island, champions := buildHeatmap(matchups, site, role, minCellN, query)

	view := matchupView{
		base:         base{Site: site, SiteURL: r.siteURL, R: r},
		Label:        label,
		LabelLower:   strings.ToLower(label),
		Slug:         slug,
		Patch:        snap.Patch,
		HasPartition: snap.Partition != nil,
		Intro:        matchupIntro(label, snap),
		HasSample:    hasSample,
		Island:       island,
		MinCellN:     minCellN,
		Links:        heatmapLinks(champions, slug, strings.ToLower(label)),
		ColumnNote:   trustedHTML(columnAvailabilityNote(minCellN, suppressed)),
		Tail:         matchupTail(label, minCellN, snap),
	}
	if snap.Partition != nil {
		view.Region = snap.Partition.Region
		view.Queue = snap.Partition.Queue
		view.Bracket = string(snap.Partition.Bracket)
		view.Window = WindowLabel(snap.Partition.SourceWindow.From, snap.Partition.SourceWindow.To)
		view.Generated = UTCStampAny(snap.Partition.GeneratedAt)
	}
	if hasSample {
		sorted := append([]int(nil), pairCounts...)
		sort.Ints(sorted)
		median := medianInt(sorted)
		view.Notice = sampleNotice(median, minCellN, suppressed, roleFor(role))
		view.Median = "n above is the median number of games behind a champion pair in this role, not a total: " +
			"a match contains many pairs, so a matrix-wide total would count the same game once per pair it " +
			"contains and would mean less than the pair sample does. Pair samples in this role run from n = " +
			IntegerAny(float64(sorted[0])) + " to n = " + IntegerAny(float64(sorted[len(sorted)-1])) +
			" games over " + IntegerAny(float64(len(sorted))) + " published pairs."
	} else {
		view.Empty = matchupEmpty(label, snap, minCellN)
	}
	if interactive && hasSample {
		view.HasBar = true
		view.Bar = matchupBar("/matchups/"+slug, query, island)
	}

	title := label + " champion matchups, patch " + snap.Patch + " - League of Legends - " + SiteName
	if snap.Partition == nil {
		title = label + " champion matchups - League of Legends - " + SiteName
	}
	page := &Page{
		Title:         title,
		Description:   matchupDescription(label, snap),
		CanonicalPath: "/matchups/" + slug + "/",
		Active:        "/matchups/" + slug,
		Partition:     snap.Partition,
		Noindex:       snap.Partition == nil,
	}
	if snap.Patch == notPublishedLabel {
		page.PatchLabelValue = notPublishedLabel
	}
	page.JSONLD, err = matchupJSONLD(r, snap, label, "/matchups/"+slug, minCellN, view.Notice.N)
	if err != nil {
		return nil, err
	}
	body, err := r.RenderBody("page:matchups", view)
	if err != nil {
		return nil, err
	}
	page.Body = body
	return page, nil
}

// cellsOf is a nil-safe accessor, so an absent artifact reads as no pairs
// rather than as a nil dereference.
func cellsOf(matchups *aggmodel.Matchups) []aggmodel.MatchupCell {
	if matchups == nil {
		return nil
	}
	return matchups.Cells
}

// medianInt is lib/format's median over an already-sorted sample: the middle
// value of an odd-length sample and the rounded midpoint of the two middle
// values of an even one. Taking the lower middle would be a different number.
func medianInt(sorted []int) int { return Median(sorted) }

// matchupIntro is MatchupBody.astro's opening paragraph, including the preview
// sentence on a demo build.
func matchupIntro(label string, snap snapshotView) string {
	if snap.Partition == nil {
		return "This table will show champion against champion matchups in " + strings.ToLower(label) +
			" as soon as a snapshot is published."
	}
	intro := "Champion against champion in " + strings.ToLower(label) + ", as the win rate of the row champion " +
		"in the pair. Each pair is measured over the source window below and only over matches in ranked solo queue."
	if preview := previewNumbersSentence(snap.Site); preview != "" {
		intro += " " + preview
	}
	return intro
}

// matchupTail is the "What this table is" paragraph.
func matchupTail(label string, minCellN int, snap snapshotView) string {
	tail := "A cell holds the win rate of the row champion against the column champion, with the number of games " +
		"it was measured over. Cells whose pair has fewer than n = " + IntegerAny(float64(minCellN)) + " games read " +
		"\"" + WithheldLiteral + "\" and show their count only; that is a pair this site declines to publish a rate " +
		"for, not a pair that does not exist."
	if preview := previewNumbersSentence(snap.Site); preview != "" {
		tail += " " + preview
	}
	_ = label
	return tail
}

// matchupDescription is the page's meta description, transcribed from
// the retired web/ tree's matchups/[role].astro.
func matchupDescription(label string, snap snapshotView) string {
	if snap.Partition == nil {
		return "The " + strings.ToLower(label) + " champion matchup matrix is not published yet: no aggregate " +
			"snapshot exists for this site, so this page renders an explicit empty state."
	}
	return "Champion-versus-champion win rates for " + strings.ToLower(label) + " in " + snap.Partition.Region +
		" ranked solo queue on League of Legends patch " + snap.Patch + ", with the games behind every pair and " +
		"thin pairs withheld."
}

// matchupEmpty is MatchupBody.astro's EmptyState for this route.
func matchupEmpty(label string, snap snapshotView, minCellN int) emptyView {
	if snap.Partition == nil {
		return emptyFor("No sample yet", emptyReason(snap.Site), nil)
	}
	threshold := minCellN
	return emptyFor(
		"No "+strings.ToLower(label)+" matchup cells published",
		"The snapshot for patch "+snap.Patch+" has no matchup artifact for this role, so no pair is shown rather "+
			"than an estimate.",
		&threshold,
	)
}

// The matrix is quadratic in the champion pool: every pair in a role is a cell,
// and every pair the artifact does not carry is a dash. A live role lists a
// hundred and sixty champions for a handful of stored cells, which is megabytes
// of markup spent on the pairs nobody measured. The grid is therefore bounded
// twice over - the axes are the champions the artifact's cells actually name,
// and a window the URL controls carries the rest of them the way the tier list's
// rows are carried.
// The three bounds below are sized from measurement, not from the plan's budget
// divided by a guess. A cell that carries a pairing - n, a tone class and the
// aria-label that spells the pairing out - is 224 B on average and 244 B at its
// longest in the demo tree, and the dash for a pair the artifact does not carry
// is 99 B; everything that is not a cell - the inlined stylesheet, the champion
// links for the whole pool, the row and column heads, the notices, the pager and
// the island's script - is 53,015 B on the live mid page and 51,791 B on the live
// bottom page, and 33,881 B on the live top page, which has no grid at all. Those
// are the widths measured against the live tier on 16.18, and the plan's ceiling
// is 150 KB of HTML with the brief's target for the default view at 100 KB, so
// even every-cell-published has to stay well inside 100 KB.
const (
	// DefaultMatrixPer is how many champions each axis of the matrix carries
	// when the query asks for no window: 12 x 12 = 144 cells, which is 35 KB at
	// the widest cell measured and so at most 88 KB of page against the heaviest
	// live page furniture measured. One number governs both axes because the
	// cost of the page is their product, and the tier list's habit of 20 rows a
	// page has no counterpart here: 20 x 20 would be 98 KB of cells and would
	// sit within a few KB of the brief's target with nothing left for growth.
	DefaultMatrixPer = 12
	// MaxMatrixPer caps ?per= on this route. MaxPer's ceiling of 200 rows has no
	// counterpart here: the window is quadratic, so a larger window is refused
	// rather than served, the same rule boundedPer applies to ?per= itself.
	// Thirty columns is 30 x 9 = 270 cells under MaxMatrixCells, 66 KB at the
	// widest cell measured, and so at most 119 KB of page against the heaviest
	// furniture measured.
	MaxMatrixPer = 30
	// MaxMatrixCells caps the cells one page renders when the server, rather
	// than the reader, has to choose how many rows fit. A filter's columns are
	// the whole role - a champion's row is its row across every measured
	// opponent - so a filter matching half the roster would otherwise be half
	// the roster by 165 columns, and a filter's page would be unbounded in
	// exactly the way the unfiltered grid no longer is. The cap is set to the
	// widest page the shipped corpus can ask for at the default window: ?q=a
	// matches 23 of the mid role's 28 champions, and 23 columns of 12 rows is
	// 276. That is 67 KB at the widest cell measured, 120 KB of the plan's
	// 150 KB against the heaviest furniture measured, and it keeps a filter
	// paging twelve rows a page like the grid it filters, so narrowing the grid
	// to a champion does not also change the shape of the page it lands on.
	MaxMatrixCells = 276
)

// matrixChampion is one champion on the matrix's axes: the artifact's id
// resolved to the name and slug a champion page lives at, or the id spelled out
// when the snapshot does not name it.
type matrixChampion struct {
	ID   int
	Name string
	Slug string
}

// matrixChampions resolves the artifact's champion list, in artifact order. It
// is the pool the axes are drawn from, not the axes themselves.
func matrixChampions(matchups *aggmodel.Matchups, site *Site) []matrixChampion {
	if matchups == nil {
		return nil
	}
	champions := make([]matrixChampion, 0, len(matchups.Champions))
	for _, id := range matchups.Champions {
		entry := matrixChampion{ID: id, Name: "Champion " + IntegerAny(float64(id)), Slug: IntegerAny(float64(id))}
		if champion, ok := site.ChampionByID(id); ok {
			entry.Name, entry.Slug = champion.Name, champion.Slug
		}
		champions = append(champions, entry)
	}
	return champions
}

// matrixAxis is the champions the artifact's cells name, ordered by the games
// those cells measure: this route's window takes the head of the axis, and the
// head of a games-ordered axis is the part of the matrix a reader came for. A
// champion no cell names has no pairing to report, so it can contribute nothing
// but a row and a column of dashes - and on a grid the dashes are what the page
// costs. Ties keep pool order, the order the rest of the page reads in.
func matrixAxis(pool []matrixChampion, matchups *aggmodel.Matchups) []matrixChampion {
	games := make(map[int]int, len(pool))
	for _, cell := range matchups.Cells {
		games[cell.ChampionID] += cell.N
		games[cell.OpponentID] += cell.N
	}
	axis := make([]matrixChampion, 0, len(pool))
	for _, champion := range pool {
		if _, named := games[champion.ID]; named {
			axis = append(axis, champion)
		}
	}
	sort.SliceStable(axis, func(i, j int) bool { return games[axis[i].ID] > games[axis[j].ID] })
	return axis
}

// matrixMatch is the filter's rows: the champions whose name or slug contains
// ?q=. Rows only. A filter is a question about a champion, and the answer to it
// is that champion's whole row across the full column axis - the shape the grid
// already has, so a cell and its mirror stay complements of the same games
// rather than two samples of them.
func matrixMatch(axis []matrixChampion, filter string) []matrixChampion {
	needle := strings.ToLower(strings.TrimSpace(filter))
	if needle == "" {
		return axis
	}
	matched := make([]matrixChampion, 0, len(axis))
	for _, champion := range axis {
		if strings.Contains(strings.ToLower(champion.Name+" "+champion.Slug), needle) {
			matched = append(matched, champion)
		}
	}
	return matched
}

// matrixPageHref is the address of one page of the matrix. Query.Href cannot
// build it: it drops `page` unless `per` is set, and this grid has a default
// window, so a link to the first page would come out with no query at all and
// would point at the page the reader is already on. Everything the reader is
// carrying - the filter, an explicit window, the patch - is kept.
func matrixPageHref(query Query, page int) string {
	values := query.Values(DefaultMatchupQuery())
	values.Set("page", strconv.Itoa(page))
	return "?" + values.Encode()
}

// matrixPer is the window ?per= asks for, inside this route's bounds, and
// DefaultMatrixPer when it asks for nothing. A value below MinPer is the
// no-pagination spelling boundedPer already normalises to zero.
func matrixPer(requested int) int {
	if requested < MinPer {
		return DefaultMatrixPer
	}
	if requested > MaxMatrixPer {
		return MaxMatrixPer
	}
	return requested
}

// matrixFit trims the rows one page carries to what the columns it renders cost.
// An unfiltered page is per by per and fits by construction; a filtered page is
// a page of rows against the whole role, so MaxMatrixCells is what stops a filter
// matching forty champions from rendering forty rows of a hundred and sixty-five
// cells each. One row is the floor: a page that showed no row would be a page
// that showed nothing, and a row of a full column axis is still the smallest
// honest answer to a filter.
func matrixFit(columns, per int) int {
	if columns <= 0 {
		return per
	}
	fit := MaxMatrixCells / columns
	if fit < 1 {
		fit = 1
	}
	if fit > per {
		return per
	}
	return fit
}

// matrixPage applies ?page= to the rows a page carries, the way paginate applies
// it to the tier list's ordered rows. The rows are what is paged, whatever the
// columns are: a page of a bounded window and a page of a filter's rows are the
// same arithmetic over different sets. `what` names those rows in the pager's
// label, which the page renders verbatim.
func matrixPage(rows []matrixChampion, per int, query Query, what string) ([]matrixChampion, *pagerView) {
	if per < 1 || len(rows) <= per {
		return rows, nil
	}
	pages := (len(rows) + per - 1) / per
	page := query.Page
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * per
	end := start + per
	if end > len(rows) {
		end = len(rows)
	}
	nav := pagerView{
		Label: "Page " + IntegerAny(float64(page)) + " of " + IntegerAny(float64(pages)) +
			", " + what + " " + IntegerAny(float64(start+1)) + "-" + IntegerAny(float64(end)) + " of " +
			IntegerAny(float64(len(rows))),
	}
	if page > 1 {
		nav.HasPrev = true
		nav.Prev = matrixPageHref(query, page-1)
	}
	if page < pages {
		nav.HasNext = true
		nav.Next = matrixPageHref(query, page+1)
	}
	return rows[start:end], &nav
}

// matrixScope is the slice of the role's matrix one page renders, and what it
// left out. A reader who cannot see the whole matrix has to be told what the
// frame in front of them leaves out and where the rest of it is: otherwise a
// bounded grid reads as a complete one, which is the failure this page exists to
// avoid. A grid that is the whole matrix and carries no filter is already
// described by the note's existing sentences, so note returns nothing and the
// page reads as it always did.
//
// Pool is every champion the artifact lists for the role, Stored is the ones its
// cells name, Columns is what the grid renders across, Rows is the rows before
// paging, Window is the page of them, and Filter is the ?q= it was read with.
type matrixScope struct {
	Role    aggmodel.Role
	Pool    []matrixChampion
	Stored  []matrixChampion
	Columns []matrixChampion
	Rows    []matrixChampion
	Window  []matrixChampion
	PerPage int
	Filter  string
}

// Filtered reports whether the grid was narrowed by ?q= rather than by the
// page's own bounds.
func (scope matrixScope) Filtered() bool { return strings.TrimSpace(scope.Filter) != "" }

// note is the disclosure the grid's note ends with.
func (scope matrixScope) note() string {
	var note strings.Builder
	if len(scope.Stored) < len(scope.Pool) {
		note.WriteString(" Only the " + IntegerAny(float64(len(scope.Stored))) + " champions the artifact stores a cell for are " +
			"rows and columns here, out of the " + IntegerAny(float64(len(scope.Pool))) + " it lists for this role: a champion no " +
			"stored cell names has no pairing to report, so it is named in the list under the grid instead of " +
			"filling a row and a column of dashes.")
	}
	if scope.Filtered() {
		if len(scope.Rows) == 0 {
			note.WriteString(" No champion in " + RoleLabel(scope.Role) + " matches that filter, so no row is shown: " +
				"clear it to see the grid again.")
			return note.String()
		}
		note.WriteString(" The filter carries a row for each of the " + IntegerAny(float64(len(scope.Rows))) +
			" champions whose name or slug contains \"" + EscapeString(strings.TrimSpace(scope.Filter)) + "\", " +
			"against all " + IntegerAny(float64(len(scope.Columns))) + " champions this role measures: every row " +
			"shown is a champion's complete row rather than the window of the matrix the grid would otherwise " +
			"put it in.")
		if len(scope.Rows) > scope.PerPage {
			if scope.PerPage <= 1 {
				note.WriteString(" They are one to a page, because a complete row already spans the role's whole " +
					"axis: the pager under the grid walks them.")
			} else {
				note.WriteString(" They are carried " + IntegerAny(float64(scope.PerPage)) +
					" to a page, walked with the pager under the grid.")
			}
		} else {
			note.WriteString(" They fit on one page.")
		}
		note.WriteString(" Clear the filter for the bounded grid.")
		return note.String()
	}
	// Nothing is left out only when every stored champion is a column and they
	// all fit on one page. Rows paged under a full set of columns still leaves
	// rows out, and a reader who is not told that reads the page as the matrix.
	if len(scope.Columns) >= len(scope.Stored) && len(scope.Rows) <= scope.PerPage {
		return note.String()
	}
	// The columns and the rows narrow independently - the columns are a window
	// of the axis ordered by games, the rows are a page of it - so the sentence
	// says only what is true of this page: a page whose columns are the whole
	// axis and whose rows are paged is paged, not windowed.
	var windowed []string
	if len(scope.Columns) < len(scope.Stored) {
		windowed = append(windowed, "its "+IntegerAny(float64(len(scope.Columns)))+" columns are the "+
			IntegerAny(float64(len(scope.Stored)))+" champions "+RoleLabel(scope.Role)+" measured the most "+
			"games for")
	}
	if len(scope.Rows) > scope.PerPage {
		windowed = append(windowed, "its rows are the role's "+IntegerAny(float64(len(scope.Stored)))+
			" champions, "+IntegerAny(float64(scope.PerPage))+" to a page, so the pager under the grid "+
			"carries the rest of them")
	}
	note.WriteString(" The grid is a window of the matrix rather than the whole of it: " +
		strings.Join(windowed, ", and ") + ". A champion the window does not carry is one this page does " +
		"not show, never one the snapshot lacks: the filter carries any champion's complete row across all " +
		IntegerAny(float64(len(scope.Stored))) + " columns the artifact measures, and every champion in the role " +
		"is linked under the grid.")
	return note.String()
}

// buildHeatmap materialises both orientations of every stored pair, then renders
// the window of the matrix as HTML. A pair the artifact does not carry, and a
// pair whose sample falls below the threshold, both render as a dash: a withheld
// pair is a refusal to publish a rate, never a zero. The second return value is
// the artifact's champion pool, which the page needs for the one link per
// champion under the grid: the grid is a window, the links are the whole role.
func buildHeatmap(matchups *aggmodel.Matchups, site *Site, role aggmodel.Role, minCellN int, query Query) (heatmapView, []matrixChampion) {
	view := heatmapView{MinCellN: minCellN}
	champions := matrixChampions(matchups, site)
	if matchups == nil {
		return view, champions
	}
	slug := RoleSlugString(role)

	type pairResult struct {
		n       int
		winRate float64
	}
	pairs := make(map[pairKey]pairResult, len(matchups.Cells)*2)
	for _, cell := range matchups.Cells {
		pairs[pairKey{cell.ChampionID, cell.OpponentID}] = pairResult{n: cell.N, winRate: cell.WinRate}
		pairs[pairKey{cell.OpponentID, cell.ChampionID}] = pairResult{n: cell.N, winRate: 1 - cell.WinRate}
	}

	published := 0
	for _, result := range pairs {
		if CellAvailabilityOf(result.n, minCellN) == AvailabilityPublished {
			published++
		}
	}

	stored := matrixAxis(champions, matchups)
	// Two shapes, one pager. Without a filter the grid is a window of the role
	// ordered by games: `per` columns, and rows carried `per` at a time, so the
	// widest page is the per x per square. A filter is the reader's own choice of
	// rows, so it keeps every column and the cell budget - not the window - is
	// what bounds the page.
	rows, columns, per := stored, stored, matrixPer(query.Per)
	if strings.TrimSpace(query.Filter) != "" {
		rows = matrixMatch(stored, query.Filter)
	} else if len(columns) > per {
		columns = columns[:per]
	}
	per = matrixFit(len(columns), per)
	window, nav := matrixPage(rows, per, query, "champions")
	if nav != nil {
		view.HasNav = true
		view.Nav = *nav
	}

	view.Rows = make([]matchupRowView, 0, len(window))
	view.Columns = make([]matchupColumnView, 0, len(columns))
	for _, champion := range columns {
		view.Columns = append(view.Columns, matchupColumnView{ID: champion.ID, Name: champion.Name})
	}
	for _, row := range window {
		rendered := matchupRowView{
			ID:    row.ID,
			Name:  row.Name,
			Slug:  row.Slug,
			Href:  "/champions/" + row.Slug + "/" + slug,
			Cells: make([]matchupCellView, 0, len(columns)),
		}
		for index, column := range columns {
			tabstop := ""
			if index == 0 {
				tabstop = ` tabindex="0"`
			}
			switch column.ID {
			case row.ID:
				rendered.Cells = append(rendered.Cells, matchupCellView{Markup: trustedHTML(
					`<td class="cell self" data-n="0"` + tabstop + ` aria-label="not applicable: the same champion ` +
						`in both axes, a champion is never matched against itself" data-astro-cid-5wqubv3u>&middot;</td>`)})
			default:
				result, ok := pairs[pairKey{row.ID, column.ID}]
				if !ok || CellAvailabilityOf(result.n, minCellN) != AvailabilityPublished {
					rendered.Cells = append(rendered.Cells, matchupCellView{Markup: trustedHTML(
						`<td class="cell missing" data-n="0"` + tabstop + ` aria-label="not published" ` +
							`data-astro-cid-5wqubv3u>&mdash;</td>`)})
					continue
				}
				deviation := result.winRate - 0.5
				rendered.Cells = append(rendered.Cells, matchupCellView{Markup: trustedHTML(
					`<td class="cell ` + matchTone(deviation) + `" data-n="` + itoa(result.n) +
						`"` + tabstop + ` aria-label="` + EscapeString(Percent(result.winRate, 1)) + ` over ` +
						EscapeString(IntegerAny(float64(result.n))) + ` games" data-astro-cid-5wqubv3u>` +
						`<span class="rate" data-astro-cid-5wqubv3u>` + EscapeString(Percent(result.winRate, 1)) +
						`</span><span class="dev" aria-hidden="true" data-astro-cid-5wqubv3u>` +
						EscapeString(signedPoints(deviation)) + `</span></td>`)})
			}
		}
		view.Rows = append(view.Rows, rendered)
	}

	totalCells := len(champions) * len(champions)
	view.Caption = "Head to head, " + RoleLabel(role) + ", patch " + matchups.Patch + ", " +
		BracketLabel(matchups.Bracket) + ". Each row is the champion in the first column and each column is the " +
		"champion it faced; the cell is the row champion's win rate in that pairing, and the small signed number " +
		"beside it is the distance from even in percentage points. A dash means the pair is not published: either " +
		"the artifact stores no cell for it, or its sample is under n = " +
		IntegerAny(float64(minCellN)) + " games in this snapshot."
	view.Note = trustedHTML(IntegerAny(float64(published)) + " of " + IntegerAny(float64(totalCells)) +
		" pairings are published for this role. Cells withheld for being below the threshold are absent from the " +
		"artifact and read as a dash here, never as zero" + suppressedClause(matchups) + "." +
		matrixScope{
			Role:    role,
			Pool:    champions,
			Stored:  stored,
			Columns: columns,
			Rows:    rows,
			Window:  window,
			PerPage: per,
			Filter:  query.Filter,
		}.note())
	return view, champions
}

// suppressedClause is the parenthesised withheld count the note carries when the
// artifact reports one.
func suppressedClause(matchups *aggmodel.Matchups) string {
	if matchups.SuppressedCells <= 0 {
		return ""
	}
	return " (" + IntegerAny(float64(matchups.SuppressedCells)) + " withheld in total)"
}

// pairKey is a directed pair, in the emitted order and never sorted, matching
// lib/rows.ts's pairKey.
type pairKey struct {
	champion int
	opponent int
}

// matchTone is HeatmapIsland's toneOf: the fill is a redundant encoding of the
// signed number printed beside it, and nothing more.
func matchTone(deviation float64) string {
	switch {
	case deviation >= 0.05:
		return "up-strong"
	case deviation > 0.005:
		return "up"
	case deviation <= -0.05:
		return "down-strong"
	case deviation < -0.005:
		return "down"
	}
	return "level"
}

// signedPoints is the signed deviation from even in percentage points without
// the unit, which the legend names once.
func signedPoints(deviation float64) string {
	points := deviation * 100
	if points < 0 {
		if -points < 0.05 {
			return "0.0"
		}
		return "-" + fixed(-points, 1)
	}
	if points < 0.05 {
		return "0.0"
	}
	return "+" + fixed(points, 1)
}

// heatmapLinks is the champion-page list under the matrix: one link per champion
// in the artifact's pool, in artifact order - the whole role, not the window the
// grid happens to show, because it is how a reader reaches the champions the
// grid leaves out.
func heatmapLinks(champions []matrixChampion, slug, labelLower string) []linkView {
	links := make([]linkView, 0, len(champions))
	for _, champion := range champions {
		links = append(links, linkView{
			Href: "/champions/" + champion.Slug + "/" + slug,
			Name: champion.Name + " " + labelLower + " matchups",
		})
	}
	return links
}

// matchupBar is the no-JavaScript control surface: a filter and a reset, both
// plain query parameters. Sorting a matrix by a champion name has no meaning
// without the client's roving-tabindex behaviour, so the form offers only what
// the server can honour.
func matchupBar(action string, query Query, island heatmapView) barView {
	def := DefaultMatchupQuery()
	bar := barView{Action: action}
	bar.Fields = append(bar.Fields, barField{
		Name:   "q",
		Label:  "Filter champions",
		Kind:   "search",
		Value:  query.Filter,
		MaxLen: MaxFilterLen,
	})
	if len(query.Values(def)) == 0 {
		return bar
	}
	bar.HasClear = true
	bar.Clear = action
	return bar
}

// matchupJSONLD is the page's structured-data node. Like the tier list's, it is
// emitted only for a live snapshot: a preview's numbers are not measurements and
// must not be published as a dataset.
func matchupJSONLD(r *Renderer, snap snapshotView, label string, path string, minCellN int, sampleSize int) (string, error) {
	if snap.Partition == nil || !snap.Site.Live() {
		return "", nil
	}
	partition := snap.Partition
	node, err := datasetNode(datasetNodeInput{
		URL:  r.absolute(path + "/"),
		Name: label + " champion matchups, League of Legends patch " + partition.Patch,
		Description: "Champion-versus-champion win rates for " + strings.ToLower(label) + " in " +
			partition.Region + " ranked solo queue, patch " + partition.Patch + ", measured over " +
			WindowLabel(partition.SourceWindow.From, partition.SourceWindow.To) + ".",
		Patch:        partition.Patch,
		SourceWindow: partition.SourceWindow,
		GeneratedAt:  partition.GeneratedAt,
		MinCellN:     minCellN,
		SampleSize:   sampleSize,
		Variables: []datasetVariable{
			{Name: "games played in the pair (n)", Unit: "games"},
			{Name: "pair win rate", Unit: "win rate"},
		},
		State: string(snap.Site.State()),
	})
	if err != nil {
		return "", err
	}
	return JSONLDNode(node)
}
