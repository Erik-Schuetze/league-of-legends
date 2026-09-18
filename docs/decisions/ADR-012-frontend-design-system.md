# ADR-012: The frontend is a SvelteKit design system with a normative guide

- Status: accepted
- Date: 2026-09-19
- Decision: owner (frontend rebuild after ADR-011)
- Related: `docs/frontend/README.md`, `docs/frontend/components.md`,
  `docs/frontend/a11y.md`, `docs/frontend/responsive.md`,
  `docs/frontend/tokens.css`, `docs/decisions/ADR-011-retire-the-web-tier.md`

## Context

ADR-011 retired the server-rendered web tier and left
`deploy/base/web/service.yaml` pointing at a Service with no backing workload.
The repository therefore publishes no website, and the next presentation layer is
free to be designed rather than inherited: the byte-parity rule died with the
reference tree it compared against, and the retired tier's static-page shapes and
constraints do not carry over.

Three things are nonetheless fixed by the data layer and by policy:

1. `agg/v1` is the deliverable - a pre-built artifact tree
   (`manifest.json`, `tierlist.json`, `champions/<id>.json`,
   `matchups/<role>.json`) whose contract (`docs/contracts.md`) mandates a
   `min_cell_n`, per-cell `n`, and a `suppressed_cells` count. Honesty about
   sample size is in the data, not an option in the UI.
2. `docs/compliance.md` holds approved legal strings - the Riot non-endorsement
   notice, the trademark statement, the free/ungated and no-MMR statements - and
   the site name. Those are publication requirements, so they are on every page.
3. The owner's operating constraints: WCAG 2.1 AA, a per-breakpoint layout
   contract, and **no request leaving the origin at render time**.

The first slice of the product is a **data explorer**: a lot of menus, a lot of
filters, and tables whose whole job is to tell a reader what data exists. The
design target is the owner's personal site (`../erik-schuetze.dev`): a cream and
beige paper canvas, retro blue interaction, and flat boxes that appear to float
because a hard, unblurred accent shadow sits offset from a 2px accent ring.

## Decision

**Recommend SvelteKit + Svelte 5 (runes) + TypeScript + Vite with
`adapter-static`, styling with plain CSS custom properties, and ship the design
as a normative document set plus an executable component toolbox.**

The deliverables, and what each is for:

| Deliverable | Role |
| --- | --- |
| `docs/frontend/README.md` | the normative design language: colour, elevation, type, space, motion, composition, review rules |
| `docs/frontend/tokens.css` | the **single source of truth for values**. Plain custom properties, importable by any future frontend |
| `docs/frontend/components.md` | the toolbox inventory, per-component props and states, and the state matrix |
| `docs/frontend/a11y.md` | the accessibility contract with a measured contrast table |
| `docs/frontend/responsive.md` | the breakpoints and the per-breakpoint layout contract |
| `docs/frontend/mockup/` | a review app that renders the toolbox as **real components**, plus `/gallery`, `/explore` and the landing page |
| `docs/frontend/screenshots/` | the owner's reference captures and the review renders |

Reasons for this stack specifically:

- **One language and one mental model** for the shell, the content pages and the
  data explorer. The explorer is an application; making it an island inside a
  content framework would mean two component models for one product.
- **Tiny runtime over pre-built JSON.** Every view is computed from artifacts the
  aggregator already produced; the browser needs no query engine and no API
  round trip per interaction.
- **URL-synced filter state**, so an explorer view is a shareable, bookmarkable
  link. Filters survive reload, rotation and a paste into chat.
- **No external requests at render time** is a default, not a discipline: fonts
  are vendored woff2, there is no CDN, no analytics and no font host.
- Static output means the artifact tree and the HTML can ship from the same place
  when the ingress workload is written.

Rejected, and why:

| Rejected | Why |
| --- | --- |
| Astro islands | best-in-class for content sites, but the explorer **is** the app; this would add a second component model and a second styling path for the filter and table surface |
| Next.js / React | heavier dependency and build surface, and its server features are features this product does not need over static artifacts |
| Nuxt / Vue | no advantage here over Svelte; a second ecosystem for no gain |
| A build-free HTML + CSS kit | the owner asked for real components and a toolbox, and the state matrix (loading / empty / suppressed / stale / error) is not expressible as static markup |
| A charting library | every visualisation in this product is a table, a bar or a matrix. A chart runtime would be a second design language to keep accessible and on-brand |
| Resurrecting the retired Go presentation tier | retired by ADR-011; its byte-parity constraint has no other side to compare against |

### Non-negotiable properties of the result

- **WCAG 2.1 AA**, verified: `tools/contrast-audit.mjs` exits 0 over 24 pairs and
  `mockup/tools/check-mockup.mjs` runs 461 structural, focus, target, motion and
  network assertions across 7 routes and 3 widths.
- **Every statistic carries its sample size.** There is no opt-out.
- **Missing data has components, not apologies**: withheld, no-published-data and
  stale are distinct, first-class states.
- **Tokens are the only place a value lives.** A component that needs a new
  value adds a token.

## Consequences

- The design is now reviewable without running a product: `docs/frontend/mockup`
  builds, `/gallery` renders every component in every state, and the PNGs in
  `screenshots/mockups/` are the artefact the owner reads.
- `/gallery` is a living specification, not a product page. It is a candidate for
  deletion once the real frontend exists - tracked as open question 4 in
  `README.md` §13.
- `tokens.css` is deliberately framework-neutral. If the stack decision is ever
  revisited, the language survives; only the components are rewritten.
- Three design decisions remain owner-dependent and are shipped as proposals with
  a placeholder, not as silent choices: the two semantic hues
  (`--sig-up #0f5f52`, `--sig-down #9e4a22`), the tier-badge encoding
  (typographic + tint rather than six fills), and the wordmark.
- The mockup's `node_modules` and build output are ignored; only source and the
  review renders are committed.
- Writing the actual frontend remains a separate piece of work. This ADR decides
  the language, the stack and the review surface; it does not write product code.
