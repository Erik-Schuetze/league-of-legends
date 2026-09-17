package aggregate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
)

// The audit trail.
//
// Every completed build attempts to close a `build_runs` row through the frozen
// contract.Store interface. Two properties matter more than which store is
// behind it:
//
//   - the row is written even when the build fails, so a patch that stops
//     publishing is visible as a run with status failed or quarantined rather
//     than as silence;
//   - a store that is missing or unreachable must never turn a published
//     artifact into a failed build. The numbers are computed from an immutable
//     archive and are correct whether or not Postgres was up, so the audit
//     failure is reported and recorded on disk, and the publish stands.
type Auditor interface {
	StartBuildRun(ctx context.Context, run contract.BuildRun) (int64, error)
	FinishBuildRun(ctx context.Context, id int64, result contract.BuildResult) error
}

// StoreAuditor records build runs through the frozen Store interface.
type StoreAuditor struct {
	Store contract.Store
	Log   *slog.Logger
}

var _ Auditor = StoreAuditor{}

func (a StoreAuditor) StartBuildRun(ctx context.Context, run contract.BuildRun) (int64, error) {
	return a.Store.StartBuildRun(ctx, run)
}

func (a StoreAuditor) FinishBuildRun(ctx context.Context, id int64, result contract.BuildResult) error {
	return a.Store.FinishBuildRun(ctx, id, result)
}

// FileAuditor records build runs as JSON files beside the artifacts.
//
// It is the fallback for a deployment with no database configured - which is
// every local run and every offline verification - and it exists because an
// unattributable artifact is a real problem even without Postgres: the manifest
// names a build run id, and something has to be able to say what that run did.
type FileAuditor struct {
	// Root is the directory the records are written under, normally the
	// aggregate root's sibling so the served tree stays clean.
	Root string
}

var _ Auditor = FileAuditor{}

// buildRunRecord is the on-disk form of a build run. It mirrors the two
// contract types so a reader can compare it with a `build_runs` row without a
// schema translation table.
type buildRunRecord struct {
	ID              int64     `json:"id"`
	Patch           string    `json:"patch"`
	Region          string    `json:"region"`
	Queue           int       `json:"queue"`
	Bracket         string    `json:"bracket"`
	GitSHA          string    `json:"git_sha"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	Status          string    `json:"status"`
	CellsTotal      int       `json:"cells_total"`
	CellsPublished  int       `json:"cells_published"`
	CellsSuppressed int       `json:"cells_suppressed"`
	ArtifactURI     string    `json:"artifact_uri"`
	Err             string    `json:"error"`
}

func (a FileAuditor) StartBuildRun(_ context.Context, run contract.BuildRun) (int64, error) {
	// Nanosecond wall clock as the id: it is unique per process, it sorts in
	// creation order, and it needs no coordination with a database that is
	// explicitly not there.
	id := time.Now().UnixNano()
	record := buildRunRecord{
		ID:        id,
		Patch:     run.Patch,
		Region:    run.Region,
		Queue:     run.Queue,
		Bracket:   run.Bracket,
		GitSHA:    run.GitSHA,
		StartedAt: run.StartedAt.UTC(),
		// A record left in this state is a build that never finished, which
		// is exactly the information a stuck-row check is looking for.
		Status: "running",
	}
	return id, a.write(id, record)
}

func (a FileAuditor) FinishBuildRun(_ context.Context, id int64, result contract.BuildResult) error {
	record := buildRunRecord{ID: id}
	if path := a.path(id); path != "" {
		if raw, err := os.ReadFile(path); err == nil { //nolint:gosec // path is built from our own id.
			_ = json.Unmarshal(raw, &record)
		}
	}
	record.Status = result.Status
	record.FinishedAt = result.FinishedAt.UTC()
	record.CellsTotal = result.CellsTotal
	record.CellsPublished = result.CellsPublished
	record.CellsSuppressed = result.CellsSuppressed
	record.ArtifactURI = result.ArtifactURI
	record.Err = result.Err
	return a.write(id, record)
}

func (a FileAuditor) path(id int64) string {
	if a.Root == "" {
		return ""
	}
	return filepath.Join(a.Root, fmt.Sprintf("build-run-%d.json", id))
}

func (a FileAuditor) write(id int64, record buildRunRecord) error {
	if a.Root == "" {
		return errors.New("file auditor has no root directory")
	}
	// The parent of the breadcrumb directory is created served, not private:
	// the breadcrumb directory is a sibling of the aggregate root, so a private
	// parent would deny every reader below it - including the aggregate tree
	// itself. os.MkdirAll gives every directory it creates the mode of the
	// call, so the parent has to be made explicitly first. See perms.go.
	if err := os.MkdirAll(filepath.Dir(a.Root), publishedDirPerm); err != nil { //nolint:gosec // G301: the parent of the breadcrumb directory also holds the served aggregate tree; see perms.go.
		return fmt.Errorf("create parent of the build run directory: %w", err)
	}
	if err := os.MkdirAll(a.Root, privateDirPerm); err != nil {
		return fmt.Errorf("create build run directory: %w", err)
	}
	buf, err := marshalDoc(record)
	if err != nil {
		return err
	}
	// The breadcrumb directory sits beside the aggregate root, not inside the
	// path Caddy serves, so it is read by this process alone and needs no
	// group or other access. See perms.go.
	if err := os.WriteFile(a.path(id), buf, privateFilePerm); err != nil {
		return fmt.Errorf("write build run %d: %w", id, err)
	}
	return nil
}

// Audit statuses. The frozen vocabulary from the contract is ok, failed and
// quarantined; the distinction the build draws is whether the input was unfit
// (quarantined) or the run itself broke (failed). An operator's response
// differs: quarantine means fix the archive, failed means look at the job.
const (
	auditStatusOK          = "ok"
	auditStatusFailed      = "failed"
	auditStatusQuarantined = "quarantined"
)

// quarantineErrors are the conditions that describe bad input rather than a
// broken build.
var quarantineErrors = []error{
	ErrMalformedArchive,
	ErrEmptyWindow,
	ErrRejectedRows,
	ErrReconciliation,
	ErrSuppressionMajority,
	ErrNoPublishedCells,
	ErrArchiveEmpty,
}

// auditStatusFor classifies a build error.
func auditStatusFor(err error) string {
	if err == nil {
		return auditStatusOK
	}
	for _, sentinel := range quarantineErrors {
		if errors.Is(err, sentinel) {
			return auditStatusQuarantined
		}
	}
	return auditStatusFailed
}

// ArtifactURI is the public location of a partition, which is what a row in
// build_runs should point at rather than a filesystem path: the row outlives the
// container it was written from.
func ArtifactURI(segDir string) string {
	return aggmodel.URLPrefix + "/" + segDir
}
