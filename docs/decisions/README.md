# Architecture decision records

Numbers are allocated here in order, and a new ADR takes the next free number.
Check this table before writing one: two agents independently claiming the same
number is the failure it exists to prevent.

| ADR | File | Title |
| --- | --- | --- |
| 001 | `ADR-001-riot-api-spine.md` | Riot API as the data spine, scraping as an optional cross-check |
| 002 | `ADR-002-postgres-plus-duckdb.md` | PostgreSQL as system of record, DuckDB as a build step, no query-serving database |
| 003 | `ADR-003-go-backend.md` | Go, single binary, no hybrid split |
| 004 | `ADR-004-kustomize-argocd.md` | Kustomize for our manifests, ArgoCD for reconciliation |
| 005 | `ADR-005-demo-data-provenance.md` | Simulated artifact sets are labelled inside the artifact |
| 006 | *(retired)* | `ADR-006-component-api-extensions.md` was deleted on 2026-09-18 with the web tier it extended; see ADR-011 |
| 007 | `ADR-007-pinned-duckdb-cli-engine.md` | The aggregation engine is the pinned DuckDB CLI, run as a subprocess |
| 008 | `ADR-008-no-third-party-ingestion.md` | No third-party ingestion; the scraper stays disabled behind a review date |
| 009 | `ADR-009-operator-identity-and-governing-law.md` | Operator identity, the contact route, and a deliberately unnamed governing law |
| 010 | `ADR-010-public-preview-posture.md` | A labelled public preview while the production key application is pending - **superseded 2026-09-17 by owner decisions D-1 and D-4; the title is kept as the record of what it decided** |
| 011 | `ADR-011-retire-the-web-tier.md` | The web tier is retired: the artifact tree is the deliverable, and the Riot obligations become written requirements |
| 012 | `ADR-012-frontend-design-system.md` | The frontend is a SvelteKit design system with a normative guide, an importable token layer, and a reviewable component toolbox |
| 013 | `ADR-013-url-space.md` | The URL space is a few canonical paths with filter state in query parameters |
