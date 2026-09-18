# league-of-legends

A self-hosted League of Legends statistics site: win rates, pick rates, ban rates,
builds and matchups by champion, role and patch. Everything is pre-computed
nightly into a published artifact tree, so a page view never touches a database
and never calls the Riot API. There is no server in this repository: the tree is
the deliverable.

Not endorsed by Riot Games.

## What is here

The pipeline is implemented end to end: `lolstats-ingest` crawls Riot's API into
an append-only raw archive and a Postgres control plane, and `lolstats-aggregate`
turns that archive into the published `agg/v1` tree. The tree is the end of the
pipeline. A Go presentation tier used to render pages from it and was removed on
2026-09-18 (`docs/decisions/ADR-011-retire-the-web-tier.md`); what a reader of the
tree owes Riot is written down in `docs/compliance.md` rather than enforced by
code.

| Path | What it is |
|---|---|
| `cmd/lolstats-ingest` | Crawler and scheduler jobs. Subcommands: `worker`, `discover-seeds`, `backfill`, `maintain` |
| `cmd/lolstats-aggregate` | Nightly DuckDB build step. Subcommands: `build`, `verify`, `manifest` |
| `cmd/gen-types` | Emits the JSON Schema and `.d.ts` for the artifact contract |
| `internal/contract` | The frozen Go interfaces between the pipeline's components |
| `internal/aggmodel` | The aggregate artifact types, path builders and schema emitter |
| `internal/riot` | The frozen Match-V5 DTO subset |
| `internal/config` | Environment-driven configuration |
| `internal/obs` | Structured logging and the Prometheus metrics registry |
| `sql/migrations` | Versioned, forward-only Postgres migrations |
| `fixtures/` | Small hand-authored sample payloads for tests, with their provenance |
| `docs/` | Architecture, data sources, compliance, frozen contracts and ADRs |

Read `docs/contracts.md` before writing any code that crosses a component
boundary. It is normative.

## Requirements

- Go 1.27 or newer
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
| `LOLSTATS_AGG_MAX_REJECTED_ROWS` | `0` | Participant rows without a champion or a role a build tolerates before it refuses to publish - the floor of the allowance. Deployed as `25`, measured against position-less rows Riot itself reports |
| `LOLSTATS_AGG_MAX_REJECTED_RATE` | `0` | Share of the window's participant rows the allowance scales with, so a growing archive does not trip a stale absolute floor: `max(floor, ceil(rate * participant_rows))`. Deployed as `0.0008`, 3.8x the worst measured rate and under a tenth of a percent of any window |
| `LOLSTATS_AGG_MIN_CONFIDENT_SHARE` | `0.5` | Share of computable cells that must clear `LOLSTATS_AGG_MIN_CELL_N` for a build to publish. Deployed as `0.15`, a value measured against the live archive's crawl depth |
| `LOLSTATS_AGG_SOURCE_WINDOW_DAYS` | `14` | Trailing window of raw data a build reads |
| `LOLSTATS_AGG_BRACKET` | `all` | Bracket segment. `all` in v1 |
| `LOLSTATS_AGG_QUEUE_ID` | `420` | Ranked solo/duo |
| `LOLSTATS_AGG_PATCH` | - | Patch to build, `major.minor`. Empty means the newest in the archive |
| `LOLSTATS_AGG_DUCKDB_MEMORY_LIMIT` | `2GiB` | Hard ceiling for each DuckDB client. Must stay well under the pod's memory limit — DuckDB otherwise sizes itself from the host's RAM |
| `LOLSTATS_AGG_DUCKDB_THREADS` | `2` | DuckDB thread pool. Matches the aggregate Job's CPU limit; the host-derived default is the node's core count |
| `LOLSTATS_AGG_DUCKDB_TEMP_DIR` | `$TMPDIR` | Parent of the `duckdb-spill` directory DuckDB spills into. Must be writable: root filesystems here are read-only |
| `LOLSTATS_AGG_DUCKDB_MAX_TEMP_SIZE` | `10GiB` | Ceiling on that spill directory. DuckDB's own default is 90 % of the node's free disk |
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

The `worker` subcommand crawls continuously: it claims fetch jobs, calls Riot
under an adaptive rate limiter, writes the raw archive and reports its progress
on the stale-and-frozen numbers an operator watches. The `discover-seeds`,
`backfill`, `maintain`, `migrate` and `static-sync` subcommands are the scheduled
and repair jobs around it; `./bin/lolstats-ingest --help` is the list.

```sh
make types        # regenerate schema/agg.{d.ts,schema.json} from the Go structs
make run          # start the ingest worker
```

## Verify

```sh
make fmt vet test test-race lint vuln build
```

There is no Node toolchain in the repository and, since the web tier was retired
on 2026-09-18, no compliance or serving lane either: the launch-blocking claims
those gates enforced are written in `docs/compliance.md` and nothing asserts them
any more, which that page records as a gap.

`make vuln` runs `govulncheck` over the module graph. Run it on any dependency
change.

### The DuckDB build tests must not skip

The end-to-end analytics tests (`internal/aggregate/fixture*_test.go`) run the
real DuckDB build over the fixture archive and compare it against numbers a human
computed by hand. They need the pinned DuckDB CLI, and they **skip** rather than
fail when it is not installed - which is right for a laptop without it, and means
`make test` alone can be green without having exercised the aggregation path at
all. That is never acceptable in CI, so CI runs a target that refuses a skip:

```sh
make duckdb       # download the pinned DuckDB v1.4.5 client into bin/, sha256-verified
make test-build   # run the package under -race; fail on a skip or a missing PASS
```

`make duckdb` is idempotent and covers `Linux/x86_64`, `Linux/aarch64` and
`Darwin/arm64`; any other platform fails loudly instead of downloading bytes it
cannot verify. The Linux checksums are the same ones the `duckdb` stage of the
`Dockerfile` ships, so the client CI tests with is the client the image runs. The
`verify` job in `.github/workflows/docker-build.yml` installs the pin this way
and runs `make test-build`, and `build-and-push` needs `verify`, so a run in
which these tests did not execute cannot publish an image.

To run the tests against a client you already have, point `LOLSTATS_DUCKDB_BIN`
at it and run `go test ./internal/aggregate/... -v` (the build rejects a client
whose version is not the pin in `internal/aggregate/engine.go`).

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
- `docs/compliance.md` - the Riot obligations that survive, the approved
  wording, and an honest account of what is no longer asserted.
- `docs/frontend/` - the frontend design system: the normative guide
  (`README.md`), the component toolbox (`components.md`), the accessibility
  contract (`a11y.md`), the responsive contract (`responsive.md`), the token
  layer (`tokens.css`) and a review mockup. No product frontend is implemented
  yet; see `docs/decisions/ADR-012-frontend-design-system.md`.
- `docs/decisions/` - the ADRs.
