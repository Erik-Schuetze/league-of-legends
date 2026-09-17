package riot

import (
	"errors"
	"fmt"
	"time"
)

// ErrKeyExpired is the sentinel behind every expiry refusal. It is exported so
// a command can tell "the key is dead" apart from "the network is down" and
// choose its exit code without matching on a string.
var ErrKeyExpired = errors.New("the Riot API key has expired")

// KeyExpiredError reports a key whose declared lifetime has passed.
//
// The owner's key is a development key, which Riot expires every 24 hours. The
// operator can record when that happens (LOLSTATS_RIOT_API_KEY_EXPIRES_AT);
// when they do, that declaration is a *fact about the key* and it is the one
// expiry signal a process can act on without spending a request to discover it.
// Left unacted on, an expired key produces the worst of both worlds: the
// pipeline keeps running, keeps spending the day's request budget on refusals,
// and keeps the last snapshot on the shelf so that the site serves it as if it
// were current.
type KeyExpiredError struct {
	// ExpiredAt is when the key stopped working, as declared.
	ExpiredAt time.Time
	// Now is the time of the check, so the message can say how far past the
	// deadline the process is running rather than leaving that to arithmetic.
	Now time.Time
}

func (e *KeyExpiredError) Error() string {
	return fmt.Sprintf(
		"the Riot API key expired at %s (%s ago): rotate it in secret/lolstats-riot (or the file named by LOLSTATS_RIOT_API_KEY_FILE) and restart; crawling and publishing stay stopped until a working key is in place",
		e.ExpiredAt.UTC().Format(time.RFC3339), e.Now.Sub(e.ExpiredAt).Truncate(time.Second))
}

// Unwrap is what makes errors.Is(err, ErrKeyExpired) work for callers that know
// the condition rather than the type.
func (e *KeyExpiredError) Unwrap() error { return ErrKeyExpired }

// KeyExpiry is the operator's declaration of when the Riot key stops working.
//
// The zero value means "not declared", and it is the zero value that every
// caller gets by default: nothing here fails on age alone, because the age of a
// key is not the same fact as its death. A development key dies after 24 hours,
// a production key does not, and KeyProvider cannot tell which it holds - so an
// undeclared key is reported by Age (the warning path that already exists) and
// its actual death is reported by Riot, as a 401 or 403 on the first call.
// KeyExpiry exists for the case the operator *did* write the deadline down, and
// then the pipeline can stop before it burns a request finding out.
type KeyExpiry struct {
	at time.Time
}

// NewKeyExpiry declares the key's expiry. A zero time declares nothing.
func NewKeyExpiry(at time.Time) KeyExpiry {
	return KeyExpiry{at: at.UTC()}
}

// Declared reports whether an expiry was written down at all.
func (e KeyExpiry) Declared() bool { return !e.at.IsZero() }

// At is the declared expiry, or the zero time when none was declared.
func (e KeyExpiry) At() time.Time { return e.at }

// Remaining is how long the key has left, and whether that is known.
func (e KeyExpiry) Remaining(now time.Time) (time.Duration, bool) {
	if !e.Declared() {
		return 0, false
	}
	return e.at.Sub(now), true
}

// Check reports the declared expiry as an error once it has passed, and nil
// otherwise - including when nothing was declared, which is not an error, only
// an absence of information.
func (e KeyExpiry) Check(now time.Time) error {
	if !e.Declared() || now.Before(e.at) {
		return nil
	}
	return &KeyExpiredError{ExpiredAt: e.at, Now: now.UTC()}
}
