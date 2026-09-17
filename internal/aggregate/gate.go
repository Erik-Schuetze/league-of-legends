package aggregate

import (
	"errors"
	"fmt"
	"math"
)

// The fail-closed gates.
//
// The rule the plan states is that a build either publishes a complete,
// internally consistent artifact set or changes nothing at all. Every failure
// below therefore happens *before* publish, and the gate set is expressed as
// one value that a test can drive directly rather than as checks scattered
// through the build.

var (
	// ErrMalformedArchive reports raw rows that are not readable match
	// summaries. A single unreadable row stops the build: a partially damaged
	// archive produces rates that are wrong in an unknown direction, and wrong
	// numbers are worse than absent ones.
	ErrMalformedArchive = errors.New("raw archive contains malformed match rows")

	// ErrEmptyWindow reports a window with no usable matches.
	ErrEmptyWindow = errors.New("patch window is empty")

	// ErrReconciliation reports that the per-cell sample sizes do not add up
	// to the participant rows the window contains.
	ErrReconciliation = errors.New("cell sample sizes do not reconcile with the raw row count")

	// ErrSuppressionMajority reports that most cells were suppressed, which
	// means the window is too thin to describe a patch.
	ErrSuppressionMajority = errors.New("majority of cells fall below the confidence floor")

	// ErrRejectedRows reports participant rows that could not be given a
	// champion and a role. They are dropped from every cell, so they silently
	// bias pick_rate against the champion or role that failed to parse.
	ErrRejectedRows = errors.New("participant rows could not be classified")

	// ErrNoPublishedCells reports a build that would publish an empty tier
	// list.
	ErrNoPublishedCells = errors.New("no cells survive suppression")
)

// GateConfig is the operator-tunable part of the gate set.
type GateConfig struct {
	// MinCellN is the confidence floor: a cell whose sample size is below it
	// is suppressed and counted, never published.
	MinCellN int

	// MinConfidentShare is the smallest share of computable cells that must
	// survive suppression. The default is 0.5, i.e. a minority of suppressions
	// is acceptable and a majority is not.
	//
	// The share is a property of the window, not of the pipeline, and a real
	// window has a long tail of one-off champion/role pairs, so the deployed
	// value is measured rather than assumed. Over the live EUW/420 archive
	// (2026-09-04..09-17, min_cell_n=100) the share was 19.3% at 34,780
	// classified rows, 12.1% at 21,620 and 2.1% at 8,770: it rises with the
	// crawl depth of the patch the window ends on and is nowhere near 50%
	// while the crawler's recent days are denser than its older ones. The
	// flag is `--min-confident-share`, the variable is
	// LOLSTATS_AGG_MIN_CONFIDENT_SHARE (see internal/config/config.go).
	MinConfidentShare float64

	// ReconcileTolerance is the number of rows by which the sum of the cell
	// sample sizes may differ from the classified participant row count.
	//
	// The two figures are reductions of the same rows, so the expected
	// difference is exactly zero and that is the default. The knob exists
	// because an operator may know of an archive defect that legitimately
	// unbalances them, and because a gate with no escape hatch gets disabled
	// wholesale instead of narrowed.
	ReconcileTolerance int

	// MaxRejectedRows is the absolute floor of the rejected-row allowance: how
	// many participant rows may lack a champion or a role before the build
	// stops, whatever the size of the window. It defaults to zero: a rejected
	// row lowers every rate it should have contributed to, and there is no way
	// to tell a queue quirk from a parsing bug by looking at the total.
	//
	// A non-zero value is an allowance measured against the archive, not a
	// relaxation of the rule. The live EUW/420 archive contains rows Riot
	// itself reports as position-less - teamPosition "" together with
	// individualPosition "Invalid", the literal sentinel, mostly in sub-four
	// minute remakes that never assigned a lane - and a build that refuses
	// them publishes nothing.
	//
	// The floor is what keeps the allowance usable on a small archive; the
	// window-sized part of it is MaxRejectedRate below. The rows themselves
	// are never guessed into a role: featureFilter drops them from every cell
	// and CheckOutput's reconciliation counts them as unclassified.
	MaxRejectedRows int

	// MaxRejectedRate is the window-sized part of the rejection allowance: a
	// share of the window's participant rows. The allowance the input gate
	// applies is
	//
	//	max(MaxRejectedRows, ceil(MaxRejectedRate x ParticipantRows))
	//
	// so it grows with the crawl instead of being outgrown by it.
	//
	// The shape is the answer to the second calibration event of this gate.
	// The allowance was an absolute count first, calibrated at 3 rejected rows
	// of 27,790 participant rows (0.011%); two weeks later the same window
	// held 141,150 participant rows with 30 of them (0.021%)
	// position-less, so a deployed 25 refused to publish a build whose *rate*
	// had barely moved. An absolute allowance on a growing archive fails on a
	// timer - the archive grew 5x between the two events - while a rate does
	// not, because the rejected rows are a property of the crawl (roughly one
	// per remake), not of its size.
	//
	// Zero is the default and means "no rate ceiling", which leaves
	// MaxRejectedRows to decide alone: an unconfigured build is still
	// fail-closed at zero allowed rows. The deployed ceiling is 0.0008
	// (0.08% of the window): 3.8x the worst rate measured over the live builds
	// (0.011%, 0.018%, 0.018%, 0.019%, 0.021%) and still under the tenth of a
	// percent the docs hold this allowance to. A classification
	// regression does not reject a rate in that band: the extraction stops
	// classifying a role, a champion or a payload shape, which rejects a large
	// fraction of the window, and a rate ceiling rejects that just as an
	// absolute one did.
	MaxRejectedRate float64
}

// DefaultGateConfig is what a build uses unless the operator overrides it.
func DefaultGateConfig(minCellN int) GateConfig {
	return GateConfig{
		MinCellN:           minCellN,
		MinConfidentShare:  0.5,
		ReconcileTolerance: 0,
		MaxRejectedRows:    0,
		MaxRejectedRate:    0,
	}
}

// AllowedRejectedRows is the allowance the input gate applies to a window of
// participantRows participant rows: the larger of the absolute floor and the
// rate ceiling, rounded up so that the ceiling is "at most this share of the
// window" rather than "less than one row below it".
//
// It is a method on the gate configuration rather than arithmetic buried in
// CheckInput because two callers need the same number: the gate that judges it
// and the run's log, which reports the allowance in force next to the count it
// judged. A window with no participant rows gets the floor: the ceiling of a
// share of nothing is nothing, and the input gate has already reported the
// empty window.
func (cfg GateConfig) AllowedRejectedRows(participantRows int) int {
	if participantRows <= 0 || cfg.MaxRejectedRate <= 0 {
		return cfg.MaxRejectedRows
	}
	ceiling := int(math.Ceil(cfg.MaxRejectedRate * float64(participantRows)))
	if ceiling > cfg.MaxRejectedRows {
		return ceiling
	}
	return cfg.MaxRejectedRows
}

// GateCounts is the evidence the gates judge.
type GateCounts struct {
	// From the archive.
	ArchiveRows   int
	MalformedRows int
	MatchesUsed   int

	// From the extraction.
	ParticipantRows int
	RejectedRows    int

	// From the cell computation.
	CellsTotal      int
	CellsPublished  int
	CellsSuppressed int

	// SumN is the sum of n over every computable cell; ClassifiedRows is the
	// number of participant rows that produced a champion and a role.
	SumN           int
	ClassifiedRows int

	// SumNPublished is the sum of n over the published cells only. It is not
	// judged directly; it is reported with the suppression share so that a
	// reader can see whether the published cells describe the window (most of
	// its rows) or only its largest champions.
	SumNPublished int
}

// CheckInput runs the gates that can be judged before any cell exists.
//
// It is a separate entry point from CheckOutput because the counts it reads are
// only complete once the window is known: a caller that evaluated the
// reconciliation gate here would compare zero cells against every classified row
// and report a failure of its own making. The order of the checks is the order of
// the diagnosis an operator wants first.
func (c GateCounts) CheckInput(cfg GateConfig) error {
	errs := []error{c.CheckArchive()}

	if c.MatchesUsed == 0 || c.ClassifiedRows == 0 {
		errs = append(errs, fmt.Errorf("%w: no matches with a champion and a role in the window (matches=%d, classified rows=%d)",
			ErrEmptyWindow, c.MatchesUsed, c.ClassifiedRows))
	}
	if allowance := cfg.AllowedRejectedRows(c.ParticipantRows); c.RejectedRows > allowance {
		errs = append(errs, fmt.Errorf("%w: %d of %d participant rows lack a champion or a role (allowed %d = max(floor %d, rate %.4f%% of the window))",
			ErrRejectedRows, c.RejectedRows, c.ParticipantRows, allowance, cfg.MaxRejectedRows, cfg.MaxRejectedRate*100))
	}

	return errors.Join(errs...)
}

// CheckArchive runs the input checks that depend on neither the window nor the
// chosen patch: that the archive holds rows at all, and that every row in it has
// an envelope the build can read.
//
// It is separated from CheckInput because it can be judged the moment the
// archive has been counted, which is before the patch is selected. A payload
// that cannot be parsed is the cause and the empty window it produces is the
// symptom, so a build that reports the symptom sends the operator to the
// crawler's partitions instead of to the row that cannot be read.
func (c GateCounts) CheckArchive() error {
	var errs []error

	if c.ArchiveRows == 0 {
		errs = append(errs, fmt.Errorf("%w: the raw archive holds no match rows", ErrEmptyWindow))
	}
	if c.MalformedRows > 0 {
		errs = append(errs, fmt.Errorf("%w: %d of %d raw rows have no readable envelope",
			ErrMalformedArchive, c.MalformedRows, c.ArchiveRows))
	}

	return errors.Join(errs...)
}

// CheckOutput runs the gates that need the finished cell computation:
// reconciliation, the suppression majority and the empty tier list.
func (c GateCounts) CheckOutput(cfg GateConfig) error {
	var errs []error

	if delta := c.SumN - c.ClassifiedRows; abs(delta) > cfg.ReconcileTolerance {
		errs = append(errs, fmt.Errorf("%w: cells hold %d participant rows but %d were classified (delta %d, tolerance %d)",
			ErrReconciliation, c.SumN, c.ClassifiedRows, delta, cfg.ReconcileTolerance))
	}

	if c.CellsTotal > 0 && c.CellsPublished == 0 {
		errs = append(errs, fmt.Errorf("%w: all %d computable cells are below min_cell_n=%d",
			ErrNoPublishedCells, c.CellsTotal, cfg.MinCellN))
	} else if c.CellsTotal > 0 {
		share := float64(c.CellsPublished) / float64(c.CellsTotal)
		if share < cfg.MinConfidentShare {
			errs = append(errs, fmt.Errorf("%w: only %d of %d cells (%.1f%%) reach min_cell_n=%d, below the %.1f%% floor%s",
				ErrSuppressionMajority, c.CellsPublished, c.CellsTotal, share*100, cfg.MinCellN,
				cfg.MinConfidentShare*100, describedShare(c.SumNPublished, c.ClassifiedRows)))
		}
	}

	return errors.Join(errs...)
}

// describedShare renders how much of the window the published cells describe,
// for the failure message only: the share of cells is what the gate judges, and
// the share of rows is what tells an operator whether the cells that did
// survive still carry the window. It is empty when there are no classified rows
// to divide by, because the input gate has already reported that window.
func describedShare(publishedRows, classifiedRows int) string {
	if classifiedRows <= 0 {
		return ""
	}
	return fmt.Sprintf(" (the published cells hold %d of %d classified rows, %.1f%%)",
		publishedRows, classifiedRows, float64(publishedRows)/float64(classifiedRows)*100)
}

// Check runs both passes, for a caller that already holds the complete counts.
// It does not stop at the first failure: an operator looking at a broken nightly
// build wants the whole diagnosis, not the first line of it.
func (c GateCounts) Check(cfg GateConfig) error {
	return errors.Join(c.CheckInput(cfg), c.CheckOutput(cfg))
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// FailureReason reduces a build error to a short, bounded label for the failure
// counter.
//
// The label is computed from the sentinel the error wraps rather than from the
// message: a metric label built from free text grows without bound, and the
// alert that reads this counter only needs to know which gate closed.
func FailureReason(err error) string {
	if err == nil {
		return "none"
	}
	for _, known := range knownFailures {
		if errors.Is(err, known.sentinel) {
			return known.reason
		}
	}
	return "other"
}

var knownFailures = []struct {
	sentinel error
	reason   string
}{
	{ErrArchiveEmpty, "archive_empty"},
	{ErrMalformedArchive, "malformed_archive"},
	{ErrEmptyWindow, "empty_window"},
	{ErrRejectedRows, "rejected_rows"},
	{ErrReconciliation, "reconciliation"},
	{ErrSuppressionMajority, "suppression_majority"},
	{ErrNoPublishedCells, "no_published_cells"},
}
