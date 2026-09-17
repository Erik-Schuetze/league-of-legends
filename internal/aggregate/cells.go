package aggregate

import (
	"fmt"
	"math"
	"sort"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// This file turns the tally rows DuckDB produces into the frozen Cell shape.
// Everything that is policy - what a rate means, when a cell is suppressed,
// how a tier is scored - lives here in Go rather than in SQL, so it is
// reviewable, unit testable, and identical for the real build and the demo.

// ConfidenceHalfWidthFactor is the numerator of the confidence half width.
//
// docs/contracts.md fixes ci95_half_width as 0.98/sqrt(n): the half width of a
// 95 percent Wilson-style interval for a proportion at p=0.5, which is the
// worst case and therefore the honest one to publish without knowing the rate.
const ConfidenceHalfWidthFactor = 0.98

// CellCount is one per (champion, role) tally straight out of DuckDB.
type CellCount struct {
	ChampionID int    `json:"champion_id"`
	Role       string `json:"role"`
	N          int    `json:"n"`
	Wins       int    `json:"wins"`
}

// BuildCount is one row of an item, rune or spell grouping. Key is opaque here
// and interpreted by the artifact writer, which is the only place that knows
// what "kind" the key belongs to.
type BuildCount struct {
	ChampionID int    `json:"champion_id"`
	Role       string `json:"role"`
	Key        []int  `json:"key"`
	N          int    `json:"n"`
	Wins       int    `json:"wins"`
}

// BanCount is the number of distinct matches a champion was banned in.
type BanCount struct {
	ChampionID int `json:"champion_id"`
	Bans       int `json:"bans"`
}

// MatchupCount is one ordered champion pair tally inside one role. Only the
// direction with the lower champion id is produced by the query, which is what
// the frozen MatchupCell shape means by "one direction only". The role travels
// with the row because the artifact is one file per role.
type MatchupCount struct {
	Role       string `json:"role"`
	ChampionID int    `json:"champion_id"`
	OpponentID int    `json:"opponent_id"`
	N          int    `json:"n"`
	Wins       int    `json:"wins"`
}

// CellInput is everything the cell policy needs.
type CellInput struct {
	Counts []CellCount
	Bans   []BanCount

	// Matches is the number of distinct matches in the partition. It is the
	// denominator of pick_rate and ban_rate, so a wrong value here silently
	// rescales two published columns.
	Matches int

	MinCellN int
}

// CellOutput is the computed tier list plus the counts the gates audit.
type CellOutput struct {
	// Cells are the published cells, sorted for a stable artifact.
	Cells []aggmodel.Cell

	// ChampionsAscending is every champion id observed in the window,
	// ascending, including champions whose only cells were suppressed. The
	// manifest uses it as the prerender list, so it must not be the list of
	// published cells.
	ChampionsAscending []int

	// Total is every computable (champion, role) tally, before suppression;
	// Suppressed is how many of those fell below MinCellN.
	Total      int
	Suppressed int

	// SumN is the sum of n over every computable tally, published or not. It
	// is the left hand side of the reconciliation gate.
	SumN int

	// BaselineWinRate is the win rate of the whole window, which the tier
	// scorer grades against.
	BaselineWinRate float64
}

// ComputeCells applies the cell policy.
//
// The rules, in the order they are applied:
//
//   - a tally whose role is not one of the five canonical values is a hard
//     error, not a skip: the extraction already normalised roles, so an
//     unknown one means the pipeline disagrees with the contract.
//   - n is mandatory and the published rate is only meaningful when n > 0, so
//     a zero or negative tally is dropped (it cannot be counted as suppressed
//     either, because it is not a cell at all) while a positive tally below
//     min_cell_n is suppressed and counted.
//   - pick_rate is the share of pick slots used by the champion, which is
//     n/(2*matches) because one match fills two slots in the role (one per
//     team). ban_rate is distinct matches the champion was banned in over the
//     matches in the window.
func ComputeCells(input CellInput) (CellOutput, error) {
	if input.MinCellN < 1 {
		return CellOutput{}, fmt.Errorf("min_cell_n must be >= 1, got %d", input.MinCellN)
	}
	if input.Matches <= 0 {
		return CellOutput{}, fmt.Errorf("match count must be > 0 to compute rates, got %d", input.Matches)
	}

	bans := make(map[int]int, len(input.Bans))
	for _, ban := range input.Bans {
		bans[ban.ChampionID] += ban.Bans
	}

	totalWins := 0
	totalN := 0
	for _, count := range input.Counts {
		if count.N <= 0 {
			return CellOutput{}, fmt.Errorf("champion %d %s: tally with n=%d cannot form a cell", count.ChampionID, count.Role, count.N)
		}
		if count.Wins < 0 || count.Wins > count.N {
			return CellOutput{}, fmt.Errorf("champion %d %s: wins %d is outside [0,%d]", count.ChampionID, count.Role, count.Wins, count.N)
		}
		totalWins += count.Wins
		totalN += count.N
	}
	if totalN == 0 {
		return CellOutput{}, fmt.Errorf("no participant rows in the window")
	}
	baseline := float64(totalWins) / float64(totalN)

	out := CellOutput{
		Total:           len(input.Counts),
		SumN:            totalN,
		BaselineWinRate: baseline,
		Cells:           make([]aggmodel.Cell, 0, len(input.Counts)),
	}
	seen := make(map[int]struct{}, len(input.Counts))
	suppressed := 0

	for _, count := range input.Counts {
		role, ok := aggmodel.RoleFromRiotPosition(count.Role)
		if !ok {
			return CellOutput{}, fmt.Errorf("champion %d: %q is not a canonically normalised role", count.ChampionID, count.Role)
		}
		seen[count.ChampionID] = struct{}{}
		if count.N < input.MinCellN {
			suppressed++
			continue
		}
		rawRate := float64(count.Wins) / float64(count.N)
		out.Cells = append(out.Cells, aggmodel.Cell{
			ChampionID:    count.ChampionID,
			Role:          role,
			N:             count.N,
			Wins:          count.Wins,
			WinRate:       round4(rawRate),
			PickRate:      round4(float64(count.N) / float64(2*input.Matches)),
			BanRate:       round4(float64(bans[count.ChampionID]) / float64(input.Matches)),
			Tier:          tierForRate(baseline, rawRate),
			CI95HalfWidth: round4(ConfidenceHalfWidthFactor / math.Sqrt(float64(count.N))),
		})
	}

	out.Suppressed = suppressed
	sortCells(out.Cells)
	out.ChampionsAscending = sortedKeys(seen)
	return out, nil
}

// sortCells orders cells the way the contract publishes them: by role in the
// canonical role order, then by descending win rate, then by champion id so
// that two champions with an identical rate still have one deterministic
// order. A stable order keeps the artifact diffable, which is how a reviewer
// notices a change in the data rather than a change in the tool.
func sortCells(cells []aggmodel.Cell) {
	rank := make(map[aggmodel.Role]int, len(aggmodel.Roles))
	for i, role := range aggmodel.Roles {
		rank[role] = i
	}
	sort.SliceStable(cells, func(i, j int) bool {
		a, b := cells[i], cells[j]
		if rank[a.Role] != rank[b.Role] {
			return rank[a.Role] < rank[b.Role]
		}
		if a.WinRate != b.WinRate {
			return a.WinRate > b.WinRate
		}
		return a.ChampionID < b.ChampionID
	})
}

func sortedKeys(set map[int]struct{}) []int {
	out := make([]int, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Ints(out)
	return out
}

// round4 rounds to four decimals, the precision the contract example uses.
// Rounding here rather than at render time keeps the artifact bytes stable
// across runs, which the demo determinism test depends on.
func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}

// computeMatchupCells converts matchup tallies into the frozen shape, grouped
// by role because the contract publishes one matrix per role.
//
// A role with no pairs yields no key at all; the caller decides whether to emit
// an empty document for it.
func computeMatchupCells(counts []MatchupCount) map[aggmodel.Role][]aggmodel.MatchupCell {
	out := make(map[aggmodel.Role][]aggmodel.MatchupCell, len(aggmodel.Roles))
	for _, count := range counts {
		if count.N <= 0 {
			continue
		}
		role, ok := aggmodel.RoleFromRiotPosition(count.Role)
		if !ok {
			continue
		}
		rate := float64(count.Wins) / float64(count.N)
		out[role] = append(out[role], aggmodel.MatchupCell{
			ChampionID:    count.ChampionID,
			OpponentID:    count.OpponentID,
			N:             count.N,
			Wins:          count.Wins,
			WinRate:       round4(rate),
			CI95HalfWidth: round4(ConfidenceHalfWidthFactor / math.Sqrt(float64(count.N))),
		})
	}
	for role := range out {
		cells := out[role]
		sort.SliceStable(cells, func(i, j int) bool {
			if cells[i].ChampionID != cells[j].ChampionID {
				return cells[i].ChampionID < cells[j].ChampionID
			}
			return cells[i].OpponentID < cells[j].OpponentID
		})
		out[role] = cells
	}
	return out
}

// MaxBuildsPerRole caps how many item, rune or spell rows a champion page
// carries per role.
//
// The cap exists because the item grouping is the seven item slots: the number
// of distinct combinations grows with the sample size, and an uncapped list
// would make the champion artifact grow without bound while telling the reader
// less and less - a build seen twice is not a recommended build. Rows are
// ranked by sample size, so the cap keeps the ones with evidence behind them.
const MaxBuildsPerRole = 10

// computeBuilds groups item, rune or spell tallies into the frozen Build shape,
// keyed by champion and then role: a champion page shows one champion's builds
// for each of its roles, and the tier list shows none of them.
//
// kind must be one of the BuildKind values the contract documents; the label is
// the key rendered as a compact, stable string, which the frontend replaces with
// names from the static dataset when it has them.
func computeBuilds(kind string, counts []BuildCount) map[int]map[aggmodel.Role][]aggmodel.Build {
	out := map[int]map[aggmodel.Role][]aggmodel.Build{}
	for _, count := range counts {
		if count.N <= 0 {
			continue
		}
		role, ok := aggmodel.RoleFromRiotPosition(count.Role)
		if !ok {
			continue
		}
		key := make([]int, len(count.Key))
		copy(key, count.Key)
		if out[count.ChampionID] == nil {
			out[count.ChampionID] = map[aggmodel.Role][]aggmodel.Build{}
		}
		out[count.ChampionID][role] = append(out[count.ChampionID][role], aggmodel.Build{
			Kind:    kind,
			Key:     key,
			Label:   buildLabel(key),
			N:       count.N,
			Wins:    count.Wins,
			WinRate: round4(float64(count.Wins) / float64(count.N)),
		})
	}
	for championID := range out {
		for role := range out[championID] {
			builds := out[championID][role]
			sort.SliceStable(builds, func(i, j int) bool {
				if builds[i].N != builds[j].N {
					return builds[i].N > builds[j].N
				}
				return compareIntSlices(builds[i].Key, builds[j].Key) < 0
			})
			if len(builds) > MaxBuildsPerRole {
				builds = builds[:MaxBuildsPerRole]
			}
			out[championID][role] = builds
		}
	}
	return out
}

func buildLabel(key []int) string {
	label := ""
	for i, value := range key {
		if i > 0 {
			label += "-"
		}
		label += fmt.Sprintf("%d", value)
	}
	return label
}

func compareIntSlices(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
