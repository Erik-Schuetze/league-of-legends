package aggregate

import (
	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// This file assembles the frozen documents from the computed cells. It is
// deliberately a pure function of its inputs: no clock, no filesystem, no
// engine. Everything that could make two builds of the same data differ lives
// outside it, which is what makes the demo determinism test meaningful.

// BuildKind constants are the values Build.Kind may take.
const (
	BuildKindItems  = "items"
	BuildKindRunes  = "runes"
	BuildKindSpells = "spells"
)

// artifactInput is everything the artifact documents are assembled from.
type artifactInput struct {
	Envelope aggmodel.Envelope
	Cells    CellOutput

	// Builds are keyed by champion, then role, because a champion page shows
	// all of its roles and the tier list none of them.
	Items  map[int]map[aggmodel.Role][]aggmodel.Build
	Runes  map[int]map[aggmodel.Role][]aggmodel.Build
	Spells map[int]map[aggmodel.Role][]aggmodel.Build

	// Matchups is one ordered-pair list per role, because the contract
	// publishes one matrix per role and the role is not a field of MatchupCell.
	Matchups map[aggmodel.Role][]aggmodel.MatchupCell

	// ChampionSlugs maps champion id to URL slug, from the published static
	// dataset. An id with no entry gets an empty slug rather than a guessed
	// one: a wrong slug is a broken link that looks like a working one.
	ChampionSlugs map[int]string
}

// buildTierList assembles the artifact every tier-list page reads.
//
// The tier list carries only published cells. A suppressed cell is absent from
// the document by construction rather than present-with-a-flag, because the
// contract's rule is that a rate never travels without its sample size and a
// suppressed cell has no publishable sample size.
func buildTierList(input artifactInput) aggmodel.TierList {
	cells := input.Cells.Cells
	if cells == nil {
		cells = []aggmodel.Cell{}
	}
	return aggmodel.TierList{Envelope: input.Envelope, Cells: cells}
}

// buildChampions assembles one document per champion that appeared in the
// window, including champions whose cells were all suppressed.
//
// A champion with no published cell still gets a document, with an empty roles
// list: the manifest lists it for prerendering, and a page that resolves to 404
// because the champion was merely rare would be a worse answer than a page that
// says the sample is too thin. The envelope's suppressed_cells is what tells the
// reader that cells were dropped.
func buildChampions(input artifactInput) map[int]aggmodel.Champion {
	published := make(map[int][]aggmodel.Cell, len(input.Cells.Cells))
	for _, cell := range input.Cells.Cells {
		published[cell.ChampionID] = append(published[cell.ChampionID], cell)
	}

	out := make(map[int]aggmodel.Champion, len(input.Cells.ChampionsAscending))
	for _, championID := range input.Cells.ChampionsAscending {
		champion := aggmodel.Champion{
			Envelope:     input.Envelope,
			ChampionID:   championID,
			ChampionSlug: input.ChampionSlugs[championID],
			Roles:        []aggmodel.ChampionRole{},
		}
		for _, role := range aggmodel.Roles {
			cell, ok := cellForRole(published[championID], role)
			if !ok {
				continue
			}
			champion.Roles = append(champion.Roles, aggmodel.ChampionRole{
				Role:   role,
				Stats:  cell,
				Items:  buildsOrEmpty(input.Items[championID][role]),
				Runes:  buildsOrEmpty(input.Runes[championID][role]),
				Spells: buildsOrEmpty(input.Spells[championID][role]),

				// Skill orders need timelines, which this build never reads.
				// The field stays an empty slice so the frontend omits the
				// section instead of rendering an empty one.
				SkillOrders: []aggmodel.SkillOrder{},
			})
		}
		out[championID] = champion
	}
	return out
}

func cellForRole(cells []aggmodel.Cell, role aggmodel.Role) (aggmodel.Cell, bool) {
	for _, cell := range cells {
		if cell.Role == role {
			return cell, true
		}
	}
	return aggmodel.Cell{}, false
}

func buildsOrEmpty(builds []aggmodel.Build) []aggmodel.Build {
	if builds == nil {
		return []aggmodel.Build{}
	}
	return builds
}

// buildMatchups assembles the document for one role.
//
// The axis is every champion that appeared in any pair for the role, including
// champions whose pairs were all suppressed, so that the heatmap keeps a stable
// order between builds. Cells below the confidence floor are dropped for the
// same reason tier-list cells are: the artifact is only allowed to carry a rate
// with a sample size behind it.
//
// It is called for all five roles even when a role has no pairs at all. An empty
// matrix is an honest artifact, and always writing the file means the
// prerendered route for a role can never point at a missing path.
func buildMatchups(input artifactInput, role aggmodel.Role, minCellN int) aggmodel.Matchups {
	doc := aggmodel.Matchups{
		Envelope:  input.Envelope,
		Role:      role,
		Champions: []int{},
		Cells:     []aggmodel.MatchupCell{},
	}
	seen := map[int]struct{}{}
	for _, cell := range input.Matchups[role] {
		if cell.ChampionID == 0 || cell.OpponentID == 0 {
			continue
		}
		seen[cell.ChampionID] = struct{}{}
		seen[cell.OpponentID] = struct{}{}
		if cell.N < minCellN {
			continue
		}
		doc.Cells = append(doc.Cells, cell)
	}
	doc.Champions = sortedKeys(seen)
	return doc
}
