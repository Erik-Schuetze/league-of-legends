package aggregate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The feature build's end-to-end test.
//
// The nightly build's fixture test reads a parquet part with a single payload
// column, which is all the nightly envelope needs because every key it reads is
// a JSON path. The feature build's envelopes also read the archive's own
// columns - region, patch, game_version, queue_id, game_creation_ms,
// game_duration_s - because a timeline payload carries none of them, so a
// payload-only part is not a shape this build can read at all. The fixture
// archive here therefore holds the full row the crawler would have written,
// which is also the only way the scope filter can be tested against the region
// spelling it exists to reconcile.
//
// The envelope columns are rendered from the fixture model rather than checked
// in beside the payloads. They are a projection of the same structs the payloads
// are rendered from - the same ids, patches, queues and durations - so a second
// file could only drift from them, and TestFixtureTimelineSourcesAreCurrent
// already fails if the fixture model and the checked-in payloads disagree.

// featureFixtureFetchedAt is the fetched_at every fixture archive row carries.
// The build does not read it; a fixture archive that omitted a column the real
// one has would not be the archive the build is tested against.
const featureFixtureFetchedAt = "2026-09-14T06:00:00Z"

// featureFixtureRegion is the published region both fixture archive rows carry.
//
// It is deliberately not the payload's platform id ("EUW1"): the crawler records
// the region it was configured with, while the payload names the shard it
// resolved to, and reconciling those two spellings is what the build's scope
// filter exists to do. A fixture that wrote EUW1 into the archive column would
// test that filter against the one spelling that cannot disagree with itself.
const featureFixtureRegion = "EUW"

// fixtureGameVersion is the four-component version renderMatch writes into a
// payload, repeated here because the archive row carries it as a column of its
// own and the two have to agree for the fixture to be a real archive row.
func fixtureGameVersion(m fixtureMatch) string {
	return m.patch + ".612.9234"
}

// featureArchiveRow renders one fixture match as the archive row the crawler
// writes beside its payload: the frozen MatchRow/TimelineRow envelope, which
// both archives share column for column.
func featureArchiveRow(m fixtureTimelineMatch, payload string) string {
	created, err := time.Parse(time.RFC3339, m.summary.created)
	if err != nil {
		panic("fixture match " + m.summary.id + ": " + err.Error())
	}
	return fmt.Sprintf(`{"match_id":%s,"region":%s,"queue_id":%d,"patch":%s,"game_version":%s,`+
		`"game_creation_ms":%d,"game_duration_s":%d,"payload_version":"1","fetched_at":%s,"payload":%s,"payload_sha256":%s}`,
		jsonText(m.summary.id),
		jsonText(featureFixtureRegion),
		m.summary.queue,
		jsonText(m.summary.patch),
		jsonText(fixtureGameVersion(m.summary)),
		created.UnixMilli(),
		m.summary.durationOf(),
		jsonText(featureFixtureFetchedAt),
		jsonText(payload),
		jsonText(featureFixtureSHA256(payload)))
}

// featureFixtureSummaryRows renders every fixture match's summary archive row.
func featureFixtureSummaryRows(matches []fixtureTimelineMatch) []string {
	rows := make([]string, 0, len(matches))
	for _, m := range matches {
		rows = append(rows, featureArchiveRow(m, renderMatch(m.summary)))
	}
	return rows
}

// featureFixtureTimelineRows renders every fixture match's timeline archive row,
// skipping the match whose timeline was never fetched: an absent timeline is a
// missing row, not a row with an empty payload, and the ledger has to record it
// as such.
func featureFixtureTimelineRows(matches []fixtureTimelineMatch) []string {
	var rows []string
	for _, m := range matches {
		if m.timeline.absent {
			continue
		}
		rows = append(rows, featureArchiveRow(m, renderTimeline(m)))
	}
	return rows
}

// jsonText quotes a string as a JSON string literal, which is how a text column
// is written into a newline-delimited JSON row.
func jsonText(s string) string {
	quoted, err := json.Marshal(s)
	if err != nil {
		panic("fixture: " + err.Error())
	}
	return string(quoted)
}

// featureFixtureSHA256 is the digest the crawler records beside a payload.
func featureFixtureSHA256(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// featureArchiveParquetSQL converts one newline-delimited JSON rendering of
// archive rows into the parquet part the crawler would have written.
//
// Every column is typed explicitly, because DuckDB's reader would otherwise
// infer game_creation_ms as a double and fetched_at as a string, and the real
// archive is written from a struct with a declared type per column. A fixture
// that let the reader guess would be testing a different schema.
func featureArchiveParquetSQL(jsonl, out string) string {
	return fmt.Sprintf(`COPY (
  SELECT match_id, region, queue_id, patch, game_version, game_creation_ms,
         game_duration_s, payload_version, fetched_at, payload, payload_sha256
  FROM read_json(%s, format = 'newline_delimited', columns = {
    match_id: 'VARCHAR',
    region: 'VARCHAR',
    queue_id: 'INTEGER',
    patch: 'VARCHAR',
    game_version: 'VARCHAR',
    game_creation_ms: 'BIGINT',
    game_duration_s: 'INTEGER',
    payload_version: 'VARCHAR',
    fetched_at: 'TIMESTAMP',
    payload: 'VARCHAR',
    payload_sha256: 'VARCHAR'
  })
) TO %s (FORMAT PARQUET, COMPRESSION ZSTD);`, quoteLiteral(jsonl), quoteLiteral(out))
}

// featureFixtureRawRoot materialises the fixture as the two-source archive
// layout the build reads and returns the raw root.
//
// Nothing binary is committed: the parts are written by the pinned client from a
// text rendering of the rows, exactly as the nightly fixture does it, so a
// machine with a different DuckDB changes nothing on disk.
func featureFixtureRawRoot(t *testing.T, matches []fixtureTimelineMatch) string {
	t.Helper()

	bin := duckDBBin(t)
	root := t.TempDir()
	sets := []struct {
		source string
		rows   []string
	}{
		{rawSourceDir, featureFixtureSummaryRows(matches)},
		{RawSourceTimeline, featureFixtureTimelineRows(matches)},
	}
	for _, set := range sets {
		if len(set.rows) == 0 {
			t.Fatalf("fixture %s has no rows", set.source)
		}
		dir := filepath.Join(root, "riot", set.source, rawPartitionPr+fixtureTimelinePartition)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create partition: %v", err)
		}
		jsonl := filepath.Join(dir, "rows.jsonl")
		if err := os.WriteFile(jsonl, []byte(strings.Join(set.rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("write fixture jsonl: %v", err)
		}
		out := filepath.Join(dir, "part-00001.parquet")
		runDuckDB(t, bin, featureArchiveParquetSQL(jsonl, out))
		if err := os.Remove(jsonl); err != nil {
			t.Fatalf("remove fixture jsonl: %v", err)
		}
	}
	return root
}

// featureFixtureOptions is the build under test: the fixture archives, the
// fixture dataset root, and a fixed clock so the manifest is reproducible.
//
// The scope is set, which the production default is not, because the fixture
// region column and the fixture payload's platform id disagree by design and
// the filter is what reconciles them.
func featureFixtureOptions(t *testing.T, datasetRoot, rawRoot string) FeatureOptions {
	t.Helper()

	return FeatureOptions{
		DatasetRoot: datasetRoot,
		RawRoot:     rawRoot,
		Region:      featureFixtureRegion,
		Queue:       aggmodel.QueueIDRankedSolo5x5,
		DuckDBBin:   duckDBBin(t),
		GitSHA:      fixtureGitSHA,
		Now:         func() time.Time { return fixtureNow },
	}
}

// featureTableGlob is the published path of one table inside the dataset root.
func featureTableGlob(result FeatureResult, table string) string {
	return parquetOf(filepath.Join(result.Dir, table))
}

// featureQuery runs one statement and returns the client's json rendering.
//
// The dataset assertions read the published tree rather than the staging tree
// on purpose: what a reader will see is the publish, and a table that only
// exists under staging is not part of the dataset.
func featureQuery(t *testing.T, statement string) string {
	t.Helper()

	cmd := exec.Command(duckDBBin(t), "-batch", "-init", "/dev/null", "-json")
	cmd.Stdin = strings.NewReader(statement + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("query failed: %v\nstatement:\n%s\nstderr:\n%s", err, statement, stderr.String())
	}
	return stdout.String()
}

// featureDecodeRows decodes the rows a query printed.
func featureDecodeRows[T any](t *testing.T, out string) []T {
	t.Helper()

	if strings.TrimSpace(out) == "" {
		return nil
	}
	var rows []T
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode query rows: %v\noutput:\n%s", err, out)
	}
	return rows
}

// featureQueryCSV runs one statement against the published dataset and returns
// the client's csv rendering.
//
// It is the value-level counterpart of featureQuery, and it exists because the
// expected values in features_dataset_test.go are csv blocks: comparing them as
// text keeps the diff to the rows that changed, and it renders a NULL as an
// empty field, which a decoded struct of zero values would hide.
func featureQueryCSV(t *testing.T, result FeatureResult, statement string) string {
	t.Helper()

	cmd := exec.Command(duckDBBin(t), "-batch", "-init", "/dev/null", "-csv")
	cmd.Stdin = strings.NewReader(statement + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("query failed: %v\nstatement:\n%s\nstderr:\n%s", err, statement, stderr.String())
	}
	return stdout.String()
}
