package webtier

import (
	"errors"
	"html/template"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The statistics routes: /tier-list/<role>, /patch/<v>/tier-list/<role>,
// /matchups/<role>, /champions/<slug> and /champions/<slug>/<role>.
//
// These are the pages the redesign is for. Every one of them is rendered from
// the published artifacts on the server, so every view of the data - the sort
// order, the filter, the page, the patch, the champions being compared - is a
// URL that can be shared, bookmarked and opened with JavaScript turned off. The
// islands still enhance the same markup, but nothing here depends on them.

// ErrNotFound is a route this tier does not serve: an unknown role or an
// unknown champion. The reference build answered those with no file at all,
// which is the same thing.
var ErrNotFound = errors.New("no such page")

// notPublishedLabel is page.ts's name for a snapshot that has no partition. The
// nav and the footer print it rather than falling back to the newest patch,
// because the page it labels is not about the newest patch.
const notPublishedLabel = "not published"

// snapshotView is lib/page.ts's Snapshot: the site, the published patches, and
// which partition this page is about.
type snapshotView struct {
	Site    *Site
	Patches []string
	// Patch is the patch this page is about, or "not published".
	Patch     string
	Partition *aggmodel.Partition
	Seg       aggmodel.Seg
	HasSeg    bool
}

// snapshot is page.ts's snapshot(patch). A patch the manifest does not list is
// not an error here: it is a snapshot with no partition, which every page
// renders as its empty state rather than as a broken one. An empty patch means
// the newest published snapshot.
func (r *Renderer) snapshot(patch string) (snapshotView, error) {
	site, err := r.Site()
	if err != nil {
		return snapshotView{}, err
	}
	view := snapshotView{Site: site, Patches: site.Patches(), Patch: notPublishedLabel}
	if patch == "" {
		if partition := site.Latest(); partition != nil {
			view.Partition, view.Patch = partition, partition.Patch
			view.Seg, view.HasSeg = SegOf(*partition), true
		}
		return view, nil
	}
	seg, ok := site.SegForPatch(patch)
	if !ok {
		return view, nil
	}
	view.Seg, view.HasSeg = seg, true
	if partition, ok := site.PartitionFor(seg); ok {
		view.Partition, view.Patch = &partition, partition.Patch
	}
	return view, nil
}

func (s snapshotView) minCellN() int {
	if s.Partition != nil {
		return s.Partition.MinCellN
	}
	return 0
}

func (s snapshotView) suppressedCells() *int {
	if s.Partition == nil {
		return nil
	}
	value := s.Partition.SuppressedCells
	return &value
}

// patchEntry is one link in the patch switcher.
type patchEntry struct {
	Patch   string
	Href    string
	Current bool
}

// linkView is one entry of a champion-links list.
type linkView struct {
	Href string
	Name string
}

// patchSwitcherFor builds the patch links. Every link keeps the reader on the
// same page for the other patch, and the current patch is marked rather than
// linked to itself, as the reference build does it.
func (r *Renderer) patchSwitcherFor(snap snapshotView, pathFor func(patch string) string, query Query, def Query) []patchEntry {
	entries := make([]patchEntry, 0, len(snap.Patches))
	for _, patch := range snap.Patches {
		next := query
		next.Patch = ""
		href := next.Href(def, pathFor(patch))
		entries = append(entries, patchEntry{Patch: patch, Href: href, Current: patch == snap.Patch})
	}
	return entries
}

// tierGrade is the class suffix the fallback tier badge uses: the tier letter
// lowercased, with a plus sign spelled out as "-plus" because the suffix lands
// in a CSS class. It is fallback/TierBadge.astro's
// `tier.toLowerCase().replace('+', '-plus')`.
func tierGrade(tier aggmodel.Tier) string {
	return strings.ReplaceAll(strings.ToLower(string(tier)), "+", "-plus")
}

// designGrade is the class suffix the design-system tier badge uses. It spells
// the same grade differently - `grade-splus` rather than `--s-plus` - because
// the two components are separate stylesheets, so the port carries both.
func designGrade(tier aggmodel.Tier) string {
	if string(tier) == "S+" {
		return "splus"
	}
	return strings.ToLower(string(tier))
}

// tierBadgeView is the TierBadge component's data.
type tierBadgeView struct {
	Grade string
	Tier  string
}

// Badge is the badge for a row's tier.
func (r *tableRow) Badge() tierBadgeView {
	return tierBadgeView{Grade: designGrade(r.Tier), Tier: string(r.Tier)}
}

// fallbackBadgeView is the fallback badge's data, which carries a tooltip the
// island badge does not.
type fallbackBadgeView struct {
	Grade string
	Tier  string
	Title string
}

// fallbackBadge builds the inline badge the tier-order paragraph carries.
func fallbackBadge(tier aggmodel.Tier, n int) fallbackBadgeView {
	title := "Tier " + string(tier)
	if n > 0 {
		title = "Tier " + string(tier) + ", n = " + IntegerAny(float64(n)) + " games"
	}
	return fallbackBadgeView{Grade: tierGrade(tier), Tier: string(tier), Title: title}
}

// emptyView is the EmptyState component's data.
type emptyView struct {
	Title       string
	Body        string
	HasMinCellN bool
	MinCellN    int
}

// emptyFor builds an empty state, carrying the threshold only when the snapshot
// declares one: an unknown threshold is not the same statement as n = 0.
func emptyFor(title string, body string, minCellN *int) emptyView {
	view := emptyView{Title: title, Body: body}
	if minCellN != nil {
		view.HasMinCellN, view.MinCellN = true, *minCellN
	}
	return view
}

// dirWord is the word the status line uses for a sort direction.
func dirWord(dir string) string {
	if dir == DirAsc {
		return "ascending"
	}
	return "descending"
}

// dirArrow is the glyph on the island's direction toggle.
func dirArrow(dir string) string {
	if dir == DirAsc {
		return "Ascending"
	}
	return "Descending"
}

// sampleNoticeView is the SampleSizeNotice component's data.
type sampleNoticeView struct {
	N                int
	NText            string
	Scope            string
	MinCellNText     string
	Suppressed       int
	SuppressedText   string
	SuppressedPlural string
	SuppressedIs     string
}

// sampleNotice builds the notice. `scope` is the phrase that says which sample
// it describes: " in Mid" when the sample is one role's, nothing when it is
// every role's.
func sampleNotice(n int, minCellN int, suppressed *int, role *aggmodel.Role) sampleNoticeView {
	notice := sampleNoticeView{N: n, NText: IntegerAny(float64(n)), MinCellNText: IntegerAny(float64(minCellN))}
	if role != nil {
		notice.Scope = " in " + RoleLabel(*role)
	}
	if suppressed != nil && *suppressed > 0 {
		notice.Suppressed = *suppressed
		notice.SuppressedText = IntegerAny(float64(*suppressed))
		notice.SuppressedPlural, notice.SuppressedIs = "s", "are"
		if *suppressed == 1 {
			notice.SuppressedPlural, notice.SuppressedIs = "", "is"
		}
	}
	return notice
}

// islandColumn is one column of the table island, in the shape the component
// template reads. `Numeric` is the design system's `align: 'end'`.
type islandColumn struct {
	Key     string
	Label   string
	Numeric bool
}

// islandView is the TableIsland component's data. Everything the island needs
// to re-sort, re-filter and page the table is in the markup, so the client
// runtime re-derives exactly the table the server already sent.
type islandView struct {
	UID        string
	Cols       []islandColumn
	SortKey    string
	AriaSort   string
	Dir        string
	DirLabel   string
	DirText    string
	StatusText string
	Caption    string
	Rows       []*tableRow
	ColCount   int
	Note       template.HTML
	EmptyNote  string
}

// columnsToIsland converts the row layer's columns into the component's shape.
func columnsToIsland(columns []sortColumn) []islandColumn {
	out := make([]islandColumn, 0, len(columns))
	for _, column := range columns {
		out = append(out, islandColumn(column))
	}
	return out
}

// islandNote is the note under the table: what "withheld" means, and how many
// cells the producer withheld. The `&ldquo;` spellings are the reference
// build's, and this string is compared byte for byte.
func islandNote(minCellN int, suppressed *int) string {
	note := "A rate is published only when its cell has at least n = " + IntegerAny(float64(minCellN)) +
		" games; thinner cells read &ldquo;withheld&rdquo; and show their count only."
	if suppressed != nil && *suppressed > 0 {
		note += " " + IntegerAny(float64(*suppressed)) + " further cells were withheld for being below the threshold."
	}
	return note
}

// columnAvailabilityNote is rows.ts's columnAvailabilityNote: the same sentence
// with the reference build's `&quot;` spellings, which is what the copy outside
// the island uses.
func columnAvailabilityNote(minCellN int, suppressed *int) string {
	note := "A rate is published only when its cell has at least n = " + IntegerAny(float64(minCellN)) +
		" games; thinner cells read &quot;withheld&quot; and show their count only."
	if suppressed != nil && *suppressed > 0 {
		note += " " + IntegerAny(float64(*suppressed)) +
			" further cells were withheld for being below the threshold."
	}
	return note
}
