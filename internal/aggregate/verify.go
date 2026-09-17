package aggregate

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// Verification of a published tree.
//
// This is the reader's half of the contract: it takes the tree the build wrote
// and re-derives everything that can be re-derived from it alone - the schema,
// the sample sizes against the confidence floor, the rate arithmetic, the
// envelope's agreement with its partition, the suppression count against the
// tier list, and the tier ordering against the win rates. It does not need the
// raw archive, Postgres or DuckDB, so it runs in a pod that has none of them.
//
// What it cannot check is stated rather than implied: pick_rate and ban_rate
// have no denominator inside the artifact (the match count is not published),
// so they are only checked to be within [0,1], and the tier labels are checked
// for internal consistency rather than recomputed, because the baseline win rate
// the scorer graded against is not published either. Both gaps are deliberate
// properties of the frozen shape and neither is something a reader could detect
// on its own - which is exactly why the repair for them is a build-time gate, in
// gate.go, and not a verification claim here.

// VerifyOptions configures one verification.
type VerifyOptions struct {
	// AggRoot is the aggregate root holding v1/.
	AggRoot string

	// Seg selects one partition. A zero field means "any", so the zero value
	// verifies every partition the manifest lists.
	Seg aggmodel.Seg

	// Source, when set, is the source every artifact must declare. An operator
	// verifying a real deployment sets riot-match-v5 so that a demo tree
	// copied into place by mistake is a failure rather than a surprise.
	Source aggmodel.Source

	// SchemaPath is the schema document to check against. Empty means the
	// schema internal/aggmodel emits, which is the same document
	// cmd/gen-types writes to schema/agg.schema.json.
	SchemaPath string

	Log *slog.Logger
}

// VerifyResult is what one verification looked at, so a caller can report a
// summary without re-reading the tree.
type VerifyResult struct {
	Manifest   aggmodel.Manifest
	Partitions int
	Documents  int
	Cells      int
	Problems   []string
}

// Verify checks a published aggregate tree.
//
// It reports every problem it finds rather than the first one, because a
// reviewer fixing an artifact set wants the whole list, and it returns an error
// whenever Problems is non-empty. The result is populated even on failure.
func Verify(opts VerifyOptions) (VerifyResult, error) {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	var result VerifyResult
	if opts.AggRoot == "" {
		return result, errors.New("aggregate root is required")
	}
	validator, err := loadValidator(opts.SchemaPath)
	if err != nil {
		return result, err
	}

	check := &verifier{aggRoot: opts.AggRoot, validator: validator, log: opts.Log}

	manifestPath := filepath.Join(opts.AggRoot, aggmodel.ManifestPath)
	// The path is the aggregate root from configuration plus the frozen
	// manifest name; there is no user-controlled component in it.
	raw, err := os.ReadFile(manifestPath) //nolint:gosec // G304: operator-configured aggregate root.
	if err != nil {
		return result, fmt.Errorf("read manifest: %w", err)
	}
	check.document("Manifest", aggmodel.ManifestPath, raw)
	var manifest aggmodel.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return result, fmt.Errorf("decode manifest: %w", err)
	}
	result.Manifest = manifest

	if manifest.Schema != aggmodel.SchemaVersion {
		check.problem("manifest: schema %d is not the version this build writes (%d)",
			manifest.Schema, aggmodel.SchemaVersion)
	}
	if !manifest.Source.Valid() {
		check.problem("manifest: source %q is not a source this project defines", manifest.Source)
	}
	if opts.Source != "" && manifest.Source != opts.Source {
		check.problem("manifest: source is %q, expected %q", manifest.Source, opts.Source)
	}
	check.duplicatePartitions(manifest.Partitions)
	check.latest(manifest)

	selected := selectPartitions(manifest.Partitions, opts.Seg)
	if len(selected) == 0 {
		check.problem("manifest: no partition matches the request (%s)", describeSeg(opts.Seg))
	}
	for _, partition := range selected {
		result.Cells += check.partition(partition, manifest)
	}
	result.Partitions = len(selected)
	result.Documents = check.documents

	result.Problems = check.problems
	if len(check.problems) > 0 {
		return result, fmt.Errorf("%d problems found in %s:\n  %s",
			len(check.problems), opts.AggRoot, strings.Join(check.problems, "\n  "))
	}
	opts.Log.Info("aggregate tree verified",
		"partitions", result.Partitions, "documents", result.Documents, "cells", result.Cells)
	return result, nil
}

// verifier accumulates problems for one tree.
type verifier struct {
	aggRoot   string
	validator *schemaValidator
	log       *slog.Logger
	problems  []string
	documents int
	cells     int
}

func (v *verifier) problem(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

// document schema-checks one file and counts it.
func (v *verifier) document(definition, relPath string, raw []byte) bool {
	v.documents++
	if err := v.validator.Validate(definition, raw); err != nil {
		v.problem("%s: %v", relPath, err)
		return false
	}
	return true
}

// readSchemaValidated reads a file, checks it against a schema definition and
// decodes it. It returns the decode error separately from the two checks, so a
// caller can carry on with the rest of the tree.
func (v *verifier) readSchemaValidated(definition, relPath string, target any) bool {
	raw, err := os.ReadFile(filepath.Join(v.aggRoot, filepath.FromSlash(relPath)))
	if err != nil {
		if os.IsNotExist(err) {
			v.problem("%s: file is missing", relPath)
		} else {
			v.problem("%s: %v", relPath, err)
		}
		return false
	}
	if !v.document(definition, relPath, raw) {
		return false
	}
	if err := json.Unmarshal(raw, target); err != nil {
		v.problem("%s: %v", relPath, err)
		return false
	}
	return true
}

// loadValidator returns the schema validator to check against.
func loadValidator(schemaPath string) (*schemaValidator, error) {
	if schemaPath != "" {
		// --schema is an operator-supplied path: a schema document is a build
		// input, not user content, and reading it is the whole point of the flag.
		raw, err := os.ReadFile(schemaPath) //nolint:gosec // G304: operator-supplied schema path.
		if err != nil {
			return nil, fmt.Errorf("read schema: %w", err)
		}
		return schemaFromJSON(raw)
	}
	// The emitted schema rather than the in-memory one: the validator reads
	// decoded JSON, and routing both worlds through the same bytes means the
	// verifier and cmd/gen-types can never disagree about what the schema says.
	raw, err := aggmodel.MarshalSchema()
	if err != nil {
		return nil, err
	}
	return schemaFromJSON(raw)
}

// selectPartitions filters the manifest by the requested segment.
func selectPartitions(partitions []aggmodel.Partition, seg aggmodel.Seg) []aggmodel.Partition {
	out := make([]aggmodel.Partition, 0, len(partitions))
	for _, partition := range partitions {
		if seg.Patch != "" && partition.Patch != seg.Patch {
			continue
		}
		if seg.Region != "" && partition.Region != seg.Region {
			continue
		}
		if seg.Queue != 0 && partition.Queue != seg.Queue {
			continue
		}
		if seg.Bracket != "" && partition.Bracket != seg.Bracket {
			continue
		}
		out = append(out, partition)
	}
	return out
}

func describeSeg(seg aggmodel.Seg) string {
	parts := make([]string, 0, 4)
	if seg.Patch != "" {
		parts = append(parts, "patch="+seg.Patch)
	}
	if seg.Region != "" {
		parts = append(parts, "region="+seg.Region)
	}
	if seg.Queue != 0 {
		parts = append(parts, fmt.Sprintf("queue=%d", seg.Queue))
	}
	if seg.Bracket != "" {
		parts = append(parts, "bracket="+string(seg.Bracket))
	}
	if len(parts) == 0 {
		return "any partition"
	}
	return strings.Join(parts, " ")
}

// duplicatePartitions reports a manifest that lists the same partition twice.
// The site build prerenders a route per entry, so a duplicate is a build that
// fails for a reason nobody would guess from the message.
func (v *verifier) duplicatePartitions(partitions []aggmodel.Partition) {
	seen := make(map[string]bool, len(partitions))
	for _, partition := range partitions {
		key := aggmodel.Seg{
			Patch: partition.Patch, Region: partition.Region,
			Queue: partition.Queue, Bracket: partition.Bracket,
		}.Dir()
		if seen[key] {
			v.problem("manifest: partition %s is listed more than once", key)
			continue
		}
		seen[key] = true
	}
}

// latest checks that the manifest's latest pointer names the newest partition
// it lists. The landing page reads Latest, so a stale pointer shows an old patch
// while the tree holds a new one.
func (v *verifier) latest(manifest aggmodel.Manifest) {
	if len(manifest.Partitions) == 0 {
		v.problem("manifest: no partitions")
		return
	}
	patches := make([]string, 0, len(manifest.Partitions))
	for _, partition := range manifest.Partitions {
		patches = append(patches, partition.Patch)
	}
	newestPatchName := newestPatch(patches)
	newest := manifest.Partitions[0]
	for _, partition := range manifest.Partitions {
		if partition.Patch == newestPatchName {
			newest = partition
			break
		}
	}
	latest := manifest.Latest
	if latest.Patch != newest.Patch || latest.Region != newest.Region ||
		latest.Queue != newest.Queue || latest.Bracket != newest.Bracket {
		v.problem("manifest: latest is %s but the newest partition is %s",
			partitionLabel(latest), partitionLabel(newest))
	}
}

func partitionLabel(partition aggmodel.Partition) string {
	return aggmodel.Seg{
		Patch: partition.Patch, Region: partition.Region,
		Queue: partition.Queue, Bracket: partition.Bracket,
	}.Dir()
}

// partition checks one published partition and returns the number of cells it
// carried.
func (v *verifier) partition(partition aggmodel.Partition, manifest aggmodel.Manifest) int {
	seg := aggmodel.Seg{
		Patch: partition.Patch, Region: partition.Region,
		Queue: partition.Queue, Bracket: partition.Bracket,
	}
	if err := seg.Validate(); err != nil {
		v.problem("manifest: partition %s: %v", segmentLabel(seg), err)
		return 0
	}
	label := partitionLabel(partition)
	dir := filepath.Join(v.aggRoot, filepath.FromSlash(seg.Dir()))
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		v.problem("%s: partition directory is missing", label)
		return 0
	}
	if partition.CellsPublished <= 0 {
		v.problem("%s: cells_published is %d", label, partition.CellsPublished)
	}
	if partition.MinCellN < 1 {
		v.problem("%s: min_cell_n is %d", label, partition.MinCellN)
	}

	var tierList aggmodel.TierList
	if !v.readSchemaValidated("TierList", seg.TierListPath(), &tierList) {
		return 0
	}
	v.checkEnvelope(label, seg.TierListPath(), tierList.Envelope, partition, manifest.Source)

	published := 0
	for i, cell := range tierList.Cells {
		where := fmt.Sprintf("%s: cell %d (champion %d)", seg.TierListPath(), i, cell.ChampionID)
		if v.checkCell(where, cell, partition.MinCellN) {
			published++
		}
	}
	if published != partition.CellsPublished {
		v.problem("%s: tierlist holds %d publishable cells but the manifest says %d",
			seg.TierListPath(), published, partition.CellsPublished)
	}
	v.checkCellOrder(seg.TierListPath(), tierList.Cells)

	v.checkChampions(seg, partition, manifest.Source)
	v.checkMatchups(seg, partition, manifest.Source)
	return published
}

// checkEnvelope checks a document's provenance against the partition it belongs
// to. Every artifact of a partition is written from one envelope, so any
// difference is either a stale file left behind by a failed publish or a
// document assembled from another partition's data.
func (v *verifier) checkEnvelope(label, relPath string, envelope aggmodel.Envelope, partition aggmodel.Partition, source aggmodel.Source) {
	if envelope.Schema != aggmodel.SchemaVersion {
		v.problem("%s: schema %d is not %d", relPath, envelope.Schema, aggmodel.SchemaVersion)
	}
	if !envelope.Source.Valid() {
		v.problem("%s: source %q is not a source this project defines", relPath, envelope.Source)
	}
	if envelope.Source != source {
		v.problem("%s: source is %q but the manifest says %q", relPath, envelope.Source, source)
	}
	if envelope.Patch != partition.Patch || envelope.Region != partition.Region ||
		envelope.Queue != partition.Queue || envelope.Bracket != partition.Bracket {
		v.problem("%s: envelope segment %s does not match partition %s", relPath,
			segmentLabel(aggmodel.Seg{
				Patch: envelope.Patch, Region: envelope.Region,
				Queue: envelope.Queue, Bracket: envelope.Bracket,
			}), label)
	}
	if envelope.MinCellN != partition.MinCellN {
		v.problem("%s: min_cell_n is %d but the manifest says %d", relPath, envelope.MinCellN, partition.MinCellN)
	}
	if envelope.SuppressedCells != partition.SuppressedCells {
		v.problem("%s: suppressed_cells is %d but the manifest says %d",
			relPath, envelope.SuppressedCells, partition.SuppressedCells)
	}
	if envelope.SourceWindow != partition.SourceWindow {
		v.problem("%s: source_window is %s..%s but the manifest says %s..%s", relPath,
			envelope.SourceWindow.From, envelope.SourceWindow.To,
			partition.SourceWindow.From, partition.SourceWindow.To)
	}
	if !envelope.GeneratedAt.Equal(partition.GeneratedAt) {
		v.problem("%s: generated_at is %s but the manifest says %s", relPath,
			envelope.GeneratedAt.Format(rfc3339), partition.GeneratedAt.Format(rfc3339))
	}
	if envelope.Patch != "" {
		from, fromErr := parseDate(envelope.SourceWindow.From)
		to, toErr := parseDate(envelope.SourceWindow.To)
		if fromErr != nil {
			v.problem("%s: source_window.from: %v", relPath, fromErr)
		}
		if toErr != nil {
			v.problem("%s: source_window.to: %v", relPath, toErr)
		}
		// The window is a claim about which days the numbers cover, and a
		// window that ends before it starts is impossible. The check lives
		// here rather than in the writer because the writer cannot produce one:
		// windowForEnd derives the start from a day count, so the only way an
		// inverted window reaches a reader is an artifact damaged on disk -
		// which is what this verifier exists for.
		if fromErr == nil && toErr == nil && to.Before(from) {
			v.problem("%s: source_window %s..%s ends before it starts",
				relPath, envelope.SourceWindow.From, envelope.SourceWindow.To)
		}
	}
}

// rfc3339 is the timestamp layout used in messages. It is a constant so the
// messages do not depend on a local time zone.
const rfc3339 = "2006-01-02T15:04:05.999999999Z07:00"

// checkCell checks one published cell and reports whether it is publishable.
//
// The checks are the contract's rule 1 (a rate never travels without its
// sample size) and rule 2 (a thin cell is suppressed, never published),
// expressed against the artifact rather than against the build.
func (v *verifier) checkCell(where string, cell aggmodel.Cell, minCellN int) bool {
	v.cells++
	ok := true
	if cell.ChampionID <= 0 {
		v.problem("%s: champion_id is %d", where, cell.ChampionID)
		ok = false
	}
	if !cell.Role.Valid() {
		v.problem("%s: role %q is not canonical", where, cell.Role)
		ok = false
	}
	if cell.N < minCellN {
		v.problem("%s: n=%d is below min_cell_n=%d, so the cell should have been suppressed",
			where, cell.N, minCellN)
		ok = false
	}
	if cell.Wins < 0 || cell.Wins > cell.N {
		v.problem("%s: wins %d is outside [0,%d]", where, cell.Wins, cell.N)
		ok = false
	}
	// The range is closed at zero on purpose: a champion can genuinely lose
	// every classified game of a window, so a published win rate of 0 is real
	// data rather than a defect. Only the exclusive lower bound would be wrong.
	if cell.WinRate < 0 || cell.WinRate > 1 {
		v.problem("%s: win_rate %v is outside [0,1]", where, cell.WinRate)
		ok = false
	}
	if expected := round4(float64(cell.Wins) / float64(cell.N)); !closeEnough(cell.WinRate, expected) {
		v.problem("%s: win_rate %v is not wins/n %v", where, cell.WinRate, expected)
		ok = false
	}
	if expected := round4(ConfidenceHalfWidthFactor / math.Sqrt(float64(cell.N))); !closeEnough(cell.CI95HalfWidth, expected) {
		v.problem("%s: ci95_half_width %v is not 0.98/sqrt(n) %v", where, cell.CI95HalfWidth, expected)
		ok = false
	}
	if cell.PickRate < 0 || cell.PickRate > 1 {
		v.problem("%s: pick_rate %v is outside [0,1]", where, cell.PickRate)
		ok = false
	}
	if cell.BanRate < 0 || cell.BanRate > 1 {
		v.problem("%s: ban_rate %v is outside [0,1]", where, cell.BanRate)
		ok = false
	}
	if !tierBandKnown(cell.Tier) {
		v.problem("%s: tier %q is not one of the published grades", where, cell.Tier)
		ok = false
	}
	return ok
}

// checkCellOrder checks the two ordering rules the contract fixes for a tier
// list: one row per (champion, role), ordered by role, then by descending win
// rate, then by champion id.
//
// The tier check rides along with it. A tier is a monotone function of the win
// rate within a partition, so a tier list sorted by descending win rate must
// have non-decreasing tier ranks. A tier that breaks that order was computed
// against a different baseline, which is the one way a published grade can be
// wrong without any single field looking wrong.
func (v *verifier) checkCellOrder(relPath string, cells []aggmodel.Cell) {
	seen := make(map[[2]int]bool, len(cells))
	previous := aggmodel.Cell{}
	havePrevious := false
	previousRank := -1

	for i, cell := range cells {
		if !cell.Role.Valid() {
			continue
		}
		key := [2]int{roleRankOf(cell.Role), cell.ChampionID}
		if seen[key] {
			v.problem("%s: champion %d appears twice in role %s", relPath, cell.ChampionID, cell.Role)
		}
		seen[key] = true

		if havePrevious && previous.Role == cell.Role {
			if cell.WinRate > previous.WinRate {
				v.problem("%s: cell %d (champion %d, win_rate %v) is out of descending win-rate order",
					relPath, i, cell.ChampionID, cell.WinRate)
			}
			if cell.WinRate == previous.WinRate && cell.ChampionID < previous.ChampionID {
				v.problem("%s: cell %d (champion %d) is out of champion-id order after an equal win rate",
					relPath, i, cell.ChampionID)
			}
		}
		if rank := tierRankOf(cell.Tier); rank >= 0 {
			if havePrevious && previous.Role == cell.Role && rank < previousRank {
				v.problem("%s: cell %d (champion %d) is graded %s after a better grade with a lower win rate",
					relPath, i, cell.ChampionID, cell.Tier)
			}
			previousRank = rank
		}
		previous = cell
		havePrevious = true
	}
}

// checkChampions checks the champion documents of one partition: one file per
// champion the manifest lists, no orphan files, and every build row inside them
// consistent with its own sample size.
func (v *verifier) checkChampions(seg aggmodel.Seg, partition aggmodel.Partition, source aggmodel.Source) {
	dir := filepath.Join(v.aggRoot, filepath.FromSlash(seg.Dir()), "champions")
	onDisk, err := readNumericFileNames(dir)
	if err != nil {
		v.problem("%s/champions: %v", seg.Dir(), err)
	}

	listed := make(map[int]bool, len(partition.Champions))
	for _, championID := range partition.Champions {
		listed[championID] = true
		var champion aggmodel.Champion
		relPath := seg.ChampionPath(championID)
		if !v.readSchemaValidated("Champion", relPath, &champion) {
			continue
		}
		v.checkEnvelope(relPath, relPath, champion.Envelope, partition, source)
		if champion.ChampionID != championID {
			v.problem("%s: champion_id is %d but the file is named %d", relPath, champion.ChampionID, championID)
		}
		roles := make(map[aggmodel.Role]bool, len(champion.Roles))
		for _, championRole := range champion.Roles {
			where := fmt.Sprintf("%s: role %s", relPath, championRole.Role)
			if !championRole.Role.Valid() {
				v.problem("%s: role %q is not canonical", relPath, championRole.Role)
				continue
			}
			if roles[championRole.Role] {
				v.problem("%s: role %s appears twice", relPath, championRole.Role)
			}
			roles[championRole.Role] = true
			if championRole.Stats.ChampionID != championID {
				v.problem("%s: stats.champion_id is %d", where, championRole.Stats.ChampionID)
			}
			if championRole.Stats.Role != championRole.Role {
				v.problem("%s: stats.role is %s", where, championRole.Stats.Role)
			}
			v.checkCell(where, championRole.Stats, partition.MinCellN)
			v.checkBuilds(where+".items", championRole.Items, BuildKindItems, 7, championRole.Stats.N)
			v.checkBuilds(where+".runes", championRole.Runes, BuildKindRunes, 6, championRole.Stats.N)
			v.checkBuilds(where+".spells", championRole.Spells, BuildKindSpells, 2, championRole.Stats.N)
			if len(championRole.Items) > MaxBuildsPerRole ||
				len(championRole.Runes) > MaxBuildsPerRole ||
				len(championRole.Spells) > MaxBuildsPerRole {
				v.problem("%s: more than %d builds of one kind", where, MaxBuildsPerRole)
			}
		}
	}
	for _, championID := range onDisk {
		if !listed[championID] {
			v.problem("%s/champions/%d.json: file is not listed in the manifest", seg.Dir(), championID)
		}
	}
}

// checkBuilds checks one build list: the kind matches the artifact section, the
// key has the length the kind's documentation implies, and the rate belongs to
// the sample size next to it.
func (v *verifier) checkBuilds(where string, builds []aggmodel.Build, kind string, keyLength int, cellN int) {
	for i, build := range builds {
		at := fmt.Sprintf("%s[%d]", where, i)
		if build.Kind != kind {
			v.problem("%s: kind is %q, expected %q", at, build.Kind, kind)
		}
		if len(build.Key) != keyLength {
			v.problem("%s: key has %d parts, expected %d", at, len(build.Key), keyLength)
		}
		if build.Label == "" {
			v.problem("%s: label is empty", at)
		}
		if build.N <= 0 || build.N > cellN {
			v.problem("%s: n=%d is not a subset of the cell's %d games", at, build.N, cellN)
		}
		if build.Wins < 0 || build.Wins > build.N {
			v.problem("%s: wins %d is outside [0,%d]", at, build.Wins, build.N)
		}
		if build.N > 0 {
			if expected := round4(float64(build.Wins) / float64(build.N)); !closeEnough(build.WinRate, expected) {
				v.problem("%s: win_rate %v is not wins/n %v", at, build.WinRate, expected)
			}
		}
	}
}

// checkMatchups checks the matchup artifacts of one partition: exactly the roles
// the manifest lists, a rate per pair, the sample floor, and an axis that covers
// every champion a surviving pair mentions.
func (v *verifier) checkMatchups(seg aggmodel.Seg, partition aggmodel.Partition, source aggmodel.Source) {
	dir := filepath.Join(v.aggRoot, filepath.FromSlash(seg.Dir()), "matchups")
	onDisk, err := readDirNames(dir)
	if err != nil {
		v.problem("%s/matchups: %v", seg.Dir(), err)
	}
	listed := make(map[string]bool, len(partition.MatchupRoles))

	for _, role := range partition.MatchupRoles {
		if !role.Valid() {
			v.problem("manifest: partition %s lists the unknown matchup role %q", segmentLabel(seg), role)
			continue
		}
		listed[role.Slug()+".json"] = true
		relPath := seg.MatchupsPath(role)
		var matchups aggmodel.Matchups
		if !v.readSchemaValidated("Matchups", relPath, &matchups) {
			continue
		}
		v.checkEnvelope(relPath, relPath, matchups.Envelope, partition, source)
		if matchups.Role != role {
			v.problem("%s: role is %s, expected %s", relPath, matchups.Role, role)
		}
		axis := make(map[int]bool, len(matchups.Champions))
		for i, championID := range matchups.Champions {
			if i > 0 && championID <= matchups.Champions[i-1] {
				v.problem("%s: the champion axis is not ascending", relPath)
				break
			}
			axis[championID] = true
		}
		for i, pair := range matchups.Cells {
			where := fmt.Sprintf("%s: pair %d (%d vs %d)", relPath, i, pair.ChampionID, pair.OpponentID)
			if pair.ChampionID <= 0 || pair.OpponentID <= 0 {
				v.problem("%s: champion ids must be positive", where)
				continue
			}
			if pair.ChampionID >= pair.OpponentID {
				v.problem("%s: only the lower-id direction is published", where)
			}
			if i > 0 {
				previous := matchups.Cells[i-1]
				if pair.ChampionID < previous.ChampionID ||
					(pair.ChampionID == previous.ChampionID && pair.OpponentID < previous.OpponentID) {
					v.problem("%s: pairs are not in ascending order", where)
				}
			}
			if pair.N < partition.MinCellN {
				v.problem("%s: n=%d is below min_cell_n=%d", where, pair.N, partition.MinCellN)
			}
			if pair.Wins < 0 || pair.Wins > pair.N || pair.N <= 0 {
				v.problem("%s: wins %d is outside [0,%d]", where, pair.Wins, pair.N)
				continue
			}
			if expected := round4(float64(pair.Wins) / float64(pair.N)); !closeEnough(pair.WinRate, expected) {
				v.problem("%s: win_rate %v is not wins/n %v", where, pair.WinRate, expected)
			}
			if expected := round4(ConfidenceHalfWidthFactor / math.Sqrt(float64(pair.N))); !closeEnough(pair.CI95HalfWidth, expected) {
				v.problem("%s: ci95_half_width %v is not 0.98/sqrt(n) %v", where, pair.CI95HalfWidth, expected)
			}
			if !axis[pair.ChampionID] || !axis[pair.OpponentID] {
				v.problem("%s: the champion axis does not cover both champions", where)
			}
		}
	}
	for _, name := range onDisk {
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if !listed[name] {
			v.problem("%s/matchups/%s: file is not listed in the manifest", seg.Dir(), name)
		}
	}
}

func segmentLabel(seg aggmodel.Seg) string { return seg.Dir() }

// closeEnough compares two rounded rates. The comparison is exact in principle
// (both sides are round4 of the same division), and the tolerance only covers a
// reader that reformatted the number on its way through JSON.
func closeEnough(got, want float64) bool {
	return math.Abs(got-want) <= 1e-9
}

// tierRankOf returns the position of a grade in the published order, or -1.
func tierRankOf(tier aggmodel.Tier) int {
	for i, candidate := range aggmodel.Tiers {
		if candidate == tier {
			return i
		}
	}
	return -1
}

func tierBandKnown(tier aggmodel.Tier) bool { return tierRankOf(tier) >= 0 }

// roleRankOf returns the canonical order of a role, used to key cell
// uniqueness. An unknown role sorts last, which the role check reports anyway.
func roleRankOf(role aggmodel.Role) int {
	for i, candidate := range aggmodel.Roles {
		if candidate == role {
			return i
		}
	}
	return len(aggmodel.Roles)
}
