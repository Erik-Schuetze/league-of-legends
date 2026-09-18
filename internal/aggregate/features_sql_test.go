package aggregate

import (
	"strings"
	"testing"
)

// The dataset's SQL is already covered end to end: features_dataset_test.go
// builds the fixture dataset and compares every published column of every
// table, by value, against expectations hand-derived from fixtureGrowth.
//
// These tests pin the handful of constructs in features_sql.go that a value
// golden cannot explain. Each one is here because getting it wrong produced, or
// would produce, a wrong number that still looks plausible:
//
//   - minutes/events must take their minute from the frame or event timestamp,
//     never from a position in the array. A frame index would be right for
//     every well-behaved timeline and silently wrong for the irregular one.
//   - the carried payload must be CAST to VARCHAR. DuckDB types a bare
//     BYTE_ARRAY column as BLOB, and json_extract on a BLOB yields NULL rather
//     than an error, so every json_extract_string downstream would quietly
//     return NULL for the whole dataset.
//   - the running KDA sums must be cast to BIGINT. DuckDB widens sum(INTEGER)
//     to HUGEINT, and DuckDB's Parquet writer has no 128-bit type, so an
//     uncast sum turns kills_to_minute into a DOUBLE. It shipped that way once.
//   - a checkpoint that the match never reached must stay NULL. The lane
//     matchups therefore impute nothing: the three checkpoint joins are LEFT
//     JOINs and no COALESCE is applied to what they bring in.
//
// The assertions are deliberately structural rather than byte-exact copies of
// the whole statement. A whole-statement golden would have to be regenerated on
// every cosmetic edit, and the repo has no golden-file precedent to copy; the
// values, which are what actually matter, are pinned by the dataset tests.
func TestFeatureDatasetSQLInvariants(t *testing.T) {
	t.Run("minute comes from the timestamp, not the frame index", func(t *testing.T) {
		frames := timelineFramesSQL("matches.parquet")
		want := "CAST(floor(CAST(json_extract(f.value, '$.timestamp') AS BIGINT) / 60000.0) AS INTEGER) AS minute,"
		if !strings.Contains(frames, want) {
			t.Errorf("frame minute is not floor(timestamp / 60000):\n%s", frames)
		}
		if strings.Contains(frames, "frame_index") {
			t.Errorf("the frames statement mentions a frame index; minute must not be derived from one:\n%s", frames)
		}

		events := timelineEventsSQL("events.parquet")
		wantEvent := "CAST(floor(CAST(json_extract(e.value, '$.timestamp') AS BIGINT) / 60000.0) AS INTEGER) AS minute,"
		if !strings.Contains(events, wantEvent) {
			t.Errorf("event minute is not floor(timestamp / 60000):\n%s", events)
		}

		table := eventsTableSQL("index.parquet", "events.parquet")
		if !strings.Contains(table, "\n  e.minute,\n") {
			t.Errorf("the events table does not pass the spill's minute through:\n%s", table)
		}
		if strings.Contains(table, "60000") {
			t.Errorf("the events table recomputes the minute instead of projecting e.minute:\n%s", table)
		}
	})

	t.Run("carried payload is cast to VARCHAR", func(t *testing.T) {
		got := carriedPayloadSQL("env.parquet", []string{"part.parquet"}, "JOIN x ON true", "match_id")
		want := "CASE WHEN e.payload_valid THEN CAST(a.payload AS VARCHAR) END AS payload"
		if !strings.Contains(got, want) {
			t.Errorf("the carried payload is not cast to VARCHAR, so json_extract would return NULL:\n%s", got)
		}
		if !strings.Contains(got, "  e.match_id,\n") {
			t.Errorf("carried columns are not projected:\n%s", got)
		}
	})

	t.Run("running KDA sums are cast to BIGINT", func(t *testing.T) {
		minutes := participantMinutesSQL("index.parquet", "participants.parquet", "frames.parquet", "kills.parquet")
		for _, column := range []string{"kills_to_minute", "deaths_to_minute", "assists_to_minute"} {
			if !strings.Contains(minutes, "AS BIGINT) AS "+column) {
				t.Errorf("%s is not cast to BIGINT, so DuckDB widens it to HUGEINT and then to DOUBLE in Parquet:\n%s",
					column, minutes)
			}
		}
	})

	t.Run("the checkpoint band is asymmetric and never imputes", func(t *testing.T) {
		picks := checkpointPicksSQL("minutes.parquet")
		for _, want := range []string{
			// Up to 60 s before the checkpoint and at most 30 s after it: the
			// observed spacing is 60.5-71.3 s, so a symmetric band would reach
			// two frames back, and an unbounded upper edge would reach forward.
			// Each expectation runs to the end of its line, so widening
			// `+ 30000` to `+ 300000` is caught rather than matched as a prefix.
			"ON f.frame_timestamp_ms BETWEEN c.checkpoint * 60000 - 60000 AND c.checkpoint * 60000 + 30000\n",
			// Nearest frame wins; on a tie the earlier one does.
			"ORDER BY abs(f.frame_timestamp_ms - c.checkpoint * 60000), f.frame_timestamp_ms) AS pick",
			// A match that ended early has no frame to offer, so it matches
			// nothing and its checkpoints stay NULL.
			"AND c.checkpoint * 60000 <= l.last_frame_ms\n",
		} {
			if !strings.Contains(picks, want) {
				t.Errorf("checkpoint selection lost %q:\n%s", want, picks)
			}
		}

		matchups := laneMatchupsSQL("index.parquet", "participants.parquet", "checkpoints.parquet")
		if strings.Contains(matchups, "COALESCE") {
			t.Errorf("lane matchups coalesce something, so an unreached checkpoint could be published as 0:\n%s", matchups)
		}
		for _, want := range []string{
			"'team_position' AS pairing_basis",
			"IN ('BOTTOM', 'SUPPORT') AS is_duo_lane",
			"<> 'JUNGLE' AS has_nominal_opponent",
			// Mine and theirs are joined by role across teams, and the diff is
			// mine minus theirs rather than theirs minus mine.
			"c5.cs_total AS cs_5",
			"c5.cs_total - o5.cs_total AS cs_diff_5",
		} {
			if !strings.Contains(matchups, want) {
				t.Errorf("lane matchups lost %q:\n%s", want, matchups)
			}
		}
	})

	t.Run("participant 0 never accumulates kills or deaths", func(t *testing.T) {
		points := killPointsSQL("events.parquet")
		for _, want := range []string{
			"WHERE event_type = 'CHAMPION_KILL' AND actor_participant_id > 0",
			"WHERE event_type = 'CHAMPION_KILL' AND target_participant_id > 0",
			"WHERE event_type = 'CHAMPION_KILL' AND length(assisting_participant_ids) > 0",
		} {
			if !strings.Contains(points, want) {
				t.Errorf("kill points lost %q, so Riot's participant-0 sentinel could accumulate:\n%s", want, points)
			}
		}
	})
}
