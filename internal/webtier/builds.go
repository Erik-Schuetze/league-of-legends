package webtier

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// The Data Dragon projection behind a build row.
//
// A `Build` in the aggregate model is ids and numbers: `key: [3078, 3111]` is a
// build, and nothing in the artifact says what those ids are called. This file
// is the tier's answer to that, ported from web/src/lib/build-lookup.ts, and it
// is the only reason a build row can render an icon instead of a number.
//
// Two sources, in this order:
//
//  1. the checked-in projection (web/src/data/{items,runes,spells}.json, or the
//     copy embedded in this binary), which is what makes a build work offline;
//  2. the aggregate tree's own static projection
//     (agg/v1/static/<ddragon_version>/{items,runes,summoner-spells}.json),
//     which wins per id when it is there, exactly as the tree's champion
//     projection wins over the checked-in one.
//
// Anything that is neither is a hard failure rather than a blank cell: a
// game-data file whose shape changed must stop the render, not publish a page
// whose icons silently disappeared. The failure is an *ArtifactError, so the
// server answers it with the 503 error page.

// buildEntry is the frozen BuildLookupEntry from ADR-006: a display name, used
// as the icon's alt text, and an absolute Data Dragon CDN URL used verbatim as
// the img src.
type buildEntry struct {
	Name string
	Icon string
}

// buildLookup is the resolved projection for one Data Dragon version.
type buildLookup struct {
	ddragonVersion string
	items          map[int]buildEntry
	runes          map[int]buildEntry
	spells         map[int]buildEntry
}

// entry resolves one build key. A missing id is reported as missing rather than
// as an error: the component renders the id itself, because an unknown id is a
// fact about the projection and not a reason to drop the row.
func (l *buildLookup) entry(kind string, id int) (buildEntry, bool) {
	var entries map[int]buildEntry
	switch kind {
	case buildKindItems:
		entries = l.items
	case buildKindRunes:
		entries = l.runes
	case buildKindSpells:
		entries = l.spells
	}
	entry, ok := entries[id]
	return entry, ok
}

// The kinds a build key can be, spelled the way the artifact spells them.
const (
	buildKindItems  = "items"
	buildKindRunes  = "runes"
	buildKindSpells = "spells"
)

// readCheckedInProjection reads one keyed projection: `{ "<id>": { name, icon } }`.
//
// The file is validated rather than trusted, exactly as build-lookup.ts does: a
// non-numeric key, an entry without a name and an icon, or an empty file is a
// fault that names the file that is wrong. The reference accepts a key with a
// trailing non-digit ("103abc" parses as 103 in JavaScript); this port rejects
// it, because no generator has ever emitted one and a file that contains one is
// more likely to be the wrong file than a slightly odd one.
func (l *Loader) readCheckedInProjection(name string) (map[int]buildEntry, error) {
	raw, path, err := l.checkedInData(name)
	if err != nil {
		return nil, err
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, dataFault(path, "is not valid JSON: %v", err)
	}
	if record == nil {
		return nil, dataFault(path, "must be an object keyed by numeric Data Dragon id.")
	}
	entries := make(map[int]buildEntry, len(record))
	for key, value := range record {
		id, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil {
			return nil, dataFault(path, "key %q is not a numeric Data Dragon id.", key)
		}
		var entry struct {
			Name string `json:"name"`
			Icon string `json:"icon"`
		}
		if err := json.Unmarshal(value, &entry); err != nil {
			return nil, dataFault(path, "entry %s must be { name, icon }: %v", key, err)
		}
		if strings.TrimSpace(entry.Name) == "" || strings.TrimSpace(entry.Icon) == "" {
			return nil, dataFault(path, "entry %s must be { name, icon }.", key)
		}
		entries[id] = buildEntry{Name: entry.Name, Icon: entry.Icon}
	}
	if len(entries) == 0 {
		return nil, dataFault(path, "has no entries.")
	}
	return entries, nil
}

// treeProjection reads the tree's static projection for one kind. A tree that
// published no such file is not a fault - the reference reads it as null and
// carries on with the checked-in projection - but a file that is there and
// unreadable is one, because it is an incomplete publication.
func treeProjection(site *Site, kind string) (map[int]buildEntry, error) {
	rows, err := treeRows(site, kind)
	if err != nil {
		if errors.Is(err, ErrArtifactMissing) || errors.Is(err, ErrNoSnapshot) {
			return map[int]buildEntry{}, nil
		}
		return nil, err
	}
	entries := make(map[int]buildEntry, len(rows))
	for _, row := range rows {
		if row.ID > 0 && row.Name != "" && row.Icon != "" {
			entries[row.ID] = buildEntry{Name: row.Name, Icon: row.Icon}
		}
	}
	return entries, nil
}

// idEntry is one row of a tree projection, reduced to the three fields the
// lookup uses. The three artifact types are structurally identical here, so
// they are flattened rather than handled three times below.
type idEntry struct {
	ID   int
	Name string
	Icon string
}

func treeRows(site *Site, kind string) ([]idEntry, error) {
	switch kind {
	case buildKindItems:
		items, err := site.Items()
		if err != nil || items == nil {
			return nil, err
		}
		rows := make([]idEntry, 0, len(items.Items))
		for _, row := range items.Items {
			rows = append(rows, idEntry{ID: row.ID, Name: row.Name, Icon: row.Icon})
		}
		return rows, nil
	case buildKindRunes:
		runes, err := site.Runes()
		if err != nil || runes == nil {
			return nil, err
		}
		rows := make([]idEntry, 0, len(runes.Runes))
		for _, row := range runes.Runes {
			rows = append(rows, idEntry{ID: row.ID, Name: row.Name, Icon: row.Icon})
		}
		return rows, nil
	case buildKindSpells:
		spells, err := site.SummonerSpells()
		if err != nil || spells == nil {
			return nil, err
		}
		rows := make([]idEntry, 0, len(spells.Spells))
		for _, row := range spells.Spells {
			rows = append(rows, idEntry{ID: row.ID, Name: row.Name, Icon: row.Icon})
		}
		return rows, nil
	}
	return nil, nil
}

// buildLookups caches the resolved projection per Data Dragon version. A page
// per champion and per role is rendered from the same projection, so re-reading
// and re-validating a 100 kB file hundreds of times would be work with no
// reader benefit.
type buildLookups struct {
	mu      sync.Mutex
	entries map[string]*buildLookup
}

// BuildLookup resolves the projection the site's Data Dragon version uses.
func (l *Loader) BuildLookup(site *Site) (*buildLookup, error) {
	if site == nil {
		return nil, ErrNoSnapshot
	}
	version := site.DdragonVersion()

	l.builds.mu.Lock()
	if cached, ok := l.builds.entries[version]; ok {
		l.builds.mu.Unlock()
		return cached, nil
	}
	l.builds.mu.Unlock()

	lookup := &buildLookup{ddragonVersion: version}
	for _, spec := range []struct {
		kind string
		file string
		out  *map[int]buildEntry
	}{
		{buildKindItems, itemsDataIn, &lookup.items},
		{buildKindRunes, runesDataIn, &lookup.runes},
		{buildKindSpells, spellsDataIn, &lookup.spells},
	} {
		checkedIn, err := l.readCheckedInProjection(spec.file)
		if err != nil {
			return nil, err
		}
		tree, err := treeProjection(site, spec.kind)
		if err != nil {
			return nil, err
		}
		merged := make(map[int]buildEntry, len(checkedIn)+len(tree))
		for id, entry := range checkedIn {
			merged[id] = entry
		}
		for id, entry := range tree {
			merged[id] = entry
		}
		*spec.out = merged
	}

	l.builds.mu.Lock()
	if l.builds.entries == nil {
		l.builds.entries = map[string]*buildLookup{}
	}
	l.builds.entries[version] = lookup
	l.builds.mu.Unlock()
	return lookup, nil
}

// BuildLookup is the site's convenience wrapper, so a view reaches the
// projection the same way it reaches any other artifact.
func (s *Site) BuildLookup() (*buildLookup, error) { return s.loader.BuildLookup(s) }

// buildRow is one rendered build row: the ids in the order the artifact
// carried them, each already resolved to an icon or left as a chip, plus the
// artifact's own label and the two published numbers.
type buildRow struct {
	Keys  []buildKey
	Label string
	// Matches and Win are the two published numbers, already formatted: the
	// design system's StatValue is presentational and takes the text it shows.
	Matches dsStat
	Win     dsStat
}

// dsStat is one design-system StatValue: a label and a value, with the value's
// format recorded in data-format so the island can re-render it.
type dsStat struct {
	Format string
	Label  string
	Value  string
}

// buildKey is one cell of a build row: either an image with alt text, or the
// numeric id when the projection cannot name it.
type buildKey struct {
	ID     int
	Name   string
	Icon   string
	HasImg bool
}

// buildList is one "Items on Ahri in top" section.
type buildList struct {
	Title string
	Rows  []buildRow
	// EmptyMessage is the component's own default, kept here so the empty
	// section reads the same as it did in the reference build.
	EmptyMessage string
}

// buildLimit is BuildList.astro's `limit = 10`. The aggregator already sorted
// by n descending, so the component renders the order it was given and only
// ever truncates.
const buildLimit = 10

// buildSection builds one section from the artifact's list. The kind spells the
// section's subject the way the DS component's title does ("Items on Ahri in
// top"), so the caller passes the title and the kind separately.
func buildSection(title string, kind string, builds []aggmodel.Build, lookup *buildLookup) buildList {
	section := buildList{Title: title, EmptyMessage: "No data for this selection."}
	shown := builds
	if len(shown) > buildLimit {
		shown = shown[:buildLimit]
	}
	for _, build := range shown {
		row := buildRow{
			Label: build.Label,
			Matches: dsStat{
				Format: "integer",
				Label:  "matches",
				Value:  Integer(float64(build.N)),
			},
			Win: dsStat{
				Format: "percent",
				Label:  "win",
				Value:  Percent(build.WinRate, 2),
			},
		}
		for _, id := range build.Key {
			key := buildKey{ID: id}
			if entry, ok := lookup.entry(kind, id); ok {
				key.Name, key.Icon, key.HasImg = entry.Name, entry.Icon, true
			}
			row.Keys = append(row.Keys, key)
		}
		section.Rows = append(section.Rows, row)
	}
	return section
}

// skillOrderRow is one row of the skill-order table.
type skillOrderRow struct {
	Order string
	N     int
	Win   float64
}

// skillOrders is the artifact's skill-order list, sorted by n descending. The
// fallback table declares initialSortKey="n", initialSortDir="desc", and the
// reference sorts it in the browser; a server-rendered table has to sort it
// here, because with JavaScript disabled a reader would otherwise see whatever
// order the artifact happened to use.
func skillOrders(orders []aggmodel.SkillOrder) []skillOrderRow {
	rows := make([]skillOrderRow, 0, len(orders))
	for _, order := range orders {
		rows = append(rows, skillOrderRow{Order: order.Order, N: order.N, Win: order.WinRate})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].N > rows[j].N })
	return rows
}
