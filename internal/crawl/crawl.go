// Package crawl turns the frozen control plane (contract.Store) and the Riot
// client (internal/riot) into the crawler described in plan section 3.2: a
// breadth-first walk that seeds a frontier from the ladder, fetches match
// payloads into the immutable archive, and widens through the ten participants
// of every match it keeps.
//
// The package owns no schema and no HTTP details. It is the policy layer:
// what to fetch next, in which order, and what a failure means. Everything it
// persists goes through contract.Store, and every payload it retains goes
// through contract.RawWriter, so the archive is written before the database
// row and a crash between the two loses nothing.
package crawl

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
	"github.com/Erik-Schuetze/league-of-legends/internal/raw"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// DefaultRegion is the region label written into archive provenance when a
// caller does not say which regional cluster it is crawling. EUW matches the
// rest of the project's defaults; the crawler is otherwise region-agnostic.
const DefaultRegion = "EUW"

// Priority bands. Lower numbers are claimed first, because fetch_queue claims
// order by (priority, id). The bands are wide apart on purpose: a seed the
// operator asked for must never be overtaken by the tail of a breadth-first
// walk, and the widening band must never overtake player history.
const (
	// PrioritySeed is for ladder seeds: work an operator explicitly asked for.
	PrioritySeed = 0
	// PriorityPlayer is for a puuid's recent history, which is what keeps the
	// frontier moving and the sample fresh.
	PriorityPlayer = 100
	// PriorityParticipant is for the ten participants of a match that was
	// just fetched. It is the widening edge of the BFS and it is deliberately
	// behind stored history: the crawl must finish the players it already
	// knows before it chases the players it just discovered, or a single hot
	// match can crowd out the walk indefinitely.
	PriorityParticipant = 200
	// PriorityBackfill is for operator-driven re-runs over a bounded key
	// range. It is the least urgent thing the crawler does.
	PriorityBackfill = 300
)

// Deps is the set of collaborators every crawl command shares. Every field is
// optional except Store, Fetcher and Writer; the zero value of the rest is
// filled in by normalize, which keeps the command wiring short and the tests
// free to override only what they touch.
type Deps struct {
	Store   contract.Store
	Fetcher Fetcher
	Writer  contract.RawWriter
	Region  string
	Log     *slog.Logger
	Metrics obs.MetricsRecorder
	Clock   riot.Clock
	// Now is injectable so that partition dates, claim windows and backoff
	// deadlines are all derived from one clock. Tests that use a fake clock
	// get deterministic dt= directories and not_before values.
	Now func() time.Time
	// KeyExpiry is the operator's declaration of when the Riot key dies
	// (LOLSTATS_RIOT_API_KEY_EXPIRES_AT). The zero value declares nothing, and
	// then the crawl behaves as it always did: the key's age is a warning and
	// its death is discovered by Riot answering 401. When the deadline is
	// written down, the loop refuses to crawl past it instead of spending the
	// rest of the day's request budget on refusals.
	KeyExpiry riot.KeyExpiry
}

func (d *Deps) normalize() {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.Metrics == nil {
		d.Metrics = obs.NopRecorder{}
	}
	if d.Clock == nil {
		d.Clock = riot.RealClock{}
	}
	if d.Now == nil {
		d.Now = d.Clock.Now
	}
	if d.Region == "" {
		d.Region = DefaultRegion
	}
	if d.Region != "" {
		d.Region = strings.ToUpper(d.Region)
	}
}

// Fetcher is the part of the Riot client the crawler uses. It is an interface
// rather than *riot.Client so the crawl tests can drive a fake that speaks the
// same headers and statuses as the real server without a socket.
type Fetcher interface {
	// MatchWithPayload returns the summary and the verbatim response body.
	MatchWithPayload(ctx context.Context, matchID string) (riot.MatchDTO, []byte, error)
	MatchIDs(ctx context.Context, q riot.MatchListQuery) ([]string, error)
	LeagueEntriesWithPayload(ctx context.Context, q riot.LeagueQuery) ([]riot.LeagueEntryDTO, []byte, error)
	ApexLeague(ctx context.Context, queue, tier string) ([]riot.LeagueEntryDTO, []byte, error)
}

// Adapter exposes *riot.Client as the frozen contract.RiotClient. It exists so
// that any consumer that only knows the contract (the aggregate side, a future
// timeline fetcher) can be handed the same client the crawler uses, without
// that consumer importing internal/riot's option surface.
type Adapter struct {
	Client *riot.Client
}

// NewAdapter wraps a client for use through contract.RiotClient.
func NewAdapter(c *riot.Client) *Adapter { return &Adapter{Client: c} }

// Match implements contract.RiotClient.
func (a *Adapter) Match(ctx context.Context, matchID string) (riot.MatchDTO, error) {
	return a.Client.Match(ctx, matchID)
}

// MatchIDsByPUUID implements contract.RiotClient.
func (a *Adapter) MatchIDsByPUUID(ctx context.Context, q contract.MatchListQuery) ([]string, error) {
	return a.Client.MatchIDs(ctx, matchListQuery(q))
}

// Key forwards the key surface so that a worker wired with the adapter still
// idles when no key is configured instead of claiming rows it cannot fetch.
func (a *Adapter) Key() (string, bool) { return a.Client.Key() }

// LeagueEntries implements contract.RiotClient.
func (a *Adapter) LeagueEntries(ctx context.Context, q contract.LeagueQuery) ([]riot.LeagueEntryDTO, error) {
	return a.Client.LeagueEntries(ctx, riot.LeagueQuery{
		Queue:    q.Queue,
		Tier:     strings.ToUpper(q.Tier),
		Division: strings.ToUpper(q.Division),
		Page:     q.Page,
	})
}

var _ contract.RiotClient = (*Adapter)(nil)

func matchListQuery(q contract.MatchListQuery) riot.MatchListQuery {
	return riot.MatchListQuery{
		PUUID:     q.PUUID,
		Count:     q.Count,
		Start:     q.Start,
		StartTime: q.StartTime,
		EndTime:   q.EndTime,
		Queue:     q.Queue,
	}
}

// MatchMetaFromDTO is the provenance the archive and the control plane share.
// Everything here is known before the payload is parsed, which is what lets the
// archive be written first: a crash between the archive write and the database
// write leaves a payload with no row, and the next pass re-fetches it.
func MatchMetaFromDTO(dto riot.MatchDTO, matchID, region string, fetchedAt time.Time) contract.MatchMeta {
	gameVersion := dto.Info.GameVersion
	return contract.MatchMeta{
		MatchID:        matchID,
		Region:         region,
		QueueID:        dto.Info.QueueID,
		Patch:          PatchFromGameVersion(gameVersion),
		GameVersion:    gameVersion,
		GameCreation:   time.UnixMilli(dto.Info.GameCreation).UTC(),
		GameDurationS:  int(dto.Info.GameDuration),
		PayloadVersion: raw.MatchPayloadVersion,
		FetchedAt:      fetchedAt.UTC(),
	}
}

// MatchRecordFromMeta is the pre-parse half of a `matches` row. Status is
// "fetched" because the crawl's job ends at retention: parse state belongs to
// the build pipeline, which moves the row to "parsed" once the transform has
// read the payload.
func MatchRecordFromMeta(meta contract.MatchMeta, rawURI string) contract.MatchRecord {
	return contract.MatchRecord{
		MatchID:        meta.MatchID,
		Region:         meta.Region,
		QueueID:        meta.QueueID,
		Patch:          meta.Patch,
		GameVersion:    meta.GameVersion,
		GameCreation:   meta.GameCreation,
		GameDurationS:  meta.GameDurationS,
		PayloadVersion: meta.PayloadVersion,
		RawURI:         rawURI,
		Status:         contract.MatchFetched,
		FetchedAt:      meta.FetchedAt,
	}
}

// PatchFromGameVersion turns "14.23.1.1234" into "14.23". The patch is what
// the published statistics are sliced by, and a major.minor patch is the unit
// players recognise; the build number would fragment every slice.
func PatchFromGameVersion(gameVersion string) string {
	// A single-component version carries no minor patch, and returning a
	// best-effort label would attribute a game to a patch it may not belong
	// to. The build side takes the same view, so both agree on "".
	parts := strings.SplitN(strings.TrimSpace(gameVersion), ".", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

// ParticipantPUUIDs returns the ten puuids of a match, preferring the
// participant list over metadata. Both are in the payload; metadata is the
// cheap path, and the participant list is the fallback for payloads where the
// metadata block was trimmed.
func ParticipantPUUIDs(dto riot.MatchDTO) []string {
	seen := make(map[string]struct{}, len(dto.Metadata.Participants)+len(dto.Info.Participants))
	out := make([]string, 0, 10)
	for _, puuid := range dto.Metadata.Participants {
		// A blank puuid would become a permanent frontier row, so a payload
		// with a hole in it is filtered here rather than written to the walk.
		if strings.TrimSpace(puuid) == "" {
			continue
		}
		if _, ok := seen[puuid]; ok {
			continue
		}
		seen[puuid] = struct{}{}
		out = append(out, puuid)
	}
	for _, p := range dto.Info.Participants {
		if strings.TrimSpace(p.PUUID) == "" {
			continue
		}
		if _, ok := seen[p.PUUID]; ok {
			continue
		}
		seen[p.PUUID] = struct{}{}
		out = append(out, p.PUUID)
	}
	return out
}
