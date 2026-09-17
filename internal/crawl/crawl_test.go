package crawl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// fixture reads a checked-in fake Riot payload. The crawl tests use the same
// bytes the fake server serves, so a change to one is a failure in both.
func fixture(t *testing.T, rel string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "fixtures", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return body
}

// fixtureMatch decodes the synthetic ranked-solo match. It goes through
// json.Unmarshal rather than the client because these tests care about the
// decoded value, not about raw retention.
func fixtureMatch(t *testing.T) riot.MatchDTO {
	t.Helper()
	var dto riot.MatchDTO
	if err := json.Unmarshal(fixture(t, "match-v5/synthetic-ranked-solo.json"), &dto); err != nil {
		t.Fatalf("decode fixture match: %v", err)
	}
	return dto
}

// testDeps builds the collaborator set the crawl commands take. Every test
// overrides one thing, so the zero-value normalisation is covered by the
// commands themselves.
func testDeps(store contract.Store, fetcher Fetcher, writer contract.RawWriter, clock riot.Clock) Deps {
	return Deps{
		Store:   store,
		Fetcher: fetcher,
		Writer:  writer,
		Region:  DefaultRegion,
		Log:     testLogger(),
		Metrics: obs.NopRecorder{},
		Clock:   clock,
		Now:     func() time.Time { return clock.Now() },
	}
}

func TestParticipantPUUIDs(t *testing.T) {
	tests := []struct {
		name string
		dto  riot.MatchDTO
		want []string
	}{
		{
			name: "metadata is the cheap path",
			dto: riot.MatchDTO{
				Metadata: riot.MatchMetadata{MatchID: "M1", Participants: []string{"a", "b"}},
				Info: riot.MatchInfo{Participants: []riot.MatchParticipant{
					{PUUID: "a"}, {PUUID: "b"},
				}},
			},
			want: []string{"a", "b"},
		},
		{
			name: "trimmed metadata falls back to the participant list",
			dto: riot.MatchDTO{
				Metadata: riot.MatchMetadata{MatchID: "M1"},
				Info: riot.MatchInfo{Participants: []riot.MatchParticipant{
					{PUUID: "a"}, {PUUID: "b"}, {PUUID: "c"},
				}},
			},
			want: []string{"a", "b", "c"},
		},
		{
			name: "duplicates collapse so the frontier stays one row per player",
			dto: riot.MatchDTO{
				Metadata: riot.MatchMetadata{Participants: []string{"a", "a", "b"}},
			},
			want: []string{"a", "b"},
		},
		{
			name: "blanks never become frontier rows",
			dto: riot.MatchDTO{
				Metadata: riot.MatchMetadata{Participants: []string{"", "a", "  "}},
			},
			want: []string{"a"},
		},
		{
			name: "no participants at all",
			dto:  riot.MatchDTO{},
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParticipantPUUIDs(tc.dto)
			if len(got) != len(tc.want) {
				t.Fatalf("ParticipantPUUIDs = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ParticipantPUUIDs = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestPatchFromGameVersion(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "16.18.612.9234", want: "16.18"},
		{in: "14.23.1.1234", want: "14.23"},
		{in: "15.1", want: "15.1"},
		{in: "", want: ""},
		{in: "16", want: ""},
		{in: "16.18", want: "16.18"},
		{in: " 16.18.612 ", want: "16.18"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := PatchFromGameVersion(tc.in); got != tc.want {
				t.Fatalf("PatchFromGameVersion(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMatchMetaFromDTO(t *testing.T) {
	dto := fixtureMatch(t)
	fetchedAt := testBaseTime()
	meta := MatchMetaFromDTO(dto, "EUW1_0000000000", "EUW", fetchedAt)

	if meta.MatchID != "EUW1_0000000000" {
		t.Fatalf("match id = %q", meta.MatchID)
	}
	if meta.Region != "EUW" {
		t.Fatalf("region = %q", meta.Region)
	}
	if meta.QueueID != dto.Info.QueueID {
		t.Fatalf("queue id = %d, want %d", meta.QueueID, dto.Info.QueueID)
	}
	if meta.Patch != PatchFromGameVersion(dto.Info.GameVersion) {
		t.Fatalf("patch = %q", meta.Patch)
	}
	if meta.GameVersion != dto.Info.GameVersion {
		t.Fatalf("game version = %q", meta.GameVersion)
	}
	if meta.GameDurationS != int(dto.Info.GameDuration) {
		t.Fatalf("duration = %d", meta.GameDurationS)
	}
	if !meta.GameCreation.Equal(time.UnixMilli(dto.Info.GameCreation).UTC()) {
		t.Fatalf("game creation = %s, want %s", meta.GameCreation, time.UnixMilli(dto.Info.GameCreation))
	}
	if !meta.FetchedAt.Equal(fetchedAt) {
		t.Fatalf("fetched at = %s", meta.FetchedAt)
	}
	if meta.PayloadVersion == "" {
		t.Fatal("payload version must be recorded for a re-readable archive")
	}
	if meta.PartitionDate() != fetchedAt.UTC().Format(time.DateOnly) {
		t.Fatalf("partition date = %q", meta.PartitionDate())
	}
}

// A fetch date that is not the game date must follow the run: a late-arriving
// match belongs to the partition of the run that fetched it.
func TestMatchMetaPartitionFollowsTheFetchDate(t *testing.T) {
	dto := fixtureMatch(t)
	fetchedAt := time.Date(2026, 3, 5, 1, 2, 3, 0, time.UTC)
	meta := MatchMetaFromDTO(dto, "M1", "EUW", fetchedAt)
	if got, want := meta.PartitionDate(), "2026-03-05"; got != want {
		t.Fatalf("PartitionDate = %q, want %q", got, want)
	}
}

func TestMatchRecordFromMeta(t *testing.T) {
	meta := contract.MatchMeta{
		MatchID:        "EUW1_1",
		Region:         "EUW",
		QueueID:        420,
		Patch:          "16.18",
		GameVersion:    "16.18.612.9234",
		GameCreation:   time.UnixMilli(1_770_000_000_000).UTC(),
		GameDurationS:  1800,
		PayloadVersion: "v1",
		FetchedAt:      testBaseTime(),
	}
	rec := MatchRecordFromMeta(meta, "raw/riot/match-v5/dt=2026-03-02/part-00001.parquet.zst")

	if rec.Status != contract.MatchFetched {
		t.Fatalf("status = %q, want fetched; the crawl stops at retention", rec.Status)
	}
	if rec.MatchID != meta.MatchID || rec.Patch != meta.Patch || rec.Region != meta.Region {
		t.Fatalf("record lost provenance: %+v", rec)
	}
	if rec.RawURI != "raw/riot/match-v5/dt=2026-03-02/part-00001.parquet.zst" {
		t.Fatalf("raw uri = %q", rec.RawURI)
	}
	if !rec.FetchedAt.Equal(meta.FetchedAt) {
		t.Fatalf("fetched at = %s", rec.FetchedAt)
	}
	if !rec.ParsedAt.IsZero() || rec.Err != "" {
		t.Fatalf("a fetched record must not carry parse state: %+v", rec)
	}
}

func TestApexTier(t *testing.T) {
	for _, tier := range []string{"CHALLENGER", "GRANDMASTER", "MASTER"} {
		if !ApexTier(tier) {
			t.Fatalf("ApexTier(%q) = false, want true", tier)
		}
	}
	// The tier arrives from an operator flag or a config file, so the check is
	// case-insensitive rather than trusting the caller's spelling.
	for _, tier := range []string{" challenger ", "grandmaster", "Master"} {
		if !ApexTier(tier) {
			t.Fatalf("ApexTier(%q) = false, want true", tier)
		}
	}
	for _, tier := range []string{"GOLD", "IRON", "DIAMOND", "", "EMERALD"} {
		if ApexTier(tier) {
			t.Fatalf("ApexTier(%q) = true, want false", tier)
		}
	}
}

func TestStaticKindsStartsWithVersions(t *testing.T) {
	kinds := StaticKinds()
	if len(kinds) == 0 {
		t.Fatal("no static kinds configured")
	}
	if kinds[0] != KindVersions {
		t.Fatalf("first static kind = %q, want %q: the patch label is read first", kinds[0], KindVersions)
	}
	seen := map[string]bool{}
	for _, kind := range kinds {
		if seen[kind] {
			t.Fatalf("duplicate static kind %q", kind)
		}
		seen[kind] = true
	}
	for _, want := range []string{KindChampions, KindItems, KindRunes, KindSummonerSpells} {
		if !seen[want] {
			t.Fatalf("static kinds %v missing %q", kinds, want)
		}
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want failureClass
	}{
		{name: "nil is transient because dropping work is worse", err: nil, want: failureTransient},
		{name: "shutdown is not a verdict on the row", err: context.Canceled, want: failureCancelled},
		{name: "a timeout is shutdown too", err: context.DeadlineExceeded, want: failureCancelled},
		{name: "wrapped cancellation still counts", err: fmt.Errorf("fetch: %w", context.Canceled), want: failureCancelled},
		{name: "rate limited", err: &riot.RateLimitedError{Method: "match", Attempts: 3}, want: failureRateLimited},
		{name: "429 status", err: &riot.StatusError{Method: "match", Status: 429}, want: failureRateLimited},
		{name: "404 is gone forever", err: &riot.StatusError{Method: "match", Status: 404}, want: failureGone},
		{name: "410 is gone forever", err: &riot.StatusError{Method: "match", Status: 410}, want: failureGone},
		{name: "400 is our bug", err: &riot.StatusError{Method: "match", Status: 400}, want: failureBadRequest},
		{name: "422 is our bug", err: &riot.StatusError{Method: "match", Status: 422}, want: failureBadRequest},
		{name: "401 is a key problem, not a match problem", err: &riot.StatusError{Method: "match", Status: 401}, want: failureKeyRefused},
		{name: "403 is a key problem", err: &riot.StatusError{Method: "match", Status: 403}, want: failureKeyRefused},
		{name: "500 is transient", err: &riot.StatusError{Method: "match", Status: 500}, want: failureTransient},
		{name: "an unclassified error retries", err: errors.New("disk full"), want: failureTransient},
		{name: "circuit breaker open is transient", err: riot.ErrCircuitOpen, want: failureTransient},
		{name: "no key is transient", err: riot.ErrNoAPIKey, want: failureTransient},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.err); got != tc.want {
				t.Fatalf("classify(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestShortCauseIsBounded(t *testing.T) {
	long := strings.Repeat("x", 400)
	if got := shortCause(errors.New(long)); len(got) != 200 {
		t.Fatalf("shortCause length = %d, want 200", len(got))
	}
	if got := shortCause(errors.New("boom")); got != "boom" {
		t.Fatalf("shortCause = %q", got)
	}
	if got := shortCause(nil); got != "" {
		t.Fatalf("shortCause(nil) = %q", got)
	}
}

// testLogger keeps the crawl output out of the test log while still exercising
// the structured logging paths.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
