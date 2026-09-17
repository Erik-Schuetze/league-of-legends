package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
)

// upsertMatchSQL is the one write path that creates a `matches` row. It only
// runs after the payload is in the archive, so the row it inserts always
// describes data that exists.
//
// DO NOTHING rather than DO UPDATE is the dedupe guarantee: a match that is
// re-crawled (because another participant's history pointed at it, or because
// a rate-limit retry was replayed) must not move its provenance, its
// `fetched_at` or its `parsed_at`. The conflict returns no row, which is how the
// caller learns that the payload was already known.
const upsertMatchSQL = `
INSERT INTO matches (
    match_id, region, queue_id, patch, game_version, game_creation,
    game_duration_s, payload_version, raw_uri, status, fetched_at, parsed_at, error
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (match_id) DO NOTHING
RETURNING match_id`

// UpsertMatch records the provenance of an archived payload. The bool reports
// whether this call is the one that inserted the row.
func (s *Store) UpsertMatch(ctx context.Context, rec contract.MatchRecord) (bool, error) {
	if rec.MatchID == "" {
		return false, errors.New("store: UpsertMatch: empty match_id")
	}
	if rec.RawURI == "" {
		return false, errors.New("store: UpsertMatch: empty raw_uri")
	}
	region := rec.Region
	if region == "" {
		region = s.opts.Region
	}
	version, err := payloadVersion(rec.PayloadVersion)
	if err != nil {
		return false, fmt.Errorf("store: UpsertMatch %s: %w", rec.MatchID, err)
	}
	status := rec.Status
	if status == "" {
		status = contract.MatchFetched
	}

	var id string
	err = s.db.QueryRowContext(ctx, upsertMatchSQL,
		rec.MatchID, region, rec.QueueID, rec.Patch, rec.GameVersion, rec.GameCreation.UTC(),
		rec.GameDurationS, version, rec.RawURI, string(status), nullTime(rec.FetchedAt),
		nullTime(rec.ParsedAt), nullText(rec.Err),
	).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("store: UpsertMatch %s: %w", rec.MatchID, err)
	}
	s.metrics.AddMatchesPersisted(1)
	return true, nil
}

const markMatchParsedSQL = `
UPDATE matches SET status = $2, parsed_at = $3, error = NULL WHERE match_id = $1`

// MarkMatchParsed moves a match to the parsed state. The previous error, if any,
// is cleared: a match that parses is not still failing.
func (s *Store) MarkMatchParsed(ctx context.Context, matchID string, parsedAt time.Time) error {
	if _, err := s.db.ExecContext(ctx, markMatchParsedSQL, matchID, string(contract.MatchParsed), nullTime(parsedAt)); err != nil {
		return fmt.Errorf("store: MarkMatchParsed %s: %w", matchID, err)
	}
	return nil
}

const markMatchFailedSQL = `
UPDATE matches SET status = $2, error = $3 WHERE match_id = $1`

// MarkMatchFailed records a parse failure. The match stays in the table: it was
// fetched, it is archived, and forgetting it would only cause a re-fetch.
func (s *Store) MarkMatchFailed(ctx context.Context, matchID string, cause string) error {
	if _, err := s.db.ExecContext(ctx, markMatchFailedSQL, matchID, string(contract.MatchFailed), nullText(cause)); err != nil {
		return fmt.Errorf("store: MarkMatchFailed %s: %w", matchID, err)
	}
	return nil
}

const matchExistsSQL = `SELECT EXISTS (SELECT 1 FROM matches WHERE match_id = $1)`

// MatchExists reports whether a match is already in the control plane.
//
// It is not on contract.Store: the crawler does not ask, it writes and lets the
// upsert decide. It exists for the backfill command, which is the one caller
// that can save a request per key by asking first.
func (s *Store) MatchExists(ctx context.Context, matchID string) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, matchExistsSQL, matchID).Scan(&exists); err != nil {
		return false, fmt.Errorf("store: MatchExists %s: %w", matchID, err)
	}
	return exists, nil
}
