package store

import (
	"context"
	"fmt"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
)

// startSeedRunSQL opens a seed audit row. The row is written before the paging
// starts, so a discoverer that is killed mid-ladder is visible as an unfinished
// run rather than as nothing at all.
const startSeedRunSQL = `
INSERT INTO crawl_seeds (tier, division, queue, region, started_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id`

// StartSeedRun records the start of a seeding pass and returns its id.
func (s *Store) StartSeedRun(ctx context.Context, run contract.SeedRun) (int64, error) {
	if run.Tier == "" || run.Division == "" {
		return 0, fmt.Errorf("store: StartSeedRun: tier and division are required")
	}
	region := run.Region
	if region == "" {
		region = s.opts.Region
	}
	startedAt := run.StartedAt
	if startedAt.IsZero() {
		startedAt = s.now()
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, startSeedRunSQL, run.Tier, run.Division, run.Queue, region, startedAt.UTC()).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: StartSeedRun: %w", err)
	}
	return id, nil
}

const finishSeedRunSQL = `
UPDATE crawl_seeds SET finished_at = $2, entries_found = $3 WHERE id = $1`

// FinishSeedRun closes a seed audit row with what it found.
func (s *Store) FinishSeedRun(ctx context.Context, id int64, finishedAt time.Time, entriesFound int) error {
	if _, err := s.db.ExecContext(ctx, finishSeedRunSQL, id, finishedAt.UTC(), entriesFound); err != nil {
		return fmt.Errorf("store: FinishSeedRun %d: %w", id, err)
	}
	return nil
}

// startBuildRunSQL opens a build audit row. Written before the build starts for
// the same reason the seed row is: a build that dies must be visible as a stuck
// row, and the aggregate binary - not this package - is the caller.
const startBuildRunSQL = `
INSERT INTO build_runs (patch, region, queue, bracket, started_at, status, git_sha)
VALUES ($1, $2, $3, $4, $5, 'running', $6)
RETURNING id`

// StartBuildRun records the start of an aggregate build.
func (s *Store) StartBuildRun(ctx context.Context, run contract.BuildRun) (int64, error) {
	if run.Patch == "" || run.Bracket == "" {
		return 0, fmt.Errorf("store: StartBuildRun: patch and bracket are required")
	}
	region := run.Region
	if region == "" {
		region = s.opts.Region
	}
	startedAt := run.StartedAt
	if startedAt.IsZero() {
		startedAt = s.now()
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, startBuildRunSQL, run.Patch, region, run.Queue, run.Bracket, startedAt.UTC(), run.GitSHA).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: StartBuildRun: %w", err)
	}
	return id, nil
}

// finishBuildRunSQL closes a build audit row.
const finishBuildRunSQL = `
UPDATE build_runs
SET finished_at = $2, status = $3, cells_total = $4, cells_published = $5,
    cells_suppressed = $6, artifact_uri = $7, error = $8
WHERE id = $1`

// buildRunStatus maps contract.BuildResult.Status onto the schema's vocabulary.
//
// The schema predates the contract (it allows 'succeeded' where the contract
// says 'ok'), and it has no place for 'quarantined'. A quarantined build is
// recorded as failed rather than as succeeded, which is the safe direction: a
// build that is held back must not read as a successful publish. The distinction
// survives in the error text.
func buildRunStatus(status string) (string, error) {
	switch status {
	case "ok":
		return "succeeded", nil
	case "failed", "quarantined":
		return "failed", nil
	case "":
		return "", fmt.Errorf("store: FinishBuildRun: status is required")
	default:
		return "", fmt.Errorf("store: FinishBuildRun: unknown status %q", status)
	}
}

// FinishBuildRun closes a build audit row with its outcome.
func (s *Store) FinishBuildRun(ctx context.Context, id int64, result contract.BuildResult) error {
	status, err := buildRunStatus(result.Status)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, finishBuildRunSQL, id, result.FinishedAt.UTC(), status,
		result.CellsTotal, result.CellsPublished, result.CellsSuppressed,
		nullText(result.ArtifactURI), nullText(result.Err))
	if err != nil {
		return fmt.Errorf("store: FinishBuildRun %d: %w", id, err)
	}
	return nil
}

// setSourceToggleSQL upserts a toggle. decided_at is refreshed only when the
// decision changes, so re-applying the same toggle from a deployment does not
// keep pushing the review date forward and hiding a stale decision.
const setSourceToggleSQL = `
INSERT INTO source_toggles (source, enabled, decided_by, decided_at, review_due_at, notes)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (source) DO UPDATE SET
    enabled       = EXCLUDED.enabled,
    decided_by    = EXCLUDED.decided_by,
    decided_at    = CASE WHEN source_toggles.enabled IS DISTINCT FROM EXCLUDED.enabled
                         THEN EXCLUDED.decided_at ELSE source_toggles.decided_at END,
    review_due_at = EXCLUDED.review_due_at,
    notes         = EXCLUDED.notes`

// SetSourceToggle writes who enabled an optional source, and when it is due for
// review.
func (s *Store) SetSourceToggle(ctx context.Context, toggle contract.SourceToggle) error {
	if toggle.Source == "" {
		return fmt.Errorf("store: SetSourceToggle: source is required")
	}
	// decided_by is NOT NULL in the schema on purpose: the compliance question
	// is "who enabled this", and a toggle with no name attached is the answer
	// that made the review necessary in the first place.
	if toggle.DecidedBy == "" {
		return fmt.Errorf("store: SetSourceToggle %s: decided_by is required", toggle.Source)
	}
	decidedAt := toggle.DecidedAt
	if decidedAt.IsZero() {
		decidedAt = s.now()
	}
	if _, err := s.db.ExecContext(ctx, setSourceToggleSQL, toggle.Source, toggle.Enabled, toggle.DecidedBy,
		decidedAt.UTC(), nullTime(toggle.ReviewDueAt), nullText(toggle.Notes)); err != nil {
		return fmt.Errorf("store: SetSourceToggle %s: %w", toggle.Source, err)
	}
	return nil
}

const sourceTogglesSQL = `
SELECT source, enabled, decided_by, decided_at, review_due_at, notes
FROM source_toggles ORDER BY source`

// SourceToggles lists every recorded toggle. A source with no row is off: the
// default lives in the absence of a decision, not in a config file.
func (s *Store) SourceToggles(ctx context.Context) ([]contract.SourceToggle, error) {
	rows, err := s.db.QueryContext(ctx, sourceTogglesSQL)
	if err != nil {
		return nil, fmt.Errorf("store: SourceToggles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []contract.SourceToggle
	for rows.Next() {
		var (
			toggle     contract.SourceToggle
			decidedAt  time.Time
			reviewDue  *time.Time
			notes      *string
			decidedBy  *string
			enabledSQL bool
		)
		if err := rows.Scan(&toggle.Source, &enabledSQL, &decidedBy, &decidedAt, &reviewDue, &notes); err != nil {
			return nil, fmt.Errorf("store: SourceToggles: %w", err)
		}
		toggle.Enabled = enabledSQL
		toggle.DecidedAt = decidedAt
		if decidedBy != nil {
			toggle.DecidedBy = *decidedBy
		}
		if reviewDue != nil {
			toggle.ReviewDueAt = *reviewDue
		}
		if notes != nil {
			toggle.Notes = *notes
		}
		out = append(out, toggle)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: SourceToggles: %w", err)
	}
	return out, nil
}
