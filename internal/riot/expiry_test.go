package riot

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Absence of a declaration is not an expiry. Every caller that does not set
// LOLSTATS_RIOT_API_KEY_EXPIRES_AT gets the zero value, and the zero value has
// to keep crawling: the key's age is a warning, not a verdict.
func TestAnUndeclaredKeyNeverExpires(t *testing.T) {
	var none KeyExpiry
	if none.Declared() {
		t.Fatal("the zero KeyExpiry declares an expiry")
	}
	if err := none.Check(testStart); err != nil {
		t.Fatalf("Check on an undeclared expiry = %v, want nil", err)
	}
	if _, ok := none.Remaining(testStart); ok {
		t.Fatal("Remaining reported a duration for an undeclared expiry")
	}
}

func TestDeclaredExpiryIsJudgedAtTheBoundary(t *testing.T) {
	expires := testStart.Add(6 * time.Hour)
	declared := NewKeyExpiry(expires)

	if err := declared.Check(expires.Add(-time.Second)); err != nil {
		t.Fatalf("one second before the deadline: %v, want nil", err)
	}
	// The deadline itself is the first instant the key is gone: a key that is
	// valid "until 12:00" is not valid at 12:00.
	if err := declared.Check(expires); !errors.Is(err, ErrKeyExpired) {
		t.Fatalf("at the deadline: %v, want ErrKeyExpired", err)
	}
	err := declared.Check(expires.Add(28 * time.Minute))
	if !errors.Is(err, ErrKeyExpired) {
		t.Fatalf("past the deadline: %v, want ErrKeyExpired", err)
	}

	var expired *KeyExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("err = %T, want a *KeyExpiredError so a caller can read the times", err)
	}
	if !expired.ExpiredAt.Equal(expires) {
		t.Fatalf("ExpiredAt = %s, want %s", expired.ExpiredAt, expires)
	}
	if !expired.Now.Equal(expires.Add(28 * time.Minute)) {
		t.Fatalf("Now = %s, want the time of the check", expired.Now)
	}
}

// The message is the operator's whole handover: it has to say what happened,
// when, and what to do. It must never carry the key itself - not even a prefix,
// because these strings are copied into issues and chat.
func TestTheExpiryMessageNamesTheFixAndNotTheKey(t *testing.T) {
	key := "RGAPI-fixture-key"
	expires := testStart.Add(-30 * time.Minute)
	err := NewKeyExpiry(expires).Check(testStart)
	if err == nil {
		t.Fatal("Check returned nil for a deadline in the past")
	}
	msg := err.Error()
	for _, want := range []string{
		"expired",
		expires.UTC().Format(time.RFC3339),
		"30m0s ago",
		"lolstats-riot",
		"rotate",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}
	if strings.Contains(msg, key) {
		t.Errorf("message %q carries key material", msg)
	}
}

func TestRemainingReportsTheTimeUntilTheDeadline(t *testing.T) {
	declared := NewKeyExpiry(testStart.Add(90 * time.Minute))
	remaining, ok := declared.Remaining(testStart)
	if !ok {
		t.Fatal("Remaining reported nothing for a declared expiry")
	}
	if remaining != 90*time.Minute {
		t.Fatalf("Remaining = %s, want 90m", remaining)
	}
	// Past the deadline the remaining time is negative rather than hidden, so
	// a caller that only wants to log it does not need a second branch.
	if late, _ := declared.Remaining(testStart.Add(2 * time.Hour)); late != -30*time.Minute {
		t.Fatalf("Remaining past the deadline = %s, want -30m", late)
	}
}
