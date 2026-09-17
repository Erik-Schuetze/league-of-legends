package riot

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrNoAPIKey is returned instead of making a call when no key is configured.
// It is a distinct error rather than a 401 because "nobody has given us a key"
// and "Riot rejected the key we have" need different operator responses: the
// first is a configuration fact, the second is an incident.
var ErrNoAPIKey = errors.New("riot: no API key configured")

// ErrCircuitOpen is returned while the breaker is tripped. Callers are expected
// to treat it as "come back later" rather than "this match failed", so that a
// key-level problem does not burn the queue's attempt budget row by row.
var ErrCircuitOpen = errors.New("riot: circuit breaker open")

// StatusError is a non-retryable HTTP status from Riot.
type StatusError struct {
	// Method is the client-side label ("match", "league-entries"), not the
	// HTTP method, because that is the label the metrics and logs carry.
	Method string
	Status int
	// Body is truncated; it exists for the operator log, not for parsing.
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("riot %s: unexpected status %d (%s)", e.Method, e.Status, http.StatusText(e.Status))
}

// RateLimitedError is returned when the request budget ran out while Riot kept
// answering 429. It carries the last Retry-After so the caller can decide
// whether to re-queue the row with a matching not_before.
type RateLimitedError struct {
	Method     string
	Attempts   int
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("riot %s: rate limited after %d attempts (retry after %s)", e.Method, e.Attempts, e.RetryAfter)
}
