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
                                +---------------------+      +---------------+
                                |  lolstats-web       | ---> |  shared Caddy | --> users
                                |  (Go tier, Deploy)  |      |  (ns `web`)   |
                                |  renders every      |      |  TLS, proxy   |
                                |  route from agg/v1  |      +---------------+
                                +---------------------+
```

The two properties worth noticing:

**Nothing leaves the origin at request time.** Every number and every sentence is
derived inside the cluster: the Go tier renders a route from the artifacts on its
volume and from nothing else. There is no request path to a database, no
third-party call, no analytics and no font or CDN fetch from the browser, so there
is no query to make slow, no connection pool to exhaust and no upstream that can
rate-limit or observe a reader.

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
| `lolstats-ingest maintain` | CronJob | Frontier pruning, raw-archive compaction, key-age check, source-toggle review dates | daily |
| `lolstats-aggregate build` | CronJob | DuckDB reads the raw archive, computes cells, suppresses thin ones, writes `agg/v1/**` and flips the manifest | nightly |
| `lolstats-aggregate verify` | CronJob | Validate published artifacts against the schema and the gate rules; alert on staleness | after build |
| `lolstats-web` | Deployment | Render every route from `agg/v1` at request time, serve `/agg/**` unchanged, answer `/healthz` and `/metrics`, and fail visibly (503 + error page) when the artifact tree is missing | continuous |
| shared Caddy (namespace `web`) | Deployment | Terminate TLS and reverse-proxy to `lolstats-web`; it is the cluster's, not this project's | continuous |

## The serving tier

`lolstats-web` is one Go binary and one Deployment (`cmd/lolstats-web`,
`internal/webtier`). It is not a file server with a router bolted on: it renders
each route from the published `agg/v1` artifacts that are mounted on the pod's
volume, which is what makes the site and `/agg/**` one origin with no CORS
exception and no route of its own in the shared proxy.

| Surface | Behaviour |
| --- | --- |
| `/healthz` | 200 `ok`, `Cache-Control: no-store`; this is the readiness and liveness probe |
| `/metrics` | Prometheus text, `lolstats_`-prefixed, `Cache-Control: no-store` |
| HTML routes | `Cache-Control: private, max-age=60, stale-while-revalidate=300`, a quoted `ETag`, `Vary: Accept-Encoding`; a matching `If-None-Match` is answered `304` with no body, a stale validator is answered with the byte-identical 200 |
| `/agg/v1/static/**` | Data Dragon JSON, reserved for the static sync. **Conditional, amended 2026-09-17**: *published* - `Cache-Control: public, max-age=3600`, safe to cache publicly because it is immutable upstream data with no reader in it; *unpublished* (today's state) - `404` with `Cache-Control: no-store`, and the pages are unaffected because the tier renders from the Data Dragon projection embedded in the binary (`internal/webtier/data.go`). `docs/contracts.md` section 4 is the authority; gap 8 of `docs/compliance.md` records why the contract was amended rather than the tree published |
| `/agg/v1/manifest.json` | `Cache-Control: public, max-age=60`; the 60s matches the nightly build's directory-rename publish, so a stale entry cannot outlive one publish cycle |
| `agg/v1` absent | 503 with a **visible** error page (`data-fault="no-snapshot"`), `no-store` - a page that cannot be rendered correctly is never served as a 200 |

`scripts/verify-serving.sh` (`make verify-serving` against the cluster through
`kubectl -n lolstats port-forward svc/lolstats-go-web 18099:80`, or
`make verify-serving-local` against a tier started over the checked-in fixture
tree) asserts every row of that table, and asserts it against the deployed
Service rather than against the source: the gate grew out of a static-site
script whose file paths and `Cache-Status` expectations no longer described
anything the tier does. The local variant starts the binary a third time over a
copy of the fixture tree with `v1/static` removed, so both states of the
conditional row above are executed rather than described, and a second time over
a deliberately corrupt aggregate root, because "503 rather than a truncated 200"
is the kind of property that only a live probe can establish.

No row is a warning. A row that the deployed tier is in neither state of - a
`404` whose miss a cache may keep, a `200` at the wrong policy, a `5xx` - fails
the gate, and `make serving-static-control` is the standing proof of that failure
direction: it stands in its own origin serving the projection with no
`Cache-Control`, and again with the projection absent and the `404` still
uncacheable, and requires the gate to reject both.

It also asserts the property the tier exists for: **the page works with
JavaScript disabled.** The filter bar is a `method="get"` form, and check 4 reads
the option values out of the served `<select>` elements, requests each one, and
requires at least two distinct documents back - so a control that only looks like
a control fails the gate. The digests are printed, so the evidence names which
two bodies differed (docs/compliance.md, amendment 2).

That form exists only in the response, so the compliance gate scans responses
rather than files: `scripts/capture-served-pages.sh` captures the HTML the tier
actually serves (routes discovered from the tier's own `/sitemap.xml`) and
`make compliance` scans that corpus on loopback over the fixture tree, in CI as
well as here. It is the only corpus now - the Astro reference tree it used to be
compared against was retired on 2026-09-17 and deleted on 2026-09-18, so there is
no second corpus for a byte comparison to come from.

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
| `agg/v1` is missing or unreadable | The pages that need it answer **503 with a visible error page** and `Cache-Control: no-store` - never a truncated 200. Pages that do not need it (`/`, `/about`) render their no-data state |
| The manifest is present but corrupt | The tier serves the artifact bytes back unchanged rather than inventing a state, so a corrupt manifest is visible as a corrupt manifest. `make verify-serving-local` byte-compares the served bytes against the fixture on purpose |
| A tier process dies | It is a Deployment with a readiness probe on `/healthz`, so the pod is replaced; the shared Caddy proxies to the Service, not to a pod |
| A request is repeated | `ETag` + `304`, and `Cache-Control: private, max-age=60, stale-while-revalidate=300` on HTML so a stale copy is revalidated rather than assumed correct |
| Postgres lost | Rebuildable from the archive. Crawl state is lost, which costs time and not data |

## What is deliberately not here

No query-serving database, no request-time aggregation, no CDN by default, no
message broker, no workflow engine, and no tracing. The Kustomize output is in
`deploy/` in this repository and is the single source of truth for the cluster
(`deploy/README.md`); the ArgoCD `Application` that reconciles it lives in the
`homecluster` repository (ADR-004).
