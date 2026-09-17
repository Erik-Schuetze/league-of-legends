package crawl

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

func newMaintainHarness(t *testing.T) (*fakeStore, Deps) {
	t.Helper()
	store := newFakeStore()
	fetcher := newFakeFetcher("RGAPI-test-key")
	writer := newFakeWriter()
	return store, testDeps(store, fetcher, writer, riot.NewFakeClock(testBaseTime()))
}

// claimLeaves holds a claim that was taken old enough for the grace window to
// have expired, which is what a worker that died mid-job leaves behind.
func claimLeaves(t *testing.T, store *fakeStore, matchID string, claimedAt time.Time) {
	t.Helper()
	store.forceEnqueue(contract.QueueItem{MatchID: matchID})
	if _, err := store.ClaimJobs(context.Background(), 1, claimedAt); err != nil {
		t.Fatalf("claim %s: %v", matchID, err)
	}
	if got := store.jobStatus(matchID); got != "claimed" {
		t.Fatalf("status = %q, want claimed", got)
	}
}

func TestMaintainReclaimsAbandonedClaims(t *testing.T) {
	store, deps := newMaintainHarness(t)
	claimLeaves(t, store, "EUW1_stale", testBaseTime().Add(-time.Hour))
	store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_fresh"})
	if _, err := store.ClaimJobs(context.Background(), 1, testBaseTime()); err != nil {
		t.Fatalf("claim fresh: %v", err)
	}

	result, err := Maintain(context.Background(), MaintainOptions{Deps: deps})
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if result.ReclaimedClaims != 1 {
		t.Fatalf("reclaimed = %d, want the stale claim only", result.ReclaimedClaims)
	}
	if got := store.jobStatus("EUW1_stale"); got != "pending" {
		t.Fatalf("stale status = %q, want it returned to the queue", got)
	}
	if got := store.jobStatus("EUW1_fresh"); got != "claimed" {
		t.Fatalf("fresh status = %q, want an in-flight claim left alone", got)
	}
}

// The grace window is what decides; a claim inside it is somebody else's work
// in progress, not a corpse.
func TestMaintainHonoursTheClaimGrace(t *testing.T) {
	store, deps := newMaintainHarness(t)
	claimLeaves(t, store, "EUW1_1", testBaseTime().Add(-5*time.Minute))

	result, err := Maintain(context.Background(), MaintainOptions{Deps: deps, ClaimGrace: time.Hour})
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if result.ReclaimedClaims != 0 {
		t.Fatalf("reclaimed = %d, want 0 with a one hour grace", result.ReclaimedClaims)
	}
	if got := store.jobStatus("EUW1_1"); got != "claimed" {
		t.Fatalf("status = %q, want claimed", got)
	}
}

func TestMaintainPrunesExhaustedFrontierEntries(t *testing.T) {
	store, deps := newMaintainHarness(t)
	old := testBaseTime().Add(-90 * 24 * time.Hour)
	store.seedFrontier(
		// Walked five times, produced nothing, not seen for the whole window.
		contract.FrontierEntry{
			PUUID: "fruitless", LastSeenAt: old, LastFetchedAt: old,
			ConsecutiveEmpty: DefaultMaxConsecutiveMiss, Priority: PriorityPlayer,
		},
		// Dead and stale.
		contract.FrontierEntry{PUUID: "gone", LastSeenAt: old, LastFetchedAt: old, Dead: true},
		// Recently seen, so the window protects it even when fruitless.
		contract.FrontierEntry{
			PUUID: "recent", LastSeenAt: testBaseTime(), LastFetchedAt: testBaseTime(),
			ConsecutiveEmpty: DefaultMaxConsecutiveMiss, Priority: PriorityPlayer,
		},
		// Never fetched. Unknown is not stale.
		contract.FrontierEntry{PUUID: "unknown", LastSeenAt: old, Priority: PrioritySeed},
	)

	result, err := Maintain(context.Background(), MaintainOptions{Deps: deps})
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if result.Pruned != 2 {
		t.Fatalf("pruned = %d, want the fruitless and the dead entry", result.Pruned)
	}
	if got := store.FrontierSizeLocked(); got != 2 {
		t.Fatalf("frontier size = %d, want the two survivors", got)
	}
	if entry := store.frontierEntry("unknown"); entry.PUUID == "" {
		t.Fatal("a never-fetched entry was pruned: deleting the unknown erases that a seed was discovered")
	}
}

func TestMaintainRecomputesFrontierPriority(t *testing.T) {
	store, deps := newMaintainHarness(t)
	store.seedFrontier(
		// A seeded player that drifted behind a participant.
		contract.FrontierEntry{PUUID: "seeded", SeedTier: "GOLD", SeedDivision: "I", Priority: PriorityParticipant, LastSeenAt: testBaseTime()},
		// A participant that was promoted to a seed priority.
		contract.FrontierEntry{PUUID: "participant", Priority: PriorityBackfill, LastSeenAt: testBaseTime()},
	)

	result, err := Maintain(context.Background(), MaintainOptions{Deps: deps})
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if result.Reprioritised != 2 {
		t.Fatalf("reprioritised = %d, want both rows", result.Reprioritised)
	}
	if got := store.frontierEntry("seeded").Priority; got != PrioritySeed {
		t.Fatalf("seeded priority = %d, want %d", got, PrioritySeed)
	}
	if got := store.frontierEntry("participant").Priority; got != PriorityPlayer {
		t.Fatalf("participant priority = %d, want %d", got, PriorityPlayer)
	}
}

// Dry run is the difference between "show me" and "spend the budget": it reads
// the state and changes nothing.
func TestMaintainDryRunChangesNothing(t *testing.T) {
	store, deps := newMaintainHarness(t)
	old := testBaseTime().Add(-90 * 24 * time.Hour)
	claimLeaves(t, store, "EUW1_stale", old)
	store.seedFrontier(contract.FrontierEntry{
		PUUID: "fruitless", LastSeenAt: old, LastFetchedAt: old,
		ConsecutiveEmpty: DefaultMaxConsecutiveMiss, Priority: PriorityParticipant,
	})

	result, err := Maintain(context.Background(), MaintainOptions{Deps: deps, DryRun: true})
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if !result.DryRun {
		t.Fatal("the result does not record that nothing was changed")
	}
	if result.Pruned != 0 || result.ReclaimedClaims != 0 || result.Reprioritised != 0 {
		t.Fatalf("result = %+v, want no changes reported", result)
	}
	if got := store.jobStatus("EUW1_stale"); got != "claimed" {
		t.Fatalf("status = %q, want the claim untouched", got)
	}
	if got := store.FrontierSizeLocked(); got != 1 {
		t.Fatalf("frontier size = %d, want the entry untouched", got)
	}
	if got := store.frontierEntry("fruitless").Priority; got != PriorityParticipant {
		t.Fatalf("priority = %d, want it untouched", got)
	}
	// The numbers are the report, so a dry run still reads them.
	if result.FrontierSize != 1 {
		t.Fatalf("reported frontier size = %d, want 1", result.FrontierSize)
	}
}

// A store that predates the maintenance surface is reported rather than
// half-run: reclaiming and pruning are one pass, and running half of it hides
// which half the operator has.
func TestMaintainReportsAStoreWithoutTheMaintenanceSurface(t *testing.T) {
	store, deps := newMaintainHarness(t)
	store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})
	deps.Store = contractOnlyStore{store}

	_, err := Maintain(context.Background(), MaintainOptions{Deps: deps})
	if err == nil {
		t.Fatal("Maintain ran against a store without the maintenance surface")
	}
	if !strings.Contains(err.Error(), "does not support maintenance") {
		t.Fatalf("err = %v, want it to name the missing surface", err)
	}
	if got := store.jobStatus("EUW1_1"); got != "pending" {
		t.Fatalf("status = %q, want the queue untouched", got)
	}
}

// The report is the point of a maintenance pass on a small crawl: the numbers
// have to come back even when nothing needed changing.
func TestMaintainReportsThePipelineState(t *testing.T) {
	store, deps := newMaintainHarness(t)
	store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_pending"})
	store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_other"})
	if _, err := store.ClaimJobs(context.Background(), 1, testBaseTime()); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := store.UpsertMatch(context.Background(), contract.MatchRecord{
		MatchID: "EUW1_done", FetchedAt: testBaseTime().Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	store.seedFrontier(
		contract.FrontierEntry{PUUID: "live", LastSeenAt: testBaseTime(), Priority: PriorityPlayer},
		contract.FrontierEntry{PUUID: "retired", LastSeenAt: testBaseTime(), LastFetchedAt: testBaseTime(), Dead: true},
	)

	result, err := Maintain(context.Background(), MaintainOptions{Deps: deps})
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if result.QueueDepths[contract.JobPending] != 1 {
		t.Fatalf("pending = %d, want 1", result.QueueDepths[contract.JobPending])
	}
	if result.QueueDepths[contract.JobClaimed] != 1 {
		t.Fatalf("claimed = %d, want 1", result.QueueDepths[contract.JobClaimed])
	}
	if !result.HasQueueOldest {
		t.Fatal("the oldest pending row was not reported")
	}
	if result.FrontierSize != 1 || result.DeadFrontierSize != 1 {
		t.Fatalf("frontier = %d/%d live/dead, want 1/1", result.FrontierSize, result.DeadFrontierSize)
	}
	if !result.HasFetched || !result.NewestFetchedAt.Equal(testBaseTime().Add(-5*time.Minute)) {
		t.Fatalf("newest fetched = %s (%v), want the match row", result.NewestFetchedAt, result.HasFetched)
	}
}

func TestMaintainNeedsAStore(t *testing.T) {
	if _, err := Maintain(context.Background(), MaintainOptions{}); err == nil {
		t.Fatal("Maintain ran without a store")
	}
}

// The defaults are the operating assumption, so they are pinned.
func TestMaintainDefaults(t *testing.T) {
	if DefaultClaimGrace != 15*time.Minute {
		t.Fatalf("claim grace = %s, want 15m", DefaultClaimGrace)
	}
	if DefaultFrontierRetention != 30*24*time.Hour {
		t.Fatalf("retention = %s, want 30d", DefaultFrontierRetention)
	}
	if DefaultMaxConsecutiveMiss != 5 {
		t.Fatalf("max consecutive miss = %d, want 5", DefaultMaxConsecutiveMiss)
	}
	if DefaultMaintainLimit != 1000 {
		t.Fatalf("limit = %d, want 1000", DefaultMaintainLimit)
	}

	store, deps := newMaintainHarness(t)
	store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_1"})
	if _, err := store.ClaimJobs(context.Background(), 1, testBaseTime()); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// A claim taken five minutes ago is inside the default grace, so the default
	// run must leave it alone.
	if _, err := Maintain(context.Background(), MaintainOptions{Deps: deps}); err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if got := store.jobStatus("EUW1_1"); got != "claimed" {
		t.Fatalf("status = %q, want the default grace to protect a recent claim", got)
	}
}

// retireRow puts a row through the state a global failure leaves it in: claimed,
// charged to its ceiling, and then dead-lettered. The attempt budget is the
// whole question a replay has to answer.
func retireRow(t *testing.T, store *fakeStore, matchID string, attempts int) {
	t.Helper()
	store.forceEnqueue(contract.QueueItem{MatchID: matchID, Attempts: attempts})
	if _, err := store.ClaimJobs(context.Background(), store.jobCount(), testBaseTime()); err != nil {
		t.Fatalf("claim %s: %v", matchID, err)
	}
	job := store.jobFor(matchID)
	if job == nil || job.status != "claimed" {
		t.Fatalf("row %s was not claimed", matchID)
	}
	if err := store.DeadLetterJob(context.Background(), job.item.ID, "riot match: unexpected status 403 (Forbidden)"); err != nil {
		t.Fatalf("dead-letter %s: %v", matchID, err)
	}
	if got := store.jobStatus(matchID); got != "dead" {
		t.Fatalf("status of %s = %q, want dead", matchID, got)
	}
}

// Before the fix a dead letter was terminal: no code path anywhere selected
// status = 'dead', so an outage that outlasted one row's retry budget discarded
// every match it touched and the row could only be recovered by hand-written
// SQL. Replay is the operator's way back, and it is deliberately opt-in: the
// condition that retired the rows is global, so only a human knows it has
// passed.
func TestMaintainReplaysDeadLetteredRowsOnlyWhenAsked(t *testing.T) {
	store, deps := newMaintainHarness(t)
	ids := []string{"EUW1_dead_a", "EUW1_dead_b", "EUW1_dead_c"}
	for _, id := range ids {
		retireRow(t, store, id, DefaultMaxAttempts)
	}
	store.forceEnqueue(contract.QueueItem{MatchID: "EUW1_live"})

	// Default: a poisoned row stays retired.
	result, err := Maintain(context.Background(), MaintainOptions{Deps: deps})
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if result.ReplayedDeadLetters != 0 {
		t.Fatalf("replayed = %d, want 0 without the opt-in", result.ReplayedDeadLetters)
	}
	for _, id := range ids {
		if got := store.jobStatus(id); got != "dead" {
			t.Fatalf("status of %s = %q, want it left retired", id, got)
		}
	}

	// A dry run reports and changes nothing, replay included.
	if _, err := Maintain(context.Background(), MaintainOptions{Deps: deps, ReplayDeadLetters: true, DryRun: true}); err != nil {
		t.Fatalf("Maintain dry run: %v", err)
	}
	for _, id := range ids {
		if got := store.jobStatus(id); got != "dead" {
			t.Fatalf("status of %s = %q, want a dry run to change nothing", id, got)
		}
	}

	// The real thing, bounded by the limit so an operator can replay in
	// measured batches.
	result, err = Maintain(context.Background(), MaintainOptions{Deps: deps, ReplayDeadLetters: true, Limit: 2})
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if result.ReplayedDeadLetters != 2 {
		t.Fatalf("replayed = %d, want the limit of 2", result.ReplayedDeadLetters)
	}
	replayed := 0
	for _, id := range ids {
		job := store.jobFor(id)
		switch store.jobStatus(id) {
		case "pending":
			replayed++
			if job.item.Attempts != 0 {
				t.Fatalf("replayed row %s kept %d attempts; the budget that retired it was spent on the outage",
					id, job.item.Attempts)
			}
		case "dead":
		default:
			t.Fatalf("status of %s = %q, want pending or dead", id, store.jobStatus(id))
		}
	}
	if replayed != 2 {
		t.Fatalf("rows returned to pending = %d, want 2", replayed)
	}

	// A replayed row is ordinary work again: the crawler can claim it.
	claimed, err := store.ClaimJobs(context.Background(), store.jobCount(), testBaseTime())
	if err != nil {
		t.Fatalf("ClaimJobs: %v", err)
	}
	claimedDead := 0
	for _, item := range claimed {
		switch item.MatchID {
		case "EUW1_dead_a", "EUW1_dead_b", "EUW1_dead_c":
			claimedDead++
		}
	}
	if claimedDead != 2 {
		t.Fatalf("claimed %d replayed rows, want 2", claimedDead)
	}
}
