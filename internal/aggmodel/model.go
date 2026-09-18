// Package aggmodel is the frozen shape of everything the aggregate build
// publishes and a reader of `agg/v1` reads.
//
// It is deliberately free of computation: it holds the artifact types, the
// path builders that put them on disk, and the schema emitter the published
// declarations are generated from. It used to be shared with the presentation
// tier - producers (internal/aggregate) and consumers (web/, then
// internal/webtier) both worked from this package - so a shape change broke the
// build in one place instead of silently producing blanks in a browser. That
// tier was deleted on 2026-09-18 (docs/decisions/ADR-011-retire-the-web-tier.md)
// and this package is now producer-only, but the property it bought is still
// worth keeping: the artifact types are the artifact contract.
//
// Changing a JSON tag, an enum value or a path template here is a contract
// change and requires an ADR. See docs/contracts.md.
package aggmodel

import "time"

// SchemaVersion is the value of the `schema` field every artifact carries.
// It moves only when a change is not backward compatible for a reader that
// already understands the previous version.
const SchemaVersion = 1

// QueueIDRankedSolo5x5 is the only queue v1 publishes.
const QueueIDRankedSolo5x5 = 420

// Role is the canonical position. Riot's MATCH-V5 payload uses a different
// spelling for two of the five (MIDDLE, UTILITY); RoleFromRiotPosition is the
// single place that normalisation happens, so no producer has to remember it.
type Role string

const (
	RoleTop     Role = "TOP"
	RoleJungle  Role = "JUNGLE"
	RoleMid     Role = "MID"
	RoleBottom  Role = "BOTTOM"
	RoleSupport Role = "SUPPORT"
)

// Roles is the canonical order. It is also the order pages render in, so a
// producer iterating this slice gets a stable axis for free.
var Roles = []Role{RoleTop, RoleJungle, RoleMid, RoleBottom, RoleSupport}

// roleSlugs is the URL segment for each role. The slugs are shorter than the
// enum values on purpose: /tier-list/mid reads as a URL, /tier-list/MID does
// not. They are frozen here because they appear in the route table.
var roleSlugs = map[Role]string{
	RoleTop:     "top",
	RoleJungle:  "jungle",
	RoleMid:     "mid",
	RoleBottom:  "bottom",
	RoleSupport: "support",
}

// Slug returns the URL segment for the role, or "" if the role is unknown.
func (r Role) Slug() string { return roleSlugs[r] }

// Valid reports whether the role is one of the five canonical values.
func (r Role) Valid() bool { _, ok := roleSlugs[r]; return ok }

// RoleFromSlug is the inverse of Slug, so a route can resolve /tier-list/mid.
func RoleFromSlug(slug string) (Role, bool) {
	for role, s := range roleSlugs {
		if s == slug {
			return role, true
		}
	}
	return "", false
}

// RoleFromRiotPosition maps a MATCH-V5 position value to a canonical role.
// It accepts both teamPosition and individualPosition spellings, because
// which one is populated varies by queue and by payload version.
func RoleFromRiotPosition(position string) (Role, bool) {
	switch position {
	case "TOP":
		return RoleTop, true
	case "JUNGLE":
		return RoleJungle, true
	case "MIDDLE", "MID":
		return RoleMid, true
	case "BOTTOM", "ADC":
		return RoleBottom, true
	case "UTILITY", "SUPPORT":
		return RoleSupport, true
	default:
		return "", false
	}
}

// Tier is a letter grade. The set is closed and ordered: Tiers is best to
// worst. The thresholds that turn a win rate into a grade are the aggregation
// engineer's to choose and to document; what is frozen here is the vocabulary
// a reader styles, and that the label always accompanies any colour so
// the grade does not depend on hue.
type Tier string

const (
	TierSPlus Tier = "S+"
	TierS     Tier = "S"
	TierA     Tier = "A"
	TierB     Tier = "B"
	TierC     Tier = "C"
	TierD     Tier = "D"
)

// Tiers is ordered best to worst.
var Tiers = []Tier{TierSPlus, TierS, TierA, TierB, TierC, TierD}

// Valid reports whether the tier is one of the six published grades.
func (t Tier) Valid() bool {
	for _, known := range Tiers {
		if t == known {
			return true
		}
	}
	return false
}

// Bracket is a rank segment. v1 publishes only BracketAll; every other value
// is reserved so that adding segmentation later is an additive path change
// rather than a URL break.
type Bracket string

const (
	BracketAll Bracket = "all"

	// Reserved for later phases. Not emitted by a v1 build.
	BracketEmeraldPlus  Bracket = "emerald_plus"
	BracketPlatinumPlus Bracket = "platinum_plus"
	BracketDiamondPlus  Bracket = "diamond_plus"
	BracketMasterPlus   Bracket = "master_plus"
)

// Source names where an artifact's numbers came from. It is the one field that
// makes a simulated artifact impossible to mistake for a real one: the demo
// subcommand exists only because there is no Riot API key in this environment,
// and a reader must be able to tell the difference from the artifact alone
// without trusting a filename or a directory.
//
// Real builds always publish SourceRiotMatchV5. See
// docs/decisions/ADR-005-demo-data-provenance.md.
type Source string

const (
	// SourceRiotMatchV5 is a build computed from the immutable raw archive of
	// MATCH-V5 match summaries.
	SourceRiotMatchV5 Source = "riot-match-v5"

	// SourceDemo is a deterministic, simulated dataset written by
	// `lolstats-aggregate demo`. It is never produced by `build`.
	SourceDemo Source = "demo"
)

// Valid reports whether the source is one the contract knows.
func (s Source) Valid() bool { return s == SourceRiotMatchV5 || s == SourceDemo }

// Envelope is the provenance every artifact carries. Rule 1 of the contract
// is that a rate never travels without its sample size, and rule 2 is that a
// thin patch is visible to the operator before it is visible to a user.
type Envelope struct {
	Schema          int       `json:"schema"`
	Source          Source    `json:"source"`
	Patch           string    `json:"patch"`
	Region          string    `json:"region"`
	Queue           int       `json:"queue"`
	Bracket         Bracket   `json:"bracket"`
	GeneratedAt     time.Time `json:"generated_at"`
	SourceWindow    Window    `json:"source_window"`
	MinCellN        int       `json:"min_cell_n"`
	SuppressedCells int       `json:"suppressed_cells"`
}

// Window is the inclusive date range the artifact was computed from, in
// 2006-01-02 form. It is the only honest way to say how stale a snapshot is.
type Window struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Cell is one (champion, role) aggregate. N is mandatory: a win rate without
// its sample size does not get published.
type Cell struct {
	ChampionID    int     `json:"champion_id"`
	Role          Role    `json:"role"`
	N             int     `json:"n"`
	Wins          int     `json:"wins"`
	WinRate       float64 `json:"win_rate"`
	PickRate      float64 `json:"pick_rate"`
	BanRate       float64 `json:"ban_rate"`
	Tier          Tier    `json:"tier"`
	CI95HalfWidth float64 `json:"ci95_half_width"`
}

// TierList is agg/v1/p/<patch>/<region>/<queue>/<bracket>/tierlist.json: one
// row per (champion, role) in the partition, including champions with no
// games in the window so a reader does not have to distinguish "absent"
// from "silent".
type TierList struct {
	Envelope
	Cells []Cell `json:"cells"`
}

// Champion is agg/v1/.../champions/<champion_id>.json: everything the champion
// detail page needs, for every role the champion was played in. One file per
// champion rather than one per (champion, role) keeps the page's role switch
// a client-side slice of data it already has.
type Champion struct {
	Envelope
	ChampionID   int            `json:"champion_id"`
	ChampionSlug string         `json:"champion_slug"`
	Roles        []ChampionRole `json:"roles"`
}

// ChampionRole is the champion's page for a single role.
type ChampionRole struct {
	Role   Role    `json:"role"`
	Stats  Cell    `json:"stats"`
	Items  []Build `json:"items"`
	Runes  []Build `json:"runes"`
	Spells []Build `json:"spells"`

	// Empty unless timelines are retained. The champion page omits the
	// section rather than rendering an empty one, which is why this is a
	// slice and not a struct.
	SkillOrders []SkillOrder `json:"skill_orders"`
}

// Build is one row of an item, rune or summoner-spell list. The three share a
// shape so one reader component renders all of them; the key is opaque to the
// component and is rendered through the static dataset's id-to-name map.
type Build struct {
	// Kind tells the component which static dataset resolves Key, and what
	// the icons mean: "items" is ordered and may contain 0 for an empty slot;
	// "runes" is primary tree, keystone, secondary tree, then shards;
	// "spells" is the two summoner spell ids in pick order.
	Kind    string  `json:"kind"`
	Key     []int   `json:"key"`
	Label   string  `json:"label"`
	N       int     `json:"n"`
	Wins    int     `json:"wins"`
	WinRate float64 `json:"win_rate"`
}

// SkillOrder is an ability leveling order, e.g. "Q>E>W". Only present when
// timelines are retained.
type SkillOrder struct {
	Order   string  `json:"order"`
	N       int     `json:"n"`
	Wins    int     `json:"wins"`
	WinRate float64 `json:"win_rate"`
}

// Matchups is agg/v1/.../matchups/<role>.json: the pairwise matrix for one
// role. Champions is the axis, sorted ascending, so the heatmap has a stable
// order even for pairs that were suppressed.
type Matchups struct {
	Envelope
	Role      Role          `json:"role"`
	Champions []int         `json:"champions"`
	Cells     []MatchupCell `json:"cells"`
}

// MatchupCell is one ordered pair. Only one direction is emitted - the pair
// with the lower champion id first - and the reader is responsible for
// mirroring it, which halves the artifact and removes any chance of the two
// directions disagreeing.
type MatchupCell struct {
	ChampionID    int     `json:"champion_id"`
	OpponentID    int     `json:"opponent_id"`
	N             int     `json:"n"`
	Wins          int     `json:"wins"`
	WinRate       float64 `json:"win_rate"`
	CI95HalfWidth float64 `json:"ci95_half_width"`
}

// Manifest is agg/v1/manifest.json. It is what the site build and the landing
// page read to decide which patches exist and how thin each one is.
type Manifest struct {
	Schema      int         `json:"schema"`
	Source      Source      `json:"source"`
	GeneratedAt time.Time   `json:"generated_at"`
	Latest      Partition   `json:"latest"`
	Partitions  []Partition `json:"partitions"`
}

// Partition describes one published aggregate. SuppressedCells and
// CellsPublished are here rather than only in build_runs so that the site can
// disclose a thin patch without querying Postgres, which it never does.
type Partition struct {
	Patch           string    `json:"patch"`
	Region          string    `json:"region"`
	Queue           int       `json:"queue"`
	Bracket         Bracket   `json:"bracket"`
	GeneratedAt     time.Time `json:"generated_at"`
	SourceWindow    Window    `json:"source_window"`
	MinCellN        int       `json:"min_cell_n"`
	SuppressedCells int       `json:"suppressed_cells"`
	CellsPublished  int       `json:"cells_published"`
	BuildRunID      int64     `json:"build_run_id"`
	GitSHA          string    `json:"git_sha"`

	// Champions is every champion id present in tierlist.json, ascending.
	// The champion page is prerendered from this list, so it must include
	// champions whose cells were suppressed.
	Champions []int `json:"champions"`

	// MatchupRoles is the roles that have a matchups artifact.
	MatchupRoles []Role `json:"matchup_roles"`
}

// The static dataset is a thin projection of Data Dragon, not a copy of it.
// It exists so a page can turn an id into a name and an icon path without a
// request to Riot at render time. Fields Riot does not publish in the
// permitted static data - champion art, splash art, marks - are deliberately
// absent, and no game lore or tooltip text is carried.

// StaticChampions is agg/v1/static/<ddragon_version>/champions.json.
type StaticChampions struct {
	DDragonVersion string           `json:"ddragon_version"`
	Champions      []StaticChampion `json:"champions"`
}

// StaticChampion is one champion's identity.
type StaticChampion struct {
	ID    int    `json:"id"`
	Key   string `json:"key"`
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Roles []Role `json:"roles"`
}

// StaticItems is agg/v1/static/<ddragon_version>/items.json.
type StaticItems struct {
	DDragonVersion string       `json:"ddragon_version"`
	Items          []StaticItem `json:"items"`
}

// StaticItem is one item. Names and icons only: no descriptions, because item
// text is Riot copy and there is no need to republish it.
type StaticItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"`
}

// StaticRunes is agg/v1/static/<ddragon_version>/runes.json.
type StaticRunes struct {
	DDragonVersion string       `json:"ddragon_version"`
	Runes          []StaticRune `json:"runes"`
}

// StaticRune is one keystone or minor rune. Tree is the path id it belongs to.
type StaticRune struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"`
	Tree int    `json:"tree"`
}

// StaticSummonerSpells is agg/v1/static/<ddragon_version>/summoner-spells.json.
type StaticSummonerSpells struct {
	DDragonVersion string                `json:"ddragon_version"`
	Spells         []StaticSummonerSpell `json:"spells"`
}

// StaticSummonerSpell is one summoner spell.
type StaticSummonerSpell struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"`
}

// StaticPatches is agg/v1/static/<ddragon_version>/patches.json. The site uses
// it to render the patch switcher and to label which patch a page describes.
type StaticPatches struct {
	DDragonVersion string   `json:"ddragon_version"`
	Latest         string   `json:"latest"`
	Patches        []string `json:"patches"`
}
