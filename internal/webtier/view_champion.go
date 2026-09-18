package webtier

import (
	"errors"
	"sort"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The champion routes: /champions/<slug> (every role the champion was played
// in) and /champions/<slug>/<role> (one role, in depth).
//
// Both are the same page with a different scope, exactly as ChampionBody.astro
// rendered both, so both are built here. The numbers come from the champion
// artifact when the aggregator published one and from the tier list cells
// otherwise, because a champion that appears in a tier list has a real sample
// even if no detail artifact was written for it.
//
// The component mix on this page is asymmetric in the reference build and the
// asymmetry is load-bearing: the design system's BuildList renders (it declares
// the `lookup` prop that turns a build key into an item, rune or spell), while
// DataTable, StatValue and TierBadge are the layouts/fallback ones. The scoped
// stylesheet the shell inlines is chosen by Page.Champion for the same reason.

// championCells is every tier list cell the snapshot published for one
// champion. It is the fallback the champion routes render from when the
// aggregator wrote no detail artifact for the champion, so it is also the
// fallback the indexability predicates below read.
func championCells(tierList *aggmodel.TierList, championID int) []aggmodel.Cell {
	if tierList == nil {
		return nil
	}
	var cells []aggmodel.Cell
	for _, cell := range tierList.Cells {
		if cell.ChampionID == championID {
			cells = append(cells, cell)
		}
	}
	return cells
}

// championOverviewIndexable reports whether /champions/<slug> is a page of
// statistics rather than a URL that renders an explicit empty state and asks not
// to be indexed. It is meaningful only when a snapshot exists: with none, every
// champion page is the empty state the page was written for, and the sitemap
// lists the prose routes only.
//
// It lives here, next to the page it describes, because the sitemap has to make
// the same decision and a second copy of a decision is a second thing to keep
// in step.
func championOverviewIndexable(cells []aggmodel.Cell) bool {
	return len(cells) > 0
}

// championRoleIndexable reports whether /champions/<slug>/<role> is a page of
// statistics. The sources are the two the page renders from, in the order it
// reads them: the champion's detail artifact first, then the tier list cells,
// because a champion that appears in a tier list has a real sample even if no
// detail artifact was written for it. A role neither source mentions is a URL
// the site publishes and asks crawlers to ignore.
func championRoleIndexable(cells []aggmodel.Cell, artifact *aggmodel.Champion, role aggmodel.Role) bool {
	if artifact != nil {
		for _, entry := range artifact.Roles {
			if entry.Role == role {
				return true
			}
		}
	}
	for _, cell := range cells {
		if cell.Role == role {
			return true
		}
	}
	return false
}

// statView is one fallback StatValue.
type statView struct {
	Label       string
	HasLabel    bool
	Value       string
	HasN        bool
	N           string
	Unavailable bool
}

// dataColumnView and dataRowView mirror the frozen DataTable column and row
// shapes the fallback table renders.
type dataColumnView struct {
	Label string
	End   bool
}

type dataCellView struct {
	Text string
	End  bool
}

type dataRowView struct {
	Cells []dataCellView
}

type dataTableView struct {
	Caption string
	Columns []dataColumnView
	Rows    []dataRowView
}

// championView is the template's view of either champion route.
type championView struct {
	base

	Headline     string
	Patch        string
	HasPartition bool
	IconURL      string
	IconAlt      string
	Intro        string
	Bracket      string
	Window       string
	Generated    string

	HasOtherRoles bool
	OtherRoles    []linkView

	HasStats     bool
	HasTier      bool
	StatsHeading string
	Notice       sampleNoticeView
	Stats        []statView
	TierBadge    fallbackBadgeView
	TierSentence string

	Empty emptyView

	HasRoles bool
	Roles    dataTableView
	HasDepth bool
	Depth    []linkView

	Builds []buildList

	HasSkillOrders bool
	SkillOrders    dataTableView

	HasMatchups  bool
	MatchupIntro string
	MatchupTable dataTableView
	MatchupEmpty emptyView
	HasPairRows  bool
	MatchupPath  string
	// MatchupRoleLower is the role name as the matrix link spells it.
	MatchupRoleLower string

	ComputedHeading  string
	ComputedSentence string

	NavOverviewHref string
	NavOverviewText string

	NoCellsNotice string
}

// ChampionPage renders /champions/<slug>.
func (r *Renderer) ChampionPage(slug string) (*Page, error) {
	return r.championPage(slug, "")
}

// ChampionRolePage renders /champions/<slug>/<role>.
func (r *Renderer) ChampionRolePage(slug string, role string) (*Page, error) {
	return r.championPage(slug, role)
}

func (r *Renderer) championPage(slug string, roleSlug string) (*Page, error) {
	snap, err := r.snapshot("")
	if err != nil {
		return nil, err
	}
	site := snap.Site
	champion, ok := site.ChampionBySlug(slug)
	if !ok {
		return nil, ErrNotFound
	}

	var role *aggmodel.Role
	if roleSlug != "" {
		value, err := r.Role(roleSlug)
		if err != nil {
			return nil, err
		}
		role = &value
	}

	var tierList *aggmodel.TierList
	if snap.HasSeg {
		loaded, err := site.TierList(snap.Seg)
		if err != nil {
			return nil, err
		}
		tierList = loaded
	}
	cells := championCells(tierList, champion.ID)

	// The detail artifact is the rich source and the tier list cells are the
	// fallback, so a missing detail artifact is not an error: it is the state
	// this page has always supported. A detail artifact that is there and
	// unreadable is a 503, because the manifest advertised it.
	var artifact *aggmodel.Champion
	if snap.HasSeg {
		loaded, err := site.ChampionDetail(snap.Seg, champion.ID)
		if err != nil {
			if !errors.Is(err, ErrArtifactMissing) {
				return nil, err
			}
		} else {
			artifact = loaded
		}
	}

	cellForRole := func(candidate aggmodel.Role) *aggmodel.Cell {
		if artifact != nil {
			for index := range artifact.Roles {
				if artifact.Roles[index].Role == candidate {
					return &artifact.Roles[index].Stats
				}
			}
		}
		for index := range cells {
			if cells[index].Role == candidate {
				return &cells[index]
			}
		}
		return nil
	}

	// A cell the aggregator published with no games in it is not a role the
	// champion was played in, and saying so would be a claim the artifact does
	// not support.
	hasGames := func(candidate aggmodel.Role) bool {
		cell := cellForRole(candidate)
		return cell != nil && cell.N > 0
	}

	playedRoles := make([]aggmodel.Role, 0, len(Roles))
	for _, candidate := range Roles {
		if hasGames(candidate) {
			playedRoles = append(playedRoles, candidate)
		}
	}

	var roleStats *aggmodel.Cell
	var roleDetail *aggmodel.ChampionRole
	if role != nil {
		roleStats = cellForRole(*role)
		if artifact != nil {
			for index := range artifact.Roles {
				if artifact.Roles[index].Role == *role {
					roleDetail = &artifact.Roles[index]
				}
			}
		}
	}

	// The overview lists the roles the champion was played in; the role page
	// lists the champion's other roles, so a reader can move sideways without
	// going back.
	listedRoles := make([]aggmodel.Role, 0, len(playedRoles))
	for _, candidate := range playedRoles {
		if role == nil || candidate != *role {
			listedRoles = append(listedRoles, candidate)
		}
	}

	// The matchup artifact is only read for a role page, and only its absence is
	// tolerated: a pair list that exists and cannot be read is a fault.
	var matchups *aggmodel.Matchups
	if role != nil && snap.HasSeg {
		loaded, err := site.Matchups(snap.Seg, *role)
		if err != nil {
			if !errors.Is(err, ErrArtifactMissing) {
				return nil, err
			}
		} else {
			matchups = loaded
		}
	}

	minCellN := snap.minCellN()
	if snap.Partition == nil {
		minCellN = 0
	}
	lookup, err := site.BuildLookup()
	if err != nil {
		return nil, err
	}

	view := championView{
		base:             base{Site: site, SiteURL: r.siteURL, R: r},
		Headline:         champion.Name,
		IconURL:          site.IconURL(champion.Icon),
		IconAlt:          champion.Name + " champion icon",
		ComputedHeading:  computedHeading(site),
		ComputedSentence: computedFromSentence(site, snap.minCellN(), snap.Partition != nil),
		NavOverviewHref:  "/champions/" + champion.Slug,
		NavOverviewText:  champion.Name + " overview",
	}
	if snap.Partition != nil {
		view.HasPartition = true
		view.Patch = snap.Patch
		view.Bracket = string(snap.Partition.Bracket)
		view.Window = WindowLabel(snap.Partition.SourceWindow.From, snap.Partition.SourceWindow.To)
		view.Generated = UTCStampAny(snap.Partition.GeneratedAt)
	}
	if role != nil {
		view.Headline = champion.Name + " " + strings.ToLower(RoleLabel(*role))
	}
	if view.HasPartition {
		view.Headline += " - patch " + snap.Patch
	}

	// The headline sentence: how many games the role rests on, or the explicit
	// statement that it rests on none.
	switch {
	case role != nil && roleStats != nil && roleStats.N > 0:
		where := "this snapshot"
		if snap.Partition != nil {
			where = snap.Partition.Region + " ranked solo queue on patch " + snap.Partition.Patch
		}
		view.Intro = champion.Name + " played in " + strings.ToLower(RoleLabel(*role)) + " in " + where +
			", with every rate shown next to the number of games it was measured over."
	case role != nil:
		view.Intro = champion.Name + " has no published cell in " + strings.ToLower(RoleLabel(*role)) +
			" in this snapshot, so this page states that rather than an estimate."
	default:
		view.Intro = champion.Name + " is known to this build as Data Dragon " + site.DdragonVersion()
		if snap.Partition != nil {
			if len(playedRoles) > 0 {
				plural := "s"
				if len(playedRoles) == 1 {
					plural = ""
				}
				view.Intro += " and was played in " + itoa(len(playedRoles)) + " role" + plural +
					" on patch " + snap.Partition.Patch
			} else {
				view.Intro += " and has no published cell in any role on patch " + snap.Partition.Patch
			}
		}
		view.Intro += "."
	}

	if role != nil && len(listedRoles) > 0 {
		view.HasOtherRoles = true
		view.OtherRoles = championRoleLinks(champion, listedRoles, "")
	}

	// The overview page tells a reader where the page itself comes from when the
	// snapshot holds nothing for the champion, because the URL exists before the
	// numbers do.
	if role == nil && len(cells) == 0 {
		view.NoCellsNotice = champion.Name + " comes from the Data Dragon " + site.DdragonVersion() +
			" champion list checked into this repository, which is why the page exists before the first snapshot does."
	}

	if roleStats != nil {
		unpublishable := CellAvailabilityOf(roleStats.N, minCellN) != AvailabilityPublished
		view.HasStats = true
		view.StatsHeading = "Statistics"
		view.HasTier = roleStats.N > 0
		if role != nil {
			view.StatsHeading = RoleLabel(*role) + " statistics"
		}
		view.Notice = sampleNotice(roleStats.N, minCellN, snap.suppressedCells(), role)
		view.Stats = []statView{
			rateStatView("Win rate", roleStats.WinRate, roleStats.N, true, unpublishable),
			rateStatView("Pick rate", roleStats.PickRate, 0, false, unpublishable),
			rateStatView("Ban rate", roleStats.BanRate, 0, false, unpublishable),
			rateStatView("95% interval half-width", roleStats.CI95HalfWidth, 0, false, unpublishable),
		}
		view.TierBadge = fallbackBadge(roleStats.Tier, roleStats.N)
		view.TierSentence = tierSentence(roleStats, minCellN, unpublishable)
	} else {
		title := "No sample yet"
		body := emptyReason(site)
		if snap.Partition != nil {
			title = "No cells published for this selection"
		}
		if role != nil && snap.Partition != nil {
			body = champion.Name + " has no published cell in " + strings.ToLower(RoleLabel(*role)) +
				" in this snapshot, so this page reports nothing rather than an estimate."
		}
		view.Empty = emptyFor(title, body, emptyFloor(snap))
	}

	if role == nil && len(playedRoles) > 0 {
		view.HasRoles = true
		view.Roles = championRoleTable(champion, playedRoles, cellForRole, minCellN, snap.Patch, snap.Partition != nil)
		if len(listedRoles) > 0 {
			view.HasDepth = true
			view.Depth = championRoleLinks(champion, listedRoles, " in depth")
		}
	}

	if roleDetail != nil && len(roleDetail.Items)+len(roleDetail.Runes)+len(roleDetail.Spells) > 0 {
		scope := "this role"
		if role != nil {
			scope = strings.ToLower(RoleLabel(*role))
		}
		view.Builds = []buildList{
			buildSection("Items on "+champion.Name+" in "+scope, buildKindItems, roleDetail.Items, lookup),
			buildSection("Runes on "+champion.Name, buildKindRunes, roleDetail.Runes, lookup),
			buildSection("Summoner spells on "+champion.Name, buildKindSpells, roleDetail.Spells, lookup),
		}
	}

	if roleDetail != nil && len(roleDetail.SkillOrders) > 0 {
		view.HasSkillOrders = true
		view.SkillOrders = skillOrderTable(champion, skillOrders(roleDetail.SkillOrders), minCellN)
	}

	if role != nil && roleStats != nil {
		view.HasMatchups = true
		rows := championMatchupRows(matchups, champion.ID, site, minCellN)
		view.HasPairRows = len(rows) > 0
		if view.HasPairRows {
			intro := champion.Name + " against every " + strings.ToLower(RoleLabel(*role)) +
				" opponent the snapshot holds a pair for."
			if minimum, ok := matchupMinN(rows); ok {
				intro += " The thinnest pair here has n = " + IntegerAny(float64(minimum)) + " games."
			}
			intro += ` A pair below the threshold reads "` + WithheldLiteral +
				`" and shows its count only; a pair the snapshot does not contain reads "` + NoSampleLiteral + `".`
			view.MatchupIntro = intro
			view.MatchupTable = matchupTable(champion, *role, rows)
		} else {
			view.MatchupEmpty = emptyFor("No matchup pairs published",
				"The snapshot holds no matchup pair for "+champion.Name+" in "+strings.ToLower(RoleLabel(*role))+
					", so no opponent is shown rather than an estimate.", emptyFloor(snap))
		}
		view.MatchupPath = "/matchups/" + RoleSlugString(*role)
		view.MatchupRoleLower = strings.ToLower(RoleLabel(*role))
	}

	jsonld, err := championJSONLD(r, snap, champion, role, roleStats, minCellN)
	if err != nil {
		return nil, err
	}
	body, err := r.RenderBody("page:champion", view)
	if err != nil {
		return nil, err
	}

	page := &Page{
		Champion:  true,
		Body:      body,
		JSONLD:    jsonld,
		Partition: snap.Partition,
	}
	partition := snap.Partition
	if role == nil {
		page.CanonicalPath = CanonicalPath("/champions/" + champion.Slug)
		page.Active = "/champions/" + champion.Slug
		page.Title = champion.Name + " statistics - League of Legends - " + SiteName
		page.Description = champion.Name + " statistics are not published yet: no aggregate snapshot exists for " +
			"this site, so this page renders an explicit empty state."
		if partition != nil {
			page.Title = champion.Name + " statistics, patch " + partition.Patch + " - League of Legends - " + SiteName
			page.Description = champion.Name + " win, pick and ban rates by role for League of Legends patch " +
				partition.Patch + ", " + partition.Region + " ranked solo queue, each with the number of games behind it."
		}
		page.Noindex = !championOverviewIndexable(cells)
	} else {
		label := RoleLabel(*role)
		slug := RoleSlugString(*role)
		page.CanonicalPath = CanonicalPath("/champions/" + champion.Slug + "/" + slug)
		page.Active = "/champions/" + champion.Slug + "/" + slug
		page.Title = champion.Name + " " + label + " statistics - League of Legends - " + SiteName
		page.Description = champion.Name + " " + strings.ToLower(label) + " statistics are not published yet: no " +
			"aggregate snapshot exists for this site, so this page renders an explicit empty state."
		if partition != nil {
			page.Title = champion.Name + " " + label + " build, runes and matchups, patch " + partition.Patch + " - " + SiteName
			page.Description = champion.Name + " in " + strings.ToLower(label) + ": win, pick and ban rates, common " +
				"builds, runes and matchup win rates for League of Legends patch " + partition.Patch +
				", each with the games behind it."
		}
		// A role the artifact measured or the tier list published is a real page;
		// one the snapshot says nothing about is a URL that exists but should not
		// be indexed as a page of statistics. The predicate is shared with the
		// sitemap, which advertises exactly the routes this is true for.
		page.Noindex = !championRoleIndexable(cells, artifact, *role)
	}
	if partition == nil {
		page.PatchLabelValue = notPublishedLabel
	}
	return page, nil
}

func rateStatView(label string, value float64, n int, withN bool, unavailable bool) statView {
	stat := statView{Label: label, HasLabel: true, Unavailable: unavailable}
	if unavailable {
		return stat
	}
	stat.Value = Percent(value, 2)
	if withN {
		stat.HasN = true
		stat.N = Integer(float64(n))
	}
	return stat
}

// tierSentence is the sentence under the stat row. The tier letter is always
// shown when the cell holds games; what changes is whether the page may claim
// what it measures.
func tierSentence(cell *aggmodel.Cell, minCellN int, unpublishable bool) string {
	if cell.N <= 0 {
		return "No tier is shown for this cell: it holds no games yet (n = 0), and a grade without games would be a " +
			"claim the snapshot cannot support."
	}
	if unpublishable {
		return "withheld: this cell holds n = " + IntegerAny(float64(cell.N)) + " games, below the n = " +
			IntegerAny(float64(minCellN)) + " this site publishes from."
	}
	return "measured over n = " + IntegerAny(float64(cell.N)) + " games, so the win rate is " + Percent(cell.WinRate, 2) +
		" plus or minus " + Decimal(cell.CI95HalfWidth*100, 2) + " percentage points at 95% confidence."
}

// emptyFloor is `partition?.min_cell_n ?? null` as emptyFor wants it.
func emptyFloor(snap snapshotView) *int {
	if snap.Partition == nil {
		return nil
	}
	value := snap.Partition.MinCellN
	return &value
}

// championRoleLinks is the link list the champion pages use twice: once as "the
// other roles in this snapshot" and once as the "in depth" links.
func championRoleLinks(champion aggmodel.StaticChampion, roles []aggmodel.Role, suffix string) []linkView {
	links := make([]linkView, 0, len(roles))
	for _, role := range roles {
		links = append(links, linkView{
			Href: "/champions/" + champion.Slug + "/" + RoleSlugString(role),
			Name: champion.Name + " " + strings.ToLower(RoleLabel(role)) + suffix,
		})
	}
	return links
}

// championRoleTable renders the "Roles" table: every role the champion was
// played in, by role.
func championRoleTable(champion aggmodel.StaticChampion, roles []aggmodel.Role, cellFor func(aggmodel.Role) *aggmodel.Cell, minCellN int, patch string, hasPartition bool) dataTableView {
	table := dataTableView{
		Caption: champion.Name + " by role",
		Columns: []dataColumnView{
			{Label: "Role"},
			{Label: "Tier"},
			{Label: "Games (n)", End: true},
			{Label: "Win rate", End: true},
			{Label: "Pick rate", End: true},
			{Label: "Ban rate", End: true},
			{Label: "95% interval", End: true},
		},
	}
	if hasPartition {
		table.Caption += ", patch " + patch
	}
	type ranked struct {
		row dataRowView
		n   int
	}
	rows := make([]ranked, 0, len(roles))
	for _, role := range roles {
		cell := cellFor(role)
		if cell == nil {
			continue
		}
		rows = append(rows, ranked{
			row: dataRowView{Cells: []dataCellView{
				{Text: RoleLabel(role)},
				{Text: tierValueText(cell)},
				{Text: Integer(float64(cell.N)), End: true},
				{Text: rateValueText(cell, minCellN, cell.WinRate), End: true},
				{Text: rateValueText(cell, minCellN, cell.PickRate), End: true},
				{Text: rateValueText(cell, minCellN, cell.BanRate), End: true},
				{Text: rateText(cell, minCellN, PlusMinus(cell.CI95HalfWidth, 2)), End: true},
			}},
			n: cell.N,
		})
	}
	// initialSortKey="n", initialSortDir="desc": the fallback table sorts in the
	// browser, so the server sorts here and the two agree.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].n > rows[j].n })
	for _, row := range rows {
		table.Rows = append(table.Rows, row.row)
	}
	return table
}

// skillOrderTable renders the skill-order table for one role detail.
func skillOrderTable(champion aggmodel.StaticChampion, rows []skillOrderRow, minCellN int) dataTableView {
	table := dataTableView{
		Caption: champion.Name + " skill orders",
		Columns: []dataColumnView{
			{Label: "Skill order"},
			{Label: "Games (n)", End: true},
			{Label: "Win rate", End: true},
		},
	}
	for _, row := range rows {
		win := WithheldLiteral
		if CellAvailabilityOf(row.N, minCellN) == AvailabilityPublished {
			win = Percent(row.Win, 2)
		}
		table.Rows = append(table.Rows, dataRowView{Cells: []dataCellView{
			{Text: row.Order},
			{Text: Integer(float64(row.N)), End: true},
			{Text: win, End: true},
		}})
	}
	return table
}

// matchupTable renders one champion's matchups in one role.
func matchupTable(champion aggmodel.StaticChampion, role aggmodel.Role, rows []matchupRow) dataTableView {
	table := dataTableView{
		Caption: champion.Name + " matchups in " + strings.ToLower(RoleLabel(role)),
		Columns: []dataColumnView{
			{Label: "Opponent"},
			{Label: "Games (n)", End: true},
			{Label: "Win rate", End: true},
			{Label: "95% interval", End: true},
		},
	}
	ordered := make([]matchupRow, len(rows))
	copy(ordered, rows)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].N > ordered[j].N })
	for _, row := range ordered {
		table.Rows = append(table.Rows, dataRowView{Cells: []dataCellView{
			{Text: row.Opponent},
			{Text: Integer(float64(row.N)), End: true},
			{Text: row.WinRate, End: true},
			{Text: row.Interval, End: true},
		}})
	}
	return table
}

// matchupRow is one row of a champion's matchup table, already formatted.
type matchupRow struct {
	Opponent string
	N        int
	WinRate  string
	Interval string
}

// championMatchupRows is rows.ts's championMatchupRows. The artifact stores
// each ordered pair once, lower champion id first, so the reader mirrors it: a
// pair whose stored direction is the other way round is read as its complement.
func championMatchupRows(matchups *aggmodel.Matchups, championID int, site *Site, minCellN int) []matchupRow {
	if matchups == nil {
		return nil
	}
	rows := make([]matchupRow, 0, len(matchups.Cells))
	for _, cell := range matchups.Cells {
		var n int
		var winRate float64
		switch {
		case cell.ChampionID == championID:
			n, winRate = cell.N, cell.WinRate
		case cell.OpponentID == championID:
			n, winRate = cell.N, 1-cell.WinRate
		default:
			continue
		}
		opponentID := cell.OpponentID
		if cell.OpponentID == championID {
			opponentID = cell.ChampionID
		}
		name := site.ChampionName(opponentID)
		if name == "" {
			name = "Champion " + itoa(opponentID)
		}
		availability := CellAvailabilityOf(n, minCellN)
		row := matchupRow{
			Opponent: name,
			N:        n,
			WinRate:  NoSampleLiteral,
			Interval: WithheldLiteral,
		}
		switch availability {
		case AvailabilityPublished:
			row.WinRate = Percent(winRate, 2)
			row.Interval = PlusMinus(cell.CI95HalfWidth, 2)
		case AvailabilityWithheld:
			row.WinRate = WithheldLiteral
		}
		rows = append(rows, row)
	}
	return rows
}

// matchupMinN is the thinnest pair the table shows, which is the number the
// page's sentence quotes.
func matchupMinN(rows []matchupRow) (int, bool) {
	lowest := 0
	found := false
	for _, row := range rows {
		if !found || row.N < lowest {
			lowest, found = row.N, true
		}
	}
	return lowest, found
}

// tierValueText is rows.ts's tierValue: a tier is a function of the win rate and
// the pick rate, so a cell that was never measured has no tier to show.
func tierValueText(cell *aggmodel.Cell) string {
	if cell.N <= 0 {
		return NoSampleLiteral
	}
	return string(cell.Tier)
}

// rateValueText and rateText are rows.ts's rateValue: a rate that exists and is
// withheld is not an ordinary missing value, so it is spelled out.
func rateValueText(cell *aggmodel.Cell, minCellN int, value float64) string {
	return rateText(cell, minCellN, Percent(value, 2))
}

func rateText(cell *aggmodel.Cell, minCellN int, published string) string {
	switch CellAvailabilityOf(cell.N, minCellN) {
	case AvailabilityNoSample:
		return NoSampleLiteral
	case AvailabilityWithheld:
		return WithheldLiteral
	default:
		return published
	}
}

// championJSONLD is the page's structured-data node, emitted only for a live
// snapshot: the reference attaches a Dataset node to a page whose numbers are
// measurements, and a preview or an empty page is not one.
func championJSONLD(r *Renderer, snap snapshotView, champion aggmodel.StaticChampion, role *aggmodel.Role, stats *aggmodel.Cell, minCellN int) (string, error) {
	if stats == nil || snap.Partition == nil || !snap.Site.Live() {
		return "", nil
	}
	partition := snap.Partition
	path := "/champions/" + champion.Slug + "/"
	name := champion.Name + " statistics, League of Legends patch " + partition.Patch
	description := "Per-role win, pick and ban rates for " + champion.Name + ", " + partition.Region +
		" ranked solo queue, patch " + partition.Patch + "."
	if role != nil {
		path = "/champions/" + champion.Slug + "/" + RoleSlugString(*role) + "/"
		name = champion.Name + " " + RoleLabel(*role) + " statistics, League of Legends patch " + partition.Patch
		description = "Win, pick and ban rates for " + champion.Name + " in " + strings.ToLower(RoleLabel(*role)) +
			", " + partition.Region + " ranked solo queue, patch " + partition.Patch + "."
	}
	node, err := datasetNode(datasetNodeInput{
		URL:          r.absolute(path),
		Name:         name,
		Description:  description,
		Patch:        partition.Patch,
		SourceWindow: partition.SourceWindow,
		GeneratedAt:  partition.GeneratedAt,
		MinCellN:     minCellN,
		SampleSize:   stats.N,
		Variables: []datasetVariable{
			{Name: "games played (n)", Unit: "games"},
			{Name: "win rate", Unit: "win rate"},
			{Name: "pick rate", Unit: "pick rate"},
			{Name: "ban rate", Unit: "ban rate"},
		},
		State: string(snap.Site.State()),
	})
	if err != nil {
		return "", err
	}
	return JSONLDNode(node)
}
