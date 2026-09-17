package webtier

import (
	"html"
	"html/template"
	"strconv"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// Shared pieces of the statistics views: the small derivations every route
// needs, kept in one place so the routes cannot disagree about them.

// EscapeString escapes a string for HTML text, so a sentence that was assembled
// in Go can be handed to a template as trusted markup without losing the
// escaping the reference build applied to it.
func EscapeString(value string) string { return html.EscapeString(value) }

// Role resolves a route's role slug. An unknown slug is a route this tier does
// not serve.
func (r *Renderer) Role(slug string) (aggmodel.Role, error) {
	role, ok := RoleFromSlug(slug)
	if !ok {
		return "", ErrNotFound
	}
	return role, nil
}

// bracketLabel is the partition's bracket, or "all ranks" when there is no
// partition to read one from.
func (s snapshotView) bracketLabel() string {
	if s.Partition == nil {
		return BracketLabel(aggmodel.BracketAll)
	}
	return BracketLabel(s.Partition.Bracket)
}

func (s snapshotView) bracketValue() string {
	if s.Partition == nil {
		return string(aggmodel.BracketAll)
	}
	return string(s.Partition.Bracket)
}

// tableIslandUID is the island's element id stem, exactly as the component
// builds it: ti-{role}-{patch}-{bracket}, with `all` for the role when the
// table is not scoped to one. The island's runtime uses it to name its own
// controls, and the no-JavaScript form reuses it so the two never collide.
func tableIslandUID(roleSlug string, patch string, bracket string) string {
	role := roleSlug
	if role == "" {
		role = "all"
	}
	return "ti-" + role + "-" + patch + "-" + bracket
}

// ariaSort is the `aria-sort` value for a column's header.
func ariaSort(dir string) string {
	if dir == DirAsc {
		return "ascending"
	}
	return "descending"
}

// statusText is the island's status line, which is also the sentence the
// no-JavaScript table needs: it is the only place the row count is stated.
func statusText(shown int, total int, label string, dir string) string {
	return "Showing " + IntegerAny(float64(shown)) + " of " + IntegerAny(float64(total)) +
		" rows, sorted by " + label + ", " + dirWord(dir) + "."
}

// scopeLabel is the phrase the island's caption uses for the table's scope.
func scopeLabel(role aggmodel.Role, empty string) string {
	if role == "" {
		if empty != "" {
			return empty
		}
		return "every role"
	}
	return RoleLabel(role)
}

// roleFor returns the role a notice is scoped to, or nil for the every-role
// table, whose sample is not one role's.
func roleFor(role aggmodel.Role) *aggmodel.Role {
	if role == "" {
		return nil
	}
	value := role
	return &value
}

// paginate applies ?per= and ?page= to the ordered rows. With no `per` the whole
// table is one page, which is what the reference build published and what the
// parity harness compares against.
func paginate(rows []*tableRow, query Query) ([]*tableRow, *pagerView) {
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
	def := DefaultTierListQuery()
	nav := pagerView{
		Label: "Page " + IntegerAny(float64(page)) + " of " + IntegerAny(float64(pages)) +
			", rows " + IntegerAny(float64(start+1)) + "-" + IntegerAny(float64(end)) + " of " + IntegerAny(float64(len(rows))),
	}
	if page > 1 {
		nav.HasPrev = true
		nav.Prev = query.With("page", strconv.Itoa(page-1)).Href(def, "")
	}
	if page < pages {
		nav.HasNext = true
		nav.Next = query.With("page", strconv.Itoa(page+1)).Href(def, "")
	}
	return rows[start:end], &nav
}

// championLinks is rows.ts's championLinks: one link per champion that has a
// published cell, in table order, deduplicated so a champion played in two
// roles is linked once.
func championLinks(cells []aggmodel.Cell, site *Site, slug string) []linkView {
	links := make([]linkView, 0, len(cells))
	seen := map[int]bool{}
	for _, cell := range cells {
		champion, ok := site.ChampionByID(cell.ChampionID)
		if !ok || seen[champion.ID] {
			continue
		}
		seen[champion.ID] = true
		links = append(links, linkView{
			Href: "/champions/" + champion.Slug + "/" + slug,
			Name: champion.Name,
		})
	}
	return links
}

// unmeasuredNote counts the cells that hold no games at all. They are not
// withheld, they are empty, and the difference is stated rather than glossed.
func unmeasuredNote(n int) string {
	if n == 1 {
		return "One cell in this snapshot holds no games yet (n = 0), so it is not ranked here: a grade beside an " +
			"empty cell would be a claim the snapshot cannot support. Its champion page reports no sample rather than a rate."
	}
	return IntegerAny(float64(n)) + " cells in this snapshot hold no games yet (n = 0), so they are not ranked here: " +
		"a grade beside an empty cell would be a claim the snapshot cannot support. Their champion pages report no " +
		"sample rather than a rate."
}

// tierListIntro is the opening sentence of a tier-list page.
func tierListIntro(snap snapshotView, label string) string {
	if snap.Partition == nil {
		return "This page will show every champion played in " + strings.ToLower(label) +
			" as soon as a snapshot is published."
	}
	sentence := "Every champion that was played in " + strings.ToLower(label) +
		" during the source window below, with the number of games each rate was measured over."
	if preview := previewNumbersSentence(snap.Site); preview != "" {
		sentence += " " + preview
	}
	return sentence
}

// tierListTail is the "What this table is" paragraph.
func tierListTail(label string, snap snapshotView, role aggmodel.Role) string {
	region := "published"
	if snap.Partition != nil {
		region = snap.Partition.Region
	}
	sentence := "Each row is one champion in " + strings.ToLower(label) +
		", measured over the source window above and only over matches in ranked solo queue in the " + region +
		" region. Win rate is wins divided by games played in that role; pick rate is how often the champion was " +
		"chosen for the role; ban rate is how often it was unavailable. The 95% interval is printed next to each rate " +
		"because a rate over a few hundred games and the same rate over tens of thousands are different claims."
	if preview := previewNumbersSentence(snap.Site); preview != "" {
		sentence += " " + preview
	}
	return sentence
}

// tierListEmpty is the empty state a tier-list page shows when it has no rows.
func tierListEmpty(snap snapshotView, label string, minCellN int) emptyView {
	if snap.Partition == nil {
		return emptyFor("No sample yet", emptyReason(snap.Site), nil)
	}
	threshold := minCellN
	return emptyFor(
		"No "+strings.ToLower(label)+" cells published",
		"The snapshot for patch "+snap.Patch+" has no tier list artifact for this selection, so this page shows "+
			"nothing rather than an estimate.",
		&threshold,
	)
}

// tierListDescription is the page's meta description, transcribed from
// the retired web/ tree's tier-list/[role].astro and its archived twin. The newest route
// describes the sample-size policy; the archived route names the source window
// it measured instead.
func tierListDescription(label string, patch string, archived bool, snap snapshotView) string {
	if archived {
		if snap.Partition == nil {
			return "The archived " + strings.ToLower(label) + " tier list for patch " + patch +
				". This patch has no published snapshot."
		}
		window := snap.Partition.SourceWindow
		return label + " champion win, pick and ban rates for League of Legends patch " + patch + ", " +
			snap.Partition.Region + " ranked solo queue, measured over " + dateOnly(window.From) + " to " +
			dateOnly(window.To) + ", each rate with its sample size."
	}
	if snap.Partition == nil {
		return "The " + strings.ToLower(label) + " tier list is not published yet: no aggregate snapshot exists " +
			"for this site, so this page renders an explicit empty state and fills in as soon as one does."
	}
	return label + " champion win, pick and ban rates for League of Legends patch " + snap.Patch + ", " +
		snap.Partition.Region + " ranked solo queue, each with its sample size and a 95% interval. " +
		"Rates are published only for cells holding at least " + IntegerAny(float64(snap.Partition.MinCellN)) + " games."
}

// dateOnly is the yyyy-mm-dd prefix of a timestamp, which is what the archived
// tier-list description quotes for its source window.
func dateOnly(stamp string) string {
	if len(stamp) >= 10 {
		return stamp[:10]
	}
	return stamp
}

// archiveSentenceText builds the plain-text archive sentence the patch route
// appends after its article. It is a string rather than markup because the
// reference build's sentence carries one link and no other markup.
func archiveSentence(site *Site, slug string, label string) (template.HTML, bool) {
	patches := site.Patches()
	if len(patches) == 0 {
		return "", false
	}
	word := "es"
	if len(patches) == 1 {
		word = ""
	}
	return trustedHTML(`<p><a href="/tier-list/` + slug + `">Newest patch ` + strings.ToLower(label) +
		` tier list</a> (` + IntegerAny(float64(len(patches))) + ` patch` + word + ` published: ` +
		strings.Join(patches, ", ") + `)</p>`), true
}

// tierListBar is the no-JavaScript control surface for a tier list: the filter,
// the sort key, the direction and the row window, all of them query parameters
// on a plain GET form.
func tierListBar(path string, query Query, def Query, columns []sortColumn, uid string) barView {
	bar := barView{Action: path}
	bar.HasClear = !query.IsDefault(def) || query.Filter != "" || query.Sort != def.Sort || query.Dir != def.Dir || query.Per != def.Per
	bar.Clear = path
	bar.Fields = []barField{
		{Name: "q", Label: "Filter champions", Kind: "search", Value: query.Filter, MaxLen: MaxFilterLen},
		{Name: "sort", Label: "Sort by", Kind: "select"},
		{Name: "dir", Label: "Direction", Kind: "select"},
		{Name: "per", Label: "Rows per page", Kind: "select"},
	}
	for _, column := range columns {
		bar.Fields[1].Options = append(bar.Fields[1].Options, optionView{
			Value: column.Key, Label: column.Label, Selected: column.Key == query.Sort,
		})
	}
	bar.Fields[2].Options = []optionView{
		{Value: DirAsc, Label: "Ascending", Selected: query.Dir == DirAsc},
		{Value: DirDesc, Label: "Descending", Selected: query.Dir == DirDesc},
	}
	for _, per := range perOptions {
		label := "All rows"
		if per > 0 {
			label = IntegerAny(float64(per)) + " rows"
		}
		bar.Fields[3].Options = append(bar.Fields[3].Options, optionView{
			Value: strconv.Itoa(per), Label: label, Selected: per == query.Per,
		})
	}
	_ = uid
	return bar
}

// comparePanel renders ?compare=: the named champions' rows from the same table,
// in the order they were named. A champion that is not in the table is reported
// as absent rather than rendered as a blank column.
func comparePanel(query Query, rows []*tableRow, label string) (compareView, bool) {
	if len(query.Compare) == 0 {
		return compareView{}, false
	}
	bySlug := map[string]*tableRow{}
	for _, row := range rows {
		if _, ok := bySlug[row.Slug]; !ok {
			bySlug[row.Slug] = row
		}
	}
	view := compareView{
		Columns: columnsToIsland([]sortColumn{
			{Key: "n", Label: "Games (n)", Numeric: true},
			{Key: "win_rate", Label: "Win rate", Numeric: true},
			{Key: "pick_rate", Label: "Pick rate", Numeric: true},
			{Key: "ban_rate", Label: "Ban rate", Numeric: true},
			{Key: "ci95", Label: "95% interval", Numeric: true},
		}),
	}
	names := make([]string, 0, len(query.Compare))
	missing := make([]string, 0, len(query.Compare))
	for _, raw := range query.Compare {
		row, ok := bySlug[raw]
		if !ok {
			names = append(names, raw)
			missing = append(missing, raw)
			continue
		}
		view.Rows = append(view.Rows, row)
		names = append(names, row.Name)
	}
	if len(missing) > 0 {
		view.Missing = strings.Join(missing, ", ")
		view.MissingOne = len(missing) == 1
	}
	view.Heading = strings.Join(names, ", ")
	var builder strings.Builder
	builder.WriteString("Comparing ")
	builder.WriteString(view.Heading)
	builder.WriteString(" from the ")
	builder.WriteString(strings.ToLower(label))
	builder.WriteString(" table on this page. These are the same cells the table above is built from, picked out")
	builder.WriteString(" by the URL rather than measured a second time; a champion whose cell is too thin to publish")
	builder.WriteString(" a rate shows the same withheld word here as it does above.")
	if len(missing) > 0 {
		verb := "have"
		if len(missing) == 1 {
			verb = "has"
		}
		builder.WriteString(" ")
		builder.WriteString(view.Missing)
		builder.WriteString(" ")
		builder.WriteString(verb)
		builder.WriteString(" no published cell here.")
	}
	view.Note = builder.String()
	return view, len(view.Rows) > 0 || len(missing) > 0
}
