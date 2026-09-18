# ADR-012: Ingest match timelines and publish a per-minute feature dataset

- Status: accepted
- Date: 2026-09-19
- Decision: owner (first contract change since ADR-001; enables the timeline work)

## Context

`contract.RiotClient` is three methods wide and `docs/contracts.md` states
outright that there is no `Timeline` method. `internal/aggregate/rawarchive.go`
hardcodes `raw/match-v5` as the only input the nightly build reads, and
`aggmodel` carries a `skill_orders` field that is structurally always empty
because no timeline has ever been fetched.

The consequence is a ceiling on every question the data layer can answer. Win
rate, pick rate, ban rate, matchups and builds are all end-of-game facts: they
describe a game that has already finished. The pipeline can say *what* happened
and never *when* it happened, so it cannot say whether CS at minute 5 against
the lane opponent predicts the outcome, whether a death at minute 10 does, or
whether the first dragon does.

Riot offers that data at
`GET /lol/match/v5/matches/{matchId}/timeline`, and the retention is asymmetric:
**match history is kept for two years, timelines for one**. The window in which
timelines can be fetched at all is therefore closing, and a match older than a
year is permanently out of reach no matter what is decided later.

Three constraints shape the decision:

- **Timelines cost a second request per match.** At a development key's limits
  (20 req/1 s and 100 req/2 min per method per region) the whole archive is not
  reachable, so the first pass must be a bounded, reproducible sample.
- **`agg/v1` is a frozen reader contract with `min_cell_n: 100`.** A
  minute-level dataset cannot be suppressed at that floor - a lane matchup is
  two participants, not a hundred - so it must not be folded into that contract.
- **A timeline payload is roughly ten times a match summary.** Every report of
  ~100 KB per summary and ~1.1 MB per timeline is third-party measurement rather
  than Riot documentation, which is why the first real run measures and records
  its own figure.

## Decision

**Match timelines are ingested as a second raw payload, and a separate Parquet
feature dataset is derived from them.**

- `contract.RiotClient` gains `Timeline(ctx, matchID)`. `contract.RawWriter`
  gains `WriteTimeline(ctx, timeline, meta)`. Both are the change
  `docs/contracts.md` requires an ADR for, and `docs/contracts.md` §2 is updated
  in the same change, including the deletion of the rule that said no such
  method exists.
- **`riot.TimelineDTO` is scoped to what the code reads**: the envelope
  (`metadata.matchId`, `metadata.participants`), `info.frameInterval` and
  `info.frames[]` with typed `participantFrames` and `timestamp`. The `events[]`
  array stays `[]json.RawMessage`. The event set is wide, polymorphic and
  under-documented, and the aggregation reads the **verbatim payload** with
  `json_extract`, exactly as it already does for summary events. Modelling
  twenty event shapes as Go structs would buy nothing the SQL does not have and
  would freeze guesses.
- **The timeline archive is a second directory**, `raw/riot/match-v5-timeline/`,
  partitioned by fetch date like every other source. It is append-only and has
  no key, so a re-fetch appends a second record and the aggregation
  de-duplicates by match id at the single point where the archive is read.
- **The dataset is published under its own root** as
  `timeline-v1/{match_index,participant_minutes,events,lane_matchups,participant_early,match_objectives}/*.parquet`
  plus `schema.json`, `README.md` and `manifest.json`. It is **not** part of the
  `agg/v1` reader contract; its manifest is a build receipt and promises no
  downstream schema version. The nightly build keeps reading `match-v5` alone,
  so a bad timeline extract cannot fail the tier list.
- **`fetch_queue` gains a `kind` column** (`match`, `timeline`) with a
  `UNIQUE (match_id, kind)`. Timeline fetches then inherit the existing claim,
  retry, backoff, dead-letter and revival machinery instead of a second job
  state machine. The default is `'match'`, so every existing row and every
  existing call site keeps its meaning.
- **No new binaries.** The image is pinned at two (ADR-011). Timeline work lands
  as `lolstats-ingest backfill-timelines` and a new `lolstats-aggregate features`
  verb.
- **The sample-selection rule is frozen here**, because the validity of every
  published number depends on it: queue 420, the configured region, matches the
  control plane already holds, `gameCreation` within the last 330 days, games
  under ten minutes excluded unless explicitly included with the excluded count
  recorded, and **selection ordered by a hash of `match_id` rather than by
  recency**. Newest-N would be a sample of the current patch and a handful of
  champions, which would silently answer meta questions about two weeks of one
  patch. A hash order over the whole eligible year is unbiased and reproducible.
- **`participantId` is the only participant identity in the dataset.** No
  `puuid` appears in any published table, which is what keeps the dataset a set
  of aggregate statistics rather than a player-identifying record.

## Consequences

- The one shared surface with the nightly build is
  `internal/aggregate/rawarchive.go`, where the single hardcoded source
  directory becomes a walk over named sources. The existing aggregate test suite
  is the regression evidence that the tier list did not move.
- `docs/contracts.md` §2 and §4 change, and the `skill_orders` note in §1 stops
  being true once timelines exist. `aggmodel.SkillOrder` is *not* wired into
  `agg/v1` by this ADR: the timeline-derived skill sequence is published in the
  dataset instead, and promoting it into the frozen contract would be a second
  contract decision.
- A timeline `404` means Riot has aged the match out, not "try again". The
  client classifies it as terminal through the existing `riot.IsNotFound` path,
  so an aged-out fetch is dead-lettered rather than retried against the
  rate-limit budget.
- Non-uniform frame spacing (observed ~60.5-71.3 s per frame since around patch
  16.1) means **minute N is derived from `timestamp`, never from the frame
  index**. This is not a detail: it is the difference between a correct CS@10
  and a CS@10 taken ten per cent early. Every table carries both
  `frame_timestamp_ms` and the derived `minute` so the irregularity is visible
  in the data.
- Known Riot defects are encoded rather than discovered later: aborted games
  (`participantFrames: null`, `frameInterval: 0`), missing frames under ten
  minutes, duplicate `SKILL_LEVEL_UP` since 15.17, objective attribution bugs,
  and `ITEM_PURCHASED` rows with `participantId: 0`. `match_index` records an
  `exclusion_reason` per match so a consumer can reproduce or override the
  default filter set. There are **no per-camp monster events**, so jungle
  pathing can only be inferred from `jungleMinionsKilled` deltas plus
  `position`; the README says so rather than implying otherwise.
- `CHAMPION-MASTERY-V4` is the natural next covariate and is deliberately not
  implemented here. It has the same shape - a `kind` on the queue, a verbatim
  archive directory, a table keyed by match and participant - and it is only
  worth measuring once the timeline tables exist to regress against.
- Scaling the sample toward the whole archive is a configuration change, bounded
  by the closing one-year retention. The per-timeline payload size and the pinned
  extraction batch size are **estimates, not measurements**; they are labelled as
  such in `docs/data-sources.md` and in the comment on the batch constant, and
  `docs/runbooks/rebuild-aggregates.md` says how to measure them on the first
  bounded real run. Raising the limit is an informed decision only once that has
  happened.

## Alternatives rejected

- **Extend `agg/v1` with timeline-derived sections.** Rejected: the contract is
  frozen, its floor (`min_cell_n: 100`) cannot apply to two-participant rows, and
  the dataset must stay rebuildable and reshapable without touching a published
  reader contract.
- **A separate `timeline_queue` table.** Rejected: it duplicates the claim,
  retry, backoff, dead-letter, revival and claim-recovery state machine, and the
  two copies would drift. A `kind` column costs one migration.
- **Show raw timeline payloads to the consumer.** Rejected: the archive is never
  published, and the project's written compliance position (`docs/compliance.md`)
  is published aggregates only. The dataset is derived statistics with no player
  identity.
- **Fetch timelines for the whole archive now.** Rejected: it cannot be done
  inside a development key's limits, and it spends a closing budget on a sample
  whose selection rule has not been measured yet.
- **A new `lolstats-timeline` binary.** Rejected: ADR-011 pins the image at two
  binaries, and both new verbs belong to binaries that already exist.
