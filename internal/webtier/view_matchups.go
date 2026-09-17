package webtier

import (
	"html/template"
	"sort"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The matchup route: /matchups/<role>.
//
// The matrix is server-rendered in full: the artifact stores each unordered
// pair once, so both orientations are materialised here, and the mirror of a
// stored pair is the complement of its win rate. That is arithmetic, not an
// estimate, and it is the same rule lib/artifacts.ts applies.

// matchupCellView is one cell of the heatmap, pre-rendered. The cells are
// assembled in Go rather than in the template because each of the three shapes
// has its own attribute set and the byte-for-byte comparison with the reference
// build leaves no room for a template conditional to add or drop one.
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

	// The published pairs, in artifact order, are the page's sample: the median
	// pair is the honest summary of a matrix, because a total across it would
	// count one match once per pair it contains.
	var pairCounts []int
	for _, cell := range cellsOf(matchups) {
		if CellAvailabilityOf(cell.N, minCellN) == AvailabilityPublished {
			pairCounts = append(pairCounts, cell.N)
		}
	}
	hasSample := matchups != nil && len(pairCounts) > 0

	island := buildHeatmap(matchups, site, role, minCellN)

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
		Links:        heatmapLinks(island, slug, strings.ToLower(label)),
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
		view.Median = "n above is the median number of games behind a champion pair on this page, not a total: " +
			"a match contains many pairs, so a matrix-wide total would count the same game once per pair it " +
			"contains and would mean less than the pair sample does. Pair samples here run from n = " +
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
// web/src/pages/matchups/[role].astro.
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

// buildHeatmap materialises both orientations of every stored pair, then renders
// the matrix as HTML. A pair the artifact does not carry, and a pair whose
// sample falls below the threshold, both render as a dash: a withheld pair is a
// refusal to publish a rate, never a zero.
func buildHeatmap(matchups *aggmodel.Matchups, site *Site, role aggmodel.Role, minCellN int) heatmapView {
	view := heatmapView{MinCellN: minCellN}
	if matchups == nil {
		return view
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

	type championView struct {
		id   int
		name string
		slug string
	}
	champions := make([]championView, 0, len(matchups.Champions))
	for _, id := range matchups.Champions {
		entry := championView{id: id, name: "Champion " + IntegerAny(float64(id)), slug: IntegerAny(float64(id))}
		if champion, ok := site.ChampionByID(id); ok {
			entry.name, entry.slug = champion.Name, champion.Slug
		}
		champions = append(champions, entry)
	}

	published := 0
	view.Rows = make([]matchupRowView, 0, len(champions))
	view.Columns = make([]matchupColumnView, 0, len(champions))
	for _, champion := range champions {
		view.Columns = append(view.Columns, matchupColumnView{ID: champion.id, Name: champion.name})
	}
	for _, row := range champions {
		rendered := matchupRowView{
			ID:    row.id,
			Name:  row.name,
			Slug:  row.slug,
			Href:  "/champions/" + row.slug + "/" + slug,
			Cells: make([]matchupCellView, 0, len(champions)),
		}
		for index, column := range champions {
			tabstop := ""
			if index == 0 {
				tabstop = ` tabindex="0"`
			}
			switch column.id {
			case row.id:
				rendered.Cells = append(rendered.Cells, matchupCellView{Markup: trustedHTML(
					`<td class="cell self" data-n="0"` + tabstop + ` aria-label="not applicable: the same champion ` +
						`in both axes, a champion is never matched against itself" data-astro-cid-5wqubv3u>&middot;</td>`)})
			default:
				result, ok := pairs[pairKey{row.id, column.id}]
				if !ok || CellAvailabilityOf(result.n, minCellN) != AvailabilityPublished {
					rendered.Cells = append(rendered.Cells, matchupCellView{Markup: trustedHTML(
						`<td class="cell missing" data-n="0"` + tabstop + ` aria-label="not published" ` +
							`data-astro-cid-5wqubv3u>&mdash;</td>`)})
					continue
				}
				published++
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
		"beside it is the distance from even in percentage points. A dash means the pair has fewer than n = " +
		IntegerAny(float64(minCellN)) + " games in this window and is not published."
	view.Note = trustedHTML(IntegerAny(float64(published)) + " of " + IntegerAny(float64(totalCells)) +
		" pairings are published for this role. Cells withheld for being below the threshold are absent from the " +
		"artifact and read as a dash here, never as zero" + suppressedClause(matchups) + ".")
	return view
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
// in the matrix, in matrix order.
func heatmapLinks(island heatmapView, slug, labelLower string) []linkView {
	links := make([]linkView, 0, len(island.Rows))
	for _, row := range island.Rows {
		links = append(links, linkView{
			Href: "/champions/" + row.Slug + "/" + slug,
			Name: row.Name + " " + labelLower + " matchups",
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
