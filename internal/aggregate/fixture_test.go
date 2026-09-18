package aggregate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The aggregate fixture: a small, hand-designed raw archive plus the numbers a
// human computed from it by hand.
//
// It exists because no Riot data may be committed and because a build step that
// publishes statistics has to be checked against arithmetic someone did with a
// pencil, not against its own output. Every match below is written out here, so
// the expected cell table in build_fixture_test.go can be read next to the
// payloads that produce it.
//
// The archive under fixtures/agg/raw is JSONL: readable, diffable and
// hand-editable. The parquet parts the build actually reads are derived from it
// by the pinned DuckDB client at test time (see materialiseArchive), so a
// checked-in binary part can never drift away from the payloads that explain
// it, and the test that exercises the conversion is also the test that proves
// the client version the docs pin is the one in use.
//
// The design is chosen to make every gate and every metric boundary observable:
//
//   - nine matches in the window on the selected patch, plus one match for each
//     negative case: another patch, another region, another queue, one match
//     played before the window and one after it;
//   - two champions played once in the top lane, so those cells land below
//     min_cell_n and must be suppressed and counted rather than published;
//   - one champion played exactly twice in the top lane, so a published cell
//     sits exactly on the floor: min_cell_n publishes and min_cell_n-1 does not,
//     which is the boundary a reader of suppressed_cells has to be able to
//     trust;
//   - a team position that is empty, so the individualPosition fallback is
//     exercised on real input rather than only in a unit test;
//   - item slots that are all zero for one participant, so the item group is
//     smaller than the cell it belongs to;
//   - a rune page that is not in the documented order, so the rune key is
//     refused rather than guessed;
//   - a summoner spell id of zero, so the spell group is smaller than its cell.
//
// The bans are drawn from champions that do not play in the match that bans
// them, which is what a real ban phase guarantees. That constraint is also what
// makes ban_rate testable: a champion can only be banned in a match it did not
// play, so a champion whose cell is published needs a match it sat out.

// fixtureRoles is the canonical role order the fixture uses for the five
// participants of each team.
var fixtureRoles = [5]string{"TOP", "JUNGLE", "MID", "BOTTOM", "SUPPORT"}

// The two team compositions. The ids are real champion ids, because inventing
// them would test nothing (see fixtures/README.md).
var (
	fixtureTeam100Champions = [5]int{24, 64, 134, 202, 111}
	fixtureTeam200Champions = [5]int{86, 121, 1, 145, 412}
)

// fixtureParticipantIndex addresses one participant by team and role index.
type fixtureParticipantIndex [2]int

// fixtureMatch is one hand-authored match in the fixture archive.
type fixtureMatch struct {
	id        string
	partition string // the dt= partition it was crawled into
	created   string // RFC3339 UTC, i.e. info.gameCreation
	patch     string
	platform  string
	queue     int
	winner    int
	// champions overrides the default composition per team when non-zero.
	champions map[int][5]int
	bans      map[int][]int
	// noItems names participants whose seven item slots are all zero.
	noItems []fixtureParticipantIndex
	// noSpells names participants whose second summoner spell is zero.
	noSpells []fixtureParticipantIndex
	// badRunes names participants whose rune page is out of documented order.
	badRunes []fixtureParticipantIndex
	// bare writes a payload with no envelope at all, which is the corruption
	// the fail-closed test feeds in.
	bare bool
	// duration overrides info.gameDuration in seconds. Zero means the default
	// 1830, which is the value every nightly expectation was computed against,
	// so only a fixture that needs a short game sets it.
	duration int
	// endResult overrides info.endOfGameResult. Empty means GameComplete, which
	// every nightly fixture carries; the timeline dataset is the only consumer
	// that reads the field, for its ledger.
	endResult string
}

// endResultOf is info.endOfGameResult, defaulted the way durationOf defaults
// the duration.
func (m fixtureMatch) endResultOf() string {
	if m.endResult == "" {
		return "GameComplete"
	}
	return m.endResult
}

func (m fixtureMatch) durationOf() int {
	if m.duration > 0 {
		return m.duration
	}
	return 1830
}

func (m fixtureMatch) championsOf(team int) [5]int {
	if override, ok := m.champions[team]; ok {
		return override
	}
	if team == 100 {
		return fixtureTeam100Champions
	}
	return fixtureTeam200Champions
}

func (m fixtureMatch) bansOf(team int) []int {
	if m.bans == nil {
		return nil
	}
	return m.bans[team]
}

// fixturePuuids is the participant identity set, shared by every match so the
// fixture is ten people playing nine games rather than ninety strangers.
var fixturePuuids = [10]string{
	"fixture-puuid-01", "fixture-puuid-02", "fixture-puuid-03", "fixture-puuid-04",
	"fixture-puuid-05", "fixture-puuid-06", "fixture-puuid-07", "fixture-puuid-08",
	"fixture-puuid-09", "fixture-puuid-10",
}

// fixtureWindowEnd is the last day of the window every expectation in this
// package is computed for. It is pinned rather than derived from the archive so
// that a change in the archive's newest partition cannot silently move the
// window under the hand-computed table.
const fixtureWindowEnd = "2026-09-14"

// fixtureWindowDays is the trailing window length the expectations assume.
const fixtureWindowDays = 14

// fixtureMinCellN is deliberately tiny: the fixture is nine matches, and a
// cell of two is the smallest sample that can distinguish "suppressed just
// below the floor" from "published just at it".
const fixtureMinCellN = 2

// fixtureMatches is the valid archive, in the order it is written out.
//
// The counted matches are F01..F08 and F13: nine matches on patch 16.18 in
// EUW1/420 inside the window. F09 is the same region, queue and window on patch
// 16.17, F10 was played before the window opened, F14 after it closed, F11 was
// played on NA1 and F12 in queue 440.
//
// Three of the counted matches displace the team 100 top laner, which is what
// gives the fixture a suppressed cell, a cell exactly on the floor, and a
// champion that other matches are free to ban.
func fixtureMatches() []fixtureMatch {
	return []fixtureMatch{
		{
			id: "EUW1_0000000001", partition: "2026-09-10", created: "2026-09-09T18:05:00Z",
			patch: "16.18", platform: "EUW1", queue: 420, winner: 100,
			bans: map[int][]int{100: {11}, 200: {2}},
		},
		{
			id: "EUW1_0000000002", partition: "2026-09-10", created: "2026-09-09T19:20:00Z",
			patch: "16.18", platform: "EUW1", queue: 420, winner: 100,
			// A zero ban id means "no ban" in a queue with fewer ban slots. It
			// must never reach ban_rate.
			bans: map[int][]int{100: {3, 0}, 200: {5, 0}},
		},
		{
			id: "EUW1_0000000003", partition: "2026-09-10", created: "2026-09-10T17:45:00Z",
			patch: "16.18", platform: "EUW1", queue: 420, winner: 100,
			bans: map[int][]int{100: {2, 3}, 200: {4, 5}},
		},
		{
			id: "EUW1_0000000004", partition: "2026-09-10", created: "2026-09-10T20:10:00Z",
			patch: "16.18", platform: "EUW1", queue: 420, winner: 100,
			// Champion 11 is played once, in the top lane, so the fixture has a
			// cell of one: below the floor, suppressed, counted. Champion 24
			// sits this match out, which is why it can be banned here.
			champions: map[int][5]int{100: {11, 64, 134, 202, 111}},
			bans:      map[int][]int{100: {24}, 200: {3}},
			// The bottom laner bought nothing, so the item group for that cell
			// is smaller than the cell.
			noItems: []fixtureParticipantIndex{{100, 3}},
		},
		{
			id: "EUW1_0000000005", partition: "2026-09-10", created: "2026-09-10T21:30:00Z",
			patch: "16.18", platform: "EUW1", queue: 420, winner: 100,
			bans: map[int][]int{100: {2}, 200: {5}},
			// A rune page in another order: no rune build may be published for
			// this participant.
			badRunes: []fixtureParticipantIndex{{100, 0}},
		},
		{
			id: "EUW1_0000000006", partition: "2026-09-14", created: "2026-09-13T18:00:00Z",
			patch: "16.18", platform: "EUW1", queue: 420, winner: 200,
			// Champion 13 is played in exactly these two matches, which lands
			// its cell exactly on min_cell_n and makes both matches losses.
			champions: map[int][5]int{100: {13, 64, 134, 202, 111}},
			bans:      map[int][]int{100: {24, 2}, 200: {3}},
		},
		{
			id: "EUW1_0000000007", partition: "2026-09-14", created: "2026-09-13T19:15:00Z",
			patch: "16.18", platform: "EUW1", queue: 420, winner: 200,
			champions: map[int][5]int{100: {13, 64, 134, 202, 111}},
			bans:      map[int][]int{100: {24}, 200: {}},
			// No second summoner spell, so the spell group for that cell is
			// smaller than the cell.
			noSpells: []fixtureParticipantIndex{{200, 1}},
		},
		{
			id: "EUW1_0000000008", partition: "2026-09-14", created: "2026-09-13T20:40:00Z",
			patch: "16.18", platform: "EUW1", queue: 420, winner: 200,
			// Champion 12 is played once, in the top lane of the team that
			// carries its position in individualPosition only: the second
			// suppressed cell, and the one the role fallback has to place.
			champions: map[int][5]int{200: {12, 121, 1, 145, 412}},
			bans:      map[int][]int{100: {86}, 200: {2}},
		},
		{
			id: "EUW1_0000000009", partition: "2026-09-14", created: "2026-09-13T21:05:00Z",
			// The previous patch, inside the window: it must be counted in the
			// window but must not reach the published patch. Champion 99 plays
			// only here, so a stale patch filter shows up as an extra champion
			// rather than only as a wrong match count.
			patch: "16.17", platform: "EUW1", queue: 420, winner: 100,
			champions: map[int][5]int{100: {99, 64, 134, 202, 111}},
			bans:      map[int][]int{100: {2}, 200: {3}},
		},
		{
			id: "EUW1_0000000010", partition: "2026-09-14", created: "2026-08-30T18:30:00Z",
			// Crawled inside the window, played before it: the window filter is
			// on gameCreation, not on the partition.
			patch: "16.18", platform: "EUW1", queue: 420, winner: 100,
			bans: map[int][]int{100: {2}, 200: {3}},
		},
		{
			id: "EUW1_0000000011", partition: "2026-09-14", created: "2026-09-12T18:00:00Z",
			// Champion 97 plays only on the other platform, so a region filter
			// that matched the wrong spelling of the platform would publish it.
			patch: "16.18", platform: "NA1", queue: 420, winner: 100,
			champions: map[int][5]int{100: {97, 64, 134, 202, 111}},
			bans:      map[int][]int{100: {2}, 200: {3}},
		},
		{
			id: "EUW1_0000000012", partition: "2026-09-14", created: "2026-09-12T18:30:00Z",
			// Champion 96 plays only in the other queue.
			patch: "16.18", platform: "EUW1", queue: 440, winner: 100,
			champions: map[int][5]int{100: {96, 64, 134, 202, 111}},
			bans:      map[int][]int{100: {2}, 200: {3}},
		},
		{
			id: "EUW1_0000000013", partition: "2026-09-14", created: "2026-09-14T23:59:00Z",
			// The last minute of the window end date: inside.
			patch: "16.18", platform: "EUW1", queue: 420, winner: 200,
			bans: map[int][]int{100: {12}, 200: {6}},
		},
		{
			id: "EUW1_0000000014", partition: "2026-09-15", created: "2026-09-15T00:01:00Z",
			// The first minute after it: outside. Champion 98 plays only here.
			patch: "16.18", platform: "EUW1", queue: 420, winner: 100,
			champions: map[int][5]int{100: {98, 64, 134, 202, 111}},
			bans:      map[int][]int{100: {2}, 200: {3}},
		},
	}
}

// fixtureCorruptMatches is the fail-closed archive: it is the valid archive
// with one extra match whose payload is a valid JSON document carrying no
// envelope at all. It is not a parse error, which is the point: the row is
// readable, so only the gate can catch it.
func fixtureCorruptMatches() []fixtureMatch {
	out := fixtureMatches()[:2]
	return append(out, fixtureMatch{
		id: "EUW1_0000000099", partition: "2026-09-10", created: "2026-09-10T22:00:00Z",
		patch: "16.18", platform: "EUW1", queue: 420, winner: 100, bare: true,
	})
}

// runePage renders one perks.styles array.
//
// ok is the documented shape: primary tree, secondary tree, stat shards, with
// the stat shards in the third entry. broken returns the same trees in another
// order, which is a payload the build must refuse rather than interpret.
func runePage(ok bool) string {
	primary := `{"description":"primaryStyle","style":8000,"selections":[{"perk":8005},{"perk":9111},{"perk":9105},{"perk":8014}]}`
	secondary := `{"description":"subStyle","style":8100,"selections":[{"perk":8139},{"perk":8126}]}`
	shards := `{"description":"statMods","style":5000,"selections":[{"perk":5008},{"perk":5008},{"perk":5002}]}`
	if ok {
		return "[" + primary + "," + secondary + "," + shards + "]"
	}
	return "[" + shards + "," + secondary + "," + primary + "]"
}

// renderMatch renders one match as the JSON document a MATCH-V5 response body
// would carry. Every field the build reads is present, plus the identity fields
// that make the payload look like what it stands in for.
func renderMatch(m fixtureMatch) string {
	if m.bare {
		return "{}"
	}
	var b strings.Builder
	created, err := time.Parse(time.RFC3339, m.created)
	if err != nil {
		panic("fixture match " + m.id + ": " + err.Error())
	}
	fmt.Fprintf(&b, `{"metadata":{"matchId":%q,"participants":[`, m.id)
	for i, puuid := range fixturePuuids {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%q", puuid)
	}
	fmt.Fprintf(&b, `]},"info":{"gameCreation":%d,"gameDuration":%d,"gameMode":"CLASSIC",`+
		`"gameVersion":%q,"mapId":11,"platformId":%q,"queueId":%d,"gameType":"MATCHED_GAME",`+
		`"endOfGameResult":%q,"teams":[`,
		created.UnixMilli(), m.durationOf(), m.patch+".612.9234", m.platform, m.queue, m.endResultOf())

	teams := [2]int{100, 200}
	for ti, team := range teams {
		if ti > 0 {
			b.WriteString(",")
		}
		bans := m.bansOf(team)
		fmt.Fprintf(&b, `{"teamId":%d,"win":%t,"bans":[`, team, team == m.winner)
		for bi, champion := range bans {
			if bi > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"championId":%d,"pickTurn":%d}`, champion, bi+1)
		}
		b.WriteString("]}")
	}

	b.WriteString(`],"participants":[`)
	first := true
	for ti, team := range teams {
		champions := m.championsOf(team)
		for role := range fixtureRoles {
			index := fixtureParticipantIndex{team, role}
			if !first {
				b.WriteString(",")
			}
			first = false
			b.WriteString(renderParticipant(m, team, ti*5+role, role, champions[role], index))
		}
	}
	b.WriteString("]}}")
	return b.String()
}

func renderParticipant(m fixtureMatch, team, seat, role, champion int, index fixtureParticipantIndex) string {
	// Team 100 carries the position in teamPosition, using the spelings Riot
	// returns there. Team 200 leaves teamPosition empty and carries it in
	// individualPosition, which is the fallback the role rule has to survive.
	teamPositions := [5]string{"TOP", "JUNGLE", "MIDDLE", "BOTTOM", "UTILITY"}
	individualPositions := [5]string{"TOP", "JUNGLE", "MID", "ADC", "SUPPORT"}
	teamPosition, individualPosition := teamPositions[role], individualPositions[role]
	if team == 200 {
		teamPosition = ""
	}

	items := [7]int{6672, 3153, 0, 0, 0, 0, 3340}
	if containsIndex(m.noItems, index) {
		items = [7]int{}
	}
	spells := [2]int{4, 14}
	if containsIndex(m.noSpells, index) {
		spells = [2]int{4, 0}
	}

	var b strings.Builder
	fmt.Fprintf(&b, `{"participantId":%d,"puuid":%q,"riotIdGameName":"fixture-summoner-%02d",`+
		`"riotIdTagline":"FIXT","summonerId":"fixture-summoner-%02d","teamId":%d,`+
		`"championId":%d,"championName":"Fixture",`+
		`"teamPosition":%q,"individualPosition":%q,"win":%t,`,
		seat+1, fixturePuuids[seat], seat+1, seat+1, team, champion, teamPosition, individualPosition, team == m.winner)
	for slot, item := range items {
		fmt.Fprintf(&b, `"item%d":%d,`, slot, item)
	}
	fmt.Fprintf(&b, `"summoner1Id":%d,"summoner2Id":%d,`, spells[0], spells[1])
	fmt.Fprintf(&b, `"perks":{"styles":%s,"statPerks":{"offense":5008,"flex":5008,"defense":5002},`+
		`"perkIds":[8005,9111,9105,8014,8139,8126,5008,5008,5002],"perkStyle":8000,"perkSubStyle":8100},`,
		runePage(!containsIndex(m.badRunes, index)))
	fmt.Fprintf(&b, `"kills":%d,"deaths":%d,"assists":%d,"totalMinionsKilled":%d,"goldEarned":12345}`,
		3+seat, 2+seat%3, 7+seat, m.durationOf()/10)
	return b.String()
}

func containsIndex(indexes []fixtureParticipantIndex, want fixtureParticipantIndex) bool {
	for _, index := range indexes {
		if index == want {
			return true
		}
	}
	return false
}

// fixtureFiles groups the rendered lines by partition.
func fixtureFiles(matches []fixtureMatch) map[string]string {
	files := map[string]string{}
	for _, m := range matches {
		files[m.partition] += renderMatch(m) + "\n"
	}
	return files
}

// fixtureRoot is the fixture directory, relative to this package.
const fixtureRoot = "../../fixtures/agg"

// TestFixtureSourcesAreCurrent fails when the checked-in JSONL no longer
// matches the match set declared in this file.
//
// The payloads are the specification the expected cell table was computed from,
// so a drift between the two would silently invalidate every expectation in
// build_fixture_test.go. Regenerate with:
//
//	LOLSTATS_AGG_WRITE_FIXTURE=1 go test ./internal/aggregate/ -run TestFixtureSources
func TestFixtureSourcesAreCurrent(t *testing.T) {
	t.Parallel()

	sets := map[string]map[string]string{
		"raw":     fixtureFiles(fixtureMatches()),
		"corrupt": fixtureFiles(fixtureCorruptMatches()),
	}
	if os.Getenv("LOLSTATS_AGG_WRITE_FIXTURE") == "1" {
		for set, files := range sets {
			for partition, content := range files {
				dir := filepath.Join(fixtureRoot, set, "riot", "match-v5", "dt="+partition)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("create %s: %v", dir, err)
				}
				path := filepath.Join(dir, "matches.jsonl")
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
				t.Logf("wrote %s", path)
			}
		}
		return
	}

	for set, files := range sets {
		for partition, want := range files {
			path := filepath.Join(fixtureRoot, set, "riot", "match-v5", "dt="+partition, "matches.jsonl")
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v (regenerate with LOLSTATS_AGG_WRITE_FIXTURE=1)", path, err)
			}
			if string(got) != want {
				t.Errorf("%s is stale: %d bytes on disk, %d bytes declared.\n"+
					"regenerate with LOLSTATS_AGG_WRITE_FIXTURE=1 go test ./internal/aggregate/ -run TestFixtureSources",
					path, len(got), len(want))
			}
		}
	}
}

// TestFixturePayloadsAreValidJSON is a self-check on the hand-written renderer:
// the fixture is only useful if the documents it produces are what the archive
// would hold.
func TestFixturePayloadsAreValidJSON(t *testing.T) {
	t.Parallel()

	for _, m := range append(fixtureMatches(), fixtureCorruptMatches()...) {
		var document map[string]any
		if err := json.Unmarshal([]byte(renderMatch(m)), &document); err != nil {
			t.Fatalf("match %s is not valid JSON: %v", m.id, err)
		}
		if m.bare {
			if len(document) != 0 {
				t.Errorf("match %s should carry no envelope, got %v", m.id, document)
			}
			continue
		}
		info, ok := document["info"].(map[string]any)
		if !ok {
			t.Fatalf("match %s has no info object", m.id)
		}
		participants, ok := info["participants"].([]any)
		if !ok || len(participants) != 10 {
			t.Fatalf("match %s has %d participants, want 10", m.id, len(participants))
		}
		teams, ok := info["teams"].([]any)
		if !ok || len(teams) != 2 {
			t.Fatalf("match %s has %d teams, want 2", m.id, len(teams))
		}
	}
}

// TestFixtureBansAreDisjointFromPlayedChampions enforces the one realism
// constraint the ban arithmetic depends on: a champion cannot be banned in a
// match it played in. Without it a later edit could ban a champion inside its
// own game and make ban_rate and pick_rate describe the same game twice.
func TestFixtureBansAreDisjointFromPlayedChampions(t *testing.T) {
	t.Parallel()

	for _, m := range append(fixtureMatches(), fixtureCorruptMatches()[2:]...) {
		played := map[int]bool{}
		for _, team := range [2]int{100, 200} {
			for _, champion := range m.championsOf(team) {
				played[champion] = true
			}
		}
		for _, team := range [2]int{100, 200} {
			for _, banned := range m.bansOf(team) {
				if banned == 0 {
					continue
				}
				if played[banned] {
					t.Errorf("match %s bans champion %d, which plays in the same match", m.id, banned)
				}
			}
		}
	}
}

// TestFixtureMatchIDsAreUnique keeps a copy and paste error in the match list
// from quietly turning two matches into one.
func TestFixtureMatchIDsAreUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	// Only the valid archive: the corrupt set deliberately replays its first
	// two matches, so both lists together legitimately repeat two ids.
	for _, m := range fixtureMatches() {
		if seen[m.id] {
			t.Errorf("match id %s is declared twice", m.id)
		}
		seen[m.id] = true
		for _, team := range [2]int{100, 200} {
			for slot, champion := range m.championsOf(team) {
				if champion <= 0 {
					t.Errorf("match %s team %d %s has champion %d", m.id, team, fixtureRoles[slot], champion)
				}
			}
		}
	}
}
