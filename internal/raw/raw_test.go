package raw

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// fetcher is a fixed instant, so a partition path in an assertion is a
// constant rather than something the test has to compute twice.
var fetchedAt = time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC)

// syntheticMatch is the shape the client decodes: a payload with fields v1 does
// not model, which is exactly what the archive must keep.
const syntheticPayload = `{"metadata":{"matchId":"EUW1_0000000001","participants":["fixture-puuid-01","fixture-puuid-02"]},` +
	`"info":{"gameCreation":1789000000000,"gameDuration":1874,"gameVersion":"16.18.612.9234","queueId":420,` +
	`"unmodelledField":{"kept":"verbatim"},"participants":[]}}`

func decodeSyntheticMatch(t *testing.T, payload string) riot.MatchDTO {
	t.Helper()
	var dto riot.MatchDTO
	if err := json.Unmarshal([]byte(payload), &dto); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return dto
}

// fetchedMatch is the only way to get a MatchDTO that carries the response
// bytes: retention happens in the client, and that is the point. The archive
// stores what Riot sent, which a value reassembled from typed fields cannot.
func fetchedMatch(t *testing.T, body string, gzipped bool) riot.MatchDTO {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if gzipped {
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			defer func() { _ = zw.Close() }()
			if _, err := io.WriteString(zw, body); err != nil {
				t.Errorf("write gzip body: %v", err)
			}
			return
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write body: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	client, err := riot.NewClient(riot.Options{
		PlatformBaseURL: srv.URL,
		RegionalBaseURL: srv.URL,
		KeyProvider:     riot.NewKeyProviderFrom("RGAPI-fixture-key", ""),
		HTTPClient:      srv.Client(),
		MaxAttempts:     1,
	})
	if err != nil {
		t.Fatalf("riot.NewClient: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)
	dto, _, err := client.MatchWithPayload(context.Background(), "EUW1_0000000001")
	if err != nil {
		t.Fatalf("MatchWithPayload: %v", err)
	}
	return dto
}

func newTestWriter(t *testing.T, mutate func(*Options)) (*Writer, string) {
	t.Helper()
	root := t.TempDir()
	opts := Options{Root: root}
	if mutate != nil {
		mutate(&opts)
	}
	w, err := New(opts)
	if err != nil {
		t.Fatalf("raw.New: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, root
}

func TestWriteMatchRoundTripsTheVerbatimPayload(t *testing.T) {
	w, root := newTestWriter(t, nil)
	ctx := context.Background()
	match := fetchedMatch(t, syntheticPayload, false)
	meta := contract.MatchMeta{
		MatchID:       "EUW1_0000000001",
		Region:        "euw",
		QueueID:       420,
		Patch:         "16.18",
		GameVersion:   "16.18.612.9234",
		GameCreation:  time.UnixMilli(1789000000000).UTC(),
		GameDurationS: 1874,
		FetchedAt:     fetchedAt,
	}
	if err := w.WriteMatch(ctx, match, meta); err != nil {
		t.Fatalf("WriteMatch: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	dir := MatchDir(root, "2026-03-02")
	if want := filepath.Join(root, "riot", "match-v5", "dt=2026-03-02"); dir != want {
		t.Fatalf("partition dir = %q, want %q", dir, want)
	}
	rows, err := ReadMatchPartition(dir)
	if err != nil {
		t.Fatalf("ReadMatchPartition: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Payload != syntheticPayload {
		t.Fatalf("payload = %q, want the response body verbatim", row.Payload)
	}
	if !strings.Contains(row.Payload, "unmodelledField") {
		t.Fatal("the payload lost a field the DTO does not model")
	}
	sum := sha256.Sum256([]byte(syntheticPayload))
	if row.PayloadSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 = %q, want %q", row.PayloadSHA256, hex.EncodeToString(sum[:]))
	}
	if row.MatchID != "EUW1_0000000001" || row.Region != "EUW" || row.QueueID != 420 {
		t.Fatalf("row identity = %+v", row)
	}
	if row.Patch != "16.18" || row.GameVersion != "16.18.612.9234" {
		t.Fatalf("row provenance = %+v", row)
	}
	if row.GameDurationS != 1874 {
		t.Fatalf("game duration = %d, want 1874", row.GameDurationS)
	}
	if !row.FetchedAt.Equal(fetchedAt) {
		t.Fatalf("fetched_at = %s, want %s", row.FetchedAt, fetchedAt)
	}
	if row.PayloadVersion != MatchPayloadVersion {
		t.Fatalf("payload version = %q", row.PayloadVersion)
	}
	if uri := MatchPartitionURI(root, meta); uri != dir {
		t.Fatalf("MatchPartitionURI = %q, want the partition %q", uri, dir)
	}
}

func TestWriteMatchFallsBackToTheTypedFields(t *testing.T) {
	// A value the client did not fetch - one built from a fixture - still has
	// to be writable, so the writer must not depend on retained bytes.
	w, root := newTestWriter(t, nil)
	match := decodeSyntheticMatch(t, `{"metadata":{"matchId":"EUW1_0000000002"},"info":{"queueId":440,"gameDuration":900,"gameVersion":"16.18.1.1","gameCreation":1789000000000}}`)
	if err := w.WriteMatch(context.Background(), match, contract.MatchMeta{FetchedAt: fetchedAt}); err != nil {
		t.Fatalf("WriteMatch: %v", err)
	}
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rows, err := ReadMatchPartition(MatchDir(root, "2026-03-02"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].MatchID != "EUW1_0000000002" || rows[0].QueueID != 440 || rows[0].GameDurationS != 900 {
		t.Fatalf("re-encoded row = %+v", rows[0])
	}
	if rows[0].PayloadSHA256 == "" {
		t.Fatal("a re-encoded payload still needs a checksum")
	}
}

func TestWriteLeaguePageRoundTrips(t *testing.T) {
	w, root := newTestWriter(t, nil)
	entries := []riot.LeagueEntryDTO{
		{PUUID: "fixture-puuid-01", QueueType: "RANKED_SOLO_5x5", Tier: "GOLD", Rank: "I", LeaguePoints: 42},
		{PUUID: "fixture-puuid-02", QueueType: "RANKED_SOLO_5x5", Tier: "GOLD", Rank: "I", LeaguePoints: 7},
		{PUUID: "fixture-puuid-03", QueueType: "RANKED_SOLO_5x5", Tier: "GOLD", Rank: "I", LeaguePoints: 88},
	}
	meta := contract.LeagueMeta{Region: "euw", QueueType: "RANKED_SOLO_5x5", Tier: "GOLD", Division: "I", FetchedAt: fetchedAt}
	if err := w.WriteLeagueEntries(context.Background(), entries, meta); err != nil {
		t.Fatalf("WriteLeagueEntries: %v", err)
	}
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	dir := LeagueDir(root, "2026-03-02", "EUW")
	if want := filepath.Join(root, "riot", "league-v4", "dt=2026-03-02", "region=EUW"); dir != want {
		t.Fatalf("league dir = %q, want %q", dir, want)
	}
	paths, err := PartPaths(dir)
	if err != nil {
		t.Fatalf("PartPaths: %v", err)
	}
	rows, err := ReadLeaguePages(paths...)
	if err != nil {
		t.Fatalf("ReadLeaguePages: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want one row per page", len(rows))
	}
	row := rows[0]
	if row.Entries != 3 {
		t.Fatalf("entries = %d, want 3", row.Entries)
	}
	if row.Region != "EUW" || row.Tier != "GOLD" || row.Division != "I" {
		t.Fatalf("row provenance = %+v", row)
	}
	var decoded []riot.LeagueEntryDTO
	if err := json.Unmarshal([]byte(row.Payload), &decoded); err != nil {
		t.Fatalf("the stored payload is not the page array: %v", err)
	}
	if len(decoded) != 3 || decoded[0].PUUID != "fixture-puuid-01" || decoded[2].LeaguePoints != 88 {
		t.Fatalf("decoded page = %+v", decoded)
	}
}

func TestWriteStaticAndAccountRoundTrip(t *testing.T) {
	w, root := newTestWriter(t, nil)
	ctx := context.Background()
	payload := []byte(`{"type":"champion","version":"16.20.1","data":{"Ahri":{"key":"103"}}}`)
	if err := w.WriteStatic(ctx, "champions", "16.20.1", "en_US", payload, fetchedAt); err != nil {
		t.Fatalf("WriteStatic: %v", err)
	}
	account := riot.AccountDTO{PUUID: "fixture-puuid-01", GameName: "Fixture", TagLine: "EUW"}
	if err := w.WriteAccount(ctx, account, []byte(`{"puuid":"fixture-puuid-01","gameName":"Fixture","tagLine":"EUW"}`), fetchedAt); err != nil {
		t.Fatalf("WriteAccount: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	staticDir := StaticDir(root, "2026-03-02", "champions")
	paths, err := PartPaths(staticDir)
	if err != nil {
		t.Fatalf("PartPaths: %v", err)
	}
	rows, err := ReadStatic(paths...)
	if err != nil {
		t.Fatalf("ReadStatic: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != "champions" || rows[0].Version != "16.20.1" || rows[0].Locale != "en_US" {
		t.Fatalf("static rows = %+v", rows)
	}
	if rows[0].Payload != string(payload) {
		t.Fatalf("static payload = %q, want the document verbatim", rows[0].Payload)
	}

	accountDir := AccountDir(root, "2026-03-02")
	paths, err = PartPaths(accountDir)
	if err != nil {
		t.Fatalf("PartPaths: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("account parts = %d, want 1", len(paths))
	}
}

func TestWriterPublishesOnlyCompletedParts(t *testing.T) {
	w, root := newTestWriter(t, func(o *Options) { o.RowsPerPart = 2 })
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		id := "EUW1_000000000" + strconv.Itoa(i)
		meta := contract.MatchMeta{MatchID: id, Region: "EUW", FetchedAt: fetchedAt}
		match := decodeSyntheticMatch(t, `{"metadata":{"matchId":"`+id+`"},"info":{"queueId":420}}`)
		if err := w.WriteMatch(ctx, match, meta); err != nil {
			t.Fatalf("WriteMatch: %v", err)
		}
	}
	dir := MatchDir(root, "2026-03-02")

	// Two rows closed a part and it was published; the fifth row is still in
	// memory. A reader that globs the archive therefore never sees a partial
	// part, which is what lets the aggregation read while the crawler writes.
	paths, err := PartPaths(dir)
	if err != nil {
		t.Fatalf("PartPaths: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("parts before Flush = %v, want 2 published and one buffered", paths)
	}
	rows, err := ReadMatches(paths...)
	if err != nil {
		t.Fatalf("ReadMatches: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("published rows = %d, want 4", len(rows))
	}

	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rows, err = ReadMatchPartition(dir)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("rows after Flush = %d, want 5", len(rows))
	}

	// A published part is immutable: the next write opens a new one rather
	// than reopening the file it just named.
	match := decodeSyntheticMatch(t, `{"metadata":{"matchId":"EUW1_0000000099"},"info":{"queueId":420}}`)
	if err := w.WriteMatch(ctx, match, contract.MatchMeta{MatchID: "EUW1_0000000099", Region: "EUW", FetchedAt: fetchedAt}); err != nil {
		t.Fatalf("WriteMatch after Flush: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	paths, err = PartPaths(dir)
	if err != nil {
		t.Fatalf("PartPaths: %v", err)
	}
	if len(paths) != 4 {
		t.Fatalf("parts after the append = %v, want a fourth file", paths)
	}
	rows, err = ReadMatchPartition(dir)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("rows = %d, want 6", len(rows))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.MatchID] = true
	}
	if !seen["EUW1_0000000099"] {
		t.Fatal("the row written after the flush was lost")
	}
}

func TestWriterLeavesNoTemporaryFilesBehind(t *testing.T) {
	w, root := newTestWriter(t, func(o *Options) { o.RowsPerPart = 1 })
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := w.WriteMatch(ctx, decodeSyntheticMatch(t, `{"metadata":{"matchId":"EUW1_0000000000"},"info":{"queueId":420}}`),
			contract.MatchMeta{MatchID: "EUW1_0000000000", Region: "EUW", FetchedAt: fetchedAt}); err != nil {
			t.Fatalf("WriteMatch: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, partTmp) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("temporary part files were left behind: %v", offenders)
	}
	if err := w.WriteMatch(ctx, decodeSyntheticMatch(t, `{"metadata":{"matchId":"x"}}`), contract.MatchMeta{MatchID: "x", FetchedAt: fetchedAt}); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after Close: err = %v, want ErrClosed", err)
	}
}

func TestWriterCreatesNoDirectoryUntilSomethingIsWritten(t *testing.T) {
	w, root := newTestWriter(t, nil)
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("an idle run left %d entries in the archive root", len(entries))
	}
}

func TestWriterRejectsIncompleteInput(t *testing.T) {
	ctx := context.Background()
	w, _ := newTestWriter(t, nil)
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "match without a fetch time",
			call: func() error {
				return w.WriteMatch(ctx, decodeSyntheticMatch(t, `{"metadata":{"matchId":"x"}}`), contract.MatchMeta{MatchID: "x"})
			},
		},
		{
			name: "league page without a fetch time",
			call: func() error {
				return w.WriteLeagueEntries(ctx, nil, contract.LeagueMeta{Region: "EUW"})
			},
		},
		{
			name: "static document without a kind",
			call: func() error {
				return w.WriteStatic(ctx, "", "16.20.1", "en_US", []byte(`{}`), fetchedAt)
			},
		},
		{
			name: "static document without a fetch time",
			call: func() error {
				return w.WriteStatic(ctx, "items", "16.20.1", "en_US", []byte(`{}`), time.Time{})
			},
		},
		{
			name: "account without a fetch time",
			call: func() error {
				return w.WriteAccount(ctx, riot.AccountDTO{}, []byte(`{}`), time.Time{})
			},
		},
		{
			name: "cancelled context",
			call: func() error {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				return w.WriteMatch(cancelled, decodeSyntheticMatch(t, `{"metadata":{"matchId":"x"}}`), contract.MatchMeta{MatchID: "x", FetchedAt: fetchedAt})
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestNewRejectsUnusableOptions(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{name: "no root", opts: Options{}},
		{name: "blank root", opts: Options{Root: "   "}},
		{name: "negative rows per part", opts: Options{Root: t.TempDir(), RowsPerPart: -1}},
		{name: "negative compression", opts: Options{Root: t.TempDir(), CompressionLevel: -1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.opts); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestPartPathsIgnoresForeignEntries(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"part-00001.parquet.zst":     "published",
		"part-00002.parquet.zst.tmp": "in flight",
		"notes.txt":                  "operator note",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	paths, err := PartPaths(dir)
	if err != nil {
		t.Fatalf("PartPaths: %v", err)
	}
	if len(paths) != 1 || filepath.Base(paths[0]) != "part-00001.parquet.zst" {
		t.Fatalf("paths = %v, want only the published part", paths)
	}
	if parts, err := PartPaths(filepath.Join(dir, "absent")); err != nil || parts != nil {
		t.Fatalf("a missing partition = %v, %v, want no paths and no error", parts, err)
	}
}

func TestArchiveKeepsTheGzipDecodedBytes(t *testing.T) {
	// The client decompresses; the archive stores what it received after
	// decompression, so a reader never has to know how the bytes travelled.
	w, root := newTestWriter(t, nil)
	match := fetchedMatch(t, syntheticPayload, true)
	if err := w.WriteMatch(context.Background(), match,
		contract.MatchMeta{MatchID: "EUW1_0000000001", Region: "EUW", FetchedAt: fetchedAt}); err != nil {
		t.Fatalf("WriteMatch: %v", err)
	}
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rows, err := ReadMatchPartition(MatchDir(root, "2026-03-02"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Payload != syntheticPayload {
		t.Fatalf("payload = %q, want the decompressed body", rows[0].Payload)
	}
}

// writeStaleTmp plants the file a crashed writer leaves behind: a part that
// was created and written to, but never flushed, so it was never renamed into
// place.
func writeStaleTmp(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, partDirPerm); err != nil {
		t.Fatalf("create partition: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("a part that was never published"), partDirPerm); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func matchMetaForTest() contract.MatchMeta {
	return contract.MatchMeta{MatchID: "EUW1_0000000001", Region: "EUW", FetchedAt: fetchedAt}
}

// TestStaleTmpFromACrashedWriterDoesNotWedgeThePartition is the regression test
// for the review's finding A. A process that dies between the first WriteMatch
// of a batch and that batch's Flush leaves part-NNNNN.parquet.zst.tmp behind.
// Before the fix every later write to that partition - in that process and in
// every future one - failed with "raw: create part ...: file exists", while
// Step still reported the batch as processed and the queue rows walked retry ->
// dead_letter. After the fix the abandoned index is stepped over, so the next
// payload lands in the partition and a reader sees it.
func TestStaleTmpFromACrashedWriterDoesNotWedgeThePartition(t *testing.T) {
	root := t.TempDir()
	dir := MatchDir(root, fetchedAt.UTC().Format(time.DateOnly))
	// The index a fresh writer would pick, already taken by a dead writer's
	// temporary file, and young enough that it is not reaped: this is the
	// fresh-crash case, the one where an operator restarts the crawler while
	// the partition is still wedged.
	writeStaleTmp(t, dir, "part-00001.parquet.zst.tmp")

	w, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("raw.New: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	ctx := context.Background()
	match := fetchedMatch(t, syntheticPayload, false)
	if err := w.WriteMatch(ctx, match, matchMetaForTest()); err != nil {
		t.Fatalf("WriteMatch on a partition with an abandoned part: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	paths, err := PartPaths(dir)
	if err != nil {
		t.Fatalf("PartPaths: %v", err)
	}
	if len(paths) != 1 || filepath.Base(paths[0]) != "part-00002.parquet.zst" {
		t.Fatalf("published parts = %v, want exactly part-00002.parquet.zst", paths)
	}
	rows, err := ReadMatchPartition(dir)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 1 || rows[0].MatchID != "EUW1_0000000001" {
		t.Fatalf("rows = %v, want the match that was written after the crash", rows)
	}
}

// TestAbandonedTmpIsReapedSoItCannotLitterForever is the other half of finding
// A: stepping over an abandoned part keeps the partition writable, and reaping
// it keeps the directory from growing a leaked file per crash. A part that is
// still young is left alone, because under the archive's one-writer-per-
// partition model a young temporary file can only belong to a writer that is
// still filling it.
func TestAbandonedTmpIsReapedSoItCannotLitterForever(t *testing.T) {
	tests := []struct {
		name       string
		now        time.Time
		wantReaped bool
		wantPart   string
	}{
		{
			name:       "younger than the staleness threshold",
			now:        time.Now(),
			wantReaped: false,
			wantPart:   "part-00002.parquet.zst",
		},
		{
			name:       "older than the staleness threshold",
			now:        time.Now().Add(partTmpStaleAfter + time.Minute),
			wantReaped: true,
			wantPart:   "part-00001.parquet.zst",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := MatchDir(root, fetchedAt.UTC().Format(time.DateOnly))
			stale := writeStaleTmp(t, dir, "part-00001.parquet.zst.tmp")

			w, err := New(Options{Root: root, Now: func() time.Time { return tc.now }})
			if err != nil {
				t.Fatalf("raw.New: %v", err)
			}
			t.Cleanup(func() { _ = w.Close() })
			ctx := context.Background()
			if err := w.WriteMatch(ctx, fetchedMatch(t, syntheticPayload, false), matchMetaForTest()); err != nil {
				t.Fatalf("WriteMatch: %v", err)
			}
			if err := w.Flush(ctx); err != nil {
				t.Fatalf("Flush: %v", err)
			}

			_, statErr := os.Stat(stale)
			if tc.wantReaped && !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("stat %s = %v, want the abandoned part to be gone", stale, statErr)
			}
			if !tc.wantReaped && statErr != nil {
				t.Fatalf("stat %s = %v, want a live writer's part to survive", stale, statErr)
			}
			paths, err := PartPaths(dir)
			if err != nil {
				t.Fatalf("PartPaths: %v", err)
			}
			if len(paths) != 1 || filepath.Base(paths[0]) != tc.wantPart {
				t.Fatalf("published parts = %v, want exactly %s", paths, tc.wantPart)
			}
		})
	}
}

// TestPartIndicesKeepAdvancingPastRepeatedAbandonedParts covers the restart
// after restart case: several crashes leave several temporary parts, and the
// writer must still find a free index rather than stopping at the first one.
func TestPartIndicesKeepAdvancingPastRepeatedAbandonedParts(t *testing.T) {
	root := t.TempDir()
	dir := MatchDir(root, fetchedAt.UTC().Format(time.DateOnly))
	for _, name := range []string{
		"part-00001.parquet.zst.tmp",
		"part-00002.parquet.zst.tmp",
		"part-00003.parquet.zst.tmp",
	} {
		writeStaleTmp(t, dir, name)
	}

	w, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("raw.New: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	ctx := context.Background()
	if err := w.WriteMatch(ctx, fetchedMatch(t, syntheticPayload, false), matchMetaForTest()); err != nil {
		t.Fatalf("WriteMatch: %v", err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	paths, err := PartPaths(dir)
	if err != nil {
		t.Fatalf("PartPaths: %v", err)
	}
	if len(paths) != 1 || filepath.Base(paths[0]) != "part-00004.parquet.zst" {
		t.Fatalf("published parts = %v, want exactly part-00004.parquet.zst", paths)
	}
}
