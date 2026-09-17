# Contracts

Everything on this page is normative and frozen. Three agents are writing this
project against these interfaces at the same time and cannot see each other's
code. A change to anything here is an interface change and requires an ADR in
`docs/decisions/` before the code changes - not a quiet edit, and not a
"compatible" tweak that turns out to be incompatible.

The Go declarations in section 2 exist verbatim in `internal/contract`, and the
Go structs behind section 1 exist verbatim in `internal/aggmodel`. This page
explains and constrains them; it does not duplicate them as a second source of
truth. Where this page and the code disagree, the code wins and this page is
the bug.

Sections:

1. Aggregate artifact shapes and the route table
2. Go interfaces
3. Frontend component API
4. `agg/v1` filesystem layout
5. CI image contract
6. Ownership map

## 1. Aggregate artifact shapes and the route table

### 1.1 Envelope and cells

Every artifact in `agg/v1/p/...` embeds the envelope below. The Go type is
`aggmodel.Envelope`; the TypeScript type is generated into
`web/src/types/agg.d.ts` by `make types`.

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
| `manifest.json` | `aggmodel.Manifest` | `Manifest` | Envelope-free: `schema`, `generated_at`, `latest`, `partitions[]` |
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

`skill_orders` is present and empty in v1. Timelines are not fetched, so no
acceptably sized sample exists for it yet, and the page hides the section when
the array is empty rather than rendering a table of forty games.

`Partition` in the manifest repeats the envelope fields for its segment plus
`cells_published`, `build_run_id`, `git_sha`, `champions: number[]` and
`matchup_roles: Role[]`. Those last two are the index the frontend uses to build
links without listing the tree: a champion route that is not in `champions` does
not exist and must 404 at build time.

### 1.3 Route table

Every route is pre-rendered at build time. `<root>` is the aggregate root, which
the static server exposes at the URL prefix `/agg`. A page that needs
`<root>/v1/manifest.json` fetches `/agg/v1/manifest.json`.

| Route | Artifacts fetched | JavaScript |
| --- | --- | --- |
| `/` | `<root>/v1/manifest.json`, `<root>/v1/static/<ddragon>/patches.json`, `<root>/v1/p/<latest>/tierlist.json` | none |
| `/tier-list/<role>` | `<root>/v1/manifest.json`, `<root>/v1/static/<ddragon>/champions.json`, `<root>/v1/p/<latest>/<region>/<queue>/<bracket>/tierlist.json` | `TableIsland`, deferred |
| `/patch/<version>/tier-list/<role>` | as above with `<version>` in place of `<latest>` | `TableIsland`, deferred |
| `/champions/<slug>` | `<root>/v1/manifest.json`, `<root>/v1/static/<ddragon>/champions.json`, `<root>/v1/p/<latest>/champions/<champion_id>.json` | `HeatmapIsland` only on the matchup section |
| `/champions/<slug>/<role>` | as above; the role selects which `ChampionRole` is rendered | same |
| `/matchups/<role>` | `<root>/v1/manifest.json`, `<root>/v1/static/<ddragon>/champions.json`, `<root>/v1/p/<latest>/matchups/<role>.json` | `HeatmapIsland`, deferred |
| `/about` | none | none |
| `/legal/terms` | none | none |
| `/legal/privacy` | none | none |
| `/disclaimer` | none | none |

`<region>`, `<queue>` and `<bracket>` in the paths above are not route
parameters. They are read from the manifest's `latest` partition and substituted
into every URL. The site publishes one region and one bracket in v1, and the
route stays free of them so that adding a second region is a new manifest entry
rather than a new route.

`<champion_id>` is looked up from `static/<ddragon>/champions.json` by matching
the route's `<slug>` against `ChampionSlug(champion.key)`. `<slug>` is never
parsed back into a champion name.

No route requires JavaScript to render its primary content. Patch switching is
plain links between pre-generated snapshots.

## 2. Go interfaces

`internal/contract` declares these. Implementations live in `internal/riot`,
`internal/store` and `internal/raw`. The metrics surface is
`obs.MetricsRecorder`, declared next to its Prometheus implementation because
every component already imports `obs`.

```go
package contract

type RiotClient interface {
	Match(ctx context.Context, matchID string) (riot.MatchDTO, error)
	MatchIDsByPUUID(ctx context.Context, q MatchListQuery) ([]string, error)
	LeagueEntries(ctx context.Context, q LeagueQuery) ([]riot.LeagueEntryDTO, error)
}

type RawWriter interface {
	WriteMatch(ctx context.Context, match riot.MatchDTO, meta MatchMeta) error
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

- **There is no `Timeline` method on `RiotClient`.** Timelines are a second
  request per match for data only the optional skill-order section uses, and v1
  ships without that section. Adding the method now buys a rate-limit cost and
  no product.
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

`RiotClient` returns `riot.MatchDTO`. The DTO subset is frozen to what Match-V5
summaries carry: `championId`, `teamPosition`, `individualPosition`, `win`,
`item0`..`item6`, `perks.styles`, `summoner1Id`/`summoner2Id`, the match
`teams[].bans`, and the match metadata/goal fields. Nothing else may be added to
the DTO without an ADR, because anything added is a field the aggregator will be
tempted to read and the archive contract will then have to carry.

## 3. Frontend component API

This section freezes file paths, component names and props. Component internals
may be filled in or restyled at any time. Names, paths and props may not change
without an ADR.

`web/src/styles/**` and `web/src/components/**` are owned by the design-system
agent. `web/src/pages/**`, `web/src/layouts/**`, `web/astro.config.mjs`,
`web/package.json` and `web/tsconfig.json` are owned by the frontend-pages agent.
`web/src/types/**` is generated by `make types` and owned by the contract owner;
nobody edits it by hand.

Every component below is `.astro`, takes no slots unless stated, and takes its
inputs only through `Astro.props`. Data types (`Cell`, `TierList`, `Champion`,
`Manifest`, `StaticChampion`, `Role`, `Tier`, `Bracket`, `Window`) come from
`web/src/types/agg.d.ts`.

### `web/src/styles/tokens.css`

A plain CSS file of custom properties, imported once by `BaseLayout.astro`. It
declares exactly these names and no others at `:root`:

```
--bg-color: #efdbbf;  --bg-light: #f1eae0;
--primary-color: #0b162a;  --accent-color: #1b4bc6;  --text-color: #242a2b;
--font-heading; --font-body; --font-mono;
--grid-line: rgba(27, 75, 202, 0.06);  --grid-size: 27px;
--border-width: 2px;  --shadow-offset-sm: 8px;  --shadow-offset-md: 12px;
--radius: 0;
```

No border radius anywhere. The focus ring is `0 0 0 2px var(--accent-color)`.
Components read tokens and never hard-code a colour, so the whole surface can be
re-themed in one file.

### `web/src/layouts/BaseLayout.astro`

```ts
interface Props {
  title: string;
  description: string;
  patch: string;
  canonical?: string;
  generatedAt?: string;   // ISO 8601, rendered by Footer
  active?: string;        // pathname, for Nav's aria-current
  noindex?: boolean;
}
```

Renders `<html>`, imports `tokens.css`, renders `Nav` from `active`, the default
slot, then `Footer` from `patch` and `generatedAt`. It performs no data fetching:
every page passes already-loaded values.

### `web/src/components/Nav.astro`

```ts
interface Props {
  patch: string;
  active?: string;
  roles?: Role[];   // default: all five
}
```

Renders the role links `/tier-list/<slug>` plus `/matchups/<slug>` and the patch
label. Sets `aria-current="page"` where `active` matches.

### `web/src/components/Footer.astro`

```ts
interface Props {
  patch: string;
  generatedAt?: string;
  sourceWindow?: Window;
}
```

Renders the data provenance line and links to `/about`, `/legal/terms`,
`/legal/privacy` and `/disclaimer`. Those four links are the Riot compliance
surface and are not removable by a page.

### `web/src/components/Card.astro`

```ts
interface Props {
  title: string;
  href?: string;
  tone?: 'default' | 'accent';   // default: 'default'
  sampleN?: number;              // renders SampleSizeNotice when set
}
```

Renders a bordered, hard-shadow surface. Uses the default slot as its body.
When `href` is set the whole card is a single link; there is no nested
interactive element inside it.

### `web/src/components/DataTable.astro`

```ts
type CellValue = string | number | null;
type Row = Record<string, CellValue>;

interface Column {
  key: string;
  label: string;
  align?: 'start' | 'end';       // default: 'start'
  sortable?: boolean;            // default: false
  format?: 'text' | 'percent' | 'integer' | 'decimal';
  digits?: number;               // default: 2, used by 'decimal'
}

interface Props {
  columns: Column[];
  rows: Row[];
  caption?: string;
  initialSortKey?: string;
  initialSortDir?: 'asc' | 'desc';   // default: 'desc'
  emptyMessage?: string;             // default: 'No data for this selection.'
  dense?: boolean;                   // default: false
}
```

Server-renders a complete `<table>` in `initialSortKey` order, so the page is
correct with JavaScript disabled. It emits no client script; sortable behaviour
comes from `TableIsland`, which provides its own markup.

### `web/src/components/TierBadge.astro`

```ts
interface Props { tier: Tier; n?: number; }
```

### `web/src/components/StatValue.astro`

```ts
interface Props {
  value: number;
  format?: 'percent' | 'integer' | 'decimal';   // default: 'percent'
  digits?: number;                               // default: 2
  label?: string;
  n?: number;                                    // renders the sample size beside the value
  unavailable?: boolean;                         // renders an em dash and the reason
}
```

`unavailable` exists so a suppressed or absent statistic is shown as explicitly
missing rather than as `0`.

### `web/src/components/SampleSizeNotice.astro`

```ts
interface Props {
  n: number;
  minCellN: number;
  suppressedCells?: number;
  role?: Role;
}
```

Renders the sample size, the threshold it is judged against, and - when
`suppressedCells` is non-zero - a sentence saying how many cells were withheld
for being too thin. Its output is a compliance requirement, not decoration.

### `web/src/components/FilterBar.astro`

```ts
interface FilterOption { value: string; label: string; }

interface FilterSpec {
  name: string;                  // query parameter name
  label: string;
  options: FilterOption[];
  selected?: string;
}

interface Props {
  filters: FilterSpec[];
  action?: string;               // default: current pathname
  method?: 'get' | 'post';       // default: 'get'
  hidden?: Record<string, string>;
}
```

A plain `<form>` of `<select>` elements with a submit button. No JavaScript: a
filter is a navigation, and a navigation is a URL that can be shared.

### `web/src/components/BuildList.astro`

```ts
interface Props {
  title: string;
  builds: Build[];
  kind: 'items' | 'runes' | 'spells';
  limit?: number;                // default: 10
  emptyMessage?: string;
}
```

Renders `Build.label` with the icon row derived from `Build.key`, plus `n` and
`win_rate` for each entry. Entries are already sorted by `n` descending by the
aggregator; the component does not re-sort.

### `web/src/components/TableIsland.astro`

```ts
interface Props {
  tierList: TierList;
  champions: StaticChampion[];
  role?: Role;                   // undefined: every role in one table
  initialSortKey?: string;       // default: 'tier'
  initialSortDir?: 'asc' | 'desc';   // default: 'desc'
}
```

The sortable, filterable tier list. It is the island the `/tier-list/<role>`
routes hydrate with `client:visible`. It must accept its props as one
serializable object, must not read `window` or `document` at module scope, and
must render a complete server-side table that is correct before hydration.

### `web/src/components/HeatmapIsland.astro`

```ts
interface Props {
  matchups: Matchups;
  champions: StaticChampion[];
  role: Role;
  minCellN: number;
}
```

The champion-vs-champion explorer. Used by `/matchups/<role>` and by the matchup
section of the champion page. Same island rules as `TableIsland`: serializable
props, no module-scope DOM access, correct markup before hydration. Cells below
`minCellN` are not present in `matchups.cells` and are rendered as unavailable,
never as zero.

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

The tree is versioned at `v1/`, not at the root, so a breaking reader change
ships as `v2/` alongside it and clients migrate on their own schedule. Nothing
inside `p/` is ever rewritten in place: a build writes its artifacts and only
then flips `manifest.json`, which is the single atomic publish step.

Served paths are this tree with the URL prefix in front:
`/agg/v1/manifest.json`, `/agg/v1/p/16.18/EUW/420/all/tierlist.json`.

The static tree is synced as a whole by version directory and old version
directories are pruned after one release of overlap. The raw archive is
`raw/riot/match-v5/dt=<date>/part-<n>.parquet.zst`, append-only, never rewritten
and never migrated in place.

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
| Base stages | `golang:1.27-alpine` for build, `gcr.io/distroless/static-debian12:nonroot` for runtime |
| Pinning | both stages pinned by digest with a readable tag in front |

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
org.opencontainers.image.base.name    gcr.io/distroless/static-debian12:nonroot
```

`revision`, `version` and `created` are supplied at build time by
`.github/workflows/docker-build.yml`, which passes `VERSION` and `REVISION` from
the metadata action and `CREATED` from the commit timestamp rather than the
build clock - so two builds of one commit carry identical labels. The
`base.name` label is set explicitly because the distroless runtime stage is
digest-pinned and its provenance would otherwise be recorded only as a digest.

Binary names inside the image are `/lolstats-ingest` and `/lolstats-aggregate`,
with `ENTRYPOINT ["/lolstats-ingest"]` and `CMD ["worker"]`.

## 6. Ownership map

| Path | Owner |
| --- | --- |
| `docs/**`, `sql/**`, `internal/aggmodel/**`, `internal/contract/**`, `internal/riot/dto.go`, root scaffolding | contract owner |
| `internal/riot/**` (client), `internal/raw/**`, `internal/store/**`, `cmd/lolstats-ingest/**`, `internal/crawl/**` | ingest agent |
| `cmd/lolstats-aggregate/**`, `internal/aggregator/**`, `internal/parquet/**` | aggregate agent |
| `web/src/styles/**`, `web/src/components/**` | design-system agent |
| `web/src/pages/**`, `web/src/layouts/**`, `web/astro.config.mjs`, `web/package.json`, `web/tsconfig.json` | frontend-pages agent |
| `web/src/types/**` | generated by `make types`; nobody edits it |
| `deploy/**` | infra engineer; not written by the scaffold |

A file that is not listed is owned by whoever needs it first, and adding a file
is additive. Changing a path that is listed above is a contract change.
