package crawl

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// leagueEntries decodes one ladder fixture array into entries.
func leagueEntries(t *testing.T, rel string) []riot.LeagueEntryDTO {
	t.Helper()
	var entries []riot.LeagueEntryDTO
	if err := json.Unmarshal(fixture(t, rel), &entries); err != nil {
		t.Fatalf("decode %s: %v", rel, err)
	}
	return entries
}

func newSeedHarness(t *testing.T) (*fakeStore, *fakeFetcher, *fakeWriter, Deps) {
	t.Helper()
	store := newFakeStore()
	fetcher := newFakeFetcher("RGAPI-test-key")
	writer := newFakeWriter()
	clock := riot.NewFakeClock(testBaseTime())
	return store, fetcher, writer, testDeps(store, fetcher, writer, clock)
}

func TestDiscoverSeedsPagesADivisionAndRecordsTheRank(t *testing.T) {
	store, fetcher, writer, deps := newSeedHarness(t)
	page1 := leagueEntries(t, "riot/league-entries-gold-i.json")
	page2 := []riot.LeagueEntryDTO{
		{PUUID: "fixture-puuid-04", QueueType: "RANKED_SOLO_5x5"},
		{PUUID: "fixture-puuid-05", QueueType: "RANKED_SOLO_5x5"},
	}
	fetcher.serveLeague("RANKED_SOLO_5x5/GOLD/I/1", page1)
	fetcher.serveLeague("RANKED_SOLO_5x5/GOLD/I/2", page2)
	// Page 3 is absent, which is what an exhausted ladder looks like.

	result, err := DiscoverSeeds(context.Background(), SeedOptions{Deps: deps})
	if err != nil {
		t.Fatalf("DiscoverSeeds: %v", err)
	}
	if result.Queue != DefaultSeedQueue || result.Tier != DefaultSeedTier || result.Division != DefaultSeedDivision {
		t.Fatalf("result = %+v, want the documented defaults", result)
	}
	if result.Pages != 3 {
		t.Fatalf("pages = %d, want 3: the empty page is what ends the walk", result.Pages)
	}
	if result.Entries != len(page1)+len(page2) {
		t.Fatalf("entries = %d, want %d", result.Entries, len(page1)+len(page2))
	}
	if result.Added != len(page1)+len(page2) {
		t.Fatalf("added = %d, want %d", result.Added, len(page1)+len(page2))
	}

	entry := store.frontierEntry("fixture-puuid-01")
	if entry.Priority != PrioritySeed {
		t.Fatalf("priority = %d, want %d", entry.Priority, PrioritySeed)
	}
	if entry.SeedTier != "GOLD" || entry.SeedDivision != "I" {
		t.Fatalf("seed rank = %s/%s, want GOLD/I: the discovery-time rank is the only snapshot we get",
			entry.SeedTier, entry.SeedDivision)
	}
	if !entry.LastSeenAt.Equal(testBaseTime()) {
		t.Fatalf("last seen = %s", entry.LastSeenAt)
	}

	run := store.seedRun(result.RunID)
	if run.FinishedAt.IsZero() {
		t.Fatal("the seed run was never closed")
	}
	if run.EntriesFound != result.Entries {
		t.Fatalf("run entries = %d, want %d", run.EntriesFound, result.Entries)
	}
	if run.Queue != DefaultSeedQueue || run.Region != DefaultRegion {
		t.Fatalf("run = %+v", run)
	}

	// Every page is archived before it is turned into frontier rows: the page
	// is the evidence for the rank snapshot.
	if len(writer.pages) != 2 {
		t.Fatalf("archived %d pages, want 2", len(writer.pages))
	}
	if writer.flushCount() < 2 {
		t.Fatalf("flush count = %d, want one per page", writer.flushCount())
	}
}

// A repeated page means the walk is past the end of the ladder, even though
// Riot answered with rows.
func TestDiscoverSeedsStopsOnAPageThatAddsNothing(t *testing.T) {
	store, fetcher, _, deps := newSeedHarness(t)
	page := leagueEntries(t, "riot/league-entries-gold-i.json")
	fetcher.serveLeague("RANKED_SOLO_5x5/GOLD/I/1", page)
	fetcher.serveLeague("RANKED_SOLO_5x5/GOLD/I/2", page)

	result, err := DiscoverSeeds(context.Background(), SeedOptions{Deps: deps})
	if err != nil {
		t.Fatalf("DiscoverSeeds: %v", err)
	}
	if result.Pages != 2 {
		t.Fatalf("pages = %d, want 2: a page that adds nothing ends the walk", result.Pages)
	}
	if result.Entries != 2*len(page) {
		t.Fatalf("entries = %d, want %d", result.Entries, 2*len(page))
	}
	if result.Added != len(page) {
		t.Fatalf("added = %d, want %d: puuids dedupe", result.Added, len(page))
	}
	if got := store.FrontierSizeLocked(); got != len(page) {
		t.Fatalf("frontier size = %d, want %d", got, len(page))
	}
}

func TestDiscoverSeedsStopsAtMaxPages(t *testing.T) {
	_, fetcher, _, deps := newSeedHarness(t)
	for page := 1; page <= 5; page++ {
		fetcher.serveLeague(leagueKey(riot.LeagueQuery{
			Queue: "RANKED_SOLO_5x5", Tier: "GOLD", Division: "I", Page: page,
		}), []riot.LeagueEntryDTO{{PUUID: "puuid-" + string(rune('a'+page))}})
	}

	result, err := DiscoverSeeds(context.Background(), SeedOptions{Deps: deps, MaxPages: 3})
	if err != nil {
		t.Fatalf("DiscoverSeeds: %v", err)
	}
	if result.Pages != 3 {
		t.Fatalf("pages = %d, want the MaxPages bound of 3", result.Pages)
	}
}

// The apex tiers come back whole, so there is one request and no paging.
func TestDiscoverSeedsApexTier(t *testing.T) {
	store, fetcher, _, deps := newSeedHarness(t)
	var apex riot.ApexLeagueDTO
	if err := json.Unmarshal(fixture(t, "riot/league-challenger.json"), &apex); err != nil {
		t.Fatalf("decode apex fixture: %v", err)
	}
	fetcher.serveApex("RANKED_SOLO_5x5/CHALLENGER", apex.Entries)

	result, err := DiscoverSeeds(context.Background(), SeedOptions{Deps: deps, Tier: "challenger"})
	if err != nil {
		t.Fatalf("DiscoverSeeds: %v", err)
	}
	if result.Tier != "CHALLENGER" {
		t.Fatalf("tier = %q, want CHALLENGER", result.Tier)
	}
	if result.Division != "" {
		t.Fatalf("division = %q, want empty: an apex tier has none", result.Division)
	}
	if result.Pages != 1 {
		t.Fatalf("pages = %d, want 1", result.Pages)
	}
	if result.Added != len(apex.Entries) {
		t.Fatalf("added = %d, want %d", result.Added, len(apex.Entries))
	}
	if got := fetcher.apexCall; got != 1 {
		t.Fatalf("apex calls = %d, want 1", got)
	}
	if got := store.frontierEntry("fixture-puuid-11").SeedTier; got != "CHALLENGER" {
		t.Fatalf("seed tier = %q, want CHALLENGER: the apex entry omits its own tier", got)
	}
}

// Seeding twice must be cheap and must not move the frontier.
func TestSeedingTheSameDivisionTwiceAddsNothing(t *testing.T) {
	store, fetcher, _, deps := newSeedHarness(t)
	page := leagueEntries(t, "riot/league-entries-gold-i.json")
	fetcher.serveLeague("RANKED_SOLO_5x5/GOLD/I/1", page)

	first, err := DiscoverSeeds(context.Background(), SeedOptions{Deps: deps})
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	second, err := DiscoverSeeds(context.Background(), SeedOptions{Deps: deps})
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if first.Added != len(page) {
		t.Fatalf("first added = %d, want %d", first.Added, len(page))
	}
	if second.Added != 0 {
		t.Fatalf("second added = %d, want 0", second.Added)
	}
	if second.Entries != len(page) {
		t.Fatalf("second entries = %d, want %d: the ladder was still read", second.Entries, len(page))
	}
	if got := store.FrontierSizeLocked(); got != len(page) {
		t.Fatalf("frontier size = %d, want %d", got, len(page))
	}
	if second.RunID == first.RunID {
		t.Fatal("each pass needs its own audit row")
	}
}

// A failed pass still closes its audit row, so a thin frontier can be traced to
// a run that failed rather than to a run that found nothing.
func TestDiscoverSeedsClosesTheRunWhenTheFetchFails(t *testing.T) {
	store, fetcher, _, deps := newSeedHarness(t)
	fetcher.errors["league:RANKED_SOLO_5x5/GOLD/I/1"] = &riot.StatusError{Method: "league-entries", Status: 500}

	result, err := DiscoverSeeds(context.Background(), SeedOptions{Deps: deps})
	if err == nil {
		t.Fatal("DiscoverSeeds swallowed a page failure")
	}
	run := store.seedRun(result.RunID)
	if run.FinishedAt.IsZero() {
		t.Fatal("a failed pass left its run row open")
	}
	if run.EntriesFound != 0 {
		t.Fatalf("run entries = %d, want 0", run.EntriesFound)
	}
}

// A local archive problem fails the pass: the page is the evidence for the
// rank snapshot, so a pass that cannot retain it has nothing to show.
func TestDiscoverSeedsFailsWhenThePageCannotBeArchived(t *testing.T) {
	_, fetcher, writer, deps := newSeedHarness(t)
	fetcher.serveLeague("RANKED_SOLO_5x5/GOLD/I/1", leagueEntries(t, "riot/league-entries-gold-i.json"))
	writer.failLeague = errors.New("disk full")

	if _, err := DiscoverSeeds(context.Background(), SeedOptions{Deps: deps}); err == nil {
		t.Fatal("DiscoverSeeds ignored an archive failure")
	}
}

func TestSeedOptionsAreNormalised(t *testing.T) {
	tests := []struct {
		name string
		opts SeedOptions
		want string
	}{
		{
			name: "the zero value uses the documented defaults",
			want: "league:RANKED_SOLO_5x5/GOLD/I/1",
		},
		{
			name: "the tier and division are trimmed and upper-cased",
			opts: SeedOptions{Queue: " ranked_solo_5x5 ", Tier: " gold ", Division: " ii "},
			want: "league:ranked_solo_5x5/GOLD/II/1",
		},
		{
			name: "an apex tier needs no division",
			opts: SeedOptions{Tier: "master"},
			want: "apex:RANKED_SOLO_5x5/MASTER",
		},
		{
			name: "an apex tier keeps its queue",
			opts: SeedOptions{Queue: "RANKED_FLEX_SR", Tier: "Challenger"},
			want: "apex:RANKED_FLEX_SR/CHALLENGER",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, fetcher, _, deps := newSeedHarness(t)
			tc.opts.Deps = deps
			if _, err := DiscoverSeeds(context.Background(), tc.opts); err != nil {
				t.Fatalf("DiscoverSeeds: %v", err)
			}
			calls := fetcher.allCalls()
			if len(calls) != 1 {
				t.Fatalf("calls = %v, want exactly one league request", calls)
			}
			if calls[0] != tc.want {
				t.Fatalf("call = %q, want %q", calls[0], tc.want)
			}
		})
	}
}

// The defaults are the contract between this pass and the operator.
func TestSeedResultCarriesTheNormalisedRank(t *testing.T) {
	_, _, _, deps := newSeedHarness(t)
	result, err := DiscoverSeeds(context.Background(), SeedOptions{Deps: deps, Tier: "grandmaster"})
	if err != nil {
		t.Fatalf("DiscoverSeeds: %v", err)
	}
	if result.Queue != DefaultSeedQueue {
		t.Fatalf("queue = %q, want %q", result.Queue, DefaultSeedQueue)
	}
	if result.Tier != "GRANDMASTER" {
		t.Fatalf("tier = %q, want GRANDMASTER", result.Tier)
	}
	if result.Division != "" {
		t.Fatalf("division = %q, want empty for an apex tier", result.Division)
	}
}

func TestDiscoverSeedsNeedsAStoreAndAFetcher(t *testing.T) {
	_, fetcher, writer, deps := newSeedHarness(t)
	if _, err := DiscoverSeeds(context.Background(), SeedOptions{Deps: Deps{Fetcher: fetcher, Writer: writer}}); err == nil {
		t.Fatal("DiscoverSeeds ran without a store")
	}
	store := newFakeStore()
	if _, err := DiscoverSeeds(context.Background(), SeedOptions{Deps: Deps{Store: store, Writer: writer, Log: testLogger()}}); err == nil {
		t.Fatal("DiscoverSeeds ran without a fetcher")
	}
	_ = deps
}

func TestFrontierEntriesSnapshotTheRank(t *testing.T) {
	_, _, _, deps := newSeedHarness(t)
	opts := SeedOptions{Deps: deps, Queue: DefaultSeedQueue, Tier: "GOLD", Division: "I"}
	entries := []riot.LeagueEntryDTO{
		{PUUID: " p1 ", Tier: "diamond", Rank: "ii"},
		{PUUID: "p2"},
		{PUUID: ""},
		{PUUID: "p2"},
	}
	got := frontierEntries(entries, opts, opts.Division)

	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2: blanks are dropped and duplicates collapse", len(got))
	}
	if got[0].PUUID != "p1" {
		t.Fatalf("puuid = %q, want the trimmed value", got[0].PUUID)
	}
	if got[0].SeedTier != "DIAMOND" || got[0].SeedDivision != "II" {
		t.Fatalf("rank = %s/%s, want the entry's own rank", got[0].SeedTier, got[0].SeedDivision)
	}
	if got[1].SeedTier != "GOLD" || got[1].SeedDivision != "I" {
		t.Fatalf("rank = %s/%s, want the requested division as the fallback", got[1].SeedTier, got[1].SeedDivision)
	}
	for _, entry := range got {
		if entry.Priority != PrioritySeed {
			t.Fatalf("priority = %d, want %d", entry.Priority, PrioritySeed)
		}
	}
}
