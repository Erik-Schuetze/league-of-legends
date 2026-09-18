package crawl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
)

// timelineCandidateRecord is a match the timeline selection rule may pick: a
// stored summary inside the retention window, in the configured queue.
func timelineCandidateRecord(id string, createdAt time.Time, durationS int) contract.MatchRecord {
	return contract.MatchRecord{
		MatchID:       id,
		Region:        "EUW1",
		QueueID:       420,
		Patch:         "16.1",
		GameVersion:   "16.1.1.1",
		GameCreation:  createdAt,
		GameDurationS: durationS,
	}
}

func timelineBackfillDeps(t *testing.T, st contract.Store) (TimelineBackfillOptions, time.Time) {
	t.Helper()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	opts := TimelineBackfillOptions{
		Deps: Deps{
			Store:  st,
			Region: "EUW1",
			Now:    func() time.Time { return now },
		},
		Limit: 10,
	}
	return opts, now
}

// TestTimelineBackfillSelectsEligibleMatches covers the three ways the rule
// narrows a control plane: the retention window, the duration floor, and the
// timeline jobs already on the queue.
func TestTimelineBackfillSelectsEligibleMatches(t *testing.T) {
	st := newFakeStore()
	opts, now := timelineBackfillDeps(t, st)
	ctx := context.Background()

	// Inside the window, long enough: selected.
	if _, err := st.UpsertMatch(ctx, timelineCandidateRecord("EUW1_GOOD", now.Add(-24*time.Hour), 1800)); err != nil {
		t.Fatal(err)
	}
	// Inside the window but a short game: no participant frames exist for it.
	if _, err := st.UpsertMatch(ctx, timelineCandidateRecord("EUW1_SHORT", now.Add(-24*time.Hour), 420)); err != nil {
		t.Fatal(err)
	}
	// Older than the timeline retention horizon: a guaranteed 404.
	if _, err := st.UpsertMatch(ctx, timelineCandidateRecord("EUW1_OLD", now.Add(-400*24*time.Hour), 1800)); err != nil {
		t.Fatal(err)
	}
	// Already fetched: the sample must not re-spend budget on it.
	if _, err := st.UpsertMatch(ctx, timelineCandidateRecord("EUW1_DONE", now.Add(-24*time.Hour), 1800)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueMatches(ctx, []contract.QueueItem{{
		MatchID: "EUW1_DONE", Kind: contract.KindTimeline, Priority: PriorityTimeline,
	}}); err != nil {
		t.Fatal(err)
	}
	if items, err := st.ClaimJobsOfKind(ctx, contract.KindTimeline, 10, now); err != nil {
		t.Fatal(err)
	} else {
		for _, item := range items {
			if err := st.CompleteJob(ctx, item.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	result, err := BackfillTimelines(ctx, opts)
	if err != nil {
		t.Fatalf("BackfillTimelines: %v", err)
	}
	if result.Eligible != 3 {
		t.Errorf("eligible = %d, want 3 (the 400-day-old match is out of the window)", result.Eligible)
	}
	if result.ShortExcluded != 1 {
		t.Errorf("short excluded = %d, want 1", result.ShortExcluded)
	}
	if result.AlreadyDone != 1 {
		t.Errorf("already done = %d, want 1", result.AlreadyDone)
	}
	if result.WouldEnqueue != 1 || result.Enqueued != 1 {
		t.Errorf("would enqueue = %d, enqueued = %d, want 1 and 1", result.WouldEnqueue, result.Enqueued)
	}

	queued, err := st.ClaimJobsOfKind(ctx, contract.KindTimeline, 10, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].MatchID != "EUW1_GOOD" {
		t.Fatalf("queued = %+v, want exactly EUW1_GOOD", queued)
	}
	if queued[0].Kind != contract.KindTimeline {
		t.Errorf("kind = %q, want %q", queued[0].Kind, contract.KindTimeline)
	}
	if queued[0].Priority != PriorityTimeline {
		t.Errorf("priority = %d, want %d", queued[0].Priority, PriorityTimeline)
	}
}

// TestTimelineBackfillDryRunEnqueuesNothing is the property that makes the
// rate-limit budget knowable before it is spent: a dry run reports what a real
// run would do and writes no job.
func TestTimelineBackfillDryRunEnqueuesNothing(t *testing.T) {
	st := newFakeStore()
	opts, now := timelineBackfillDeps(t, st)
	opts.DryRun = true
	ctx := context.Background()

	if _, err := st.UpsertMatch(ctx, timelineCandidateRecord("EUW1_A", now.Add(-24*time.Hour), 1800)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertMatch(ctx, timelineCandidateRecord("EUW1_B", now.Add(-24*time.Hour), 1800)); err != nil {
		t.Fatal(err)
	}

	result, err := BackfillTimelines(ctx, opts)
	if err != nil {
		t.Fatalf("BackfillTimelines: %v", err)
	}
	if !result.DryRun {
		t.Error("DryRun = false, want true")
	}
	if result.WouldEnqueue != 2 {
		t.Errorf("would enqueue = %d, want 2", result.WouldEnqueue)
	}
	if result.Enqueued != 0 {
		t.Errorf("enqueued = %d, want 0 on a dry run", result.Enqueued)
	}
	if len(st.jobs) != 0 {
		t.Errorf("dry run wrote %d queue rows, want 0", len(st.jobs))
	}
}

// TestTimelineBackfillRespectsLimit pins the cap: -limit is what turns an
// open-ended selection into a budget an operator can pay for.
func TestTimelineBackfillRespectsLimit(t *testing.T) {
	st := newFakeStore()
	opts, now := timelineBackfillDeps(t, st)
	opts.Limit = 2
	ctx := context.Background()

	for _, id := range []string{"A", "B", "C", "D", "E"} {
		if _, err := st.UpsertMatch(ctx, timelineCandidateRecord("EUW1_"+id, now.Add(-24*time.Hour), 1800)); err != nil {
			t.Fatal(err)
		}
	}

	result, err := BackfillTimelines(ctx, opts)
	if err != nil {
		t.Fatalf("BackfillTimelines: %v", err)
	}
	if result.Ready != 5 {
		t.Errorf("ready = %d, want 5", result.Ready)
	}
	if result.WouldEnqueue != 2 || result.Enqueued != 2 {
		t.Errorf("would enqueue = %d, enqueued = %d, want 2 and 2", result.WouldEnqueue, result.Enqueued)
	}
	if len(st.jobs) != 2 {
		t.Errorf("queue rows = %d, want 2", len(st.jobs))
	}
}

// TestTimelineBackfillIncludeShortOverridesFloor pins that the duration floor
// is a default and not a rule: a short game can be asked for deliberately.
func TestTimelineBackfillIncludeShortOverridesFloor(t *testing.T) {
	st := newFakeStore()
	opts, now := timelineBackfillDeps(t, st)
	opts.IncludeShort = true
	ctx := context.Background()

	if _, err := st.UpsertMatch(ctx, timelineCandidateRecord("EUW1_SHORT", now.Add(-24*time.Hour), 300)); err != nil {
		t.Fatal(err)
	}
	result, err := BackfillTimelines(ctx, opts)
	if err != nil {
		t.Fatalf("BackfillTimelines: %v", err)
	}
	if result.ShortExcluded != 0 {
		t.Errorf("short excluded = %d, want 0 with IncludeShort", result.ShortExcluded)
	}
	if result.Enqueued != 1 {
		t.Errorf("enqueued = %d, want 1", result.Enqueued)
	}
}

// TestTimelineBackfillDefaultsClockAndHorizon pins the default horizon to the
// retention asymmetry: a run that does not say how far back to look must not
// ask for timelines Riot has already dropped.
func TestTimelineBackfillDefaultsClockAndHorizon(t *testing.T) {
	st := newFakeStore()
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	if _, err := st.UpsertMatch(ctx, timelineCandidateRecord("EUW1_RECENT", now.Add(-100*24*time.Hour), 1800)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertMatch(ctx, timelineCandidateRecord("EUW1_STALE", now.Add(-340*24*time.Hour), 1800)); err != nil {
		t.Fatal(err)
	}

	result, err := BackfillTimelines(ctx, TimelineBackfillOptions{
		Deps: Deps{
			Store:  st,
			Region: "EUW1",
			Now:    func() time.Time { return now },
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("BackfillTimelines: %v", err)
	}
	if result.Eligible != 1 || result.Enqueued != 1 {
		t.Errorf("eligible = %d, enqueued = %d, want 1 and 1 (330-day default horizon)",
			result.Eligible, result.Enqueued)
	}
	if result.Eligible > 0 && result.ShortExcluded != 0 {
		t.Errorf("short excluded = %d, want 0", result.ShortExcluded)
	}
}

// TestTimelineBackfillNeedsStore keeps the wiring honest: a backfill with no
// control plane is a programming error, not an empty selection.
func TestTimelineBackfillNeedsStore(t *testing.T) {
	_, err := BackfillTimelines(context.Background(), TimelineBackfillOptions{Limit: 1})
	if err == nil {
		t.Fatal("BackfillTimelines without a store: want error, got nil")
	}
}

// TestTimelineBackfillReportsStoreFailure makes sure a failing selection is
// reported as a failure rather than as a run that found nothing.
func TestTimelineBackfillReportsStoreFailure(t *testing.T) {
	st := newFakeStore()
	opts, _ := timelineBackfillDeps(t, st)
	opts.Deps.Store = failingCandidateStore{st}
	if _, err := BackfillTimelines(context.Background(), opts); err == nil {
		t.Fatal("want the selection error to surface")
	}
}

type failingCandidateStore struct{ *fakeStore }

func (f failingCandidateStore) TimelineCandidates(context.Context, contract.TimelineQuery) (contract.TimelineCandidates, error) {
	return contract.TimelineCandidates{}, errors.New("control plane down")
}
