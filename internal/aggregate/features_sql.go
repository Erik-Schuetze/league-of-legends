package aggregate

import (
	"fmt"
	"strings"
)

// The feature dataset is a second, independent build. It reads both raw
// archives - the match summaries and the match timelines - and writes plain
// Parquet feature tables for an analyst to open with DuckDB or pandas. It
// shares the extraction plumbing with the nightly tier list and nothing else:
// the nightly build keeps reading match-v5 alone, so a bad timeline extract
// cannot fail it, and the dataset is not part of the frozen agg/v1 reader
// contract, so a minute-level table is never suppressed by min_cell_n. See
// docs/decisions/ADR-012-ingest-match-timelines.md.
//
// The statements below run one process each (CLIEngine starts the pinned DuckDB
// binary per statement), so no relation survives between them and every
// intermediate is a file. The shape is therefore always the same: a statement
// reads the archive or an earlier spill, and COPYs its output to a parquet
// file; the next statement reads that file back through a glob, so a batch
// boundary is invisible past the phase that created it.
//
// Batching is not an optimisation here, it is the difference between a build
// that finishes and one that dies. A timeline payload is roughly ten times a
// summary payload and the extraction unnests two independent arrays out of it
// (frames[].participantFrames and frames[].events[]), so the batch size of the
// nightly extraction is not inherited: featureBatchParts below is re-measured
// against this payload class, on a live archive, and pinned with the numbers
// recorded beside it.

const (
	// featureSummarySource and featureTimelineSource name the raw payload
	// directories this build reads.
	featureSummarySource  = "match-v5"
	featureTimelineSource = "match-v5-timeline"

	// featureBatchParts is how many raw parts one payload-carrying statement
	// reads. A part is one fetch of up to a few hundred payloads, so this is a
	// bound on payload bytes in the engine's memory rather than on rows.
	//
	// Re-measured for timelines, because a timeline is a much larger payload
	// than a summary and an inherited constant would be an estimate. See
	// docs/data-sources.md for the measurement this value was pinned from.
	featureBatchParts = 4

	// featureCheckpoints are the minutes the per-matchup table is built at.
	// They are the three the requested analysis asks for; every minute stays
	// in participant_minutes, so a different checkpoint is a query, not a
	// rebuild.
	featureCheckpointList = "(5, 10, 15)"
)

// featureScopeFilter renders the optional scope predicate over the archive
// envelope columns, which is what an operator narrows a dataset to.
//
// It is empty by default on purpose. The dataset describes what the archive
// holds - the crawl is what chose the sample (see the frozen selection rule in
// ADR-012) - so the build narrowing it again would make the published coverage
// a function of two rules instead of one.
//
// The region is matched on the payload's platform id under both spellings, not
// on the archive's region column. The two are not the same string - the
// published region is EUW and the platform Riot serves from is EUW1 - so
// comparing the archive column to the configured region would match an archive
// written from configuration and silently match nothing in an archive written
// from the platform id, which is a build that fails closed with an empty window
// on a correct archive. See platformFilter.
//
// prefix qualifies the platform column for statements that read the envelope
// beside another relation, which is every statement that carries a payload;
// it is empty for the single-relation ones.
func featureScopeFilter(prefix, region, platform string, queue int) string {
	var conditions []string
	if strings.ToUpper(strings.TrimSpace(region)) != "" || strings.TrimSpace(platform) != "" {
		conditions = append(conditions, platformCondition(prefix, platformFilter(region, platform)))
	}
	if queue > 0 {
		column := "queue_id"
		if prefix != "" {
			column = prefix + "." + column
		}
		conditions = append(conditions, fmt.Sprintf("%s = %d", column, queue))
	}
	if len(conditions) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(conditions, " AND ")
}

// summaryEnvelopeSQL reads the per-match keys of the summary archive, carrying
// no payload.
//
// It is the same two-statement split the nightly build uses and for the same
// measured reason (see envelopeSQL): one statement that projects fields out of
// a 1.4 MB payload and also carries that payload through does not fit in the
// engine's memory budget, and one that projects fields while dropping the
// payload does. The keys are collapsed over a few thousand rows here so the
// payload statement can be a plain join against a relation unique on
// (part, part_row).
//
// end_of_game_result and participant_count are free additions to the nightly
// envelope because they are additive transforms over an archive that is
// already there: no re-crawl, no new request. end_of_game_result is
// undocumented by Riot (developer-relations#890) and is read rather than
// trusted - it is published so an analyst can branch on it and so
// match_index can explain an aborted game without inferring it from a frame
// count.
//
// Provenance is read back out of the payload rather than taken from the
// archive's own columns, with region as the one exception. The two agree on a
// real archive - the crawler wrote both from the same DTO - but a payload-derived
// envelope is the shape the nightly statement established, it is what makes the
// archive filter testable without materialising eleven typed columns, and it
// cannot drift from the payload it describes. region has no JSON path: the
// crawl recorded it from configuration, so the archive column is the only
// source and the filter treats it as the same field under both spellings.
func summaryEnvelopeSQL(parts []string) string {
	guarded := func(expression string) string {
		return "CASE WHEN json_valid(payload) THEN " + expression + " END"
	}
	text := func(path string) string {
		return guarded("json_extract_string(payload, " + quoteLiteral(path) + ")")
	}
	number := func(path, kind string) string {
		return "CAST(" + guarded("json_extract(payload, "+quoteLiteral(path)+")") + " AS " + kind + ")"
	}
	arrayLen := func(path string) string {
		array := "json_extract(payload, " + quoteLiteral(path) + ")"
		return "CASE WHEN json_valid(payload) AND json_type(" + array + ") = 'ARRAY'" +
			" THEN CAST(json_array_length(" + array + ") AS INTEGER) ELSE NULL END"
	}
	return fmt.Sprintf(`SELECT
  part,
  part_row,
  match_id,
  region,
  %s AS patch,
  game_version,
  queue_id,
  game_creation_ms,
  game_duration_s,
  platform_id,
  participants,
  end_of_game_result,
  payload_valid
FROM (
  SELECT filename AS part,
         file_row_number AS part_row,
         region,
         %s AS match_id,
         %s AS game_version,
         %s AS queue_id,
         %s AS game_creation_ms,
         %s AS game_duration_s,
         %s AS platform_id,
         %s AS participants,
         %s AS end_of_game_result,
         json_valid(payload) AS payload_valid
  FROM read_parquet(%s, union_by_name = true, filename = true, file_row_number = true)
) extracted
QUALIFY ROW_NUMBER() OVER (
  PARTITION BY COALESCE(match_id, part || '#' || CAST(part_row AS VARCHAR))
  ORDER BY part, part_row) = 1`,
		patchOf("game_version"),
		text("$.metadata.matchId"),
		text("$.info.gameVersion"),
		number("$.info.queueId", "INTEGER"),
		number("$.info.gameCreation", "BIGINT"),
		number("$.info.gameDuration", "INTEGER"),
		text("$.info.platformId"),
		arrayLen("$.info.participants"),
		text("$.info.endOfGameResult"),
		fileList(parts))
}

// patchOf renders the major.minor patch of a game version column.
//
// It is the SQL spelling of internal/crawl.PatchFromGameVersion, and it exists
// so the dataset reports the same patch string the published statistics are
// sliced by. A game version that is missing or too short yields NULL rather
// than a one-component or empty patch, because "16" is not a patch and a
// partition keyed on it would collect every game of a season.
func patchOf(column string) string {
	return fmt.Sprintf("CASE WHEN %[1]s IS NOT NULL AND length(%[1]s) - length(replace(%[1]s, '.', '')) >= 1"+
		" THEN split_part(%[1]s, '.', 1) || '.' || split_part(%[1]s, '.', 2) END", column)
}

// timelineEnvelopeSQL reads the per-match keys of the timeline archive.
//
// The provenance columns are the archive's own - region, patch, game_version,
// queue_id, game_creation_ms, game_duration_s - and not extracted from the
// payload, because raw.TimelineRow carries exactly the summary's envelope with
// the same column names. A timeline's provenance *is* its match's, so the two
// archives can be joined on the archive columns and no JSON path has to be
// evaluated to scope one against the other.
//
// The two fields that are only in the timeline payload are frameInterval and
// the frame count, and both are needed before any frame is read: they are what
// separates an aborted game (frameInterval 0, no frames) from a game whose
// frames are absent for another reason, and they are what match_index records
// so an operator can filter on them without touching a 1 MB column.
//
// participant_frame_count is the third, and it exists because the other two do
// not answer the question the ledger actually asks. Riot returns `frames` with
// `participantFrames: null` on an aborted game, so a payload can carry frames,
// a positive frameInterval and not one usable per-participant record; counting
// the array members alone would classify such a game as eligible and then
// publish a lane_matchups row of NULLs for it. So the count is of the
// per-participant records, `json_each` on a JSON null yields a single row whose
// key is NULL, and only rows with a key are counted. It is a correlated scalar
// subquery rather than a join because the outer statement must stay one row per
// payload: this is a count, and joining frames here would multiply the
// provenance columns the envelope exists to keep single-valued.
//
// De-duplication is by match id, first in part then in row order, exactly as
// the summary envelope does it. The timeline archive has no key at all and a
// re-fetch appends rather than replaces (Riot cannot be re-asked for a timeline
// once it has aged out, so the crawler never overwrites one), which makes a
// second record a real possibility and a second game in every count if it is
// not collapsed here.
//
// platform_id is the archive's own region column under the other name. A
// timeline payload has no platformId - the field is a summary's - so the only
// value a timeline archive has is what the crawler recorded from configuration,
// which is the same string it recorded on the summary row beside it. Naming it
// platform_id here is what lets one scope predicate serve both envelopes; that
// predicate accepts the region and the platform id it resolves to, so an
// archive written from either configuration is read the same way.
func timelineEnvelopeSQL(parts []string) string {
	guarded := func(expression string) string {
		return "CASE WHEN json_valid(payload) THEN " + expression + " END"
	}
	number := func(path string) string {
		return "CAST(" + guarded("json_extract(payload, "+quoteLiteral(path)+")") + " AS INTEGER)"
	}
	arrayLen := func(path string) string {
		array := "json_extract(payload, " + quoteLiteral(path) + ")"
		return "CASE WHEN json_valid(payload) AND json_type(" + array + ") = 'ARRAY'" +
			" THEN CAST(json_array_length(" + array + ") AS INTEGER) ELSE NULL END"
	}
	return fmt.Sprintf(`SELECT
  part,
  part_row,
  match_id,
  region,
  patch,
  game_version,
  queue_id,
  game_creation_ms,
  game_duration_s,
  platform_id,
  frame_interval_ms,
  frame_count,
  participant_frame_count,
  payload_valid
FROM (
  SELECT filename AS part,
         file_row_number AS part_row,
         match_id,
         region,
         patch,
         game_version,
         queue_id,
         game_creation_ms,
         game_duration_s,
         region AS platform_id,
         %s AS frame_interval_ms,
         %s AS frame_count,
         %s AS participant_frame_count,
         json_valid(payload) AS payload_valid
  FROM read_parquet(%s, union_by_name = true, filename = true, file_row_number = true)
) extracted
QUALIFY ROW_NUMBER() OVER (
  PARTITION BY COALESCE(match_id, part || '#' || CAST(part_row AS VARCHAR))
  ORDER BY part, part_row) = 1`,
		number("$.info.frameInterval"),
		arrayLen("$.info.frames"),
		participantFrameCount(),
		fileList(parts))
}

// participantFrameCount counts the per-participant frame records a timeline
// payload carries, which is the only figure that says whether the per-minute
// tables can have rows for that match.
func participantFrameCount() string {
	frames := "COALESCE(CAST(json_extract(payload, " + quoteLiteral("$.info.frames") + ") AS VARCHAR), '[]')"
	return "CASE WHEN json_valid(payload) THEN (SELECT count(*) FROM UNNEST(from_json(" + frames +
		", '[\"JSON\"]')) AS f(value), json_each(json_extract(f.value, " +
		quoteLiteral("$.participantFrames") + ")) AS pf WHERE pf.key IS NOT NULL) END"
}

// carriedPayloadSQL carries the payload of every envelope row, with no JSON
// call of its own.
//
// The envelope columns are passed in rather than fixed, because the summary and
// timeline envelopes carry different sets and the point of this statement is
// that it does no extraction: whatever the keys statement decided (including
// whether a payload is JSON at all) is joined back onto the payload it belongs
// to. This is the second half of the split described on summaryEnvelopeSQL.
//
// Whether the payload may be carried is decided by the payload_valid flag the
// keys statement spilled, not by a second json_valid here - the flag is what
// lets a corrupt payload reach the gate as a countable row instead of turning
// this statement into a DuckDB parse error.
func carriedPayloadSQL(envelopePath string, parts []string, scope string, columns ...string) string {
	projected := ""
	for _, column := range columns {
		projected += "  e." + column + ",\n"
	}
	return fmt.Sprintf(`SELECT
%s  CASE WHEN e.payload_valid THEN CAST(a.payload AS VARCHAR) END AS payload
FROM (
  SELECT filename, file_row_number, payload
  FROM read_parquet(%s, union_by_name = true, filename = true, file_row_number = true)
) a
JOIN %s e ON a.filename = e.part AND a.file_row_number = e.part_row
%s`,
		projected, fileList(parts), parquetOf(envelopePath), scope)
}

// summaryParticipantSQL unnests the summary payload's participant array into
// one row per participant, which is the identity half of every per-participant
// feature: role, champion, team and win.
//
// participantId is read from the element rather than taken from the array
// index. Riot's array is 0-based and its elements carry 1-based ids, and the
// timeline's participantFrames is an object keyed by that same 1-based id, so
// reading the id the payload states is what makes the two joins of this build
// agree without either side compensating for an off-by-one. The index is never
// used as identity anywhere in this file.
//
// role is the assignment (teamPosition) and only falls back to the detection
// (individualPosition), which is the nightly build's normalisation reused
// verbatim. The lane pairing later in this file depends on it: teamPosition is
// constrained to exactly one of each role per team, which is what makes
// "the opposing laner" a well-defined person, where individualPosition is a
// per-isolation guess that pairs worse and sometimes pairs nobody.
func summaryParticipantSQL(matchesPath string) string {
	field := func(path string) string { return "json_extract_string(p.value, '" + path + "')" }
	role := participantRoleSQL(field("$.teamPosition"), field("$.individualPosition"))
	return fmt.Sprintf(`SELECT
  m.match_id,
  m.region,
  m.patch,
  m.queue_id,
  m.platform_id,
  epoch_ms(m.game_creation_ms) AS game_creation,
  m.game_duration_s,
  CAST(json_extract(p.value, '$.participantId') AS INTEGER) AS participant_id,
  CAST(json_extract(p.value, '$.championId') AS INTEGER) AS champion_id,
  CAST(json_extract(p.value, '$.teamId') AS INTEGER) AS team_id,
  %s AS role,
  json_extract_string(p.value, '$.teamPosition') AS team_position,
  json_extract_string(p.value, '$.individualPosition') AS individual_position,
  CAST(json_extract(p.value, '$.win') AS BOOLEAN) AS win
FROM %s m,
     UNNEST(from_json(json_extract(m.payload, '$.info.participants'), '["JSON"]')) AS p(value)
WHERE m.payload IS NOT NULL`, role, parquetOf(matchesPath))
}

// framesExpr renders the frames array of a timeline payload as a list of JSON
// values, empty when the game has no frames at all.
//
// The COALESCE is what makes an aborted game a row count of zero instead of a
// DuckDB parse error. An aborted game carries `frameInterval: 0` and either
// `frames: []` or no frames member at all (developer-relations#898), and
// from_json on a NULL argument is not the same as from_json on '[]'. The cast
// to VARCHAR first is deliberate: from_json takes a JSON or VARCHAR argument,
// and handing it an expression whose type is JSON-or-VARCHAR depending on the
// payload is what the cast removes.
func framesExpr(payload string) string {
	return "from_json(COALESCE(CAST(json_extract(" + payload + ", '$.info.frames') AS VARCHAR), '[]'), '[\"JSON\"]')"
}

// timelineFramesSQL unnests frames[].participantFrames into one row per
// (match, participant, frame).
//
// minute is derived from the frame's timestamp and never from its position in
// the array. Frames stopped being evenly spaced around patch 16.1 - the gap is
// observed between ~60.5 s and ~71.3 s rather than 60 s
// (developer-relations#1129) - so counting frames would put "minute 10" up to
// ten per cent early, which is exactly the error a CS@10 analysis cannot
// absorb. Both the timestamp and the derived minute are published so the
// irregularity is visible in the data instead of smoothed away by it.
//
// participantFrames is an object keyed by the string form of the 1-based
// participantId, which is why json_each is used and why its key is cast for the
// join. A frame whose participantFrames is null - the aborted-game shape that
// sits beside an empty frames array - produces no rows rather than a NULL row,
// so the tables below never carry a participant-shaped hole.
func timelineFramesSQL(matchesPath string) string {
	participantFrame := func(path, kind string) string {
		return "CAST(json_extract(pf.value, '" + path + "') AS " + kind + ")"
	}
	return fmt.Sprintf(`SELECT
  m.match_id,
  m.region,
  m.patch,
  m.queue_id,
  CAST(pf.key AS INTEGER) AS participant_id,
  CAST(json_extract(f.value, '$.timestamp') AS BIGINT) AS frame_timestamp_ms,
  CAST(floor(CAST(json_extract(f.value, '$.timestamp') AS BIGINT) / 60000.0) AS INTEGER) AS minute,
  %s AS level,
  %s AS xp,
  %s AS total_gold,
  %s AS current_gold,
  %s AS minions_killed,
  %s AS jungle_minions_killed,
  %s + %s AS cs_total,
  %s AS position_x,
  %s AS position_y
FROM %s m,
     UNNEST(%s) AS f(value),
     json_each(json_extract(f.value, '$.participantFrames')) AS pf
WHERE m.payload IS NOT NULL`,
		participantFrame("$.level", "INTEGER"),
		participantFrame("$.xp", "BIGINT"),
		participantFrame("$.totalGold", "INTEGER"),
		participantFrame("$.currentGold", "INTEGER"),
		participantFrame("$.minionsKilled", "INTEGER"),
		participantFrame("$.jungleMinionsKilled", "INTEGER"),
		participantFrame("$.minionsKilled", "INTEGER"),
		participantFrame("$.jungleMinionsKilled", "INTEGER"),
		participantFrame("$.position.x", "INTEGER"),
		participantFrame("$.position.y", "INTEGER"),
		parquetOf(matchesPath),
		framesExpr("m.payload"))
}

// timelineEventsSQL unnests frames[].events into one wide row per event.
//
// One wide table rather than twenty narrow ones, because the consumer is an
// analyst writing SQL: the person asking "what happened at minute three" wants
// one relation to filter, not a union over event shapes. Every column is NULL
// for the event types that do not carry it, and that NULL means "this event
// type has no such field" - it is never a substituted zero.
//
// The 0 sentinel is preserved rather than converted. Riot uses participantId 0
// for "no participant" - a minion, a turret, an execute - and collapsing it to
// NULL would throw away the distinction between "killed by a minion" and "we do
// not know who did it". The one place that does treat 0 as absence is the KDA
// accumulation, where a minion kill must not enter a summoner's death count;
// that is a decision about one calculation, and it is made there.
//
// is_duplicate_skill_level_up marks the Patch 15.17 defect
// (developer-relations#1100) rather than dropping the row: SKILL_LEVEL_UP is
// emitted twice for the same (participant, slot, level), and a consumer
// reconstructing a skill order has to decide whether to collapse it. Marking
// leaves that decision where it belongs, and leaves the count of how often it
// happened in the data.
//
// victimDamageDealt and victimDamageReceived are deliberately not unfolded.
// They are per-event arrays of per-participant damage packets, by far the
// largest part of a timeline, and nothing in this dataset aggregates them; an
// analyst who needs them reads the raw archive, which the README says so.
func timelineEventsSQL(matchesPath string) string {
	value := func(path, kind string) string {
		return "CAST(json_extract(e.value, '" + path + "') AS " + kind + ")"
	}
	text := func(path string) string {
		return "json_extract_string(e.value, '" + path + "')"
	}
	timestamp := "CAST(json_extract(e.value, '$.timestamp') AS BIGINT)"
	return fmt.Sprintf(`SELECT
  match_id,
  region,
  patch,
  queue_id,
  CAST(row_number() OVER (PARTITION BY match_id ORDER BY frame_index, event_in_frame) - 1 AS INTEGER) AS event_index,
  timestamp_ms,
  minute,
  event_type,
  actor_participant_id,
  target_participant_id,
  assisting_participant_ids,
  team_id,
  position_x,
  position_y,
  lane_type,
  monster_type,
  monster_subtype,
  building_type,
  tower_type,
  ward_type,
  item_id,
  before_item_id,
  after_item_id,
  skill_slot,
  level,
  kill_type,
  multi_kill_length,
  transform_type,
  kill_streak_length,
  bounty,
  shutdown_bounty,
  winning_team,
  (event_type = 'SKILL_LEVEL_UP'
    AND row_number() OVER (
      PARTITION BY match_id, actor_participant_id, skill_slot, level
      ORDER BY frame_index, event_in_frame) > 1) AS is_duplicate_skill_level_up
FROM (
  SELECT
    m.match_id,
    m.region,
    m.patch,
    m.queue_id,
    f.frame_index,
    e.event_index AS event_in_frame,
    %s AS timestamp_ms,
    CAST(floor(%s / 60000.0) AS INTEGER) AS minute,
    %s AS event_type,
    COALESCE(%s, %s, %s) AS actor_participant_id,
    %s AS target_participant_id,
    %s AS assisting_participant_ids,
    %s AS team_id,
    %s AS position_x,
    %s AS position_y,
    %s AS lane_type,
    %s AS monster_type,
    COALESCE(%s, %s) AS monster_subtype,
    %s AS building_type,
    %s AS tower_type,
    %s AS ward_type,
    %s AS item_id,
    %s AS before_item_id,
    %s AS after_item_id,
    %s AS skill_slot,
    %s AS level,
    %s AS kill_type,
    %s AS multi_kill_length,
    %s AS transform_type,
    %s AS kill_streak_length,
    %s AS bounty,
    %s AS shutdown_bounty,
    %s AS winning_team
  FROM %s m,
       UNNEST(%s) WITH ORDINALITY AS f(value, frame_index),
       UNNEST(from_json(COALESCE(CAST(json_extract(f.value, '$.events') AS VARCHAR), '[]'), '["JSON"]'))
         WITH ORDINALITY AS e(value, event_index)
  WHERE m.payload IS NOT NULL
) extracted`,
		timestamp,
		timestamp,
		text("$.type"),
		value("$.participantId", "INTEGER"),
		value("$.killerId", "INTEGER"),
		value("$.creatorId", "INTEGER"),
		value("$.victimId", "INTEGER"),
		value("$.assistingParticipantIds", "INTEGER[]"),
		value("$.teamId", "INTEGER"),
		value("$.position.x", "INTEGER"),
		value("$.position.y", "INTEGER"),
		text("$.laneType"),
		text("$.monsterType"),
		text("$.monsterSubType"),
		text("$.soulType"),
		text("$.buildingType"),
		text("$.towerType"),
		text("$.wardType"),
		value("$.itemId", "INTEGER"),
		value("$.beforeId", "INTEGER"),
		value("$.afterId", "INTEGER"),
		value("$.skillSlot", "INTEGER"),
		value("$.level", "INTEGER"),
		text("$.killType"),
		value("$.multiKillLength", "INTEGER"),
		text("$.transformType"),
		value("$.killStreakLength", "INTEGER"),
		value("$.bounty", "INTEGER"),
		value("$.shutdownBounty", "INTEGER"),
		value("$.winningTeam", "INTEGER"),
		parquetOf(matchesPath),
		framesExpr("m.payload"))
}

// killPointsSQL turns the CHAMPION_KILL rows of the events spill into long-form
// kill/death/assist points, one row per participant per event they took part
// in.
//
// Long form is what makes the running totals a single windowed sum instead of
// three correlated subqueries over a wide table. The alternative - counting
// kills, deaths and assists separately and joining them per frame - needs three
// separate cumulative joins and three chances to disagree about what "at or
// before this frame" means; one relation with one ordering cannot disagree with
// itself.
//
// Assists come from assistingParticipantIds, which is a list, so one kill
// contributes a row per assister and the count is per participant as it should
// be. Participant 0 is filtered out of every branch: it is Riot's sentinel for
// a minion, a turret or an execute, and it must not become an eleventh
// participant that accumulates deaths.
func killPointsSQL(eventsPath string) string {
	branch := func(participant, kills, deaths, assists string) string {
		return fmt.Sprintf(`  SELECT
    match_id,
    %s AS participant_id,
    timestamp_ms,
    %s AS kills,
    %s AS deaths,
    %s AS assists
  FROM %s
  WHERE event_type = 'CHAMPION_KILL' AND %s > 0`, participant, kills, deaths, assists,
			parquetOf(eventsPath), participant)
	}
	return fmt.Sprintf(`SELECT match_id, participant_id, timestamp_ms, kills, deaths, assists
FROM (
%s
  UNION ALL
%s
  UNION ALL
  SELECT
    match_id,
    CAST(unnest(assisting_participant_ids) AS INTEGER) AS participant_id,
    timestamp_ms,
    0 AS kills,
    0 AS deaths,
    1 AS assists
  FROM %s
  WHERE event_type = 'CHAMPION_KILL' AND length(assisting_participant_ids) > 0
) points`,
		branch("actor_participant_id", "1", "0", "0"),
		branch("target_participant_id", "0", "1", "0"),
		parquetOf(eventsPath))
}

// checkpointPicksSQL picks, for every participant of every match, the frame
// that represents the 5, 10 and 15 minute checkpoints.
//
// It is a statement of its own, and a file, because three tables need the same
// answer and a second copy of this rule would be a second definition of "CS at
// 10" - which is precisely the number the dataset exists to publish.
//
// The rule is "the frame nearest the checkpoint within a 30-second grace,
// because frames are not evenly spaced since patch 16.1
// (developer-relations#1129), and no value at all when the game's last frame is
// before the checkpoint". Both halves matter:
//
//   - Without the grace window a checkpoint lands on a frame that does not
//     exist and the value becomes NULL for a game that plainly reached minute
//     ten, which is missing data invented by the query.
//   - Without the last-frame gate a game that ended at minute three would be
//     answered with its minute-three frame, which is missing data turned into a
//     wrong number - the failure mode that is worse than the NULL.
//
// The grace is capped at 30 seconds above the frame before the checkpoint,
// because the observed spacing is between ~60.5 s and ~71.3 s. It can therefore
// select a frame up to 30 s after the minute mark, and never one from two
// minutes earlier.
func checkpointPicksSQL(minutesPath string) string {
	return fmt.Sprintf(`WITH frames AS (
  SELECT match_id, participant_id, frame_timestamp_ms, cs_total, xp, total_gold, level,
         kills_to_minute, deaths_to_minute, assists_to_minute
  FROM %s
),
checkpoints AS (SELECT * FROM (VALUES (5), (10), (15)) AS t(checkpoint)),
last_frame AS (
  SELECT match_id, max(frame_timestamp_ms) AS last_frame_ms FROM frames GROUP BY match_id
),
nearest AS (
  SELECT
    f.match_id,
    f.participant_id,
    c.checkpoint,
    f.frame_timestamp_ms,
    f.cs_total,
    f.xp,
    f.total_gold,
    f.level,
    f.kills_to_minute,
    f.deaths_to_minute,
    f.assists_to_minute,
    row_number() OVER (
      PARTITION BY f.match_id, f.participant_id, c.checkpoint
      ORDER BY abs(f.frame_timestamp_ms - c.checkpoint * 60000), f.frame_timestamp_ms) AS pick
  FROM frames f
  JOIN checkpoints c
    ON f.frame_timestamp_ms BETWEEN c.checkpoint * 60000 - 60000 AND c.checkpoint * 60000 + 30000
  JOIN last_frame l
    ON l.match_id = f.match_id AND c.checkpoint * 60000 <= l.last_frame_ms
)
SELECT
  match_id,
  participant_id,
  checkpoint,
  frame_timestamp_ms,
  cs_total,
  xp,
  total_gold,
  level,
  kills_to_minute,
  deaths_to_minute,
  assists_to_minute
FROM nearest
WHERE pick = 1`, parquetOf(minutesPath))
}

// laneMatchupsSQL is the table that answers the question the dataset was asked
// for: one row per (match, participant) paired with the opposing laner, and the
// CS, XP, gold and level each of the two held at minutes 5, 10 and 15, plus
// their running kills, deaths and assists.
//
// The pairing is by role, and the role is the assignment (teamPosition) rather
// than the detection (individualPosition). That is what makes "the opposing
// laner" a person: teamPosition is constrained to exactly one TOP, one JUNGLE,
// one MIDDLE, one BOTTOM and one UTILITY per team, so the join is a function.
// pairing_basis records that this is an inferred pairing and not an observed
// one, because a reader has to know that before treating it as ground truth.
//
// NULL is never imputed. A checkpoint the game never reached yields NULL, not
// zero and not the previous frame carried forward, because this table is going
// to be regressed on and "0 CS at 10" and "we do not know" must not be the same
// value. A difference column is NULL whenever either side of it is, which is
// the arithmetic doing the same thing.
//
// Jungle and the duo lane are flagged rather than merged into the pairing. A
// jungler has a nominal opposite jungler but no lane, and BOTTOM+UTILITY is a
// two-versus-two lane in which a single "lane opponent" is ill-defined;
// has_nominal_opponent and is_duo_lane let an analyst choose whether to include
// them instead of discovering the ambiguity after a number is published.
func laneMatchupsSQL(matchIndexPath, participantsPath, checkpointsPath string) string {
	var columns, joins []string
	for _, minute := range []int{5, 10, 15} {
		mine, theirs := fmt.Sprintf("c%d", minute), fmt.Sprintf("o%d", minute)
		joins = append(joins,
			fmt.Sprintf("LEFT JOIN chk %s ON %s.match_id = pr.match_id AND %s.participant_id = pr.participant_id AND %s.checkpoint = %d",
				mine, mine, mine, mine, minute),
			fmt.Sprintf("LEFT JOIN chk %s ON %s.match_id = pr.match_id AND %s.participant_id = pr.opponent_participant_id AND %s.checkpoint = %d",
				theirs, theirs, theirs, theirs, minute))
		pairs := [][2]string{
			{mine + ".cs_total", fmt.Sprintf("cs_%d", minute)},
			{theirs + ".cs_total", fmt.Sprintf("opponent_cs_%d", minute)},
			{fmt.Sprintf("%s.cs_total - %s.cs_total", mine, theirs), fmt.Sprintf("cs_diff_%d", minute)},
			{fmt.Sprintf("%s.xp - %s.xp", mine, theirs), fmt.Sprintf("xp_diff_%d", minute)},
			{fmt.Sprintf("%s.total_gold - %s.total_gold", mine, theirs), fmt.Sprintf("gold_diff_%d", minute)},
			{fmt.Sprintf("%s.level - %s.level", mine, theirs), fmt.Sprintf("level_diff_%d", minute)},
			{mine + ".kills_to_minute", fmt.Sprintf("kills_%d", minute)},
			{mine + ".deaths_to_minute", fmt.Sprintf("deaths_%d", minute)},
			{mine + ".assists_to_minute", fmt.Sprintf("assists_%d", minute)},
		}
		for _, pair := range pairs {
			columns = append(columns, fmt.Sprintf("%s AS %s", pair[0], pair[1]))
		}
	}
	return fmt.Sprintf(`WITH p AS (
  SELECT pa.*
  FROM %s pa
  JOIN %s mi ON mi.match_id = pa.match_id AND mi.timeline_eligible
),
pairs AS (
  SELECT
    a.match_id,
    a.region,
    a.patch,
    a.queue_id,
    a.participant_id,
    b.participant_id AS opponent_participant_id,
    a.role,
    a.champion_id,
    b.champion_id AS opponent_champion_id,
    a.team_id,
    a.win,
    'team_position' AS pairing_basis,
    a.role IN ('BOTTOM', 'SUPPORT') AS is_duo_lane,
    a.role <> 'JUNGLE' AS has_nominal_opponent
  FROM p a
  JOIN p b ON b.match_id = a.match_id AND b.role = a.role AND b.team_id <> a.team_id
  WHERE a.role IS NOT NULL
),
chk AS (SELECT * FROM %s)
SELECT
  pr.match_id,
  pr.region,
  pr.patch,
  pr.queue_id,
  pr.participant_id,
  pr.opponent_participant_id,
  pr.role,
  pr.champion_id,
  pr.opponent_champion_id,
  pr.team_id,
  pr.win,
  pr.pairing_basis,
  pr.is_duo_lane,
  pr.has_nominal_opponent,
  %s
FROM pairs pr
%s`,
		parquetOf(participantsPath), parquetOf(matchIndexPath),
		parquetOf(checkpointsPath),
		strings.Join(columns, ",\n  "),
		strings.Join(joins, "\n"))
}

// eventActorsSQL turns the wide events table into one row per participant per
// event they took part in.
//
// It exists because the alternative is five correlated subqueries over a wide
// NULL-rich relation, one per participant-side fact, each with its own idea of
// what "the participant this event belongs to" means. Here it is stated once:
// actor is the participant who did it (participantId, killerId or creatorId,
// whichever the event type carries), target is the victim, and assist is each
// member of assistingParticipantIds.
//
// Participant 0 does not appear. It is Riot's sentinel for "no participant" -
// a minion, a turret, an execute - and it is kept in the events table so that
// "killed by a minion" stays distinguishable from "unknown", but it is not a
// participant and must not accumulate deaths in a per-participant aggregate.
func eventActorsSQL(eventsPath string) string {
	const shared = `match_id,
    event_index,
    event_type,
    timestamp_ms,
    minute,
    team_id,
    lane_type,
    ward_type,
    monster_type,
    monster_subtype,
    skill_slot,
    level,
    item_id,
    position_x,
    position_y,
    is_duplicate_skill_level_up`
	participant := func(column, role string) string {
		return fmt.Sprintf(`  SELECT
    %s,
    %s AS participant_id,
    '%s' AS role_in_event
  FROM %s
  WHERE %s > 0`, shared, column, role, parquetOf(eventsPath), column)
	}
	return fmt.Sprintf(`SELECT * FROM (
%s
  UNION ALL
%s
  UNION ALL
  SELECT
    %s,
    CAST(unnest(assisting_participant_ids) AS INTEGER) AS participant_id,
    'assist' AS role_in_event
  FROM %s
  WHERE length(assisting_participant_ids) > 0
) event_participants`,
		participant("actor_participant_id", "actor"),
		participant("target_participant_id", "target"),
		shared,
		parquetOf(eventsPath))
}

// participantEarlySQL is one row per (match, participant) of early-game facts:
// first blood, the first death and where it happened, plates, item timings, the
// skill order, wards and objective participation.
//
// It is a convenience projection, and it says so in schema.json: the
// authoritative timestamped sequence of every one of these facts is a query
// over the events table, and the columns here are the ones an analyst reaches
// for often enough to deserve a column. Nobody should treat skill_order as the
// source of truth for a skill order, which is why the README repeats it.
//
// Two defect-driven decisions are visible in the columns:
//
//   - skill_order drops the duplicate SKILL_LEVEL_UP events of patch 15.17
//     (developer-relations#1100) instead of publishing an inflated sequence,
//     and duplicate_skill_ups counts how many were dropped, so a consumer can
//     tell a clean order from a repaired one.
//   - first_item_ts_ms ignores ITEM_PURCHASED events with participantId 0 and
//     item ids of 0 (developer-relations#1069, #1174), which otherwise produce
//     a purchase at time zero by nobody.
func participantEarlySQL(matchIndexPath, participantsPath, actorsPath, checkpointsPath string) string {
	return fmt.Sprintf(`WITH p AS (
  SELECT pa.match_id, pa.region, pa.patch, pa.queue_id, pa.participant_id, pa.champion_id,
         pa.role, pa.team_id, pa.win
  FROM %s pa
  JOIN %s mi ON mi.match_id = pa.match_id AND mi.timeline_eligible
),
a AS (SELECT * FROM %s),
involvement AS (
  SELECT
    match_id,
    participant_id,
    count(*) FILTER (WHERE event_type = 'WARD_PLACED' AND role_in_event = 'actor') AS wards_placed,
    count(*) FILTER (WHERE event_type = 'WARD_KILL' AND role_in_event = 'actor') AS wards_killed,
    count(*) FILTER (WHERE event_type = 'TURRET_PLATE_DESTROYED' AND role_in_event = 'actor') AS plates_destroyed,
    count(*) FILTER (WHERE event_type = 'ELITE_MONSTER_KILL' AND monster_type = 'DRAGON'
                       AND role_in_event IN ('actor', 'assist')) AS dragons_participated,
    count(*) FILTER (WHERE event_type = 'ELITE_MONSTER_KILL' AND monster_type = 'HORDE'
                       AND role_in_event IN ('actor', 'assist')) AS grubs_participated,
    count(*) FILTER (WHERE event_type = 'ELITE_MONSTER_KILL' AND monster_type = 'RIFTHERALD'
                       AND role_in_event IN ('actor', 'assist')) AS heralds_participated,
    count(*) FILTER (WHERE event_type = 'ELITE_MONSTER_KILL' AND monster_type = 'BARON_NASHOR'
                       AND role_in_event IN ('actor', 'assist')) AS barons_participated,
    count(*) FILTER (WHERE event_type = 'ELITE_MONSTER_KILL' AND monster_type = 'ATAKHAN'
                       AND role_in_event IN ('actor', 'assist')) AS atakhan_participated,
    count(*) FILTER (WHERE event_type = 'SKILL_LEVEL_UP' AND role_in_event = 'actor'
                       AND is_duplicate_skill_level_up) AS duplicate_skill_ups,
    min(timestamp_ms) FILTER (WHERE event_type = 'CHAMPION_KILL' AND role_in_event = 'target')
      AS first_death_ts_ms,
    arg_min(position_x, timestamp_ms) FILTER (WHERE event_type = 'CHAMPION_KILL' AND role_in_event = 'target')
      AS first_death_position_x,
    arg_min(position_y, timestamp_ms) FILTER (WHERE event_type = 'CHAMPION_KILL' AND role_in_event = 'target')
      AS first_death_position_y
  FROM a
  GROUP BY 1, 2
),
first_blood_event AS (
  SELECT match_id, min(event_index) AS event_index
  FROM a
  WHERE event_type = 'CHAMPION_KILL'
  GROUP BY match_id
),
first_blood AS (
  SELECT a.match_id, a.participant_id, TRUE AS first_blood_involvement
  FROM a
  JOIN first_blood_event fb ON fb.match_id = a.match_id AND fb.event_index = a.event_index
  WHERE a.role_in_event IN ('actor', 'assist')
),
skills AS (
  SELECT
    match_id,
    participant_id,
    string_agg(CAST(skill_slot AS VARCHAR), '' ORDER BY timestamp_ms, event_index) AS skill_order
  FROM a
  WHERE event_type = 'SKILL_LEVEL_UP' AND role_in_event = 'actor'
    AND NOT is_duplicate_skill_level_up AND skill_slot IS NOT NULL
  GROUP BY 1, 2
),
item_sequence AS (
  SELECT
    match_id,
    participant_id,
    timestamp_ms,
    item_id,
    row_number() OVER (PARTITION BY match_id, participant_id ORDER BY timestamp_ms, event_index) AS seq
  FROM a
  WHERE event_type = 'ITEM_PURCHASED' AND role_in_event = 'actor' AND item_id > 0
),
items AS (
  SELECT
    match_id,
    participant_id,
    min(timestamp_ms) FILTER (WHERE seq = 1) AS first_item_ts_ms,
    min(item_id) FILTER (WHERE seq = 1) AS first_item_id,
    min(timestamp_ms) FILTER (WHERE seq = 2) AS second_item_ts_ms,
    min(item_id) FILTER (WHERE seq = 2) AS second_item_id
  FROM item_sequence
  GROUP BY 1, 2
),
plate_cum AS (
  SELECT
    match_id,
    participant_id,
    timestamp_ms,
    count(*) OVER (
      PARTITION BY match_id, participant_id
      ORDER BY timestamp_ms ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS plates
  FROM a
  WHERE event_type = 'TURRET_PLATE_DESTROYED' AND role_in_event = 'actor'
),
plates_at AS (
  SELECT
    c.match_id,
    c.participant_id,
    max(COALESCE(pc.plates, 0)) FILTER (WHERE c.checkpoint = 5) AS plates_at_5,
    max(COALESCE(pc.plates, 0)) FILTER (WHERE c.checkpoint = 10) AS plates_at_10,
    max(COALESCE(pc.plates, 0)) FILTER (WHERE c.checkpoint = 15) AS plates_at_15
  FROM %s c
  ASOF LEFT JOIN plate_cum pc
    ON pc.match_id = c.match_id AND pc.participant_id = c.participant_id
   AND c.frame_timestamp_ms >= pc.timestamp_ms
  GROUP BY 1, 2
)
SELECT
  p.match_id,
  p.region,
  p.patch,
  p.queue_id,
  p.participant_id,
  p.champion_id,
  p.role,
  p.team_id,
  p.win,
  COALESCE(fb.first_blood_involvement, FALSE) AS first_blood_involvement,
  inv.first_death_ts_ms,
  CAST(floor(inv.first_death_ts_ms / 60000.0) AS INTEGER) AS first_death_minute,
  inv.first_death_position_x,
  inv.first_death_position_y,
  COALESCE(inv.plates_destroyed, 0) AS plates_destroyed,
  pa.plates_at_5,
  pa.plates_at_10,
  pa.plates_at_15,
  it.first_item_ts_ms,
  it.first_item_id,
  it.second_item_ts_ms,
  it.second_item_id,
  s.skill_order,
  COALESCE(inv.duplicate_skill_ups, 0) AS duplicate_skill_ups,
  COALESCE(inv.wards_placed, 0) AS wards_placed,
  COALESCE(inv.wards_killed, 0) AS wards_killed,
  COALESCE(inv.dragons_participated, 0) AS dragons_participated,
  COALESCE(inv.grubs_participated, 0) AS grubs_participated,
  COALESCE(inv.heralds_participated, 0) AS heralds_participated,
  COALESCE(inv.barons_participated, 0) AS barons_participated,
  COALESCE(inv.atakhan_participated, 0) AS atakhan_participated
FROM p
LEFT JOIN involvement inv ON inv.match_id = p.match_id AND inv.participant_id = p.participant_id
LEFT JOIN first_blood fb ON fb.match_id = p.match_id AND fb.participant_id = p.participant_id
LEFT JOIN skills s ON s.match_id = p.match_id AND s.participant_id = p.participant_id
LEFT JOIN items it ON it.match_id = p.match_id AND it.participant_id = p.participant_id
LEFT JOIN plates_at pa ON pa.match_id = p.match_id AND pa.participant_id = p.participant_id`,
		parquetOf(participantsPath), parquetOf(matchIndexPath), parquetOf(actorsPath),
		parquetOf(checkpointsPath))
}

// matchIndexSQL is the eligibility ledger: one row per match the scope covers,
// with the reason the match is or is not in the working set.
//
// It is the most important table in the dataset for trust, because it is what
// makes every filter this build applies reversible. The reasons are ordered so
// that each match has exactly one, and they are read in the order a person
// would ask: is the summary readable, is there a timeline at all, is the
// timeline readable, did the game run, was it long enough, do the frames exist.
//
// "do the frames exist" is answered by participant_frame_count and not by the
// frame count, because Riot returns frames whose participantFrames are null on
// an aborted game: a payload can carry ten frames, a positive frameInterval and
// not one per-participant record. Classifying on the array members alone would
// call such a game eligible and then publish a lane_matchups row in which every
// checkpoint is NULL, which is exactly the kind of plausible-but-empty row the
// ledger exists to prevent.
//
// It reads only the two envelope spills - no payload is touched to build it -
// which is also why it is written before the payload batches: coverage is worth
// knowing even when the extraction later fails.
//
// The scope predicate is applied to the summary side and to the summary side
// only, because the summary is what defines the row set: a match is in this
// dataset because the archive holds its summary, and its timeline only says
// whether the per-minute tables can have rows for it. That is also why an
// unreadable row cannot appear here - the envelope spills carry every row the
// archive holds and the archive gate refuses a build in which one of them has
// no identity (see checkGates), so by the time this statement runs, every row
// it can see has a match id and a payload the parser could read.
//
// The duration floor is the same floor the crawl used to choose the sample
// (ADR-012), passed in rather than hardcoded so the ledger and the crawl cannot
// disagree about what "too short" means.
func matchIndexSQL(envelopePath, timelineEnvelopePath, scope string, minDurationS int) string {
	return fmt.Sprintf(`WITH s AS (
  SELECT * FROM %s
  %s
),
t AS (
  SELECT * FROM %s
),
graded AS (
  SELECT
    s.match_id,
    s.region,
    s.platform_id,
    s.patch,
    s.queue_id,
    s.game_version,
    epoch_ms(s.game_creation_ms) AS game_creation,
    s.game_duration_s,
    s.end_of_game_result,
    t.frame_interval_ms,
    t.frame_count,
    t.participant_frame_count,
    CASE
      WHEN t.match_id IS NULL THEN 'no_timeline'
      WHEN t.frame_interval_ms = 0 AND COALESCE(t.participant_frame_count, 0) = 0 THEN 'aborted'
      WHEN t.frame_interval_ms = 0 THEN 'frame_interval_zero'
      WHEN COALESCE(s.game_duration_s, 0) < %d THEN 'too_short'
      WHEN COALESCE(t.participant_frame_count, 0) = 0 THEN 'frames_null'
      ELSE 'ok'
    END AS exclusion_reason,
    t.match_id IS NOT NULL AS timeline_present
  FROM s
  LEFT JOIN t ON t.match_id = s.match_id
)
SELECT
  match_id,
  region,
  platform_id,
  patch,
  queue_id,
  game_version,
  game_creation,
  game_duration_s,
  end_of_game_result,
  frame_interval_ms,
  frame_count,
  participant_frame_count,
  timeline_present,
  exclusion_reason,
  exclusion_reason = 'ok' AS timeline_eligible
FROM graded`, parquetOf(envelopePath), scope, parquetOf(timelineEnvelopePath), minDurationS)
}

// participantMinutesSQL is one row per (match, participant, frame): the
// per-minute economy curve with the running KDA at that minute beside it.
//
// Every minute the payload carries is kept, not only the three checkpoints.
// Ten participants over a full game is a few hundred rows per match, which is
// nothing for a Parquet file, and restricting the range would force a re-crawl
// for the first question nobody anticipated.
//
// Running KDA is "at or before this frame's timestamp", accumulated in one
// windowed sum over the long-form kill points. The frame's own timestamp is the
// comparison, not the minute, because two frames can share a minute and a kill
// in the second half of that minute must not be counted in the first frame of
// it - and because the frame timestamps are irregular, which is exactly why
// they are carried on every row.
//
// The running sums are cast to BIGINT. DuckDB widens a sum of integers to
// HUGEINT, and its Parquet writer has no 128-bit integer type, so a HUGEINT
// column this build writes comes back as DOUBLE when the table is read again.
// A cumulative kill, death or assist count never approaches the range of a
// 64-bit integer, so BIGINT is both exact and what the schema document
// promises; checkSchema refuses the build if a later aggregate reintroduces the
// widening.
func participantMinutesSQL(matchIndexPath, participantsPath, framesPath, killPointsPath string) string {
	return fmt.Sprintf(`WITH p AS (
  SELECT pa.*
  FROM %s pa
  JOIN %s mi ON mi.match_id = pa.match_id AND mi.timeline_eligible
),
f AS (SELECT * FROM %s),
merged AS (
  SELECT
    match_id,
    participant_id,
    timestamp_ms,
    sum(kills) AS kills,
    sum(deaths) AS deaths,
    sum(assists) AS assists
  FROM %s
  GROUP BY 1, 2, 3
),
running AS (
  SELECT
    match_id,
    participant_id,
    timestamp_ms,
    CAST(sum(kills) OVER (
      PARTITION BY match_id, participant_id ORDER BY timestamp_ms
      ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS BIGINT) AS kills_to_minute,
    CAST(sum(deaths) OVER (
      PARTITION BY match_id, participant_id ORDER BY timestamp_ms
      ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS BIGINT) AS deaths_to_minute,
    CAST(sum(assists) OVER (
      PARTITION BY match_id, participant_id ORDER BY timestamp_ms
      ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS BIGINT) AS assists_to_minute
  FROM merged
)
SELECT
  f.match_id,
  f.participant_id,
  p.champion_id,
  p.role,
  p.team_id,
  p.win,
  f.frame_timestamp_ms,
  f.minute,
  f.level,
  f.xp,
  f.total_gold,
  f.current_gold,
  f.minions_killed,
  f.jungle_minions_killed,
  f.cs_total,
  f.position_x,
  f.position_y,
  COALESCE(r.kills_to_minute, 0) AS kills_to_minute,
  COALESCE(r.deaths_to_minute, 0) AS deaths_to_minute,
  COALESCE(r.assists_to_minute, 0) AS assists_to_minute,
  p.patch,
  p.region,
  p.queue_id
FROM f
JOIN p ON p.match_id = f.match_id AND p.participant_id = f.participant_id
ASOF LEFT JOIN running r
  ON r.match_id = f.match_id AND r.participant_id = f.participant_id
 AND f.frame_timestamp_ms >= r.timestamp_ms`,
		parquetOf(participantsPath), parquetOf(matchIndexPath),
		parquetOf(framesPath), parquetOf(killPointsPath))
}

// eventsTableSQL is the wide event table, scoped to the matches that have a
// timeline and carrying the scope columns an analyst filters on.
//
// The scope columns come from match_index rather than being carried through the
// extraction, so the events table and the ledger cannot disagree about which
// patch a match belongs to.
func eventsTableSQL(matchIndexPath, eventsPath string) string {
	return fmt.Sprintf(`SELECT
  e.match_id,
  mi.region,
  mi.patch,
  mi.queue_id,
  e.event_index,
  e.timestamp_ms,
  e.minute,
  e.event_type,
  e.actor_participant_id,
  e.target_participant_id,
  e.assisting_participant_ids,
  e.team_id,
  e.position_x,
  e.position_y,
  e.lane_type,
  e.monster_type,
  e.monster_subtype,
  e.building_type,
  e.tower_type,
  e.ward_type,
  e.item_id,
  e.before_item_id,
  e.after_item_id,
  e.skill_slot,
  e.level,
  e.kill_type,
  e.multi_kill_length,
  e.transform_type,
  e.kill_streak_length,
  e.bounty,
  e.shutdown_bounty,
  e.winning_team,
  e.is_duplicate_skill_level_up
FROM %s e
JOIN %s mi ON mi.match_id = e.match_id AND mi.timeline_eligible`,
		parquetOf(eventsPath), parquetOf(matchIndexPath))
}

// teamCounts builds the pair of per-team count columns for one event filter, so
// the objective totals are written once and named consistently for both teams.
func teamCounts(eventFilter, name string) []string {
	var out []string
	for _, team := range []int{100, 200} {
		out = append(out, fmt.Sprintf(
			"count(*) FILTER (WHERE team_id = %d AND %s) AS %s_team_%d",
			team, eventFilter, name, team))
	}
	return out
}

// prefixed rewrites `expr AS alias` column definitions into qualified
// references to the relation those expressions were defined in, so a column
// list can be written once and both defined and selected.
func prefixed(columns []string, qualifier string) []string {
	out := make([]string, 0, len(columns))
	for _, column := range columns {
		alias := column
		if idx := strings.LastIndex(column, " AS "); idx >= 0 {
			alias = column[idx+len(" AS "):]
		}
		out = append(out, qualifier+alias)
	}
	return out
}

// matchObjectivesSQL is one row per match of objective control: who took the
// first blood, the first tower, the first dragon and the soul, and at what
// timestamp, plus each team's totals.
//
// This is the "which early events influence the outcome" table. Joined to
// lane_matchups on match_id the win column is already there, so the question
// needs no further join.
//
// Two things about it are deliberate. Timestamps are milliseconds since game
// start as Riot reports them, while the human-readable minute of the same event
// is a column in the events table - the raw value is kept here because a
// reader regressing on timing wants the unrounded number. And the attribution
// is taken exactly as reported: Riot has acknowledged that BUILDING_KILL team
// attribution flips (developer-relations#1000), that more than five plates can
// be credited to one turret (#703) and that DRAGON_SOUL_GIVEN can be emitted
// early with teamId 0 (#1026). The counts are therefore correct as a count of
// what the payload said, which is not always what happened, and the README says
// so rather than the query guessing.
//
// team_events exists because CHAMPION_KILL carries no teamId. Monster, building
// and plate events state the team that did it, but a kill states only the
// killer's participant id, so a team count over `team_id` alone would report
// zero kills for both teams on every match. The killer's team is resolved from
// the participants relation instead, which is the same identity the rest of the
// dataset joins on, and it is resolved here rather than by inventing a team_id
// on the events table: `events.team_id` stays exactly what the payload said.
func matchObjectivesSQL(matchIndexPath, eventsPath, participantsPath string) string {
	var columns []string
	for _, spec := range []struct{ filter, name string }{
		{"event_type = 'CHAMPION_KILL'", "kills"},
		{"event_type = 'ELITE_MONSTER_KILL' AND monster_type = 'DRAGON'", "dragons"},
		{"event_type = 'ELITE_MONSTER_KILL' AND monster_type = 'HORDE'", "grubs"},
		{"event_type = 'ELITE_MONSTER_KILL' AND monster_type = 'RIFTHERALD'", "heralds"},
		{"event_type = 'ELITE_MONSTER_KILL' AND monster_type = 'BARON_NASHOR'", "barons"},
		{"event_type = 'ELITE_MONSTER_KILL' AND monster_type = 'ATAKHAN'", "atakhan"},
		{"event_type = 'DRAGON_SOUL_GIVEN'", "souls"},
		{"event_type = 'BUILDING_KILL' AND tower_type IS NOT NULL", "towers"},
		{"event_type = 'TURRET_PLATE_DESTROYED'", "plates"},
	} {
		columns = append(columns, teamCounts(spec.filter, spec.name)...)
	}
	return fmt.Sprintf(`WITH mi AS (
  SELECT * FROM %s WHERE timeline_eligible
),
e AS (SELECT * FROM %s),
p AS (SELECT match_id, participant_id, team_id FROM %s),
team_events AS (
  SELECT
    e.match_id,
    e.event_index,
    e.timestamp_ms,
    e.event_type,
    e.monster_type,
    e.monster_subtype,
    e.tower_type,
    CASE WHEN e.event_type = 'CHAMPION_KILL' THEN killer.team_id ELSE e.team_id END AS team_id
  FROM e
  LEFT JOIN p killer
    ON killer.match_id = e.match_id AND killer.participant_id = e.actor_participant_id
),
per_match AS (
  SELECT
    match_id,
    arg_min(team_id, event_index) FILTER (WHERE event_type = 'CHAMPION_KILL' AND team_id IS NOT NULL)
      AS first_blood_team,
    min(timestamp_ms) FILTER (WHERE event_type = 'CHAMPION_KILL') AS first_blood_ts_ms,
    arg_min(team_id, event_index) FILTER (
      WHERE event_type = 'BUILDING_KILL' AND tower_type = 'OUTER' AND team_id IS NOT NULL)
      AS first_tower_team,
    min(timestamp_ms) FILTER (WHERE event_type = 'BUILDING_KILL' AND tower_type = 'OUTER')
      AS first_tower_ts_ms,
    arg_min(team_id, event_index) FILTER (
      WHERE event_type = 'ELITE_MONSTER_KILL' AND monster_type = 'DRAGON' AND team_id IS NOT NULL)
      AS first_dragon_team,
    min(timestamp_ms) FILTER (WHERE event_type = 'ELITE_MONSTER_KILL' AND monster_type = 'DRAGON')
      AS first_dragon_ts_ms,
    arg_min(team_id, event_index) FILTER (WHERE event_type = 'DRAGON_SOUL_GIVEN' AND team_id IS NOT NULL)
      AS soul_team,
    arg_min(monster_subtype, event_index) FILTER (WHERE event_type = 'DRAGON_SOUL_GIVEN')
      AS soul_type,
    %s
  FROM team_events
  GROUP BY match_id
)
SELECT
  mi.match_id,
  mi.region,
  mi.patch,
  mi.queue_id,
  mi.game_duration_s,
  pm.first_blood_team,
  pm.first_blood_ts_ms,
  pm.first_tower_team,
  pm.first_tower_ts_ms,
  pm.first_dragon_team,
  pm.first_dragon_ts_ms,
  pm.soul_team,
  pm.soul_type,
  %s
FROM mi
LEFT JOIN per_match pm ON pm.match_id = mi.match_id`,
		parquetOf(matchIndexPath), parquetOf(eventsPath), parquetOf(participantsPath),
		strings.Join(columns, ",\n    "),
		strings.Join(prefixed(columns, "pm."), ",\n  "))
}

// featureSQLChunkMarker
