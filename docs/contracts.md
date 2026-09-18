# Contracts

Everything on this page is normative and frozen. It fixes the shapes two
components written separately have to agree on, so a change to anything here is
an interface change and requires an ADR in `docs/decisions/` before the code
changes - not a quiet edit, and not a "compatible" tweak that turns out to be
incompatible.

The Go declarations in section 2 exist verbatim in `internal/contract`, and the
Go structs behind section 1 exist verbatim in `internal/aggmodel`. This page
explains and constrains them; it does not duplicate them as a second source of
truth. Where this page and the code disagree, the code wins and this page is
the bug.

Sections:

1. Aggregate artifact shapes
2. Go interfaces
4. `agg/v1` filesystem layout
5. CI image contract

Sections 3 (frontend component API) and 6 (ownership map) were removed on
2026-09-18: the Astro tree they described and the concurrent-agent arrangement
they assigned paths for are both gone. Section 1.3, the route table, was removed
the same day when the Go tier that rendered those routes was retired; the
artifacts it read are still the contract, and only the page-shaped view of them
went. The git history carries all three sections if the reasoning is ever wanted.

## 1. Aggregate artifact shapes

### 1.1 Envelope and cells

Every artifact in `agg/v1/p/...` embeds the envelope below. The Go type is
`aggmodel.Envelope`; the TypeScript type is generated into
`schema/agg.d.ts` by `make types`.

```json
{
  "schema": 1,
  "patch": "16.18",
  "region": "EUW",
  "queue": 420,
  "bracket": "all",
  "generated_at": "2026-09-17T02:14:00Z",
  "source_window": { "from": "2026-09-03", "to": "2026-09-17" },
  "min_cell_n": 100,
  "suppressed_cells": 12
}
```

A cell is one (champion, role) pair. `n` is mandatory on every cell: a win rate
without its sample size is not published.

```json
{
  "champion_id": 64,
  "role": "JUNGLE",
  "n": 8421,
  "wins": 4310,
  "win_rate": 0.5118,
  "pick_rate": 0.0871,
  "ban_rate": 0.1210,
  "tier": "A",
  "ci95_half_width": 0.0107
}
```

Two rules are contract, not presentation:

1. **`n` is mandatory on every cell.** A producer that cannot compute `n` must
   not emit the cell.
2. **Cells below `min_cell_n` are suppressed and counted, never emitted.** The
   count goes in `suppressed_cells` on the same envelope and in the manifest, so
   a thin patch is visible to the operator before it is visible to a user.

Enumerations, frozen:

| Name | Values |
| --- | --- |
| `Role` | `TOP`, `JUNGLE`, `MID`, `BOTTOM`, `SUPPORT` |
| `Role` url slug | `top`, `jungle`, `mid`, `bottom`, `support` |
| `Tier` | `S+`, `S`, `A`, `B`, `C`, `D` |
| `Bracket` | `all`, `emerald_plus`, `platinum_plus`, `diamond_plus`, `master_plus` |

Riot's `MIDDLE` and `UTILITY` team positions are normalised to `MID` and
`SUPPORT` in exactly one place, `aggmodel.RoleFromRiotPosition`. No other
producer normalises roles. `bracket` is `all` in v1; the segment exists from
day one so that adding brackets later is additive instead of a URL break.

### 1.2 Artifact files

| Artifact | Go type | TS type | Contents |
| --- | --- | --- | --- |
| `tierlist.json` | `aggmodel.TierList` | `TierList` | Envelope plus `cells: Cell[]`, one row per (champion, role) |
| `champions/<id>.json` | `aggmodel.Champion` | `Champion` | Envelope plus `champion_id`, `champion_slug`, `roles: ChampionRole[]` |
| `matchups/<role>.json` | `aggmodel.Matchups` | `Matchups` | Envelope plus `role`, `champions: number[]`, `cells: MatchupCell[]` |
| `manifest.json` | `aggmodel.Manifest` | `Manifest` | The publish pointer and the frozen contract: `schema`, `source`, `generated_at`, `latest`, `partitions[]`. See section 4.3 |
| `static/<ddragon>/*.json` | `aggmodel.Static*` | `Static*` | Data Dragon projection, see section 4 |

`ChampionRole` carries the whole champion page for one role. The page reads one
file, not six:

```ts
interface ChampionRole {
  role: Role;
  stats: Cell;
  items: Build[];
  runes: Build[];
  spells: Build[];
  skill_orders: SkillOrder[];
}
interface Build { kind: string; key: number[]; label: string; n: number; wins: number; win_rate: number; }
interface SkillOrder { order: string; n: number; wins: number; win_rate: number; }
```

`skill_orders` is present and empty in v1. `agg/v1` does not read the timeline
archive, so no acceptably sized sample exists for it here; the skill orders for
matches that have timelines are published in the separate `timeline-v1` dataset
(`docs/aggregation.md`), not in this contract. The page hides the section when
the array is empty rather than rendering a table of forty games.

`Partition` in the manifest repeats the envelope fields for its segment plus
`cells_published`, `build_run_id`, `git_sha`, `champions: number[]` and
`matchup_roles: Role[]`. Those last two are the index a reader uses to build
links without listing the tree: a champion that is not in `champions` has no
published artifact, and a reader must treat it as absent rather than guess.

## 2. Go interfaces

`internal/contract` declares these. Implementations live in `internal/riot`,
`internal/store` and `internal/raw`. The metrics surface is
`obs.MetricsRecorder`, declared next to its Prometheus implementation because
every component already imports `obs`.

```go
package contract

type RiotClient interface {
	Match(ctx context.Context, matchID string) (riot.MatchDTO, error)
	Timeline(ctx context.Context, matchID string) (riot.TimelineDTO, error)
	MatchIDsByPUUID(ctx context.Context, q MatchListQuery) ([]string, error)
	LeagueEntries(ctx context.Context, q LeagueQuery) ([]riot.LeagueEntryDTO, error)
}

type RawWriter interface {
	WriteMatch(ctx context.Context, match riot.MatchDTO, meta MatchMeta) error
	WriteTimeline(ctx context.Context, timeline riot.TimelineDTO, meta MatchMeta) error
	WriteLeagueEntries(ctx context.Context, entries []riot.LeagueEntryDTO, meta LeagueMeta) error
	Flush(ctx context.Context) error
}

type Store interface {
	// matches
	UpsertMatch(ctx context.Context, rec MatchRecord) (inserted bool, err error)
	MarkMatchParsed(ctx context.Context, matchID string, parsedAt time.Time) error
	MarkMatchFailed(ctx context.Context, matchID string, cause string) error

	// fetch_queue
	EnqueueMatches(ctx context.Context, items []QueueItem) (enqueued int, err error)
	ClaimJobs(ctx context.Context, limit int, now time.Time) ([]QueueItem, error)
	CompleteJob(ctx context.Context, id int64) error
	RetryJob(ctx context.Context, id int64, notBefore time.Time, cause string) error
	DeadLetterJob(ctx context.Context, id int64, cause string) error

	// crawl_frontier
	UpsertFrontier(ctx context.Context, entries []FrontierEntry) (added int, err error)
	ClaimFrontier(ctx context.Context, limit int, now time.Time) ([]FrontierEntry, error)
	MarkFrontierFetched(ctx context.Context, puuid string, at time.Time, empty bool) error
	MarkFrontierDead(ctx context.Context, puuid string, cause string) error
	PruneFrontier(ctx context.Context, before time.Time, maxConsecutiveEmpty int) (int, error)
	FrontierSize(ctx context.Context) (int, error)

	// crawl_seeds
	StartSeedRun(ctx context.Context, run SeedRun) (int64, error)
	FinishSeedRun(ctx context.Context, id int64, finishedAt time.Time, entriesFound int) error

	// build_runs
	StartBuildRun(ctx context.Context, run BuildRun) (int64, error)
	FinishBuildRun(ctx context.Context, id int64, result BuildResult) error

	// source_toggles
	SetSourceToggle(ctx context.Context, toggle SourceToggle) error
	SourceToggles(ctx context.Context) ([]SourceToggle, error)

	Ping(ctx context.Context) error
}
```

From `internal/obs`:

```go
package obs

type MetricsRecorder interface {
	ObserveRiotRequest(method string, status int, seconds float64)
	IncRiotRetry(method, reason string)
	SetRiotKeyAge(seconds float64)
	AddQueueClaimed(n int)
	AddMatchesPersisted(n int)
	AddRawBytesWritten(n int64)
	SetFrontierSize(n int)
	SetPipelineStaleness(stage string, seconds float64)
	ObserveBuildDuration(seconds float64)
	AddCellsPublished(n int)
	AddCellsSuppressed(n int)
	IncBuildFailure(reason string)
}
```

Rules that the signatures do not express:

- **`RiotClient` has a `Timeline` method.** Timelines are a second ingested
  payload: they are fetched per match, archived under their own source, and
  built into the separate `timeline-v1` dataset that is not part of this
  contract. The method and `RawWriter.WriteTimeline` are governed by
  `docs/decisions/ADR-014-ingest-match-timelines.md`; the `agg/v1` artifacts stay
  computed from match summaries alone.
- **`ClaimJobs` and `ClaimFrontier` must use `FOR UPDATE SKIP LOCKED`.** The
  crawler is required to be safe to run concurrently with itself. Handing the
  same row to two callers is a bug in the implementation, not in the caller.
- **The archive is written before the database.** `WriteMatch` then
  `UpsertMatch`. A crash between the two loses no data, because `raw_uri` is
  recoverable from the archive and the queue row is idempotent.
- **`Flush` is on the interface deliberately.** A writer that is not flushed by
  the end of a run has written nothing. Leaving it to a deferred `Close` on a
  concrete type makes "the raw archive is empty and nobody noticed" possible.
- **`MatchMeta.PartitionDate()` is the fetch date, not the game date.** A
  late-arriving match lands in the partition of the run that fetched it, which is
  what keeps a partition append-only and complete.
- **A failed build leaves the previous artifacts live.** `BuildResult.Status` is
  `ok`, `failed` or `quarantined`; publishing nothing beats publishing garbage.

`RiotClient.Match` returns `riot.MatchDTO`; `RiotClient.Timeline` returns
`riot.TimelineDTO` and is not part of this contract (ADR-014). The `MatchDTO`
subset is frozen to what Match-V5
summaries carry: `championId`, `teamPosition`, `individualPosition`, `win`,
`item0`..`item6`, `perks.styles`, `summoner1Id`/`summoner2Id`, the match
`teams[].bans`, and the match metadata/goal fields. Nothing else may be added to
the DTO without an ADR, because anything added is a field the aggregator will be
tempted to read and the archive contract will then have to carry.

## 4. `agg/v1` filesystem layout

The aggregate root contains only what is described here. Every path is produced
by a function in `internal/aggmodel/paths.go`; no producer formats these
templates by hand.

```
<agg-root>/
  v1/
    manifest.json
    p/
      <patch>/                  # major.minor, e.g. 16.18
        <region>/               # e.g. EUW
          <queue>/              # numeric queue id, e.g. 420
            <bracket>/          # all in v1
              tierlist.json
              champions/
                <champion_id>.json
              matchups/
                <role_slug>.json
    static/
      <ddragon_version>/        # e.g. 15.18.1
        champions.json
        items.json
        runes.json
        summoner-spells.json
        patches.json
```

### 4.1 The tree

The tree is versioned at `v1/`, not at the root, so a breaking reader change
ships as `v2/` alongside it and clients migrate on their own schedule.

### 4.2 Publishing

Nothing inside `p/` is ever rewritten in place: a build writes its artifacts and
only then flips `manifest.json`, which is the single atomic publish step.
Publishing nothing beats publishing garbage, so a build that fails leaves the
previous tree - and the previous manifest - live.

The static tree is synced as a whole by version directory and old version
directories are pruned after one release of overlap. The raw archives are
`raw/riot/match-v5/dt=<date>/part-<n>.parquet[.zst]` (match summaries) and
`raw/riot/match-v5-timeline/dt=<date>/part-<n>.parquet[.zst]` (match timelines),
each append-only, never rewritten and never migrated in place.

### 4.3 The frozen manifest contract

`manifest.json` is the one file a reader may depend on for the shape of
everything else, so its keys are frozen. Frozen does not mean optional: a
missing key is a contract break, and a reader must fail closed on a manifest that
does not carry them rather than rendering a page with holes in it.

| Key | Frozen value or meaning |
| --- | --- |
| `schema` | `1`. A change here is a new major version of the tree, not an edit |
| `source` | `riot-match-v5`. The provenance the pages read their labelling from: only this value lets a page describe MATCH-V5, and every other value renders the demo or no-data language |
| `generated_at` | RFC 3339 build time |
| `latest` | The partition every route without an explicit patch reads. A partition is the envelope fields plus `min_cell_n`, `cells_published`, `suppressed_cells`, `build_run_id`, `git_sha`, `champions[]` and `matchup_roles[]` - so the suppression numbers are nested here, one set per partition, and are **not** top-level keys |
| `partitions[]` | Every published partition, including `latest`. One entry in v1 |
| `min_cell_n` | `100`. The suppression floor: a cell with `n` below it is not emitted, and the reader cannot reconstruct it |
| `cells_published` | Cells actually published in that partition |
| `suppressed_cells` | Cells withheld by `min_cell_n`. Published on purpose, so a thin patch is visible to the operator and to the page rather than looking like an empty region |

The deployed snapshot publishes exactly one partition - `16.18` / `EUW` / `420` /
`all` - with `min_cell_n: 100` and 173 champion ids in `latest.champions`. Two
readings of it, to make the point that these are readings and not constants:
`build_run_id` 19 / `git_sha` `9526227` / **244 cells published, 522 suppressed**
at 2026-09-17T23:19:48Z, and `build_run_id` 24 / `git_sha` `fbed839` / **248
published, 521 suppressed** at 2026-09-18T02:04Z. They move with every publish -
and what is frozen
is the *presence* of the keys and their meaning. That it is a real tree is why
the contract can cite numbers rather than placeholders: `cells_published` far
below the champion-role cross product is the expected state of a young archive,
and it is disclosed rather than hidden.

### 4.4 Serving the tree (no server)

**Nothing serves this tree.** The Go tier that did was retired on 2026-09-18
(`docs/decisions/ADR-011-retire-the-web-tier.md`), and this section is kept in
trimmed form because two of the things it fixed are still contract:

- **The URL prefix.** The aggregate root is published at the URL prefix `/agg`,
  on the same origin as anything that reads it: `/agg/v1/manifest.json`,
  `/agg/v1/p/16.18/EUW/420/all/tierlist.json`,
  `/agg/v1/static/<ddragon_version>/champions.json`. There is no separate
  artifact host and no CORS exception, so a page and the data it renders cannot
  become two origins that drift apart. A future serving layer must keep the
  prefix rather than inventing a second one.
- **Cache policy.** `public, max-age=60` with an `ETag` on
  `manifest.json` and the artifacts under `p/`, and `public, max-age=3600` on
  `/agg/v1/static/<ddragon_version>/**.json` -- immutable upstream data with no
  reader in it, so it is safe to cache publicly for longer. The static prefix is
  **conditional**: it is served at that policy whenever the published tree
  carries it, and an unpublished path under the reserved prefix answers `404`
  with `Cache-Control: no-store` rather than an invented `200` (see the
  static-projection amendment below).

Two failure modes were contract rather than implementation detail, and remain the
behaviour a reader has to expect:

- **A missing or unreadable `agg/v1` is an error, never a partial success.** A
  page that cannot be rendered correctly must not be served at all; the retired
  tier answered `503` with a visible error page and a corrupt root was required
  to fail rather than to serve a truncated body.
- **A corrupt artifact is passed through as it is.** A reader serves the bytes it
  was given rather than inventing a state, which is why the retired tier
  byte-compared its served manifest against a deliberately corrupt fixture.

Nothing asserts any of this now. The harness that did - a script that started the
tier over the checked-in fixture tree, probed each route's status, headers and
`Content-Length`, re-requested with `If-None-Match` for a `304`, and re-ran over
a corrupt root - went with the tier. There is no automated evidence for section
4.4 today, and `docs/compliance.md` records that as a gap.

**Amendment: the Data Dragon projection is reserved, and not published. Amended
2026-09-17, last reviewed 2026-09-18. Reason: this section froze a prefix the
deployed Service did not serve, and the gate reported the disagreement as a
`WARN` and still exited `0`, so the frozen wording outlived the served reality it
described.**

Measured against the deployed tier at 2026-09-17T23:30:33Z, every Data Dragon URL
this section used to promise answered `404` from the tier's own error page, with
`Cache-Control: no-store`; `16.18.1`, `16.18`, `16.19.1`, the bare
`/agg/v1/static/` and a nonsense version were all probed and all answered the
same way. The rest of the tree was served at its own policy, so what was absent
was one projection, not the artifact and not the tier.

Two properties follow, and both are still contract:

- **The prefix stays reserved, and the version in the path is the Data Dragon
  version, not the game patch.** `internal/aggmodel/paths.go` defines
  `v1/static/<ddragon_version>/{champions,items,runes,summoner-spells,patches}.json`.
  `/agg/v1/static/16.18/champions.json` is therefore a miss by design when the
  game patch is `16.18` and the Data Dragon version is `16.18.1`.
- **No page depended on it, and a future one need not either.** The retired tier
  rendered champion, item, rune and spell metadata from a Data Dragon projection
  embedded in its binary, preferring a published copy when one existed. So the
  pages were complete whether or not the projection was published, and the
  fallback was the server's business rather than the build's.

What the retired harness did about this is the part worth keeping as a design
note, because it is the shape of a check that can actually fail: it asserted
whichever of the two conformant shapes it observed and rejected anything else. A
**published** version had to answer `200` with exactly
`Cache-Control: public, max-age=3600`, a declared `Content-Length` that matched
the bytes delivered, and a JSON body. An **unpublished** version had to answer
`404` **and** `Cache-Control: no-store` on every probed version, with the absence
stated in the check's own output. Everything else failed: a `404` without
`no-store`, a `200` with the wrong policy, a `5xx`, or a version that could not be
derived and probed at all. A published version alongside `404`s for the
patch-version candidates was not a failure, because the path carries the Data
Dragon version and not the game patch, so the candidates routinely disagree - but
each probed version had to be in one of the two conformant shapes.

The failure direction of that check was itself a control, because a check that
only ever passes is the defect this amendment was written against: a stand-in
HTTP origin - python3's file server, no cluster and no network - served the
projection with no `Cache-Control` at all, and again with the projection absent
and the `404` still uncacheable, and the check had to reject both. Anything that
publishes or serves this prefix must pick one of the two states and stay in it,
and publishing the projection remains an open requirement of the publisher rather
than a defect of any consumer. `docs/compliance.md` records that as a gap.

## 5. CI image contract

One image contains both binaries. A deployment or CronJob selects the binary it
needs with `command`, so a new subcommand never needs a new image.

| Item | Value |
| --- | --- |
| Registry and repository | `ghcr.io/erik-schuetze/league-of-legends` |
| Tag on every push to the default branch | `sha-<7-char-short-sha>` |
| Tag on a `v*` git tag | `v<semver>` and `sha-<7-char-short-sha>` |
| Tag on the tip of the default branch | `latest` |
| Platforms | `linux/amd64` only; the cluster is amd64 |
| Base stages | `golang:1.27-alpine` for build, `debian:12-slim` for the DuckDB CLI, `gcr.io/distroless/cc-debian12:nonroot` for runtime |
| Pinning | all three stages pinned by digest with a readable tag in front |

Labels written onto the published image:

```
org.opencontainers.image.title        league-of-legends stats pipeline
org.opencontainers.image.description  Riot API ingestion and DuckDB aggregation
                                      for a self-hosted League of Legends statistics site
org.opencontainers.image.source       https://github.com/Erik-Schuetze/league-of-legends
org.opencontainers.image.url          https://github.com/Erik-Schuetze/league-of-legends
org.opencontainers.image.licenses     MIT
org.opencontainers.image.revision     <git sha>
org.opencontainers.image.version      <semver or sha-<short>>
org.opencontainers.image.created      <RFC 3339 build time>
org.opencontainers.image.base.name    gcr.io/distroless/cc-debian12:nonroot
```

`revision`, `version` and `created` are supplied at build time by
`.github/workflows/docker-build.yml`, which passes `VERSION` and `REVISION` from
the metadata action and `CREATED` from the commit timestamp rather than the
build clock - so two builds of one commit carry identical labels. The
`base.name` label is set explicitly because the distroless runtime stage is
digest-pinned and its provenance would otherwise be recorded only as a digest.
The runtime base is the `cc` variant rather than `static` because the pinned
DuckDB CLI is a glibc binary that needs `libc`, `libstdc++` and `libgcc_s`,
which the `cc` variant carries and the `static` variant does not; that amendment
is recorded in `docs/decisions/ADR-007-pinned-duckdb-cli-engine.md`.

Binary names inside the image are `/lolstats-ingest` and `/lolstats-aggregate`,
with `ENTRYPOINT ["/lolstats-ingest"]` and `CMD ["worker"]`. The third binary,
`/lolstats-web`, and the Data Dragon fallback fixtures at `/web/src/fixtures`
were removed with the serving tier on 2026-09-18
(`docs/decisions/ADR-011-retire-the-web-tier.md`); the image is now smaller by
one binary and one fixture tree, and nothing in the image listens on a socket.

An image is only published by a run in which the DuckDB-dependent build tests
actually executed. The end-to-end analytics tests in
`internal/aggregate/fixture*_test.go` run the real build over the fixture
archive, and they skip themselves when the pinned DuckDB CLI cannot be resolved -
which is right locally and means the `Test` step is green either way. The
`verify` job therefore also runs `make test-build`, which installs the pinned
DuckDB release into `bin/` (sha256-verified against the per-architecture
constants the `duckdb` stage of the `Dockerfile` verifies the copy it ships
against), exports `LOLSTATS_DUCKDB_BIN` at its absolute path, and fails if
`TestBuildAgainstHandComputedFixture` did not report `PASS` or if any test in the
package reported `SKIP`. A skip is a failure in CI, not a pass. The same two
commands - `make duckdb` and then `make test-build` - are how a contributor runs
that gate locally.

Two further gates used to run in the same job, and both are gone:

- **The serving contract and the compliance gate.** A local serving harness
  started the tier over the checked-in fixture tree and again over a corrupt
  aggregate root; `make compliance`, `make compliance-negative-control` and
  `make compliance-gnu` ran the launch-blocking compliance gate, its negative
  controls, and the gate again under a GNU userland locally (the gate's scans
  hand a NUL-delimited path list to `grep`, whose empty-list behaviour differs
  between GNU and BSD). All of it, plus the `if: always()` guard that kept those
  results readable while some other check was red, went with the tier on
  2026-09-18. The launch-blocking claims they enforced are still requirements and
  are now written in `docs/compliance.md`.
- **The render-parity gate,** retired on 2026-09-17 together with its live
  variant and its mutation control. It required the Go tier's rendered bytes to
  equal those of `web/dist` - the Astro build of the tree the tier replaced - and
  the served design layer had, deliberately and by recorded decision, diverged
  from that tree in three ways: the frozen CSS layer inlined into every document
  (14,178 raw / 5,015 gzip bytes), the `--bg-light` -> `--surface` rename, and one
  added nav entry. A byte comparison against a retired tree cannot be a pass/fail
  gate for the tree that superseded it, so the gate was deleted rather than
  mirrored. On 2026-09-18 both renderers and the trees behind them were deleted
  too, so there is nothing left to compare and no design authority to name.

`make duckdb` and `make test-build` are therefore the whole of the image's
build-time evidence now: they are the only thing standing between a green
`verify` job and a published image, and they still fail on a skip.
