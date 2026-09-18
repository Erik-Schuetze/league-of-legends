package riot

import (
	"os"
	"strings"
	"sync"
	"time"
)

// EnvAPIKey is the environment variable holding the Riot key. It matches the
// name the config package reads, so a process started for the crawler does not
// need two different spellings of the same secret.
const EnvAPIKey = "LOLSTATS_RIOT_API_KEY" //nolint:gosec // an environment variable name, not a credential

// EnvAPIKeyFile is the environment variable naming a file that holds the key.
//
// It exists because a development key expires every 24 hours: the operator
// writes the new key into this file and the running worker picks it up on its
// next request, with no restart and no second key in the environment. The file
// wins over the environment variable when both are present, because writing a
// file is an explicit act and setting an environment variable often is not.
const EnvAPIKeyFile = "LOLSTATS_RIOT_API_KEY_FILE" //nolint:gosec // an environment variable name, not a credential

// keyFileTTL is how long a read key is trusted before the file is stat'd again.
// Short enough that a rotation is picked up within a minute, long enough that a
// crawl does not stat a file on every request.
const keyFileTTL = 30 * time.Second

// KeyProvider resolves the Riot key on each request, optionally from a file
// that may be rewritten underneath it.
//
// It never fails hard. A worker with no key is a worker that serves metrics and
// logs one warning - not a CrashLoopBackOff that hides the real problem, and not
// a process that exits the moment a 24-hour key expires mid-run.
type KeyProvider struct {
	file string
	env  string
	now  func() time.Time
	ttl  time.Duration

	mu        sync.Mutex
	current   string
	firstSeen time.Time
	lastRead  time.Time
	fileErr   error
}

// NewKeyProvider reads LOLSTATS_RIOT_API_KEY_FILE and LOLSTATS_RIOT_API_KEY from
// the environment. Both may be empty, which is a supported configuration.
func NewKeyProvider() *KeyProvider {
	return NewKeyProviderFrom(os.Getenv(EnvAPIKey), os.Getenv(EnvAPIKeyFile))
}

// NewKeyProviderFrom is the injectable form, used by tests and by callers that
// already resolved the configuration.
func NewKeyProviderFrom(envKey, file string) *KeyProvider {
	return &KeyProvider{
		env:  strings.TrimSpace(envKey),
		file: strings.TrimSpace(file),
		now:  time.Now,
		ttl:  keyFileTTL,
	}
}

// Key returns the key to send and whether one is available at all.
func (k *KeyProvider) Key() (string, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()

	now := k.now()
	if k.file != "" && (k.current == "" || now.Sub(k.lastRead) >= k.ttl) {
		k.lastRead = now
		if data, err := os.ReadFile(k.file); err != nil {
			// A missing or unreadable file keeps the last good key: an
			// operator rotating a key may delete and recreate the file,
			// and a crawl stopping in that instant would be a bug.
			k.fileErr = err
		} else if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
			k.fileErr = nil
			k.set(trimmed, now)
		}
	}
	if k.current == "" && k.env != "" {
		k.set(k.env, now)
	}
	return k.current, k.current != ""
}

// set records the key material and, when it changed, restarts the age clock.
// Age is what the 24-hour key alert watches, so a rotation must reset it.
func (k *KeyProvider) set(key string, now time.Time) {
	if key != k.current {
		k.current = key
		k.firstSeen = now
	}
}

// AgeKnown is how long the current key has been in use by this process, and
// whether that is known at all. A development key expires after 24 hours, so
// this is the number the alert and the maintain job read.
//
// The second result is what makes the reading usable. A provider that holds no
// key has no age, and answering 0 is indistinguishable from a key rotated a
// moment ago - two readings that call for opposite actions, which is how a
// gauge that always read 0 stayed unnoticed. The crawl worker asks for both
// results and publishes the metric only when the age is known.
func (k *KeyProvider) AgeKnown() (time.Duration, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.current == "" {
		return 0, false
	}
	return k.now().Sub(k.firstSeen), true
}

// Age is the one-value form the readiness probe prints. It reads 0 when the age
// is unknown, so a caller that publishes the number rather than displaying it
// asks AgeKnown instead.
func (k *KeyProvider) Age() time.Duration {
	age, _ := k.AgeKnown()
	return age
}

// Source describes where the key came from, for the startup log. It names the
// mechanism and never the key.
func (k *KeyProvider) Source() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	switch {
	case k.current == "":
		return "none"
	case k.file != "" && k.fileErr == nil:
		return "file"
	default:
		return "environment"
	}
}

// Rotated reports whether the provider currently holds a key loaded from the
// file, which is how the worker notices a rotation worth logging.
func (k *KeyProvider) Rotated() bool {
	return k.Source() == "file"
}
