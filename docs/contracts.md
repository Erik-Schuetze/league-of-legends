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

`skill_orders` is present and empty in v1. Timelines are not fetched, so no
acceptably sized sample exists for it yet, and the page hides the section when
the array is empty rather than rendering a table of forty games.

`Partition` in the manifest repeats the envelope fields for its segment plus
`cells_published`, `build_run_id`, `git_sha`, `champions: number[]` and
`matchup_roles: Role[]`. Those last two are the index the frontend uses to build
links without listing the tree: a champion route that is not in `champions` does
not exist and must 404 at build time.

### 1.3 Route table

Every route is **rendered at request time** by the Go serving tier
(`lolstats-web`, section 4.4) from the artifacts below - not pre-rendered into a
directory of HTML. `<root>` is the aggregate root on the tier's volume, and the
tier exposes it at the URL prefix `/agg`, so a page that needs
`<root>/v1/manifest.json` reads `/agg/v1/manifest.json` through the same origin
it is served from. There is no CORS exception and no second server.

| Route | Artifacts read | JavaScript |
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
plain links between snapshots, and the sort/filter/paging controls are GET forms
that the tier answers server-side; the islands only add interaction on top of a
page that is already complete.

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
That is a `box-shadow` value: `outline: var(--focus-ring)` is silently dropped by
CSS, so a component draws the ring as `outline: var(--border-width) solid
var(--accent-color)` and only ever uses `--focus-ring` in a `box-shadow`. Inside
a surface painted in `--primary-color` (the masthead, the footer) the ring
recolours to `--accent-on-dark` (`#a6b2d7`, 8.58:1), because the accent is only
2.47:1 there, below the 3:1 WCAG 2.2 SC 1.4.11 asks of a focus indicator. Both
are declared in `global.css`; the thirteen names above are unchanged.
Components read tokens and never hard-code a colour, so the whole surface can be
re-themed in one file. The opt-in utility classes `global.css` adds on top of the
frozen block (`.ds-container`, `.ds-panel`, `.ds-table-scroll`, `.ds-num`,
`.ds-visually-hidden`, `.ds-navbar`, `.ds-on-dark`) are documented in
`docs/design-system.md`; they widen no component's prop surface.

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

### 4.1 The tree

The tree is versioned at `v1/`, not at the root, so a breaking reader change
ships as `v2/` alongside it and clients migrate on their own schedule.

### 4.2 Publishing

Nothing inside `p/` is ever rewritten in place: a build writes its artifacts and
only then flips `manifest.json`, which is the single atomic publish step.
Publishing nothing beats publishing garbage, so a build that fails leaves the
previous tree - and the previous manifest - live.

The static tree is synced as a whole by version directory and old version
directories are pruned after one release of overlap. The raw archive is
`raw/riot/match-v5/dt=<date>/part-<n>.parquet.zst`, append-only, never rewritten
and never migrated in place.

### 4.3 The frozen manifest contract

`manifest.json` is the one file a reader may depend on for the shape of
everything else, so its keys are frozen. Frozen does not mean optional: a
missing key is a contract break, and `scripts/verify-serving.sh` fails when the
served manifest does not carry them.

| Key | Frozen value or meaning |
| --- | --- |
| `schema` | `1`. A change here is a new major version of the tree, not an edit |
| `source` | `riot-match-v5`. The provenance the pages read their labelling from: only this value lets a page describe MATCH-V5, and every other value renders the demo or no-data language |
| `generated_at` | RFC 3339 build time |
| `latest` | The partition every route without an explicit patch reads. A partition is the envelope fields plus `cells_published`, `build_run_id`, `git_sha`, `champions[]` and `matchup_roles[]` |
| `partitions[]` | Every published partition, including `latest`. One entry in v1 |
| `min_cell_n` | `100`. The suppression floor: a cell with `n` below it is not emitted, and the reader cannot reconstruct it |
| `cells_published` | Cells actually published in that partition |
| `suppressed_cells` | Cells withheld by `min_cell_n`. Published on purpose, so a thin patch is visible to the operator and to the page rather than looking like an empty region |

The deployed snapshot as of 2026-09-17 publishes exactly one partition -
`16.18` / `EUW` / `420` / `all` - with `min_cell_n: 100`, **130 cells published
and 511 suppressed**. That is a real tree, and it is why the contract can cite
numbers rather than placeholders: `cells_published` far below the champion-role
cross product is the expected state of a young archive, and it is disclosed
rather than hidden.

### 4.4 How the tree is served

The serving tier exposes the aggregate root at the URL prefix `/agg` on its own
origin: `/agg/v1/manifest.json`,
`/agg/v1/p/16.18/EUW/420/all/tierlist.json`,
`/agg/v1/static/<ddragon_version>/champions.json`. There is no separate
artifact host and no CORS exception, which is why a page and the data it renders
cannot become two origins that drift apart.

Cache policy is part of the contract, because it is what a reader's browser and
every intermediate cache will do with the bytes:

| Path | Response |
| --- | --- |
| `/agg/v1/manifest.json` and the other artifacts under `p/` | `Cache-Control: public, max-age=60`, with an `ETag` |
| `/agg/v1/static/<ddragon_version>/**.json` | `Cache-Control: public, max-age=3600` - immutable upstream data with no reader in it, so it is safe to cache publicly for longer |
| HTML | `Cache-Control: private, max-age=60, stale-while-revalidate=300`, `ETag`, `Vary: Accept-Encoding`; a matching `If-None-Match` is `304` |

Two failure modes are contract, not implementation detail:

- **A missing or unreadable `agg/v1` is a 503 with a visible error page**, never
  a 200 with a truncated body. A page that cannot be rendered correctly must not
  be served at all.
- **A corrupt artifact is passed through as it is.** The tier serves the bytes
  it was given rather than inventing a state; `make verify-serving-local`
  byte-compares the served manifest against a deliberately corrupt fixture for
  exactly this reason.

**Known gap, 2026-09-17.** The deployed snapshot publishes **no `v1/static/`
tree**: `/agg/v1/static/<version>/champions.json` is 404 for both `16.18.1` and
`16.18`, and the pages therefore fall back to the copy of Data Dragon checked
into `web/src/fixtures/v1/static`. The serving tier is correct - it serves what
exists - but the publisher's static sync is not yet writing the projection, so
the fallback is live rather than latent. `scripts/verify-serving.sh` reports
this as a WARN against the deployed tier and as a checked assertion against the
fixture tree, so the gap is visible without pretending the tier is at fault.

## 5. CI image contract

One image contains all three binaries. A deployment or CronJob selects the binary
it needs with `command`, so a new subcommand never needs a new image; the serving
tier is the third (`lolstats-web`, section 4.4), which is why adding it did not
add an image.

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

Binary names inside the image are `/lolstats-ingest`, `/lolstats-aggregate` and
`/lolstats-web` (with the Data Dragon fallback fixtures at
`/web/src/fixtures`), with `ENTRYPOINT ["/lolstats-ingest"]` and
`CMD ["worker"]`.

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

Two more gates run in the same job, both added because they were passing for the
wrong reason:

- **Render parity must not skip.** `make test-parity` builds the reference tree
  (`make web-dist`) and then requires `TestRenderParity`,
  `TestInteractiveRenderParity` and `TestFeedParity` to report `PASS`. While the
  reference tree was built *after* the test step, all three called `t.Skip` and
  gated nothing; a `SKIP` is a failure here, and `make test-parity
  WEB_DIST_SKIP=1` is the negative control that proves it.
- **The serving contract and the compliance gate.** `make verify-serving-local`
  starts the tier over the checked-in fixture tree and again over a deliberately
  corrupt aggregate root, and `make compliance` plus `make
  compliance-negative-control` run the launch-blocking compliance gate and its
  negative controls. The gate's scans hand a NUL-delimited list of paths to
  `grep`, whose empty-list behaviour differs between GNU (the CI runner) and BSD
  (macOS), so check 12 asserts that both an empty list and a real list behave and
  `make compliance-gnu` re-runs the gate in a GNU userland locally; the reason is
  recorded in `docs/compliance.md`.

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
