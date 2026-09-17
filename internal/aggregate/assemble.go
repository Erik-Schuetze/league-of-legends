package aggregate

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// Assembling and publishing.
//
// Assembly is pure with respect to the world outside the aggregate tree: it
// reads the published static dataset for champion slugs, writes the frozen
// documents into the staging tree, and merges the manifest. Publishing then
// moves that tree into place. Nothing between the gates and the publish can
// change a number, which is what makes "fail closed" a property of the whole run
// rather than of the checks.

// checkInputGates runs the gates that can be judged before any cell exists:
// the archive, the window and the rejected rows.
//
// The gate set is evaluated in two passes because two of its gates are about the
// cell computation itself, which cannot run until the window is known to be
// non-empty. This pass exists so that an empty or malformed archive produces an
// archive error rather than a division error from the cell maths.
func (s *buildState) checkInputGates() error {
	s.counts = GateCounts{
		ArchiveRows:     s.archiveStats.ArchiveRows,
		MalformedRows:   s.archiveStats.MalformedRows,
		MatchesUsed:     s.windowStats.MatchesUsed,
		ParticipantRows: s.windowStats.ParticipantRows,
		RejectedRows:    s.windowStats.RejectedRows,
		ClassifiedRows:  s.windowStats.ParticipantRows - s.windowStats.RejectedRows,
	}
	// A tolerated rejection is exactly the kind of thing that must not be
	// silent: the published rates are computed without those rows, so the count
	// and the allowance belong in the run's log next to the rates they affect.
	//
	// The allowance is logged on every run, not only on the runs that reject
	// something: it is the number an operator has to compare against a new
	// measurement, and a ceiling that only appears in a log line when it is
	// about to be crossed cannot be calibrated from the outside.
	allowance := s.opts.Gates.AllowedRejectedRows(s.windowStats.ParticipantRows)
	s.opts.Log.Info("rejection allowance in force",
		"allowed", allowance,
		"floor", s.opts.Gates.MaxRejectedRows,
		"rate", s.opts.Gates.MaxRejectedRate,
		"participant_rows", s.windowStats.ParticipantRows)
	if s.windowStats.RejectedRows > 0 {
		s.opts.Log.Warn("participant rows lack a champion or a role",
			"rejected_rows", s.windowStats.RejectedRows,
			"participant_rows", s.windowStats.ParticipantRows,
			"allowed", allowance,
			"reason", "Riot reported no usable position; the rows are excluded from every cell")
	}
	return s.counts.CheckInput(s.opts.Gates)
}

// computeCells applies the cell policy, which is the only place a published rate
// is calculated.
func (s *buildState) computeCells() error {
	cells, err := ComputeCells(CellInput{
		Counts:   s.cellCounts,
		Bans:     s.banCounts,
		Matches:  s.windowStats.MatchesUsed,
		MinCellN: s.opts.MinCellN,
	})
	if err != nil {
		return fmt.Errorf("compute cells: %w", err)
	}
	s.cells = cells
	s.opts.Log.Info("cells computed",
		"total", cells.Total, "published", len(cells.Cells), "suppressed", cells.Suppressed,
		"published_rows", cells.SumNPublished, "rows", s.windowStats.ParticipantRows-s.windowStats.RejectedRows,
		"baseline_win_rate", cells.BaselineWinRate)
	return nil
}

// checkOutputGates runs the gates that need the finished cell computation:
// reconciliation, the suppression majority and the empty tier list.
func (s *buildState) checkOutputGates() error {
	s.counts.CellsTotal = s.cells.Total
	s.counts.CellsPublished = len(s.cells.Cells)
	s.counts.CellsSuppressed = s.cells.Suppressed
	s.counts.SumN = s.cells.SumN
	s.counts.SumNPublished = s.cells.SumNPublished
	s.counts.ClassifiedRows = s.windowStats.ParticipantRows - s.windowStats.RejectedRows
	return s.counts.CheckOutput(s.opts.Gates)
}

// assemble writes the frozen documents into the staging tree and merges the
// manifest.
//
// The manifest is written into the staging tree and published last, so a reader
// never sees a manifest naming a partition whose files are not there yet.
func (s *buildState) assemble() error {
	envelope := aggmodel.Envelope{
		Schema:          aggmodel.SchemaVersion,
		Source:          s.source,
		Patch:           s.seg.Patch,
		Region:          s.seg.Region,
		Queue:           s.seg.Queue,
		Bracket:         s.seg.Bracket,
		GeneratedAt:     s.generatedAt,
		SourceWindow:    s.window,
		MinCellN:        s.opts.MinCellN,
		SuppressedCells: s.cells.Suppressed,
	}
	input := artifactInput{
		Envelope:      envelope,
		Cells:         s.cells,
		Items:         computeBuilds(BuildKindItems, s.itemCounts),
		Runes:         computeBuilds(BuildKindRunes, s.runeCounts),
		Spells:        computeBuilds(BuildKindSpells, s.spellCounts),
		Matchups:      computeMatchupCells(s.matchupCounts),
		ChampionSlugs: loadChampionSlugs(s.opts.AggRoot, s.opts.Log),
	}

	if err := writeDoc(s.staging, s.seg.TierListPath(), buildTierList(input)); err != nil {
		return err
	}
	champions := buildChampions(input)
	for _, championID := range s.cells.ChampionsAscending {
		if err := writeDoc(s.staging, s.seg.ChampionPath(championID), champions[championID]); err != nil {
			return err
		}
	}
	for _, role := range aggmodel.Roles {
		doc := buildMatchups(input, role, s.opts.MinCellN)
		if err := writeDoc(s.staging, s.seg.MatchupsPath(role), doc); err != nil {
			return err
		}
	}

	s.partition = aggmodel.Partition{
		Patch:           s.seg.Patch,
		Region:          s.seg.Region,
		Queue:           s.seg.Queue,
		Bracket:         s.seg.Bracket,
		GeneratedAt:     s.generatedAt,
		SourceWindow:    s.window,
		MinCellN:        s.opts.MinCellN,
		SuppressedCells: s.cells.Suppressed,
		CellsPublished:  len(s.cells.Cells),
		BuildRunID:      s.buildRunID,
		GitSHA:          s.opts.GitSHA,
		Champions:       append([]int{}, s.cells.ChampionsAscending...),
		// Every role gets a matrix artifact, so every role is listed. A role
		// with no pairs publishes an empty matrix rather than a missing file,
		// which keeps a prerendered route from pointing at nothing.
		MatchupRoles: append([]aggmodel.Role{}, aggmodel.Roles...),
	}
	manifest, err := UpdateManifest(s.opts.AggRoot, s.partition, s.source, s.generatedAt)
	if err != nil {
		return err
	}
	if err := writeDoc(s.staging, aggmodel.ManifestPath, manifest); err != nil {
		return err
	}
	s.manifest = manifest
	s.opts.Log.Info("artifacts assembled",
		"champions", len(s.cells.ChampionsAscending), "partitions", len(manifest.Partitions))
	return nil
}

// publishLive swaps the staged partition and the manifest into the live tree.
func (s *buildState) publishLive(result *BuildResult) error {
	publisher := Publisher{AggRoot: s.opts.AggRoot, Log: s.opts.Log}
	placed, err := publisher.Publish(s.staging, []string{s.seg.Dir()}, aggmodel.ManifestPath)
	if err != nil {
		return err
	}
	result.Published = placed
	result.Manifest = s.manifest
	s.opts.Log.Info("build published",
		"partition", s.seg.Dir(),
		"cells_published", len(s.cells.Cells),
		"cells_suppressed", s.cells.Suppressed,
		"artifact_uri", ArtifactURI(s.seg.Dir()))
	return nil
}

// loadChampionSlugs reads champion id to slug out of the published static
// dataset.
//
// The aggregate build does not own the static dataset and must not invent a
// slug: it reads whatever the static sync job has published, and a champion with
// no entry gets an empty slug. An empty slug is a link the frontend can choose
// not to render; a guessed slug is a link that 404s while looking correct.
func loadChampionSlugs(aggRoot string, log *slog.Logger) map[int]string {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	staticDir := filepath.Join(aggRoot, aggmodel.VersionDir, "static")
	versions, err := readDirNames(staticDir)
	if err != nil || len(versions) == 0 {
		log.Info("no published static dataset yet: champion slugs will be empty", "dir", staticDir)
		return nil
	}
	// Data Dragon versions sort lexically only within a season, which is what
	// the static sync publishes; the newest directory is the one the site uses.
	sort.Strings(versions)
	latest := versions[len(versions)-1]
	doc, err := readJSONDoc[aggmodel.StaticChampions](filepath.Join(staticDir, latest, "champions.json"))
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn("could not read published static champions", "dir", latest, "error", err)
		}
		return nil
	}
	slugs := make(map[int]string, len(doc.Champions))
	for _, champion := range doc.Champions {
		slugs[champion.ID] = champion.Slug
	}
	log.Info("champion slugs loaded", "ddragon_version", doc.DDragonVersion, "champions", len(slugs))
	return slugs
}
