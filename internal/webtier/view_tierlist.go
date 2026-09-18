package webtier

import (
	"html/template"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The tier-list routes: /tier-list/<role> and /patch/<version>/tier-list/<role>.
// The two share one view so they cannot drift, exactly as the reference build
// shared one body component between them.

// perOptions are the row windows a reader may choose. "All" is the default and
// reproduces the reference build's single unpaged table byte for byte.
var perOptions = []int{0, 25, 50, 100}

type optionView struct {
	Value    string
	Label    string
	Selected bool
}

type barField struct {
	Name    string
	Label   string
	Kind    string
	Value   string
	MaxLen  int
	Options []optionView
}

type hiddenField struct {
	Name  string
	Value string
}

// barView is the no-JavaScript control surface. It is an addition to the
// reference build, not a port of it: the reference hid the island's controls
// until its runtime ran, which left a JavaScript-free reader with a table and
// no way to change how it was shown. This is a plain GET form, so every
// selection it can make is a URL, and every URL it can make is rendered by the
// server. It is emitted only when the tier is serving pages; the parity harness
// renders with it switched off, which is how the byte comparison against the
// static build stays meaningful.
type barView struct {
	Action   string
	Clear    string
	HasClear bool
	Fields   []barField
	Hidden   []hiddenField
}

// pagerView is the no-JavaScript pagination control, built from links only.
type pagerView struct {
	Prev    string
	Next    string
	HasPrev bool
	HasNext bool
	Label   string
}

// compareView is the comparison panel: the rows of the champions named in
// ?compare=, taken from the same table, in the order they were named.
type compareView struct {
	Heading    string
	Columns    []islandColumn
	Rows       []*tableRow
	Missing    string
	MissingOne bool
	Note       string
}

type tierListView struct {
	base

	Label        string
	LabelLower   string
	Slug         string
	Patch        string
	HasPartition bool
	Region       string
	Queue        int
	Bracket      string
	Window       string
	Generated    string
	Intro        string

	Patches []patchEntry

	HasSample      bool
	Notice         sampleNoticeView
	Island         islandView
	Note           template.HTML
	HasUnmeasured  bool
	UnmeasuredNote string
	SPlusBadge     fallbackBadgeView
	Links          []linkView
	Tail           string
	Empty          emptyView

	HasBar     bool
	Bar        barView
	HasPageNav bool
	PageNav    pagerView
	HasCompare bool
	Compare    compareView

	Archive    template.HTML
	HasArchive bool
}

// TierListPage renders /tier-list/<role>.
func (r *Renderer) TierListPage(role string, query Query, interactive bool) (*Page, error) {
	value, err := r.Role(role)
	if err != nil {
		return nil, err
	}
	return r.tierListPage(value, "", query, interactive, false)
}

// PatchTierListPage renders /patch/<version>/tier-list/<role>: the same table
// for a patch that is no longer the newest one, plus the link back to the
// newest patch's page.
func (r *Renderer) PatchTierListPage(role string, patch string, query Query, interactive bool) (*Page, error) {
	value, err := r.Role(role)
	if err != nil {
		return nil, err
	}
	return r.tierListPage(value, patch, query, interactive, true)
}

func (r *Renderer) tierListPage(role aggmodel.Role, patch string, query Query, interactive bool, archived bool) (*Page, error) {
	snap, err := r.snapshot(patch)
	if err != nil {
		return nil, err
	}
	site := snap.Site
	slug := RoleSlugString(role)
	label := RoleLabel(role)
	def := DefaultTierListQuery()

	var tierList *aggmodel.TierList
	if snap.HasSeg {
		// A partition the manifest publishes is a promise that its artifacts
		// are there. A missing or unreadable tier list under that promise is an
		// incomplete publication, which is a 503 and not a page with a table
		// that quietly lost its rows.
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
		for _, cell := range tierList.Cells {
			if cell.Role == role {
				allCells = append(allCells, cell)
			}
		}
	}
	var measured []aggmodel.Cell
	for _, cell := range allCells {
		if cell.N > 0 {
			measured = append(measured, cell)
		}
	}
	unmeasured := len(allCells) - len(measured)

	published := 0
	cellGames := 0
	for _, cell := range measured {
		if CellAvailabilityOf(cell.N, minCellN) == AvailabilityPublished {
			published++
			cellGames += cell.N
		}
	}
	hasSample := snap.HasSeg && len(measured) > 0

	rows := TierListRows(measured, site, minCellN, false)
	columns := TierListColumns(false)

	// The filter, the sort and the page are applied to the rows the server
	// already has. Nothing here depends on the client: the same query string
	// produces the same table, in the same order, on any machine.
	filtered := FilterRows(rows, query.Filter)
	ordered := OrderRows(filtered, query.Sort, query.Dir)
	window, pageNav := paginate(ordered, query)

	island := islandView{
		UID:        tableIslandUID(slug, snap.Patch, snap.bracketValue()),
		Cols:       columnsToIsland(columns),
		SortKey:    query.Sort,
		AriaSort:   ariaSort(query.Dir),
		Dir:        query.Dir,
		DirLabel:   dirWord(query.Dir),
		DirText:    dirArrow(query.Dir),
		StatusText: statusText(len(window), len(ordered), ColumnLabel(columns, query.Sort), query.Dir),
		Rows:       window,
		ColCount:   len(columns),
		Note:       trustedHTML(islandNote(minCellN, suppressed)),
	}
	island.Caption = "Champion tier list for " + scopeLabel(role, "") + ", patch " + snap.Patch + ", " +
		snap.bracketLabel() + ". Rates are published only when the cell has at least n = " +
		IntegerAny(float64(minCellN)) + " games."

	view := tierListView{
		base:         base{Site: site, SiteURL: r.siteURL, R: r},
		Label:        label,
		LabelLower:   strings.ToLower(label),
		Slug:         slug,
		Patch:        snap.Patch,
		HasPartition: snap.Partition != nil,
		Intro:        tierListIntro(snap, label),
		Patches:      r.patchSwitcherFor(snap, func(patch string) string { return "/patch/" + patch + "/tier-list/" + slug }, query, def),
		HasSample:    hasSample,
		Notice:       sampleNotice(cellGames, minCellN, suppressed, roleFor(role)),
		Island:       island,
		Note:         trustedHTML(columnAvailabilityNote(minCellN, suppressed)),
		SPlusBadge:   fallbackBadge(aggmodel.TierSPlus, 0),
		Tail:         tierListTail(label, snap, role),
	}
	if snap.Partition != nil {
		view.Region = snap.Partition.Region
		view.Queue = snap.Partition.Queue
		view.Bracket = string(snap.Partition.Bracket)
		view.Window = WindowLabel(snap.Partition.SourceWindow.From, snap.Partition.SourceWindow.To)
		view.Generated = UTCStampAny(snap.Partition.GeneratedAt)
	}
	view.Links = championLinks(measured, site, slug)
	if unmeasured > 0 {
		view.HasUnmeasured = true
		view.UnmeasuredNote = unmeasuredNote(unmeasured)
	}
	if !hasSample {
		view.Empty = tierListEmpty(snap, label, minCellN)
	}

	path := "/tier-list/" + slug
	if archived {
		path = "/patch/" + patch + "/tier-list/" + slug
		archive, ok := archiveSentence(site, slug, label)
		view.Archive, view.HasArchive = archive, ok
	}
	if interactive && hasSample {
		view.HasBar = true
		view.Bar = tierListBar(path, query, def, columns, island.UID)
	}
	if interactive && pageNav != nil {
		view.HasPageNav = true
		view.PageNav = *pageNav
	}
	if interactive && hasSample && len(query.Compare) > 0 {
		view.Compare, view.HasCompare = comparePanel(query, rows, label)
	}

	titlePatch := snap.Patch
	if archived {
		titlePatch = patch
	}
	title := label + " tier list, patch " + titlePatch + " - League of Legends - " + SiteName
	if !archived && snap.Partition == nil {
		title = label + " tier list - League of Legends - " + SiteName
	}
	page := &Page{
		Title:         title,
		Description:   tierListDescription(label, titlePatch, archived, snap),
		CanonicalPath: CanonicalPath(path),
		Active:        path,
		Partition:     snap.Partition,
		Noindex:       snap.Partition == nil,
	}
	if snap.Patch == notPublishedLabel {
		page.PatchLabelValue = notPublishedLabel
	}
	page.JSONLD, err = tierListJSONLD(r, snap, label, path, cellGames, titlePatch, archived)
	if err != nil {
		return nil, err
	}
	body, err := r.RenderBody("page:tierlist", view)
	if err != nil {
		return nil, err
	}
	page.Body = body
	return page, nil
}

// tierListJSONLD is the page's structured-data node. It is emitted only for a
// live snapshot: the reference build attaches a Dataset node to a page whose
// numbers are measurements, and a preview or an empty page is not one.
func tierListJSONLD(r *Renderer, snap snapshotView, label string, path string, cellGames int, patch string, archived bool) (string, error) {
	if snap.Partition == nil || !snap.Site.Live() {
		return "", nil
	}
	partition := snap.Partition
	node, err := datasetNode(datasetNodeInput{
		URL:          r.absolute(path + "/"),
		Name:         label + " tier list, League of Legends patch " + partition.Patch,
		Description:  tierListDescription(label, patch, archived, snap),
		Patch:        partition.Patch,
		SourceWindow: partition.SourceWindow,
		GeneratedAt:  partition.GeneratedAt,
		MinCellN:     partition.MinCellN,
		SampleSize:   cellGames,
		Variables: []datasetVariable{
			{Name: "games played (n)", Unit: "games"},
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
