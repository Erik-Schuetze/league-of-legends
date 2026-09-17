// Package store implements the Postgres control plane declared by
// contract.Store: dedupe state for matches, the fetch queue, the crawl
// frontier, and the audit rows for seed and build runs.
//
// Two properties drive every query in this package:
//
//   - The crawler must be safe to run concurrently with itself. Anything that
//     hands out work - ClaimJobs, ClaimFrontier - goes through
//     `FOR UPDATE SKIP LOCKED`, so two workers can never receive the same row.
//     A "claim" that both workers see is a duplication bug that no amount of
//     care in the caller can repair.
//
//   - A restart must not lose or repeat work. Every state change is a single
//     statement with a WHERE clause that encodes the state it expects, which
//     makes the operation idempotent under an unknown number of retries: the
//     second execution changes nothing and says so through the rows-affected
//     count rather than through an error.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	// pgx's database/sql driver. The whole package is written against
	// database/sql rather than pgx's native interface so that a test can drive
	// it with a mock driver, and so that the SQL stays visible as SQL: the
	// interesting parts of this package are the statements and the locking
	// they do, not the driver.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
)

const (
	defaultMaxConns         = 8
	defaultConnTimeout      = 10 * time.Second
	defaultFrontierCooldown = 6 * time.Hour

	// defaultRegion labels rows whose caller left the region empty. Every
	// provenance column in the schema is NOT NULL, so a region-less row would
	// otherwise be rejected by the database rather than by the caller's intent.
	defaultRegion = "EUW"

	// maxClaimLimit bounds a single claim. A limit this large is already more
	// work than one batch can archive between two heartbeats; beyond it the
	// extra rows would only be rows held out of other workers' reach.
	maxClaimLimit = 1000
)

// Options configures Open. Everything has a default that is safe for the
// single-node development deployment the pipeline actually runs on.
type Options struct {
	DSN string
	// MaxConns is the pool ceiling. It is small on purpose: the crawler is
	// rate-limited by Riot long before it is limited by Postgres.
	MaxConns int
	// ConnTimeout bounds the startup connectivity check only.
	ConnTimeout time.Duration
	// Region is the default region written to frontier entries whose caller
	// left it empty, so a frontier row can never be region-less.
	Region string
	// FrontierCooldown is how long a claimed PUUID stays out of the frontier
	// after it is handed to a worker.
	FrontierCooldown time.Duration
	Metrics          obs.MetricsRecorder
	// Now is the clock. Nil means time.Now. It is injected rather than
	// defaulted because "not_before <= now" is the whole retry policy.
	Now func() time.Time
}

// Store is the Postgres control plane. It is safe for concurrent use: it holds
// no state beyond the pool and the immutable options.
type Store struct {
	db      *sql.DB
	opts    Options
	now     func() time.Time
	metrics obs.MetricsRecorder
}

var _ contract.Store = (*Store)(nil)

// Open connects, verifies the connection and returns a store. Failing here
// rather than on the first query is deliberate: a process that starts, logs
// "ready" and only then discovers that the database is wrong is a process whose
// readiness signal lied.
func Open(ctx context.Context, opts Options) (*Store, error) {
	if opts.DSN == "" {
		return nil, errors.New("store: DSN is required")
	}
	db, err := sql.Open("pgx", opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	s := newStore(db, opts)

	timeout := opts.ConnTimeout
	if timeout <= 0 {
		timeout = defaultConnTimeout
	}
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := s.Ping(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	return s, nil
}

// newStore wires a pool that the caller already owns. Tests use it directly.
func newStore(db *sql.DB, opts Options) *Store {
	if opts.MaxConns <= 0 {
		opts.MaxConns = defaultMaxConns
	}
	if opts.Region == "" {
		opts.Region = defaultRegion
	}
	if opts.FrontierCooldown <= 0 {
		opts.FrontierCooldown = defaultFrontierCooldown
	}
	db.SetMaxOpenConns(opts.MaxConns)
	db.SetMaxIdleConns(opts.MaxConns)
	db.SetConnMaxLifetime(time.Hour)
	db.SetConnMaxIdleTime(5 * time.Minute)

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	metrics := opts.Metrics
	if metrics == nil {
		metrics = obs.NopRecorder{}
	}
	return &Store{db: db, opts: opts, now: now, metrics: metrics}
}

// Close releases the pool. The caller owns the shutdown order: the archive
// writer is flushed before the store is closed, never after.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Ping reports whether the database answers. A failing ping is not fatal
// anywhere in the crawler: it means "not ready", which the health endpoint
// reports and the worker retries.
func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("store: closed")
	}
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("store: ping: %w", err)
	}
	return nil
}

// Region is the region new frontier entries are labelled with when the caller
// leaves it empty.
func (s *Store) Region() string { return s.opts.Region }

// Now exposes the store's clock so callers can hand the same instant to a claim
// and to the retry that follows it.
func (s *Store) Now() time.Time { return s.now() }

func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return 0
	case limit > maxClaimLimit:
		return maxClaimLimit
	default:
		return limit
	}
}

// payloadVersion converts the archive's string version tag into the integer the
// schema stores. Recorded as a number because the control plane must be able to
// say "everything at version 2 is drained" without string comparison.
func payloadVersion(v string) (int, error) {
	if v == "" {
		return defaultPayloadVersion, nil
	}
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("store: payload version %q is not a number", v)
		}
		n = n*10 + int(r-'0')
		if n > 1<<20 {
			return 0, fmt.Errorf("store: payload version %q is out of range", v)
		}
	}
	return n, nil
}

const defaultPayloadVersion = 1

// nullTime renders a zero time as SQL NULL. A zero time in the schema would be
// year 1, which is a lie of a different kind than "not yet".
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

// nullText renders an empty cause as SQL NULL, so "no error recorded" and
// "error recorded as the empty string" stay distinguishable.
func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
