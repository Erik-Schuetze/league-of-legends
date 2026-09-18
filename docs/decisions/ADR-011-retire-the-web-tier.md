# ADR-011: Retire the server-rendered web tier

- Status: accepted
- Date: 2026-09-18
- Decision: owner (demolition of the presentation layer)
- Supersedes: `ADR-006-component-api-extensions.md`, which described the
  component API of a tree that no longer exists

## Context

The site was served by a Go presentation tier (`cmd/lolstats-web`,
`internal/webtier`) that had been ported byte for byte from an Astro build. That
reference build (`web/`) was deleted on 2026-09-18, and with it went every
test, gate and decision record that gave the port its shape. What survived was
the port's own constraints as orphaned comments: a byte-parity rule against a
tree that no deployment rendered from, a compliance gate that captured and
scanned pages a running tier served, and a deploy tree whose Service pointed at
a workload with no data behind it.

None of that is the data layer. The crawler, the aggregator and the artifact
contract are the parts of this repository that produce something no other copy
holds. The presentation layer was a consumer of the published `agg/v1` tree, and
it was the only consumer.

## Decision

**The web tier is retired. The crawl, aggregate and artifact layers stay.**

- `internal/webtier/` and `cmd/lolstats-web/` are deleted, together with the
  compliance scripts and Makefile lanes that existed only to start or scan the
  tier. The image drops from three binaries to two.
- The byte-parity constraint is retired, not carried: the reference build it
  compared against does not exist, so a parity rule has no other side.
- Porting the tier to a new stack was rejected. The tier was a presentation
  layer with no reader contract that outlives it, and a future website is free
  to consume the artifact tree afresh rather than inherit the port's shapes.
- `deploy/base/web/service.yaml` is kept as the future ingress point; it has no
  backing workload until a new tier is written.
- The Riot policy obligations the tier used to enforce at runtime, and the
  approved non-endorsement wording it used to carry, are moved into
  `docs/compliance.md` as written requirements. Compliance is a property of the
  publication, not of the deleted renderer.
- `cmd/gen-types` and `schema/` stay: the artifact declarations are the data
  layer's own output.

## Consequences

The repository publishes no website. `agg/v1` is produced and stored, and
nothing serves it; the shared Caddy upstream resolves to a Service with no
endpoints. That is the intended state until a new presentation layer is written
against the artifact contract in `docs/contracts.md`.

Git history is untouched: the deleted tier and its decisions remain reachable in
the commits that describe them, and the working tree is what this ADR changes.
