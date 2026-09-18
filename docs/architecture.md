# Architecture

The shape of the system, and why it has this shape. Decisions with a rejected
alternative behind them live in `docs/decisions/`; this page is the map.

## Data flow

```
        Riot API (EUW)                  Data Dragon / Community Dragon
             |                                        |
   adaptive token-bucket limiter,                     |  static sync job
   gzip, retry, Retry-After, backoff                  v
             |                              site asset pipeline
             v                              (champions, items, runes, spells)
   +---------------------+                            |
   |  lolstats-ingest    |  long-running worker       |
   |  (Deployment)       |  consumes fetch_queue      |
   +---------------------+                            |
      |             |                                 |
      |             +--> raw archive (zstd Parquet, immutable, PVC)
      |                  raw/riot/match-v5/dt=.../            [PRIMARY DATA]
      |                  raw/riot/match-v5-timeline/dt=.../   [PRIMARY DATA]
      v
   +-------------------------------------------+
   |  PostgreSQL - control plane only          |
   |  matches, fetch_queue, crawl_frontier,    |
   |  crawl_seeds, build_runs, source_toggles  |
   +-------------------------------------------+
             ^                    |
             |                    |  reads raw Parquet plus control metadata
             |                    v
   +---------------------+     +----------------------+
   |  lolstats-ingest    |     |  lolstats-aggregate  |
   |  CronJobs:          |     |  (nightly CronJob)   |
   |   - discover-seeds  |     |  DuckDB build step   |
   |   - backfill        |     +----------------------+
   |   - maintain        |                |
   +---------------------+                v
                                agg/v1/** (JSON artifacts, immutable per build)
                                manifest.json (patches, n per cell, suppressed cells)
                                           |
                                           v
                                           |
                                           v
                                   agg/v1/** is read from the
                                   volume by whatever needs it
                                 (no server in this repository)
```

The two properties worth noticing:

**Nothing leaves the origin at request time.** Every number and every sentence is
derived inside the cluster from the artifacts on the volume and from nothing else.
There is no request path to a database, no third-party call, no analytics and no
font or CDN fetch from the browser, so there is no query to make slow, no
connection pool to exhaust and no upstream that can rate-limit or observe a
reader. Since 2026-09-18 there is also no renderer in this repository: the
artifacts are the deliverable, and whoever displays them reads files.

**There are exactly two stores, and they hold different kinds of thing.**
Postgres holds control-plane state whose write volume is bounded by pipeline
events - one row per match, per queue item, per PUUID, per build. The raw archive
holds the payloads themselves and is the only copy that cannot be re-fetched,
because Riot retains match history for two years and timelines for one. Per-
participant feature rows live in Parquet, not Postgres, so the aggregate shape
can change without a migration. The published `agg/v1` tree is a third thing
again: derived, disposable and rebuildable from the archive, which is why losing
it is an outage rather than a data loss.

## Components

| Component | Kind | Responsibility | Trigger |
| --- | --- | --- | --- |
| `lolstats-ingest worker` | Deployment | Claim match-fetch jobs, call Riot, write raw archive, dedupe into Postgres, update frontier | continuous |
| `lolstats-ingest discover-seeds` | CronJob | Pull LEAGUE-V4 ladder entries, upsert PUUIDs into the frontier with the seed tier recorded | daily |
| `lolstats-ingest backfill` | CronJob, parameterised | Re-run fetch jobs over a bounded key range for repair or gap-filling | manual |
| `lolstats-ingest backfill-timelines` | CronJob | Enqueue timeline fetches for the bounded sample that is already archived, in a separate `kind` of `fetch_queue` row | weekly |
| `lolstats-ingest maintain` | CronJob | Frontier pruning, raw-archive compaction, key-age check, source-toggle review dates | daily |
| `lolstats-aggregate build` | CronJob | DuckDB reads the raw archive, computes cells, suppresses thin ones, writes `agg/v1/**` and flips the manifest | nightly |
| `lolstats-aggregate verify` | CronJob | Validate published artifacts against the schema and the gate rules; alert on staleness | after build |
| `lolstats-aggregate features` | one-off Job | DuckDB reads **both** raw archives and writes the six-table `timeline-v1` feature dataset plus its schema, README and manifest. Never runs from the nightly job, so a bad timeline extract cannot fail the tier list | manual |
| `lolstats-web` | Deployment, **retired 2026-09-18** | Rendered every route from `agg/v1` at request time, served `/agg/**` unchanged, answered `/healthz` and `/metrics`, and failed visibly (503 + error page) when the artifact tree was missing. Deleted with its tier; `deploy/base/web/service.yaml` is now unbaked | - |
| shared Caddy (namespace `web`) | Deployment | Terminate TLS and reverse-proxy to the `lolstats-web` Service. It is the cluster's, not this project's, and its upstream no longer resolves | continuous |

## What serves the tree

**Nothing, in this repository.** The Go tier that did - one Deployment,
`cmd/lolstats-web`, `internal/webtier` - was removed on 2026-09-18
(`docs/decisions/ADR-011-retire-the-web-tier.md`). What is left is the artifact
tree and the rules a reader of it has to honour; `docs/contracts.md` section 4
is the authority for both.

| Property | Behaviour |
| --- | --- |
| URL prefix | The aggregate root is published at `/agg`, on the same origin as whatever reads it, so a page and its data cannot become two origins that drift apart |
| Cache policy | `public, max-age=60` with an `ETag` on `manifest.json` and the artifacts under `p/`; `public, max-age=3600` on `/agg/v1/static/<ddragon_version>/**.json`, which is immutable upstream data. The static prefix is **conditional**: served at that policy when published, `404` with `Cache-Control: no-store` when it is not |
| `agg/v1` absent | An error, never a partial success. A page that cannot be rendered correctly must not be served at all - the retired tier answered 503 with a visible error page |
| Corrupt artifact | Passed through as it is. A reader serves the bytes it was given rather than inventing a state |
| Repeated request | `ETag` + `304`, and `private, max-age=60, stale-while-revalidate=300` on HTML so a stale copy is revalidated rather than assumed correct |

Nothing asserts any of that now. The harness that did went with the tier, as did
the compliance gate that scanned the pages the running tier served. Those
launch-blocking claims are still requirements and are written in
`docs/compliance.md`, which also records their absence of automated evidence as a
gap.

**No gate is left behind.** A serving harness used to assert each cache
property against the deployed Service, and a compliance gate used to scan the
HTML the running tier served - the only corpus a scanner could read, because the
filter form exists in the response and not in a file. Both went with the tier on
2026-09-18. Nothing here is asserted by automation any more; `docs/compliance.md`
is where the surviving obligations live and where that gap is recorded.

## Boundaries

**The Riot client is in-house.** The rate limiter is the component most likely
to get the project banned and it has to be adaptive, reading
`X-App-Rate-Limit` and `X-Method-Rate-Limit` from every response and honouring
`Retry-After` on 429. No maintained Go library does that. See ADR-001.

**The aggregate build never calls Riot.** Its only input is the raw archive plus
control metadata. That is what makes a rebuild reproducible from a pinned image
digest, and what makes "Riot changed the payload" an additive transform change
rather than an emergency.

**No consumer computes a statistic.** Every number in the tree arrives
pre-computed, with its `n`. Suppression happens in the aggregate build, so a thin
cell cannot reach a reader even by accident.

**The binaries never write to their own image.** Raw archive, aggregate output
and the site volume are mounts. The runtime image is distroless and nonroot.

## Failure behaviour

| Failure | Behaviour |
| --- | --- |
| Riot returns 429 | Honour `Retry-After`, reduce the token-bucket rate, retry; the queue row is not lost |
| Riot returns 403 | Circuit-break, alert on key age; a rejected key is a stop condition, not a retry loop |
| Crash between archive write and dedupe | Nothing is lost: the archive is written first and the queue row is idempotent |
| Aggregate build fails | The previous artifacts stay live and the manifest is not flipped. Publishing nothing beats publishing garbage |
| Aggregate build is thin | `cells_suppressed` is surfaced in the manifest; thin is visible before it is wrong |
| `agg/v1` is missing or unreadable | An error, never a partial success: a consumer that cannot read the tree must not present a page as if it could. The retired tier answered **503 with a visible error page** and `Cache-Control: no-store` rather than a truncated 200 |
| The manifest is present but corrupt | The bytes are passed through unchanged rather than a state being invented, so a corrupt manifest is visible as a corrupt manifest |
| A reader repeats a request | `ETag` + `304`, and `private, max-age=60, stale-while-revalidate=300` on HTML so a stale copy is revalidated rather than assumed correct |
| Postgres lost | Rebuildable from the archive. Crawl state is lost, which costs time and not data |

## What is deliberately not here

No query-serving database, no request-time aggregation, no CDN by default, no
message broker, no workflow engine, and no tracing. The Kustomize output is in
`deploy/` in this repository and is the single source of truth for the cluster
(`deploy/README.md`); the ArgoCD `Application` that reconciles it lives in the
`homecluster` repository (ADR-004).
