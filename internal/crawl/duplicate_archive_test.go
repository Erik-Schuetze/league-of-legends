package crawl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
)

// A re-walk of a match the control plane already holds must not append a second
// record to the raw archive.
//
// The two stores are not the same store, and only one of them is idempotent.
// `matches` has match_id as its primary key and the crawler inserts with
// ON CONFLICT DO NOTHING, so walking a known match again costs one wasted
// upsert. The archive is append-only parquet with no key at all: a second walk
// of the same match appends a second copy of the same payload and nothing
// collapses it. The `matches` table being idempotent is exactly why this defect
// survived - it was verified by walking a match twice and measuring a duplicate
// delta of zero, but that measurement was taken on the table, not on the
// archive.
//
// The duplicate is not merely wasted bytes. Every published number is counted
// from archive rows, so a duplicated match is a duplicated game: it doubles the
// participants behind the cell tallies while matches_used, which counts distinct
// match ids, keeps counting the match once. The build de-duplicates the archive
// for its own defence (internal/aggregate/sql.go), but the cheapest and only
// complete fix is not to write the second copy at all.
//
// The live crawl path never asked whether it already had the match, unlike the
// backfill path, which consults KnownMatchChecker before it spends a fetch
// (internal/crawl/backfill.go). So a re-walk - a frontier whose history window
// comes round again, a maintain replay, an operator replaying a key range -
// re-fetched the payload from Riot and archived it a second time.
func TestAWalkedMatchIsNotArchivedTwice(t *testing.T) {
	h := newHarness(t, nil)
	h.serveFixture(t, "EUW1_1")

	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})
	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("first Step: %v", err)
	}
	if got := h.writer.writeCount(); got != 1 {
		t.Fatalf("archive writes after the first walk = %d, want 1", got)
	}
	if got := h.store.jobStatus("EUW1_1"); got != "done" {
		t.Fatalf("job status after the first walk = %q, want done", got)
	}

	// The same match comes round again. forceEnqueue is the same re-presentation
	// the other tests use: it is what a history page that still lists an old
	// match, or a replayed key range, looks like to the queue.
	h.clock.Advance(48 * time.Hour)
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})
	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("second Step: %v", err)
	}

	if got := h.writer.writeCount(); got != 1 {
		t.Fatalf("archive writes after the re-walk = %d, want 1: the archive is append-only and has no key, so a second walk appends a second copy of a payload that is already stored", got)
	}
	if ids := h.writer.matchIDs(); len(ids) != 1 {
		t.Fatalf("archived records = %v, want one record for the match", ids)
	}
	// The row is closed rather than stranded. The match really is ingested, so
	// the work is finished: leaving the row claimed would hide it for the whole
	// claim grace and then re-offer it, which is how the second archive record
	// was produced in the first place.
	if got := h.store.jobStatus("EUW1_1"); got != "done" {
		t.Fatalf("job status after the re-walk = %q, want done", got)
	}
	// The saving is the fetch as much as the write: on a real key this is a Riot
	// call the crawl does not have to make.
	if got := h.fetcher.fetchCount("match:"); got != 1 {
		t.Fatalf("match fetches = %d, want 1: a known match must not be re-fetched", got)
	}
	if got := h.store.matchCount(); got != 1 {
		t.Fatalf("matches = %d, want 1: the repeated walk must not add a control-plane row either", got)
	}
}

// The check that prevents the duplicate is a saving, not a gate: a control plane
// that cannot answer it must not cost the row its fetch. Failing the row there
// would drop work - the exact trade the crawler refuses elsewhere, where the
// upsert is left to decide - so the row is fetched, archived and inserted
// exactly as it was before the check existed.
func TestAnUnanswerableKnownMatchCheckStillFetchesTheRow(t *testing.T) {
	h := newHarness(t, nil)
	h.serveFixture(t, "EUW1_1")
	h.store.failExists = errors.New("control plane down")
	h.store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})

	if _, err := h.worker.Step(context.Background()); err != nil {
		t.Fatalf("Step: %v", err)
	}

	if got := h.fetcher.fetchCount("match:EUW1_1"); got != 1 {
		t.Fatalf("match fetches = %d, want 1: a lookup that failed must not decide the row", got)
	}
	if got := h.writer.writeCount(); got != 1 {
		t.Fatalf("archive writes = %d, want 1", got)
	}
	if got := h.store.matchCount(); got != 1 {
		t.Fatalf("matches = %d, want 1", got)
	}
	if got := h.store.jobStatus("EUW1_1"); got != "done" {
		t.Fatalf("job status = %q, want done", got)
	}
}
