package webtier

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The row model for every published table, and the ordering rules that go with
// it.
//
// This is a port of web/src/lib/rows.ts and of the sorting half of
// web/src/components/TableIsland.astro. It is a port rather than a rewrite on
// purpose: the tier list is sorted twice - once here, on the server, and again
// in the browser by the island - and the two orderings have to agree, because
// the island only ever reorders rows it did not create. A tie-break that exists
// on one side and not the other would move a reader's rows under them the
// moment the island boots.

// tierRank is the ordering the tier letters sort by, best first. It is the same
// map TableIsland carries, and it is also what `data-v-tier` publishes, so a
// sort by tier in the browser and a sort by tier on the server cannot disagree.
var tierRank = map[aggmodel.Tier]int{
	aggmodel.TierSPlus: 6,
	aggmodel.TierS:     5,
	aggmodel.TierA:     4,
	aggmodel.TierB:     3,
	aggmodel.TierC:     2,
	aggmodel.TierD:     1,
}

// bracketLabels are the words the caption uses for a partition's bracket.
var bracketLabels = map[aggmodel.Bracket]string{
	aggmodel.BracketAll:          "all ranks",
	aggmodel.BracketEmeraldPlus:  "Emerald and above",
	aggmodel.BracketPlatinumPlus: "Platinum and above",
	aggmodel.BracketDiamondPlus:  "Diamond and above",
	aggmodel.BracketMasterPlus:   "Master and above",
}

// BracketLabel returns the caption spelling of a bracket.
func BracketLabel(bracket aggmodel.Bracket) string {
	if label, ok := bracketLabels[bracket]; ok {
		return label
	}
	return string(bracket)
}

// rowValue is one cell's sort value, in the shape the island's `readValue`
// produces: a rate that was not published is absent rather than zero, so it
// sorts last in both directions.
type rowValue struct {
	missing bool
	numeric bool
	num     float64
	str     string
}

func numberValue(value float64) rowValue { return rowValue{numeric: true, num: value} }
func stringValue(value string) rowValue  { return rowValue{str: value} }
func missingValue() rowValue             { return rowValue{missing: true} }

// tableRow is one published (champion, role) cell as a table row.
type tableRow struct {
	Name      string
	Slug      string
	IconURL   string
	Href      string
	Role      aggmodel.Role
	RoleLabel string
	RoleIndex int
	EveryRole bool

	Tier          aggmodel.Tier
	TierRank      int
	TierPublished bool
	N             int
	Availability  CellAvailability

	WinRate  float64
	PickRate float64
	BanRate  float64
	CI95     float64

	values map[string]rowValue
}

// Search is the haystack the filter matches, exactly as the island builds it.
func (r *tableRow) Search() string {
	return strings.ToLower(r.Name + " " + r.RoleLabel)
}

// Published reports whether the row's rates may be shown.
func (r *tableRow) Published() bool { return r.Availability == AvailabilityPublished }

// Unavailable is the word a withheld rate column reads as.
func (r *tableRow) Unavailable() string {
	if r.Availability == AvailabilityNoSample {
		return NoSampleLiteral
	}
	return WithheldLiteral
}

// NText is the games column. `n` is never withheld: the count is the one number
// a thin cell is allowed to show.
func (r *tableRow) NText() string { return Integer(float64(r.N)) }

// WinRateText, PickRateText, BanRateText and CI95Text are the rate cells. They
// are empty for a cell that may not publish a rate; the template renders the
// unavailable word instead.
func (r *tableRow) WinRateText() string  { return Percent(r.WinRate, 2) }
func (r *tableRow) PickRateText() string { return Percent(r.PickRate, 2) }
func (r *tableRow) BanRateText() string  { return Percent(r.BanRate, 2) }
func (r *tableRow) CI95Text() string     { return PlusMinus(r.CI95, 2) }

// TierAttr, NAttr, RoleAttr and the rate attributes are the `data-v-*` values
// the island sorts on. They are the raw values, not the printed ones: the
// printed rate carries a percent sign and a thousands separator, neither of
// which can be compared.
func (r *tableRow) TierAttr() string { return itoa(r.TierRank) }
func (r *tableRow) NAttr() string    { return itoa(r.N) }
func (r *tableRow) RoleAttr() string { return itoa(r.RoleIndex) }

func rateAttr(published bool, value float64) string {
	if !published {
		return ""
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func (r *tableRow) WinRateAttr() string { return rateAttr(r.Published(), r.WinRate) }
func (r *tableRow) PickRateAttr() string {
	return rateAttr(r.Published(), r.PickRate)
}
func (r *tableRow) BanRateAttr() string { return rateAttr(r.Published(), r.BanRate) }
func (r *tableRow) CI95Attr() string    { return rateAttr(r.Published(), r.CI95) }

// sortValue returns the value the row sorts on for a column key.
func (r *tableRow) sortValue(key string) rowValue {
	if value, ok := r.values[key]; ok {
		return value
	}
	return missingValue()
}

// sortColumns are the columns a reader may sort by, in the order the island's
// select lists them, with the flags that decide how two values compare.
type sortColumn struct {
	Key     string
	Label   string
	Numeric bool
}

// TierListColumns is the column set of a tier list. `everyRole` selects the
// variant that carries a Role column, which is what the all-roles table uses.
func TierListColumns(everyRole bool) []sortColumn {
	columns := []sortColumn{
		{Key: "champion", Label: "Champion"},
		{Key: "tier", Label: "Tier"},
		{Key: "n", Label: "Games (n)", Numeric: true},
		{Key: "win_rate", Label: "Win rate", Numeric: true},
		{Key: "pick_rate", Label: "Pick rate", Numeric: true},
		{Key: "ban_rate", Label: "Ban rate", Numeric: true},
		{Key: "ci95", Label: "95% interval", Numeric: true},
	}
	if !everyRole {
		return columns
	}
	out := make([]sortColumn, 0, len(columns)+1)
	out = append(out, columns[0], sortColumn{Key: "role", Label: "Role"})
	return append(out, columns[1:]...)
}

// RoleColumns is the column set of the champion-by-role table.
func RoleColumns() []sortColumn {
	return []sortColumn{
		{Key: "role", Label: "Role"},
		{Key: "tier", Label: "Tier"},
		{Key: "n", Label: "Games (n)", Numeric: true},
		{Key: "win_rate", Label: "Win rate", Numeric: true},
		{Key: "pick_rate", Label: "Pick rate", Numeric: true},
		{Key: "ban_rate", Label: "Ban rate", Numeric: true},
		{Key: "ci95", Label: "95% interval", Numeric: true},
	}
}

// SortKeys lists the column keys a query may sort by, so an unknown ?sort=
// falls back to the default instead of sorting by nothing.
func SortKeys(columns []sortColumn) []string {
	keys := make([]string, 0, len(columns))
	for _, column := range columns {
		keys = append(keys, column.Key)
	}
	return keys
}

// ColumnLabel returns the label of a sort key, which the status line quotes.
func ColumnLabel(columns []sortColumn, key string) string {
	for _, column := range columns {
		if column.Key == key {
			return column.Label
		}
	}
	return key
}

// Collate is a deterministic stand-in for `String.prototype.localeCompare`
// with the 'en' locale, which is what the reference sorts names with.
//
// The two agree on the ASCII champion names this site publishes: English
// collation compares letter base first and case second, which is what comparing
// the case-folded strings does. They are not the same function in general - the
// reference delegates to ICU and this does not - so the difference is confined
// to accents and apostrophes, and the comparison falls back to the raw strings
// so that no two distinct names ever compare equal.
func Collate(left string, right string) int {
	lowerLeft, lowerRight := strings.ToLower(left), strings.ToLower(right)
	if lowerLeft != lowerRight {
		return strings.Compare(lowerLeft, lowerRight)
	}
	return strings.Compare(left, right)
}

// OrderRows sorts rows the way the reference does: by the chosen column in the
// chosen direction, with ties broken busiest-first and then alphabetically.
//
// The tie-break is not decoration. The island re-sorts the rows it was given,
// so the server's order is the client's starting order, and an unstable
// comparison here would show a reader a different table the moment the island
// booted. Ties are therefore fully ordered rather than left to the sort.
func OrderRows(rows []*tableRow, key string, direction string) []*tableRow {
	ordered := make([]*tableRow, len(rows))
	copy(ordered, rows)
	sign := 1.0
	if direction != DirAsc {
		sign = -1
	}
	sortStable(ordered, func(left, right *tableRow) int {
		if result := compareValues(left.sortValue(key), right.sortValue(key), sign); result != 0 {
			return result
		}
		if left.N != right.N {
			if left.N > right.N {
				return -1
			}
			return 1
		}
		return Collate(left.Name, right.Name)
	})
	return ordered
}

// compareValues is the reference's compareValues: a missing value sorts last in
// both directions, numbers compare numerically and everything else collates.
func compareValues(left, right rowValue, sign float64) int {
	if left.missing || right.missing {
		if left.missing == right.missing {
			return 0
		}
		if left.missing {
			return 1
		}
		return -1
	}
	if left.numeric && right.numeric {
		if left.num == right.num {
			return 0
		}
		if left.num < right.num {
			return -int(sign)
		}
		return int(sign)
	}
	collated := Collate(left.str, right.str)
	if collated == 0 {
		return 0
	}
	if collated < 0 {
		return -int(sign)
	}
	return int(sign)
}

// FilterRows applies the filter the island's input applies: a case-insensitive
// substring match against `data-search`.
func FilterRows(rows []*tableRow, filter string) []*tableRow {
	needle := strings.ToLower(strings.TrimSpace(filter))
	if needle == "" {
		return rows
	}
	kept := make([]*tableRow, 0, len(rows))
	for _, row := range rows {
		if strings.Contains(row.Search(), needle) {
			kept = append(kept, row)
		}
	}
	return kept
}

// sortStable sorts rows with the three-way comparison above. Equality keeps the
// incoming order, which matters only when the tie-break is exhausted, and the
// tie-break makes that impossible for two distinct rows.
func sortStable(rows []*tableRow, compare func(left, right *tableRow) int) {
	sort.SliceStable(rows, func(left, right int) bool {
		return compare(rows[left], rows[right]) < 0
	})
}

// itoa is strconv.Itoa without the import at every call site.
func itoa(value int) string { return strconv.Itoa(value) }

// roleIndex is ROLES.indexOf(role), the number the island sorts the role
// column on. It is -1 for a role the site does not publish.
func roleIndex(role aggmodel.Role) int {
	for index, candidate := range Roles {
		if candidate == role {
			return index
		}
	}
	return -1
}

// TierListRows builds the rows the tier-list table shows for a set of cells.
//
// The empty string and the missing value are different inputs: a cell that was
// never measured has no tier to show, so it reads "no sample" rather than a
// grade, and its rates are absent rather than zero.
func TierListRows(cells []aggmodel.Cell, site *Site, minCellN int, everyRole bool) []*tableRow {
	rows := make([]*tableRow, 0, len(cells))
	for _, cell := range cells {
		name := site.ChampionName(cell.ChampionID)
		slug := site.ChampionSlug(cell.ChampionID)
		measured := cell.N > 0
		tier := cell.Tier
		if !measured {
			tier = aggmodel.Tier(NoSampleLiteral)
		}
		row := &tableRow{
			Name:          name,
			Slug:          slug,
			IconURL:       site.IconURL(championIcon(site, cell.ChampionID)),
			Role:          cell.Role,
			RoleLabel:     RoleLabel(cell.Role),
			RoleIndex:     roleIndex(cell.Role),
			EveryRole:     everyRole,
			Tier:          tier,
			TierRank:      TierRank(cell.Tier),
			TierPublished: measured,
			N:             cell.N,
			Availability:  CellAvailabilityOf(cell.N, minCellN),
			WinRate:       cell.WinRate,
			PickRate:      cell.PickRate,
			BanRate:       cell.BanRate,
			CI95:          cell.CI95HalfWidth,
		}
		if everyRole {
			row.Href = "/champions/" + slug
		} else {
			row.Href = "/champions/" + slug + "/" + RoleSlugString(cell.Role)
		}
		row.values = rowValues(row, everyRole)
		rows = append(rows, row)
	}
	return rows
}

// championIcon is the icon file name the static champion dataset carries, or
// the empty string when the champion is not known to this build.
func championIcon(site *Site, championID int) string {
	if champion, ok := site.ChampionByID(championID); ok {
		return champion.Icon
	}
	return ""
}

// rowValues is the island's `values` map: the raw value each column sorts on.
// A rate that may not be published is missing rather than zero, which is the
// same rule the fallback table applies to a null cell.
func rowValues(row *tableRow, everyRole bool) map[string]rowValue {
	published := row.Published()
	values := map[string]rowValue{
		"champion":  stringValue(row.Name),
		"tier":      stringValue(string(row.Tier)),
		"n":         numberValue(float64(row.N)),
		"win_rate":  rateValue(published, row.WinRate),
		"pick_rate": rateValue(published, row.PickRate),
		"ban_rate":  rateValue(published, row.BanRate),
		"ci95":      rateValue(published, row.CI95),
	}
	if row.TierPublished {
		values["tier"] = numberValue(float64(row.TierRank))
	}
	if everyRole {
		values["role"] = numberValue(float64(row.RoleIndex))
	}
	return values
}

func rateValue(published bool, value float64) rowValue {
	if !published {
		return missingValue()
	}
	return numberValue(value)
}

// ChampionRoleRows builds the champion overview's table: one row per role the
// champion was played in, ordered by the table's own sort rather than here.
func ChampionRoleRows(roles []aggmodel.Role, cells map[aggmodel.Role]aggmodel.Cell, site *Site, minCellN int) []*tableRow {
	rows := make([]*tableRow, 0, len(roles))
	for _, role := range roles {
		cell := cells[role]
		measured := cell.N > 0
		tier := cell.Tier
		if !measured {
			tier = aggmodel.Tier(NoSampleLiteral)
		}
		row := &tableRow{
			Role:          role,
			RoleLabel:     RoleLabel(role),
			RoleIndex:     roleIndex(role),
			Tier:          tier,
			TierRank:      TierRank(cell.Tier),
			TierPublished: measured,
			N:             cell.N,
			Availability:  CellAvailabilityOf(cell.N, minCellN),
			WinRate:       cell.WinRate,
			PickRate:      cell.PickRate,
			BanRate:       cell.BanRate,
			CI95:          cell.CI95HalfWidth,
		}
		row.values = map[string]rowValue{
			"role":      stringValue(RoleLabel(role)),
			"n":         numberValue(float64(cell.N)),
			"win_rate":  rateValue(row.Published(), cell.WinRate),
			"pick_rate": rateValue(row.Published(), cell.PickRate),
			"ban_rate":  rateValue(row.Published(), cell.BanRate),
			"ci95":      rateValue(row.Published(), cell.CI95HalfWidth),
		}
		if measured {
			row.values["tier"] = numberValue(float64(row.TierRank))
		} else {
			row.values["tier"] = stringValue(NoSampleLiteral)
		}
		rows = append(rows, row)
	}
	_ = site
	return rows
}
