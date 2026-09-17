package riot

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fixturePath reaches the repository's fixtures directory from this package.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "fixtures", "match-v5", name)
}

// TestSyntheticMatchFixtureDecodes is the guard on the frozen DTO: the fixture
// is a specification of what MATCH-V5 carries, so a field that stops decoding is
// a contract break rather than a stale test. DisallowUnknownFields is the other
// half - it catches a fixture that has drifted into carrying fields nothing
// reads, which is how a DTO quietly grows.
func TestSyntheticMatchFixtureDecodes(t *testing.T) {
	raw, err := os.ReadFile(fixturePath(t, "synthetic-ranked-solo.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var got MatchDTO
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	if got.Metadata.MatchID != "EUW1_0000000000" {
		t.Errorf("matchId = %q", got.Metadata.MatchID)
	}
	if len(got.Info.Participants) != 10 {
		t.Fatalf("participants = %d, want 10", len(got.Info.Participants))
	}
	if len(got.Metadata.Participants) != len(got.Info.Participants) {
		t.Errorf("metadata participants = %d, info participants = %d",
			len(got.Metadata.Participants), len(got.Info.Participants))
	}
	if got.Info.QueueID != 420 {
		t.Errorf("queueId = %d, want 420", got.Info.QueueID)
	}

	roles := map[string]int{}
	for _, p := range got.Info.Participants {
		roles[p.TeamPosition]++
		if p.ChampionID == 0 {
			t.Errorf("participant %s has no champion", p.PUUID)
		}
		if len(p.Perks.Styles) != 3 {
			t.Errorf("participant %s has %d perk styles, want 3", p.PUUID, len(p.Perks.Styles))
		}
	}
	for _, role := range []string{"TOP", "JUNGLE", "MIDDLE", "BOTTOM", "UTILITY"} {
		if roles[role] != 2 {
			t.Errorf("role %s appears %d times, want 2", role, roles[role])
		}
	}

	// The zero-champion ban is the shape that a naive ban-rate calculation
	// counts as a champion, so the fixture has to keep at least one.
	bans, empty := 0, 0
	for _, team := range got.Info.Teams {
		for _, ban := range team.Bans {
			bans++
			if ban.ChampionID == 0 {
				empty++
			}
		}
	}
	if bans != 10 {
		t.Errorf("bans = %d, want 10", bans)
	}
	if empty == 0 {
		t.Error("fixture has no empty ban; the championId 0 case is no longer covered")
	}

	// Empty item slots are the other zero-means-absent case.
	emptySlots := 0
	for _, p := range got.Info.Participants {
		for _, id := range []int{p.Item0, p.Item1, p.Item2, p.Item3, p.Item4, p.Item5, p.Item6} {
			if id == 0 {
				emptySlots++
			}
		}
	}
	if emptySlots == 0 {
		t.Error("fixture has no empty item slot; that case is no longer covered")
	}
}
