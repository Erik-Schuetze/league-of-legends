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

// patchRow is one row of patchListSQL.
type patchRow struct {
	Patch           string `json:"patch"`
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
func (s *buildState) run(ctx context.Context, result *BuildResult) error {
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

	// The macro filter is patch-free: it selects the region, the queue and the
	// window, which is what the extraction sees before the patch is chosen.
	filters := filterSQL(s.opts.Region, s.opts.Platform, s.opts.Queue, s.window, "")
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

// extract is phase one: read the archive, spill it, and derive the feature rows
// and the patch list. It runs as a single script (extractScript) so that a probe
// can run exactly what the build runs.
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
	statements := []string{copyParquet(envelopeSQL(s.stagedPaths), s.envelopePath)}
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

	patchRows, err := readJSONArray[patchRow](s.path("patches.json"))
	if err != nil {
		return err
	}
	s.patch, err = choosePatch(patchRows, s.opts.Patch)
	if err != nil {
		return err
	}
	s.seg.Patch = s.patch
	s.opts.Log.Info("patch selected", "patch", s.patch,
		"in_window", len(patchRows), "pinned", s.opts.Patch != "")
	return nil
}

// choosePatch picks the patch the build publishes.
//
// Every patch the window contains is validated, not just the chosen one: a
// malformed patch anywhere in the window means the extraction produced a game
// version that cannot be attributed, and attributing the other rows anyway would
// publish a partition whose boundary is not understood.
func choosePatch(rows []patchRow, pinned string) (string, error) {
	found := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, _, ok := splitPatch(row.Patch); !ok {
			return "", fmt.Errorf("%w: patch %q derived from gameVersion is not major.minor",
				ErrMalformedArchive, row.Patch)
		}
		found[row.Patch] = struct{}{}
	}
	if len(found) == 0 {
		return "", fmt.Errorf("%w: no match in the window carries a patch", ErrEmptyWindow)
	}
	if pinned != "" {
		if _, ok := found[pinned]; !ok {
			return "", fmt.Errorf("%w: patch %s has no match in the window", ErrEmptyWindow, pinned)
		}
		return pinned, nil
	}
	patches := make([]string, 0, len(found))
	for patch := range found {
		patches = append(patches, patch)
	}
	return newestPatch(patches), nil
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
