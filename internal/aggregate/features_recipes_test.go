package aggregate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The dataset's recipes are executed, not trusted.
//
// There is no web tier and no query service, so the README this build writes is
// the analyst's entire interface to the data (ADR-011, ADR-014). A recipe that
// does not run is therefore not a documentation defect - it is the interface
// failing, and it fails for the one reader who cannot debug it. This test is the
// only thing standing between a generated recipe and that reader, so it runs
// every block the build emits, against a real built dataset, and fails on the
// first one that does not.
//
// Two deliberate choices about how it does that:
//
//   - The blocks are parsed out of the README the build just wrote, not out of
//     the generator's source. The two are the same text by construction, but a
//     test that reads the source proves the source is self-consistent, while a
//     test that reads the artifact proves the reader's copy works. It is the
//     reader's copy that the recipes exist for, and a funnel between the
//     generator and the publish step would be invisible to the source-reading
//     version.
//
//   - The row counts are asserted, not just "the query returned". A statement
//     that silently returns nothing - a join that stopped matching, a filter
//     that excludes everything - is the failure mode a recipe is most likely to
//     acquire, and it is exactly the one that "it ran without error" cannot see.
//
// The pandas block at the end is the exception, and it is checked structurally
// instead: pandas is not a dependency of this repository and is not installed in
// the environment this suite runs in, so executing it would turn a Go test into
// a Python requirement. Its column references are resolved against the emitted
// schema.json instead, which catches a renamed or dropped column - the failure a
// reader would hit - without inventing a dependency to catch it.
//
// What this proves, and what it does not: the fixture holds three eligible
// matches, so these assertions pin each recipe's structure, its filters and its
// shape on a real dataset. They cannot pin its statistics. A recipe can be
// structurally faithful and still ask the wrong question, and no assertion below
// would notice - that is what the recipe audit is for, and the reason the
// dataset's own counts are published beside the recipes rather than asserted
// here.
type featureRecipeBlock struct {
	lang string
	body string
}

// featureRecipePlaceholder is the path the README tells its reader to replace.
const featureRecipePlaceholder = "/path/to/dataset"

// featureRecipeBlocks returns the fenced code blocks of a README in order.
func featureRecipeBlocks(t *testing.T, readme string) []featureRecipeBlock {
	t.Helper()

	var blocks []featureRecipeBlock
	var lang string
	var body []string
	open := false
	for _, line := range strings.Split(readme, "\n") {
		if strings.HasPrefix(line, "```") {
			if !open {
				open = true
				lang = strings.TrimPrefix(line, "```")
				body = nil
				continue
			}
			blocks = append(blocks, featureRecipeBlock{lang: lang, body: strings.Join(body, "\n")})
			open = false
			continue
		}
		if open {
			body = append(body, line)
		}
	}
	if open {
		t.Fatal("README has an unterminated code block")
	}
	return blocks
}

// featureRecipeCases is every SQL recipe the README is expected to emit, one
// entry each, keyed by a fragment of the recipe itself so that a recipe which
// changes shape fails here instead of silently matching nothing.
//
// The row counts are this fixture's, and a zero is a count like any other: the
// bucketed curve excludes buckets of fewer than 30 games by design, and the
// fixture has three eligible matches, so zero rows is the recipe working. Writing
// that down is the point - the alternative is a test that would pass just as
// happily if the query returned nothing because the query was broken.
var featureRecipeCases = []struct {
	name   string
	marker string
	rows   int
	why    string
}{
	{
		name:   "cs_at_five",
		marker: "cs_diff_5 <= -10",
		rows:   1,
		why:    "one bucket: no fixture lane lost ten CS by minute five",
	},
	{
		name:   "kda_vs_opponent",
		marker: "opponent_kda_15",
		rows:   2,
		why:    "mid and top, the two solo lanes",
	},
	{
		name:   "cs_diff_curve",
		marker: "cs_diff_bucket",
		rows:   0,
		why:    "HAVING n >= 30 excludes every bucket of a three-match fixture",
	},
	{
		name:   "minute_curves",
		marker: "avg_jungle_cs",
		rows:   20,
		why:    "the fixture's frames cover minutes one to twenty",
	},
	{
		name:   "first_objective",
		marker: "took_first_dragon",
		rows:   2,
		why:    "one row per side of the first dragon, taken or not",
	},
	{
		name:   "event_timing",
		marker: "median_minute",
		rows:   10,
		why:    "one row per event type the fixture emits",
	},
	{
		name:   "death_positions",
		marker: "target_participant_id > 0",
		rows:   7,
		why:    "distinct death minutes and coordinates among the eligible matches",
	},
	{
		name:   "skill_order",
		marker: "skill_order IS NOT NULL",
		rows:   1,
		why:    "one top-lane skill order among the eligible matches",
	},
}

// featureRecipeRowCount counts the rows a JSON-rendered query printed.
func featureRecipeRowCount(out string) int {
	if strings.TrimSpace(out) == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSpace(out), "\n"))
}

func TestFeatureDatasetRecipesRun(t *testing.T) {
	result := featureFixtureDataset(t)
	raw, err := os.ReadFile(filepath.Join(result.Dir, "README.md"))
	if err != nil {
		t.Fatalf("read the emitted README: %v", err)
	}
	blocks := featureRecipeBlocks(t, string(raw))

	var sql, python int
	for _, block := range blocks {
		switch block.lang {
		case "sql":
			sql++
		case "python":
			python++
		default:
			t.Errorf("README emits a %q block; a recipe language this test cannot run is a recipe nobody runs", block.lang)
		}
	}
	if sql != len(featureRecipeCases) {
		t.Fatalf("README emits %d SQL recipes and this test knows %d: a new recipe must be added to featureRecipeCases with its expected row count, or it ships unexecuted",
			sql, len(featureRecipeCases))
	}
	if python != 1 {
		t.Fatalf("README emits %d python blocks, want 1", python)
	}

	for _, c := range featureRecipeCases {
		t.Run(c.name, func(t *testing.T) {
			block, ok := featureRecipeBlockFor(blocks, "sql", c.marker)
			if !ok {
				t.Fatalf("no SQL recipe contains %q: the recipe moved or changed shape, so its expected row count is stale", c.marker)
			}
			statement := featureRecipeDatasetPath(t, block.body, result.Dir)
			got := featureRecipeRowCount(featureQuery(t, statement))
			if got != c.rows {
				t.Errorf("recipe returned %d rows, want %d (%s)\nstatement:\n%s", got, c.rows, c.why, statement)
			}
		})
	}

	t.Run("lane_opponent_filters", func(t *testing.T) {
		// The two recipes that compare a participant to a lane opponent. Both
		// must keep the filters that make the comparison mean one thing: a lane
		// opponent exists in a solo lane, and the jungle has no lane at all.
		for _, marker := range []string{"cs_diff_5 <= -10", "opponent_kda_15"} {
			block, ok := featureRecipeBlockFor(blocks, "sql", marker)
			if !ok {
				t.Fatalf("no SQL recipe contains %q", marker)
			}
			for _, want := range []string{"is_duo_lane", "has_nominal_opponent"} {
				if !strings.Contains(block.body, want) {
					t.Errorf("the recipe containing %q dropped %q, so it compares a number to a number that does not mean the same thing", marker, want)
				}
			}
		}
	})

	t.Run("kda_covers_five_ten_and_fifteen", func(t *testing.T) {
		// This recipe is the reason the dataset exists: the question is which of
		// a lane opponent's advantages at minute five, ten and fifteen predict
		// the game's outcome. A recipe that reads one checkpoint, or that never
		// reaches the opponent, answers a different question under the same
		// heading - which is what it used to do.
		block, ok := featureRecipeBlockFor(blocks, "sql", "opponent_kda_15")
		if !ok {
			t.Fatal("no SQL recipe contains \"opponent_kda_15\"")
		}
		for _, want := range []string{
			"opponent_participant_id",
			"kills_5", "kills_10", "kills_15",
		} {
			if !strings.Contains(block.body, want) {
				t.Errorf("the KDA recipe no longer mentions %q", want)
			}
		}

		// A deathless game has a death count of zero, so every one of the six
		// ratios - both sides at each of the three checkpoints - has to divide
		// by greatest(deaths, 1) rather than by the raw count. Asserting the
		// count of the guard, not just its presence, is what makes a recipe that
		// guards five of the six unrepresentable.
		if got := strings.Count(block.body, "greatest("); got < 6 {
			t.Errorf("the KDA recipe guards %d of its six ratios with greatest(deaths, 1), want at least 6", got)
		}
		if bad := regexp.MustCompile(`/\s*[po]\.deaths_`).FindString(block.body); bad != "" {
			t.Errorf("the KDA recipe divides by a raw death count (%q), which is zero in a deathless game", bad)
		}

		// One output column per checkpoint, each appearing exactly once: the
		// heading promises a curve, and a query that reads a single checkpoint
		// three times - which DuckDB accepts, since duplicate output names are
		// legal - would print three identical columns under three different
		// names and look like a curve.
		//
		// Counting the output names is not enough on its own, because an alias
		// can be right while the expression beneath it reads another checkpoint.
		// So the signed difference is pinned per checkpoint too: it is the
		// metric the recipe is for, and reversing it would invert the finding.
		for _, want := range []string{
			"avg_kda_diff_5", "avg_kda_diff_10", "avg_kda_diff_15",
			"kda_5 - opponent_kda_5",
			"kda_10 - opponent_kda_10",
			"kda_15 - opponent_kda_15",
		} {
			if got := strings.Count(block.body, want); got != 1 {
				t.Errorf("the KDA recipe names %s %d times, want once", want, got)
			}
		}
	})

	t.Run("python_block_columns_exist", func(t *testing.T) {
		block, ok := featureRecipeBlockFor(blocks, "python", "read_parquet")
		if !ok {
			t.Fatal("no python block calls read_parquet")
		}
		// The pandas block reads a table *directory*, which is how pyarrow's
		// dataset discovery works, so the directory has to be there and hold
		// parts rather than being a glob it would have to expand itself.
		table := filepath.Join(result.Dir, "lane_matchups")
		entries, err := os.ReadDir(table)
		if err != nil {
			t.Fatalf("the python recipe reads %s as a directory: %v", table, err)
		}
		parts := 0
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".parquet") {
				parts++
			}
		}
		if parts == 0 {
			t.Errorf("%s holds no parquet parts, so the python recipe reads nothing", table)
		}

		var doc featureSchemaDoc
		raw, err := os.ReadFile(filepath.Join(result.Dir, "schema.json"))
		if err != nil {
			t.Fatalf("read the emitted schema.json: %v", err)
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("decode schema.json: %v", err)
		}
		columns := map[string]bool{}
		for _, table := range doc.Tables {
			if table.Name != "lane_matchups" {
				continue
			}
			for _, column := range table.Columns {
				columns[column.Name] = true
			}
		}
		if len(columns) == 0 {
			t.Fatal("schema.json describes no lane_matchups columns")
		}

		if !strings.Contains(block.body, featureRecipePlaceholder) {
			t.Errorf("the python recipe does not name the dataset through %q:\n%s", featureRecipePlaceholder, block.body)
		}

		// `lm` is the table and `solo` the filtered frame; both are addressed
		// with the same column names, which is the only thing this can check
		// without a pandas runtime. A trailing `(` means a method call
		// (`solo.groupby(...)`) rather than a column, so the second capture
		// group is what distinguishes the two - Go's regexp has no lookahead.
		references := regexp.MustCompile(`\b(?:lm|solo)\.([a-z_][a-z0-9_]*)(\s*\()`).FindAllStringSubmatch(block.body, -1)
		if len(references) == 0 {
			t.Error("the python recipe references no columns of lane_matchups, so this check proved nothing")
		}
		for _, match := range references {
			if match[2] != "" {
				continue
			}
			if !columns[match[1]] {
				t.Errorf("the python recipe reads lane_matchups.%s, which schema.json does not describe", match[1])
			}
		}
	})
}

// featureRecipeBlockFor finds the one block of a language whose body contains a
// fragment.
func featureRecipeBlockFor(blocks []featureRecipeBlock, lang, marker string) (featureRecipeBlock, bool) {
	for _, block := range blocks {
		if block.lang == lang && strings.Contains(block.body, marker) {
			return block, true
		}
	}
	return featureRecipeBlock{}, false
}

// featureRecipeDatasetPath substitutes the README's placeholder with the
// published dataset, and fails if there was nothing to substitute.
//
// The placeholder is the README's own contract with its reader - "replace
// /path/to/dataset with the directory this file is in" - so a recipe that
// hard-codes a path instead would be one that reader cannot run at all, and it
// would pass a test that substituted a path unconditionally.
func featureRecipeDatasetPath(t *testing.T, body, dir string) string {
	t.Helper()

	if !strings.Contains(body, featureRecipePlaceholder) {
		t.Fatalf("recipe does not name the dataset through %q:\n%s", featureRecipePlaceholder, body)
	}
	return strings.ReplaceAll(body, featureRecipePlaceholder, dir)
}
