# ADR-002: PostgreSQL as system of record, DuckDB as a build step, no query-serving database

- Status: accepted
- Date: 2026-09-17
- Decision: D2

## Context

The pipeline has two very different storage problems and it is easy to solve both
with the same tool and get both wrong.

**Control-plane state:** match dedupe, provenance, a fetch queue, the crawl
frontier, seed and build audit rows. Write volume is bounded by pipeline events -
one row per match, per queue item, per PUUID, per build. Concurrent writers
matter. Idempotency matters more than throughput.

**Analysis:** `GROUP BY champion, role, rank bracket, patch` over every
participant feature row in the retention window. This query is expensive, and it
is the whole product.

The tempting simplification is one analytics database serving both. The mistake
in it is that it moves the expensive query to request time, where it has to be
fast, correct under concurrency and impossible to overload.

## Decision

**PostgreSQL** is the system of record for dedupe state, crawl frontier, the job
queue and pipeline metadata - exactly the six tables in
`sql/migrations/0001_init.up.sql`.

**DuckDB, pinned to an LTS release, is a build step and not a service.** The
nightly job reads the raw Parquet archive, computes cells, suppresses those below
`min_cell_n` and writes static JSON and Parquet artifacts to `agg/v1/**`.

**Per-participant feature rows live in Parquet, not Postgres.** Only the control
plane lives in the database.

**The website never queries a database at request time.** Caddy serves
pre-computed files.

## Alternatives considered

**ClickHouse now.** Fastest at exactly the target query shape, and the honest
runner-up on performance. Rejected on two grounds: it is a second stateful engine
to run and back up for a few hundred thousand rows per patch, and it has no real
upsert - so "re-crawling a player's history" stops being the no-op that
`INSERT ... ON CONFLICT (match_id) DO NOTHING` makes it.

**TimescaleDB.** Continuous aggregates are genuinely attractive for this
workload. Rejected because the aggregation is already a nightly job, so the
benefit is marginal against an extension and a licence question.

**SQLite in WAL mode plus Parquet plus DuckDB.** The closest call in the plan.
Fewest moving parts, no server to back up, and it would work. It loses on
concurrent-writer ergonomics and on operational familiarity - a `SKIP LOCKED`
queue is one line in Postgres and a serialisation problem in SQLite.

**Object-storage-only.** Cannot hold dedupe state or crawl cursors.

**A serving database behind the site.** Rejected outright: it converts a static
file read into a query, which is the one property that makes this architecture
safe under a spike.

## Consequences

- Ingest throughput is bounded by Riot's ~500 requests per 10 seconds, which is
  roughly 12-120 rows per second. Every candidate has more write throughput than
  that, so raw ingest performance did not discriminate between the options - the
  upsert primitive and the backup story did.
- The database stays small enough to dump nightly and to restore in a test, and
  derived data can be rebuilt from the archive rather than migrated in place.
- Aggregation is allowed to change shape freely, because it never needed a
  migration.
- Publish latency is bounded below by the build cadence. A number is at least one
  nightly build old, and the site says so with `generated_at`.

## Migration path

The raw layer is Parquet from day one, so a self-hosted ClickHouse can point at
the same directories and take over aggregation one workload at a time, with no
change to ingest or to the artifact contract.

**Trigger to migrate:** the raw archive exceeds roughly 10^8 rows, or the nightly
build stops fitting its window.

**Verify in Phase 0 before pinning versions:** Postgres 18 versus 19, and the
DuckDB release. DuckDB specifically must be pinned to an LTS release and must not
follow `latest`, because a 2.0 breaking-change window is expected around
2026-10-21 and this project should not be re-validating an analytics engine
mid-build.
