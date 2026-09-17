// Package aggregate is the DuckDB build step behind cmd/lolstats-aggregate.
//
// It reads the immutable raw match archive (raw/riot/match-v5/...), extracts
// per-participant feature rows, reduces them to per (champion, role) cells for
// one patch window, and publishes the frozen agg/v1 artifact tree described in
// docs/contracts.md.
//
// Why DuckDB is driven as a pinned CLI subprocess rather than through a Go
// binding: every Go binding for DuckDB either requires CGO or ships a static
// library that must be linked into the process. This repository builds every
// binary with CGO_ENABLED=0 and ships them on a distroless runtime with no libc
// to link against. Both an evaluation of github.com/marcboeker/go-duckdb and of
// the official github.com/duckdb/duckdb-go/v2 were performed and both fail to
// build with CGO_ENABLED=0. The pinned CLI is therefore the engine; the reason
// and the rejected alternatives are recorded in
// docs/decisions/ADR-006-pinned-duckdb-cli-engine.md.
//
// The package is deliberately engine-shaped rather than script-shaped: Go owns
// the walk over the archive, the policy (suppression, tier scoring, gates,
// reconciliation) and the publish protocol, and DuckDB is asked for one
// relation at a time. That keeps every rule that matters reviewable in Go.
package aggregate
