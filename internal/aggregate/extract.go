package aggregate

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// This file runs the two phases of SQL and reads the tallies back.
//
// Nothing is read from DuckDB's stdout: every statement writes a file (parquet
// for a relation that is read again, JSON for a tally that is read once) and Go
// reads the file. That is what makes the engine replaceable and the readback
// testable without an engine at all - a fake engine in a test only has to write
// the files the query would have written.

// archiveStatsRow is one row of archiveStatsSQL.
type archiveStatsRow struct {
	ArchiveRows   int `json:"archive_rows"`
	MalformedRows int `json:"malformed_rows"`
}

// windowStatsRow is one row of windowStatsSQL.
type windowStatsRow struct {
	MatchesUsed     int `json:"matches_used"`
	ParticipantRows int `json:"participant_rows"`
	RejectedRows    int `json:"rejected_rows"`
	BanRows         int `json:"ban_rows"`
}

// patchRow is one row of patchListSQL or of envelopePatchListSQL.
//
// Both statements produce the same list from the same archive by two different
// routes, and checkPatchEvidence compares them column by column on every run.
type patchRow struct {
	Patch           string `json:"patch"`
	Matches         int    `json:"matches"`
	ParticipantRows int    `json:"participant_rows"`
}

// The build key expressions. They live here rather than inline in the phase so
// that a golden test can read the SQL the build compiles, and so the rule each
// grouping applies is next to the reason for it.
const (
	// The seven item slots, in slot order: the contract documents that an item
	// key is ordered and may contain 0 for an empty slot.
	itemKeyExpr = "[p.item0, p.item1, p.item2, p.item3, p.item4, p.item5, p.item6]"
	// A row with nothing in the first six slots is not a build, it is an
	// incomplete payload; requiring at least one real item keeps those out of
	// the list without changing the rate of anything that is published.
	itemExtra = "(p.item0 > 0 OR p.item1 > 0 OR p.item2 > 0 OR p.item3 > 0 OR p.item4 > 0 OR p.item5 > 0)"

	// The rune style list already has the documented shape (primary tree,
	// keystone, secondary tree, three shards) or is NULL; NULL rows are counted
	// as rejected rather than guessed at.
	runeKeyExpr = "p.rune_style"
	runeExtra   = "(p.rune_style IS NOT NULL)"

	// The two summoner spells in pick order.
	spellKeyExpr = "[p.summoner1_id, p.summoner2_id]"
	spellExtra   = "(p.summoner1_id > 0 AND p.summoner2_id > 0)"

	// How many staged archive parts one batch of the extraction holds. Every
	// statement that reads a batch reads at most this much payload, which is
	// what keeps the unnesting statements inside the engine's memory limit; see
	// extract for the measurement. Eight parts of the live archive are about
	// 160 matches, or 12 MB of payload.
	extractBatchParts = 8
)

// planArchive resolves the window and the parts that can contribute to it.
//
// The window end defaults to the newest partition date in the archive rather
// than to today. That choice is what makes an offline verification meaningful:
// the same archive builds the same window on any machine at any time, so a
// repeated build of an unchanged archive publishes byte-identical artifacts.
func planArchive(opts BuildOptions) ([]ArchivePart, aggmodel.Window, error) {
	all, err := RawArchive{Root: opts.RawRoot}.Parts()
	if err != nil {
		return nil, aggmodel.Window{}, err
	}
	end := opts.WindowEnd
	if end == "" {
		for _, part := range all {
			if part.PartitionDate > end {
				end = part.PartitionDate
			}
		}
	}
	window, err := windowForEnd(end, opts.WindowDays)
	if err != nil {
		return nil, aggmodel.Window{}, err
	}
	parts, err := RawArchive{Root: opts.RawRoot, Before: window.From}.Parts()
	if err != nil {
		return nil, aggmodel.Window{}, err
	}
	return parts, window, nil
}

// path returns a path inside the build scratch directory.
func (s *buildState) path(name string) string { return filepath.Join(s.scratch, name) }

// run drives the phases of one build.
func (s *buildState) run(ctx context.Context, result *BuildResult) (err error) {
	engine := s.opts.Engine
	if engine == nil {
		cli, err := OpenCLIEngine(ctx, s.opts.DuckDBBin, s.opts.AllowVersionMismatch, s.opts.DuckDB, s.opts.Log)
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()
		engine = cli
	}
	s.engine = engine
	s.seg = aggmodel.Seg{
		Patch: s.opts.Patch, Region: s.opts.Region, Queue: s.opts.Queue, Bracket: s.opts.Bracket,
	}
	// The row is closed by whichever way this function returns, including the
	// paths that fail before it is opened at all. finishAudit does nothing when
	// no row was written.
	defer func() { s.finishAudit(err) }()

	// The macro filter is patch-free: it selects the region, the queue and the
	// window, which is what the extraction sees before the patch is chosen.
	filters := filterSQL(s.opts.Region, s.opts.Platform, s.opts.Queue, s.window, "")

	// The patch is resolved from the envelope before the row is opened, because
	// the row records the partition the build publishes and the store will not
	// open one whose patch is empty. The payloads are unfolded afterwards, so the
	// row is still open across all of the expensive work.
	if err := s.spillEnvelope(ctx); err != nil {
		return err
	}
	s.resolvePatch(ctx)
	s.openAudit(result)
	// The run's identity exists from here, and it is judged before any of the
	// expensive work: a build that cannot name the revision and the run behind
	// its numbers must not publish them, and must not spend the extraction
	// finding that out.
	if err := s.checkProvenance(); err != nil {
		return err
	}

	if err := s.extract(ctx, filters); err != nil {
		return err
	}
	if err := s.reduce(ctx, filters); err != nil {
		return err
	}
	if err := s.checkInputGates(); err != nil {
		return err
	}
	if err := s.computeCells(); err != nil {
		return err
	}
	if err := s.checkOutputGates(); err != nil {
		return err
	}
	if err := s.assemble(); err != nil {
		return err
	}
	return s.publishLive(result)
}

// spillEnvelope writes the archive's per-match keys, which is the first of the
// two spill halves and the only one that runs before the patch is chosen.
//
// It is patch-free on purpose. It is the statement the patch is chosen from, so
// it has to describe every patch the window holds; the choice is then applied by
// the reductions over the smaller feature rows, not by the spill.
func (s *buildState) spillEnvelope(ctx context.Context) error {
	return s.engine.Exec(ctx, copyParquet(envelopeSQL(s.stagedPaths), s.envelopePath))
}

// resolvePatch chooses the patch the build publishes from the envelope the spill
// just wrote, so that the audit row can name the partition before any payload is
// unfolded.
//
// It is best effort, and deliberately so. The authority on which patch is
// published is the feature spill - extract re-derives the choice from the rows
// the extraction actually produced and refuses the build if the two disagree -
// and every refusal choosePatch can make is a refusal the extraction reports, in
// the order it has always reported it, with the archive statistics and the row
// counts that make the reason legible. A failure here is therefore reported as a
// warning and not returned: what it costs is the row's patch, never a different
// verdict on the archive.
func (s *buildState) resolvePatch(ctx context.Context) {
	if err := s.engine.Exec(ctx, copyJSON(envelopePatchListSQL(s.envelopePath, envelopeFilterSQL(
		s.opts.Region, s.opts.Platform, s.opts.Queue, s.window)), s.path("patches_envelope.json"))); err != nil {
		s.opts.Log.Warn("the window's patches could not be listed before the audit row", "error", err)
		return
	}
	rows, err := readJSONArray[patchRow](s.path("patches_envelope.json"))
	if err != nil {
		s.opts.Log.Warn("the window's patches could not be read before the audit row", "error", err)
		return
	}
	chosen, err := choosePatch(rows, s.opts.Patch)
	if err != nil {
		s.opts.Log.Warn("the window holds no patch to choose before the audit row", "error", err)
		return
	}
	s.envelopePatches = rows
	s.envelopeResolved = true
	s.patch = chosen.Patch
	s.seg.Patch = chosen.Patch
	s.opts.Log.Info("patch selected", "patch", chosen.Patch, "source", "envelope",
		"patches_in_window", len(rows), "matches", chosen.Matches, "participant_rows", chosen.ParticipantRows,
		"pinned", s.opts.Patch != "")
}

// extract is phase one: read the archive, spill it, and derive the feature rows
// and the patch list. It runs the payload half as a single script
// (extractScript) so that a probe can run exactly what the build runs: the
// envelope half is a statement of its own (spillEnvelope) because the patch is
// chosen from it before this phase starts.
//
// Everything here that touches a payload is batched by parts, because every
// statement that reads a payload holds its whole input in memory while it runs.
// Three measurements on the live archive (205 staged parts, 3,643 matches in the
// window, 283 MB of payload) fix the shape:
//
//   - A single statement that projects the envelope keys and carries the payload
//     does not fit. Splitting it into a keys-only statement (envelopeSQL, which
//     also computes the payload_valid flag, because json_valid does not fit
//     beside a projected payload) and a JSON-free payload statement (payloadSQL)
//     does.
//   - The statements that unnest the payload's arrays do not fit when they read
//     the whole window: the participants statement over every spilled file dies
//     with "Out of Memory Error: failed to allocate data of size 16.0 MiB
//     (1008.7 MiB/1.0 GiB used)" under a 1 GiB engine limit and at 1.9 GiB under
//     a 2 GiB limit, and preserve_insertion_order does not change it. Reading one
//     batch of parts at a time does: the whole 26-batch script completes at a
//     1.5 GiB limit, and at 1 GiB the statement that dies is one batch's features
//     COPY rather than the window-wide one.
//   - What that statement costs is dominated by the rune-style list, not by the
//     JSON extraction around it: replacing runeStyleSQL's CASE with a NULL list
//     of the same type makes the same batch complete at the 1 GiB limit that it
//     otherwise dies under. So the batch boundary and the engine's limit are
//     chosen together - extractBatchParts below and DefaultDuckDBMemoryLimit.
//
// The payload is spilled one file per batch of parts and the unnesting
// statements run once per batch file, each appending to the features and bans
// directories. Every later statement reads those directories through a glob, so
// the batch boundary is invisible past this phase.
func (s *buildState) extractScript(filters string) string {
	var statements []string
	for i := 0; i*extractBatchParts < len(s.stagedPaths); i++ {
		parts := s.stagedPaths[i*extractBatchParts:]
		if len(parts) > extractBatchParts {
			parts = parts[:extractBatchParts]
		}
		matches := filepath.Join(s.matchesDir, batchFileName("matches", i))
		statements = append(statements,
			copyParquet(payloadSQL(s.envelopePath, parts), matches),
			copyParquet(participantsSQL(matches, filters), filepath.Join(s.featuresDir, batchFileName("features", i))),
			copyParquet(bansSQL(matches, filters), filepath.Join(s.bansDir, batchFileName("bans", i))),
		)
	}
	statements = append(statements,
		copyJSON(archiveStatsSQL(s.matchesPath), s.path("archive_stats.json")),
		copyJSON(patchListSQL(s.featuresPath), s.path("patches.json")),
	)
	return joinStatements(statements...)
}

// batchFileName is the name of one batch's file inside a phase directory.
func batchFileName(kind string, i int) string { return fmt.Sprintf("%s-%05d.parquet", kind, i) }

func (s *buildState) extract(ctx context.Context, filters string) error {
	script := s.extractScript(filters)
	if err := s.engine.Exec(ctx, script); err != nil {
		return err
	}

	stats, err := readJSONArray[archiveStatsRow](s.path("archive_stats.json"))
	if err != nil {
		return err
	}
	if len(stats) != 1 {
		return fmt.Errorf("archive statistics returned %d rows, expected 1", len(stats))
	}
	s.archiveStats = stats[0]

	// The archive-level gate runs here rather than with the rest of the input
	// gate because it needs neither the window nor the patch, and because a
	// payload that cannot be read is the cause of the empty window it produces:
	// reporting the symptom instead would hide the row the operator has to fix.
	counts := GateCounts{ArchiveRows: s.archiveStats.ArchiveRows, MalformedRows: s.archiveStats.MalformedRows}
	if err := counts.CheckArchive(); err != nil {
		return err
	}

	// The patch chosen from the envelope is re-derived here, from the rows the
	// extraction actually produced. Two statements that are meant to list the
	// same matches are worth comparing on every run: a drift between them would
	// publish a partition the chooser never saw, and the difference is exactly
	// the kind a tally hides.
	patchRows, err := readJSONArray[patchRow](s.path("patches.json"))
	if err != nil {
		return err
	}
	spilled, err := choosePatch(patchRows, s.opts.Patch)
	if err != nil {
		return err
	}
	if s.envelopeResolved {
		if err := checkPatchEvidence(s.envelopePatches, patchRows); err != nil {
			return err
		}
	} else {
		// The envelope could not be read, so this is where the patch was chosen.
		s.opts.Log.Info("patch selected", "patch", spilled.Patch, "source", "features",
			"matches", spilled.Matches, "participant_rows", spilled.ParticipantRows,
			"pinned", s.opts.Patch != "")
	}
	s.patch = spilled.Patch
	s.seg.Patch = s.patch
	return nil
}

// checkPatchEvidence compares the patch list the envelope produced with the one
// the feature spill produced.
//
// The two lists are the same window of the same archive: one counted from the
// envelope before the payloads were unfolded, one counted from the rows that
// unnesting produced. Comparing the patch sets alone would not notice a match
// going missing between them, so every column is compared, and a disagreement is
// a failure rather than a warning: the build would otherwise publish a partition
// whose boundary was chosen from rows it did not publish.
func checkPatchEvidence(envelope, features []patchRow) error {
	counted := make(map[string]patchRow, len(envelope))
	for _, row := range envelope {
		counted[row.Patch] = row
	}
	for _, row := range features {
		other, ok := counted[row.Patch]
		if !ok {
			return fmt.Errorf("patch %s carries matches in the extracted features but none in the envelope", row.Patch)
		}
		if other.Matches != row.Matches || other.ParticipantRows != row.ParticipantRows {
			return fmt.Errorf("patch %s is %d matches and %d participant rows in the envelope, but %d and %d in the extracted features",
				row.Patch, other.Matches, other.ParticipantRows, row.Matches, row.ParticipantRows)
		}
	}
	if len(features) != len(envelope) {
		return fmt.Errorf("the envelope holds %d patches in the window and the extracted features hold %d",
			len(envelope), len(features))
	}
	return nil
}

// choosePatch picks the patch the build publishes, and reports the row it was
// picked from so that the audit trail and the log carry the patch's match and
// participant counts.
//
// Every patch the window contains is validated, not just the chosen one: a
// malformed patch anywhere in the window means the extraction produced a game
// version that cannot be attributed, and attributing the other rows anyway would
// publish a partition whose boundary is not understood.
func choosePatch(rows []patchRow, pinned string) (patchRow, error) {
	found := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, _, ok := splitPatch(row.Patch); !ok {
			return patchRow{}, fmt.Errorf("%w: patch %q derived from gameVersion is not major.minor",
				ErrMalformedArchive, row.Patch)
		}
		found[row.Patch] = struct{}{}
	}
	if len(found) == 0 {
		return patchRow{}, fmt.Errorf("%w: no match in the window carries a patch", ErrEmptyWindow)
	}
	if pinned != "" {
		if _, ok := found[pinned]; !ok {
			return patchRow{}, fmt.Errorf("%w: patch %s has no match in the window", ErrEmptyWindow, pinned)
		}
	}
	patches := make([]string, 0, len(found))
	for patch := range found {
		patches = append(patches, patch)
	}
	chosen := pinned
	if chosen == "" {
		chosen = newestPatch(patches)
	}
	for _, row := range rows {
		if row.Patch == chosen {
			return row, nil
		}
	}
	return patchRow{}, fmt.Errorf("%w: patch %s has no match in the window", ErrEmptyWindow, chosen)
}

// reduce is phase two: every tally the artifacts are assembled from, scoped to
// the chosen patch.
func (s *buildState) reduce(ctx context.Context, filters string) error {
	script := joinStatements(
		copyJSON(windowStatsSQL(s.matchesPath, s.featuresPath, s.bansPath, filters, s.patch),
			s.path("window_stats.json")),
		copyJSON(cellCountsSQL(s.featuresPath, s.patch), s.path("cell_counts.json")),
		copyJSON(banCountsSQL(s.bansPath, s.patch), s.path("ban_counts.json")),
		copyJSON(buildCountsSQL(s.featuresPath, s.patch, itemKeyExpr, itemExtra), s.path("items.json")),
		copyJSON(buildCountsSQL(s.featuresPath, s.patch, runeKeyExpr, runeExtra), s.path("runes.json")),
		copyJSON(buildCountsSQL(s.featuresPath, s.patch, spellKeyExpr, spellExtra), s.path("spells.json")),
		copyJSON(matchupCountsSQL(s.featuresPath, s.patch), s.path("matchups.json")),
	)
	if err := s.engine.Exec(ctx, script); err != nil {
		return err
	}

	stats, err := readJSONArray[windowStatsRow](s.path("window_stats.json"))
	if err != nil {
		return err
	}
	if len(stats) != 1 {
		return fmt.Errorf("window statistics returned %d rows, expected 1", len(stats))
	}
	s.windowStats = stats[0]

	if s.cellCounts, err = readJSONArray[CellCount](s.path("cell_counts.json")); err != nil {
		return err
	}
	if s.banCounts, err = readJSONArray[BanCount](s.path("ban_counts.json")); err != nil {
		return err
	}
	if s.itemCounts, err = readJSONArray[BuildCount](s.path("items.json")); err != nil {
		return err
	}
	if s.runeCounts, err = readJSONArray[BuildCount](s.path("runes.json")); err != nil {
		return err
	}
	if s.spellCounts, err = readJSONArray[BuildCount](s.path("spells.json")); err != nil {
		return err
	}
	if s.matchupCounts, err = readJSONArray[MatchupCount](s.path("matchups.json")); err != nil {
		return err
	}
	s.opts.Log.Info("tallies read",
		"cells", len(s.cellCounts), "bans", len(s.banCounts), "items", len(s.itemCounts),
		"runes", len(s.runeCounts), "spells", len(s.spellCounts), "matchups", len(s.matchupCounts))
	return nil
}

// joinStatements concatenates statements into one script.
//
// Every statement this package generates ends with a semicolon, so a newline is
// enough to separate them, and one script means one DuckDB process per phase
// rather than one per statement.
func joinStatements(statements ...string) string {
	return strings.Join(statements, "\n")
}
