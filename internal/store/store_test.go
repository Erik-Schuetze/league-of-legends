package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/sql/migrations"
)

// The store is a small amount of SQL and a large amount of policy about when
// that SQL may run. The policy is what these tests are for: that a duplicate
// match cannot be re-inserted, that a claim reserves a row for exactly one
// worker, that a state change guarded by the state it expects does nothing when
// the state has moved on, and that every statement is reachable only through
// arguments the caller actually derived.
//
// They run against a mock driver because Postgres is not part of this
// environment; TestStoreAgainstRealPostgres exercises the same paths for real
// whenever LOLSTATS_TEST_DB_DSN points at a reachable database. The mock pins
// the SQL text and the arguments, which is the half a database cannot check:
// a real server happily accepts a query with the wrong WHERE clause.

var testNow = time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)

// wsRun mirrors sqlmock's own normalisation, so an expectation can be written by
// quoting the constant the production code uses instead of retyping it.
var wsRun = regexp.MustCompile(`\s+`)

func sqlPattern(query string) string {
	return regexp.QuoteMeta(strings.TrimSpace(wsRun.ReplaceAllString(query, " ")))
}

// recorder counts what the store reports. The store's only observable effect
// other than SQL is instrumentation, so a test that does not check it cannot
// tell "recorded nothing" from "recorded the wrong thing".
type recorder struct {
	queueClaimed     int
	matchesPersisted int
	frontierSize     int
}

func (r *recorder) ObserveRiotRequest(string, int, float64) {}
func (r *recorder) IncRiotRetry(string, string)             {}
func (r *recorder) SetRiotKeyAge(float64)                   {}
func (r *recorder) AddQueueClaimed(n int)                   { r.queueClaimed += n }
func (r *recorder) AddMatchesPersisted(n int)               { r.matchesPersisted += n }
func (r *recorder) AddRawBytesWritten(int64)                {}
func (r *recorder) SetFrontierSize(n int)                   { r.frontierSize = n }
func (r *recorder) SetPipelineStaleness(string, float64)    {}
func (r *recorder) ObserveBuildDuration(float64)            {}
func (r *recorder) AddCellsPublished(int)                   {}
func (r *recorder) AddCellsSuppressed(int)                  {}
func (r *recorder) IncBuildFailure(string)                  {}

func newTestStore(t *testing.T, mutate func(*Options)) (*Store, sqlmock.Sqlmock, *recorder) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet SQL expectations: %v", err)
		}
		_ = db.Close()
	})
	rec := &recorder{}
	opts := Options{Metrics: rec, Now: func() time.Time { return testNow }}
	if mutate != nil {
		mutate(&opts)
	}
	return newStore(db, opts), mock, rec
}

func TestUpsertMatchReportsInsertedOnlyOnTheFirstWrite(t *testing.T) {
	s, mock, rec := newTestStore(t, nil)
	rec0 := contract.MatchRecord{
		MatchID:        "EUW1_0000000001",
		QueueID:        420,
		Patch:          "15.1",
		GameVersion:    "15.1.1.1",
		GameCreation:   testNow.Add(-30 * time.Minute),
		GameDurationS:  1830,
		PayloadVersion: "2",
		RawURI:         "raw/riot/match-v5/dt=2026-03-02/region=EUW/part-00001.parquet.zst",
		FetchedAt:      testNow,
	}
	args := []driver.Value{
		rec0.MatchID, "EUW", rec0.QueueID, rec0.Patch, rec0.GameVersion, rec0.GameCreation.UTC(),
		rec0.GameDurationS, 2, rec0.RawURI, string(contract.MatchFetched), rec0.FetchedAt.UTC(),
		nil, nil,
	}

	mock.ExpectQuery(sqlPattern(upsertMatchSQL)).WithArgs(args...).
		WillReturnRows(sqlmock.NewRows([]string{"match_id"}).AddRow(rec0.MatchID))

	inserted, err := s.UpsertMatch(context.Background(), rec0)
	if err != nil {
		t.Fatalf("UpsertMatch: %v", err)
	}
	if !inserted {
		t.Fatal("first write reported inserted=false, want true")
	}
	if rec.matchesPersisted != 1 {
		t.Fatalf("matches persisted = %d, want 1", rec.matchesPersisted)
	}

	// ON CONFLICT DO NOTHING returns no row. That is the dedupe signal, and it
	// must be reported as a successful no-op rather than as an error: the
	// archive already holds the payload, so nothing is wrong.
	mock.ExpectQuery(sqlPattern(upsertMatchSQL)).WithArgs(args...).WillReturnError(sql.ErrNoRows)
	inserted, err = s.UpsertMatch(context.Background(), rec0)
	if err != nil {
		t.Fatalf("second UpsertMatch: %v", err)
	}
	if inserted {
		t.Fatal("second write reported inserted=true, want false")
	}
	if rec.matchesPersisted != 1 {
		t.Fatalf("matches persisted = %d after a duplicate, want 1", rec.matchesPersisted)
	}
}

func TestUpsertMatchRejectsIncompleteRecords(t *testing.T) {
	cases := []struct {
		name string
		rec  contract.MatchRecord
	}{
		{"no match id", contract.MatchRecord{RawURI: "raw/x.parquet.zst"}},
		{"no raw uri", contract.MatchRecord{MatchID: "EUW1_1"}},
		{"non numeric payload version", contract.MatchRecord{MatchID: "EUW1_1", RawURI: "raw/x", PayloadVersion: "v2"}},
		{"payload version out of range", contract.MatchRecord{MatchID: "EUW1_1", RawURI: "raw/x", PayloadVersion: "99999999"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// No expectation is registered: the test fails if a statement is
			// sent at all, which is the point of validating before writing.
			s, _, _ := newTestStore(t, nil)
			if _, err := s.UpsertMatch(context.Background(), tc.rec); err == nil {
				t.Fatal("UpsertMatch accepted a record it should have rejected")
			}
		})
	}
}

func TestUpsertMatchSurfacesDatabaseErrors(t *testing.T) {
	s, mock, _ := newTestStore(t, nil)
	mock.ExpectQuery(sqlPattern(upsertMatchSQL)).WillReturnError(errors.New("connection reset"))

	_, err := s.UpsertMatch(context.Background(), contract.MatchRecord{MatchID: "EUW1_1", RawURI: "raw/x"})
	if err == nil {
		t.Fatal("UpsertMatch swallowed a database error")
	}
	if !strings.Contains(err.Error(), "EUW1_1") {
		t.Fatalf("error does not name the match: %v", err)
	}
}

func TestPayloadVersionAcceptsOnlyDigits(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"", 1, false},
		{"1", 1, false},
		{"02", 2, false},
		{"v2", 0, true},
		{"-1", 0, true},
		{"9999999999", 0, true},
	}
	for _, tc := range cases {
		got, err := payloadVersion(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("payloadVersion(%q) = %d, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("payloadVersion(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("payloadVersion(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestClaimJobsSkipsLockedRowsAndReturnsUrgentFirst(t *testing.T) {
	if !strings.Contains(claimJobsSQL, "FOR UPDATE SKIP LOCKED") {
		t.Fatal("claimJobsSQL lost its SKIP LOCKED clause: two workers would claim the same row")
	}
	s, mock, rec := newTestStore(t, nil)

	claimedAt := testNow.Add(-time.Second)
	rows := sqlmock.NewRows([]string{"id", "match_id", "priority", "attempts", "not_before", "claimed_at", "status"}).
		AddRow(3, "EUW1_3", 200, 1, testNow, claimedAt, "claimed").
		AddRow(1, "EUW1_1", 0, 0, testNow, claimedAt, "claimed").
		AddRow(2, "EUW1_2", 100, 2, testNow.Add(-time.Hour), claimedAt, "claimed")
	mock.ExpectQuery(sqlPattern(claimJobsSQL)).
		WithArgs(testNow.UTC(), 20, string(contract.JobClaimed)).
		WillReturnRows(rows)

	items, err := s.ClaimJobs(context.Background(), 20, testNow)
	if err != nil {
		t.Fatalf("ClaimJobs: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("claimed %d jobs, want 3", len(items))
	}
	// RETURNING does not promise an order and the statement's order is the whole
	// rate-limit policy, so the caller must not depend on the driver's.
	wantOrder := []string{"EUW1_1", "EUW1_2", "EUW1_3"}
	for i, want := range wantOrder {
		if items[i].MatchID != want {
			t.Fatalf("claim order = %v, want %v", []string{items[0].MatchID, items[1].MatchID, items[2].MatchID}, wantOrder)
		}
	}
	if items[0].Status != contract.JobClaimed || items[0].ClaimedAt.IsZero() {
		t.Fatalf("claimed item = %+v, want a claimed row with a claim time", items[0])
	}
	if rec.queueClaimed != 3 {
		t.Fatalf("queue claimed metric = %d, want 3", rec.queueClaimed)
	}
}

func TestClaimJobsClampsItsLimit(t *testing.T) {
	t.Run("zero means no work and no query", func(t *testing.T) {
		s, _, rec := newTestStore(t, nil)
		items, err := s.ClaimJobs(context.Background(), 0, testNow)
		if err != nil || items != nil {
			t.Fatalf("ClaimJobs(0) = %v, %v; want nil, nil", items, err)
		}
		if rec.queueClaimed != 0 {
			t.Fatalf("queue claimed = %d, want 0", rec.queueClaimed)
		}
	})
	t.Run("an oversized limit is capped", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectQuery(sqlPattern(claimJobsSQL)).
			WithArgs(testNow.UTC(), maxClaimLimit, string(contract.JobClaimed)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "match_id", "priority", "attempts", "not_before", "claimed_at", "status"}))
		items, err := s.ClaimJobs(context.Background(), maxClaimLimit*10, testNow)
		if err != nil {
			t.Fatalf("ClaimJobs: %v", err)
		}
		if len(items) != 0 {
			t.Fatalf("claimed = %v, want none", items)
		}
	})
}

func TestJobStateChangesAreGuardedByTheStateTheyExpect(t *testing.T) {
	// Zero rows affected is the normal outcome of every one of these: the job
	// was reclaimed by maintenance or finished by another worker. An error here
	// would turn an ordinary race into an incident.
	for _, sqlText := range []string{completeJobSQL, retryJobSQL, deadLetterJobSQL} {
		if !strings.Contains(sqlText, "status = $") {
			t.Fatalf("statement lost its status guard:\n%s", sqlText)
		}
	}

	t.Run("complete", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectExec(sqlPattern(completeJobSQL)).
			WithArgs(int64(7), string(contract.JobDone), string(contract.JobClaimed)).
			WillReturnResult(sqlmock.NewResult(0, 0))
		if err := s.CompleteJob(context.Background(), 7); err != nil {
			t.Fatalf("CompleteJob: %v", err)
		}
	})
	t.Run("retry records the cause", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		notBefore := testNow.Add(90 * time.Second)
		mock.ExpectExec(sqlPattern(retryJobSQL)).
			WithArgs(int64(7), string(contract.JobRetry), notBefore.UTC(), "riot: status 503", string(contract.JobClaimed)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := s.RetryJob(context.Background(), 7, notBefore, "riot: status 503"); err != nil {
			t.Fatalf("RetryJob: %v", err)
		}
	})
	t.Run("retry with no cause writes NULL", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectExec(sqlPattern(retryJobSQL)).
			WithArgs(int64(7), string(contract.JobRetry), testNow.UTC(), nil, string(contract.JobClaimed)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := s.RetryJob(context.Background(), 7, testNow, ""); err != nil {
			t.Fatalf("RetryJob: %v", err)
		}
	})
	t.Run("dead letter stamps the retirement time", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectExec(sqlPattern(deadLetterJobSQL)).
			WithArgs(int64(9), string(contract.JobDead), testNow.UTC(), "riot: 404 for match", string(contract.JobClaimed)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := s.DeadLetterJob(context.Background(), 9, "riot: 404 for match"); err != nil {
			t.Fatalf("DeadLetterJob: %v", err)
		}
	})
	t.Run("errors are wrapped with the id", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectExec(sqlPattern(completeJobSQL)).WillReturnError(errors.New("deadlock detected"))
		err := s.CompleteJob(context.Background(), 7)
		if err == nil || !strings.Contains(err.Error(), "7") {
			t.Fatalf("CompleteJob error = %v, want the job id in the message", err)
		}
	})
}

// enqueueSQL rebuilds the statement EnqueueMatches assembles. Reproducing the
// text is the point: an accidental change to the conflict clause or the column
// list is a change to the dedupe guarantee, and it should fail here rather than
// in production as a duplicated fetch.
//
// That clause is a DO UPDATE ... WHERE status = 'dead' rather than a DO NOTHING,
// because DO NOTHING is what made a dead-lettered match id unreachable forever:
// re-discovering the match could not put it back in the queue. It is also
// bounded by the row's revival budget, because unbounded it is not a recovery
// path but a loop - see revivalBudget. The status and the budget are passed as
// the last two parameters, after the five per row.
func enqueueSQL(rows int) string {
	var sb strings.Builder
	sb.WriteString("INSERT INTO fetch_queue (match_id, priority, attempts, not_before, status) VALUES ")
	for i := 0; i < rows; i++ {
		if i > 0 {
			sb.WriteString(", ")
		}
		base := i * 5
		sb.WriteString("($")
		sb.WriteString(strconvI(base + 1))
		sb.WriteString(", $")
		sb.WriteString(strconvI(base + 2))
		sb.WriteString(", $")
		sb.WriteString(strconvI(base + 3))
		sb.WriteString(", $")
		sb.WriteString(strconvI(base + 4))
		sb.WriteString(", $")
		sb.WriteString(strconvI(base + 5))
		sb.WriteString(")")
	}
	sb.WriteString(" ON CONFLICT (match_id) DO UPDATE SET ")
	sb.WriteString(reviveDeadRowSet)
	sb.WriteString(" WHERE ")
	sb.WriteString(reviveDeadRowWhere(rows*5+1, rows*5+2))
	return sb.String()
}

func strconvI(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return strconvI(n/10) + string(rune('0'+n%10))
}

func TestEnqueueMatchesDedupesInsideOneBatch(t *testing.T) {
	// A PUUID's history overlaps every other participant's, so the same match id
	// arrives repeatedly by design. One statement cannot insert the same key
	// twice, so the batch must arrive collapsed.
	s, mock, _ := newTestStore(t, nil)
	items := []contract.QueueItem{
		{MatchID: "EUW1_1", Priority: 200},
		{MatchID: "", Priority: 100},
		{MatchID: "EUW1_2", Priority: 100},
		{MatchID: "EUW1_1", Priority: 0},
	}
	mock.ExpectExec(sqlPattern(enqueueSQL(2))).
		WithArgs("EUW1_1", 0, 0, testNow.UTC(), string(contract.JobPending),
			"EUW1_2", 100, 0, testNow.UTC(), string(contract.JobPending),
			string(contract.JobDead), revivalBudget).
		WillReturnResult(sqlmock.NewResult(0, 1))

	added, err := s.EnqueueMatches(context.Background(), items)
	if err != nil {
		t.Fatalf("EnqueueMatches: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1 (the second row lost the conflict)", added)
	}
}

func TestEnqueueMatchesChunksLargeBatchesAndKeepsAnExplicitNotBefore(t *testing.T) {
	const total = enqueueBatch + 3
	items := make([]contract.QueueItem, 0, total)
	for i := 0; i < total; i++ {
		items = append(items, contract.QueueItem{
			MatchID:   "EUW1_" + strconvI(i),
			Priority:  100,
			NotBefore: testNow.Add(time.Minute),
		})
	}
	s, mock, _ := newTestStore(t, nil)
	mock.ExpectExec(sqlPattern(enqueueSQL(enqueueBatch))).WillReturnResult(sqlmock.NewResult(0, enqueueBatch))
	mock.ExpectExec(sqlPattern(enqueueSQL(3))).WillReturnResult(sqlmock.NewResult(0, 3))

	added, err := s.EnqueueMatches(context.Background(), items)
	if err != nil {
		t.Fatalf("EnqueueMatches: %v", err)
	}
	if added != total {
		t.Fatalf("added = %d, want %d", added, total)
	}
}

func TestEnqueueMatchesWithNothingToDoDoesNotQuery(t *testing.T) {
	s, _, _ := newTestStore(t, nil)
	added, err := s.EnqueueMatches(context.Background(), nil)
	if err != nil || added != 0 {
		t.Fatalf("EnqueueMatches(nil) = %d, %v; want 0, nil", added, err)
	}
}

// frontierSQL rebuilds the frontier upsert, for the same reason enqueueSQL does:
// the ON CONFLICT clause here decides whether a dead PUUID stays dead, and
// whether a re-discovery counts as a discovery.
func frontierSQL(rows int) string {
	var sb strings.Builder
	sb.WriteString("INSERT INTO crawl_frontier (puuid, region, seed_tier, seed_division, last_seen_at, last_fetched_at, consecutive_empty, priority, dead) VALUES ")
	for i := 0; i < rows; i++ {
		if i > 0 {
			sb.WriteString(", ")
		}
		base := i * 6
		sb.WriteString("($")
		sb.WriteString(strconvI(base + 1))
		sb.WriteString(", $")
		sb.WriteString(strconvI(base + 2))
		sb.WriteString(", $")
		sb.WriteString(strconvI(base + 3))
		sb.WriteString(", $")
		sb.WriteString(strconvI(base + 4))
		sb.WriteString(", $")
		sb.WriteString(strconvI(base + 5))
		sb.WriteString(", NULL, 0, $")
		sb.WriteString(strconvI(base + 6))
		sb.WriteString(", false)")
	}
	sb.WriteString(" ON CONFLICT (puuid) DO UPDATE SET region = EXCLUDED.region, seed_tier = EXCLUDED.seed_tier,")
	sb.WriteString(" seed_division = EXCLUDED.seed_division,")
	sb.WriteString(" last_seen_at = GREATEST(crawl_frontier.last_seen_at, EXCLUDED.last_seen_at),")
	sb.WriteString(" priority = LEAST(crawl_frontier.priority, EXCLUDED.priority) RETURNING (xmax = 0)")
	return sb.String()
}

func TestUpsertFrontierCountsOnlyNewPUUIDs(t *testing.T) {
	s, mock, _ := newTestStore(t, nil)
	mock.ExpectQuery(sqlPattern(frontierSQL(2))).
		WithArgs("puuid-a", "EUW", "GOLD", "I", testNow.UTC(), 100,
			"puuid-b", "NA", "", "", testNow.Add(-time.Hour).UTC(), 200).
		WillReturnRows(sqlmock.NewRows([]string{"inserted"}).AddRow(true).AddRow(false))

	added, err := s.UpsertFrontier(context.Background(), []contract.FrontierEntry{
		{PUUID: "puuid-a", SeedTier: "GOLD", SeedDivision: "I", Priority: 100},
		{PUUID: "puuid-b", Region: "NA", LastSeenAt: testNow.Add(-time.Hour), Priority: 200},
		{PUUID: "puuid-a"},
	})
	if err != nil {
		t.Fatalf("UpsertFrontier: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1 (only the row Postgres reported as inserted)", added)
	}
}

func TestClaimFrontierUsesSkipLockedAndHonoursTheCooldown(t *testing.T) {
	if !strings.Contains(claimFrontierSQL, "FOR UPDATE SKIP LOCKED") {
		t.Fatal("claimFrontierSQL lost its SKIP LOCKED clause: two workers would walk the same player")
	}
	s, mock, _ := newTestStore(t, nil)
	previous := testNow.Add(-time.Hour)
	rows := sqlmock.NewRows([]string{
		"puuid", "region", "seed_tier", "seed_division", "consecutive_empty", "priority", "dead",
		"picked_last_fetched_at", "last_seen_at",
	}).
		AddRow("puuid-seen", "EUW", "GOLD", "I", 0, 100, false, previous, previous).
		AddRow("puuid-new", "EUW", "", "", 0, 100, false, nil, testNow)

	mock.ExpectQuery(sqlPattern(claimFrontierSQL)).
		WithArgs(testNow.UTC(), 2, defaultFrontierCooldown.Seconds()).
		WillReturnRows(rows)

	entries, err := s.ClaimFrontier(context.Background(), 2, testNow)
	if err != nil {
		t.Fatalf("ClaimFrontier: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("claimed %d entries, want 2", len(entries))
	}
	// A never-walked PUUID is the most valuable work there is, so it must sort
	// ahead of one that was walked an hour ago.
	if entries[0].PUUID != "puuid-new" {
		t.Fatalf("claim order = %s, %s; want the never-fetched entry first", entries[0].PUUID, entries[1].PUUID)
	}
}

func TestFrontierBookkeeping(t *testing.T) {
	t.Run("fetched, empty", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectExec(sqlPattern(markFrontierFetchedSQL)).
			WithArgs("puuid-a", testNow.UTC(), true).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := s.MarkFrontierFetched(context.Background(), "puuid-a", testNow, true); err != nil {
			t.Fatalf("MarkFrontierFetched: %v", err)
		}
	})
	t.Run("dead with no cause writes NULL", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectExec(sqlPattern(markFrontierDeadSQL)).
			WithArgs("puuid-a", nil).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := s.MarkFrontierDead(context.Background(), "puuid-a", ""); err != nil {
			t.Fatalf("MarkFrontierDead: %v", err)
		}
	})
	t.Run("prune reports the rows it removed", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		before := testNow.Add(-30 * 24 * time.Hour)
		mock.ExpectExec(sqlPattern(pruneFrontierSQL)).
			WithArgs(before.UTC(), 5).
			WillReturnResult(sqlmock.NewResult(0, 4))
		n, err := s.PruneFrontier(context.Background(), before, 5)
		if err != nil {
			t.Fatalf("PruneFrontier: %v", err)
		}
		if n != 4 {
			t.Fatalf("pruned = %d, want 4", n)
		}
	})
	t.Run("sizes", func(t *testing.T) {
		s, mock, rec := newTestStore(t, nil)
		mock.ExpectQuery(sqlPattern("SELECT count(*) FROM crawl_frontier WHERE dead = false")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(17))
		if n, err := s.FrontierSize(context.Background()); err != nil || n != 17 {
			t.Fatalf("FrontierSize = %d, %v; want 17, nil", n, err)
		}
		if rec.frontierSize != 17 {
			t.Fatalf("frontier size metric = %d, want 17", rec.frontierSize)
		}
		mock.ExpectQuery(sqlPattern("SELECT count(*) FROM crawl_frontier WHERE dead = true")).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
		if n, err := s.DeadFrontierSize(context.Background()); err != nil || n != 3 {
			t.Fatalf("DeadFrontierSize = %d, %v; want 3, nil", n, err)
		}
	})
}

func TestMaintenanceStatements(t *testing.T) {
	t.Run("reset stuck claims", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		olderThan := testNow.Add(-15 * time.Minute)
		mock.ExpectExec(sqlPattern(resetStuckClaimsSQL)).
			WithArgs(olderThan.UTC(), "retry", testNow.UTC(), claimTimeoutCause, "claimed", 50).
			WillReturnResult(sqlmock.NewResult(0, 2))
		n, err := s.ResetStuckClaims(context.Background(), olderThan, 50)
		if err != nil {
			t.Fatalf("ResetStuckClaims: %v", err)
		}
		if n != 2 {
			t.Fatalf("reclaimed = %d, want 2", n)
		}
	})
	t.Run("a zero limit reclaims nothing", func(t *testing.T) {
		s, _, _ := newTestStore(t, nil)
		n, err := s.ResetStuckClaims(context.Background(), testNow, 0)
		if err != nil || n != 0 {
			t.Fatalf("ResetStuckClaims(0) = %d, %v; want 0, nil", n, err)
		}
	})
	t.Run("recompute priority", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectExec(sqlPattern(recomputeFrontierPrioritySQL)).WillReturnResult(sqlmock.NewResult(0, 5))
		n, err := s.RecomputeFrontierPriority(context.Background())
		if err != nil || n != 5 {
			t.Fatalf("RecomputeFrontierPriority = %d, %v; want 5, nil", n, err)
		}
	})
	t.Run("queue depths", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectQuery(sqlPattern(queueDepthsSQL)).
			WillReturnRows(sqlmock.NewRows([]string{"status", "count"}).
				AddRow("pending", 120).AddRow("retry", 7).AddRow("dead", 3))
		depths, err := s.QueueDepths(context.Background())
		if err != nil {
			t.Fatalf("QueueDepths: %v", err)
		}
		want := map[contract.JobStatus]int{contract.JobPending: 120, contract.JobRetry: 7, contract.JobDead: 3}
		for status, n := range want {
			if depths[status] != n {
				t.Fatalf("depth[%s] = %d, want %d", status, depths[status], n)
			}
		}
	})
	t.Run("queue oldest", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectQuery(sqlPattern(queueOldestSQL)).
			WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(testNow.Add(-2 * time.Hour)))
		oldest, ok, err := s.QueueOldest(context.Background())
		if err != nil || !ok {
			t.Fatalf("QueueOldest = %s, %v, %v; want a time and true", oldest, ok, err)
		}
		mock.ExpectQuery(sqlPattern(queueOldestSQL)).
			WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(nil))
		oldest, ok, err = s.QueueOldest(context.Background())
		if err != nil {
			t.Fatalf("QueueOldest on an empty queue: %v", err)
		}
		if ok || !oldest.IsZero() {
			t.Fatalf("QueueOldest on an empty queue = %s, %v; want the zero time and false", oldest, ok)
		}
	})
	t.Run("newest fetched", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectQuery(sqlPattern(newestFetchedAtSQL)).
			WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(testNow.Add(-time.Minute)))
		newest, ok, err := s.NewestFetchedAt(context.Background())
		if err != nil || !ok {
			t.Fatalf("NewestFetchedAt = %s, %v, %v", newest, ok, err)
		}
		mock.ExpectQuery(sqlPattern(newestFetchedAtSQL)).
			WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
		if _, ok, err = s.NewestFetchedAt(context.Background()); err != nil || ok {
			t.Fatalf("NewestFetchedAt on an empty pipeline = %v, %v; want false, nil", ok, err)
		}
	})
}

// A dead letter used to be terminal: nothing selected status = 'dead' and the
// enqueue conflict clause was DO NOTHING, so a match id retired by a global
// outage could never be re-queued - not by replay, not by re-discovery, not by
// hand. Both halves of the recovery path are asserted here.
func TestDeadLetteredWorkIsRecoverable(t *testing.T) {
	t.Run("re-discovering a retired match revives its row", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectExec(sqlPattern(enqueueSQL(1))).
			WithArgs("EUW1_1", 100, 0, testNow.UTC(), string(contract.JobPending),
				string(contract.JobDead), revivalBudget).
			WillReturnResult(sqlmock.NewResult(0, 0))
		if _, err := s.EnqueueMatches(context.Background(), []contract.QueueItem{{MatchID: "EUW1_1", Priority: 100}}); err != nil {
			t.Fatalf("EnqueueMatches: %v", err)
		}
		// The parameter is the whole guarantee: the conflict clause only
		// touches rows in this state, so a pending or claimed row keeps its
		// attempts and its deadline.
		if !strings.Contains(enqueueSQL(1), "WHERE fetch_queue.status = $6") {
			t.Fatal("the enqueue conflict clause no longer guards on the retired status")
		}
		// The second half of the guard is the revival budget, and it is what
		// makes the revival terminate: a row that is rediscovered by every walk
		// of every player who played it would otherwise be re-queued forever,
		// each revival restarting the attempt budget that exists to retire it.
		if !strings.Contains(enqueueSQL(1), "fetch_queue.revivals < $7") {
			t.Fatal("the enqueue conflict clause revives a dead row regardless of how often it has already been revived")
		}
		if revivalBudget < 1 {
			t.Fatalf("revivalBudget = %d, want at least one revival so a transient outage is still recoverable", revivalBudget)
		}
	})
	t.Run("replay returns retired rows to pending with a fresh budget", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectExec(sqlPattern(replayDeadLetteredSQL)).
			WithArgs(string(contract.JobPending), testNow.UTC(), replayedDeadLetterCause, string(contract.JobDead), 25).
			WillReturnResult(sqlmock.NewResult(0, 4))
		replayed, err := s.ReplayDeadLettered(context.Background(), 25)
		if err != nil {
			t.Fatalf("ReplayDeadLettered: %v", err)
		}
		if replayed != 4 {
			t.Fatalf("replayed = %d, want the 4 rows Postgres reported", replayed)
		}
	})
	t.Run("a zero limit replays nothing and does not query", func(t *testing.T) {
		s, _, _ := newTestStore(t, nil)
		replayed, err := s.ReplayDeadLettered(context.Background(), 0)
		if err != nil || replayed != 0 {
			t.Fatalf("ReplayDeadLettered(0) = %d, %v; want 0, nil", replayed, err)
		}
	})
	t.Run("releasing a claim refunds the attempt", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		notBefore := testNow.Add(30 * time.Second)
		mock.ExpectExec(sqlPattern(releaseJobSQL)).
			WithArgs(int64(7), string(contract.JobRetry), notBefore.UTC(), "riot: circuit breaker open", string(contract.JobClaimed)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := s.ReleaseJob(context.Background(), 7, notBefore, "riot: circuit breaker open"); err != nil {
			t.Fatalf("ReleaseJob: %v", err)
		}
		if !strings.Contains(releaseJobSQL, "GREATEST(attempts - 1, 0)") {
			t.Fatal("release no longer refunds the attempt, so an outage can still retire the backlog")
		}
	})
}

func TestSeedAndBuildRunsAreAuditedFromStartToFinish(t *testing.T) {
	t.Run("seed run", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectQuery(sqlPattern(startSeedRunSQL)).
			WithArgs("GOLD", "I", "RANKED_SOLO_5x5", "EUW", testNow.UTC()).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
		id, err := s.StartSeedRun(context.Background(), contract.SeedRun{Tier: "GOLD", Division: "I", Queue: "RANKED_SOLO_5x5", StartedAt: testNow})
		if err != nil {
			t.Fatalf("StartSeedRun: %v", err)
		}
		if id != 41 {
			t.Fatalf("seed run id = %d, want 41", id)
		}
		// A seeding pass that dies mid-ladder must be visible as an unfinished
		// row, so the finishing update is a separate statement.
		mock.ExpectExec(sqlPattern(finishSeedRunSQL)).
			WithArgs(int64(41), testNow.Add(time.Minute).UTC(), 1000).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := s.FinishSeedRun(context.Background(), 41, testNow.Add(time.Minute), 1000); err != nil {
			t.Fatalf("FinishSeedRun: %v", err)
		}
	})
	t.Run("seed run needs a ladder position", func(t *testing.T) {
		s, _, _ := newTestStore(t, nil)
		if _, err := s.StartSeedRun(context.Background(), contract.SeedRun{Division: "I"}); err == nil {
			t.Fatal("StartSeedRun accepted a run with no tier")
		}
	})
	t.Run("build run", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectQuery(sqlPattern(startBuildRunSQL)).
			WithArgs("15.1", "EUW", 420, "GOLD_I", testNow.UTC(), "abc123").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
		id, err := s.StartBuildRun(context.Background(), contract.BuildRun{Patch: "15.1", Queue: 420, Bracket: "GOLD_I", StartedAt: testNow, GitSHA: "abc123"})
		if err != nil {
			t.Fatalf("StartBuildRun: %v", err)
		}
		if id != 9 {
			t.Fatalf("build run id = %d, want 9", id)
		}
		mock.ExpectExec(sqlPattern(finishBuildRunSQL)).
			WithArgs(int64(9), testNow.Add(time.Minute).UTC(), "succeeded", 120, 100, 20, "s3://bucket/build.parquet", nil).
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := s.FinishBuildRun(context.Background(), 9, contract.BuildResult{
			Status: "ok", FinishedAt: testNow.Add(time.Minute),
			CellsTotal: 120, CellsPublished: 100, CellsSuppressed: 20,
			ArtifactURI: "s3://bucket/build.parquet",
		}); err != nil {
			t.Fatalf("FinishBuildRun: %v", err)
		}
	})
	t.Run("a quarantined build is recorded as failed, never as succeeded", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectExec(sqlPattern(finishBuildRunSQL)).
			WithArgs(int64(9), testNow.UTC(), "failed", 0, 0, 0, nil, "gate: rank cells suppressed").
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := s.FinishBuildRun(context.Background(), 9, contract.BuildResult{
			Status: "quarantined", FinishedAt: testNow, Err: "gate: rank cells suppressed",
		}); err != nil {
			t.Fatalf("FinishBuildRun: %v", err)
		}
	})
	t.Run("unknown build status is rejected", func(t *testing.T) {
		s, _, _ := newTestStore(t, nil)
		if err := s.FinishBuildRun(context.Background(), 9, contract.BuildResult{Status: "maybe"}); err == nil {
			t.Fatal("FinishBuildRun accepted an unknown status")
		}
	})
}

func TestSourceTogglesRequireANameAndDefaultToOff(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		due := testNow.Add(90 * 24 * time.Hour)
		mock.ExpectExec(sqlPattern(setSourceToggleSQL)).
			WithArgs("ddragon", true, "erik", testNow.UTC(), due.UTC(), "static data is permissively licensed").
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := s.SetSourceToggle(context.Background(), contract.SourceToggle{
			Source: "ddragon", Enabled: true, DecidedBy: "erik", DecidedAt: testNow,
			ReviewDueAt: due, Notes: "static data is permissively licensed",
		}); err != nil {
			t.Fatalf("SetSourceToggle: %v", err)
		}
	})
	t.Run("an unnamed decision is refused", func(t *testing.T) {
		s, _, _ := newTestStore(t, nil)
		if err := s.SetSourceToggle(context.Background(), contract.SourceToggle{Source: "ddragon"}); err == nil {
			t.Fatal("SetSourceToggle accepted a toggle with no decided_by")
		}
	})
	t.Run("list", func(t *testing.T) {
		s, mock, _ := newTestStore(t, nil)
		mock.ExpectQuery(sqlPattern(sourceTogglesSQL)).
			WillReturnRows(sqlmock.NewRows([]string{"source", "enabled", "decided_by", "decided_at", "review_due_at", "notes"}).
				AddRow("ddragon", true, "erik", testNow, nil, nil))
		toggles, err := s.SourceToggles(context.Background())
		if err != nil {
			t.Fatalf("SourceToggles: %v", err)
		}
		if len(toggles) != 1 || toggles[0].Source != "ddragon" || !toggles[0].Enabled {
			t.Fatalf("toggles = %+v, want one enabled ddragon row", toggles)
		}
		if !toggles[0].ReviewDueAt.IsZero() || toggles[0].Notes != "" {
			t.Fatalf("nullable columns = %+v, want zero values", toggles[0])
		}
	})
}

func TestOpenRefusesToStartWithoutADSN(t *testing.T) {
	if _, err := Open(context.Background(), Options{}); err == nil {
		t.Fatal("Open accepted an empty DSN")
	}
}

// The startup connect against an unreachable database is covered in
// connect_test.go: it retries for a bounded window and then gives up loudly,
// which is the behaviour a single fail-fast test asserted the opposite of.

func TestPingFailsOnceTheStoreIsClosed(t *testing.T) {
	s, mock, _ := newTestStore(t, nil)
	mock.ExpectClose()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("Ping succeeded on a closed store")
	}
}

func TestOptionsDefaultsAreConservative(t *testing.T) {
	s, _, _ := newTestStore(t, nil)
	if s.Region() != defaultRegion {
		t.Fatalf("region = %q, want %q", s.Region(), defaultRegion)
	}
	if s.opts.MaxConns != defaultMaxConns {
		t.Fatalf("max conns = %d, want %d", s.opts.MaxConns, defaultMaxConns)
	}
	if s.opts.FrontierCooldown != defaultFrontierCooldown {
		t.Fatalf("frontier cooldown = %s, want %s", s.opts.FrontierCooldown, defaultFrontierCooldown)
	}
	if got := s.Now(); !got.Equal(testNow) {
		t.Fatalf("Now() = %s, want the injected clock", got)
	}
}

func TestEmbeddedMigrationsAreOrderedUniqueAndChecksummed(t *testing.T) {
	files, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(files) < 2 {
		t.Fatalf("embedded migrations = %d, want at least the initial schema and the ingest additions", len(files))
	}
	if files[0].version != 1 {
		t.Fatalf("first migration = %d, want 1", files[0].version)
	}
	for i, f := range files {
		if i > 0 && f.version <= files[i-1].version {
			t.Fatalf("migration %d is not after %d: the applied order would be ambiguous", f.version, files[i-1].version)
		}
		if f.name == "" {
			t.Fatalf("migration %d has no name", f.version)
		}
		body, err := migrations.FS.ReadFile(embeddedName(t, f.version))
		if err != nil {
			t.Fatalf("read embedded migration %d: %v", f.version, err)
		}
		sum := sha256.Sum256(body)
		want := hex.EncodeToString(sum[:])
		// The checksum is the whole "two environments agree about the schema"
		// argument, so it has to be the checksum of the bytes in the binary.
		if f.checksum != want {
			t.Fatalf("migration %d checksum = %s, want %s", f.version, f.checksum, want)
		}
		if len(f.checksum) != 64 {
			t.Fatalf("migration %d checksum has length %d, want 64", f.version, len(f.checksum))
		}
	}
}

// embeddedName finds the file the loader read for a version. Reconstructing the
// name from the version would have to reproduce the loader's zero padding, which
// would make this test agree with a bug rather than with the file.
func embeddedName(t *testing.T, version int64) string {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		var got int64
		if _, err := fmt.Sscanf(name, "%d_", &got); err != nil {
			continue
		}
		if got == version {
			return name
		}
	}
	t.Fatalf("no embedded file for migration %d", version)
	return ""
}

func TestWrapMigrationJoinsTheBookkeepingToTheDDL(t *testing.T) {
	m := migrationFile{
		version:  2,
		name:     "o'brien",
		body:     "BEGIN;\n\nSELECT 1;\n\nCOMMIT;\n",
		checksum: "deadbeef",
	}
	script := wrapMigration(m)

	// The body's own wrappers have to be replaced rather than nested: a nested
	// COMMIT would commit the DDL before the version row is written, which is
	// exactly the half-applied state the wrapper exists to prevent.
	if n := strings.Count(script, "BEGIN;"); n != 1 {
		t.Fatalf("script contains %d BEGIN statements, want 1:\n%s", n, script)
	}
	if n := strings.Count(script, "COMMIT;"); n != 1 {
		t.Fatalf("script contains %d COMMIT statements, want 1:\n%s", n, script)
	}
	if !strings.Contains(script, "SELECT 1;") {
		t.Fatalf("script lost the migration body:\n%s", script)
	}
	if !strings.Contains(script, "VALUES (2, 'o''brien', 'deadbeef', now());") {
		t.Fatalf("script does not record the migration with its checksum:\n%s", script)
	}
	if strings.Index(script, "SELECT 1;") > strings.Index(script, "INSERT INTO schema_migrations") {
		t.Fatal("the version row is written before the migration body")
	}

	// A migration without its own wrappers is still applied, and still wrapped.
	bare := wrapMigration(migrationFile{version: 3, name: "bare", body: "SELECT 2;", checksum: "c0ffee"})
	if n := strings.Count(bare, "BEGIN;"); n != 1 {
		t.Fatalf("bare migration wrapped %d times, want 1:\n%s", n, bare)
	}
}

func TestMigrationVersionsListsTheEmbeddedSet(t *testing.T) {
	versions, err := MigrationVersions()
	if err != nil {
		t.Fatalf("MigrationVersions: %v", err)
	}
	files, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(versions) != len(files) {
		t.Fatalf("MigrationVersions = %v, want one entry per embedded file (%d)", versions, len(files))
	}
	for i := range versions {
		if versions[i] != files[i].version {
			t.Fatalf("MigrationVersions[%d] = %d, want %d", i, versions[i], files[i].version)
		}
	}
}

func TestMigrateUpRefusesAnEmptyDSNAndAnUnreachableOne(t *testing.T) {
	if _, err := MigrateUp(context.Background(), ""); err == nil {
		t.Fatal("MigrateUp accepted an empty DSN")
	}
	// Forward-only migrations must fail loudly rather than half-apply: an
	// unreachable database is the common case, and it must not look like
	// "nothing to do".
	if _, err := MigrateUp(context.Background(), "postgres://nobody@127.0.0.1:1/nothing?sslmode=disable&connect_timeout=1"); err == nil {
		t.Fatal("MigrateUp succeeded against a port that cannot be listening")
	}
}

// TestStoreAgainstRealPostgres runs the same paths against a real server when one
// is reachable. The mock suite above pins the SQL; only a server can prove the
// SQL is valid, that the constraints admit what the crawler writes, and that
// SKIP LOCKED really does keep two claims from overlapping.
//
// It is skipped, not failed, when LOLSTATS_TEST_DB_DSN is unset: this
// environment has no Postgres, and a red suite would misreport that as a broken
// store. The skip message says which of the two happened.
func TestStoreAgainstRealPostgres(t *testing.T) {
	dsn := os.Getenv("LOLSTATS_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("LOLSTATS_TEST_DB_DSN is unset: no Postgres is reachable here, so the store is covered by the go-sqlmock suite; set LOLSTATS_TEST_DB_DSN to a scratch database to run this")
	}
	ctx := context.Background()
	if _, err := MigrateUp(ctx, dsn); err != nil {
		t.Skipf("LOLSTATS_TEST_DB_DSN is set but unusable (%v): skipping the real-database half", err)
	}
	s, err := Open(ctx, Options{DSN: dsn, ConnTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	matchA := "EUW1_TEST_" + suffix + "_a"
	matchB := "EUW1_TEST_" + suffix + "_b"
	matchC := "EUW1_TEST_" + suffix + "_c"
	matchD := "EUW1_TEST_" + suffix + "_d"
	puuidA := "test-puuid-" + suffix + "-a"
	puuidB := "test-puuid-" + suffix + "-b"
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM fetch_queue WHERE match_id = ANY($1)`,
			`DELETE FROM matches WHERE match_id = ANY($1)`,
			`DELETE FROM crawl_frontier WHERE puuid = ANY($1)`,
		} {
			if _, err := s.db.ExecContext(context.Background(), stmt, pqArray([]string{matchA, matchB, matchC, matchD, puuidA, puuidB})); err != nil {
				t.Logf("cleanup %q: %v", stmt, err)
			}
		}
	})

	now := time.Now().UTC()
	rec := func(id string) contract.MatchRecord {
		return contract.MatchRecord{
			MatchID: id, Region: "EUW", QueueID: 420, Patch: "15.1", GameVersion: "15.1.1.1",
			GameCreation: now.Add(-20 * time.Minute), GameDurationS: 1800, PayloadVersion: "1",
			RawURI: "raw/riot/match-v5/dt=2026-03-02/region=EUW/part-00001.parquet.zst", FetchedAt: now,
		}
	}

	t.Run("a second upsert is a no-op", func(t *testing.T) {
		inserted, err := s.UpsertMatch(ctx, rec(matchA))
		if err != nil {
			t.Fatalf("UpsertMatch: %v", err)
		}
		if !inserted {
			t.Fatal("first upsert reported inserted=false")
		}
		inserted, err = s.UpsertMatch(ctx, rec(matchA))
		if err != nil {
			t.Fatalf("second UpsertMatch: %v", err)
		}
		if inserted {
			t.Fatal("re-crawling a known match reported a second insert")
		}
	})

	t.Run("queueing is idempotent and claiming is exclusive", func(t *testing.T) {
		added, err := s.EnqueueMatches(ctx, []contract.QueueItem{
			{MatchID: matchA, Priority: 100},
			{MatchID: matchB, Priority: 0},
			{MatchID: matchA, Priority: 50},
		})
		if err != nil {
			t.Fatalf("EnqueueMatches: %v", err)
		}
		if added != 2 {
			t.Fatalf("added = %d, want 2", added)
		}
		if added, err = s.EnqueueMatches(ctx, []contract.QueueItem{{MatchID: matchA, Priority: 0}}); err != nil || added != 0 {
			t.Fatalf("re-queueing a known match = %d, %v; want 0, nil", added, err)
		}

		claimed, err := s.ClaimJobs(ctx, 10, time.Now().UTC())
		if err != nil {
			t.Fatalf("ClaimJobs: %v", err)
		}
		if len(claimed) != 2 {
			t.Fatalf("claimed %d jobs, want 2", len(claimed))
		}
		if claimed[0].MatchID != matchB {
			t.Fatalf("claimed %s first, want the priority 0 row (%s)", claimed[0].MatchID, matchB)
		}
		// The duplicate id inside the batch kept the more urgent of its two
		// priorities (50, not 100), which is what keeps a match discovered as a
		// participant and as a seed from being walked late.
		if claimed[1].Priority != 50 {
			t.Fatalf("duplicate id kept priority %d, want the more urgent 50", claimed[1].Priority)
		}
		// A second worker asking at the same instant must get nothing: this is
		// the property FOR UPDATE SKIP LOCKED exists for.
		again, err := s.ClaimJobs(ctx, 10, time.Now().UTC())
		if err != nil {
			t.Fatalf("second ClaimJobs: %v", err)
		}
		if len(again) != 0 {
			t.Fatalf("a second claim returned %d rows; two workers would fetch the same match", len(again))
		}

		for _, item := range claimed {
			if err := s.CompleteJob(ctx, item.ID); err != nil {
				t.Fatalf("CompleteJob %d: %v", item.ID, err)
			}
		}
		if again, err = s.ClaimJobs(ctx, 10, time.Now().UTC()); err != nil || len(again) != 0 {
			t.Fatalf("completed jobs were claimed again: %d rows, %v", len(again), err)
		}
	})

	t.Run("a claim abandoned by a dead worker is resumed, not lost or doubled", func(t *testing.T) {
		// Retrying and dead-lettering a row that does not exist must be a silent
		// no-op: these run from maintenance against ids that may already have
		// been cleaned up, and an error there would stop the whole pass.
		if err := s.RetryJob(ctx, 0, now, "unused"); err != nil {
			t.Fatalf("RetryJob on a missing row: %v", err)
		}
		if err := s.DeadLetterJob(ctx, 0, "unused"); err != nil {
			t.Fatalf("DeadLetterJob on a missing row: %v", err)
		}
		if err := s.CompleteJob(ctx, 0); err != nil {
			t.Fatalf("CompleteJob on a missing row: %v", err)
		}
		if added, err := s.EnqueueMatches(ctx, []contract.QueueItem{{MatchID: matchA, Priority: 100}}); err != nil {
			t.Fatalf("EnqueueMatches: %v", err)
		} else if added != 0 {
			t.Fatalf("a done job was re-queued: added = %d", added)
		}

		if inserted, err := s.UpsertMatch(ctx, rec(matchC)); err != nil || !inserted {
			t.Fatalf("UpsertMatch(%s) = %v, %v", matchC, inserted, err)
		}
		if added, err := s.EnqueueMatches(ctx, []contract.QueueItem{{MatchID: matchC, Priority: 100}}); err != nil || added != 1 {
			t.Fatalf("EnqueueMatches(%s) = %d, %v", matchC, added, err)
		}

		claimed, err := s.ClaimJobs(ctx, 10, time.Now().UTC())
		if err != nil {
			t.Fatalf("ClaimJobs: %v", err)
		}
		if len(claimed) != 1 || claimed[0].MatchID != matchC {
			t.Fatalf("claimed = %+v, want exactly %s", claimed, matchC)
		}
		stuck := claimed[0]

		// The worker dies without completing. Another worker asking now gets
		// nothing, so the fetch is not duplicated.
		if again, err := s.ClaimJobs(ctx, 10, time.Now().UTC()); err != nil || len(again) != 0 {
			t.Fatalf("a second worker claimed an in-flight job: %+v, %v", again, err)
		}

		// Maintenance reclaims it once the claim is older than the grace period.
		n, err := s.ResetStuckClaims(ctx, time.Now().UTC().Add(time.Second), 10)
		if err != nil {
			t.Fatalf("ResetStuckClaims: %v", err)
		}
		if n < 1 {
			t.Fatal("ResetStuckClaims reclaimed nothing, so the job would be stuck forever")
		}
		resumed, err := s.ClaimJobs(ctx, 10, time.Now().UTC().Add(time.Second))
		if err != nil {
			t.Fatalf("ClaimJobs after reclaim: %v", err)
		}
		if len(resumed) != 1 || resumed[0].ID != stuck.ID {
			t.Fatalf("resumed = %+v, want the same job (%d) that was abandoned", resumed, stuck.ID)
		}
		// The attempt counter keeps counting across the restart, which is what
		// makes the attempt ceiling reach a dead letter instead of looping.
		if resumed[0].Attempts <= stuck.Attempts {
			t.Fatalf("attempts = %d after a reclaim from %d, want it to keep counting", resumed[0].Attempts, stuck.Attempts)
		}

		// A backoff in the future takes the row out of reach until it elapses ...
		if err := s.RetryJob(ctx, resumed[0].ID, time.Now().UTC().Add(time.Hour), "test: backing off"); err != nil {
			t.Fatalf("RetryJob: %v", err)
		}
		if again, err := s.ClaimJobs(ctx, 10, time.Now().UTC()); err != nil || len(again) != 0 {
			t.Fatalf("a job behind its backoff was claimed: %+v, %v", again, err)
		}
		// ... and the attempt it then gets is the one that decides it is out of
		// attempts and retires it. Retiring happens from a claim, so the job has
		// to be claimed again before the dead letter means anything.
		later := time.Now().UTC().Add(2 * time.Hour)
		again, err := s.ClaimJobs(ctx, 10, later)
		if err != nil {
			t.Fatalf("ClaimJobs after the backoff: %v", err)
		}
		if len(again) != 1 || again[0].ID != resumed[0].ID {
			t.Fatalf("claimed = %+v after the backoff, want the backed-off job back", again)
		}
		if err := s.DeadLetterJob(ctx, again[0].ID, "test: out of attempts"); err != nil {
			t.Fatalf("DeadLetterJob: %v", err)
		}
		if again, err := s.ClaimJobs(ctx, 10, later.Add(time.Hour)); err != nil || len(again) != 0 {
			t.Fatalf("a dead letter was claimed: %+v, %v", again, err)
		}
	})

	t.Run("the frontier is claimed once and cooled down", func(t *testing.T) {
		added, err := s.UpsertFrontier(ctx, []contract.FrontierEntry{
			{PUUID: puuidA, SeedTier: "GOLD", SeedDivision: "I", Priority: 100},
			{PUUID: puuidB, SeedTier: "GOLD", SeedDivision: "I", Priority: 100},
		})
		if err != nil {
			t.Fatalf("UpsertFrontier: %v", err)
		}
		if added != 2 {
			t.Fatalf("added = %d, want 2", added)
		}
		// Re-discovery is not discovery: the second call must report zero.
		if added, err = s.UpsertFrontier(ctx, []contract.FrontierEntry{{PUUID: puuidA}}); err != nil || added != 0 {
			t.Fatalf("re-discovery = %d, %v; want 0, nil", added, err)
		}

		claimed, err := s.ClaimFrontier(ctx, 10, time.Now().UTC())
		if err != nil {
			t.Fatalf("ClaimFrontier: %v", err)
		}
		if len(claimed) != 2 {
			t.Fatalf("claimed %d frontier rows, want 2", len(claimed))
		}
		if claimed[0].LastSeenAt.IsZero() {
			t.Fatal("claim did not record last_seen_at, so a never-walked row would look stale")
		}
		// The claim itself is the cooldown: an immediate second claim by another
		// worker must find nothing.
		if again, err := s.ClaimFrontier(ctx, 10, time.Now().UTC()); err != nil || len(again) != 0 {
			t.Fatalf("a second frontier claim returned %d rows, %v", len(again), err)
		}
		if err := s.MarkFrontierFetched(ctx, puuidA, time.Now().UTC(), true); err != nil {
			t.Fatalf("MarkFrontierFetched: %v", err)
		}
		if err := s.MarkFrontierDead(ctx, puuidB, "test: account closed"); err != nil {
			t.Fatalf("MarkFrontierDead: %v", err)
		}
		if n, err := s.DeadFrontierSize(ctx); err != nil || n < 1 {
			t.Fatalf("DeadFrontierSize = %d, %v; want at least 1", n, err)
		}
		if n, err := s.RecomputeFrontierPriority(ctx); err != nil || n < 1 {
			t.Fatalf("RecomputeFrontierPriority = %d, %v; want at least 1 changed row", n, err)
		}
	})

	t.Run("audit rows open and close", func(t *testing.T) {
		seedID, err := s.StartSeedRun(ctx, contract.SeedRun{Tier: "GOLD", Division: "I", Queue: "RANKED_SOLO_5x5", StartedAt: now})
		if err != nil {
			t.Fatalf("StartSeedRun: %v", err)
		}
		if err := s.FinishSeedRun(ctx, seedID, now.Add(time.Minute), 1000); err != nil {
			t.Fatalf("FinishSeedRun: %v", err)
		}
		buildID, err := s.StartBuildRun(ctx, contract.BuildRun{Patch: "15.1", Queue: 420, Bracket: "GOLD_I", StartedAt: now, GitSHA: "test"})
		if err != nil {
			t.Fatalf("StartBuildRun: %v", err)
		}
		if err := s.FinishBuildRun(ctx, buildID, contract.BuildResult{Status: "ok", FinishedAt: now, CellsTotal: 1, CellsPublished: 1}); err != nil {
			t.Fatalf("FinishBuildRun: %v", err)
		}
	})

	t.Run("a dead letter is revived a bounded number of times", func(t *testing.T) {
		// D5. The revive-on-enqueue rule reset attempts to zero and matched on
		// status = 'dead' alone, so a permanently failing row walked
		// dead -> pending -> claimed -> retry -> claimed -> dead for ever: every
		// later discovery of the same match brought it back, and the crawl
		// never finished. The row now carries a revival budget, which the
		// operator's replay deliberately does not consume.
		if added, err := s.EnqueueMatches(ctx, []contract.QueueItem{{MatchID: matchD, Priority: 100}}); err != nil || added != 1 {
			t.Fatalf("EnqueueMatches(%s) = %d, %v; want 1, nil", matchD, added, err)
		}

		revivals := 0
		stalledAt := -1
		for walk := 0; walk < revivalBudget+5; walk++ {
			// One walk of the pipeline: claim it, fail it, retire it, and let
			// the next discovery of the same id queue it again.
			claimed, err := s.ClaimJobs(ctx, 10, time.Now().UTC())
			if err != nil {
				t.Fatalf("ClaimJobs (walk %d): %v", walk, err)
			}
			if len(claimed) == 0 {
				// Out of budget. Under the old rule this walk read "dead" and
				// revived the row again, for ever.
				stalledAt = walk
				break
			}
			if len(claimed) != 1 || claimed[0].MatchID != matchD {
				t.Fatalf("walk %d claimed %+v, want exactly %s", walk, claimed, matchD)
			}
			if err := s.DeadLetterJob(ctx, claimed[0].ID, "test: Riot will never serve this match"); err != nil {
				t.Fatalf("DeadLetterJob: %v", err)
			}
			added, err := s.EnqueueMatches(ctx, []contract.QueueItem{{MatchID: matchD, Priority: 100}})
			if err != nil {
				t.Fatalf("EnqueueMatches (walk %d): %v", walk, err)
			}
			if added == 1 {
				revivals++
			}
		}

		if stalledAt < 0 {
			t.Fatalf("the row was still being claimed after %d revivals: it never stops coming back", revivalBudget+5)
		}
		if revivals != revivalBudget {
			t.Fatalf("the row was revived %d times, want exactly %d: a dead letter that revives for ever is a crawl that cannot finish",
				revivals, revivalBudget)
		}
		var status string
		var budget, attempts int
		if err := s.db.QueryRowContext(ctx,
			`SELECT status, revivals, attempts FROM fetch_queue WHERE match_id = $1`, matchD).
			Scan(&status, &budget, &attempts); err != nil {
			t.Fatalf("read back %s: %v", matchD, err)
		}
		if status != string(contract.JobDead) {
			t.Fatalf("status = %q, want %q: the row is out of revival budget", status, contract.JobDead)
		}
		if budget != revivalBudget {
			t.Fatalf("revivals = %d, want %d", budget, revivalBudget)
		}

		// The negative control for "bounded" not degenerating into "dropped":
		// the row is not gone, it is waiting for an operator to say the
		// condition that retired it has been fixed.
		replayed, err := s.ReplayDeadLettered(ctx, 10)
		if err != nil {
			t.Fatalf("ReplayDeadLettered: %v", err)
		}
		if replayed < 1 {
			t.Fatal("ReplayDeadLettered replayed nothing, so a row out of budget can never be revisited")
		}
		if err := s.db.QueryRowContext(ctx, `SELECT status FROM fetch_queue WHERE match_id = $1`, matchD).Scan(&status); err != nil {
			t.Fatalf("read back after replay: %v", err)
		}
		if status != string(contract.JobPending) {
			t.Fatalf("status after replay = %q, want %q", status, contract.JobPending)
		}
		// And the explicit replay is not undone by the next discovery: the
		// automatic path stays out of budget.
		if added, err := s.EnqueueMatches(ctx, []contract.QueueItem{{MatchID: matchD, Priority: 100}}); err != nil || added != 0 {
			t.Fatalf("EnqueueMatches after a replay = %d, %v; want 0, nil", added, err)
		}
	})

	t.Run("reports read back what was written", func(t *testing.T) {
		depths, err := s.QueueDepths(ctx)
		if err != nil {
			t.Fatalf("QueueDepths: %v", err)
		}
		if depths[contract.JobDone] < 2 {
			t.Fatalf("done jobs = %d, want at least the two this test completed", depths[contract.JobDone])
		}
		if _, ok, err := s.QueueOldest(ctx); err != nil {
			t.Fatalf("QueueOldest: %v", err)
		} else if ok == false {
			t.Log("queue has no ready work left, which is the expected end state of this test")
		}
		newest, ok, err := s.NewestFetchedAt(ctx)
		if err != nil {
			t.Fatalf("NewestFetchedAt: %v", err)
		}
		if !ok || newest.IsZero() {
			t.Fatal("NewestFetchedAt reports an empty pipeline after a successful upsert")
		}
	})
}

// pqArray renders a text[] literal. pgx accepts a []string parameter natively,
// but this keeps the cleanup statement independent of the driver's array codec.
func pqArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, `"`+strings.ReplaceAll(v, `"`, `\"`)+`"`)
	}
	return "{" + strings.Join(quoted, ",") + "}"
}
