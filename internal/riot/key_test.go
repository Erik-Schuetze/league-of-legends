package riot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A provider with no key has no age to report, and the report has to say so.
// The defect this pins: the only writer of lolstats_riot_key_age_seconds sat
// behind a two-value Age assertion that the real client could not satisfy, so
// the number was never a measurement - it was the gauge's default, and a
// default of 0 is exactly what a key rotated a moment ago reads as.
func TestKeyAgeIsKnownOnlyOnceAKeyExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	now := testStart
	keys := NewKeyProviderFrom("", path)
	keys.ttl = 0
	keys.now = func() time.Time { return now }

	if age, known := keys.AgeKnown(); known || age != 0 {
		t.Fatalf("age = %s, known = %v, want an unknown age, not a duration", age, known)
	}
	if _, ok := keys.Key(); ok {
		t.Fatal("expected no key to be configured")
	}
	if age, known := keys.AgeKnown(); known || age != 0 {
		t.Fatalf("age after a failed lookup = %s, known = %v, want unknown", age, known)
	}

	if err := os.WriteFile(path, []byte("RGAPI-one\n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if _, ok := keys.Key(); !ok {
		t.Fatal("expected the key file to be read")
	}
	now = now.Add(90 * time.Minute)
	age, known := keys.AgeKnown()
	if !known {
		t.Fatal("a provider holding a key reported its age as unknown")
	}
	if age != 90*time.Minute {
		t.Fatalf("age = %s, want 90m", age)
	}
	// The one-value form the readiness probe prints stays available; it is the
	// lossy reading, which is why nothing that publishes a metric uses it.
	if keys.Age() != 90*time.Minute {
		t.Fatalf("Age = %s, want the same 90m", keys.Age())
	}

	// A rotation restarts the clock, and the age stays known across it: an
	// operator exchanging the key has not made the new key's age unknown.
	if err := os.WriteFile(path, []byte("RGAPI-two\n"), 0o600); err != nil {
		t.Fatalf("rewrite key: %v", err)
	}
	if _, ok := keys.Key(); !ok {
		t.Fatal("expected the rotated key")
	}
	if age, known := keys.AgeKnown(); !known || age != 0 {
		t.Fatalf("age after rotation = %s, known = %v, want a known 0", age, known)
	}
}

// The crawl worker discovers the key surface by asserting on its Fetcher, which
// is the client, and it is that assertion which publishes the gauge. Until the
// client carried the method, the assertion was satisfied by the crawl test fake
// alone - the production type it stands in for was missing it, and no test
// could see that. This asserts the real client answers the same question.
func TestTheRealClientAnswersTheWorkersKeyAgeAssertion(t *testing.T) {
	var subject any = newKeyAgeTestClient(t, NewKeyProviderFrom("", filepath.Join(t.TempDir(), "absent")))

	k, ok := subject.(interface {
		Age() (time.Duration, bool)
	})
	if !ok {
		t.Fatal("the real client does not satisfy the crawl worker's key-age assertion")
	}
	if age, known := k.Age(); known || age != 0 {
		t.Fatalf("age = %s, known = %v, want unknown with no key", age, known)
	}

	now := testStart
	keys := NewKeyProviderFrom("RGAPI-environment", "")
	keys.now = func() time.Time { return now }
	client := newKeyAgeTestClient(t, keys)
	if _, ok := client.Key(); !ok {
		t.Fatal("expected the environment key")
	}
	now = now.Add(3 * time.Hour)
	age, known := client.Age()
	if !known || age != 3*time.Hour {
		t.Fatalf("age = %s, known = %v, want a known 3h", age, known)
	}
}

func newKeyAgeTestClient(t *testing.T, keys *KeyProvider) *Client {
	t.Helper()
	client, err := NewClient(Options{
		PlatformBaseURL: "https://platform.example",
		RegionalBaseURL: "https://regional.example",
		KeyProvider:     keys,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}
