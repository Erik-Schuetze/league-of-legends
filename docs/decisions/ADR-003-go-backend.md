# ADR-003: Go, single binary, no hybrid split

- Status: accepted
- Date: 2026-09-17
- Decision: D4

## Context

Three agents write this pipeline against a frozen interface, and it has to run on
a shared homelab node next to everything else. That makes the language choice
partly a resource question and partly a question of which runtime makes the hard
component easy.

The hard component is a long-running, restart-safe, backpressure-aware crawler
that respects adaptive rate limits and must never get the project's key revoked.
The rest is I/O orchestration: read Parquet, write JSON, supervise subprocesses.

## Decision

Go. One language for the backend, two binaries from one repository
(`lolstats-ingest`, `lolstats-aggregate`), one image with both in it.

No hybrid split. TypeScript appears only in the frontend build, where a Go struct
change reaches it as a regenerated `.d.ts` rather than as a hand-maintained
duplicate.

## Alternatives considered

**Node/TypeScript backend.** The usual argument for it is type sharing across the
stack. That argument does not apply here: the frontend consumes pre-computed
aggregate JSON, so the Go types that produce it can emit a JSON Schema and a
`.d.ts` (`make types`, `cmd/gen-types`), and the sharing problem disappears
without a shared runtime. What remains is a 210-250 MB runtime per process on a
node that runs a worker plus several short-lived CronJobs, against a roughly
10-30 MB static binary.

**Python.** Best library ecosystem for DuckDB and Parquet, and the runner-up in
data work. Rejected for the crawler, which is the component that has to be
correct about concurrency, cancellation and restart safety - the place where
Python's packaging and process model cost the most and its data advantages help
the least.

**Hybrid: Go for ingest, Python or Node for aggregation.** Rejected because
aggregation belongs in SQL and DuckDB, not in either language. The step is
`duckdb` reading Parquet and writing JSON; what surrounds it is orchestration,
which is the same job in any language, so a second runtime buys nothing.

**Rust.** Would produce the best crawler and the longest schedule.

**Third-party Riot client library.** `golio` is the only maintained Go option and
it last shipped in December 2025 with **no rate limiter**, which is the one
component this project genuinely must own. `goriot` is unmaintained. On the
TypeScript side only `twisted` is healthy. The DTO layer is small and frozen
(`internal/riot/dto.go`); the client and limiting logic are written in-house
because they are the project's actual risk.

## Consequences

- `CGO_ENABLED=0` produces a fully static binary, so the runtime image is
  distroless and nonroot with no libc, no shell and no package manager.
- Concurrency primitives fit the crawler exactly: `errgroup`, a semaphore and
  `context` cancellation for shutdown and for `Retry-After` delays.
- The Go structs in `internal/aggmodel` are the single source of truth for the
  artifact contract, and the frontend's types are generated from them. A field
  added on the Go side is a field the frontend can read without a second edit.
- Two binaries in one image means a new subcommand never needs a new image, and a
  deployment or CronJob selects the binary with `command`.

## Reversal trigger

None expected. The artifacts, the raw archive and the control-plane schema are
all language-neutral, so the ingest side could be rewritten without touching the
contract. That is a deliberate property, not an accident: the frozen interfaces
in `docs/contracts.md` are what make this decision cheap to reverse and therefore
safe to make.

**Verify in Phase 0:** whether the Riot client library situation has changed, and
whether MATCH-V5 field coverage matches `internal/riot/dto.go` against a real
payload rather than a published definition.
