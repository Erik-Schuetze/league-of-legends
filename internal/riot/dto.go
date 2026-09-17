// Package riot holds the Riot API data transfer objects v1 depends on.
//
// The types in this file are a frozen contract: the raw archive stores the
// verbatim payload, but everything derived from a payload is computed through
// these fields, so renaming or retyping one is an ADR, not a refactor. Additive
// changes are safe - encoding/json ignores fields it is not told about, and a
// field added here simply becomes readable.
//
// Only fields v1 actually uses appear. Everything else stays in the raw
// archive, where it can be added to a transform later without a re-crawl.
package riot

// MatchDTO is the subset of MATCH-V5 GET /lol/match/v5/matches/{matchId} that
// v1 reads: identity, queue and patch, one row per participant, and the draft's
// bans.
type MatchDTO struct {
	Metadata MatchMetadata `json:"metadata"`
	Info     MatchInfo     `json:"info"`

	// raw is the response body this value was decoded from. It is unexported
	// because no caller reads it as a field: RawPayload is the accessor, and
	// the archive writer uses that, so a value built by hand in a test is
	// still writable.
	raw []byte
}

// MatchMetadata carries the match identity and the participant puuids. The
// puuids are how the crawler walks the graph: every match it fetches yields
// nine more players to consider for the frontier, which is what makes the
// crawl breadth-first rather than a fixed sample.
type MatchMetadata struct {
	MatchID      string   `json:"matchId"`
	Participants []string `json:"participants"`
}

// MatchInfo is the match-level facts the control plane and the aggregation
// both need.
type MatchInfo struct {
	// Epoch milliseconds, Riot's own shape.
	GameCreation int64 `json:"gameCreation"`
	// Seconds. Riot changed the unit of this field once already, which is
	// why the unit is named in the Go field rather than left implicit.
	GameDuration int64              `json:"gameDuration"`
	GameVersion  string             `json:"gameVersion"`
	QueueID      int                `json:"queueId"`
	Participants []MatchParticipant `json:"participants"`
	Teams        []MatchTeam        `json:"teams"`
}

// MatchTeam carries the team id and its bans. Ban rate is computed from these
// and from pick rate, so a payload without them cannot produce a tier list.
type MatchTeam struct {
	TeamID int        `json:"teamId"`
	Bans   []MatchBan `json:"bans"`
}

// MatchBan is one ban entry. ChampionID 0 means the team did not ban, which
// happens in modes with fewer bans and is why the aggregation must filter on
// zero rather than counting every entry.
type MatchBan struct {
	ChampionID int `json:"championId"`
	PickTurn   int `json:"pickTurn"`
}

// MatchParticipant is one player's line in the match summary.
type MatchParticipant struct {
	PUUID              string `json:"puuid"`
	ChampionID         int    `json:"championId"`
	ChampionName       string `json:"championName"`
	TeamID             int    `json:"teamId"`
	TeamPosition       string `json:"teamPosition"`
	IndividualPosition string `json:"individualPosition"`
	Win                bool   `json:"win"`

	// Six item slots plus the trinket. Item 0 in a slot means empty, which
	// the aggregation drops rather than rendering as an item with id 0.
	Item0 int `json:"item0"`
	Item1 int `json:"item1"`
	Item2 int `json:"item2"`
	Item3 int `json:"item3"`
	Item4 int `json:"item4"`
	Item5 int `json:"item5"`
	Item6 int `json:"item6"`

	Summoner1ID int        `json:"summoner1Id"`
	Summoner2ID int        `json:"summoner2Id"`
	Perks       MatchPerks `json:"perks"`
}

// MatchPerks is the rune page a participant played.
type MatchPerks struct {
	// The order Riot returns styles in is the rune page's own order, so the
	// aggregation reads position rather than trusting Description.
	Styles []MatchPerkStyle `json:"styles"`
}

// MatchPerkStyle is one rune tree on the page. Style is the tree id; for the
// stat-shard pseudo-tree Riot returns a style entry with no tree id of its own.
type MatchPerkStyle struct {
	Description string               `json:"description"`
	Style       int                  `json:"style"`
	Selections  []MatchPerkSelection `json:"selections"`
}

// MatchPerkSelection is one chosen rune or shard. The perk id is the identity;
// the var1/var2/var3 values Riot also returns are stat-dependent and are not
// read, which is why they are not modelled here.
type MatchPerkSelection struct {
	Perk int `json:"perk"`
}

// LeagueEntryDTO is one entry of LEAGUE-V4
// GET /lol/league/v4/entries/{queue}/{tier}/{division}. These are the crawl
// seeds: each entry names a puuid and the rank it was observed at, which is the
// only rank attribution this project has and the reason per-rank pages carry a
// snapshot caveat.
type LeagueEntryDTO struct {
	PUUID        string `json:"puuid"`
	QueueType    string `json:"queueType"`
	Tier         string `json:"tier"`
	Rank         string `json:"rank"`
	LeaguePoints int    `json:"leaguePoints"`

	// raw is the entry's own bytes from the ladder response, retained for the
	// same reason as MatchDTO.raw: seeding archives whole pages verbatim.
	raw []byte
}

// AccountDTO is the ACCOUNT-V1 GET /riot/account/v1/accounts/by-riot-id
// response: the identity step that turns a name a human typed into the puuid
// the crawl is keyed on.
//
// Only the puuid is load-bearing. gameName and tagLine are kept because an
// operator reading a log line needs to know which account a puuid was, and
// because composing them back from nothing is impossible.
type AccountDTO struct {
	PUUID    string `json:"puuid"`
	GameName string `json:"gameName"`
	TagLine  string `json:"tagLine"`
}

// ApexLeagueDTO is the LEAGUE-V4 apex shape: challengerleagues, grandmasterleagues
// and masterleagues by queue return the whole league in one response, because
// those tiers have no divisions to page through.
//
// The entries reuse LeagueEntryDTO rather than a separate item type: the apex
// list omits the tier and rank on each entry, and the client fills them in from
// the request, which keeps one frontier row shape for both seeding paths.
type ApexLeagueDTO struct {
	Tier    string           `json:"tier"`
	Queue   string           `json:"queue"`
	Name    string           `json:"name"`
	Entries []LeagueEntryDTO `json:"entries"`
}
