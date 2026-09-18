package webtier

// The explorer: /explore, and the two downloads that carry the published
// artifact itself, /explore/export.csv and /explore/export.json.
//
// The page computes no statistic. Every rate, interval and sample size on it is
// read from the published agg/v1 snapshot, and a rate the snapshot does not
// publish is rendered as withheld, with the count that made it withheld, never
// as zero and never as an empty cell. The JSON download is the artifact's own
// bytes, so that a reader can diff it against the published file; the CSV is a
// projection of the same cells and is labelled as one.

import (
	"bytes"
	"encoding/csv"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

const (
	// exploreRoleParam and exploreFloorParam are this page's own two parameters.
	// They are parsed here rather than in query.go because they select what this
	// page shows, and a route that is already scoped by its path has no other
	// reader for them.
	exploreRoleParam  = "role"
	exploreFloorParam = "min_n"

	// exploreCSVContentType is the CSV download's type. The JSON download is
	// jsonContentType, because it is the artifact's own bytes.
	exploreCSVContentType = "text/csv; charset=utf-8"

	// exploreExportCSVPath and exploreExportJSONPath are the frozen download
	// paths. There is deliberately no route under /agg/: the snapshot tree is not
	// an HTTP surface of this tier, and these two handlers are the only way to
	// read it.
	exploreExportCSVPath  = "/explore/export.csv"
	exploreExportJSONPath = "/explore/export.json"

	// exploreMaxFloor bounds a floor a request may ask for. It is above every
	// published sample size, so a reader can ask for a view in which every cell is
	// withheld, which is how the page shows what withheld means.
	exploreMaxFloor = 1000000
)

// exploreFloors are the floors a reader may ask this view to apply to a rate, in
// games. They are absolute rather than multiples of the snapshot's own
// threshold, so that "at least n = 5,000 games" means the same thing whichever
// partition is published. The snapshot's own floor is offered as well, and is
// the default, so the ordinary view of the page is the published one.
var exploreFloors = []int{1000, 2000, 5000, 10000, 25000}

// exploreParameter carries the two parameters this page reads itself, so that
// every link and hidden field the page builds can carry them unchanged.
type exploreParameter struct {
	// Role is a published role, or "" for every role.
	Role aggmodel.Role
	// Floor is the floor this view applies to a rate, in games.
	Floor int
	// Asked is true when the request named a floor itself.
	Asked bool
}

// parseExploreParameter reads the two parameters off the request. An unknown
// role or an unreadable floor is not an error: it is reported, so that the page
// can say what it did with the request, and the view falls back to its default
// rather than rendering a selection that does not exist.
func parseExploreParameter(values url.Values) (exploreParameter, []string) {
	var (
		filter exploreParameter
		notes  []string
	)
	if raw := strings.TrimSpace(values.Get(exploreRoleParam)); raw != "" {
		if role, ok := roleByName(raw); ok {
			filter.Role = role
		} else {
			notes = append(notes, "No published role is named "+exploreQuote(raw)+", so this view shows every role.")
		}
	}
	if raw := strings.TrimSpace(values.Get(exploreFloorParam)); raw != "" {
		filter.Asked = true
		if floor, err := strconv.Atoi(raw); err == nil && floor >= 0 && floor <= exploreMaxFloor {
			filter.Floor = floor
		} else {
			notes = append(notes, "A floor of "+exploreQuote(raw)+" games is not a number this view can apply, so the floor selected below applies instead.")
		}
	}
	return filter, notes
}

// exploreQuote renders a request value for quoting back into prose. It is
// trimmed to the same length the filter is, so that a long query string cannot
// push the page's own sentence around.
func exploreQuote(raw string) string {
	return strconv.Quote(truncateRunes(strings.TrimSpace(raw), MaxFilterLen))
}

// forSnapshot resolves the request's floor against a published partition. A
// request that asked for no floor gets the snapshot's own, and a floor below the
// snapshot's own is raised to it: a published cell can never be rendered as if
// it were below the threshold its publication used.
func (f exploreParameter) forSnapshot(partition *aggmodel.Partition) exploreParameter {
	resolved := f
	if resolved.Floor < partition.MinCellN {
		resolved.Floor = partition.MinCellN
	}
	return resolved
}

// raised reports whether this view applies a floor above the snapshot's own,
// which is the only way a published cell can be rendered as withheld.
func (f exploreParameter) raised(partition *aggmodel.Partition) bool {
	return f.Floor > partition.MinCellN
}

// roleByName resolves a role as the snapshot publishes it, accepting the URL
// slug beside the display label, because a reader who shares a link should not
// have to know which spelling the site writes.
func roleByName(raw string) (aggmodel.Role, bool) {
	trimmed := strings.TrimSpace(raw)
	if role, ok := RoleFromSlug(strings.ToLower(trimmed)); ok {
		return role, true
	}
	for _, role := range Roles {
		if strings.EqualFold(RoleLabel(role), trimmed) {
			return role, true
		}
	}
	return "", false
}

// exploreColumns is the column set this page renders: every column of the
// published cell, and the role it was played in. The tier-list column set with
// the role is the artifact's own shape, so the table and the download cannot
// disagree about which columns a cell has.
func exploreColumns() []sortColumn {
	return TierListColumns(true)
}

// explorePerOptions are the row windows the explorer offers. The default is a
// hundred rather than the whole selection: the explorer spans every role at
// once, so an unpaged table is several times the size of a tier list, and the
// page-quality budget in the plan is measured per route. "All rows" stays on the
// list because a reader who asks for the whole table should get it.
var explorePerOptions = []int{0, 25, 50, 100, 200}

// DefaultExploreQuery is the view an unfiltered request gets: the busiest cells
// first, a hundred rows to a page, and the newest published patch. The default
// is also what a link drops, so a shared URL names only what it changes.
func DefaultExploreQuery() Query {
	return Query{Sort: "n", Dir: DirDesc, Per: 100}
}

// exploreFilteredCells narrows the published cells to the selection the request
// asks for. The published artifact is never rewritten and never re-measured:
// this is a view of it.
func exploreFilteredCells(cells []aggmodel.Cell, filter exploreParameter) []aggmodel.Cell {
	if filter.Role == "" {
		return cells
	}
	kept := make([]aggmodel.Cell, 0, len(cells))
	for _, cell := range cells {
		if cell.Role == filter.Role {
			kept = append(kept, cell)
		}
	}
	return kept
}

// exploreWindow applies ?per= and ?page= to the ordered rows, and builds the
// pagination links from the whole view rather than from the shared query alone:
// the role and the floor are this page's own parameters, and a link that
// silently dropped them would land a reader on a different table than the one
// they were reading.
func exploreWindow(rows []*tableRow, query Query, filter exploreParameter, path string) ([]*tableRow, *pagerView) {
	if query.Per <= 0 || len(rows) <= query.Per {
		return rows, nil
	}
	pages := (len(rows) + query.Per - 1) / query.Per
	page := query.Page
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * query.Per
	end := start + query.Per
	if end > len(rows) {
		end = len(rows)
	}
	nav := pagerView{
		Label: "Page " + IntegerAny(float64(page)) + " of " + IntegerAny(float64(pages)) +
			", rows " + IntegerAny(float64(start+1)) + "-" + IntegerAny(float64(end)) +
			" of " + IntegerAny(float64(len(rows))),
	}
	if page > 1 {
		nav.HasPrev = true
		nav.Prev = exploreHref(path, query, filter, page-1)
	}
	if page < pages {
		nav.HasNext = true
		nav.Next = exploreHref(path, query, filter, page+1)
	}
	return rows[start:end], &nav
}

// exploreHref builds a link to this page carrying the whole view: the query the
// shared reader parsed, the two parameters this page parses itself, and the page
// number. The first page is named by the absence of ?page=, so a link back to it
// is the same address a reader would have typed.
func exploreHref(path string, query Query, filter exploreParameter, page int) string {
	values := query.Values(DefaultExploreQuery())
	if filter.Role != "" {
		values.Set(exploreRoleParam, string(filter.Role))
	}
	if filter.Asked {
		values.Set(exploreFloorParam, strconv.Itoa(filter.Floor))
	}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	if encoded := values.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}

// exploreSelectionHref is a link that changes one of the two parameters this
// page parses itself, and goes back to the first page of the new selection: the
// third page of a narrowed table is not the third page of the table it narrows.
func exploreSelectionHref(path string, query Query, filter exploreParameter, role string, floor int) string {
	next := filter
	if role == "" {
		next.Role = ""
	} else if value, ok := roleByName(role); ok {
		next.Role = value
	}
	next.Floor, next.Asked = floor, floor > 0
	nextQuery := query
	nextQuery.Page = 1
	return exploreHref(path, nextQuery, next, 1)
}

// exploreFloorOptions are the floors the selection offers, the snapshot's own
// floor first and spelled as the floor the artifact was filtered at.
func exploreFloorOptions(partition *aggmodel.Partition, filter exploreParameter) []optionView {
	options := []optionView{{
		Value:    strconv.Itoa(partition.MinCellN),
		Label:    "at least n = " + IntegerAny(float64(partition.MinCellN)) + " games (the snapshot's own)",
		Selected: filter.Floor == partition.MinCellN,
	}}
	for _, floor := range exploreFloors {
		if floor <= partition.MinCellN {
			continue
		}
		options = append(options, optionView{
			Value:    strconv.Itoa(floor),
			Label:    "at least n = " + IntegerAny(float64(floor)) + " games",
			Selected: filter.Floor == floor,
		})
	}
	return options
}

// exploreRoleOptions are the roles the selection offers, every role first.
func exploreRoleOptions(filter exploreParameter) []optionView {
	options := []optionView{{Value: "", Label: "every role", Selected: filter.Role == ""}}
	for _, role := range Roles {
		options = append(options, optionView{
			Value: string(role), Label: RoleLabel(role), Selected: filter.Role == role,
		})
	}
	return options
}

// exploreRoleLinks are the roles as addresses. They are the same choice the
// bar's form offers, spelled as a URL, so a reader without JavaScript and a
// reader sending someone a selection both get a link that reproduces the view.
func exploreRoleLinks(path string, query Query, filter exploreParameter) []linkView {
	links := []linkView{{Name: "Every role", Href: exploreSelectionHref(path, query, filter, "", filter.Floor)}}
	for _, role := range Roles {
		links = append(links, linkView{
			Name: RoleLabel(role),
			Href: exploreSelectionHref(path, query, filter, string(role), filter.Floor),
		})
	}
	return links
}

// exploreFloorLinks are the floors as addresses. A raised floor is the view in
// which a published cell is withheld, so it is a link rather than a hidden
// trick: a reader can be sent to the page that shows what withheld looks like.
func exploreFloorLinks(path string, query Query, filter exploreParameter, partition *aggmodel.Partition) []linkView {
	links := []linkView{{
		Name: "At least n = " + IntegerAny(float64(partition.MinCellN)) + " games (the snapshot's own floor)",
		Href: exploreSelectionHref(path, query, filter, string(filter.Role), partition.MinCellN),
	}}
	for _, floor := range exploreFloors {
		if floor <= partition.MinCellN {
			continue
		}
		links = append(links, linkView{
			Name: "At least n = " + IntegerAny(float64(floor)) + " games",
			Href: exploreSelectionHref(path, query, filter, string(filter.Role), floor),
		})
	}
	return links
}

// exploreView is the explorer's own data: the selection on screen, the
// provenance of the snapshot it is a view of, and the two downloads.
type exploreView struct {
	base

	// Provenance, exactly as the artifact declares it.
	HasSnapshot bool
	DataState   string
	Patch       string
	Region      string
	Queue       int
	Bracket     string
	Window      string
	Generated   string
	Patches     []patchEntry

	// The selection.
	Role          aggmodel.Role
	RoleLabel     string
	Floor         int
	FloorText     string
	MinCellN      int
	MinCellNText  string
	Raised        bool
	RaisedNote    string
	Notes         []string
	CellCount     string
	ChampionCount string
	Suppressed    int
	HasSuppressed bool
	CellGames     string
	RowCount      string

	// The table.
	HasRows        bool
	HasSample      bool
	Island         islandView
	ShowRole       bool
	Empty          emptyView
	Notice         sampleNoticeView
	Note           template.HTML
	HasUnmeasured  bool
	UnmeasuredNote string

	// The controls.
	HasBar     bool
	Bar        barView
	HasPageNav bool
	PageNav    pagerView

	// The selection as addresses, for a reader without JavaScript.
	RoleLinks  []linkView
	FloorLinks []linkView

	// The downloads.
	Exports    []linkView
	ExportNote string

	// Prose.
	Intro string
	Tail  string
	Links []linkView
}

// ExplorePage renders /explore: a view of the published aggregate snapshot with
// the columns of the artifact itself, the two parameters this page reads on top
// of the shared query, and the downloads that carry the artifact.
//
// The page is a view of the snapshot, never a measurement of its own: every
// number below is read from the published cells, and a cell the selection may
// not publish reads "withheld" with its count rather than a zero.
func (r *Renderer) ExplorePage(values url.Values, interactive bool) (*Page, error) {
	const path = explorePath
	def := DefaultExploreQuery()
	query := ParseQuery(values, def, SortKeys(exploreColumns()))
	// ParseQuery reads ?per= itself and treats its absence as "every row", which
	// is the tier list's default and not this page's: the explorer spans every
	// role at once, so an unpaged table is several times a tier list's weight.
	// The fallback's window is therefore applied here, where the window is this
	// page's own decision, and an explicit ?per= - including ?per=0 for the whole
	// table - still wins.
	if strings.TrimSpace(values.Get("per")) == "" {
		query.Per = def.Per
	}
	filter, notes := parseExploreParameter(values)

	snap, err := r.snapshot(query.Patch)
	if err != nil {
		return nil, err
	}
	site := snap.Site

	var tierList *aggmodel.TierList
	if snap.HasSeg {
		// The manifest publishes a partition only when its artifacts are there.
		// A missing or unreadable tier list under that promise is an incomplete
		// publication, which is an error rather than a table that quietly lost
		// its rows.
		loaded, err := site.TierList(snap.Seg)
		if err != nil {
			return nil, err
		}
		tierList = loaded
	}
	minCellN := snap.minCellN()
	suppressed := snap.suppressedCells()

	var allCells []aggmodel.Cell
	if tierList != nil {
		allCells = tierList.Cells
	}
	measured := make([]aggmodel.Cell, 0, len(allCells))
	for _, cell := range allCells {
		if cell.N > 0 {
			measured = append(measured, cell)
		}
	}
	unmeasured := len(allCells) - len(measured)

	if snap.Partition != nil {
		filter = filter.forSnapshot(snap.Partition)
	}

	// The selection is the whole snapshot's cells narrowed by the role and read
	// at the floor the reader asked for. The floor is what a published cell is
	// judged against, so a floor above the snapshot's own is how a cell that is
	// published becomes a cell this view may not print.
	selected := exploreFilteredCells(measured, filter)
	// The counts the view states about itself are counted over the selection it
	// renders, not over the whole snapshot: "n = 166,917 games in Jungle" is a
	// claim about the artifact that the artifact does not support when Jungle
	// holds 33,898 of them.
	published, cellGames := 0, 0
	for _, cell := range selected {
		if CellAvailabilityOf(cell.N, filter.Floor) == AvailabilityPublished {
			published++
			cellGames += cell.N
		}
	}
	// snapshotGames is the sample the whole artifact was published with, which is
	// what a statement about the artifact as a dataset may claim, whatever floor
	// this particular view is rendered at.
	snapshotGames := 0
	for _, cell := range measured {
		if CellAvailabilityOf(cell.N, minCellN) == AvailabilityPublished {
			snapshotGames += cell.N
		}
	}
	hasSample := snap.HasSeg && len(measured) > 0

	// The artifact's withheld count is a statement about the whole snapshot at
	// the producer's own floor. It is rendered snapshot-wide in the metadata
	// line below. A sentence that describes this view — its role and its floor —
	// must not carry it, because "513 further cells below that threshold" is not
	// true of a threshold this view invented or of a role this view narrowed to.
	raised := snap.Partition != nil && filter.raised(snap.Partition)
	withheld := suppressed
	if raised || filter.Role != "" {
		withheld = nil
	}

	rows := TierListRows(selected, site, filter.Floor, true)
	columns := exploreColumns()

	// The filter, the sort and the page are applied to the rows the server
	// already has, so the same URL produces the same table on any machine.
	filtered := FilterRows(rows, query.Filter)
	ordered := OrderRows(filtered, query.Sort, query.Dir)
	window, pageNav := exploreWindow(ordered, query, filter, path)

	island := islandView{
		UID:        tableIslandUID("all", snap.Patch, snap.bracketValue()),
		Cols:       columnsToIsland(columns),
		SortKey:    query.Sort,
		AriaSort:   ariaSort(query.Dir),
		Dir:        query.Dir,
		DirLabel:   dirWord(query.Dir),
		DirText:    dirArrow(query.Dir),
		StatusText: statusText(len(window), len(ordered), ColumnLabel(columns, query.Sort), query.Dir),
		Rows:       window,
		ColCount:   len(columns),
		Note:       trustedHTML(islandNote(filter.Floor, withheld)),
	}
	island.Caption = exploreCaption(snap, filter)

	view := exploreView{
		base:         base{Site: site, SiteURL: r.siteURL, R: r},
		HasSnapshot:  snap.HasSeg,
		DataState:    string(site.State()),
		Patch:        snap.Patch,
		Patches:      explorePatches(snap, query, filter),
		Role:         filter.Role,
		RoleLabel:    RoleLabel(filter.Role),
		Floor:        filter.Floor,
		FloorText:    IntegerAny(float64(filter.Floor)),
		MinCellN:     minCellN,
		MinCellNText: IntegerAny(float64(minCellN)),
		Raised:       raised,
		Notes:        notes,
		HasRows:      len(window) > 0,
		HasSample:    hasSample,
		Island:       island,
		ShowRole:     true,
		Notice:       sampleNotice(cellGames, filter.Floor, withheld, roleFor(filter.Role)),
		Note:         trustedHTML(columnAvailabilityNote(filter.Floor, withheld)),
		Empty:        exploreEmpty(snap, filter, published),
		Intro:        exploreIntro(snap),
		Tail:         exploreTail(snap, filter),
		Links:        exploreChampionLinks(measured, site),
		RoleLinks:    exploreRoleLinks(path, query, filter),
		CellCount:    IntegerAny(float64(len(measured))),
		CellGames:    IntegerAny(float64(cellGames)),
		RowCount:     IntegerAny(float64(len(window))),
		Exports: []linkView{
			{Name: "The published artifact as CSV, one row per cell", Href: exploreExportCSVPath},
			{Name: "The published artifact as JSON, byte for byte", Href: exploreExportJSONPath},
		},
	}
	if snap.Partition != nil {
		view.FloorLinks = exploreFloorLinks(path, query, filter, snap.Partition)
		view.Region = snap.Partition.Region
		view.Queue = snap.Partition.Queue
		view.Bracket = string(snap.Partition.Bracket)
		view.Window = WindowLabel(snap.Partition.SourceWindow.From, snap.Partition.SourceWindow.To)
		view.Generated = UTCStampAny(snap.Partition.GeneratedAt)
		view.Suppressed, view.HasSuppressed = snap.Partition.SuppressedCells, snap.Partition.SuppressedCells > 0
		view.ChampionCount = IntegerAny(float64(len(snap.Partition.Champions)))
	}
	if unmeasured > 0 {
		view.HasUnmeasured, view.UnmeasuredNote = true, unmeasuredNote(unmeasured)
	}
	if view.Raised {
		view.RaisedNote = "This view publishes a rate only above n = " + view.FloorText +
			" games, which is above the floor of " + view.MinCellNText + " games the snapshot was filtered at. " +
			"Cells between the two floors are published in the artifact and are withheld here, which is what a " +
			"withheld cell looks like: the count is still shown, the rate is not."
	}
	if interactive && hasSample {
		view.HasBar = true
		view.Bar = exploreBar(snap, query, filter)
	}
	if interactive && pageNav != nil {
		view.HasPageNav = true
		view.PageNav = *pageNav
	}
	view.ExportNote = exploreExportNote(snap)

	title := "Data explorer, patch " + snap.Patch + " - League of Legends - " + SiteName
	if !snap.HasSeg {
		title = "Data explorer - League of Legends - " + SiteName
	}
	page := &Page{
		Title:         title,
		Description:   exploreDescription(snap),
		CanonicalPath: CanonicalPath(path),
		Active:        path,
		Partition:     snap.Partition,
		Noindex:       snap.Partition == nil,
	}
	if snap.Patch == notPublishedLabel {
		page.PatchLabelValue = notPublishedLabel
	}
	page.JSONLD, err = exploreJSONLD(r, snap, snapshotGames)
	if err != nil {
		return nil, err
	}
	body, err := r.RenderBody("page:explore", view)
	if err != nil {
		return nil, err
	}
	page.Body = body
	return page, nil
}

// exploreCaption is the table's caption: what the table is a view of, and the
// one sentence that says what a withheld cell means. It names the selection's
// own floor rather than the snapshot's, because the caption describes the table
// on screen.
func exploreCaption(snap snapshotView, filter exploreParameter) string {
	scope := "every role"
	if filter.Role != "" {
		scope = RoleLabel(filter.Role)
	}
	if !snap.HasSeg {
		return "No published snapshot yet, so there is no table to caption."
	}
	return "Every champion and role cell of the published snapshot for patch " + snap.Patch + ", " +
		snap.bracketLabel() + ", filtered to " + scope + ". Rates are published only when the cell has at least n = " +
		IntegerAny(float64(filter.Floor)) + " games."
}

// exploreBar is the page's control surface: a plain GET form, so every selection
// it can make is a URL, and every URL it can make is a URL the server renders.
func exploreBar(snap snapshotView, query Query, filter exploreParameter) barView {
	def := DefaultExploreQuery()
	bar := barView{
		Action: explorePath,
		Clear:  explorePath,
		HasClear: query.Sort != def.Sort || query.Dir != def.Dir || query.Filter != "" ||
			query.Page > 1 || query.Per != def.Per || query.Patch != "" || len(query.Compare) > 0 ||
			filter.Role != "" || (snap.Partition != nil && filter.Floor != snap.Partition.MinCellN),
	}
	if query.Patch != "" {
		bar.Hidden = []hiddenField{{Name: "patch", Value: query.Patch}}
	}
	sortOptions := make([]optionView, 0, len(exploreColumns()))
	for _, column := range exploreColumns() {
		sortOptions = append(sortOptions, optionView{
			Value: column.Key, Label: column.Label, Selected: column.Key == query.Sort,
		})
	}
	per := make([]optionView, 0, len(explorePerOptions))
	for _, option := range explorePerOptions {
		label := "All rows"
		if option > 0 {
			label = IntegerAny(float64(option)) + " rows"
		}
		per = append(per, optionView{Value: strconv.Itoa(option), Label: label, Selected: option == query.Per})
	}
	bar.Fields = []barField{
		{Name: exploreRoleParam, Label: "Role", Kind: "select", Options: exploreRoleOptions(filter)},
	}
	if snap.Partition != nil {
		bar.Fields = append(bar.Fields, barField{
			Name: exploreFloorParam, Label: "Publish rates from", Kind: "select",
			Options: exploreFloorOptions(snap.Partition, filter),
		})
	}
	bar.Fields = append(bar.Fields,
		barField{Name: "q", Label: "Filter champions", Kind: "search", Value: query.Filter, MaxLen: MaxFilterLen},
		barField{Name: "sort", Label: "Sort by", Kind: "select", Options: sortOptions},
		barField{Name: "dir", Label: "Direction", Kind: "select", Options: []optionView{
			{Value: DirAsc, Label: "Ascending", Selected: query.Dir == DirAsc},
			{Value: DirDesc, Label: "Descending", Selected: query.Dir == DirDesc},
		}},
		barField{Name: "per", Label: "Rows per page", Kind: "select", Options: per},
	)
	return bar
}

// exploreChampionLinks is the crawlable list of the champions the table holds,
// linked at their overview rather than at a role, because the explorer's
// selection is not a role.
func exploreChampionLinks(cells []aggmodel.Cell, site *Site) []linkView {
	links := make([]linkView, 0, len(cells))
	seen := map[int]bool{}
	for _, cell := range cells {
		champion, ok := site.ChampionByID(cell.ChampionID)
		if !ok || seen[champion.ID] {
			continue
		}
		seen[champion.ID] = true
		links = append(links, linkView{Href: "/champions/" + champion.Slug, Name: champion.Name})
	}
	return links
}

// explorePatches are the patch links. Unlike the tier list's switcher, the
// explorer has no per-patch path, so the patch travels as the shared ?patch=
// parameter and every other part of the view is carried with it.
func explorePatches(snap snapshotView, query Query, filter exploreParameter) []patchEntry {
	entries := make([]patchEntry, 0, len(snap.Patches))
	for _, patch := range snap.Patches {
		next := query
		next.Patch = patch
		next.Page = 1
		entries = append(entries, patchEntry{
			Patch:   patch,
			Href:    exploreHref(explorePath, next, filter, 1),
			Current: patch == snap.Patch,
		})
	}
	return entries
}

// exploreEmpty is the state a selection with no rows shows.
func exploreEmpty(snap snapshotView, filter exploreParameter, published int) emptyView {
	if !snap.HasSeg {
		return emptyFor("No sample yet", emptyReason(snap.Site), nil)
	}
	threshold := filter.Floor
	scope := "cells of this snapshot"
	if filter.Role != "" {
		scope = RoleLabel(filter.Role) + " cells of patch " + snap.Patch
	}
	if published == 0 {
		return emptyFor(
			"No cell of this selection is published at this floor",
			"The snapshot for patch "+snap.Patch+" holds no cell in "+scope+" whose sample reaches n = "+
				IntegerAny(float64(filter.Floor))+" games, so this view shows nothing rather than an estimate.",
			&threshold,
		)
	}
	return emptyFor(
		"No row matches this selection",
		"The selection holds "+IntegerAny(float64(published))+" published cells, but none of them matches the "+
			"filter, so the table is empty rather than filled in.",
		&threshold,
	)
}

// exploreIntro is the opening sentence of the page.
func exploreIntro(snap snapshotView) string {
	if !snap.HasSeg {
		return "This page will show the published aggregate snapshot as a table to explore as soon as one " +
			"exists, with a download of the artifact beside it. Nothing is estimated in the meantime."
	}
	patch := snap.Patch
	sentence := "Every champion and role pair the snapshot for patch " + patch + " publishes, as one row per " +
		"cell, with the sample size behind each rate and the 95% interval that sample supports. The table is a view " +
		"of the published artifact: the numbers are read from it, not computed here, and the two downloads below " +
		"carry the artifact itself."
	if preview := previewNumbersSentence(snap.Site); preview != "" {
		sentence += " " + preview
	}
	return sentence
}

// exploreTail is the closing section: what the columns mean, which is the part
// a reader has to know to read the table at all.
func exploreTail(snap snapshotView, filter exploreParameter) string {
	if !snap.HasSeg {
		return "There is nothing under the columns yet: no aggregate snapshot has been published for this site."
	}
	region := snap.Partition.Region
	return "Each row is one (champion, role) cell of the published snapshot: n is the number of games the cell " +
		"was measured over, win rate is wins divided by those games, pick rate is how often the champion was " +
		"chosen for the role and ban rate is how often it was unavailable, all of them over ranked solo queue in " +
		"the " + region + " region and only over the source window above. The 95% interval is the half-width of the " +
		"interval the sample supports, so a rate over a few hundred games and the same rate over tens of thousands " +
		"of games are visibly different claims. A cell the selection may not publish keeps its count and reads " +
		"withheld: the count is the one number a thin cell is allowed to show."
}

// exploreDescription is the page's meta description.
func exploreDescription(snap snapshotView) string {
	if !snap.HasSeg {
		return "The data explorer is not published yet: no aggregate snapshot exists for this site, so the page " +
			"renders an explicit empty state and fills in as soon as one does."
	}
	return "Every champion and role cell of the published aggregate snapshot for League of Legends patch " +
		snap.Patch + ", " + snap.Partition.Region + " ranked solo queue, with its sample size, win, pick and ban " +
		"rate and a 95% interval, sortable, filterable and downloadable as CSV or JSON."
}

// exploreExportNote is the sentence beside the downloads. The downloads are the
// artifact, not the page: a reader checking a number against its source needs to
// know that the selection on screen does not narrow them.
func exploreExportNote(snap snapshotView) string {
	if !snap.HasSeg {
		return "There is nothing to download yet: no aggregate snapshot has been published for this site."
	}
	return "Both downloads are the whole artifact for patch " + snap.Patch + " (" + snap.Partition.Region +
		", queue " + IntegerAny(float64(snap.Partition.Queue)) + ", bracket " + string(snap.Partition.Bracket) +
		"), not the selection above and not a re-measurement: the CSV is that artifact projected one row per " +
		"cell, and the JSON is its own bytes, so a reader can diff the download against the published file."
}

// exploreJSONLD is the page's structured-data node, and it is emitted only for a
// live snapshot: a Dataset node claims measurements, and a preview is not one.
func exploreJSONLD(r *Renderer, snap snapshotView, sampleGames int) (string, error) {
	if snap.Partition == nil || !snap.Site.Live() {
		return "", nil
	}
	partition := snap.Partition
	node, err := datasetNode(datasetNodeInput{
		URL:          r.absolute(explorePath + "/"),
		Name:         "League of Legends champion statistics dataset, patch " + partition.Patch,
		Description:  exploreDescription(snap),
		Patch:        partition.Patch,
		SourceWindow: partition.SourceWindow,
		GeneratedAt:  partition.GeneratedAt,
		MinCellN:     partition.MinCellN,
		SampleSize:   sampleGames,
		Variables: []datasetVariable{
			{Name: "games played (n)", Unit: "games"},
			{Name: "wins", Unit: "games"},
			{Name: "win rate", Unit: "win rate"},
			{Name: "pick rate", Unit: "pick rate"},
			{Name: "ban rate", Unit: "ban rate"},
			{Name: "95% confidence half-width", Unit: "percentage points"},
		},
		State: string(snap.Site.State()),
	})
	if err != nil {
		return "", err
	}
	return JSONLDNode(node)
}

// exploreExportError is a download this tier will not serve, carrying the fault
// kind the handler should report. It exists so the reason a download failed is
// the reason stated to the reader, rather than a generic error page.
type exploreExportError struct {
	Kind   string
	Status int
	Detail string
}

func (e *exploreExportError) Error() string { return e.Detail }

// ExploreExportJSON returns the published tier-list artifact byte for byte.
//
// It is deliberately a copy and not a re-serialisation: a reader checking a
// number on the page against its source can diff the download against the file
// the aggregate build published, and a byte copy is the only shape for which
// that diff is empty.
func (r *Renderer) ExploreExportJSON(values url.Values) ([]byte, error) {
	snap, err := r.snapshot(ParseQuery(values, DefaultExploreQuery(), nil).Patch)
	if err != nil {
		return nil, err
	}
	if !snap.HasSeg {
		return nil, &exploreExportError{
			Kind: FaultNoSnapshot, Status: http.StatusServiceUnavailable,
			Detail: "no aggregate snapshot has been published for this site, so there is no artifact to export",
		}
	}
	root, err := r.Loader().RootDir()
	if err != nil {
		return nil, err
	}
	if root == "" {
		return nil, &exploreExportError{
			Kind: FaultNoSnapshot, Status: http.StatusServiceUnavailable,
			Detail: "no aggregate snapshot has been published for this site, so there is no artifact to export",
		}
	}
	relative := snap.Seg.TierListPath()
	full := filepath.Join(root, filepath.FromSlash(relative))
	if !withinDir(root, full) {
		return nil, &exploreExportError{
			Kind: FaultNotFound, Status: http.StatusNotFound,
			Detail: "no artifact of the published tree matches " + relative,
		}
	}
	info, statErr := os.Stat(full)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		return nil, &exploreExportError{
			Kind: FaultNoSnapshot, Status: http.StatusServiceUnavailable,
			Detail: "the published tree has no " + relative,
		}
	case statErr != nil:
		return nil, artifactFault(full, "cannot be read: %v", statErr)
	case info.IsDir():
		return nil, &exploreExportError{
			Kind: FaultNotFound, Status: http.StatusNotFound,
			Detail: relative + " is a directory of the published tree, not an artifact",
		}
	case info.Size() > maxArtifactBytes:
		return nil, &exploreExportError{
			Kind: FaultRender, Status: http.StatusInternalServerError,
			Detail: relative + " is larger than this tier will read in one response",
		}
	}
	return os.ReadFile(full) // #nosec G304 -- full is the snapshot's own artifact path, chosen by the route table, not by the request
}

// exploreCSVHeader is the header of the CSV download. Every field of the
// published cell is present, plus the partition it belongs to and the
// champion's name and slug, so the file stands alone: the aggregate artifact
// carries champion ids, and a data scientist should not have to join two files
// to read one row.
func exploreCSVHeader() []string {
	return []string{
		"patch", "region", "queue", "bracket", "source", "generated_at",
		"source_window_from", "source_window_to", "min_cell_n", "suppressed_cells",
		"champion_id", "champion_name", "champion_slug", "role",
		"n", "wins", "win_rate", "pick_rate", "ban_rate", "ci95_half_width", "tier",
	}
}

// ExploreExportCSV projects the published tier-list artifact to one row per
// cell. It is a projection and not a copy, and it says so on the page: the
// fields are the artifact's own, unrounded and uncomputed, and the file carries
// the artifact's provenance in every row.
func (r *Renderer) ExploreExportCSV(values url.Values) ([]byte, error) {
	snap, err := r.snapshot(ParseQuery(values, DefaultExploreQuery(), nil).Patch)
	if err != nil {
		return nil, err
	}
	if !snap.HasSeg {
		return nil, &exploreExportError{
			Kind: FaultNoSnapshot, Status: http.StatusServiceUnavailable,
			Detail: "no aggregate snapshot has been published for this site, so there is no artifact to export",
		}
	}
	tierList, err := snap.Site.TierList(snap.Seg)
	if err != nil {
		return nil, err
	}

	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write(exploreCSVHeader()); err != nil {
		return nil, err
	}
	partition := []string{
		tierList.Patch, tierList.Region, strconv.Itoa(tierList.Queue), string(tierList.Bracket),
		string(tierList.Source), tierList.GeneratedAt.UTC().Format(time.RFC3339),
		tierList.SourceWindow.From, tierList.SourceWindow.To,
		strconv.Itoa(tierList.MinCellN), strconv.Itoa(tierList.SuppressedCells),
	}
	for _, cell := range tierList.Cells {
		name, slug := "", ""
		if champion, ok := snap.Site.ChampionByID(cell.ChampionID); ok {
			name, slug = champion.Name, champion.Slug
		}
		row := append(make([]string, 0, len(partition)+11), partition...)
		row = append(row,
			strconv.Itoa(cell.ChampionID), name, slug, string(cell.Role),
			strconv.Itoa(cell.N), strconv.Itoa(cell.Wins),
			exploreRate(cell.WinRate), exploreRate(cell.PickRate), exploreRate(cell.BanRate),
			exploreRate(cell.CI95HalfWidth), string(cell.Tier),
		)
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// exploreRate fixes a rate's significant digits so a reader can compare the
// export with the snapshot with an ordinary diff. It is the artifact's number,
// shortened, not a rate computed here.
func exploreRate(value float64) string {
	return strconv.FormatFloat(value, 'g', 6, 64)
}
