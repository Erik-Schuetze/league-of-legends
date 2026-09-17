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
                                +---------------------+      +--------------+
                                |  Astro build        | ---> |  Caddy       | ---> users
                                |  (CronJob / CI)     |      |  file_server |
                                +---------------------+      +--------------+
```

The two properties worth noticing:

**Nothing is fetched at request time.** The site is a directory of pre-rendered
HTML and pre-computed JSON. There is no request path to a database, so there is
no query to make slow, no connection pool to exhaust and nothing to overload
under a spike beyond static file serving.

**There are exactly two stores, and they hold different kinds of thing.**
Postgres holds control-plane state whose write volume is bounded by pipeline
events - one row per match, per queue item, per PUUID, per build. The raw archive
holds the payloads themselves and is the only copy that cannot be re-fetched,
because Riot retains match history for two years and timelines for one. Per-
participant feature rows live in Parquet, not Postgres, so the aggregate shape
can change without a migration.

## Components

| Component | Kind | Responsibility | Trigger |
| --- | --- | --- | --- |
| `lolstats-ingest worker` | Deployment | Claim match-fetch jobs, call Riot, write raw archive, dedupe into Postgres, update frontier | continuous |
| `lolstats-ingest discover-seeds` | CronJob | Pull LEAGUE-V4 ladder entries, upsert PUUIDs into the frontier with the seed tier recorded | daily |
| `lolstats-ingest backfill` | CronJob, parameterised | Re-run fetch jobs over a bounded key range for repair or gap-filling | manual |
| `lolstats-ingest maintain` | CronJob | Frontier pruning, raw-archive compaction, key-age check, source-toggle review dates | daily |
| `lolstats-aggregate build` | CronJob | DuckDB reads the raw archive, computes cells, suppresses thin ones, writes `agg/v1/**` and flips the manifest | nightly |
| `lolstats-aggregate verify` | CronJob | Validate published artifacts against the schema and the gate rules; alert on staleness | after build |
| Astro build | CronJob / CI | Pre-render every route from the aggregate tree | after build |
| Caddy | Deployment | Serve the site and `/agg/**` from disk with caching and compression | continuous |

## Boundaries

**The Riot client is in-house.** The rate limiter is the component most likely
to get the project banned and it has to be adaptive, reading
`X-App-Rate-Limit` and `X-Method-Rate-Limit` from every response and honouring
`Retry-After` on 429. No maintained Go library does that. See ADR-001.

**The aggregate build never calls Riot.** Its only input is the raw archive plus
control metadata. That is what makes a rebuild reproducible from a pinned image
digest, and what makes "Riot changed the payload" an additive transform change
rather than an emergency.

**The frontend never computes a statistic.** Every number it renders arrives
pre-computed, with its `n`. Suppression happens in the aggregate build, so a
thin cell cannot reach a page even by accident.

**The binaries never write to their own image.** Raw archive, aggregate output
and the site volume are mounts. The runtime image is distroless and nonroot.

## Failure behaviour

| Failure | Behaviour |
| --- | --- |
| Riot returns 429 | Honour `Retry-After`, reduce the token-bucket rate, retry; the queue row is not lost |
| Riot returns 403 | Circuit-break, alert on key age; a rejected key is a stop condition, not a retry loop |
| Crash between archive write and dedupe | Nothing is lost: the archive is written first and the queue row is idempotent |
| Aggregate build fails | The previous artifacts stay live and the manifest is not flipped. Publishing nothing beats publishing garbage |
| Aggregate build is thin | `cells_suppressed` is surfaced in the manifest and on the page; thin is visible before it is wrong |
| Site build fails | The previous site stays live; Caddy keeps serving the last good directory |
| Postgres lost | Rebuildable from the archive. Crawl state is lost, which costs time and not data |

## What is deliberately not here

No query-serving database, no request-time aggregation, no CDN by default, no
message broker, no workflow engine, no tracing, and no deployment manifests in
the scaffold - the Kustomize output under `deploy/` is an infra agent's, and the
ArgoCD `Application` that reconciles it lives in the `homecluster` repository
(ADR-004).
