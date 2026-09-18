package aggregate

import (
	"fmt"
	"strings"
)

// The feature dataset's schema and README.
//
// Both documents are generated from one specification - featureSchemaColumns -
// which is also the specification the build gate checks the built tables
// against. That is deliberate: a hand-written schema document is a second place
// to be wrong, and the failure mode is silent, because a column described but
// never built looks exactly like a column built but never queried. Here the
// document is derived from the specification and the specification is checked
// against DESCRIBE output, so the three agree or the build refuses.
//
// The types below are the types DuckDB reports, with the nullability suffix
// dropped. They are the types the SQL casts to rather than the types JSON
// happens to hold: json_extract returns JSON, and an uncast JSON column would
// be a relation nobody can compute on.

// featureColumn is one column of one table, as documented to a reader.
type featureColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Unit is the unit of a numeric column - "milliseconds", "minutes",
	// "gold", or empty when the column is a count, an identity or a flag.
	// A number without a unit is the most common way a dataset is misread.
	Unit string `json:"unit,omitempty"`
	// Semantics says what the column means, and where a value can be NULL, why
	// it is NULL. It is written for the analyst, not for the compiler.
	Semantics string `json:"semantics"`
}

// featureTableSchema is one table of the dataset, as documented.
type featureTableSchema struct {
	Name    string          `json:"name"`
	Rows    int             `json:"rows"`
	Columns []featureColumn `json:"columns"`
}

// featureSchemaDoc is schema.json.
type featureSchemaDoc struct {
	Dataset     string               `json:"dataset"`
	GeneratedBy string               `json:"generated_by"`
	Scope       FeatureScope         `json:"scope"`
	Notice      string               `json:"notice"`
	Tables      []featureTableSchema `json:"tables"`
}

const featureSchemaNotice = "One row of this document per column of every table, in the order the " +
	"build writes them. Types are the types DuckDB reports. This describes the dataset; it does not " +
	"promise those columns to a future build - a reshape publishes timeline-v2."

// featureSchemaColumns is the dataset's column specification, keyed by table and
// ordered as the SQL projects each table.
//
// It is the single source for three things: the published schema.json, the
// README's table of contents, and the gate that compares every built table
// against what is documented here. A column added to the SQL and forgotten here
// fails the build, which is the only way this document stays true.
var featureSchemaColumns = map[string][]featureColumn{
	"match_index": {
		{"match_id", "VARCHAR", "", "Riot match id. The join key of every other table, and unique in this one."},
		{"region", "VARCHAR", "", "Regional routing value from the raw archive (e.g. EUW)."},
		{"platform_id", "VARCHAR", "", "Platform the match was played on, as the summary payload reports it."},
		{"patch", "VARCHAR", "", "Two-component game version (e.g. 16.18), echoed rather than derived from game_version."},
		{"queue_id", "INTEGER", "", "Riot queue id; 420 is ranked solo, which is the queue the sample was drawn from."},
		{"game_version", "VARCHAR", "", "Full client version string, for a minor-patch split the patch column hides."},
		{"game_creation", "TIMESTAMP", "", "Game creation time, from game_creation_ms. UTC."},
		{"game_duration_s", "INTEGER", "seconds", "Game duration as the summary reports it."},
		{"end_of_game_result", "VARCHAR", "", "info.endOfGameResult verbatim. Riot does not document its values (#890), so treat anything other than GameComplete as suspicious rather than as a category."},
		{"frame_interval_ms", "INTEGER", "milliseconds", "info.frameInterval. NULL when there is no timeline or it is unreadable. 0 with no frames is the aborted-game shape (#898)."},
		{"frame_count", "INTEGER", "", "Number of frames in the timeline payload, or NULL when the payload is unreadable."},
		{"participant_frame_count", "BIGINT", "", "Number of per-participant frame records the timeline yielded. This, not frame_count, is what decides frames_null: Riot returns frames whose participantFrames are null, so a timeline can carry frames and still contribute no minutes."},
		{"timeline_present", "BOOLEAN", "", "Whether a timeline row exists for this match at all. This is a fact about the archive, not about the game."},
		{"exclusion_reason", "VARCHAR", "", "Exactly one of no_timeline, aborted, frame_interval_zero, too_short, frames_null, ok. The first matching reason, in that order. A payload the envelope cannot identify never reaches this column: the build fails closed on it instead, so an unreadable archive row cannot be mistaken for a graded one."},
		{"timeline_eligible", "BOOLEAN", "", "exclusion_reason = 'ok'. Every other table is a subset of the eligible matches, so filtering on this reproduces the dataset's own working set."},
	},
	"participant_minutes": {
		{"match_id", "VARCHAR", "", "Riot match id."},
		{"participant_id", "INTEGER", "", "1-10, as the payload states it. There is no puuid anywhere in this dataset; this is the only identity a participant has."},
		{"champion_id", "INTEGER", "", "Champion played, from the summary payload."},
		{"role", "VARCHAR", "", "Normalised teamPosition, falling back to individualPosition when teamPosition is empty: TOP, JUNGLE, MID, BOTTOM, SUPPORT."},
		{"team_id", "INTEGER", "", "100 or 200."},
		{"win", "BOOLEAN", "", "Whether this participant's team won. The outcome column every other table can be joined to."},
		{"frame_timestamp_ms", "BIGINT", "milliseconds", "Frame timestamp. Frames are not evenly spaced since about patch 16.1 (#1129), so this is carried beside minute rather than implied by it."},
		{"minute", "INTEGER", "minutes", "floor(frame_timestamp_ms / 60000). Derived from the timestamp, never from the frame index. Two frames can share a minute."},
		{"level", "INTEGER", "", "Champion level at this frame."},
		{"xp", "BIGINT", "", "Total experience at this frame."},
		{"total_gold", "INTEGER", "gold", "Total gold earned (not current gold) at this frame."},
		{"current_gold", "INTEGER", "gold", "Unspent gold at this frame. total_gold minus current_gold is roughly what has been spent."},
		{"minions_killed", "INTEGER", "", "Lane minions killed, cumulative."},
		{"jungle_minions_killed", "INTEGER", "", "Jungle monsters killed, cumulative. No per-camp events exist, so clear paths can only be inferred from this column's deltas."},
		{"cs_total", "INTEGER", "", "minions_killed + jungle_minions_killed. The CS a CS@5 comparison uses."},
		{"position_x", "INTEGER", "", "Map x at this frame, in Riot's game units, not a map image pixel."},
		{"position_y", "INTEGER", "", "Map y at this frame."},
		{"kills_to_minute", "BIGINT", "", "CHAMPION_KILL events involving this participant as killer at or before frame_timestamp_ms. 0 when none, never NULL."},
		{"deaths_to_minute", "BIGINT", "", "Deaths at or before frame_timestamp_ms. Deaths by minion, turret or execute are not counted: only kills the timeline attributes to a participant."},
		{"assists_to_minute", "BIGINT", "", "Assists at or before frame_timestamp_ms."},
		{"patch", "VARCHAR", "", "Scope column from match_index, so a patch split needs no join."},
		{"region", "VARCHAR", "", "Scope column from match_index."},
		{"queue_id", "INTEGER", "", "Scope column from match_index."},
	},
	"events": {
		{"match_id", "VARCHAR", "", "Riot match id."},
		{"region", "VARCHAR", "", "Scope column from match_index."},
		{"patch", "VARCHAR", "", "Scope column from match_index."},
		{"queue_id", "INTEGER", "", "Scope column from match_index."},
		{"event_index", "INTEGER", "", "0-based position of this event in the match, ordered by frame then by position within the frame. Stable for a given payload."},
		{"timestamp_ms", "BIGINT", "milliseconds", "Milliseconds since game start, as Riot reports it."},
		{"minute", "INTEGER", "minutes", "floor(timestamp_ms / 60000)."},
		{"event_type", "VARCHAR", "", "Riot's type string verbatim, e.g. CHAMPION_KILL, ELITE_MONSTER_KILL, SKILL_LEVEL_UP."},
		{"actor_participant_id", "INTEGER", "", "Who did it: participantId, killerId or creatorId, whichever the event type carries. 0 is Riot's sentinel for no participant (a minion, a turret, an execute) and is preserved, never converted to NULL."},
		{"target_participant_id", "INTEGER", "", "victimId. 0 as above."},
		{"assisting_participant_ids", "INTEGER[]", "", "assistingParticipantIds, verbatim, in the order Riot emitted them. Empty list when none."},
		{"team_id", "INTEGER", "", "The event's team where it carries one. See the README on Riot's acknowledged attribution bugs (#1000, #1026)."},
		{"position_x", "INTEGER", "", "Event position on the map, where the event carries one."},
		{"position_y", "INTEGER", "", "Event position on the map, where the event carries one."},
		{"lane_type", "VARCHAR", "", "laneType, e.g. on TURRET_PLATE_DESTROYED."},
		{"monster_type", "VARCHAR", "", "monsterType: DRAGON, RIFTHERALD, BARON_NASHOR, HORDE, ATAKHAN."},
		{"monster_subtype", "VARCHAR", "", "monsterSubType, or soulType for DRAGON_SOUL_GIVEN. Atakhan carries no sub-type (#1034), so it is NULL here."},
		{"building_type", "VARCHAR", "", "buildingType, e.g. TOWER_BUILDING on BUILDING_KILL."},
		{"tower_type", "VARCHAR", "", "towerType: OUTER, INNER, BASE, NEXUS_TURRET."},
		{"ward_type", "VARCHAR", "", "wardType on WARD_PLACED and WARD_KILL. UNDEFINED appears in practice (#1125) and is kept rather than filtered."},
		{"item_id", "INTEGER", "", "itemId on ITEM_PURCHASED, ITEM_SOLD and ITEM_DESTROYED. Zeroed ids occur (#1174)."},
		{"before_item_id", "INTEGER", "", "beforeId on ITEM_UNDO. The undo is reported inverted in some cases (#126)."},
		{"after_item_id", "INTEGER", "", "afterId on ITEM_UNDO."},
		{"skill_slot", "INTEGER", "", "skillSlot, 1-4 on SKILL_LEVEL_UP, plus 0 for the unspent first slot in some payloads."},
		{"level", "INTEGER", "", "level on SKILL_LEVEL_UP: the champion level the slot was spent at."},
		{"kill_type", "VARCHAR", "", "killType on CHAMPION_SPECIAL_KILL: KILL_FIRST_BLOOD, KILL_ACE, KILL_MULTI."},
		{"multi_kill_length", "INTEGER", "", "multiKillLength on a KILL_MULTI. NULL on every other kill type."},
		{"transform_type", "VARCHAR", "", "transformType, e.g. on the transformation events of transform champions."},
		{"kill_streak_length", "INTEGER", "", "killStreakLength on CHAMPION_KILL: the victim's streak before the death."},
		{"bounty", "INTEGER", "gold", "bounty on the kill, as reported."},
		{"shutdown_bounty", "INTEGER", "gold", "shutdownBounty on the kill, as reported."},
		{"winning_team", "INTEGER", "", "winningTeam on GAME_END. 0 on an aborted game (#898)."},
		{"is_duplicate_skill_level_up", "BOOLEAN", "", "True on the second and later SKILL_LEVEL_UP for the same participant, slot and level, which Riot emits since patch 15.17 (#1100). Marked, never dropped."},
	},
	"lane_matchups": {
		{"match_id", "VARCHAR", "", "Riot match id."},
		{"region", "VARCHAR", "", "Scope column from match_index."},
		{"patch", "VARCHAR", "", "Scope column."},
		{"queue_id", "INTEGER", "", "Scope column."},
		{"participant_id", "INTEGER", "", "The participant whose line this is."},
		{"opponent_participant_id", "INTEGER", "", "The opposite-team participant with the same role. See pairing_basis."},
		{"role", "VARCHAR", "", "The shared role the pairing was made on."},
		{"champion_id", "INTEGER", "", "Champion played by this participant."},
		{"opponent_champion_id", "INTEGER", "", "Champion played by the opponent."},
		{"team_id", "INTEGER", "", "100 or 200."},
		{"win", "BOOLEAN", "", "Whether this participant's team won."},
		{"pairing_basis", "VARCHAR", "", "Always 'team_position' in this version: the pairing is an inference from the constrained role assignment, not an observed lane."},
		{"is_duo_lane", "BOOLEAN", "", "True for BOTTOM and SUPPORT, whose lane has two players per side and therefore no single lane opponent."},
		{"has_nominal_opponent", "BOOLEAN", "", "False for JUNGLE, which has a nominal opposite jungler but no lane. A filter, not a reason to drop the row."},
		{"cs_5", "INTEGER", "", "CS (lane plus jungle minions) at minute 5. NULL when the game has no frame near minute 5."},
		{"opponent_cs_5", "INTEGER", "", "The opponent's cs_5, from the same frame selection. NULL as above; never imputed."},
		{"cs_diff_5", "INTEGER", "", "cs_5 - opponent_cs_5. NULL whenever either side is NULL, which is the point: 0 and unknown must not be the same value."},
		{"xp_diff_5", "BIGINT", "", "Experience difference at minute 5."},
		{"gold_diff_5", "INTEGER", "gold", "total_gold difference at minute 5."},
		{"level_diff_5", "INTEGER", "", "Level difference at minute 5."},
		{"kills_5", "BIGINT", "", "This participant's kills at minute 5, counted from CHAMPION_KILL events at or before the chosen frame."},
		{"deaths_5", "BIGINT", "", "Deaths at minute 5."},
		{"assists_5", "BIGINT", "", "Assists at minute 5."},
		{"cs_10", "INTEGER", "", "CS at minute 10, as cs_5."},
		{"opponent_cs_10", "INTEGER", "", "The opponent's cs_10."},
		{"cs_diff_10", "INTEGER", "", "cs_10 - opponent_cs_10."},
		{"xp_diff_10", "BIGINT", "", "Experience difference at minute 10."},
		{"gold_diff_10", "INTEGER", "gold", "total_gold difference at minute 10."},
		{"level_diff_10", "INTEGER", "", "Level difference at minute 10."},
		{"kills_10", "BIGINT", "", "Kills at minute 10."},
		{"deaths_10", "BIGINT", "", "Deaths at minute 10."},
		{"assists_10", "BIGINT", "", "Assists at minute 10."},
		{"cs_15", "INTEGER", "", "CS at minute 15, as cs_5."},
		{"opponent_cs_15", "INTEGER", "", "The opponent's cs_15."},
		{"cs_diff_15", "INTEGER", "", "cs_15 - opponent_cs_15."},
		{"xp_diff_15", "BIGINT", "", "Experience difference at minute 15."},
		{"gold_diff_15", "INTEGER", "gold", "total_gold difference at minute 15."},
		{"level_diff_15", "INTEGER", "", "Level difference at minute 15."},
		{"kills_15", "BIGINT", "", "Kills at minute 15."},
		{"deaths_15", "BIGINT", "", "Deaths at minute 15."},
		{"assists_15", "BIGINT", "", "Assists at minute 15."},
	},
	"participant_early": {
		{"match_id", "VARCHAR", "", "Riot match id."},
		{"region", "VARCHAR", "", "Scope column from match_index."},
		{"patch", "VARCHAR", "", "Scope column."},
		{"queue_id", "INTEGER", "", "Scope column."},
		{"participant_id", "INTEGER", "", "1-10. One row per participant per eligible match, whether or not anything happened to them."},
		{"champion_id", "INTEGER", "", "Champion played."},
		{"role", "VARCHAR", "", "Normalised role, as participant_minutes.role."},
		{"team_id", "INTEGER", "", "100 or 200."},
		{"win", "BOOLEAN", "", "Whether this participant's team won."},
		{"first_blood_involvement", "BOOLEAN", "", "Whether this participant killed or assisted the first CHAMPION_KILL of the game. False rather than NULL when there was no kill."},
		{"first_death_ts_ms", "BIGINT", "milliseconds", "Timestamp of this participant's first death, or NULL when they never died to a participant."},
		{"first_death_minute", "INTEGER", "minutes", "floor(first_death_ts_ms / 60000). NULL when first_death_ts_ms is NULL."},
		{"first_death_position_x", "INTEGER", "", "Map x of the first death, taken at the earliest such event."},
		{"first_death_position_y", "INTEGER", "", "Map y of the first death."},
		{"plates_destroyed", "BIGINT", "", "Turret plates this participant destroyed, over the whole game, from TURRET_PLATE_DESTROYED. Zero rather than NULL."},
		{"plates_at_5", "BIGINT", "", "Plates destroyed at or before the minute-5 checkpoint frame. NULL when the game has no such frame, never 0."},
		{"plates_at_10", "BIGINT", "", "Plates destroyed at or before minute 10, as plates_at_5."},
		{"plates_at_15", "BIGINT", "", "Plates destroyed at or before minute 15, as plates_at_5."},
		{"first_item_ts_ms", "BIGINT", "milliseconds", "Timestamp of the first ITEM_PURCHASED event attributed to this participant. NULL when the timeline carries none or Riot attributed it to participant 0 (#1069)."},
		{"first_item_id", "INTEGER", "", "Item id of that first purchase."},
		{"second_item_ts_ms", "BIGINT", "milliseconds", "Timestamp of the second purchase: the two-item powerspike timing. NULL when fewer than two purchases were attributed."},
		{"second_item_id", "INTEGER", "", "Item id of the second purchase."},
		{"skill_order", "VARCHAR", "", "Skill slots in the order they were spent, one character per slot, e.g. '1234567890' style strings of digits. Duplicates from #1100 are excluded. NULL when the payload carries no SKILL_LEVEL_UP."},
		{"duplicate_skill_ups", "BIGINT", "", "How many SKILL_LEVEL_UP events for this participant were marked duplicates. Published so the magnitude of #1100 is visible rather than assumed."},
		{"wards_placed", "BIGINT", "", "WARD_PLACED events with this participant as actor. Includes the UNDEFINED ward types of #1125, which is why the events table keeps the type."},
		{"wards_killed", "BIGINT", "", "WARD_KILL events with this participant as actor."},
		{"dragons_participated", "BIGINT", "", "Dragons this participant killed or assisted. Each dragon counts once per participant, from the events relation, not the objective totals."},
		{"grubs_participated", "BIGINT", "", "Voidgrubs (monsterType HORDE) this participant killed or assisted."},
		{"heralds_participated", "BIGINT", "", "Rift Heralds this participant killed or assisted."},
		{"barons_participated", "BIGINT", "", "Barons this participant killed or assisted."},
		{"atakhan_participated", "BIGINT", "", "Atakhan kills this participant took part in. Atakhan carries no monsterSubType (#1034), so it is counted here by type alone."},
	},
	"match_objectives": {
		{"match_id", "VARCHAR", "", "Riot match id. One row per eligible match."},
		{"region", "VARCHAR", "", "Scope column from match_index."},
		{"patch", "VARCHAR", "", "Scope column."},
		{"queue_id", "INTEGER", "", "Scope column."},
		{"game_duration_s", "INTEGER", "seconds", "Game duration, carried so an objective count can be read against the time available."},
		{"first_blood_team", "INTEGER", "", "Team that took the first kill, by earliest event index. NULL when the game has no kills."},
		{"first_blood_ts_ms", "BIGINT", "milliseconds", "When that kill happened."},
		{"first_tower_team", "INTEGER", "", "Team that destroyed the first OUTER turret. NULL when none fell."},
		{"first_tower_ts_ms", "BIGINT", "milliseconds", "When that turret fell."},
		{"first_dragon_team", "INTEGER", "", "Team that took the first dragon. NULL when none was taken."},
		{"first_dragon_ts_ms", "BIGINT", "milliseconds", "When that dragon was taken."},
		{"soul_team", "INTEGER", "", "Team credited with the dragon soul by DRAGON_SOUL_GIVEN. NULL when no soul was taken; can be 0 because Riot has emitted the event early with teamId 0 (#1026)."},
		{"soul_type", "VARCHAR", "", "The soul's element, from soulType on DRAGON_SOUL_GIVEN."},
		{"kills_team_100", "BIGINT", "", "CHAMPION_KILL events with team_id 100, by Riot's attribution."},
		{"kills_team_200", "BIGINT", "", "CHAMPION_KILL events with team_id 200."},
		{"dragons_team_100", "BIGINT", "", "ELITE_MONSTER_KILL with monsterType DRAGON attributed to team 100."},
		{"dragons_team_200", "BIGINT", "", "As above for team 200. The two sum to the dragons in the timeline, which may not be the dragons in the game (#1000)."},
		{"grubs_team_100", "BIGINT", "", "monsterType HORDE kills by team 100."},
		{"grubs_team_200", "BIGINT", "", "monsterType HORDE kills by team 200."},
		{"heralds_team_100", "BIGINT", "", "monsterType RIFTHERALD kills by team 100."},
		{"heralds_team_200", "BIGINT", "", "monsterType RIFTHERALD kills by team 200."},
		{"barons_team_100", "BIGINT", "", "monsterType BARON_NASHOR kills by team 100."},
		{"barons_team_200", "BIGINT", "", "monsterType BARON_NASHOR kills by team 200."},
		{"atakhan_team_100", "BIGINT", "", "monsterType ATAKHAN kills by team 100."},
		{"atakhan_team_200", "BIGINT", "", "monsterType ATAKHAN kills by team 200."},
		{"souls_team_100", "BIGINT", "", "DRAGON_SOUL_GIVEN events attributed to team 100."},
		{"souls_team_200", "BIGINT", "", "DRAGON_SOUL_GIVEN events attributed to team 200."},
		{"towers_team_100", "BIGINT", "", "BUILDING_KILL events with a towerType for team 100, at any tier."},
		{"towers_team_200", "BIGINT", "", "As above for team 200. Riot has acknowledged that this attribution flips in some payloads (#1000)."},
		{"plates_team_100", "BIGINT", "", "TURRET_PLATE_DESTROYED events attributed to team 100. Riot has acknowledged more than five plates can be credited to one turret (#703)."},
		{"plates_team_200", "BIGINT", "", "TURRET_PLATE_DESTROYED events attributed to team 200."},
	},
}

// featureSchema renders schema.json for one build.
//
// The row counts come from the build rather than from the specification,
// because a schema that cannot say how many rows it describes is a schema a
// reader has to take on faith.
func featureSchema(counts FeatureCounts, opts FeatureOptions) featureSchemaDoc {
	tables := make([]featureTableSchema, 0, len(featureTables))
	for _, name := range featureTables {
		tables = append(tables, featureTableSchema{
			Name:    name,
			Rows:    counts.TableRows[name],
			Columns: featureSchemaColumns[name],
		})
	}
	return featureSchemaDoc{
		Dataset:     FeatureDatasetDir,
		GeneratedBy: "lolstats-aggregate features",
		Scope: FeatureScope{
			Region:   opts.Region,
			Platform: opts.Platform,
			Queue:    opts.Queue,
		},
		Notice: featureSchemaNotice,
		Tables: tables,
	}
}

// featureReadme renders the dataset README.
//
// The README is the dataset's interface: there is no web tier and no query
// service, so an analyst's first contact with this data is this document. It is
// therefore written as a manual - what the sample is, what is known to be wrong
// with it, and recipes that run - and its counts are this build's counts, not an
// example from some earlier run that happened to look plausible.
func featureReadme(counts FeatureCounts, opts FeatureOptions) string {
	var b strings.Builder
	write := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	write("# Match timeline feature dataset (`%s`)\n\n", FeatureDatasetDir)
	write("Derived from the raw Riot archive by `lolstats-aggregate features`. ")
	write("Generated by a build that publishes the manifest last, so the tree this file is in ")
	write("and the tree it describes are the same tree.\n\n")
	write("**This is a sample, not a census.** The crawler fetched timelines for a bounded, ")
	write("reproducible subset of the archive - selected by a hash of the match id over the ")
	write("eligible window - rather than every match it has a summary for. Nothing in this ")
	write("dataset can tell you what fraction of *all* games behaved a certain way; it can tell ")
	write("you what fraction of *these* games did, and the README's first rule is to report it ")
	write("that way.\n\n")
	write("**This is not a reader contract.** `agg/v1` is frozen and versioned; this dataset is ")
	write("not. A reshape publishes `timeline-v2` beside it.\n\n")

	write("## What this build read\n\n")
	write("| | |\n|---|---|\n")
	write("| scope | region `%s`, platform `%s`, queue `%d` |\n", orAny(opts.Region), orAny(opts.Platform), opts.Queue)
	write("| summary rows | %d |\n", counts.ArchiveRows)
	write("| timeline rows | %d |\n", counts.TimelineRows)
	write("| matches | %d |\n", counts.Matches)
	write("| with a timeline | %d |\n", counts.TimelinePresent)
	write("| eligible | %d |\n", counts.Eligible)
	write("| timelines with no summary | %d |\n", counts.Orphans)
	write("| minimum game duration | %d s |\n\n", opts.MinDurationS)

	write("## Tables\n\n")
	write("Every table carries `match_id`, and every table is a subset of the eligible matches, ")
	write("so a join on `match_id` never needs a filter first. Only `match_index` contains ")
	write("matches that were excluded.\n\n")
	write("| table | rows | what it is |\n|---|---|---|\n")
	for _, name := range featureTables {
		write("| `%s` | %d | %s |\n", name, counts.TableRows[name], featureTablePurpose(name))
	}
	write("\nColumns and their units are in `schema.json` in this directory.\n\n")

	write("## Coverage and exclusions\n\n")
	write("`match_index.exclusion_reason` is the whole story of what this dataset kept and what ")
	write("it dropped, and it is exact: a match has one reason, and the reasons are applied in ")
	write("the order below. Filter to `timeline_eligible` to get the dataset's own working set, ")
	write("or select a reason to work with what was left out.\n\n")
	write("| reason | matches | what it means |\n|---|---|---|\n")
	for _, reason := range featureExclusionOrder {
		write("| `%s` | %d | %s |\n", reason, counts.Excluded[reason], featureExclusionMeaning(reason))
	}
	write("| (never classified) | %d | a ledger bug; the build refuses to publish when this is non-zero |\n\n",
		counts.Excluded["unexplained"])

	write("## Caveats\n\n")
	for _, caveat := range featureCaveats() {
		write("- %s\n", caveat)
	}
	write("\n")

	write("## Recipes\n\n")
	write("Every recipe is plain SQL against this directory with DuckDB. Replace ")
	write("`/path/to/dataset` with the directory this file is in.\n\n")

	write("### CS at 5 minutes versus the lane opponent, by outcome\n\n")
	write("```sql\n")
	write("SELECT\n  cs_diff_5 <= -10 AS lost_lane,\n  count(*) AS n,\n  avg(win::INT) AS win_rate\n")
	write("FROM read_parquet('/path/to/dataset/lane_matchups/*.parquet')\n")
	write("WHERE NOT is_duo_lane AND has_nominal_opponent AND cs_diff_5 IS NOT NULL\n")
	write("GROUP BY 1 ORDER BY 1;\n```\n\n")
	write("`is_duo_lane` and `has_nominal_opponent` are filters, not decorations: the bot lane ")
	write("is a 2-v-2 and the jungle has no lane, so a \"lane opponent\" comparison including ")
	write("them is comparing a number to a number that does not mean the same thing.\n\n")

	write("### KDA at 5, 10 and 15 minutes versus the lane opponent\n\n")
	write("```sql\n")
	write("WITH pairs AS (\n")
	write("  SELECT p.role, p.win,\n")
	write("    (p.kills_5 + p.assists_5) / greatest(p.deaths_5, 1) AS kda_5,\n")
	write("    (o.kills_5 + o.assists_5) / greatest(o.deaths_5, 1) AS opponent_kda_5,\n")
	write("    (p.kills_10 + p.assists_10) / greatest(p.deaths_10, 1) AS kda_10,\n")
	write("    (o.kills_10 + o.assists_10) / greatest(o.deaths_10, 1) AS opponent_kda_10,\n")
	write("    (p.kills_15 + p.assists_15) / greatest(p.deaths_15, 1) AS kda_15,\n")
	write("    (o.kills_15 + o.assists_15) / greatest(o.deaths_15, 1) AS opponent_kda_15\n")
	write("  FROM read_parquet('/path/to/dataset/lane_matchups/*.parquet') p\n")
	write("  JOIN read_parquet('/path/to/dataset/lane_matchups/*.parquet') o\n")
	write("    ON o.match_id = p.match_id AND o.participant_id = p.opponent_participant_id\n")
	write("  WHERE NOT p.is_duo_lane AND p.has_nominal_opponent\n")
	write(")\n")
	write("SELECT role, count(*) AS n,\n")
	write("  avg(kda_5 - opponent_kda_5) AS avg_kda_diff_5,\n")
	write("  avg(kda_10 - opponent_kda_10) AS avg_kda_diff_10,\n")
	write("  avg(kda_15 - opponent_kda_15) AS avg_kda_diff_15,\n")
	write("  avg(win::INT) AS win_rate\n")
	write("FROM pairs GROUP BY 1 ORDER BY n DESC;\n```\n\n")
	write("The table holds one row per participant, so the opponent is reached by ")
	write("joining it to itself on `opponent_participant_id`. The filter is the same ")
	write("one the CS recipe uses, and for the same reason: a lane opponent only ")
	write("exists in a solo lane, so the jungle and the bot lane would compare a ")
	write("number to a number that does not mean the same thing.\n\n")
	write("`greatest(deaths, 1)` is the KDA convention, not a fudge: a deathless game ")
	write("has a real ratio and it is not infinity, and dividing by zero would give ")
	write("`inf` or an error instead of a row.\n\n")
	write("A `NULL` at a checkpoint means the game ended before it, so the three ")
	write("columns do not cover the same games: `avg_kda_diff_15` is averaged over ")
	write("fewer games than `avg_kda_diff_5`, and it is `NULL` outright when no game ")
	write("in the sample reached minute 15. This is the dataset's own rule visible in ")
	write("a query - a value a game never reached is missing, never carried forward ")
	write("and never zero.\n\n")

	write("### Curve form: CS difference against win rate, bucketed\n\n")
	write("```sql\n")
	write("SELECT\n  floor(cs_diff_10 / 10.0) * 10 AS cs_diff_bucket,\n")
	write("  count(*) AS n,\n  avg(win::INT) AS win_rate\n")
	write("FROM read_parquet('/path/to/dataset/lane_matchups/*.parquet')\n")
	write("WHERE role = 'MID' AND cs_diff_10 IS NOT NULL\n")
	write("GROUP BY 1 HAVING n >= 30 ORDER BY 1;\n```\n\n")
	write("The `HAVING` is a guard for a corpus, not for the shape of the query: it ")
	write("keeps a bucket that two games landed in from being plotted as a curve. On ")
	write("a bounded sample it can therefore return zero rows, and that is the query ")
	write("working - lower the threshold rather than suspecting the dataset.\n\n")

	write("### Per-minute economy curves\n\n")
	write("```sql\n")
	write("SELECT minute,\n  avg(cs_total) AS avg_cs,\n")
	write("  avg(CASE WHEN role = 'JUNGLE' THEN jungle_minions_killed END) AS avg_jungle_cs,\n")
	write("  avg(total_gold) AS avg_gold\n")
	write("FROM read_parquet('/path/to/dataset/participant_minutes/*.parquet')\n")
	write("WHERE minute <= 20\nGROUP BY 1 ORDER BY 1;\n```\n\n")
	write("`minute` is derived from the frame timestamp, not from the frame index: frames have ")
	write("not been evenly spaced since about patch 16.1, so index-based minutes are up to ten ")
	write("per cent early. `frame_timestamp_ms` is on every row if you would rather bin it ")
	write("yourself.\n\n")

	write("### First-objective timing against the outcome\n\n")
	write("```sql\n")
	write("SELECT\n  m.first_dragon_team = p.team_id AS took_first_dragon,\n")
	write("  count(DISTINCT p.match_id) AS games,\n")
	write("  count(*) AS participant_sides,\n  avg(p.win::INT) AS win_rate\n")
	write("FROM read_parquet('/path/to/dataset/match_objectives/*.parquet') m\n")
	write("JOIN read_parquet('/path/to/dataset/participant_early/*.parquet') p USING (match_id)\n")
	write("WHERE m.first_dragon_team IS NOT NULL\n")
	write("GROUP BY 1 ORDER BY 1;\n```\n\n")
	write("Both counts are there because they answer different questions: ")
	write("`participant_early` holds ten rows per match, so a game contributes ten ")
	write("sides to `participant_sides` and one to `games`. The win rate is still the ")
	write("win rate - every side of a game carries that game's outcome - but a row ")
	write("count read as a game count would be ten times too large.\n\n")

	write("### Event timing distribution\n\n")
	write("```sql\n")
	write("SELECT event_type, count(*) AS n,\n")
	write("  quantile_cont(timestamp_ms, 0.5) / 60000 AS median_minute,\n")
	write("  count(*) FILTER (WHERE is_duplicate_skill_level_up) AS duplicates\n")
	write("FROM read_parquet('/path/to/dataset/events/*.parquet')\n")
	write("GROUP BY 1 ORDER BY n DESC;\n```\n\n")
	write("The `duplicates` column is not a mistake: Riot emits `SKILL_LEVEL_UP` twice for the ")
	write("same slot since patch 15.17 and those rows are marked rather than removed, so you can ")
	write("see the defect's magnitude instead of inheriting a silent correction.\n\n")

	write("### Death positions\n\n")
	write("```sql\n")
	write("SELECT minute, position_x, position_y, count(*) AS deaths\n")
	write("FROM read_parquet('/path/to/dataset/events/*.parquet')\n")
	write("WHERE event_type = 'CHAMPION_KILL' AND target_participant_id > 0\n")
	write("GROUP BY 1, 2, 3;\n```\n\n")
	write("Feed the result to a heatmap library; the coordinates are Riot's game units, not map ")
	write("image pixels, so a plot needs the map's own transform. The position is the ")
	write("victim's death location - that is the reading this dataset publishes, and ")
	write("it is the same one `participant_early.first_death_position_x` uses - because ")
	write("Riot's own `position` field on a kill is ambiguous in the wild.\n\n")

	write("### Skill order and item timings\n\n")
	write("```sql\n")
	write("SELECT role, skill_order, count(*) AS n\n")
	write("FROM read_parquet('/path/to/dataset/participant_early/*.parquet')\n")
	write("WHERE skill_order IS NOT NULL AND role = 'TOP'\n")
	write("GROUP BY 1, 2 ORDER BY n DESC LIMIT 20;\n```\n\n")

	write("### pandas\n\n")
	write("```python\n")
	write("import pandas as pd\n\n")
	write("lm = pd.read_parquet('/path/to/dataset/lane_matchups')\n")
	write("solo = lm[(~lm.is_duo_lane) & lm.has_nominal_opponent & lm.cs_diff_5.notna()].copy()\n")
	write("solo['cs_diff_bucket'] = (solo.cs_diff_5 // 10 * 10).astype(int)\n")
	write("print(solo.groupby('cs_diff_bucket').agg(n=('win', 'size'), win_rate=('win', 'mean')))\n```\n\n")
	write("`win` arrives as a real boolean and the nullable columns arrive as `float64` with ")
	write("`NaN`, which is the pandas spelling of \"the game never reached minute 10\". Do not ")
	write("fill it with zero.\n\n")

	write("## Reconstruction rules\n\n")
	write("Three identities hold in this dataset and are worth asserting on before trusting a ")
	write("number:\n\n")
	write("1. `match_index.timeline_eligible` selects exactly the matches present in the other ")
	write("five tables.\n")
	write("2. `lane_matchups` has exactly ten rows per eligible match: one per participant, ")
	write("paired by role.\n")
	write("3. `cs_total = minions_killed + jungle_minions_killed` on every row of ")
	write("`participant_minutes`.\n\n")

	return b.String()
}

// featureExclusionOrder is the order the ledger applies the reasons in, which is
// also the order the README explains them, because a reader reconciling a count
// needs the same precedence the build used.
var featureExclusionOrder = []string{
	"ok", "no_timeline", "aborted", "frame_interval_zero",
	"too_short", "frames_null",
}

// featureExclusionMeaning is the operator's explanation of one reason.
func featureExclusionMeaning(reason string) string {
	switch reason {
	case "ok":
		return "eligible: readable summary, readable timeline, frames present, long enough"
	case "no_timeline":
		return "no timeline row exists for this match. Either the sample did not cover it, or the timeline aged out of Riot's one-year retention and can never be fetched"
	case "aborted":
		return "frameInterval is 0 and there are no frames: an aborted game, in which no participant ever moved"
	case "frame_interval_zero":
		return "frameInterval is 0 but frames exist, which Riot does not document; the timeline is not trusted"
	case "too_short":
		return "the game is shorter than the minimum duration, so its timeline has no usable frame set"
	case "frames_null":
		return "the frames member is absent or not an array"
	}
	return "unknown reason - this is a build bug, not a data property"
}

// featureTablePurpose is the one-line description the README's table list uses.
func featureTablePurpose(table string) string {
	switch table {
	case "match_index":
		return "one row per match in scope, with the reason it was kept or excluded"
	case "participant_minutes":
		return "one row per participant per frame: the per-minute economy, position and running KDA"
	case "events":
		return "one wide row per event, every column that the event type does not carry left NULL"
	case "lane_matchups":
		return "one row per participant per eligible match: the checkpoints against the opposing laner"
	case "participant_early":
		return "one row per participant per eligible match: first blood, first death, wards, plates, items, skill order"
	case "match_objectives":
		return "one row per eligible match: first blood, first tower, first dragon, soul, and per-team objective counts"
	}
	return "unknown table - this is a build bug, not a data property"
}

// featureCaveats is the list of things a reader has to know before publishing a
// number from this dataset.
//
// Every entry is either a Riot-acknowledged defect or a property of the payload
// that would otherwise be discovered as an anomaly. They are written here
// rather than in a comment because this list is the dataset's honesty budget:
// a number whose caveat is not read is a number that will be reported wrongly.
func featureCaveats() []string {
	return []string{
		"**Frames are not evenly spaced.** Since about patch 16.1 the interval is " +
			"observed between roughly 60.5 s and 71.3 s, not 60 s " +
			"(RiotGames/developer-relations#1129). `minute` is derived from the frame " +
			"timestamp, and `frame_timestamp_ms` is kept beside it, so a checkpoint at " +
			"\"minute 5\" is the frame nearest 5:00, not the sixth frame.",
		"**A checkpoint is a nearest-frame choice, not an exact instant.** The " +
			"minute-5, -10 and -15 columns come from the frame closest to that minute " +
			"within a grace window, so they are comparable across games but not " +
			"exact. Where no frame fits, the column is NULL.",
		"**NULL is never imputed.** A game that ended at minute 8 has NULL for the " +
			"minute-10 and -15 columns, not zeros and not a carried-forward value. `0` " +
			"means zero; it never means \"unknown\".",
		"**The lane opponent is inferred from `teamPosition`.** Riot's summary carries " +
			"a constrained role assignment and a separate per-isolation guess; the " +
			"constrained one is used because it pairs exactly once per role per team. " +
			"`pairing_basis` records this and `is_duo_lane` marks the bot lane, where a " +
			"single lane opponent does not really exist.",
		"**Aborted games are excluded, not repaired.** Riot reports them with " +
			"`frameInterval: 0` and no frames " +
			"(RiotGames/developer-relations#898). They appear in `match_index` with " +
			"`exclusion_reason = 'aborted'` and nowhere else.",
		"**Short games may have no frames at all** " +
			"(RiotGames/developer-relations#71), which is what `too_short` and " +
			"`frames_null` record.",
		"**`SKILL_LEVEL_UP` is emitted twice since patch 15.17** " +
			"(RiotGames/developer-relations#1100). Both rows are kept in `events` and the " +
			"second is marked `is_duplicate_skill_level_up`; `participant_early.skill_order` " +
			"excludes them.",
		"**Objective attribution is as reported, not as it happened.** Riot has " +
			"acknowledged `BUILDING_KILL` team attribution flipping " +
			"(RiotGames/developer-relations#1000), more than five plates on one turret " +
			"(#703), and `DRAGON_SOUL_GIVEN` emitted early with `teamId` 0 (#1026). " +
			"`match_objectives` counts what the payload said.",
		"**Atakhan carries no sub-type** (RiotGames/developer-relations#1034), so " +
			"`monster_subtype` is NULL for it and `atakhan_team_*` is counted by type alone.",
		"**Some `ITEM_PURCHASED` events have `participantId: 0`** " +
			"(RiotGames/developer-relations#1069), and item ids can be zeroed " +
			"(#1174). Those purchases belong to no participant, so they do not appear " +
			"in `participant_early` item timings - which is why a first-item timestamp " +
			"can be NULL for a participant who bought items.",
		"**`WARD_PLACED` volume is inflated by `wardType: UNDEFINED`** " +
			"(RiotGames/developer-relations#1125). `ward_type` is kept in `events` so the " +
			"count can be filtered rather than taken on trust.",
		"**Jungle pathing cannot be reconstructed from events.** Regular camps emit no " +
			"event at all. Clear routes are only inferable from `jungle_minions_killed` " +
			"deltas joined to `position_x`/`position_y` on consecutive frames, and the " +
			"result is an inference, not a record.",
		"**Participant identity stops at the match.** There is no `puuid` in any table " +
			"and no stable cross-match participant key. Per-player questions are out of " +
			"scope by design, not by oversight - see `docs/compliance.md`.",
		"**Timelines expire after one year** while match summaries expire after two. A " +
			"match in `match_index` with `exclusion_reason = 'no_timeline'` may therefore " +
			"be permanently unfetchable rather than merely unfetched.",
	}
}

// orAny renders an empty scope value as a wildcard, because an empty region in
// the README would read as an omission rather than as "not narrowed".
func orAny(value string) string {
	if value == "" {
		return "any"
	}
	return value
}
