package aggregate

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/raw"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// The writer-to-DuckDB boundary.
//
// Every other test of this dataset reads a fixture archive whose parquet was
// produced by DuckDB from a text rendering of the rows, and that rendering
// declares `payload: 'VARCHAR'` because the test has to say what type it means.
// Which means the fixture tests cannot, even in principle, catch a change in how
// the archive is written: they assert the read of a type they chose themselves.
//
// The archive's payload column is where that matters. It is a Go string holding
// arbitrary JSON, and DuckDB's parquet reader resolves the physical BYTE_ARRAY
// to VARCHAR or to BLOB by the logical annotation the writer put on it. A BLOB
// is not a JSON value: json_extract returns nothing, the envelope's
// json_valid(payload) is false, and every row is graded malformed - the whole
// dataset refuses to build, with a gate failure that names none of the causes.
// The build reads this column through the same reader, so the boundary is worth
// one test that goes through the real writer and the real reader rather than
// two that go through a rendering.
//
// The assertion is deliberately threefold: the type, the digest the writer
// recorded beside the payload, and the exact byte count of the body the client
// fetched. The type alone would pass for a writer that silently re-encoded the
// payload, which is the other half of what the archive promises - it stores the
// bytes Riot sent.
func TestFeatureArchivePayloadReadsAsJSONInDuckDB(t *testing.T) {
	match := fixtureTimelineMatches()[0]
	summaryBody, timelineBody := renderMatch(match.summary), renderTimeline(match)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := summaryBody
		if strings.HasSuffix(r.URL.Path, "/timeline") {
			body = timelineBody
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write %s: %v", r.URL.Path, err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := riot.NewClient(riot.Options{
		PlatformBaseURL: server.URL,
		RegionalBaseURL: server.URL,
		KeyProvider:     riot.NewKeyProviderFrom("RGAPI-fixture-key", ""),
		HTTPClient:      server.Client(),
		MaxAttempts:     1,
	})
	if err != nil {
		t.Fatalf("riot.NewClient: %v", err)
	}

	ctx := context.Background()
	summary, err := client.Match(ctx, match.summary.id)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	timeline, err := client.Timeline(ctx, match.summary.id)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}

	meta := fixtureArchiveMeta(t, match)
	root := t.TempDir()
	writer, err := raw.New(raw.Options{Root: root})
	if err != nil {
		t.Fatalf("raw.New: %v", err)
	}
	if err := writer.WriteMatch(ctx, summary, meta); err != nil {
		t.Fatalf("WriteMatch: %v", err)
	}
	if err := writer.WriteTimeline(ctx, timeline, meta); err != nil {
		t.Fatalf("WriteTimeline: %v", err)
	}
	if err := writer.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	partition := meta.PartitionDate()
	for _, archive := range []struct {
		name   string
		dir    string
		body   string
		digest string
	}{
		{"summary", raw.MatchDir(root, partition), summaryBody, featureFixtureSHA256(summaryBody)},
		{"timeline", raw.TimelineDir(root, partition), timelineBody, featureFixtureSHA256(timelineBody)},
	} {
		t.Run(archive.name, func(t *testing.T) {
			parts, err := raw.PartPaths(archive.dir)
			if err != nil {
				t.Fatalf("PartPaths: %v", err)
			}
			if len(parts) != 1 {
				t.Fatalf("archive %s holds %d parts, want 1", archive.name, len(parts))
			}
			// The reader the build itself uses, asked the two questions the
			// build asks of every row: what type is the payload, and does the
			// match id come out of it.
			rows := featureDecodeRows[struct {
				PayloadType  string `json:"payload_type"`
				MatchID      string `json:"match_id"`
				PayloadBytes int64  `json:"payload_bytes"`
				Digest       string `json:"digest"`
			}](t, featureQuery(t, `SELECT
  typeof(payload) AS payload_type,
  json_extract_string(payload, '$.metadata.matchId') AS match_id,
  strlen(payload) AS payload_bytes,
  payload_sha256 AS digest
FROM read_parquet(`+quoteLiteral(parts[0])+`)`))
			if len(rows) != 1 {
				t.Fatalf("read back %d rows, want 1", len(rows))
			}
			row := rows[0]
			if row.PayloadType != "VARCHAR" {
				t.Errorf("typeof(payload) = %q, want VARCHAR: a BLOB is not a JSON value and the feature build cannot read it",
					row.PayloadType)
			}
			if row.MatchID != match.summary.id {
				t.Errorf("json_extract(payload, '$.metadata.matchId') = %q, want %q", row.MatchID, match.summary.id)
			}
			if row.PayloadBytes != int64(len(archive.body)) {
				t.Errorf("strlen(payload) = %d, want the fetched body's %d bytes", row.PayloadBytes, len(archive.body))
			}
			if row.Digest != archive.digest {
				t.Errorf("payload_sha256 = %q, want the digest of the fetched body %q", row.Digest, archive.digest)
			}
		})
	}
}

// fixtureArchiveMeta is the provenance the crawler records beside a fixture
// payload: the same envelope both archives carry.
func fixtureArchiveMeta(t *testing.T, m fixtureTimelineMatch) contract.MatchMeta {
	t.Helper()

	created, err := time.Parse(time.RFC3339, m.summary.created)
	if err != nil {
		t.Fatalf("fixture match %s creation: %v", m.summary.id, err)
	}
	fetchedAt, err := time.Parse(time.RFC3339, featureFixtureFetchedAt)
	if err != nil {
		t.Fatalf("fixture fetch time: %v", err)
	}
	return contract.MatchMeta{
		MatchID:       m.summary.id,
		Region:        featureFixtureRegion,
		QueueID:       m.summary.queue,
		Patch:         m.summary.patch,
		GameVersion:   fixtureGameVersion(m.summary),
		GameCreation:  created,
		GameDurationS: m.summary.durationOf(),
		FetchedAt:     fetchedAt,
	}
}
