# ADR-013: The URL space is a few canonical paths with filter state in query parameters

- Status: accepted
- Date: 2026-09-18
- Decision: owner (handover review, question 1)
- Related: `docs/frontend/README.md` section 8.1 (the route and parameter
  tables), `docs/decisions/ADR-012-frontend-design-system.md`,
  `docs/contracts.md`

## Context

There is no route table anywhere in the repository. `docs/contracts.md` section
1.3 held one, and it was deleted with the Go presentation tier in ADR-011. The
design guide defines no URL space either, so the shape of every link in this
product is currently undefined.

Two facts make that a problem rather than a detail:

1. `docs/frontend/README.md` section 8 rule 2 requires filter state to be
   reflected in the URL. The explorer is the first slice, and its filters are
   combinatorial: patch x region x queue x bracket x role x tier. Whatever the
   URL looks like, it has to be decided before the explorer is written, not
   after.
2. A URL here is a shared link. The owner pastes them into chat, and a link that
   resolves to a different view than the one it named is a bug that outlives the
   code that produced it.

The available axes are all present in the artifact contract: `patch`, `region`,
`queue` and `bracket` select a partition, and `role` and `tier` filter rows
within it.

## Decision

**A small set of canonical paths names the page. The partition and the filters
live in the query string. Nothing that selects a partition, a subset of rows or
a sort order is ever a path segment.**

| Path | Page |
| --- | --- |
| `/` | landing |
| `/explore` | the data explorer |
| `/champion/<slug>` | one champion, every role and both directions of matchup |
| `/about` | where the numbers come from |
| `/disclaimer` | the non-endorsement notice, standalone |
| `/legal/terms` | terms |
| `/legal/privacy` | privacy |

The query parameters, and what each one selects, are specified in
`docs/frontend/README.md` section 8.1, together with the rule that a parameter at
its default value is omitted from the URL. That rule is what makes exactly one
URL denote each view.

Three sub-decisions, because they are not implied by the table:

- **The champion page is keyed by `slug`, not by the numeric id.** The catalogue
  in `static/<ddragon_version>/champions.json` carries both. A slug is stable
  across patches and readable in a link, and the id is an implementation detail
  of the artefact tree.
- **`/agg` is not part of this table.** The artifact tree is served from the same
  origin under that prefix; it is data, not a page, and it carries no chrome, no
  nav and no route of its own.
- **`/gallery` is not part of this table.** It is the design system's own review
  surface (`docs/frontend/mockup`), not a product route.

## Reasons

**Partitions in the path would multiply the site into a matrix of near-identical
pages.** Two patches, a region, three queues and five brackets is thirty URLs per
champion page, differing only in a coverage stamp and the numbers under it. A
crawler indexes them as near-duplicates and a reader has no reason to prefer one
over another; no human writes a link to *diamond_plus, queue 440, patch 16.17*.
The unfiltered path stays the canonical document, and a filtered view points at
it with a `canonical` link.

**A path that encodes a filter set has no canonical form.** Order, casing, and
which defaults are written out explicitly all produce distinct URLs for the same
view, so "one view, one URL" would have to be enforced by hand in every place
that builds a link.

**Query parameters are what every layer already treats as "the same page,
different state".** The back button, reload, the History API and every share
target handle them correctly without a convention of our own. Static hosting does
not care: the adapter emits one document per path, and the query is read in the
browser.

**Rejected: one page per partition** (`/explore/16.18/EUW/420/all`). It reads
well, and it is what the deleted route table of `docs/contracts.md` section 1.3
did for the server-rendered tier. It fails on the duplicate matrix above, and it
puts a value that changes every two weeks into a path that published links
depend on.

**Rejected: everything in the path, filters included**
(`/explore/patch-16.18/role-MID`). It removes the query string from the design
and costs the canonical form, and it makes a filtered view a different document
rather than the same document in a state.

**Rejected: filters in the fragment** (`/explore#role=MID`). A fragment is not
sent to the server and is not what a share target treats as state; it is the
worst of both.

## Consequences

- A view is a link: `/explore?queue=440&bracket=emerald_plus&role=MID`, and
  `/champion/ahri?patch=16.18`.
- The explorer owns a small URL codec: read the parameters into the filter state
  on load, write the state back on change. Every control that filters writes
  through it, so no control can produce an off-canonical URL.
- An unknown or unpublished parameter value is not an error page. It falls back
  to the default, the URL is rewritten to the canonical form, and the page
  states which partitions are published. Withheld data is a first-class state
  (`docs/frontend/components.md` section 6), not a 404.
- The artifact tree under `/agg` is reachable but unlinked. Nothing in the
  product's navigation points at it.
- The mockup in `docs/frontend/mockup` deliberately does not implement this: it
  keeps filter state in component state and renders the link the URL would carry,
  because it is a design artefact and has no loader. The first product frontend
  implements the table above as written.
