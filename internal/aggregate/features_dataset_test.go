package aggregate

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The feature dataset's value-level fixture test.
//
// The dataset is six tables of numbers, and the only test that means anything
// about numbers is one that says which numbers. Every expectation below is
// hand-derived from the fixture model in fixturetimeline_test.go - the model's
// fixtureGrowth documents the per-minute arithmetic, and each want block is that
// arithmetic written out - so a reader can check a row against the model without
// running anything, and the build cannot change a figure without failing here.
//
// The blocks are compared as CSV rather than decoded into structs. A decoded row
// is a list of assertions about the columns the test author remembered to name,
// and the columns nobody names are exactly the ones a regression changes; the
// csv rendering puts every column of every selected row in the diff. It also
// keeps NULL visible as an empty field, which matters more here than anywhere
// else in this repository: the tables promise that a checkpoint the game never
// reached is NULL rather than zero, and a struct of zero values would make the
// promise untestable.
//
// What each case is for, beyond its own table:
//
//   - match_index grades all eight archive shapes, so the ledger's exclusion
//     CASE is pinned in its full order rather than at one branch.
//   - minutes_204_irregular_spacing is the correctness rule the whole per-minute
//     extract rests on. Match 204's frames are 65 s apart, so its minute column
//     skips 12 and every later minute is one below the frame index. A build that
//     derived the minute from the frame index would produce a contiguous 1..16
//     here and a plausible wrong cs curve with it.
//   - lane_matchups_204_grace is the same spacing seen through the checkpoint
//     picker: the minute-10 row is the 585 s frame, whose own minute is 9,
//     because that is the frame nearest 600 s inside the accepted band.
//   - lane_matchups_208_null and participant_early_208_first_death are the
//     never-imputed rule: a 13-minute game has no minute-15 checkpoint, and the
//     absence is NULL in every derived column rather than a zero or a
//     carried-forward frame.
//   - events_201 carries at least one of every event shape the extract reads,
//     including the duplicated SKILL_LEVEL_UP that Patch 15.17 emitted and the
//     event whose timestamp is not on a frame boundary.
func TestFeatureDatasetFixtureGoldens(t *testing.T) {
	result := featureFixtureDataset(t)
	for _, golden := range featureFixtureGoldens {
		t.Run(golden.name, func(t *testing.T) {
			statement := strings.Replace(golden.query, "%s", featureTableGlob(result, golden.table), 1)
			if got := featureQueryCSV(t, result, statement); got != golden.want {
				t.Errorf("query:\n%s\n\ngot:\n%s\nwant:\n%s", statement, got, golden.want)
			}
		})
	}
}

// featureFixtureDataset builds the fixture dataset once per test.
//
// The build is a DuckDB pass over eight matches and takes about half a second,
// so every test in this file rebuilding it is cheaper than the bookkeeping that
// sharing one would need - and a shared build would have to outlive the
// t.TempDir of whichever test happened to run first.
func featureFixtureDataset(t *testing.T) FeatureResult {
	t.Helper()

	result, err := Features(context.Background(),
		featureFixtureOptions(t, t.TempDir(), featureFixtureRawRoot(t, fixtureTimelineMatches())))
	if err != nil {
		t.Fatalf("fixture build: %v", err)
	}
	return result
}

// featureFixtureGoldens is the expected csv rendering of one query per case.
//
// "%s" is the published glob of the case's own table, substituted once by the
// test so that a case names its table rather than a path.
var featureFixtureGoldens = []struct {
	name  string
	table string
	query string
	want  string
}{
	{
		name:  "match_index",
		table: "match_index",
		query: `SELECT match_id, region, patch, queue_id, game_duration_s, end_of_game_result,
       frame_interval_ms, frame_count, participant_frame_count, timeline_present,
       exclusion_reason, timeline_eligible
FROM %s
ORDER BY match_id`,
		want: `match_id,region,patch,queue_id,game_duration_s,end_of_game_result,frame_interval_ms,frame_count,participant_frame_count,timeline_present,exclusion_reason,timeline_eligible
EUW1_0000000201,EUW,16.03,420,1830,GameComplete,60000,30,300,true,ok,true
EUW1_0000000202,EUW,16.03,420,240,GameComplete,60000,4,40,true,too_short,false
EUW1_0000000203,EUW,16.04,420,90,Abort_TooFewPlayers,0,0,0,true,aborted,false
EUW1_0000000204,EUW,16.05,420,1080,GameComplete,60000,16,160,true,ok,true
EUW1_0000000205,EUW,16.05,420,1830,GameComplete,NULL,NULL,NULL,false,no_timeline,false
EUW1_0000000206,EUW,16.06,420,1830,GameComplete,0,3,30,true,frame_interval_zero,false
EUW1_0000000207,EUW,16.06,420,1830,GameComplete,60000,3,0,true,frames_null,false
EUW1_0000000208,EUW,16.07,420,780,GameComplete,60000,13,130,true,ok,true
`,
	},
	{
		name:  "minutes_201_participant_1",
		table: "participant_minutes",
		query: `SELECT minute, frame_timestamp_ms, minions_killed, jungle_minions_killed, cs_total,
       xp, total_gold, current_gold, level, position_x, position_y,
       kills_to_minute, deaths_to_minute, assists_to_minute
FROM %s
WHERE match_id = 'EUW1_0000000201' AND participant_id = 1 AND minute IN (1, 5, 10, 15, 30)
ORDER BY minute`,
		want: `minute,frame_timestamp_ms,minions_killed,jungle_minions_killed,cs_total,xp,total_gold,current_gold,level,position_x,position_y,kills_to_minute,deaths_to_minute,assists_to_minute
1,60000,8,0,8,300,500,100,1,1000,200,0,0,0
5,300000,40,0,40,1500,2500,500,2,1000,1000,1,0,0
10,600000,80,0,80,3000,5000,1000,4,1000,2000,1,1,0
15,900000,120,0,120,4500,7500,1500,6,1000,3000,2,1,0
30,1800000,240,0,240,9000,15000,3000,11,1000,6000,3,1,0
`,
	},
	{
		name:  "minutes_201_participant_2_jungle",
		table: "participant_minutes",
		query: `SELECT minute, minions_killed, jungle_minions_killed, cs_total
FROM %s
WHERE match_id = 'EUW1_0000000201' AND participant_id = 2 AND minute IN (1, 5)
ORDER BY minute`,
		want: `minute,minions_killed,jungle_minions_killed,cs_total
1,0,8,8
5,0,36,36
`,
	},
	{
		name:  "minutes_204_irregular_spacing",
		table: "participant_minutes",
		query: `SELECT minute, frame_timestamp_ms, cs_total
FROM %s
WHERE match_id = 'EUW1_0000000204' AND participant_id = 1
ORDER BY minute`,
		want: `minute,frame_timestamp_ms,cs_total
1,65000,8
2,130000,16
3,195000,24
4,260000,32
5,325000,40
6,390000,48
7,455000,56
8,520000,64
9,585000,72
10,650000,80
11,715000,88
13,780000,104
14,845000,112
15,910000,120
16,975000,128
17,1040000,136
`,
	},
	{
		name:  "lane_matchups_201",
		table: "lane_matchups",
		query: `SELECT *
FROM %s
WHERE match_id = 'EUW1_0000000201'
ORDER BY participant_id`,
		want: `match_id,region,patch,queue_id,participant_id,opponent_participant_id,role,champion_id,opponent_champion_id,team_id,win,pairing_basis,is_duo_lane,has_nominal_opponent,cs_5,opponent_cs_5,cs_diff_5,xp_diff_5,gold_diff_5,level_diff_5,kills_5,deaths_5,assists_5,cs_10,opponent_cs_10,cs_diff_10,xp_diff_10,gold_diff_10,level_diff_10,kills_10,deaths_10,assists_10,cs_15,opponent_cs_15,cs_diff_15,xp_diff_15,gold_diff_15,level_diff_15,kills_15,deaths_15,assists_15
EUW1_0000000201,EUW,16.03,420,1,6,TOP,24,86,100,true,team_position,false,true,40,45,-5,-50,-100,0,1,0,0,80,85,-5,-50,-100,0,1,1,0,120,125,-5,-50,-100,0,2,1,0
EUW1_0000000201,EUW,16.03,420,2,7,JUNGLE,64,121,100,true,team_position,false,false,36,41,-5,-50,-100,0,0,0,1,71,76,-5,-50,-100,0,0,0,1,106,111,-5,-50,-100,0,0,0,1
EUW1_0000000201,EUW,16.03,420,3,8,MID,134,1,100,true,team_position,false,true,42,47,-5,-50,-100,0,0,0,0,82,87,-5,-50,-100,0,0,0,0,122,127,-5,-50,-100,0,0,0,0
EUW1_0000000201,EUW,16.03,420,4,9,BOTTOM,202,145,100,true,team_position,true,true,43,48,-5,-50,-100,0,0,0,0,83,88,-5,-50,-100,0,0,0,0,123,128,-5,-50,-100,0,0,0,0
EUW1_0000000201,EUW,16.03,420,5,10,SUPPORT,111,412,100,true,team_position,true,true,44,49,-5,-50,-100,0,0,0,0,84,89,-5,-50,-100,0,0,0,0,124,129,-5,-50,-100,0,0,0,0
EUW1_0000000201,EUW,16.03,420,6,1,TOP,86,24,200,false,team_position,false,true,45,40,5,50,100,0,0,1,0,85,80,5,50,100,0,1,1,0,125,120,5,50,100,0,1,1,0
EUW1_0000000201,EUW,16.03,420,7,2,JUNGLE,121,64,200,false,team_position,false,false,41,36,5,50,100,0,0,0,0,76,71,5,50,100,0,0,0,0,111,106,5,50,100,0,0,1,0
EUW1_0000000201,EUW,16.03,420,8,3,MID,1,134,200,false,team_position,false,true,47,42,5,50,100,0,0,0,0,87,82,5,50,100,0,0,0,0,127,122,5,50,100,0,0,0,0
EUW1_0000000201,EUW,16.03,420,9,4,BOTTOM,145,202,200,false,team_position,true,true,48,43,5,50,100,0,0,0,0,88,83,5,50,100,0,0,0,0,128,123,5,50,100,0,0,0,0
EUW1_0000000201,EUW,16.03,420,10,5,SUPPORT,412,111,200,false,team_position,true,true,49,44,5,50,100,0,0,0,0,89,84,5,50,100,0,0,0,0,129,124,5,50,100,0,0,0,0
`,
	},
	{
		name:  "lane_matchups_204_grace",
		table: "lane_matchups",
		query: `SELECT participant_id, role, cs_5, cs_10, opponent_cs_10, cs_15
FROM %s
WHERE match_id = 'EUW1_0000000204'
ORDER BY participant_id`,
		want: `participant_id,role,cs_5,cs_10,opponent_cs_10,cs_15
1,TOP,40,72,77,120
2,JUNGLE,36,64,69,106
3,MID,42,74,79,122
4,BOTTOM,43,75,80,123
5,SUPPORT,44,76,81,124
6,TOP,45,77,72,125
7,JUNGLE,41,69,64,111
8,MID,47,79,74,127
9,BOTTOM,48,80,75,128
10,SUPPORT,49,81,76,129
`,
	},
	{
		name:  "lane_matchups_208_null",
		table: "lane_matchups",
		query: `SELECT participant_id, cs_5, cs_10, cs_15, xp_diff_15, gold_diff_15, level_diff_15,
       kills_15, deaths_15, assists_15
FROM %s
WHERE match_id = 'EUW1_0000000208'
ORDER BY participant_id`,
		want: `participant_id,cs_5,cs_10,cs_15,xp_diff_15,gold_diff_15,level_diff_15,kills_15,deaths_15,assists_15
1,40,80,NULL,NULL,NULL,NULL,NULL,NULL,NULL
2,36,71,NULL,NULL,NULL,NULL,NULL,NULL,NULL
3,42,82,NULL,NULL,NULL,NULL,NULL,NULL,NULL
4,43,83,NULL,NULL,NULL,NULL,NULL,NULL,NULL
5,44,84,NULL,NULL,NULL,NULL,NULL,NULL,NULL
6,45,85,NULL,NULL,NULL,NULL,NULL,NULL,NULL
7,41,76,NULL,NULL,NULL,NULL,NULL,NULL,NULL
8,47,87,NULL,NULL,NULL,NULL,NULL,NULL,NULL
9,48,88,NULL,NULL,NULL,NULL,NULL,NULL,NULL
10,49,89,NULL,NULL,NULL,NULL,NULL,NULL,NULL
`,
	},
	{
		name:  "participant_early_201",
		table: "participant_early",
		query: `SELECT *
FROM %s
WHERE match_id = 'EUW1_0000000201'
ORDER BY participant_id`,
		want: `match_id,region,patch,queue_id,participant_id,champion_id,role,team_id,win,first_blood_involvement,first_death_ts_ms,first_death_minute,first_death_position_x,first_death_position_y,plates_destroyed,plates_at_5,plates_at_10,plates_at_15,first_item_ts_ms,first_item_id,second_item_ts_ms,second_item_id,skill_order,duplicate_skill_ups,wards_placed,wards_killed,dragons_participated,grubs_participated,heralds_participated,barons_participated,atakhan_participated
EUW1_0000000201,EUW,16.03,420,1,24,TOP,100,true,true,481000,8,6000,6100,3,1,3,3,60000,1055,420000,3153,12,1,0,0,0,0,0,0,0
EUW1_0000000201,EUW,16.03,420,2,64,JUNGLE,100,true,true,NULL,NULL,NULL,NULL,0,0,0,0,NULL,NULL,NULL,NULL,NULL,0,0,0,1,1,1,1,0
EUW1_0000000201,EUW,16.03,420,3,134,MID,100,true,false,NULL,NULL,NULL,NULL,0,0,0,0,NULL,NULL,NULL,NULL,NULL,0,1,1,0,0,0,0,0
EUW1_0000000201,EUW,16.03,420,4,202,BOTTOM,100,true,false,NULL,NULL,NULL,NULL,0,0,0,0,NULL,NULL,NULL,NULL,NULL,0,0,0,0,0,0,0,0
EUW1_0000000201,EUW,16.03,420,5,111,SUPPORT,100,true,false,NULL,NULL,NULL,NULL,0,0,0,0,NULL,NULL,NULL,NULL,NULL,0,0,0,0,0,0,0,0
EUW1_0000000201,EUW,16.03,420,6,86,TOP,200,false,false,181000,3,5000,5000,0,0,0,0,NULL,NULL,NULL,NULL,NULL,0,0,0,0,0,0,0,0
EUW1_0000000201,EUW,16.03,420,7,121,JUNGLE,200,false,false,721000,12,0,0,0,0,0,0,NULL,NULL,NULL,NULL,NULL,0,0,0,1,0,0,0,0
EUW1_0000000201,EUW,16.03,420,8,1,MID,200,false,false,NULL,NULL,NULL,NULL,0,0,0,0,NULL,NULL,NULL,NULL,NULL,0,0,0,0,0,0,0,0
EUW1_0000000201,EUW,16.03,420,9,145,BOTTOM,200,false,false,NULL,NULL,NULL,NULL,0,0,0,0,NULL,NULL,NULL,NULL,NULL,0,0,0,0,0,0,0,0
EUW1_0000000201,EUW,16.03,420,10,412,SUPPORT,200,false,false,NULL,NULL,NULL,NULL,0,0,0,0,NULL,NULL,NULL,NULL,NULL,0,0,0,0,0,0,0,0
`,
	},
	{
		name:  "participant_early_208_first_death",
		table: "participant_early",
		query: `SELECT participant_id, first_death_ts_ms, first_death_minute,
       first_death_position_x, first_death_position_y
FROM %s
WHERE match_id = 'EUW1_0000000208'
ORDER BY participant_id`,
		want: `participant_id,first_death_ts_ms,first_death_minute,first_death_position_x,first_death_position_y
1,NULL,NULL,NULL,NULL
2,NULL,NULL,NULL,NULL
3,NULL,NULL,NULL,NULL
4,NULL,NULL,NULL,NULL
5,NULL,NULL,NULL,NULL
6,66000,1,0,0
7,NULL,NULL,NULL,NULL
8,NULL,NULL,NULL,NULL
9,NULL,NULL,NULL,NULL
10,NULL,NULL,NULL,NULL
`,
	},
	{
		name:  "match_objectives",
		table: "match_objectives",
		query: `SELECT *
FROM %s
ORDER BY match_id`,
		want: `match_id,region,patch,queue_id,game_duration_s,first_blood_team,first_blood_ts_ms,first_tower_team,first_tower_ts_ms,first_dragon_team,first_dragon_ts_ms,soul_team,soul_type,kills_team_100,kills_team_200,dragons_team_100,dragons_team_200,grubs_team_100,grubs_team_200,heralds_team_100,heralds_team_200,barons_team_100,barons_team_200,atakhan_team_100,atakhan_team_200,souls_team_100,souls_team_200,towers_team_100,towers_team_200,plates_team_100,plates_team_200
EUW1_0000000201,EUW,16.03,420,1830,100,181000,100,541000,100,301000,100,FIRE_DRAGON,3,1,1,1,1,0,1,0,1,0,0,0,1,0,1,0,3,0
EUW1_0000000204,EUW,16.05,420,1080,100,305000,NULL,NULL,NULL,NULL,NULL,NULL,1,1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0
EUW1_0000000208,EUW,16.07,420,780,100,66000,NULL,NULL,NULL,NULL,NULL,NULL,1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0
`,
	},
	{
		name:  "events_201",
		table: "events",
		query: `SELECT *
FROM %s
WHERE match_id = 'EUW1_0000000201'
ORDER BY event_index`,
		want: `match_id,region,patch,queue_id,event_index,timestamp_ms,minute,event_type,actor_participant_id,target_participant_id,assisting_participant_ids,team_id,position_x,position_y,lane_type,monster_type,monster_subtype,building_type,tower_type,ward_type,item_id,before_item_id,after_item_id,skill_slot,level,kill_type,multi_kill_length,transform_type,kill_streak_length,bounty,shutdown_bounty,winning_team,is_duplicate_skill_level_up
EUW1_0000000201,EUW,16.03,420,0,60000,1,SKILL_LEVEL_UP,1,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,1,2,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,1,60000,1,ITEM_PURCHASED,1,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,1055,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,2,181000,3,CHAMPION_KILL,1,6,[2],NULL,5000,5000,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,300,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,3,181000,3,CHAMPION_SPECIAL_KILL,1,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,KILL_FIRST_BLOOD,1,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,4,245000,4,TURRET_PLATE_DESTROYED,1,NULL,NULL,100,NULL,NULL,TOP,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,5,301000,5,ELITE_MONSTER_KILL,2,NULL,NULL,100,NULL,NULL,NULL,DRAGON,FIRE_DRAGON,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,6,305000,5,TURRET_PLATE_DESTROYED,1,NULL,NULL,100,NULL,NULL,TOP,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,7,361000,6,ELITE_MONSTER_KILL,2,NULL,NULL,100,NULL,NULL,NULL,HORDE,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,8,365000,6,TURRET_PLATE_DESTROYED,1,NULL,NULL,100,NULL,NULL,TOP,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,9,420000,7,ITEM_PURCHASED,1,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,3153,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,10,481000,8,CHAMPION_KILL,6,1,[],NULL,6000,6100,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,300,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,11,541000,9,BUILDING_KILL,1,NULL,NULL,100,3000,3000,TOP,NULL,NULL,TOWER,OUTER,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,12,601000,10,WARD_PLACED,3,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,YELLOW_TRINKET,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,13,661000,11,ELITE_MONSTER_KILL,7,NULL,NULL,200,NULL,NULL,NULL,DRAGON,WATER_DRAGON,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,14,721000,12,CHAMPION_KILL,1,7,[],NULL,0,0,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,300,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,15,725000,12,WARD_KILL,3,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,YELLOW_TRINKET,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,16,781000,13,SKILL_LEVEL_UP,1,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,1,2,NULL,NULL,NULL,NULL,NULL,NULL,NULL,true
EUW1_0000000201,EUW,16.03,420,17,785000,13,SKILL_LEVEL_UP,1,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,2,3,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,18,961000,16,CHAMPION_KILL,1,6,[],NULL,0,0,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,0,300,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,19,1021000,17,ELITE_MONSTER_KILL,2,NULL,NULL,100,NULL,NULL,NULL,BARON_NASHOR,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,20,1081000,18,ELITE_MONSTER_KILL,2,NULL,NULL,100,NULL,NULL,NULL,RIFTHERALD,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
EUW1_0000000201,EUW,16.03,420,21,1201000,20,DRAGON_SOUL_GIVEN,NULL,NULL,NULL,100,NULL,NULL,NULL,NULL,FIRE_DRAGON,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,false
`,
	},
	{
		name:  "events_204",
		table: "events",
		query: `SELECT event_index, event_type, timestamp_ms, minute, actor_participant_id,
       target_participant_id, assisting_participant_ids, team_id
FROM %s
WHERE match_id = 'EUW1_0000000204'
ORDER BY event_index`,
		want: `event_index,event_type,timestamp_ms,minute,actor_participant_id,target_participant_id,assisting_participant_ids,team_id
0,CHAMPION_KILL,305000,5,1,6,[],NULL
1,CHAMPION_KILL,610000,10,6,1,[],NULL
`,
	},
	{
		name:  "events_208",
		table: "events",
		query: `SELECT event_index, event_type, timestamp_ms, minute, actor_participant_id,
       target_participant_id, assisting_participant_ids, team_id
FROM %s
WHERE match_id = 'EUW1_0000000208'
ORDER BY event_index`,
		want: `event_index,event_type,timestamp_ms,minute,actor_participant_id,target_participant_id,assisting_participant_ids,team_id
0,CHAMPION_KILL,66000,1,1,6,[],NULL
`,
	},
	{
		name:  "minutes_census",
		table: "participant_minutes",
		query: `SELECT match_id, count(*) AS rows, count(DISTINCT participant_id) AS participants,
       min(minute) AS first_minute, max(minute) AS last_minute
FROM %s
GROUP BY match_id
ORDER BY match_id`,
		want: `match_id,rows,participants,first_minute,last_minute
EUW1_0000000201,300,10,1,30
EUW1_0000000204,160,10,1,17
EUW1_0000000208,130,10,1,13
`,
	},
	{
		name:  "events_census",
		table: "events",
		query: `SELECT match_id, count(*) AS rows, count(DISTINCT event_index) AS indices
FROM %s
GROUP BY match_id
ORDER BY match_id`,
		want: `match_id,rows,indices
EUW1_0000000201,22,22
EUW1_0000000204,2,2
EUW1_0000000208,1,1
`,
	},
	{
		name:  "matchup_census",
		table: "lane_matchups",
		query: `SELECT match_id, count(*) AS rows, count(DISTINCT participant_id) AS participants,
       sum(CASE WHEN cs_15 IS NULL THEN 1 ELSE 0 END) AS null_cs_15
FROM %s
GROUP BY match_id
ORDER BY match_id`,
		want: `match_id,rows,participants,null_cs_15
EUW1_0000000201,10,10,0
EUW1_0000000204,10,10,0
EUW1_0000000208,10,10,10
`,
	},
	{
		name:  "early_census",
		table: "participant_early",
		query: `SELECT match_id, count(*) AS rows, sum(CASE WHEN first_blood_involvement THEN 1 ELSE 0 END) AS first_blood
FROM %s
GROUP BY match_id
ORDER BY match_id`,
		want: `match_id,rows,first_blood
EUW1_0000000201,10,2
EUW1_0000000204,10,1
EUW1_0000000208,10,1
`,
	},
	{
		name:  "events_assist_types",
		table: "events",
		query: `SELECT typeof(assisting_participant_ids) AS assisting_type, count(*) AS rows
FROM %s
GROUP BY 1
ORDER BY 1`,
		want: `assisting_type,rows
INTEGER[],25
`,
	},
	{
		name:  "objectives_count",
		table: "match_objectives",
		query: `SELECT count(*) AS rows FROM %s`,
		want: `rows
3
`,
	},
}

// TestFeatureDatasetFixtureLedger pins the build's own accounting.
//
// The value goldens above say what the tables contain; this says what the build
// concluded about the archive it read, which is the half a reader cannot check
// from the tables alone. The two have to agree: lane_matchups carries ten rows
// for each of the three eligible matches and none for the five the ledger
// explains, and the reconciliation gate exists because a pairing that matched
// the wrong opponent produces a complete, plausible table - so the row counts
// and the ledger's verdict are asserted here beside each other.
func TestFeatureDatasetFixtureLedger(t *testing.T) {
	result := featureFixtureDataset(t)

	// Eight matches were crawled, seven timelines were fetched: match 205 aged
	// out of the timeline window before the backfill reached it, which is a
	// missing row and not an empty payload.
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"archive rows", result.Counts.ArchiveRows, 8},
		{"timeline rows", result.Counts.TimelineRows, 7},
		{"matches", result.Counts.Matches, 8},
		{"timeline present", result.Counts.TimelinePresent, 7},
		{"eligible", result.Counts.Eligible, 3},
		{"malformed payloads", result.Counts.Malformed, 0},
		{"orphan timelines", result.Counts.Orphans, 0},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}

	// One reason per archive shape, each counted once. A reason that went
	// missing here is a hole in the ledger's CASE; a reason that appeared with
	// the wrong count is a match graded by the wrong branch of it.
	wantExcluded := map[string]int{
		"ok":                  3,
		"too_short":           1,
		"aborted":             1,
		"no_timeline":         1,
		"frame_interval_zero": 1,
		"frames_null":         1,
	}
	if !reflect.DeepEqual(result.Counts.Excluded, wantExcluded) {
		t.Errorf("exclusions = %v, want %v", result.Counts.Excluded, wantExcluded)
	}

	// The per-table counts the manifest publishes, which are read back from the
	// tables after they are written rather than carried from the statements.
	wantRows := map[string]int{
		"match_index":         8,
		"participant_minutes": 590,
		"events":              25,
		"lane_matchups":       30,
		"participant_early":   30,
		"match_objectives":    3,
	}
	if !reflect.DeepEqual(result.Counts.TableRows, wantRows) {
		t.Errorf("table rows = %v, want %v", result.Counts.TableRows, wantRows)
	}
	if len(result.Tables) != len(featureTables) {
		t.Fatalf("manifest tables = %v, want all %d", result.Tables, len(featureTables))
	}
	for _, table := range result.Tables {
		if table.Rows != wantRows[table.Name] {
			t.Errorf("manifest row count for %s = %d, want %d", table.Name, table.Rows, wantRows[table.Name])
		}
	}

	// The dataset and the build are described by three documents, and a dataset
	// without them is a directory of parquet files nobody can interpret: the
	// schema is the machine-readable column list, the README is the entry point
	// and the manifest is the receipt.
	for _, doc := range []string{"manifest.json", "schema.json", "README.md"} {
		if _, err := os.Stat(filepath.Join(result.Dir, doc)); err != nil {
			t.Errorf("published dataset has no %s: %v", doc, err)
		}
	}
	for _, table := range featureTables {
		if _, err := os.Stat(filepath.Join(result.Dir, table)); err != nil {
			t.Errorf("published dataset has no %s table: %v", table, err)
		}
	}

	manifest := result.Manifest
	if manifest.Dataset != FeatureDatasetDir {
		t.Errorf("manifest dataset = %q, want %q", manifest.Dataset, FeatureDatasetDir)
	}
	if got, want := manifest.Sources, []string{featureSummarySource, featureTimelineSource}; !reflect.DeepEqual(got, want) {
		t.Errorf("manifest sources = %v, want %v", got, want)
	}
	if manifest.Eligible != 3 || manifest.Matches != 8 {
		t.Errorf("manifest matches/eligible = %d/%d, want 8/3", manifest.Matches, manifest.Eligible)
	}
	if manifest.MinDurationS != DefaultFeatureMinDurationS {
		t.Errorf("manifest min_duration_s = %d, want the default %d", manifest.MinDurationS, DefaultFeatureMinDurationS)
	}
	if manifest.RawParts != 1 || manifest.TimelineParts != 1 {
		t.Errorf("manifest raw/timeline parts = %d/%d, want 1/1", manifest.RawParts, manifest.TimelineParts)
	}
}
