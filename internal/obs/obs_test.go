package obs

import "testing"

// The key-age gauge may only exist once a real age has been published. A
// registered Gauge is scraped as 0 from the moment the process starts, and 0 is
// what a key rotated a moment ago reads as - which is how a gauge whose only
// call site could never be reached stayed unnoticed for two days.
func TestRiotKeyAgeIsAbsentUntilAKnownAgeIsPublished(t *testing.T) {
	m := NewMetrics()

	if _, present := riotKeyAgeValue(t, m); present {
		t.Fatal("lolstats_riot_key_age_seconds exists before an age is known")
	}

	m.SetRiotKeyAge(13 * 3600)

	value, present := riotKeyAgeValue(t, m)
	if !present {
		t.Fatal("lolstats_riot_key_age_seconds did not appear after a known age was published")
	}
	if value != 13*3600 {
		t.Fatalf("value = %v, want %v", value, 13*3600)
	}
}

// riotKeyAgeValue reports the published age, and whether the series exists at
// all - absence is the reading this test is really about.
func riotKeyAgeValue(t *testing.T, m *Metrics) (float64, bool) {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "lolstats_riot_key_age_seconds" {
			continue
		}
		samples := family.GetMetric()
		if len(samples) == 0 {
			return 0, false
		}
		return samples[0].GetGauge().GetValue(), true
	}
	return 0, false
}
