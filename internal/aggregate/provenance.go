package aggregate

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Provenance is what makes a published number traceable: the revision the image
// was built from, and the build_runs row that recorded the run that produced it.
//
// The manifest has to carry both, because the frozen schema lists them as
// required properties and the site renders them on its about page. "Unrecorded"
// therefore cannot be expressed by leaving the keys out - the only honest
// expression is to refuse to publish at all, which is what
// GateConfig.RequireProvenance does on every deployed build.
//
// The zero values below are what an artifact whose provenance was never
// recorded reads as: a tree repaired by hand, a partition directory discovered
// by a re-index that no manifest entry ever described. They are deliberately
// not plausible values - a build_runs id starts at 1, so 0 names no row, and
// UnknownGitSHA is not a revision - because a plausible-looking number would be
// indistinguishable from a real one to every reader but this package.
var (
	// ErrUnrecordedProvenance reports a build that would publish numbers
	// naming neither the revision nor the run behind them, while the operator
	// has asked for that guarantee.
	ErrUnrecordedProvenance = errors.New("build provenance was not recorded")
)

// UnknownGitSHA is what a build records when the image does not say which
// revision it was built from. It is recorded rather than left blank so that the
// absence is visible in the artifact and in the build_runs row, and it is what
// the provenance gate refuses to publish.
const UnknownGitSHA = "unknown"

// revisionPattern accepts the abbreviations git itself prints, so a short sha
// from a hand-run build is provenance rather than a failure, while "unknown",
// the empty string and anything else that is not hex is not.
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// IsRevision reports whether sha names a git revision.
func IsRevision(sha string) bool {
	return revisionPattern.MatchString(strings.ToLower(strings.TrimSpace(sha)))
}

// ProvenanceProblem describes what a published partition would be missing, and
// returns the empty string when the provenance is complete. It is shared by the
// gate and by its tests so that the reason an operator reads is the reason the
// gate judged.
func ProvenanceProblem(gitSHA string, buildRunID int64) string {
	var problems []string
	if !IsRevision(gitSHA) {
		problems = append(problems, fmt.Sprintf("git_sha %q is not a revision", gitSHA))
	}
	if buildRunID <= 0 {
		problems = append(problems, fmt.Sprintf("build_run_id %d names no build_runs row", buildRunID))
	}
	return strings.Join(problems, "; ")
}

// checkProvenance refuses to publish an artifact whose provenance is unrecorded.
//
// It runs as soon as the run has an identity - after the build_runs row is
// opened, before the first payload is unfolded - so an untraceable build costs
// one dial rather than a DuckDB pass, and so it can never reach the publish
// step. Nothing has been written to the aggregate root at that point, which is
// the same property every other gate has: the published tree is either replaced
// completely or not at all.
//
// The gate is off unless an operator asks for it, which keeps a developer's
// fixture build and an offline verification working with no database and no
// image revision to name. See GateConfig.RequireProvenance.
func (s *buildState) checkProvenance() error {
	if !s.opts.Gates.RequireProvenance {
		return nil
	}
	problem := ProvenanceProblem(s.opts.GitSHA, s.buildRunID)
	if problem == "" {
		return nil
	}
	return fmt.Errorf("%w: %s; refusing to publish numbers that name neither the revision nor the run behind them",
		ErrUnrecordedProvenance, problem)
}
