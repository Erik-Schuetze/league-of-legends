// Package raw is the immutable payload archive: append-only zstd Parquet
// parts, written before the database, in the layout the aggregator and DuckDB
// read back.
//
// The ordering is the whole point. A match payload can be fetched exactly once
// - once the queue row is claimed, the only copy of the response is whatever
// this package writes - so the archive is written first and the control plane
// second. A crash in between costs a duplicate archive row (harmless) and
// never costs a payload (unrecoverable).
//
// # Layout
//
//	raw/riot/match-v5/v1/dt=YYYY-MM-DD/part-00001.parquet.zst
//	raw/riot/league-v4/v1/dt=YYYY-MM-DD/region=EUW/part-00001.parquet.zst
//	raw/riot/ddragon/v1/dt=YYYY-MM-DD/kind=champions/part-00001.parquet.zst
//
// dt= is the *fetch* date, not the game date, so a partition is complete the
// day it is closed and a late-arriving match lands in the run that fetched it.
// Partition columns are repeated inside the files as ordinary columns: a part
// file is readable on its own, which matters when an operator copies one
// partition out to debug it.
//
// # Parts
//
// Parts are written to a `.tmp` sibling and renamed once the footer is on
// disk. That makes a part either absent or complete: a reader that globs the
// archive can never see a half-written footer, which is what lets the
// aggregator read the archive while the crawler writes it.
package raw

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
)

// API directory names. They match the Riot API family, not the URL path, so
// the archive stays stable if Riot reshuffles its routes.
const (
	APIMatch   = "match-v5"
	APILeague  = "league-v4"
	APIAccount = "account-v1"
	APIDDragon = "ddragon"
	// APITimeline is a directory of its own rather than a second row type
	// inside match-v5, because the two payloads have different retention at
	// Riot - a summary lives two years and a timeline one - so a partition
	// that mixed them would answer "what do we have for this date" with a
	// half-empty answer for one of the two.
	APITimeline = "match-v5-timeline"
)

// Payload schema versions, recorded as a column rather than as a directory
// segment.
//
// The layout the plan froze - raw/riot/<api>/dt=<date>/ - has no version
// directory, and the aggregation reads that exact shape. Versioning still
// matters, so it lives where a query can filter on it: every row carries
// payload_version, and a v2 reader selects what it understands instead of
// having to be told which directory to open.
const (
	MatchPayloadVersion    = "1"
	LeaguePayloadVersion   = "1"
	AccountPayloadVersion  = "1"
	StaticPayloadVersion   = "1"
	TimelinePayloadVersion = "1"
)

// archiveRoot joins the archive root with the api prefix shared by every
// partition of that api.
func archiveRoot(root, api string) string {
	return filepath.Join(root, "riot", api)
}

// MatchDir is the partition directory for matches fetched on date (YYYY-MM-DD).
func MatchDir(root, date string) string {
	return filepath.Join(archiveRoot(root, APIMatch), "dt="+date)
}

// LeagueDir is the partition directory for LEAGUE-V4 pages fetched on date.
// The region is part of the partition because a ladder page is region-scoped
// and a reader almost always wants one region.
func LeagueDir(root, date, region string) string {
	return filepath.Join(archiveRoot(root, APILeague), "dt="+date, "region="+strings.ToUpper(region))
}

// AccountDir is the partition directory for ACCOUNT-V1 lookups.
func AccountDir(root, date string) string {
	return filepath.Join(archiveRoot(root, APIAccount), "dt="+date)
}

// StaticDir is the partition directory for one Data Dragon document kind
// fetched on date.
func StaticDir(root, date, kind string) string {
	return filepath.Join(archiveRoot(root, APIDDragon), "dt="+date, "kind="+kind)
}

// MatchPartitionURI is the archive path recorded in matches.raw_uri.
//
// It names the partition rather than the part file. The part a payload lands in
// is decided by whichever writer held the open part, so a URI naming a file
// would have to be written after the fact; the partition is where a reader
// looks, and it is known before the payload is written. That is what makes the
// write order - archive first, database second - safe: the database row can
// always be reconstructed from the archive, and never the other way round.
func MatchPartitionURI(root string, meta contract.MatchMeta) string {
	return MatchDir(root, meta.PartitionDate())
}

// TimelineDir is the partition directory for match timelines fetched on date.
//
// There is no TimelinePartitionURI, because no control-plane column records one.
// A timeline has no row of its own in `matches`; the fetch_queue row saying a
// timeline was fetched is the record, and a reader that wants the payload joins
// the archive on match_id. Adding a second URI column would be a second source
// of truth for a path that is already derivable.
func TimelineDir(root, date string) string {
	return filepath.Join(archiveRoot(root, APITimeline), "dt="+date)
}

// MatchRow is one fetched match, payload and provenance together.
//
// Payload is the verbatim response body. It is the reason this archive exists:
// Riot keeps two years of match history and this project intends to outlive
// that, so the bytes are retained even though the typed columns next to them
// are all v1 reads.
type MatchRow struct {
	MatchID string `parquet:"match_id"`
	Region  string `parquet:"region"`
	QueueID int32  `parquet:"queue_id"`
	// Patch is the two-component game version ("16.18"). It is echoed rather
	// than derived so a reader does not have to reimplement Riot's versioning.
	Patch          string    `parquet:"patch"`
	GameVersion    string    `parquet:"game_version"`
	GameCreationMS int64     `parquet:"game_creation_ms"`
	GameDurationS  int32     `parquet:"game_duration_s"`
	PayloadVersion string    `parquet:"payload_version"`
	FetchedAt      time.Time `parquet:"fetched_at,timestamp"`
	Payload        string    `parquet:"payload"`
	PayloadSHA256  string    `parquet:"payload_sha256"`
}

// TimelineRow is one fetched match timeline, payload and provenance together.
//
// The columns are deliberately identical to MatchRow's. A timeline's provenance
// *is* the summary's - same match id, queue, patch and game - and the same
// envelope SQL then reads either archive without a second extraction path. The
// one difference is where the row lives: raw/riot/match-v5-timeline/, because
// Riot retains a timeline for one year against the summary's two, so the two
// archives fill up and go stale on different clocks.
//
// There is no unique key. Riot cannot be re-asked for a timeline once it has
// aged out, so a re-fetch appends a second record rather than replacing the
// first, and the aggregation de-duplicates by match id at the single point
// where it reads the archive.
type TimelineRow struct {
	MatchID        string    `parquet:"match_id"`
	Region         string    `parquet:"region"`
	QueueID        int32     `parquet:"queue_id"`
	Patch          string    `parquet:"patch"`
	GameVersion    string    `parquet:"game_version"`
	GameCreationMS int64     `parquet:"game_creation_ms"`
	GameDurationS  int32     `parquet:"game_duration_s"`
	PayloadVersion string    `parquet:"payload_version"`
	FetchedAt      time.Time `parquet:"fetched_at,timestamp"`
	Payload        string    `parquet:"payload"`
	PayloadSHA256  string    `parquet:"payload_sha256"`
}

// LeagueRow is one LEAGUE-V4 page. The entries themselves are not columns:
// the page is retained verbatim and flattened by the reader, because seeding
// reads the page and the aggregation never reads this table at all.
type LeagueRow struct {
	Region         string    `parquet:"region"`
	QueueType      string    `parquet:"queue_type"`
	Tier           string    `parquet:"tier"`
	Division       string    `parquet:"division"`
	Entries        int32     `parquet:"entries"`
	PayloadVersion string    `parquet:"payload_version"`
	FetchedAt      time.Time `parquet:"fetched_at,timestamp"`
	Payload        string    `parquet:"payload"`
	PayloadSHA256  string    `parquet:"payload_sha256"`
}

// StaticRow is one Data Dragon document: the whole JSON body of a version,
// champion, item, rune or summoner-spell file.
//
// Static data is archived rather than fetched on demand because a site that
// renames a champion must not silently reinterpret last month's numbers. The
// aggregation joins on the version recorded here.
type StaticRow struct {
	Kind           string    `parquet:"kind"`
	Version        string    `parquet:"version"`
	Locale         string    `parquet:"locale"`
	PayloadVersion string    `parquet:"payload_version"`
	FetchedAt      time.Time `parquet:"fetched_at,timestamp"`
	Payload        string    `parquet:"payload"`
	PayloadSHA256  string    `parquet:"payload_sha256"`
}

// AccountRow is one ACCOUNT-V1 lookup: the puuid a Riot ID resolved to, kept
// because a name is not stable and the archive is the only place that can say
// what a name meant on a given day.
type AccountRow struct {
	PUUID          string    `parquet:"puuid"`
	GameName       string    `parquet:"game_name"`
	TagLine        string    `parquet:"tag_line"`
	PayloadVersion string    `parquet:"payload_version"`
	FetchedAt      time.Time `parquet:"fetched_at,timestamp"`
	Payload        string    `parquet:"payload"`
	PayloadSHA256  string    `parquet:"payload_sha256"`
}
