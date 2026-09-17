package aggregate

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

// A raw archive record for a match that is already archived is a fact of life:
// the crawler writes the payload before it records the match, so a crash
// between the two writes, a replay of a maintain batch, or a click of the
// re-crawl button all leave the archive holding the same match twice.
//
// The build must publish the same numbers either way. Two things go wrong if it
// does not, and both are the kind of error a reader cannot see:
//
//   - the published win rate and the sample size printed next to it are counted
//     over different populations, so the page shows a percentage its own `n`
//     does not support; and
//   - the number moves when nothing about the games changed, which makes a
//     re-crawl look like a change in the meta.
//
// The tests below feed the build an archive with a match written into it three
// times - twice more than it should be - and require the output to be byte for
// byte the output of the pristine archive.

// buildFixtureArchive runs the real build, DuckDB included, over one fixture
// archive and returns what it published.
func buildFixtureArchive(t *testing.T, files map[string]string) BuildResult {
	t.Helper()

	aggRoot := t.TempDir()
	opts := fixtureBuildOptions(t, aggRoot, fixtureRawRoot(t, files))
	opts.Auditor = FileAuditor{Root: filepath.Join(t.TempDir(), "build-runs")}

	result, err := Build(context.Background(), opts)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	return result
}

// duplicatedFixtureArchive is the fixture archive with its first match written
// into two later partitions as well.
//
// The second record is byte for byte the first, which is what the crawler used
// to append when it re-walked a match. The third is the same match with the
// winner swapped, which is the shape a payload that was re-serialised at the
// origin has: same id, different body. The first record of a match is the one
// the build keeps - the same row the `matches` table holds, because the archive
// write comes before the insert, so a record appended later can never be the
// one the control plane knows about.
func duplicatedFixtureArchive(matches []fixtureMatch) map[string]string {
	files := fixtureFiles(matches)

	repeated := matches[0]
	repeated.partition = "2026-09-13"
	files[repeated.partition] += renderMatch(repeated) + "\n"

	refetched := matches[0]
	refetched.partition = "2026-09-12"
	refetched.winner = 200
	files[refetched.partition] += renderMatch(refetched) + "\n"

	return files
}

// TestADuplicatedArchiveRecordDoesNotMoveTheNumbers requires the build to read
// a match that the archive holds three times exactly once.
func TestADuplicatedArchiveRecordDoesNotMoveTheNumbers(t *testing.T) {
	t.Parallel()

	matches := fixtureMatches()
	cleanFiles := fixtureFiles(matches)
	dirtyFiles := duplicatedFixtureArchive(matches)

	// The premise: the duplicate is really in the input the second build reads.
	// Both extra records carry the id of the first fixture match.
	if got := strings.Count(dirtyFiles["2026-09-13"]+dirtyFiles["2026-09-12"], `"matchId":"`+matches[0].id+`"`); got != 2 {
		t.Fatalf("the duplicated archive holds %d extra records for %s, want 2", got, matches[0].id)
	}

	clean := buildFixtureArchive(t, cleanFiles)
	dirty := buildFixtureArchive(t, dirtyFiles)

	// The hand-computed table is the anchor: whatever the duplicate does, the
	// pristine archive must keep publishing the numbers a human derived.
	if clean.Counts != fixtureExpectedCounts {
		t.Fatalf("gate counts for the pristine archive are %+v, want %+v", clean.Counts, fixtureExpectedCounts)
	}

	if dirty.Counts != clean.Counts {
		t.Errorf("a duplicated archive record moved the gate counts:\nwith the duplicate:   %+v\nwithout it:          %+v\n"+
			"Every count here is a denominator of something the site publishes, so a re-walk must not move any of them.",
			dirty.Counts, clean.Counts)
	}
	requireCells(t, "tier list built from an archive holding a match three times", dirty.Cells.Cells, fixtureExpectedCells)
}

// TestAPublishedRateAgreesWithItsOwnSampleSize is the second half of the same
// requirement, stated as the invariant a reader is entitled to check.
//
// pick_rate is published as picks over twice the sample size, and the sample
// size is the `matches_used` the same build reports for the window, so a
// published pick_rate has to be a whole number of picks out of 2n. It also has
// to fit inside n: a champion holds at most one slot of one role per match, so
// a cell can never cover more matches than the window has, and a rate that
// implies it does is being counted over a larger population than the `n`
// printed next to it.
//
// This test never compares the document against a stored expectation - it
// compares the document with itself, which is the check a reader cannot do and
// the reason the duplicated archive was worse than merely wrong.
func TestAPublishedRateAgreesWithItsOwnSampleSize(t *testing.T) {
	t.Parallel()

	result := buildFixtureArchive(t, duplicatedFixtureArchive(fixtureMatches()))

	n := float64(result.Counts.MatchesUsed)
	if n == 0 {
		t.Fatalf("the build reported a sample size of zero, so this test would pass vacuously")
	}
	if len(result.Cells.Cells) == 0 {
		t.Fatalf("the build published no cells, so this test would pass vacuously")
	}

	published := 0
	for _, cell := range result.Cells.Cells {
		// A rate is rounded to four places before it is published, so a whole
		// number of picks is exact to well inside this tolerance.
		picks := float64(cell.PickRate) * 2 * n
		if math.Abs(picks-math.Round(picks)) > 0.01 {
			t.Errorf("champion %d %s: pick_rate %v over the sample size n=%d published beside it is %v picks, "+
				"which is not a whole number - the rate and its own n are counted over different populations",
				cell.ChampionID, cell.Role, cell.PickRate, int(n), picks)
			continue
		}
		if got := int(math.Round(picks)); got != cell.N {
			t.Errorf("champion %d %s: pick_rate %v over the sample size n=%d published beside it is %d picks, "+
				"but the cell reports %d participants",
				cell.ChampionID, cell.Role, cell.PickRate, int(n), got, cell.N)
			continue
		}
		if cell.N > int(n) {
			t.Errorf("champion %d %s: the cell covers %d of a window's %d matches, and its pick_rate %v over "+
				"the same n=%d is %v picks. A champion cannot hold a role slot twice in one match, so the rate "+
				"is counted over more games than the sample size beside it.",
				cell.ChampionID, cell.Role, cell.N, int(n), cell.PickRate, int(n), picks)
			continue
		}
		published++
	}

	if published == 0 {
		t.Fatalf("no cell was checked, so this test would pass vacuously")
	}
}
