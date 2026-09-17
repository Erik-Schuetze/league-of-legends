package aggregate

import (
	"fmt"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// This file renders the SQL the build runs. Every statement is generated from
// validated Go values: paths are single-quote escaped, patches are validated
// against the aggmodel patch pattern before they get here, and every number is
// formatted from an int. No unvalidated text reaches DuckDB.
//
// The statements target the DuckDB CLI rather than a prepared-statement API
// because the engine is a subprocess. Only long-stable SQL is used
// (read_parquet, json_extract, from_json, UNNEST, epoch_ms, COPY), and a golden
// test pins the generated text so a change in the generated SQL is a visible
// diff rather than a silent change in published numbers.

// quoteLiteral renders a Go string as a single-quoted SQL string literal.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// fileList renders a Go slice as a SQL list of string literals.
func fileList(paths []string) string {
	quoted := make([]string, 0, len(paths))
	for _, path := range paths {
		quoted = append(quoted, quoteLiteral(path))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// parquetOf renders a read_parquet relation for one path.
func parquetOf(path string) string {
	return fmt.Sprintf("read_parquet(%s, union_by_name = true)", quoteLiteral(path))
}

// participantRoleSQL renders the normalisation of Riot's position fields into
// an aggmodel.Role.
//
// The two position fields have different semantics: teamPosition is the
// assigned role and is empty in some queues, individualPosition is the detected
// role. Where both exist the assignment wins, so the rule is "teamPosition
// unless empty, then individualPosition", which is exactly what
// aggmodel.RoleFromRiotPosition expects to receive. The accepted spellings
// mirror that function one for one, including MIDDLE/MID and UTILITY/SUPPORT.
// Anything else becomes NULL and is rejected and counted, never guessed.
func participantRoleSQL(teamPosition, individualPosition string) string {
	chosen := fmt.Sprintf("COALESCE(NULLIF(%s, ''), %s)", teamPosition, individualPosition)
	return "CASE " +
		fmt.Sprintf("WHEN %s = 'TOP' THEN 'TOP' ", chosen) +
		fmt.Sprintf("WHEN %s = 'JUNGLE' THEN 'JUNGLE' ", chosen) +
		fmt.Sprintf("WHEN %s IN ('MIDDLE', 'MID') THEN 'MID' ", chosen) +
		fmt.Sprintf("WHEN %s IN ('BOTTOM', 'ADC') THEN 'BOTTOM' ", chosen) +
		fmt.Sprintf("WHEN %s IN ('UTILITY', 'SUPPORT') THEN 'SUPPORT' ", chosen) +
		"ELSE NULL END"
}

// envelopeSQL reads the raw match parquet parts, normalises the envelope fields
// every later statement needs, and de-duplicates the result. It does not carry
// the payload - it carries a boolean saying whether the payload is JSON at all.
//
// It is the first half of the spill, split from payloadSQL because the two
// halves have to be two statements: evaluating a JSON function on a payload row
// while *also* passing that payload through is the one shape the engine cannot
// afford inside the Job's memory budget. Measured on the real archive (154
// parts, 268 MB) at the configured 1 GiB limit, one statement that extracts the
// five envelope fields *and* projects the payload dies with "Out of Memory
// Error" at 1023.9 MiB of the 1.0 GiB limit, and so does a keys-file variant of
// it that still calls json_valid beside the payload; each half on its own
// completes: the JSON work with the payload dropped from its output, and the
// payload carried through a join against the keys with no JSON call of its own.
// Splitting them is therefore not a style choice, it is what makes the build
// fit. This is why the validity flag is computed here, where the payload is
// already being parsed, and consumed in payloadSQL, which never parses it.
//
// Splitting is also what keeps the de-duplication correct rather than merely
// affordable. The keys are collapsed here, over ~3 k keys, so the second
// statement can be a plain join against a relation that is unique on
// (part, part_row) and produce exactly one row per match.
//
// One row per match, not one row per archive record. The archive is append-only
// parquet with no key, so the same match can be written to it twice - a re-walk
// of a match the crawler had already stored, or a retry that re-fetched a
// payload whose archive write had succeeded (the crawler now asks the control
// plane before it fetches, but the window between its archive write and its
// `matches` insert cannot be closed that way). A second record is not inert: it
// is a second game in every count taken over these rows, so it doubled the
// participants behind the cell tallies, the wins behind each published win rate
// and the pick rate's numerator, while matches_used - count(DISTINCT match_id) -
// kept counting the match once. The tier list then published a rate and, beside
// it, an `n` that did not support it, which is worse than being wrong: a reader
// cannot tell which of the two numbers is the false one.
//
// De-duplicating here, at the single point where the archive is read, is what
// makes every later statement agree by construction. The alternatives do not:
// counting distinct per statement leaves the feature rows themselves doubled -
// the cell tallies that the published rates and the reconciliation gate are
// computed from - and the `matches` table being idempotent says nothing about
// the archive, which is a different store with no key for ON CONFLICT to
// collapse.
//
// The record kept is the first in part order, then in row order inside the part,
// which is the copy the control plane kept: the crawler inserts with ON CONFLICT
// DO NOTHING, so the earliest archived copy of a match is the one `matches`
// holds and the one a re-fetch never replaced. A payload whose match id cannot
// be read keeps a partition key of its own (its part and row number), so a row
// the gate has to count as malformed is never collapsed into another row.
//
// A payload that is not valid JSON yields NULL envelope fields and a false
// payload_valid here, and therefore a NULL payload in the second statement,
// instead of failing either one, so the build can count it and fail closed with
// a precise message rather than with a DuckDB parse error. A truncated or
// corrupted payload is exactly the input the fail-closed test feeds in.
//
// Every extraction is guarded, not just the payload column. DuckDB's
// json_extract and json_extract_string fail the entire statement on a payload
// it cannot parse, so an unguarded extraction would turn one corrupt row into
// an abort with an opaque parse error: the gate would never run, the archive
// stats would never be counted, and the audit row would lose its reason. A
// guarded extraction yields NULL envelope fields instead, which is the row the
// gate is built to count and refuse.
//
// participants is the one envelope field no later phase reduces: it is the
// length of the payload's participant array, and it is here so that the patch
// can be chosen from the envelope before any payload is unfolded (see
// envelopePatchListSQL). The feature spill produces one row per participant of
// every match the envelope kept, so this count is also what makes the two patch
// lists comparable column by column.
func envelopeSQL(parts []string) string {
	guarded := func(expression string) string {
		return "CASE WHEN json_valid(payload) THEN " + expression + " END"
	}
	text := func(path string) string {
		return guarded("json_extract_string(payload, " + quoteLiteral(path) + ")")
	}
	number := func(path, kind string) string {
		return "CAST(" + guarded("json_extract(payload, "+quoteLiteral(path)+")") + " AS " + kind + ")"
	}
	// arrayLen counts the members of an array in the payload, and yields NULL
	// when there is no such array: json_array_length fails the statement on a
	// value that is not one, so the type is checked first. A row whose
	// participants member is missing or is not an array is a row the participant
	// extraction cannot read either, and the spill reports it with its own error
	// rather than this statement failing on it first.
	arrayLen := func(path string) string {
		array := "json_extract(payload, " + quoteLiteral(path) + ")"
		return "CASE WHEN json_valid(payload) AND json_type(" + array + ") = 'ARRAY'" +
			" THEN CAST(json_array_length(" + array + ") AS INTEGER) ELSE NULL END"
	}
	return fmt.Sprintf(`SELECT
  part,
  part_row,
  match_id,
  game_version,
  platform_id,
  queue_id,
  game_creation_ms,
  payload_valid,
  participants
FROM (
  SELECT filename AS part,
         file_row_number AS part_row,
         %s AS match_id,
         %s AS game_version,
         %s AS platform_id,
         %s AS queue_id,
         %s AS game_creation_ms,
         %s AS participants,
         json_valid(payload) AS payload_valid
  FROM read_parquet(%s, union_by_name = true, filename = true, file_row_number = true)
) extracted
QUALIFY ROW_NUMBER() OVER (
  PARTITION BY COALESCE(match_id, part || '#' || CAST(part_row AS VARCHAR))
  ORDER BY part, part_row) = 1`,
		text("$.metadata.matchId"),
		text("$.info.gameVersion"),
		text("$.info.platformId"),
		number("$.info.queueId", "INTEGER"),
		number("$.info.gameCreation", "BIGINT"),
		arrayLen("$.info.participants"),
		fileList(parts))
}

// payloadSQL is the second half of the spill: the payload of every match
// envelopeSQL kept, with the envelope fields joined back on.
//
// The join is against the spilled keys rather than a second extraction, so this
// statement evaluates no JSON path at all: the archive is re-read for the
// payload column and the ~3 k keys are the join's build side, which is the
// shape the measurement above showed to fit where the single-statement version
// did not.
//
// Whether a payload may be carried is decided by the `payload_valid` flag
// envelopeSQL spilled, not by a second `json_valid` here. That is not a
// micro-optimisation: `json_valid` builds a JSON document from the 1.4 MB
// payload of every match, and a statement that both parses the payload and
// carries it out is the one shape that exhausted the 1 GiB engine limit even
// after the spill was split - measured, at 1023.9 MiB of the 1.0 GiB limit, in
// both the single-statement original and a keys-file-plus-join variant. Parsing
// the payload to a boolean in the keys-only statement (which discards the
// payload) and passing the payload through unparsed in this one fits, because
// neither half does both at once.
//
// The flag keeps the semantics the single-statement version had: a payload that
// is not valid JSON is stored as NULL here, so the unnesting statements see a
// row they can filter on `payload IS NOT NULL` instead of a row that fails
// their own json_extract with a parse error before the gate that is meant to
// report it has had a chance to run.
func payloadSQL(envelopePath string, parts []string) string {
	return fmt.Sprintf(`SELECT
  e.match_id,
  e.game_version,
  e.platform_id,
  e.queue_id,
  e.game_creation_ms,
  CASE WHEN e.payload_valid THEN CAST(a.payload AS VARCHAR) END AS payload
FROM (
  SELECT filename, file_row_number, payload
  FROM read_parquet(%s, union_by_name = true, filename = true, file_row_number = true)
) a
JOIN %s e ON a.filename = e.part AND a.file_row_number = e.part_row`,
		fileList(parts), parquetOf(envelopePath))
}

// runeStyleSQL renders the rune build key for one participant: the primary
// tree, the keystone, the secondary tree, then the three stat shards.
//
// The key is only produced when it is known to mean one thing. The styles array
// is documented as [primary, secondary, stat shards] and the descriptions are
// the identity of each entry, so the ordering is checked rather than assumed: a
// payload that returned the trees in another order, or returned fewer entries,
// yields NULL and is counted as a rejected row rather than grouped into a build
// whose meaning is unknown. A wrong build recommendation is worse than a
// missing one.
//
// The three shard perks are read from `statPerks`, which is where MATCH-V5
// documents them, and fall back to the third style entry. Both spellings of the
// stat-shard description are accepted because the staged fixture set uses
// "statMod" while the documented value is "statMods"; the ordering check above
// is what keeps the meaning fixed, not the spelling of that one word.
//
// The two list branches are the most expensive projection in the extraction: on
// the live archive, replacing this CASE with a NULL list of the same type is the
// difference between a batch's features COPY failing under a 1 GiB engine limit
// and completing under it (see extract). The measurement is why the extraction
// batches by parts at all, so treat a change here as a change to that bound.
func runeStyleSQL(alias string) string {
	at := func(path string) string { return fmt.Sprintf("json_extract(%s.value, '%s')", alias, path) }
	text := func(path string) string { return fmt.Sprintf("json_extract_string(%s.value, '%s')", alias, path) }
	nonNull := func(paths ...string) string {
		parts := make([]string, 0, len(paths))
		for _, path := range paths {
			parts = append(parts, at(path)+" IS NOT NULL")
		}
		return strings.Join(parts, " AND ")
	}
	ordered := "COALESCE(" +
		text("$.perks.styles[0].description") + " = 'primaryStyle', FALSE) AND " +
		"COALESCE(" + text("$.perks.styles[1].description") + " = 'subStyle', FALSE)"

	statPerks := "[CAST(" + at("$.perks.styles[0].style") + " AS INTEGER), " +
		"CAST(" + at("$.perks.styles[0].selections[0].perk") + " AS INTEGER), " +
		"CAST(" + at("$.perks.styles[1].style") + " AS INTEGER), " +
		"CAST(" + at("$.perks.statPerks.offense") + " AS INTEGER), " +
		"CAST(" + at("$.perks.statPerks.flex") + " AS INTEGER), " +
		"CAST(" + at("$.perks.statPerks.defense") + " AS INTEGER)]"

	styleShards := "[CAST(" + at("$.perks.styles[0].style") + " AS INTEGER), " +
		"CAST(" + at("$.perks.styles[0].selections[0].perk") + " AS INTEGER), " +
		"CAST(" + at("$.perks.styles[1].style") + " AS INTEGER), " +
		"CAST(" + at("$.perks.styles[2].selections[0].perk") + " AS INTEGER), " +
		"CAST(" + at("$.perks.styles[2].selections[1].perk") + " AS INTEGER), " +
		"CAST(" + at("$.perks.styles[2].selections[2].perk") + " AS INTEGER)]"

	return "CASE " +
		"WHEN " + ordered + " AND " + nonNull("$.perks.statPerks.offense", "$.perks.statPerks.flex", "$.perks.statPerks.defense") +
		" THEN " + statPerks + " " +
		"WHEN " + ordered + " AND " + text("$.perks.styles[2].description") + " IN ('statMods', 'statMod')" +
		" AND " + nonNull("$.perks.styles[2].selections[0].perk", "$.perks.styles[2].selections[1].perk", "$.perks.styles[2].selections[2].perk") +
		" THEN " + styleShards + " " +
		"ELSE NULL END"
}

// participantsSQL unnests the participants array of every match into one row
// per participant and extracts the feature columns the plan requires:
// champion, role, win, the seven item slots, the rune key and the two summoner
// spells. Timelines are never read: the tier list is computed from match
// summaries alone.
func participantsSQL(matchesPath string, filters string) string {
	// The position fields are read as text, not as JSON: the normaliser
	// compares them against literals, and a JSON value cannot be compared with
	// a string literal without casting the literal to JSON first, which fails
	// on the empty string that Riot uses for "no assigned position".
	field := func(path string) string { return "json_extract_string(p.value, '" + path + "')" }
	role := participantRoleSQL(field("$.teamPosition"), field("$.individualPosition"))
	return fmt.Sprintf(`SELECT
  m.match_id,
  m.game_version,
  m.platform_id,
  m.queue_id,
  epoch_ms(m.game_creation_ms) AS game_creation,
  CAST(split_part(m.game_version, '.', 1) || '.' || split_part(m.game_version, '.', 2) AS VARCHAR) AS patch,
  CAST(json_extract(p.value, '$.championId') AS INTEGER) AS champion_id,
  %s AS role,
  CAST(json_extract(p.value, '$.win') AS BOOLEAN) AS win,
  CAST(json_extract(p.value, '$.teamId') AS INTEGER) AS team_id,
  CAST(json_extract(p.value, '$.item0') AS INTEGER) AS item0,
  CAST(json_extract(p.value, '$.item1') AS INTEGER) AS item1,
  CAST(json_extract(p.value, '$.item2') AS INTEGER) AS item2,
  CAST(json_extract(p.value, '$.item3') AS INTEGER) AS item3,
  CAST(json_extract(p.value, '$.item4') AS INTEGER) AS item4,
  CAST(json_extract(p.value, '$.item5') AS INTEGER) AS item5,
  CAST(json_extract(p.value, '$.item6') AS INTEGER) AS item6,
  CAST(json_extract(p.value, '$.summoner1Id') AS INTEGER) AS summoner1_id,
  CAST(json_extract(p.value, '$.summoner2Id') AS INTEGER) AS summoner2_id,
  %s AS rune_style
FROM %s m, UNNEST(from_json(json_extract(m.payload, '$.info.participants'), '["JSON"]')) AS p(value)
WHERE %s`,
		role,
		runeStyleSQL("p"),
		parquetOf(matchesPath), filters)
}

// bansSQL unnests teams[].bans[] into one row per banned champion.
//
// The patch is carried on the row rather than filtered here: the spill is
// patch-free so that it is a function of the window alone, because the window is
// what the patch is chosen from - it cannot also be scoped by the choice it
// feeds (see envelopePatchListSQL). The reductions apply the patch when they
// read these rows back (banReduceFilter).
//
// Zero champion ids are filtered out. Riot uses 0 for "no ban" in queues that
// allow fewer than five bans per side, and counting those would inflate
// ban_rate.
func bansSQL(matchesPath string, filters string) string {
	return fmt.Sprintf(`SELECT
  m.match_id,
  CAST(split_part(m.game_version, '.', 1) || '.' || split_part(m.game_version, '.', 2) AS VARCHAR) AS patch,
  CAST(json_extract(b.value, '$.championId') AS INTEGER) AS champion_id
FROM %s m,
     UNNEST(from_json(json_extract(m.payload, '$.info.teams'), '["JSON"]')) AS t(team),
     UNNEST(from_json(json_extract(t.team, '$.bans'), '["JSON"]')) AS b(value)
WHERE %s AND CAST(json_extract(b.value, '$.championId') AS INTEGER) > 0`,
		parquetOf(matchesPath), filters)
}

// filterSQL renders the WHERE fragment every per-match statement shares: a
// usable envelope, the region, the queue, the patch window and, when the
// operator named one, the patch.
//
// The region is matched on both spellings of the platform (see region.go),
// because the published region and the payload's platformId are not the same
// string.
//
// The queue is matched exactly, and the partition is published under the queue
// it describes. Ranked flex and ranked solo are different populations with
// different pick rates, so a build that mixed them would publish a tier list
// that describes neither; a queue of zero means "every queue the archive holds"
// and still refuses rows with no queue id at all, which are rows whose envelope
// did not parse.
//
// The patch is derived from gameVersion rather than from the crawl partition,
// so a match played on the boundary patch but crawled after a partition
// rotation still lands in the right build.
func filterSQL(region, platform string, queue int, window aggmodel.Window, patch string) string {
	conditions := append([]string{
		"m.game_version IS NOT NULL",
		"m.match_id IS NOT NULL",
		"m.payload IS NOT NULL"},
		scopeConditions("m", region, platform, queue, window)...)
	conditions = append(conditions, patchCondition("m", patch))
	return strings.Join(nonEmpty(conditions), "\n    AND ")
}

// scopeConditions renders the predicates that select one window: the region, the
// queue and the date range.
//
// They are shared, rather than repeated, because the window is selected over two
// different relations - the payload spill, where a row carries its payload, and
// the envelope, where it does not - and a window that meant two different things
// depending on which relation it was read from would be a build whose patch and
// whose published rows came from different populations.
func scopeConditions(alias, region, platform string, queue int, window aggmodel.Window) []string {
	return []string{
		platformCondition(alias, platformFilter(region, platform)),
		queueCondition(alias, queue),
		fmt.Sprintf("CAST(epoch_ms(%s.game_creation_ms) AS DATE) BETWEEN DATE %s AND DATE %s",
			alias, quoteLiteral(window.From), quoteLiteral(window.To)),
	}
}

// envelopeFilterSQL is filterSQL over the envelope rather than over the payload
// spill.
//
// Two conditions differ, and both are because the envelope has no payload
// column. "carries a usable payload" is the payload_valid flag envelopeSQL
// spilled instead of a re-parsed json_valid, and "contributes a participant" is
// the participants count being a positive number: the unnest the feature spill
// runs produces one row per member of that array, and no rows at all when there
// is no array, so a match with no participants carries no patch either - it is
// the empty window the build refuses rather than an empty partition it publishes.
func envelopeFilterSQL(region, platform string, queue int, window aggmodel.Window) string {
	conditions := append([]string{
		"m.game_version IS NOT NULL",
		"m.match_id IS NOT NULL",
		"m.payload_valid",
		"m.participants > 0"},
		scopeConditions("m", region, platform, queue, window)...)
	return strings.Join(nonEmpty(conditions), "\n    AND ")
}

// queueCondition renders the queue predicate for one alias.
func queueCondition(alias string, queue int) string {
	if queue <= 0 {
		// Not pinned: every queue with a real queue id, which is the guard
		// against an envelope whose queueId did not parse.
		return alias + ".queue_id > 0"
	}
	return fmt.Sprintf("%s.queue_id = %d", alias, queue)
}

// platformCondition renders the region predicate for one alias. An empty list
// cannot happen (a region is always configured) but renders as a predicate that
// is simply false rather than as a syntax error.
func platformCondition(alias string, platforms []string) string {
	values := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		values = append(values, quoteLiteral(platform))
	}
	if len(values) == 1 {
		return fmt.Sprintf("%s.platform_id = %s", alias, values[0])
	}
	return fmt.Sprintf("%s.platform_id IN (%s)", alias, strings.Join(values, ", "))
}

// patchCondition renders the patch predicate for one alias, or the empty string
// when no patch is pinned.
func patchCondition(alias, patch string) string {
	if patch == "" {
		return ""
	}
	return fmt.Sprintf(
		"CAST(split_part(%s.game_version, '.', 1) || '.' || split_part(%s.game_version, '.', 2) AS VARCHAR) = %s",
		alias, alias, quoteLiteral(patch))
}

// patchOnlyFilter scopes a reduction over the extracted feature rows to one
// patch, or to every patch when no patch is pinned.
func patchOnlyFilter(alias, patch string) string {
	if patch == "" {
		return ""
	}
	return alias + ".patch = " + quoteLiteral(patch)
}

// featureFilter is the WHERE fragment every reduction over the extracted
// feature rows shares: a champion, a canonical role, a decided win or loss, and
// the patch the build publishes.
func featureFilter(alias, patch string) string {
	conditions := []string{
		alias + ".role IS NOT NULL",
		alias + ".champion_id > 0",
		alias + ".win IS NOT NULL",
		patchOnlyFilter(alias, patch),
	}
	return strings.Join(nonEmpty(conditions), " AND ")
}

// banReduceFilter is the WHERE fragment reductions over extracted ban rows
// share.
func banReduceFilter(alias, patch string) string {
	return strings.Join(nonEmpty([]string{alias + ".champion_id > 0", patchOnlyFilter(alias, patch)}), " AND ")
}

// cellCountsSQL reduces the feature rows to one tally per (champion, role).
func cellCountsSQL(featuresPath, patch string) string {
	return fmt.Sprintf(`SELECT
  p.champion_id,
  p.role,
  CAST(count(*) AS INTEGER) AS n,
  CAST(sum(CASE WHEN p.win THEN 1 ELSE 0 END) AS INTEGER) AS wins
FROM %s p
WHERE %s
GROUP BY p.champion_id, p.role
ORDER BY p.role, p.champion_id`, parquetOf(featuresPath), featureFilter("p", patch))
}

// banCountsSQL reduces the ban rows to one tally per champion, counted in
// distinct matches: a malformed payload could claim two bans for one champion
// in one match, and that must not count twice. It is also counted in distinct
// matches because ban_rate's denominator is matches, not ban slots.
func banCountsSQL(bansPath, patch string) string {
	return fmt.Sprintf(`SELECT
  b.champion_id,
  CAST(count(DISTINCT b.match_id) AS INTEGER) AS bans
FROM %s b
WHERE %s
GROUP BY b.champion_id
ORDER BY b.champion_id`, parquetOf(bansPath), banReduceFilter("b", patch))
}

// buildCountsSQL reduces the feature rows to one tally per build key, for the
// given key expression (the seven item slots, the six rune values, or the two
// summoner spell ids).
//
// extra narrows the grouping further; it is how the item grouping can require a
// non-empty inventory and the rune grouping can require a parseable style list,
// without those rules leaking into the shared feature filter.
func buildCountsSQL(featuresPath, patch, keyExpr, extra string) string {
	where := strings.Join(nonEmpty([]string{featureFilter("p", patch), extra}), " AND ")
	return fmt.Sprintf(`SELECT
  p.champion_id,
  p.role,
  %s AS key,
  CAST(count(*) AS INTEGER) AS n,
  CAST(sum(CASE WHEN p.win THEN 1 ELSE 0 END) AS INTEGER) AS wins
FROM %s p
WHERE %s
GROUP BY 1, 2, 3
ORDER BY 1, 2`, keyExpr, parquetOf(featuresPath), where)
}

// matchupCountsSQL reduces the feature rows to one tally per ordered champion
// pair per role, in the direction the contract publishes: lower champion id
// first.
//
// The self join is on the same match and the same role with different teams,
// which is exactly the lane opponent. The mirror case (both teams fielded the
// same champion in the same role) is ordered by team id instead of dropped:
// aggregated normal games can mirror, and a pair that really happened must not
// disappear from the matrix.
func matchupCountsSQL(featuresPath, patch string) string {
	return fmt.Sprintf(`SELECT
  a.role AS role,
  a.champion_id,
  b.champion_id AS opponent_id,
  CAST(count(*) AS INTEGER) AS n,
  CAST(sum(CASE WHEN a.win THEN 1 ELSE 0 END) AS INTEGER) AS wins
FROM %s a
JOIN %s b
  ON a.match_id = b.match_id
 AND a.role = b.role
 AND a.team_id <> b.team_id
WHERE %s AND %s
  AND (a.champion_id < b.champion_id OR (a.champion_id = b.champion_id AND a.team_id < b.team_id))
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3`,
		parquetOf(featuresPath), parquetOf(featuresPath), featureFilter("a", patch), featureFilter("b", patch))
}

// patchListSQL lists the patches the extracted window holds, with the number of
// matches and of participant rows each contributed, so the build can pick the
// newest patch deterministically when the operator did not name one.
func patchListSQL(featuresPath string) string {
	return fmt.Sprintf(`SELECT
  p.patch,
  CAST(count(DISTINCT p.match_id) AS INTEGER) AS matches,
  CAST(count(*) AS INTEGER) AS participant_rows
FROM %s p
WHERE p.patch IS NOT NULL
GROUP BY p.patch
ORDER BY p.patch`, parquetOf(featuresPath))
}

// envelopePatchListSQL is patchListSQL over the envelope instead of over the
// feature spill: the same window, the same matches, counted before any payload
// has been unfolded.
//
// This is what lets the build choose the patch before it extracts, and choosing
// the patch before it extracts is what lets the audit row name the partition the
// build is about to publish: the store refuses a run whose patch is empty, so a
// row opened before the choice is a row that is not written at all whenever the
// operator did not pin a patch.
//
// The patch expression is deliberately the one participantsSQL derives, guard
// included: a match with no readable game version has no patch, and a NULL group
// would reach the chooser as a patch it has to reject as malformed rather than
// as a row that simply carries none.
func envelopePatchListSQL(envelopePath, filters string) string {
	return fmt.Sprintf(`SELECT
  CAST(split_part(m.game_version, '.', 1) || '.' || split_part(m.game_version, '.', 2) AS VARCHAR) AS patch,
  CAST(count(*) AS INTEGER) AS matches,
  CAST(sum(m.participants) AS INTEGER) AS participant_rows
FROM %s m
WHERE %s
GROUP BY 1
ORDER BY 1`, parquetOf(envelopePath), filters)
}

// archiveStatsSQL reports archive-wide counts. They are not scoped to the
// window: the archive is immutable input, so a row that cannot be read at all is
// a reason to stop even when the damage happens to fall outside today's window,
// because the next window may not be so lucky.
func archiveStatsSQL(matchesPath string) string {
	matches := parquetOf(matchesPath)
	return fmt.Sprintf(`SELECT
  CAST((SELECT count(*) FROM %s) AS INTEGER) AS archive_rows,
  CAST((SELECT count(*) FROM %s WHERE payload IS NULL OR match_id IS NULL OR game_version IS NULL OR queue_id IS NULL OR game_creation_ms IS NULL) AS INTEGER) AS malformed_rows
`, matches, matches)
}

// windowStatsSQL reports the counts the gates and the rate denominators use.
//
// It is patch-scoped, because the gates compare these figures against the cell
// tallies, which are patch-scoped: comparing a window-wide participant count
// against a patch-scoped sum of cell sample sizes would fail the reconciliation
// gate on every window that spans two patches. The match count is scoped by the
// same filter the extraction used plus the chosen patch, so matches_used and the
// feature rows come from one predicate over one patch.
func windowStatsSQL(matchesPath, featuresPath, bansPath, filters, patch string) string {
	matches, features, bans := parquetOf(matchesPath), parquetOf(featuresPath), parquetOf(bansPath)
	scope := patchOnlyFilter("p", patch)
	// matches_used is the denominator of pick_rate and ban_rate, so it has to
	// count the same games the cells are counted from. A window that spans a
	// patch rollover - which is the normal case for a nightly build on the day
	// a patch ships - contains matches on two patches, and counting both would
	// halve every pick rate in the published partition.
	matchFilter := strings.Join(nonEmpty([]string{filters, patchCondition("m", patch)}), " AND ")
	rejected := strings.Join(nonEmpty([]string{
		scope,
		"(p.role IS NULL OR p.champion_id IS NULL OR p.champion_id <= 0 OR p.win IS NULL)",
	}), " AND ")
	scoped := ""
	if scope != "" {
		scoped = "WHERE " + scope
	}
	return fmt.Sprintf(`SELECT
  CAST((SELECT count(DISTINCT m.match_id) FROM %s m WHERE %s) AS INTEGER) AS matches_used,
  CAST((SELECT count(*) FROM %s p %s) AS INTEGER) AS participant_rows,
  CAST((SELECT count(*) FROM %s p WHERE %s) AS INTEGER) AS rejected_rows,
  CAST((SELECT count(*) FROM %s b) AS INTEGER) AS ban_rows
`, matches, matchFilter, features, scoped, features, rejected, bans)
}

// copyJSON writes a query result to a JSON array file.
func copyJSON(query string, target string) string {
	return fmt.Sprintf("COPY (\n%s\n) TO %s (FORMAT JSON, ARRAY true);\n", query, quoteLiteral(target))
}

// copyParquet writes a query result to a parquet file with the zstd codec, the
// same codec the raw archive uses.
func copyParquet(query string, target string) string {
	return fmt.Sprintf("COPY (\n%s\n) TO %s (FORMAT PARQUET, COMPRESSION ZSTD);\n", query, quoteLiteral(target))
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
