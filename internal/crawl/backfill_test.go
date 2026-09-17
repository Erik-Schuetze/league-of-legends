package crawl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// matchDTOFor builds the smallest match that still carries the fields a record
// needs, so a backfill test can key on any id.
func matchDTOFor(matchID string, at time.Time) riot.MatchDTO {
	return riot.MatchDTO{
		Metadata: riot.MatchMetadata{MatchID: matchID},
		Info: riot.MatchInfo{
			GameCreation: at.UnixMilli(),
			QueueID:      420,
			GameVersion:  "16.18.612.9234",
			GameDuration: 1800,
			Participants: []riot.MatchParticipant{{PUUID: "p1"}, {PUUID: "p2"}},
		},
	}
}

func newBackfillHarness(t *testing.T) (*fakeStore, *fakeFetcher, *fakeWriter, Deps) {
	t.Helper()
	store := newFakeStore()
	fetcher := newFakeFetcher("RGAPI-test-key")
	writer := newFakeWriter()
	return store, fetcher, writer, testDeps(store, fetcher, writer, riot.NewFakeClock(testBaseTime()))
}

func TestBackfillFetchesAnExplicitIDList(t *testing.T) {
	store, fetcher, writer, deps := newBackfillHarness(t)
	for _, id := range []string{"EUW1_1", "EUW1_2", "EUW1_3"} {
		fetcher.serve(id, matchDTOFor(id, testBaseTime()), []byte(`{"matchId":"`+id+`"}`))
	}

	result, err := Backfill(context.Background(), BackfillOptions{
		Deps:     deps,
		MatchIDs: []string{"EUW1_1", "EUW1_2", "EUW1_3"},
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if result.Candidates != 3 || result.Fetched != 3 || result.Inserted != 3 {
		t.Fatalf("result = %+v, want 3 candidates, 3 fetched, 3 inserted", result)
	}
	if result.Known != 0 || result.Failed != 0 {
		t.Fatalf("result = %+v, want no known and no failures", result)
	}
	if got := writer.matchIDs(); len(got) != 3 {
		t.Fatalf("archived %v, want every key", got)
	}
	if writer.flushCount() == 0 {
		t.Fatal("a backfill must flush the archive once at the end")
	}
	// The queue is not the backfill's business: a repair writes matches, it does
	// not schedule them.
	if store.queueLen() != 0 {
		t.Fatalf("queue length = %d, want 0", store.queueLen())
	}
}

func TestBackfillDedupesAndHonoursTheLimit(t *testing.T) {
	_, fetcher, _, deps := newBackfillHarness(t)
	for _, id := range []string{"EUW1_1", "EUW1_2", "EUW1_3"} {
		fetcher.serve(id, matchDTOFor(id, testBaseTime()), []byte(`{}`))
	}

	result, err := Backfill(context.Background(), BackfillOptions{
		Deps: deps,
		// The blank is dropped, the repeat collapses, and the cap keeps the
		// first two of the three distinct keys.
		MatchIDs: []string{"EUW1_1", "", "EUW1_1", "EUW1_2", "EUW1_3"},
		Limit:    2,
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if result.Candidates != 2 {
		t.Fatalf("candidates = %d, want 2", result.Candidates)
	}
	if got := fetcher.fetchCount("match:"); got != 2 {
		t.Fatalf("fetches = %d, want 2", got)
	}
}

// A repeated backfill must be cheap: the control plane already has the payload,
// so the key is skipped and counted rather than re-fetched.
func TestBackfillSkipsKnownMatchesUnlessForced(t *testing.T) {
	tests := []struct {
		name         string
		force        bool
		wantFetched  int
		wantKnown    int
		wantInserted int
	}{
		{name: "known keys are skipped", wantFetched: 1, wantKnown: 2, wantInserted: 1},
		{name: "force re-fetches but stays idempotent", force: true, wantFetched: 3, wantKnown: 0, wantInserted: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, fetcher, _, deps := newBackfillHarness(t)
			for _, id := range []string{"EUW1_1", "EUW1_2", "EUW1_3"} {
				fetcher.serve(id, matchDTOFor(id, testBaseTime()), []byte(`{}`))
			}
			for _, known := range []string{"EUW1_1", "EUW1_2"} {
				if _, err := store.UpsertMatch(context.Background(), contract.MatchRecord{MatchID: known}); err != nil {
					t.Fatalf("seed known match: %v", err)
				}
			}

			result, err := Backfill(context.Background(), BackfillOptions{
				Deps:     deps,
				MatchIDs: []string{"EUW1_1", "EUW1_2", "EUW1_3"},
				Force:    tc.force,
			})
			if err != nil {
				t.Fatalf("Backfill: %v", err)
			}
			if result.Fetched != tc.wantFetched || result.Known != tc.wantKnown || result.Inserted != tc.wantInserted {
				t.Fatalf("result = %+v, want fetched=%d known=%d inserted=%d",
					result, tc.wantFetched, tc.wantKnown, tc.wantInserted)
			}
			if got := store.matchCount(); got != 3 {
				t.Fatalf("matches recorded = %d, want 3: the upsert dedupes on match_id", got)
			}
		})
	}
}

// The window is a repair over one player's history, so the paging parameters
// must reach Riot unchanged.
func TestBackfillPagesAPUUIDWindow(t *testing.T) {
	store, fetcher, _, deps := newBackfillHarness(t)
	fetcher.history["p1"] = []string{"EUW1_1", "EUW1_2", "EUW1_3", "EUW1_4", "EUW1_5"}
	for _, id := range fetcher.history["p1"] {
		fetcher.serve(id, matchDTOFor(id, testBaseTime()), []byte(`{}`))
	}
	from := testBaseTime().Add(-7 * 24 * time.Hour)
	to := testBaseTime()

	result, err := Backfill(context.Background(), BackfillOptions{
		Deps:         deps,
		PUUID:        "p1",
		From:         from,
		To:           to,
		Limit:        4,
		HistoryCount: 2,
		Queue:        420,
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if result.Candidates != 4 {
		t.Fatalf("candidates = %d, want the Limit of 4", result.Candidates)
	}
	if result.Fetched != 4 || result.Inserted != 4 {
		t.Fatalf("result = %+v, want 4 fetched and inserted", result)
	}

	queries := fetcher.historyQueries()
	if len(queries) != 2 {
		t.Fatalf("history lookups = %d, want 2 pages of 2", len(queries))
	}
	if queries[0].Start != 0 || queries[1].Start != 2 {
		t.Fatalf("starts = %d,%d, want 0,2", queries[0].Start, queries[1].Start)
	}
	for _, q := range queries {
		if q.PUUID != "p1" || q.Queue != 420 {
			t.Fatalf("query = %+v, want puuid p1 on queue 420", q)
		}
		if !q.StartTime.Equal(from) || !q.EndTime.Equal(to) {
			t.Fatalf("window = %s..%s, want %s..%s", q.StartTime, q.EndTime, from, to)
		}
		if q.Count != 2 {
			t.Fatalf("count = %d, want 2", q.Count)
		}
	}
	if store.queueLen() != 0 {
		t.Fatalf("queue length = %d, want 0", store.queueLen())
	}
}

// A key that fails is collected, not fatal: a repair that stops at the first
// 500 is not a repair.
func TestBackfillCollectsFailuresAndReportsThem(t *testing.T) {
	_, fetcher, _, deps := newBackfillHarness(t)
	fetcher.serve("EUW1_1", matchDTOFor("EUW1_1", testBaseTime()), []byte(`{}`))
	fetcher.errors["match:EUW1_2"] = &riot.StatusError{Method: "match", Status: 500}
	fetcher.serve("EUW1_3", matchDTOFor("EUW1_3", testBaseTime()), []byte(`{}`))

	result, err := Backfill(context.Background(), BackfillOptions{
		Deps:     deps,
		MatchIDs: []string{"EUW1_1", "EUW1_2", "EUW1_3"},
	})
	if err == nil {
		t.Fatal("a failed key must be reported to the operator")
	}
	if result.Candidates != 3 || result.Fetched != 2 || result.Failed != 1 {
		t.Fatalf("result = %+v, want 3 candidates, 2 fetched, 1 failed", result)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("failures = %v, want one line", result.Failures)
	}
}

// The failure list is capped; the count is not.
func TestBackfillCapsTheFailureListButNotTheCount(t *testing.T) {
	_, fetcher, _, deps := newBackfillHarness(t)
	ids := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		id := "EUW1_" + string(rune('a'+i))
		ids = append(ids, id)
		fetcher.errors["match:"+id] = errors.New("boom")
	}

	result, err := Backfill(context.Background(), BackfillOptions{Deps: deps, MatchIDs: ids, Limit: 25})
	if err == nil {
		t.Fatal("want an error for a run where every key failed")
	}
	if result.Failed != 25 {
		t.Fatalf("failed = %d, want the exact count of 25", result.Failed)
	}
	if len(result.Failures) != maxBackfillFailures {
		t.Fatalf("failures = %d, want the cap of %d", len(result.Failures), maxBackfillFailures)
	}
}

func TestBackfillRejectsAnEmptyKeyRange(t *testing.T) {
	_, _, _, deps := newBackfillHarness(t)
	if _, err := Backfill(context.Background(), BackfillOptions{Deps: deps}); err == nil {
		t.Fatal("a backfill with no key source must be refused")
	}
}

func TestBackfillWithAnEmptyHistoryIsANoOp(t *testing.T) {
	_, fetcher, writer, deps := newBackfillHarness(t)
	result, err := Backfill(context.Background(), BackfillOptions{Deps: deps, PUUID: "nobody"})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if result.Candidates != 0 || result.Fetched != 0 {
		t.Fatalf("result = %+v, want nothing to do", result)
	}
	if writer.flushCount() != 0 {
		t.Fatalf("flushes = %d, want none when there was nothing to write", writer.flushCount())
	}
	if got := fetcher.fetchCount("match:"); got != 0 {
		t.Fatalf("match fetches = %d, want 0", got)
	}
}

func TestBackfillStopsOnContextCancellation(t *testing.T) {
	_, fetcher, _, deps := newBackfillHarness(t)
	fetcher.serve("EUW1_1", matchDTOFor("EUW1_1", testBaseTime()), []byte(`{}`))
	fetcher.serve("EUW1_2", matchDTOFor("EUW1_2", testBaseTime()), []byte(`{}`))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Backfill(ctx, BackfillOptions{Deps: deps, MatchIDs: []string{"EUW1_1", "EUW1_2"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestBackfillNeedsItsDependencies(t *testing.T) {
	store := newFakeStore()
	fetcher := newFakeFetcher("k")
	writer := newFakeWriter()
	log := testLogger()

	tests := []struct {
		name string
		deps Deps
	}{
		{name: "no store", deps: Deps{Fetcher: fetcher, Writer: writer, Log: log}},
		{name: "no fetcher", deps: Deps{Store: store, Writer: writer, Log: log}},
		{name: "no writer", deps: Deps{Store: store, Fetcher: fetcher, Log: log}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Backfill(context.Background(), BackfillOptions{Deps: tc.deps, MatchIDs: []string{"EUW1_1"}}); err == nil {
				t.Fatal("Backfill ran without its dependencies")
			}
		})
	}
}

// A store without MatchExists cannot make the skip decision, so the backfill
// fetches and lets the upsert report what was already there.
func TestBackfillWithoutMatchExistsStillWorks(t *testing.T) {
	store, fetcher, _, deps := newBackfillHarness(t)
	deps.Store = contractOnlyStore{store}
	fetcher.serve("EUW1_1", matchDTOFor("EUW1_1", testBaseTime()), []byte(`{}`))

	result, err := Backfill(context.Background(), BackfillOptions{Deps: deps, MatchIDs: []string{"EUW1_1"}})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if result.Fetched != 1 || result.Inserted != 1 {
		t.Fatalf("result = %+v, want one fetch and one insert", result)
	}
}

// A record must point at the archive, not at a second copy of the path
// configuration: the writer is the single source of truth for where the bytes
// went.
func TestBackfillRecordsTheArchiveURI(t *testing.T) {
	store, fetcher, _, deps := newBackfillHarness(t)
	fetcher.serve("EUW1_1", matchDTOFor("EUW1_1", testBaseTime()), []byte(`{}`))
	if _, err := Backfill(context.Background(), BackfillOptions{Deps: deps, MatchIDs: []string{"EUW1_1"}}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	rec := store.record("EUW1_1")
	if rec.RawURI == "" || rec.RawURI[0] != '/' {
		t.Fatalf("raw uri = %q, want an absolute path under the writer root", rec.RawURI)
	}
}
