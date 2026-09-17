# Changelog

All notable changes to this project are recorded here. Entries are factual and
short. "Breaking" means something that used to work no longer does.

## Unreleased

### Added

- Repository scaffold: `Makefile`, `Dockerfile`, `.golangci.yml`, `go.mod`, MIT
  licence.
- Frozen contracts in `docs/contracts.md`: aggregate artifact shapes, the route
  table, the Go interfaces, the frontend component API and the image contract.
- `internal/contract` - the Go interface surface (`RiotClient`, `RawWriter`,
  `Store`).
- `internal/aggmodel` - artifact types, path builders and a JSON Schema and
  TypeScript emitter.
- `internal/riot` - the frozen Match-V5 DTO subset.
- `internal/config` - environment-driven configuration. All variables are
  prefixed `LOLSTATS_`.
- `internal/obs` - structured logging and the Prometheus metrics registry.
- `sql/migrations/0001_init.up.sql` - the six control-plane tables.
- `cmd/lolstats-ingest` and `cmd/lolstats-aggregate` - compiling subcommand
  skeletons.
- `cmd/gen-types` - generates `web/src/types` from the Go structs.
- `web/` - Astro project, static output, placeholder landing page.
- `docs/architecture.md`, `docs/data-sources.md`, `docs/compliance.md`.
- `docs/decisions/ADR-001` to `ADR-004`.
- `fixtures/` - hand-authored sample payloads with their provenance.

### Notes

- The ingest `worker` subcommand starts, serves metrics and shuts down cleanly,
  but the crawl loop is not implemented. `discover-seeds`, `backfill` and
  `maintain` exit 3 with a not-implemented notice.
- No aggregation logic exists yet, so `lolstats-aggregate` exits 3 for every
  subcommand.
