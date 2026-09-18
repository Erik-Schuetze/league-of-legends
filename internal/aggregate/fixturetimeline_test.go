package aggregate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The timeline fixture: the second raw archive, and the payloads the feature
// build reads out of it.
//
// It is a root of its own, fixtures/agg/timeline, holding both trees - a
// summary archive and a timeline archive - rather than more partitions inside
// fixtures/agg/raw. Two reasons, and both are about leaving the nightly
// evidence where it is:
//
//   - The nightly fixture's nine counted matches have their durations and their
//     creation times hand-computed into the expected cell table in
//     fixturebuild_test.go. Adding matches to that archive would mean editing
//     that table, which would mean editing the evidence for the numbers the
//     nightly build publishes today.
//   - The feature build scopes by region and queue, and the nightly fixture
//     already spends its negative cases - another region, another queue, two
//     matches outside the window - on the nightly build's own filters. A
//     separate root costs one directory and buys two archives that cannot move
//     each other's numbers.
//
// Eight matches: one for every reason the ledger can record, plus three
// eligible games that between them exercise evenly spaced frames, irregular
// frames and a game that ends before the last checkpoint. Every shape below is
// one Riot is documented to return, not one invented to make the SQL branch;
// the comments name the issue each stands for, and the grade each match must
// receive is written beside its payload so the fixture and its expectation are
// read together.
//
// The numbers every feature is asserted against come from fixtureGrowth, one
// linear function of (seat, minute). That is deliberate: it makes the expected
// values in features_fixture_test.go checkable with a pencil, which is the same
// standard the nightly fixture holds itself to.

// fixtureTimelineRoot is the fixture directory, relative to this package.
const fixtureTimelineRoot = "../../fixtures/agg/timeline"

// fixtureTimelinePartition is the dt= partition both archives are partitioned
// into. One partition, because the build does not care how a fetch date is
// spelled and a second one would only test itself.
const fixtureTimelinePartition = "2026-09-14"

// fixtureTimelineCreated is info.gameCreation for every timeline fixture match,
// as RFC3339, the spelling the nightly fixture uses. It is before the partition,
// because a match is crawled after it is played.
const fixtureTimelineCreated = "2026-09-13T18:05:00Z"

// fixtureTimelineEvent is one event, before it is placed into a frame.
//
// Riot nests events inside the frame they occurred in, and events and frames do
// not share a clock: an event at 181000 ms sits in the frame at 240000 ms. So
// the fixture declares the event's own timestamp and the renderer files it into
// the first frame at or after it, which is what the API does.
type fixtureTimelineEvent struct {
	timestamp int
	// body is the event object including its braces, without a timestamp.
	body string
}

// fixtureTimeline is the timeline half of one fixture match.
type fixtureTimeline struct {
	// absent means the match has no timeline row in the archive at all. The
	// sample is ordered by hash, so a match whose timeline aged out is normal.
	absent bool
	// interval is info.frameInterval, in milliseconds.
	interval int
	// timestamps are the frame timestamps, in order.
	timestamps []int
	// nullFrames renders every frame's participantFrames as null, which is the
	// aborted-game shape that still carries a frames array.
	nullFrames bool
	events     []fixtureTimelineEvent
}

// fixtureTimelineMatch is one fixture match and the timeline that goes with it.
type fixtureTimelineMatch struct {
	summary  fixtureMatch
	timeline fixtureTimeline
	// grade is the exclusion_reason match_index must record. It is the
	// expectation, written beside the payload shape that produces it.
	grade string
}

// fixtureTimelineMatches is the whole fixture archive.
func fixtureTimelineMatches() []fixtureTimelineMatch {
	return []fixtureTimelineMatch{
		fixtureLongGame(),
		fixtureShortGame(),
		fixtureAbortedGame(),
		fixtureIrregularFrames(),
		fixtureMissingTimeline(),
		fixtureZeroIntervalFrames(),
		fixtureNullParticipantFrames(),
		fixtureShortFrames(),
	}
}

// fixtureSummary is the summary half of a timeline fixture match, with the
// defaults they all share.
func fixtureSummary(id, patch string, winner int) fixtureMatch {
	return fixtureMatch{
		id:        id,
		partition: fixtureTimelinePartition,
		created:   fixtureTimelineCreated,
		patch:     patch,
		platform:  "EUW1",
		queue:     aggmodel.QueueIDRankedSolo5x5,
		winner:    winner,
	}
}

// fixtureLongGame is the fully populated eligible match, and the one every
// positive expectation is computed from.
//
// It carries at least one of every event the extract reads, because a feature
// with no fixture is a feature nobody has watched produce a number: a first
// blood with an assist, a second kill, three turret plates, a dragon for each
// team, grubs, a herald, a baron, an outer tower, a dragon soul, an item
// purchase at minute 1 and a second at minute 7, a ward placed and one cleared,
// and a duplicated SKILL_LEVEL_UP - the last being the Patch 15.17 defect
// (RiotGames/developer-relations#1100), which must be flagged rather than
// counted as a skill point.
//
// The kill at 181000 ms is not on a frame boundary on purpose: it lands in the
// frame at 240000 ms. A minute derived from the frame index would call it minute
// 4; a minute derived from the event's own timestamp calls it minute 3, which
// is the truth and is what the fixture asserts.
func fixtureLongGame() fixtureTimelineMatch {
	return fixtureTimelineMatch{
		summary: fixtureSummary("EUW1_0000000201", "16.03", 100),
		grade:   "ok",
		timeline: fixtureTimeline{
			interval:   60000,
			timestamps: minuteFrames(30),
			events: []fixtureTimelineEvent{
				{60000, skillEvent(1, 1, 2)},
				{60000, itemEvent(1, 1055)},
				{181000, killEvent(1, 6, []int{2}, 5000, 5000)},
				{181000, specialKillEvent(1, "KILL_FIRST_BLOOD")},
				{245000, plateEvent(100, 1, "TOP")},
				{301000, eliteEvent("DRAGON", "FIRE_DRAGON", 100, 2)},
				{305000, plateEvent(100, 1, "TOP")},
				{361000, eliteEvent("HORDE", "", 100, 2)},
				{365000, plateEvent(100, 1, "TOP")},
				{420000, itemEvent(1, 3153)},
				{481000, killEvent(6, 1, nil, 6000, 6100)},
				{541000, buildingEvent("TOWER", "OUTER", "TOP", 100, 1, 3000, 3000)},
				{601000, wardPlacedEvent(3, "YELLOW_TRINKET")},
				{661000, eliteEvent("DRAGON", "WATER_DRAGON", 200, 7)},
				{721000, killEvent(1, 7, nil, 0, 0)},
				{725000, wardKilledEvent(3, "YELLOW_TRINKET")},
				// The same (actor, slot, level) a second time: the duplicate defect.
				{781000, skillEvent(1, 1, 2)},
				{785000, skillEvent(1, 2, 3)},
				{961000, killEvent(1, 6, nil, 0, 0)},
				{1021000, eliteEvent("BARON_NASHOR", "", 100, 2)},
				{1081000, eliteEvent("RIFTHERALD", "", 100, 2)},
				{1201000, soulEvent(100, "FIRE_DRAGON")},
			},
		},
	}
}

// fixtureShortGame is the game under the floor. Its summary says four minutes
// and its timeline has four frames, so nothing about it is malformed - it is
// simply too early to ask what happened at minute 5. A reader who passes
// -min-duration 0 gets the match in the ledger and its frames in
// participant_minutes; the default excludes it with a recorded reason instead
// of dropping it silently.
func fixtureShortGame() fixtureTimelineMatch {
	summary := fixtureSummary("EUW1_0000000202", "16.03", 100)
	summary.duration = 240
	return fixtureTimelineMatch{
		summary: summary,
		grade:   "too_short",
		timeline: fixtureTimeline{
			interval:   60000,
			timestamps: minuteFrames(4),
			events: []fixtureTimelineEvent{
				{60000, killEvent(6, 1, nil, 0, 0)},
				{180000, killEvent(1, 6, nil, 0, 0)},
			},
		},
	}
}

// fixtureAbortedGame is the aborted shape: frameInterval zero, an empty frames
// array, and a summary that reports the abort
// (RiotGames/developer-relations#898). It still carries a timeline row, so it
// is present and ineligible - the distinction the ledger exists to make, and
// the one an "is the timeline there" filter gets wrong.
func fixtureAbortedGame() fixtureTimelineMatch {
	summary := fixtureSummary("EUW1_0000000203", "16.04", 0)
	summary.duration = 90
	summary.endResult = "Abort_TooFewPlayers"
	return fixtureTimelineMatch{
		summary:  summary,
		grade:    "aborted",
		timeline: fixtureTimeline{interval: 0},
	}
}

// fixtureIrregularFrames is the second eligible match, and the one that pins
// the frame-spacing rule.
//
// Its frames are 65000 ms apart - inside the 60.5-71.3 s band Riot has returned
// since about Patch 16.1 (RiotGames/developer-relations#1129) - while
// frameInterval still says 60000, because that field is nominal and the payload
// is the truth. The frame nearest the five-minute checkpoint is therefore the
// one at 325000 ms, which is the sixth frame in one-based counting and minute 5
// by its own timestamp: a build that counted frames as minutes would file it
// under minute 6 and the deltas would be wrong by a whole minute.
//
// It stops at 1040000 ms - seventeen minutes and twenty seconds - so the
// minutes are not contiguous: 715000 ms is minute 11 and 780000 ms is minute
// 13, and minute 12 has no frame at all.
//
// The minute-10 checkpoint lands on the frame at 585000 ms, whose own minute is
// 9, because the next frame at 650000 ms is 83 seconds past minute 10 and
// outside the grace window. That is the window doing its job rather than a
// defect, and it is pinned here so a change to the window shows up as a diff.
func fixtureIrregularFrames() fixtureTimelineMatch {
	summary := fixtureSummary("EUW1_0000000204", "16.05", 200)
	summary.duration = 1080
	return fixtureTimelineMatch{
		summary: summary,
		grade:   "ok",
		timeline: fixtureTimeline{
			interval:   60000,
			timestamps: steppedFrames(16, 65000),
			events: []fixtureTimelineEvent{
				{305000, killEvent(1, 6, nil, 0, 0)},
				{610000, killEvent(6, 1, nil, 0, 0)},
			},
		},
	}
}

// fixtureMissingTimeline is the ordinary hole: the match is in the archive and
// its timeline was never fetched, or 404ed because it aged past the one-year
// retention that is shorter than the two-year match history. It is the majority
// case in a partial sample, and it has to appear in the ledger rather than
// simply fail to appear at all.
func fixtureMissingTimeline() fixtureTimelineMatch {
	return fixtureTimelineMatch{
		summary:  fixtureSummary("EUW1_0000000205", "16.05", 100),
		grade:    "no_timeline",
		timeline: fixtureTimeline{absent: true},
	}
}

// fixtureZeroIntervalFrames is the third aborted shape: the interval is zero
// but frames arrive anyway. It is distinct from fixtureAbortedGame in that it
// has data, and distinct from every other grade because a zero interval makes
// the spacing rule undefined - so it is refused by interval, not by count.
func fixtureZeroIntervalFrames() fixtureTimelineMatch {
	return fixtureTimelineMatch{
		summary: fixtureSummary("EUW1_0000000206", "16.06", 100),
		grade:   "frame_interval_zero",
		timeline: fixtureTimeline{
			interval:   0,
			timestamps: minuteFrames(3),
			events:     []fixtureTimelineEvent{{60000, killEvent(1, 6, nil, 0, 0)}},
		},
	}
}

// fixtureNullParticipantFrames is the aborted game that still reports its
// interval: participantFrames is null in every frame.
//
// json_each(null) yields one row with a null key rather than no rows, so
// counting keys would report this match as having data and the extract would
// publish ten rows of nothing per frame. The extract counts participant frames
// instead, which is why this fixture exists in this shape.
func fixtureNullParticipantFrames() fixtureTimelineMatch {
	return fixtureTimelineMatch{
		summary: fixtureSummary("EUW1_0000000207", "16.06", 100),
		grade:   "frames_null",
		timeline: fixtureTimeline{
			interval:   60000,
			timestamps: minuteFrames(3),
			nullFrames: true,
		},
	}
}

// fixtureShortFrames is the third eligible match: a complete, real game that
// simply stops at thirteen minutes.
//
// It exists for the checkpoints a game never reached. Minute 15 has no frame at
// all, so cs_15 and its four sibling deltas must be null - not zero, and not the
// minute-13 frame carried forward. A table that gets regressed on cannot spell
// "unknown" the same way it spells "nothing happened".
//
// Its one kill is at 66000 ms and is filed into the frame at 120000 ms, so the
// event's own minute is 1 while its frame's minute is 2. first_death_minute is
// derived from the event timestamp, which is why the two disagree here without
// either being wrong.
func fixtureShortFrames() fixtureTimelineMatch {
	summary := fixtureSummary("EUW1_0000000208", "16.07", 100)
	summary.duration = 780
	return fixtureTimelineMatch{
		summary: summary,
		grade:   "ok",
		timeline: fixtureTimeline{
			interval:   60000,
			timestamps: minuteFrames(13),
			events: []fixtureTimelineEvent{
				{66000, killEvent(1, 6, nil, 0, 0)},
			},
		},
	}
}

// minuteFrames returns evenly spaced frames, one per minute from 1 to minutes.
func minuteFrames(minutes int) []int {
	out := make([]int, 0, minutes)
	for minute := 1; minute <= minutes; minute++ {
		out = append(out, minute*60000)
	}
	return out
}

// steppedFrames returns count frames step milliseconds apart, starting one step
// in - the spacing Riot returns when a frame is scheduled but late.
func steppedFrames(count, step int) []int {
	out := make([]int, 0, count)
	for i := 1; i <= count; i++ {
		out = append(out, i*step)
	}
	return out
}

// fixtureGrowth is the per-minute model every fixture frame is drawn from.
//
// It is deliberately linear and legible, because the expected values in
// features_fixture_test.go are this function written out as literals, and a
// reader has to be able to check one against the other without running
// anything:
//
//	minionsKilled        8*minute + seat          lane minions, one per 7.5s
//	jungleMinionsKilled  7*minute + seat          camp monsters, jungle only
//	xp                   300*minute + 10*seat
//	totalGold            500*minute + 20*seat
//	currentGold          100*minute + 5*seat
//	level                1 + minute/3
//
// The jungle seats are 1 and 6 - the second participant of each team - so
// seat%5 == 1 is the jungler and takes no lane minions at all. That is what
// makes cs_total a lane statistic in this fixture rather than a
// role-independent one, and it is the reason the lane pairing has something to
// get wrong.
//
// Positions are x = 1000 + 100*seat and y = 200*minute, so a row's position
// says which participant and which minute it belongs to without a join.
func fixtureGrowth(seat, minute int) (minions, jungleMinions, xp, totalGold, currentGold, level int) {
	minions, jungleMinions = 8*minute+seat, 0
	if seat%5 == 1 {
		minions, jungleMinions = 0, 7*minute+seat
	}
	return minions, jungleMinions, 300*minute + 10*seat,
		500*minute + 20*seat, 100*minute + 5*seat, 1 + minute/3
}

// renderTimeline renders one fixture timeline the way the API returns it.
func renderTimeline(m fixtureTimelineMatch) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"metadata":{"matchId":%q,"participants":[`, m.summary.id)
	for i, puuid := range fixturePuuids {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%q", puuid)
	}
	fmt.Fprintf(&b, `]},"info":{"frameInterval":%d,"frames":[`, m.timeline.interval)

	// Events are filed into the first frame at or after their own timestamp,
	// which is how the API nests them. previous is the frame before the one
	// being written, so an event is written exactly once.
	previous := 0
	for i, timestamp := range m.timeline.timestamps {
		if i > 0 {
			b.WriteString(",")
		}
		var events []string
		for _, event := range m.timeline.events {
			if event.timestamp > previous && event.timestamp <= timestamp {
				events = append(events,
					fmt.Sprintf(`{"timestamp":%d,%s`, event.timestamp, event.body[1:]))
			}
		}
		fmt.Fprintf(&b, `{"timestamp":%d,"participantFrames":%s,"events":[%s]}`,
			timestamp, renderParticipantFrames(m.timeline, timestamp), strings.Join(events, ","))
		previous = timestamp
	}
	b.WriteString("]}}")
	return b.String()
}

// renderParticipantFrames renders one frame's participantFrames object, keyed by
// the string form of the 1-based participantId.
func renderParticipantFrames(timeline fixtureTimeline, timestamp int) string {
	if timeline.nullFrames {
		return "null"
	}
	minute := timestamp / 60000
	var b strings.Builder
	b.WriteString("{")
	for seat := range fixturePuuids {
		if seat > 0 {
			b.WriteString(",")
		}
		minions, jungle, xp, totalGold, currentGold, level := fixtureGrowth(seat, minute)
		fmt.Fprintf(&b, `"%d":{"participantId":%d,"level":%d,"xp":%d,"currentGold":%d,`+
			`"totalGold":%d,"minionsKilled":%d,"jungleMinionsKilled":%d,`+
			`"position":{"x":%d,"y":%d}}`,
			seat+1, seat+1, level, xp, currentGold, totalGold, minions, jungle,
			1000+100*seat, 200*minute)
	}
	b.WriteString("}")
	return b.String()
}

// The event constructors. Each writes the fields the extract reads and nothing
// else, which is the point of a fixture: a field the SQL does not read is a
// field nobody has agreed on.

func killEvent(killer, victim int, assists []int, x, y int) string {
	return fmt.Sprintf(`{"type":"CHAMPION_KILL","killerId":%d,"victimId":%d,`+
		`"assistingParticipantIds":%s,"position":{"x":%d,"y":%d},`+
		`"bounty":300,"killStreakLength":0}`,
		killer, victim, intList(assists), x, y)
}

func specialKillEvent(participant int, killType string) string {
	return fmt.Sprintf(`{"type":"CHAMPION_SPECIAL_KILL","killerId":%d,"killType":%q,`+
		`"multiKillLength":1}`, participant, killType)
}

// eliteEvent writes an ELITE_MONSTER_KILL. monsterSubType is omitted when empty,
// because Riot does not send an empty string there - it sends no field - and the
// extract coalesces monsterSubType with soulType for the soul event.
func eliteEvent(monster, subtype string, team, killer int) string {
	fields := fmt.Sprintf(`"monsterType":%q,"teamId":%d,"killerId":%d`, monster, team, killer)
	if subtype != "" {
		fields += fmt.Sprintf(`,"monsterSubType":%q`, subtype)
	}
	return fmt.Sprintf(`{"type":"ELITE_MONSTER_KILL",%s}`, fields)
}

func soulEvent(team int, soul string) string {
	return fmt.Sprintf(`{"type":"DRAGON_SOUL_GIVEN","teamId":%d,"soulType":%q}`, team, soul)
}

func plateEvent(team, killer int, lane string) string {
	return fmt.Sprintf(`{"type":"TURRET_PLATE_DESTROYED","teamId":%d,"killerId":%d,"laneType":%q}`,
		team, killer, lane)
}

func buildingEvent(building, tower, lane string, team, killer, x, y int) string {
	return fmt.Sprintf(`{"type":"BUILDING_KILL","buildingType":%q,"towerType":%q,"laneType":%q,`+
		`"teamId":%d,"killerId":%d,"position":{"x":%d,"y":%d}}`,
		building, tower, lane, team, killer, x, y)
}

func itemEvent(participant, item int) string {
	return fmt.Sprintf(`{"type":"ITEM_PURCHASED","participantId":%d,"itemId":%d}`, participant, item)
}

func skillEvent(participant, slot, level int) string {
	return fmt.Sprintf(`{"type":"SKILL_LEVEL_UP","participantId":%d,"skillSlot":%d,"level":%d,`+
		`"levelUpType":"NORMAL"}`, participant, slot, level)
}

func wardPlacedEvent(creator int, ward string) string {
	return fmt.Sprintf(`{"type":"WARD_PLACED","creatorId":%d,"wardType":%q}`, creator, ward)
}

func wardKilledEvent(killer int, ward string) string {
	return fmt.Sprintf(`{"type":"WARD_KILL","killerId":%d,"wardType":%q}`, killer, ward)
}

// intList renders an integer slice as a JSON array, and nil as an empty one: the
// field is present on every CHAMPION_KILL the API sends, even when no one
// assisted.
func intList(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprint(value))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// fixtureTimelineSummaryFiles groups the rendered summary payloads by partition.
func fixtureTimelineSummaryFiles(matches []fixtureTimelineMatch) map[string]string {
	files := map[string]string{}
	for _, m := range matches {
		files[m.summary.partition] += renderMatch(m.summary) + "\n"
	}
	return files
}

// fixtureTimelineFiles groups the rendered timelines by partition, skipping the
// match whose timeline was never fetched.
func fixtureTimelineFiles(matches []fixtureTimelineMatch) map[string]string {
	files := map[string]string{}
	for _, m := range matches {
		if m.timeline.absent {
			continue
		}
		files[m.summary.partition] += renderTimeline(m) + "\n"
	}
	return files
}

// lastTimestamp is the final frame timestamp, or zero for a timeline with no
// frames.
func lastTimestamp(timestamps []int) int {
	if len(timestamps) == 0 {
		return 0
	}
	return timestamps[len(timestamps)-1]
}

// TestFixtureTimelinePayloadsAreValidJSON is a self-check on the hand-written
// renderer: the fixture is only useful if the documents it produces are the
// documents the archive would hold. It also refuses an event that no frame can
// hold, which would otherwise be dropped from the payload in silence.
func TestFixtureTimelinePayloadsAreValidJSON(t *testing.T) {
	t.Parallel()

	for _, m := range fixtureTimelineMatches() {
		if !m.timeline.absent {
			var document map[string]any
			if err := json.Unmarshal([]byte(renderTimeline(m)), &document); err != nil {
				t.Fatalf("timeline for %s is not valid JSON: %v", m.summary.id, err)
			}
			info, ok := document["info"].(map[string]any)
			if !ok {
				t.Fatalf("timeline for %s carries no info", m.summary.id)
			}
			frames, ok := info["frames"].([]any)
			if !ok {
				t.Fatalf("timeline for %s carries no frames", m.summary.id)
			}
			if want := len(m.timeline.timestamps); len(frames) != want {
				t.Errorf("%s: %d frames rendered, %d declared", m.summary.id, len(frames), want)
			}
		}
		for _, event := range m.timeline.events {
			if last := lastTimestamp(m.timeline.timestamps); event.timestamp > last {
				t.Errorf("%s: event at %d is past the last frame at %d and would be dropped",
					m.summary.id, event.timestamp, last)
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(event.body), &body); err != nil {
				t.Fatalf("%s: event at %d is not valid JSON: %v", m.summary.id, event.timestamp, err)
			}
		}
	}
}

// TestFixtureTimelineSourcesAreCurrent fails when the checked-in JSONL no longer
// matches the fixture declared in this file.
//
// Regenerate with:
//
//	LOLSTATS_AGG_WRITE_FIXTURE=1 go test ./internal/aggregate/ -run TestFixtureTimelineSources
func TestFixtureTimelineSourcesAreCurrent(t *testing.T) {
	t.Parallel()

	matches := fixtureTimelineMatches()
	sets := []struct {
		source string
		file   string
		files  map[string]string
	}{
		{rawSourceDir, "matches.jsonl", fixtureTimelineSummaryFiles(matches)},
		{RawSourceTimeline, "timelines.jsonl", fixtureTimelineFiles(matches)},
	}

	if os.Getenv("LOLSTATS_AGG_WRITE_FIXTURE") == "1" {
		for _, set := range sets {
			for partition, content := range set.files {
				dir := filepath.Join(fixtureTimelineRoot, "riot", set.source, rawPartitionPr+partition)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("create %s: %v", dir, err)
				}
				path := filepath.Join(dir, set.file)
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
				t.Logf("wrote %s", path)
			}
		}
		return
	}

	for _, set := range sets {
		for partition, want := range set.files {
			path := filepath.Join(fixtureTimelineRoot, "riot", set.source, rawPartitionPr+partition, set.file)
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v (regenerate with LOLSTATS_AGG_WRITE_FIXTURE=1)", path, err)
			}
			if string(got) != want {
				t.Errorf("%s is stale: %d bytes on disk, %d bytes declared.\n"+
					"regenerate with LOLSTATS_AGG_WRITE_FIXTURE=1 go test ./internal/aggregate/ -run TestFixtureTimelineSources",
					path, len(got), len(want))
			}
		}
	}
}
