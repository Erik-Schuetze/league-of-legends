# league-of-legends

A self-hosted League of Legends statistics site: win rates, pick rates, ban rates,
builds and matchups by champion, role and patch. Everything is pre-computed
nightly and served as static files, so a page view never touches a database and
never calls the Riot API.

Not endorsed by Riot Games.

## What is here

This repository currently contains the scaffold: the frozen interfaces, the
control-plane schema, the toolchain and a placeholder site. The crawler and the
aggregator are written against these interfaces by separate agents and are not
implemented yet.

| Path | What it is |
|---|---|
| `cmd/lolstats-ingest` | Crawler and scheduler jobs. Subcommands: `worker`, `discover-seeds`, `backfill`, `maintain` |
| `cmd/lolstats-aggregate` | Nightly DuckDB build step. Subcommands: `build`, `verify`, `manifest` |
| `cmd/gen-types` | Emits the JSON Schema and `.d.ts` the frontend consumes |
| `internal/contract` | The frozen Go interfaces between the pipeline's components |
| `internal/aggmodel` | The aggregate artifact types, path builders and schema emitter |
| `internal/riot` | The frozen Match-V5 DTO subset |
| `internal/config` | Environment-driven configuration |
| `internal/obs` | Structured logging and the Prometheus metrics registry |
| `sql/migrations` | Versioned, forward-only Postgres migrations |
| `web/` | The Astro site |
| `fixtures/` | Small hand-authored sample payloads for tests, with their provenance |
| `docs/` | Architecture, data sources, compliance, frozen contracts and ADRs |

Read `docs/contracts.md` before writing any code that crosses a component
boundary. It is normative.

## Requirements

- Go 1.27 or newer
- Node 22 or newer, for `web/`
- Postgres 16 or newer, for the control plane. Not needed to build or test.
- No Docker daemon is needed to build or test; `make docker-build` needs one.

`golangci-lint` and `govulncheck` are pinned in the `Makefile` and run through
`go run`, so no global install is required.

## Configure

Everything is environment-driven, and every variable is prefixed
`LOLSTATS_`. `internal/config/config.go` is the list; the table below is the
short version. Missing or invalid values are reported together in one error
rather than one at a time.

| Variable | Default | Meaning |
|---|---|---|
| `LOLSTATS_ENV` | `dev` | `dev`, `staging` or `prod` |
| `LOLSTATS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LOLSTATS_LOG_FORMAT` | `json` | `json` or `text` |
| `LOLSTATS_METRICS_ADDR` | `:9090` | Address the Prometheus listener binds |
| `LOLSTATS_SHUTDOWN_GRACE` | `20s` | Grace period on SIGINT or SIGTERM |
| `LOLSTATS_RIOT_API_KEY` | - | Riot API key. Development keys expire every 24 hours |
| `LOLSTATS_RIOT_REGION` | `EUW` | Region label used in artifact paths |
| `LOLSTATS_RIOT_PLATFORM_ROUTE` | `euw1` | Platform route for ACCOUNT-V1 and LEAGUE-V4 |
| `LOLSTATS_RIOT_REGIONAL_ROUTE` | `europe` | Regional route for MATCH-V5 |
| `LOLSTATS_RIOT_TIMEOUT` | `10s` | Per-request timeout |
| `LOLSTATS_RIOT_MAX_ATTEMPTS` | `5` | Attempts before a job is retried or dead-lettered |
| `LOLSTATS_RIOT_APP_RATE_PER_SECOND` | `18` | Ceiling the adaptive limiter will not exceed |
| `LOLSTATS_RIOT_APP_RATE_PER_2MIN` | `95` | Two-minute ceiling, same rule |
| `LOLSTATS_RIOT_API_KEY_EXPIRES_AT` | - | RFC 3339. Drives the key-age metric |
| `LOLSTATS_POSTGRES_DSN` | - | `postgres://user:pass@host:5432/db?sslmode=disable` |
| `LOLSTATS_POSTGRES_MAX_CONNS` | `8` | Pool size |
| `LOLSTATS_POSTGRES_CONN_TIMEOUT` | `5s` | Connect timeout |
| `LOLSTATS_RAW_ROOT` | `/var/lib/lolstats/raw` | Raw archive root, append-only |
| `LOLSTATS_RAW_ROWS_PER_PART` | `20000` | Rows per Parquet part file |
| `LOLSTATS_RAW_COMPRESSION_LEVEL` | `3` | zstd level |
| `LOLSTATS_AGG_ROOT` | `/var/lib/lolstats/agg` | Aggregate output root, served at `/agg` |
| `LOLSTATS_AGG_SCHEMA_VERSION` | `1` | Artifact envelope version. Changing it needs an ADR |
| `LOLSTATS_AGG_MIN_CELL_N` | `100` | Minimum sample size for a published cell |
| `LOLSTATS_AGG_SOURCE_WINDOW_DAYS` | `14` | Trailing window of raw data a build reads |
| `LOLSTATS_AGG_BRACKET` | `all` | Bracket segment. `all` in v1 |
| `LOLSTATS_AGG_QUEUE_ID` | `420` | Ranked solo/duo |
| `LOLSTATS_AGG_PATCH` | - | Patch to build, `major.minor`. Empty means the newest in the archive |
| `LOLSTATS_HTTP_*_TIMEOUT` | `10s`-`60s` | Metrics listener timeouts |

## Apply the schema

```sh
psql "$LOLSTATS_POSTGRES_DSN" -f sql/migrations/0001_init.up.sql
```

Migrations are forward-only. `0001_init.down.sql` exists to reverse the initial
migration during development; it drops control-plane tables only and never
touches the raw archive or the aggregate output.

## Run it

```sh
make build                          # both binaries into bin/
LOLSTATS_RIOT_API_KEY=... ./bin/lolstats-ingest worker
./bin/lolstats-ingest --help        # every subcommand
```

The `worker` subcommand currently starts, serves metrics and shuts down cleanly
on a signal. The crawl loop is not written yet, so it logs a notice and does
nothing else. The `discover-seeds`, `backfill` and `maintain` subcommands validate
their configuration, report that they are unimplemented and exit 3 - deliberately
not 0, so an unimplemented CronJob cannot look healthy.

```sh
make types        # regenerate web/src/types from the Go structs
make run          # start the ingest worker
make web-build    # build the Astro site
```

## Verify

```sh
make fmt vet test test-race lint vuln build
cd web && npm ci && npm run build
```

`make vuln` runs `govulncheck` over the module graph. Run it on any dependency
change.

## The image

One image, both binaries. A Deployment or CronJob picks the binary with
`command`.

```sh
make docker-build
```

`ghcr.io/erik-schuetze/league-of-legends`, `linux/amd64`, distroless nonroot,
`CGO_ENABLED=0`. Tags and labels are frozen in `docs/contracts.md` section 5.

## Documentation

- `docs/contracts.md` - frozen interfaces. Read this before writing cross-component code.
- `docs/architecture.md` - how the pieces fit, and what happens when one fails.
- `docs/data-sources.md` - where every datum comes from, and the Phase 0 gate table.
- `docs/compliance.md` - Riot policy conformance checklist and its evidence.
- `docs/decisions/` - the ADRs.
- `AGENTS.md` - how changes are written down in this repository.
- `backlog.md` - deferred work.
