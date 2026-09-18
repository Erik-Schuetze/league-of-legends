package aggregate

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
)

// This file carries the tests that watch the two things a published statistic
// must never do: appear from input it cannot be computed from, and disappear
// when a run fails.
//
// The fixture archive is the same hand-authored archive the arithmetic tests
// use, edited one field at a time. Editing a payload rather than adding a
// second fixture set keeps the corruption honest: the only difference between
// the good build and the failing build is the single field the test names.

// ---------------------------------------------------------------------------
// Payload editing
// ---------------------------------------------------------------------------

// editPayload round trips one fixture payload through a generic map so a test
// can rewrite a single field.
//
// The round trip is deliberate: the edit then travels through the same JSON the
// archive carries, including the fields the build never reads, so an edit
// cannot quietly drop part of the document.
func editPayload(t *testing.T, payload string, edit func(doc map[string]any)) string {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		t.Fatalf("decode fixture payload: %v", err)
	}
	edit(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode edited payload: %v", err)
	}
	return string(out)
}

// payloadInfo returns the info object of a decoded payload.
func payloadInfo(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()

	info, ok := doc["info"].(map[string]any)
	if !ok {
		t.Fatal("fixture payload has no info object")
	}
	return info
}

// payloadParticipants returns the ten participant objects of a decoded payload.
func payloadParticipants(t *testing.T, doc map[string]any) []any {
	t.Helper()

	participants, ok := payloadInfo(t, doc)["participants"].([]any)
	if !ok {
		t.Fatal("fixture payload has no participants array")
	}
	if len(participants) != 10 {
		t.Fatalf("fixture payload has %d participants, want 10", len(participants))
	}
	return participants
}

// payloadParticipant returns the participant in one seat.
//
// Seats are written team by team, five per team, in the fixture role order,
// which is what the raw payload looks like too: the array is the only ordering
// a participant list carries, and the team id is what says which side a
// participant is on.
func payloadParticipant(t *testing.T, doc map[string]any, team, seat int) map[string]any {
	t.Helper()

	if team != 100 && team != 200 {
		t.Fatalf("fixture team must be 100 or 200, got %d", team)
	}
	if seat < 0 || seat > 4 {
		t.Fatalf("fixture seat must be 0..4, got %d", seat)
	}
	index := seat
	if team == 200 {
		index += 5
	}
	participant, ok := payloadParticipants(t, doc)[index].(map[string]any)
	if !ok {
		t.Fatalf("fixture participant %d is not an object", index)
	}
	return participant
}

// payloadChampion reports the champion id of one seat, so a test can assert it
// edited the seat it meant to edit.
func payloadChampion(t *testing.T, doc map[string]any, team, seat int) int {
	t.Helper()

	champion, ok := payloadParticipant(t, doc, team, seat)["championId"].(float64)
	if !ok {
		t.Fatalf("fixture seat %d/%d has no champion id", team, seat)
	}
	return int(champion)
}

// archiveOf materialises one match as a one-partition archive, with the edit
// applied when one is given.
func archiveOf(t *testing.T, match fixtureMatch, edit func(doc map[string]any)) string {
	t.Helper()

	body := renderMatch(match)
	if edit != nil {
		body = editPayload(t, body, edit)
	}
	return fixtureRawRoot(t, map[string]string{match.partition: body + "\n"})
}

// archiveFor materialises a subset of the fixture archive.
func archiveFor(t *testing.T, indexes ...int) string {
	t.Helper()

	matches := fixtureMatches()
	subset := make([]fixtureMatch, 0, len(indexes))
	for _, index := range indexes {
		if index < 0 || index >= len(matches) {
			t.Fatalf("fixture match index %d is out of range", index)
		}
		subset = append(subset, matches[index])
	}
	return fixtureRawRoot(t, fixtureFiles(subset))
}

// ---------------------------------------------------------------------------
// Result inspection
// ---------------------------------------------------------------------------

// cellOf returns the published cell of one champion in one role.
func cellOf(t *testing.T, result BuildResult, championID int, role aggmodel.Role) aggmodel.Cell {
	t.Helper()

	for _, cell := range result.Cells.Cells {
		if cell.ChampionID == championID && cell.Role == role {
			return cell
		}
	}
	t.Fatalf("no published cell for champion %d in %s: %s", championID, role, formatCells(result.Cells.Cells))
	return aggmodel.Cell{}
}

// hasCell reports whether a cell was published at all.
func hasCell(result BuildResult, championID int, role aggmodel.Role) bool {
	for _, cell := range result.Cells.Cells {
		if cell.ChampionID == championID && cell.Role == role {
			return true
		}
	}
	return false
}

// championRolesIn lists the roles one champion was classified into, in the
// canonical role order the build publishes.
func championRolesIn(result BuildResult, championID int) []string {
	var roles []string
	for _, cell := range result.Cells.Cells {
		if cell.ChampionID == championID {
			roles = append(roles, string(cell.Role))
		}
	}
	return roles
}

// requireCounts checks the counters a table row cares about. A zero field is
// not checked, because most rows assert about two of the nine counters and a
// full nine-field expectation would be noise.
func requireCounts(t *testing.T, label string, got, want GateCounts) {
	t.Helper()

	checks := []struct {
		name     string
		got, wnt int
	}{
		{"archive_rows", got.ArchiveRows, want.ArchiveRows},
		{"malformed_rows", got.MalformedRows, want.MalformedRows},
		{"matches_used", got.MatchesUsed, want.MatchesUsed},
		{"participant_rows", got.ParticipantRows, want.ParticipantRows},
		{"rejected_rows", got.RejectedRows, want.RejectedRows},
		{"classified_rows", got.ClassifiedRows, want.ClassifiedRows},
		{"cells_total", got.CellsTotal, want.CellsTotal},
		{"cells_published", got.CellsPublished, want.CellsPublished},
		{"cells_suppressed", got.CellsSuppressed, want.CellsSuppressed},
		{"sum_n", got.SumN, want.SumN},
	}
	for _, check := range checks {
		if check.wnt == 0 {
			continue
		}
		if check.got != check.wnt {
			t.Errorf("%s: %s = %d, want %d", label, check.name, check.got, check.wnt)
		}
	}
}

// requireNoStagingResidue fails when a run left a staging or trash directory
// behind. The published tree is the only output a build may leave.
func requireNoStagingResidue(t *testing.T, label, root string) {
	t.Helper()

	for _, file := range relativeFiles(t, root) {
		if strings.Contains(file, stagingPrefix) || strings.Contains(file, ".trash-") {
			t.Errorf("%s: %s was left behind", label, file)
		}
	}
}

// ---------------------------------------------------------------------------
// The role rule
// ---------------------------------------------------------------------------

// TestFixtureRoleFallback walks the position rule across the spellings Riot
// uses in each of the two position fields.
//
// The rule has three parts and the table has a row for each: the assigned
// position wins when it is set, an empty assignment falls back to the detected
// position, and a position string the build does not know is rejected and
// counted rather than guessed. The last part is the one that matters: a guess
// would file a participant under a role nobody played, and the cell it lands in
// would look exactly like a measured one.
//
// Team 100's top laner of the first fixture match is the seat under test. The
// test asserts the champion id before it edits, so a change to the fixture
// composition fails here rather than silently editing a different seat.
func TestFixtureRoleFallback(t *testing.T) {
	t.Parallel()

	const (
		team   = 100
		seat   = 0
		heroID = 24
	)

	cases := []struct {
		name               string
		teamPosition       string
		individualPosition string
		wantRole           aggmodel.Role
		wantFailure        error
	}{
		{
			name:               "the assigned position wins",
			teamPosition:       "MIDDLE",
			individualPosition: "SUPPORT",
			wantRole:           aggmodel.RoleMid,
		},
		{
			name:               "the assigned position wins even when both are valid",
			teamPosition:       "TOP",
			individualPosition: "MID",
			wantRole:           aggmodel.RoleTop,
		},
		{
			name:               "an empty assignment falls back to the detected position",
			teamPosition:       "",
			individualPosition: "TOP",
			wantRole:           aggmodel.RoleTop,
		},
		{
			name:               "the detected position uses the ADC spelling",
			teamPosition:       "",
			individualPosition: "ADC",
			wantRole:           aggmodel.RoleBottom,
		},
		{
			name:               "an empty assignment with an empty detection is rejected",
			teamPosition:       "",
			individualPosition: "",
			wantFailure:        ErrRejectedRows,
		},
		{
			name:               "an unknown assignment is rejected rather than guessed",
			teamPosition:       "BOTLANE",
			individualPosition: "ADC",
			wantFailure:        ErrRejectedRows,
		},
		{
			name:               "two unknown positions are rejected",
			teamPosition:       "LANE",
			individualPosition: "LANE",
			wantFailure:        ErrRejectedRows,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rawRoot := archiveOf(t, fixtureMatches()[0], func(doc map[string]any) {
				if got := payloadChampion(t, doc, team, seat); got != heroID {
					t.Fatalf("fixture seat changed: team %d seat %d plays champion %d, want %d",
						team, seat, got, heroID)
				}
				participant := payloadParticipant(t, doc, team, seat)
				participant["teamPosition"] = tc.teamPosition
				participant["individualPosition"] = tc.individualPosition
			})

			aggRoot := t.TempDir()
			opts := fixtureBuildOptions(t, aggRoot, rawRoot)
			// One match cannot clear a floor of two, so the floor is lowered to
			// the smallest value that publishes anything at all: this test is
			// about the role the row was filed under, not about the floor.
			opts.MinCellN = 1
			opts.Gates = DefaultGateConfig(1)

			result, err := Build(context.Background(), opts)
			if tc.wantFailure != nil {
				if !errors.Is(err, tc.wantFailure) {
					t.Fatalf("build error = %v, want %v", err, tc.wantFailure)
				}
				if result.Counts.RejectedRows != 1 {
					t.Errorf("rejected_rows = %d, want 1", result.Counts.RejectedRows)
				}
				if files := relativeFiles(t, aggRoot); len(files) != 0 {
					t.Errorf("a rejected row published %v", files)
				}
				return
			}
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if result.Counts.RejectedRows != 0 {
				t.Errorf("rejected_rows = %d, want 0", result.Counts.RejectedRows)
			}
			if got := championRolesIn(result, heroID); !equalStrings(got, []string{string(tc.wantRole)}) {
				t.Errorf("champion %d was classified as %v, want only %s", heroID, got, tc.wantRole)
			}
			if cell := cellOf(t, result, heroID, tc.wantRole); cell.N != 1 {
				t.Errorf("n = %d, want 1", cell.N)
			}
		})
	}
}

// TestFixtureToleratedRejection pins what an operator-allowance buys and what it
// does not.
//
// The live archive contains rows Riot reports as position-less (teamPosition ""
// with individualPosition "Invalid", its literal sentinel, in remakes). The
// gate's escape hatch lets a measured number of those through instead of
// refusing to publish, but the row still must not be guessed into a role, and
// the reconciliation of published rows against classified rows must still hold
// with the tolerated row counted as unclassified. A fix that made the gate
// green by inventing a role would fail the cell assertions here.
func TestFixtureToleratedRejection(t *testing.T) {
	t.Parallel()

	const (
		team   = 100
		seat   = 0
		heroID = 24
	)

	rawRoot := archiveOf(t, fixtureMatches()[0], func(doc map[string]any) {
		participant := payloadParticipant(t, doc, team, seat)
		// Exactly the shape the live archive holds for a remake: the assigned
		// position is empty and the detected position is Riot's sentinel.
		participant["teamPosition"] = ""
		participant["individualPosition"] = "Invalid"
	})

	aggRoot := t.TempDir()
	opts := fixtureBuildOptions(t, aggRoot, rawRoot)
	opts.MinCellN = 1
	opts.Gates = DefaultGateConfig(1)
	opts.Gates.MaxRejectedRows = 1

	result, err := Build(context.Background(), opts)
	if err != nil {
		t.Fatalf("a tolerated rejection still failed the build: %v", err)
	}
	if result.Counts.RejectedRows != 1 {
		t.Fatalf("rejected_rows = %d, want 1", result.Counts.RejectedRows)
	}
	if result.Counts.ClassifiedRows != result.Counts.ParticipantRows-1 {
		t.Errorf("classified_rows = %d of %d participant rows, want exactly the rejected row removed",
			result.Counts.ClassifiedRows, result.Counts.ParticipantRows)
	}
	if result.Counts.SumN != result.Counts.ClassifiedRows {
		t.Errorf("cells hold %d rows, want %d: the tolerated row must not appear in a cell",
			result.Counts.SumN, result.Counts.ClassifiedRows)
	}
	if got := championRolesIn(result, heroID); len(got) != 0 {
		t.Errorf("champion %d was classified as %v from a position-less row, want no cell", heroID, got)
	}
	if result.Counts.CellsPublished == 0 {
		t.Error("no cell survived min_cell_n=1: a tolerated rejection must still publish the rest of the window")
	}
}

// TestFixtureToleratedRejectionFromTheRateCeiling runs the same fixture through
// the deployed rate instead of the absolute floor, which is the wiring the
// nightly build uses: the allowance has two operator inputs and the gate has to
// read both.
func TestFixtureToleratedRejectionFromTheRateCeiling(t *testing.T) {
	t.Parallel()

	rawRoot := archiveOf(t, fixtureMatches()[0], func(doc map[string]any) {
		participant := payloadParticipant(t, doc, 100, 0)
		participant["teamPosition"] = ""
		participant["individualPosition"] = "Invalid"
	})

	aggRoot := t.TempDir()
	opts := fixtureBuildOptions(t, aggRoot, rawRoot)
	opts.MinCellN = 1
	opts.Gates = DefaultGateConfig(1)
	// No floor at all: the fixture's window is small, so the allowance has to
	// come from the rate, which rounds up to one row.
	opts.Gates.MaxRejectedRows = 0
	opts.Gates.MaxRejectedRate = 0.0008

	result, err := Build(context.Background(), opts)
	if err != nil {
		t.Fatalf("the rate ceiling did not tolerate the remake row: %v", err)
	}
	if result.Counts.RejectedRows != 1 {
		t.Fatalf("rejected_rows = %d, want 1", result.Counts.RejectedRows)
	}
	if want := opts.Gates.AllowedRejectedRows(result.Counts.ParticipantRows); want != 1 {
		t.Fatalf("the fixture window should round the rate ceiling up to one row, got %d", want)
	}
	if result.Counts.CellsPublished == 0 {
		t.Error("no cell survived min_cell_n=1: a tolerated rejection must still publish the rest of the window")
	}
}

// TestGateConfidentShareIsExactAndExplainsRows pins the two halves of the
// confidence-majority failure: the share of cells is what the gate judges, and
// the message also has to say how much of the window the surviving cells carry,
// because that is the number an operator calibrates the share against.
func TestGateConfidentShareIsExactAndExplainsRows(t *testing.T) {
	t.Parallel()

	// 2 of 10 cells published: 20% of the cells, holding 80 of 100 rows.
	counts := GateCounts{
		ArchiveRows: 100, MatchesUsed: 10, ParticipantRows: 100, ClassifiedRows: 100,
		CellsTotal: 10, CellsPublished: 2, CellsSuppressed: 8, SumN: 100, SumNPublished: 80,
	}
	cases := []struct {
		share   float64
		wantErr bool
	}{
		{0.5, true},
		{0.21, true},
		{0.2, false},
		{0.15, false},
	}
	for _, tc := range cases {
		cfg := DefaultGateConfig(1)
		cfg.MinConfidentShare = tc.share
		err := counts.CheckOutput(cfg)
		if gotErr := errors.Is(err, ErrSuppressionMajority); gotErr != tc.wantErr {
			t.Errorf("share = %v: ErrSuppressionMajority = %v (%v), want %v", tc.share, gotErr, err, tc.wantErr)
		}
	}

	cfg := DefaultGateConfig(1)
	err := counts.CheckOutput(cfg)
	if err == nil {
		t.Fatal("expected the default 0.5 share to fail on a 20% window")
	}
	for _, want := range []string{"2 of 10 cells (20.0%)", "80 of 100 classified rows, 80.0%"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not report %q: %v", want, err)
		}
	}
}

// TestGateConfidentShareReportsNothingWithoutRows keeps the row sentence out of
// a window the input gate has already rejected for having no classified rows:
// there is no share to report there, and printing 0.0% of 0 would read like a
// second, independent defect.
func TestGateConfidentShareReportsNothingWithoutRows(t *testing.T) {
	t.Parallel()

	counts := GateCounts{ArchiveRows: 5, MatchesUsed: 0, ParticipantRows: 0, ClassifiedRows: 0,
		CellsTotal: 4, CellsPublished: 1, CellsSuppressed: 3, SumN: 0, SumNPublished: 0}
	cfg := DefaultGateConfig(1)
	err := counts.CheckOutput(cfg)
	if err == nil {
		t.Fatal("expected the 25% share to fail")
	}
	if strings.Contains(err.Error(), "classified rows") {
		t.Errorf("error invents a row share for an empty window: %v", err)
	}
}

// TestGateRejectedRowAllowanceIsExact pins the boundary of the allowance: the
// count is a ceiling, not a threshold.
func TestGateRejectedRowAllowanceIsExact(t *testing.T) {
	t.Parallel()

	counts := GateCounts{ArchiveRows: 10, MatchesUsed: 5, ParticipantRows: 10, RejectedRows: 2}
	cases := []struct {
		allowed int
		wantErr bool
	}{
		{0, true},
		{1, true},
		{2, false},
		{3, false},
	}

	for _, tc := range cases {
		cfg := DefaultGateConfig(1)
		cfg.MaxRejectedRows = tc.allowed
		err := counts.CheckInput(cfg)
		if gotErr := errors.Is(err, ErrRejectedRows); gotErr != tc.wantErr {
			t.Errorf("allowed = %d: ErrRejectedRows = %v (%v), want %v", tc.allowed, gotErr, err, tc.wantErr)
		}
	}
}

// TestGateRejectedRowAllowanceScalesWithTheWindow is the regression test for the
// second calibration event of this gate: the allowance used to be an absolute
// count, the archive grew 5x under it, and a window whose rejection *rate* had
// barely moved stopped publishing. It pins the three properties the shape has to
// keep - the floor decides a small window, the rate ceiling follows the archive,
// and a window that rejects a large fraction fails however deep the archive is.
func TestGateRejectedRowAllowanceScalesWithTheWindow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		floor           int
		rate            float64
		participantRows int
		rejectedRows    int
		wantAllowance   int
		wantErr         bool
	}{
		{
			name:  "the floor decides the window the measurement was taken on",
			floor: 25, rate: 0.0008, participantRows: 27790, rejectedRows: 3, wantAllowance: 25,
		},
		{
			name:  "the floor still decides a window the rate would allow less of",
			floor: 25, rate: 0.0008, participantRows: 10000, rejectedRows: 25, wantAllowance: 25,
		},
		{
			name:  "the floor is a ceiling too",
			floor: 25, rate: 0.0008, participantRows: 10000, rejectedRows: 26, wantAllowance: 25, wantErr: true,
		},
		{
			// build 17: 30 of 141,150 rejected (0.021%) against a deployed 25.
			name:  "the deployed pair publishes the window that broke",
			floor: 25, rate: 0.0008, participantRows: 141150, rejectedRows: 30, wantAllowance: 113,
		},
		{
			name:  "the rate ceiling is a ceiling, not a threshold",
			floor: 25, rate: 0.0008, participantRows: 141150, rejectedRows: 113, wantAllowance: 113,
		},
		{
			name:  "one row over the rate ceiling still stops the build",
			floor: 25, rate: 0.0008, participantRows: 141150, rejectedRows: 114, wantAllowance: 113, wantErr: true,
		},
		{
			// The same rate, an archive 5x deeper: the allowance follows it
			// instead of being outgrown by it.
			name:  "the allowance follows the archive",
			floor: 25, rate: 0.0008, participantRows: 705750, rejectedRows: 150, wantAllowance: 565,
		},
		{
			// A classification regression does not reject 0.08% of a window;
			// it rejects the role, the champion or the payload shape it broke,
			// which is a large fraction however large the window is.
			name:  "a regression still stops a deep archive",
			floor: 25, rate: 0.0008, participantRows: 705750, rejectedRows: 7058, wantAllowance: 565, wantErr: true,
		},
		{
			name:  "no floor and no rate stays fail-closed",
			floor: 0, rate: 0, participantRows: 141150, rejectedRows: 1, wantAllowance: 0, wantErr: true,
		},
		{
			name:  "a rate of zero leaves the floor to decide",
			floor: 25, rate: 0, participantRows: 141150, rejectedRows: 25, wantAllowance: 25,
		},
	}

	for _, tc := range cases {
		cfg := DefaultGateConfig(1)
		cfg.MaxRejectedRows = tc.floor
		cfg.MaxRejectedRate = tc.rate

		if got := cfg.AllowedRejectedRows(tc.participantRows); got != tc.wantAllowance {
			t.Errorf("%s: AllowedRejectedRows(%d) = %d, want %d", tc.name, tc.participantRows, got, tc.wantAllowance)
		}

		counts := GateCounts{
			ArchiveRows: tc.participantRows, MatchesUsed: 1,
			ParticipantRows: tc.participantRows, RejectedRows: tc.rejectedRows,
			ClassifiedRows: tc.participantRows - tc.rejectedRows,
		}
		err := counts.CheckInput(cfg)
		if gotErr := errors.Is(err, ErrRejectedRows); gotErr != tc.wantErr {
			t.Errorf("%s: ErrRejectedRows = %v (%v), want %v", tc.name, gotErr, err, tc.wantErr)
		}
	}
}

// TestGateRejectedRowAllowanceIsReportable keeps the failure message usable as
// calibration evidence: an operator reading it has to be able to see which half
// of the allowance was in force without going back to the ConfigMap.
func TestGateRejectedRowAllowanceIsReportable(t *testing.T) {
	t.Parallel()

	counts := GateCounts{ArchiveRows: 141150, MatchesUsed: 14115, ParticipantRows: 141150,
		RejectedRows: 30, ClassifiedRows: 141120}
	cfg := DefaultGateConfig(1)
	cfg.MaxRejectedRows = 25

	err := counts.CheckInput(cfg)
	if err == nil {
		t.Fatal("expected 30 rejected rows against a floor of 25 to fail")
	}
	for _, want := range []string{"30 of 141150 participant rows", "allowed 25", "floor 25", "rate 0.0000% of the window"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not report %q: %v", want, err)
		}
	}

	// The same counts against the deployed pair pass, which is what makes the
	// failure above a statement about the allowance rather than about the
	// window.
	cfg.MaxRejectedRate = 0.0008
	if err := counts.CheckInput(cfg); err != nil {
		t.Errorf("the deployed allowance should publish this window: %v", err)
	}
}

// TestFixtureRoleFallbackOnTheDetectedField pins the fallback against the real
// archive rather than against an edited payload.
//
// The fixture's second team carries no assigned position at all, so the whole
// team is classified from the detected position. If the fallback broke, the
// build would either reject half of every match or file it under nothing, and
// both show up here as a missing cell.
func TestFixtureRoleFallbackOnTheDetectedField(t *testing.T) {
	t.Parallel()

	aggRoot := t.TempDir()
	result, err := Build(context.Background(), fixtureBuildOptions(t, aggRoot, fixtureRawRoot(t, fixtureFiles(fixtureMatches()))))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if result.Counts.RejectedRows != 0 {
		t.Fatalf("rejected_rows = %d, want 0: the detected position was not used", result.Counts.RejectedRows)
	}

	// Both teams contribute five cells per match, so every role holds both
	// compositions of the fixture.
	for _, want := range []struct {
		championID int
		role       aggmodel.Role
	}{
		{24, aggmodel.RoleTop},      // team 100, assigned position
		{86, aggmodel.RoleTop},      // team 200, detected position
		{64, aggmodel.RoleJungle},   // team 100, assigned position
		{121, aggmodel.RoleJungle},  // team 200, detected position
		{111, aggmodel.RoleSupport}, // team 100, assigned UTILITY
		{412, aggmodel.RoleSupport}, // team 200, detected SUPPORT
	} {
		if !hasCell(result, want.championID, want.role) {
			t.Errorf("no cell for champion %d in %s: %s", want.championID, want.role, formatCells(result.Cells.Cells))
		}
	}
}

// ---------------------------------------------------------------------------
// Patch and window boundaries
// ---------------------------------------------------------------------------

// cellSpec is one expected cell: the sample size and the wins behind it.
type cellSpec struct {
	championID int
	role       aggmodel.Role
	n          int
	wins       int
}

// requireCellSpec checks one published cell, naming both figures so that a
// failure says whether the cell moved or the arithmetic did.
func requireCellSpec(t *testing.T, label string, result BuildResult, spec cellSpec) {
	t.Helper()

	cell := cellOf(t, result, spec.championID, spec.role)
	if cell.N != spec.n || cell.Wins != spec.wins {
		t.Errorf("%s: champion %d in %s: n/wins = %d/%d, want %d/%d",
			label, spec.championID, spec.role, cell.N, cell.Wins, spec.n, spec.wins)
	}
}

// requireAbsentCell checks that a (champion, role) cell was not published.
//
// Absence is the assertion that catches the interesting scoping bug: a filter
// that is too wide does not corrupt a number, it invents a champion.
func requireAbsentCell(t *testing.T, label string, result BuildResult, spec cellSpec) {
	t.Helper()

	if hasCell(result, spec.championID, spec.role) {
		t.Errorf("%s: champion %d in %s was published: %s",
			label, spec.championID, spec.role, formatCells(result.Cells.Cells))
	}
}

// TestFixturePatchAndWindowBoundaries walks the source window, the patch scope
// and the region and queue filters across the fixture archive.
//
// The fixture is built so that each axis has exactly one match that only a
// correct filter can place: the last minute of the window end, the first minute
// after it, a match played before the window opened but crawled inside it, a
// match on the previous patch, a match on the other platform and a match in the
// other queue. Each of those matches also carries a champion that plays nowhere
// else, so a filter that is one match too wide shows up as a champion in the
// tier list rather than only as a wrong counter.
//
// Two of the rows assert something stronger than a counter: that dropping the
// rows the filters are supposed to drop produces a byte-identical tree. A
// boundary that is off by one partition would still produce the right counts in
// a hand-written table if the table were written from the same misreading; it
// cannot produce the same bytes.
func TestFixturePatchAndWindowBoundaries(t *testing.T) {
	t.Parallel()

	everyMatch := func(t *testing.T) string {
		t.Helper()
		return fixtureRawRoot(t, fixtureFiles(fixtureMatches()))
	}
	indexes := func(want ...int) func(*testing.T) string {
		return func(t *testing.T) string {
			t.Helper()
			return archiveFor(t, want...)
		}
	}

	// The baseline is the fixture window against the whole archive. Every other
	// row is either a delta from it or a filter that must not change it.
	baseline := t.TempDir()
	baselineResult, err := Build(context.Background(), fixtureBuildOptions(t, baseline, everyMatch(t)))
	if err != nil {
		t.Fatalf("baseline build: %v", err)
	}
	baselineTree := hashTree(t, baseline)

	// withArchiveRows is the baseline counts for an archive that is missing
	// rows the filters were already dropping: every published figure has to be
	// identical, and only the archive row counter may move.
	withArchiveRows := func(archiveRows int) GateCounts {
		counts := fixtureExpectedCounts
		counts.ArchiveRows = archiveRows
		return counts
	}

	// A champion that plays only in one place, for the absence checks.
	topCell := func(championID int) cellSpec {
		return cellSpec{championID: championID, role: aggmodel.RoleTop}
	}
	absentOutsideTheWindow := []cellSpec{topCell(96), topCell(97), topCell(98), topCell(99)}
	absentOnThePreviousPatch := []cellSpec{topCell(24), topCell(13), topCell(11), topCell(12)}

	cases := []struct {
		name             string
		rawRoot          func(*testing.T) string
		patch            string
		windowEnd        string
		minCellN         int
		wantPatch        string
		wantWindow       aggmodel.Window
		wantCounts       GateCounts
		wantCells        []cellSpec
		absent           []cellSpec
		wantBaselineTree bool
		wantFailure      error
	}{
		{
			name:       "the fixture window against the whole archive",
			rawRoot:    everyMatch,
			wantPatch:  "16.18",
			wantWindow: fixtureExpectedWindow,
			wantCounts: fixtureExpectedCounts,
			wantCells: []cellSpec{
				{24, aggmodel.RoleTop, 6, 4},
				{86, aggmodel.RoleTop, 8, 3},
				{13, aggmodel.RoleTop, 2, 0},
				{64, aggmodel.RoleJungle, 9, 5},
				{121, aggmodel.RoleJungle, 9, 4},
			},
			absent:           absentOutsideTheWindow,
			wantBaselineTree: true,
		},
		{
			name:      "a window end one day later admits the last minute of the archive",
			rawRoot:   everyMatch,
			windowEnd: "2026-09-15",
			wantCounts: GateCounts{
				MatchesUsed: 10, CellsTotal: 14, CellsPublished: 11, CellsSuppressed: 3,
			},
			// Champion 98 plays only in that match, which is the last minute
			// of the new window end. Its cell is computable (one game, one win)
			// but it is below the floor, so the cell counters move and the tier
			// list still does not name it.
			absent: []cellSpec{topCell(96), topCell(97), topCell(98), topCell(99)},
		},
		{
			name:      "a window end one day earlier drops the last day of matches",
			rawRoot:   everyMatch,
			windowEnd: "2026-09-13",
			wantCounts: GateCounts{
				MatchesUsed: 8, CellsTotal: 13, CellsPublished: 11, CellsSuppressed: 2,
			},
			// Champion 24 loses its ninth and tenth game, champion 86 gains
			// nothing, and both keep the wins they had.
			wantCells: []cellSpec{
				{24, aggmodel.RoleTop, 5, 4},
				{86, aggmodel.RoleTop, 7, 2},
				{13, aggmodel.RoleTop, 2, 0},
				{64, aggmodel.RoleJungle, 8, 5},
			},
			absent: absentOutsideTheWindow,
		},
		{
			name:       "the previous patch is counted in the window and published nowhere",
			rawRoot:    indexes(0, 1, 2, 3, 4, 5, 6, 7, 9, 10, 11, 12, 13),
			wantCounts: withArchiveRows(len(fixtureMatches()) - 1),
			// The match on patch 16.17 (index 8) is inside the window and inside
			// every other filter, so only the patch scope can keep it out: it is
			// one of the five rows the archive counts and no query counts, and
			// removing it changes nothing else.
			absent:           []cellSpec{topCell(99)},
			wantBaselineTree: true,
		},
		{
			name:       "matches outside the window, the region and the queue change nothing",
			rawRoot:    indexes(0, 1, 2, 3, 4, 5, 6, 7, 8, 12),
			wantCounts: withArchiveRows(10),
			// F10 was played before the window opened, F14 after it closed,
			// F11 on the other platform and F12 in the other queue. None of
			// them may move a number, including the ban rates they contribute
			// to, so the tree must be the same bytes.
			wantBaselineTree: true,
		},
		{
			name:      "pinning the previous patch publishes only that patch",
			rawRoot:   everyMatch,
			patch:     "16.17",
			minCellN:  1,
			wantPatch: "16.17",
			wantCounts: GateCounts{
				MatchesUsed: 1, CellsTotal: 10, CellsPublished: 10, CellsSuppressed: 0,
			},
			wantCells: []cellSpec{{99, aggmodel.RoleTop, 1, 1}, {86, aggmodel.RoleTop, 1, 0}},
			absent:    absentOnThePreviousPatch,
		},
		{
			name:        "a patch that is not in the window fails closed",
			rawRoot:     everyMatch,
			patch:       "16.19",
			wantFailure: ErrEmptyWindow,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Not parallel: the rows share the aggregate root of the baseline
			// to prove it is left untouched, and two builds staging inside one
			// root at the same time would see each other's scratch tree.
			aggRoot := t.TempDir()
			opts := fixtureBuildOptions(t, aggRoot, tc.rawRoot(t))
			if tc.patch != "" {
				opts.Patch = tc.patch
			}
			if tc.windowEnd != "" {
				opts.WindowEnd = tc.windowEnd
			}
			if tc.minCellN > 0 {
				opts.MinCellN = tc.minCellN
				opts.Gates = DefaultGateConfig(tc.minCellN)
			}

			result, err := Build(context.Background(), opts)
			if tc.wantFailure != nil {
				if !errors.Is(err, tc.wantFailure) {
					t.Fatalf("build error = %v, want %v", err, tc.wantFailure)
				}
				if files := relativeFiles(t, aggRoot); len(files) != 0 {
					t.Errorf("a refused window published %v", files)
				}
				return
			}
			if err != nil {
				t.Fatalf("build: %v", err)
			}

			if tc.wantPatch != "" && result.Seg.Patch != tc.wantPatch {
				t.Errorf("patch = %q, want %q", result.Seg.Patch, tc.wantPatch)
			}
			if tc.wantWindow != (aggmodel.Window{}) {
				if result.Partition.SourceWindow != tc.wantWindow {
					t.Errorf("source window = %+v, want %+v", result.Partition.SourceWindow, tc.wantWindow)
				}
			}
			requireCounts(t, tc.name, result.Counts, tc.wantCounts)
			for _, spec := range tc.wantCells {
				requireCellSpec(t, tc.name, result, spec)
			}
			for _, spec := range tc.absent {
				requireAbsentCell(t, tc.name, result, spec)
			}
			if tc.wantBaselineTree {
				requireSameTree(t, tc.name, baselineTree, hashTree(t, aggRoot))
			}
			requireNoStagingResidue(t, tc.name, aggRoot)
		})
	}

	// The baseline is not a build the subtests may have disturbed.
	requireSameTree(t, "baseline", baselineTree, hashTree(t, baseline))
	if baselineResult.Seg.Patch != "16.18" {
		t.Errorf("baseline patch = %q, want 16.18", baselineResult.Seg.Patch)
	}
}

// ---------------------------------------------------------------------------
// Failing closed
// ---------------------------------------------------------------------------

// archiveInPartition materialises matches into a partition of the test's
// choosing.
//
// The crawl partition and the game creation date are independent, so a test
// that wants a part outside the window cannot get one from the fixture match
// set: it has to name the partition itself.
func archiveInPartition(t *testing.T, partition string, matches ...fixtureMatch) string {
	t.Helper()

	var body strings.Builder
	for _, match := range matches {
		body.WriteString(renderMatch(match))
		body.WriteString("\n")
	}
	return fixtureRawRoot(t, map[string]string{partition: body.String()})
}

// truncatedMatchArchive writes one match record cut in half, which is what a
// part killed mid-flush looks like.
func truncatedMatchArchive(t *testing.T) string {
	t.Helper()

	match := fixtureMatches()[0]
	body := renderMatch(match)
	return fixtureRawRoot(t, map[string]string{match.partition: body[:len(body)/2] + "\n"})
}

// emptyParticipantsArchive keeps the envelope and drops every participant.
func emptyParticipantsArchive(t *testing.T) string {
	t.Helper()

	match := fixtureMatches()[0]
	body := editPayload(t, renderMatch(match), func(doc map[string]any) {
		payloadInfo(t, doc)["participants"] = []any{}
	})
	return fixtureRawRoot(t, map[string]string{match.partition: body + "\n"})
}

// notAPatchArchive replaces the game version with something the patch
// normaliser cannot read.
func notAPatchArchive(t *testing.T) string {
	t.Helper()

	match := fixtureMatches()[0]
	body := editPayload(t, renderMatch(match), func(doc map[string]any) {
		payloadInfo(t, doc)["gameVersion"] = "not-a-patch"
	})
	return fixtureRawRoot(t, map[string]string{match.partition: body + "\n"})
}

// TestBuildFailsClosedOnBadInput feeds the build input it must refuse, and
// checks two things each time: that the run failed, and that the artifacts of
// the previous good build are byte for byte where they were.
//
// The second half is the reason this test exists. A build that stops is only
// useful if it stops before it publishes, and the only way to know that is to
// compare the tree with itself. Every row therefore builds the good fixture
// first, into the same aggregate root the failing run is pointed at.
func TestBuildFailsClosedOnBadInput(t *testing.T) {
	t.Parallel()

	aggRoot := t.TempDir()
	good, err := Build(context.Background(), fixtureBuildOptions(t, aggRoot, fixtureRawRoot(t, fixtureFiles(fixtureMatches()))))
	if err != nil {
		t.Fatalf("good build: %v", err)
	}
	before := hashTree(t, aggRoot)

	cases := []struct {
		name         string
		rawRoot      func(*testing.T) string
		patch        string
		windowEnd    string
		wantFailure  error
		wantContains string
		wantStatus   string
	}{
		{
			// The payload is valid JSON, so no reader can reject it: the row is
			// readable and carries no envelope. Only the gate can catch this,
			// and only the audit row can explain it.
			name: "a payload with no envelope at all",
			rawRoot: func(t *testing.T) string {
				t.Helper()
				return fixtureRawRoot(t, fixtureFiles(fixtureCorruptMatches()))
			},
			wantFailure: ErrMalformedArchive,
			wantStatus:  auditStatusQuarantined,
		},
		{
			// A truncated payload is not valid JSON, so it has no readable
			// envelope either: the reader nulls it out rather than letting
			// DuckDB abort the statement, and the gate counts it. This is the
			// row that would otherwise reach the operator as a parser error
			// with no archive statistics and no audit reason.
			name:         "a payload truncated mid record",
			rawRoot:      truncatedMatchArchive,
			wantFailure:  ErrMalformedArchive,
			wantContains: "malformed",
			wantStatus:   auditStatusQuarantined,
		},
		{
			// A part crawled into a partition the window cannot reach: the
			// window end is pinned, so the partition cannot widen it.
			name: "a partition older than the window",
			rawRoot: func(t *testing.T) string {
				t.Helper()
				return archiveInPartition(t, "2026-08-30", fixtureMatches()[0])
			},
			wantFailure: ErrArchiveEmpty,
			wantStatus:  "",
		},
		{
			name:        "a window with no matches in it",
			rawRoot:     func(t *testing.T) string { t.Helper(); return archiveFor(t, 13) },
			wantFailure: ErrEmptyWindow,
			wantStatus:  auditStatusQuarantined,
		},
		{
			// Every match in the window is readable and correctly attributed,
			// and none of them carries a participant: the patch is chosen from
			// the extracted rows, so there is no patch to publish and the build
			// stops before it invents an empty partition.
			name:         "a match with no participants",
			rawRoot:      emptyParticipantsArchive,
			wantFailure:  ErrEmptyWindow,
			wantContains: "no match in the window carries a patch",
			wantStatus:   auditStatusQuarantined,
		},
		{
			// A game version that is not major.minor cannot be attributed to a
			// patch, so the rows behind it would be published under a boundary
			// nobody understands. The build refuses the whole window instead of
			// publishing the rows it can attribute.
			name:         "a game version that is not a patch",
			rawRoot:      notAPatchArchive,
			wantFailure:  ErrMalformedArchive,
			wantContains: "not major.minor",
			wantStatus:   auditStatusQuarantined,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Not parallel: the rows share the aggregate root under proof.
			auditRoot := t.TempDir()
			opts := fixtureBuildOptions(t, aggRoot, tc.rawRoot(t))
			opts.Auditor = FileAuditor{Root: auditRoot}
			if tc.patch != "" {
				opts.Patch = tc.patch
			}
			if tc.windowEnd != "" {
				opts.WindowEnd = tc.windowEnd
			}

			result, err := Build(context.Background(), opts)
			if err == nil {
				t.Fatalf("the build accepted input it must refuse: %+v", result.Counts)
			}
			if tc.wantFailure != nil && !errors.Is(err, tc.wantFailure) {
				t.Errorf("build error = %v, want %v", err, tc.wantFailure)
			}
			if tc.wantContains != "" && !strings.Contains(err.Error(), tc.wantContains) {
				t.Errorf("build error %q does not mention %q", err, tc.wantContains)
			}

			// The whole point: the previous artifacts are still live.
			requireSameTree(t, tc.name, before, hashTree(t, aggRoot))
			requireNoStagingResidue(t, tc.name, aggRoot)

			// The live manifest still describes the good build.
			manifest, manifestErr := ReadManifest(aggRoot)
			if manifestErr != nil {
				t.Fatalf("read manifest: %v", manifestErr)
			}
			if len(manifest.Partitions) != 1 || !reflect.DeepEqual(manifest.Latest, good.Partition) {
				t.Errorf("the manifest now describes %+v, want %+v", manifest.Latest, good.Partition)
			}

			if tc.wantStatus == "" {
				if result.BuildRunID != 0 {
					t.Errorf("a refused archive opened build run %d", result.BuildRunID)
				}
				return
			}
			if result.BuildRunID == 0 {
				t.Fatal("a refused run recorded no build run")
			}
			record := readAuditRecord(t, auditRoot, result.BuildRunID)
			if got := record["status"]; got != tc.wantStatus {
				t.Errorf("audit status = %v, want %q", got, tc.wantStatus)
			}
			if got := record["cells_published"]; got != float64(0) {
				t.Errorf("audit cells_published = %v, want 0", got)
			}
			if message, _ := record["error"].(string); message == "" {
				t.Error("the audit row does not say why the run failed")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Injecting a defect the archive cannot carry
// ---------------------------------------------------------------------------

// jsonLiteralPath matches a JSON file path in a SQL statement.
//
// The statements carry other single-quoted literals - JSON paths, position
// spellings, patch strings - so the pattern is narrowed to a path that ends in
// .json, and the caller narrows it again by file name.
var jsonLiteralPath = regexp.MustCompile(`'([^']+\.json)'`)

// doctoringEngine runs the pinned client and then rewrites one document the
// client has just written.
//
// The injection point is deliberate. The reconciliation gate compares the sum
// of the cell sample sizes with the number of participant rows that produced
// them, and those two figures are reductions of the same rows, so a correct
// archive cannot produce a disagreement. A disagreement has to be injected
// between the statement that writes a reduction and the Go code that reads it
// back, which is exactly what this engine does - and it is also what a real
// defect in a reduction looks like from the gate's side.
type doctoringEngine struct {
	t     *testing.T
	inner Engine
	base  string
	edit  func(value any)
	fired bool
}

var _ Engine = (*doctoringEngine)(nil)

// newDoctoringEngine opens the pinned client and wraps it.
func newDoctoringEngine(t *testing.T, bin, base string, edit func(value any)) *doctoringEngine {
	t.Helper()

	inner, err := OpenCLIEngine(context.Background(), bin, false, DuckDBSettings{}, nil)
	if err != nil {
		t.Fatalf("open the pinned client: %v", err)
	}
	t.Cleanup(func() { _ = inner.Close() })
	return &doctoringEngine{t: t, inner: inner, base: base, edit: edit}
}

// Exec runs the statement and then doctors the document it wrote.
func (d *doctoringEngine) Exec(ctx context.Context, sql string) error {
	if err := d.inner.Exec(ctx, sql); err != nil {
		return err
	}
	path := d.written(sql)
	if path == "" || d.fired {
		// Once is enough: a second edit would apply the defect twice and the
		// test would be proving something about the doctoring rather than
		// about the gate.
		return nil
	}
	d.fired = true
	d.rewrite(path)
	return nil
}

func (d *doctoringEngine) Version(ctx context.Context) (string, error) { return d.inner.Version(ctx) }

// Close is a no-op: the test owns the client it opened.
func (d *doctoringEngine) Close() error { return nil }

// written returns the path of the document under test, if this statement wrote
// it.
func (d *doctoringEngine) written(sql string) string {
	for _, match := range jsonLiteralPath.FindAllStringSubmatch(sql, -1) {
		if filepath.Base(match[1]) == d.base {
			return match[1]
		}
	}
	return ""
}

// rewrite decodes the document, edits it and writes it back.
func (d *doctoringEngine) rewrite(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		d.t.Fatalf("read doc %s: %v", path, err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		d.t.Fatalf("decode doc %s: %v", path, err)
	}
	d.edit(value)
	out, err := json.Marshal(value)
	if err != nil {
		d.t.Fatalf("encode doc %s: %v", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		d.t.Fatalf("write doc %s: %v", path, err)
	}
}

// requireFired fails when the engine never found the document it was asked to
// doctor, which would leave the test asserting about a defect that never
// existed.
func (d *doctoringEngine) requireFired(t *testing.T) {
	t.Helper()

	if !d.fired {
		t.Fatalf("the injected defect never reached %s", d.base)
	}
}

// TestBuildReconciliationGateIsExactAndBounded injects a window that cannot
// have come from the rows it was reduced from, and walks the gate from both
// sides.
//
// The two figures are reductions of the same participant rows, so the expected
// difference is exactly zero: one row more than the cells account for is a
// failure at the default setting. The tolerance exists so that an operator who
// knows of an archive defect can narrow the gate instead of disabling it, and
// the last row proves the tolerance is a bound rather than a switch: a
// tolerance of two still refuses a defect of three.
func TestBuildReconciliationGateIsExactAndBounded(t *testing.T) {
	t.Parallel()

	bin := duckDBBin(t)
	rawRoot := fixtureRawRoot(t, fixtureFiles(fixtureMatches()))
	ctx := context.Background()

	// Every row gets its own aggregate root holding a good build, so a row that
	// publishes cannot invalidate the preservation check of the row after it.
	goodRoot := func(t *testing.T) string {
		t.Helper()

		root := t.TempDir()
		if _, err := Build(ctx, fixtureBuildOptions(t, root, rawRoot)); err != nil {
			t.Fatalf("good build: %v", err)
		}
		return root
	}

	cases := []struct {
		name           string
		injectedRows   int
		tolerance      int
		wantFailure    error
		wantClassified int
	}{
		{
			name:         "one row the cells do not account for is refused",
			injectedRows: 1,
			wantFailure:  ErrReconciliation,
		},
		{
			name:         "three rows are refused by a tolerance of two",
			injectedRows: 3,
			tolerance:    2,
			wantFailure:  ErrReconciliation,
		},
		{
			// Accepted, and accepted loudly: the published partition still
			// carries the doctored figure, because the build reports what the
			// archive told it rather than what it would have preferred. The
			// tolerance widens the gate, it does not rewrite the input.
			name:           "the same defect within the tolerance publishes",
			injectedRows:   1,
			tolerance:      1,
			wantClassified: 91,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Not parallel: each row runs two builds and asserts about the tree
			// the first one published.
			aggRoot := goodRoot(t)
			before := hashTree(t, aggRoot)

			auditRoot := t.TempDir()
			opts := fixtureBuildOptions(t, aggRoot, rawRoot)
			opts.Gates = DefaultGateConfig(fixtureMinCellN)
			opts.Gates.ReconcileTolerance = tc.tolerance
			opts.Auditor = FileAuditor{Root: auditRoot}
			engine := newDoctoringEngine(t, bin, "window_stats.json", func(value any) {
				rows, ok := value.([]any)
				if !ok || len(rows) == 0 {
					t.Fatalf("window statistics are not a non-empty row list: %T", value)
				}
				row, ok := rows[0].(map[string]any)
				if !ok {
					t.Fatalf("window statistics row is not an object: %T", rows[0])
				}
				participants, ok := row["participant_rows"].(float64)
				if !ok {
					t.Fatalf("window statistics carry no participant_rows: %v", row)
				}
				row["participant_rows"] = participants + float64(tc.injectedRows)
			})
			opts.Engine = engine

			result, err := Build(ctx, opts)
			engine.requireFired(t)

			if tc.wantFailure != nil {
				if !errors.Is(err, tc.wantFailure) {
					t.Fatalf("build error = %v, want %v", err, tc.wantFailure)
				}
				// The two figures the gate compares are still reported, which is
				// what lets an operator see the size of the disagreement.
				if result.Counts.SumN != fixtureExpectedCounts.SumN {
					t.Errorf("sum_n = %d, want %d", result.Counts.SumN, fixtureExpectedCounts.SumN)
				}
				if got := result.Counts.ClassifiedRows; got != fixtureExpectedCounts.ClassifiedRows+tc.injectedRows {
					t.Errorf("classified_rows = %d, want %d", got, fixtureExpectedCounts.ClassifiedRows+tc.injectedRows)
				}
				requireSameTree(t, tc.name, before, hashTree(t, aggRoot))
				requireNoStagingResidue(t, tc.name, aggRoot)

				record := readAuditRecord(t, auditRoot, result.BuildRunID)
				if got := record["status"]; got != auditStatusQuarantined {
					t.Errorf("audit status = %v, want %q", got, auditStatusQuarantined)
				}
				return
			}

			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if got := result.Counts.ClassifiedRows; got != tc.wantClassified {
				t.Errorf("classified_rows = %d, want %d", got, tc.wantClassified)
			}
			if got := result.Counts.SumN; got != fixtureExpectedCounts.SumN {
				t.Errorf("sum_n = %d, want %d", got, fixtureExpectedCounts.SumN)
			}
			// The defect is in the reconciliation, not in the cells: the
			// published counters must be exactly what the good build produced,
			// except for the one figure the injected defect moved, which is
			// asserted above.
			wantCounts := fixtureExpectedCounts
			wantCounts.ClassifiedRows = 0
			wantCounts.ParticipantRows = fixtureExpectedCounts.ParticipantRows + tc.injectedRows
			requireCounts(t, tc.name, result.Counts, wantCounts)
			checkAuditRow(t, result, auditRoot)
		})
	}
}

// ---------------------------------------------------------------------------
// Simulated data
// ---------------------------------------------------------------------------

// TestDemoIsDeterministicAndLabelled checks the three properties that make the
// demo safe to ship: it is reproducible, it says what it is, and it validates.
//
// The demo exists because no Riot API key is available in this environment, so
// the one thing it must never do is look like measured data. Determinism is
// part of that: a demo whose numbers changed between runs could not be used to
// review a presentation change, and its generated_at is derived from the
// simulated window rather than read from the clock.
func TestDemoIsDeterministicAndLabelled(t *testing.T) {
	t.Parallel()

	first, second := t.TempDir(), t.TempDir()
	one, err := Demo(DemoOptions{OutDir: first, MinCellN: 2})
	if err != nil {
		t.Fatalf("first demo run: %v", err)
	}
	two, err := Demo(DemoOptions{OutDir: second, MinCellN: 2})
	if err != nil {
		t.Fatalf("second demo run: %v", err)
	}

	// Reproducible: the same seed publishes the same bytes, generated_at
	// included.
	requireSameTree(t, "demo rerun", hashTree(t, first), hashTree(t, second))
	if one.Seed != DemoSeed {
		t.Errorf("demo seed = %d, want the fixed seed %d", one.Seed, DemoSeed)
	}
	if !one.GeneratedAt.Equal(two.GeneratedAt) || one.GeneratedAt.IsZero() {
		t.Errorf("generated_at = %s and %s, want one non-zero derived instant",
			one.GeneratedAt, two.GeneratedAt)
	}

	// Labelled: the manifest and every document it points at carry the demo
	// source.
	if one.Manifest.Source != aggmodel.SourceDemo {
		t.Errorf("manifest source = %q, want %q", one.Manifest.Source, aggmodel.SourceDemo)
	}
	seg := one.Seg
	if len(one.Manifest.Latest.Champions) == 0 || len(one.Manifest.Latest.MatchupRoles) == 0 {
		t.Fatalf("the demo manifest lists no champions or no matchup roles: %+v", one.Manifest.Latest)
	}
	documents := []struct {
		path   string
		target any
		source func(any) aggmodel.Source
	}{
		{
			path:   aggmodel.ManifestPath,
			target: &aggmodel.Manifest{},
			source: func(value any) aggmodel.Source { return value.(*aggmodel.Manifest).Source },
		},
		{
			path:   seg.TierListPath(),
			target: &aggmodel.TierList{},
			source: func(value any) aggmodel.Source { return value.(*aggmodel.TierList).Source },
		},
		{
			path:   seg.ChampionPath(one.Manifest.Latest.Champions[0]),
			target: &aggmodel.Champion{},
			source: func(value any) aggmodel.Source { return value.(*aggmodel.Champion).Source },
		},
		{
			path:   seg.MatchupsPath(one.Manifest.Latest.MatchupRoles[0]),
			target: &aggmodel.Matchups{},
			source: func(value any) aggmodel.Source { return value.(*aggmodel.Matchups).Source },
		},
	}
	for _, document := range documents {
		path := filepath.Join(first, filepath.FromSlash(document.path))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("frozen path %s is missing: %v", document.path, err)
		}
		readDocument(t, path, document.target)
		if got := document.source(document.target); got != aggmodel.SourceDemo {
			t.Errorf("%s declares source %q, want %q", document.path, got, aggmodel.SourceDemo)
		}
	}

	// The notice sits next to the tree and explains it without being part of
	// the served paths.
	notice, err := os.ReadFile(filepath.Join(first, DemoNoticeFile))
	if err != nil {
		t.Fatalf("read the demo notice: %v", err)
	}
	for _, want := range []string{"SIMULATED DATA", string(aggmodel.SourceDemo), string(aggmodel.SourceRiotMatchV5)} {
		if !strings.Contains(string(notice), want) {
			t.Errorf("the demo notice does not mention %q", want)
		}
	}

	// Valid: the tree passes verification against the schema cmd/gen-types
	// writes, which is the same document the published declarations come from.
	schema := filepath.Join("..", "..", "schema", "agg.schema.json")
	if _, err := os.Stat(schema); err != nil {
		t.Fatalf("the schema cmd/gen-types writes is missing: %v", err)
	}
	verified, err := Verify(VerifyOptions{AggRoot: first, SchemaPath: schema, Source: aggmodel.SourceDemo})
	if err != nil {
		t.Fatalf("verify the demo tree: %v", err)
	}
	if len(verified.Problems) != 0 {
		t.Errorf("verification found %d problems: %v", len(verified.Problems), verified.Problems)
	}
	if verified.Documents == 0 {
		t.Error("verification checked no documents")
	}

	// And a verification that demands real data must reject it, because that is
	// what an operator runs against the real site.
	strict, strictErr := Verify(VerifyOptions{AggRoot: first, SchemaPath: schema, Source: aggmodel.SourceRiotMatchV5})
	if strictErr == nil && len(strict.Problems) == 0 {
		t.Error("verification accepted a demo tree as riot-match-v5")
	}
}

// TestDemoAndRealTreesCannotBeMixed checks the two guards that keep simulated
// and measured artifacts from ending up in one directory.
//
// Either guard on its own is enough to prevent a silent mix; both together mean
// the confusion ADR-005 exists to prevent cannot happen in either direction,
// including when an operator points a job at the wrong directory.
func TestDemoAndRealTreesCannotBeMixed(t *testing.T) {
	t.Parallel()

	demoRoot := t.TempDir()
	if _, err := Demo(DemoOptions{OutDir: demoRoot, MinCellN: 2}); err != nil {
		t.Fatalf("demo: %v", err)
	}

	// A real build must refuse to publish over the demo tree, and it must do so
	// before it reads anything: the raw root it is given does not exist.
	opts := BuildOptions{
		AggRoot:  demoRoot,
		RawRoot:  filepath.Join(t.TempDir(), "does-not-exist"),
		Region:   "EUW",
		Queue:    aggmodel.QueueIDRankedSolo5x5,
		Bracket:  aggmodel.BracketAll,
		MinCellN: 2,
		Metrics:  obs.NopRecorder{},
	}
	if _, err := Build(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "demo") {
		t.Errorf("a real build over a demo tree returned %v, want a refusal that names the demo", err)
	}

	// The demo must refuse a tree a real build published.
	realRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realRoot, aggmodel.VersionDir), 0o755); err != nil {
		t.Fatalf("create real tree: %v", err)
	}
	manifest, err := json.Marshal(aggmodel.Manifest{
		Schema:      aggmodel.SchemaVersion,
		Source:      aggmodel.SourceRiotMatchV5,
		GeneratedAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realRoot, aggmodel.ManifestPath), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := Demo(DemoOptions{OutDir: realRoot, MinCellN: 2}); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("the demo over a real tree returned %v, want a refusal", err)
	}
}

// ---------------------------------------------------------------------------
// Publishing one partition of many
// ---------------------------------------------------------------------------

// TestBuildIsIncrementalAndKeepsOlderPartitions publishes the previous patch
// into a tree that already holds the current one.
//
// A nightly build produces one partition at a time, so the interesting failure
// is not a wrong number: it is a tree that loses the partitions it was not
// asked to rebuild. The test therefore asserts about the partition that the
// second build did not touch, and about the manifest entry that describes it,
// which is the only record a reader has that the older patch exists.
func TestBuildIsIncrementalAndKeepsOlderPartitions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	aggRoot := t.TempDir()
	rawRoot := fixtureRawRoot(t, fixtureFiles(fixtureMatches()))

	current, err := Build(ctx, fixtureBuildOptions(t, aggRoot, rawRoot))
	if err != nil {
		t.Fatalf("build the current patch: %v", err)
	}
	afterFirst := hashTree(t, aggRoot)

	// The second build publishes a different patch into the same tree, one
	// minute later so that generated_at is a real move rather than a rerun.
	previous := fixtureBuildOptions(t, aggRoot, rawRoot)
	previous.Patch = "16.17"
	previous.MinCellN = 1
	previous.Gates = DefaultGateConfig(1)
	previous.Now = func() time.Time { return fixtureNow.Add(time.Minute) }
	previousResult, err := Build(ctx, previous)
	if err != nil {
		t.Fatalf("build the previous patch: %v", err)
	}
	afterSecond := hashTree(t, aggRoot)

	// The partition the first build published is untouched, byte for byte.
	prefix := current.Seg.Dir() + "/"
	if got, want := subtree(afterSecond.Files, prefix), subtree(afterFirst.Files, prefix); !reflect.DeepEqual(got, want) {
		t.Errorf("publishing %s changed %d of %d files under %s",
			previousResult.Seg.Dir(), countDiffering(got, want), len(want), prefix)
	}

	manifest, err := ReadManifest(aggRoot)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	wantPartitions := []string{current.Seg.Dir(), previousResult.Seg.Dir()}
	sort.Strings(wantPartitions)
	gotPartitions := make([]string, 0, len(manifest.Partitions))
	for _, partition := range manifest.Partitions {
		seg := aggmodel.Seg{Patch: partition.Patch, Region: partition.Region, Queue: partition.Queue, Bracket: partition.Bracket}
		gotPartitions = append(gotPartitions, seg.Dir())
		if _, err := os.Stat(filepath.Join(aggRoot, filepath.FromSlash(seg.Dir()))); err != nil {
			t.Errorf("the manifest lists %s, which is not on disk: %v", seg.Dir(), err)
		}
	}
	sort.Strings(gotPartitions)
	if !reflect.DeepEqual(gotPartitions, wantPartitions) {
		t.Errorf("the manifest lists %v, want %v", gotPartitions, wantPartitions)
	}

	// latest still points at the newer patch: publishing an older patch is a
	// backfill, and a reader that followed latest must not be walked backwards.
	if manifest.Latest.Patch != current.Seg.Patch {
		t.Errorf("latest points at patch %s, want %s", manifest.Latest.Patch, current.Seg.Patch)
	}

	// Rerunning the second build is idempotent: same window, same clock, same
	// bytes, which is what makes a retried nightly job safe to just retry.
	if _, err := Build(ctx, previous); err != nil {
		t.Fatalf("rerun the previous patch: %v", err)
	}
	requireSameTree(t, "the rerun of one partition", afterSecond, hashTree(t, aggRoot))

	// The tree holds one provenance, and the manifest enforces it as well as
	// the build does: a caller that writes the document directly cannot smuggle
	// simulated partitions in beside measured ones.
	if _, err := UpdateManifest(aggRoot, previousResult.Partition, aggmodel.SourceDemo, fixtureNow); err == nil ||
		!strings.Contains(err.Error(), "only one source") {
		t.Errorf("merging a demo partition into a real tree returned %v, want a refusal", err)
	}
}

// subtree returns the entries of a tree digest under one prefix.
func subtree(tree map[string]string, prefix string) map[string]string {
	out := make(map[string]string)
	for path, digest := range tree {
		if strings.HasPrefix(path, prefix) {
			out[path] = digest
		}
	}
	return out
}

// countDiffering counts the entries two tree digests do not agree on, so a
// failure says how much moved rather than dumping both maps.
func countDiffering(got, want map[string]string) int {
	differing := 0
	for path, digest := range want {
		if got[path] != digest {
			differing++
		}
	}
	return differing
}

// ---------------------------------------------------------------------------
// The verifier against a real build
// ---------------------------------------------------------------------------

// TestVerifyAcceptsTheFixtureBuild runs the verifier over the tree the fixture
// archive builds, and then over that tree with one field damaged.
//
// The first half is the only check that the writer and the verifier agree on
// what a correct artifact set is. A verifier rule stricter than the writer
// makes the verification command useless against real data - it would report a
// problem for a build that did everything right - and no test of the verifier
// alone can see that, because the bad rule is inside the verifier.
//
// The second half is the control that keeps the first half honest: a verifier
// that accepts everything passes an acceptance test too, so every rule the
// fixture tree relies on is shown to reject a tree that breaks it.
func TestVerifyAcceptsTheFixtureBuild(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	good := t.TempDir()
	rawRoot := fixtureRawRoot(t, fixtureFiles(fixtureMatches()))
	if _, err := Build(ctx, fixtureBuildOptions(t, good, rawRoot)); err != nil {
		t.Fatalf("build the fixture archive: %v", err)
	}

	result, err := Verify(VerifyOptions{AggRoot: good, Source: aggmodel.SourceRiotMatchV5})
	if err != nil {
		t.Fatalf("verifying a good build failed: %v", err)
	}
	if got, want := result.Cells, fixtureExpectedCounts.CellsPublished; got != want {
		t.Errorf("verification checked %d cells, want the %d published cells", got, want)
	}
	if result.Partitions != 1 || result.Documents == 0 {
		t.Errorf("verification checked %d partitions and %d documents, want 1 and some",
			result.Partitions, result.Documents)
	}

	seg := aggmodel.Seg{Patch: "16.18", Region: "EUW",
		Queue: aggmodel.QueueIDRankedSolo5x5, Bracket: aggmodel.BracketAll}
	// championID is a champion the tier list and the manifest both carry, so
	// the case that edits it starts from a document that exists.
	championID := fixtureExpectedCells[0].ChampionID

	cases := []struct {
		name   string
		want   string
		damage func(t *testing.T, root string)
	}{
		{
			name: "a cell below the floor",
			want: "should have been suppressed",
			damage: func(t *testing.T, root string) {
				editTierList(t, root, seg, func(cells []map[string]any) []map[string]any {
					cells[0]["n"] = fixtureMinCellN - 1
					cells[0]["wins"] = fixtureMinCellN - 1
					cells[0]["win_rate"] = 1
					cells[0]["ci95_half_width"] = 0.98 / math.Sqrt(float64(fixtureMinCellN-1))
					return cells
				})
			},
		},
		{
			name: "a confidence interval that is not 0.98/sqrt(n)",
			want: "is not 0.98/sqrt(n)",
			damage: func(t *testing.T, root string) {
				editTierList(t, root, seg, func(cells []map[string]any) []map[string]any {
					cells[0]["ci95_half_width"] = 0.5
					return cells
				})
			},
		},
		{
			name: "a win rate that is not wins/n",
			want: "is not wins/n",
			damage: func(t *testing.T, root string) {
				editTierList(t, root, seg, func(cells []map[string]any) []map[string]any {
					cells[0]["win_rate"] = 1.0
					return cells
				})
			},
		},
		{
			name: "a tier list that lost a cell",
			want: "publishable cells but the manifest says",
			damage: func(t *testing.T, root string) {
				editTierList(t, root, seg, func(cells []map[string]any) []map[string]any {
					return cells[1:]
				})
			},
		},
		{
			// The schema rejects an unpublished grade before the tier-list
			// check runs, so this case pins the schema message rather than the
			// verifier's own wording. Both exist: the schema document is the
			// contract cmd/gen-types ships to a consumer, so a grade no reader
			// can render must fail at the schema, not later.
			name: "a grade the schema does not allow",
			want: "cells[0].tier",
			damage: func(t *testing.T, root string) {
				editTierList(t, root, seg, func(cells []map[string]any) []map[string]any {
					cells[0]["tier"] = "S++"
					return cells
				})
			},
		},
		{
			name: "a champion document that is gone",
			want: "file is missing",
			damage: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, filepath.FromSlash(seg.ChampionPath(championID)))); err != nil {
					t.Fatalf("remove champion document: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			copyTree(t, good, root)
			tc.damage(t, root)

			_, err := Verify(VerifyOptions{AggRoot: root, Source: aggmodel.SourceRiotMatchV5})
			if err == nil {
				t.Fatalf("verification accepted a tree with %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("verification of %s reported:\n%v\nwant a problem mentioning %q", tc.name, err, tc.want)
			}
		})
	}

	// The manifest is the last control, because a tree whose manifest lies
	// about where its numbers came from must fail even when every number in it
	// is right.
	root := t.TempDir()
	copyTree(t, good, root)
	manifestPath := filepath.Join(root, filepath.FromSlash(aggmodel.ManifestPath))
	manifest, err := readJSONDoc[map[string]any](manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest["source"] = string(aggmodel.SourceDemo)
	if err := writeDoc(root, aggmodel.ManifestPath, manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := Verify(VerifyOptions{AggRoot: root, Source: aggmodel.SourceRiotMatchV5}); err == nil {
		t.Error("verification accepted a real tree whose manifest claims to be demo data")
	}
}

// editTierList rewrites the tier list of one partition in place.
//
// The edit is applied to the decoded documents rather than to the bytes so a
// case states the field it damages and nothing else, and the re-encode writes
// the documents back through the same writer the build uses.
func editTierList(t *testing.T, root string, seg aggmodel.Seg, edit func(cells []map[string]any) []map[string]any) {
	t.Helper()

	relPath := seg.TierListPath()
	doc, err := readJSONDoc[map[string]any](filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatalf("read tier list: %v", err)
	}
	cells, ok := doc["cells"].([]any)
	if !ok {
		t.Fatalf("tier list has no cells array")
	}
	decoded := make([]map[string]any, 0, len(cells))
	for _, cell := range cells {
		one, ok := cell.(map[string]any)
		if !ok {
			t.Fatalf("tier list cell is not an object")
		}
		decoded = append(decoded, one)
	}
	edited := edit(decoded)
	doc["cells"] = edited
	if err := writeDoc(root, relPath, doc); err != nil {
		t.Fatalf("write tier list: %v", err)
	}
}

// copyTree copies every file under src into dst, which is how a damage case
// starts from the build the acceptance half already approved.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()

	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := os.ReadFile(path) //nolint:gosec // G304: a test-owned temporary tree.
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
}
