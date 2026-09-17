# ADR-006: Additive component API extensions, and self-hosted fonts

- Status: accepted
- Date: 2026-09-17
- Decision: coordinator (frontend and design-system workstreams)
- Supersedes: nothing. Amends the frontend component API frozen in `docs/contracts.md` by addition only.

## Context

`docs/contracts.md` freezes the frontend component API: names, paths and prop
types. Freezing is what let the visual language (`web/src/styles/`) and the
component library (`web/src/components/`) be written in parallel with the pages
that compose them. Once the pages existed, two places where the frozen surface
was too narrow to render real data became visible, and one part of the
typography contract was named but never implemented:

1. **`Card` could not carry cell-size context.** `Card` has `sampleN?: number`
   and renders a bare `n 1,234` line. The site has an explicit rule that a
   small sample is disclosed next to the number it qualifies, and
   `SampleSizeNotice` already implements that disclosure - but `Card` had no
   field to pass a minimum cell size through, so a card either printed a bare
   count or had to build the notice itself, which is exactly the duplication the
   component exists to prevent.

2. **`BuildList` had no way to resolve Data Dragon ids to game data.**
   `Build.key` is `number[]` and the component renders those numbers as
   monospaced chips, because it has no icon base URL and no game-data version.
   The aggregator pipeline now publishes that mapping as static JSON, so the
   numbers are resolvable without a runtime fetch.

3. **The typography contract named three faces and shipped none of them.**
   Plan section 7.1 names Inter for body, JetBrains Mono for headings and all
   numbers, and Montserrat for the brand. No `@font-face` rule existed
   anywhere, so every page rendered in a system fallback, and the site would
   have had to reach a third-party font CDN to be fixed - which the project's
   "no third-party runtime requests" rule forbids.

A third, smaller matter surfaced at the same time and is recorded here because
it looks like a defect and is not one. The brief for this change described
`Nav` as linking to `/matchups/<slug>` while the route table says
`/matchups/<role>`. `Nav.astro` already emits
`` `/matchups/${roleSlug(role)}` ``, which is `/matchups/mid` and so on - the
route table's shape. No route change was made and no route was invented.

## Decision

Three additive changes, all optional, none of them a rename or a removal.

**1. `Card` gains `minCellN?: number`.** When it is present together with
`sampleN`, the card composes `SampleSizeNotice` with the two values instead of
printing the bare `n` line. `SampleSizeNotice` is imported by `Card`; it is not
reimplemented, and it is not passed in as a slot, which would have moved the
disclosure's markup into every call site.

**2. `BuildList` gains `lookup?: BuildLookup`.** The interface is declared in
`BuildList.astro` and is agreed **by shape, not by import**: the pages pass the
object they built from `web/src/data/{items,runes,spells}.json`, and the
component does not know where the JSON came from.

```ts
export interface BuildLookupEntry { name: string; icon: string } // icon is a Data Dragon CDN path
export interface BuildLookup {
  ddragonVersion: string;
  items: Record<number, BuildLookupEntry>;
  runes: Record<number, BuildLookupEntry>;
  spells: Record<number, BuildLookupEntry>;
}
```

When the lookup resolves a key, that key renders as an `<img>` with the site's
border treatment and `alt` text, and the key cell stops being `aria-hidden`
because the images now carry the meaning. When the lookup is absent, or when an
individual key is missing from it, that key renders as today's numeric chip. The
component degrades per key; it never fails to render a row.

**3. Self-hosted fonts, declared in `web/src/styles/fonts.css`.** Three
families, one file per weight, served from this origin at `/fonts/`:

| Family | Role | Files |
| --- | --- | --- |
| Inter | body copy | `inter-400.woff2`, `inter-600.woff2`, `inter-700.woff2` |
| JetBrains Mono | headings and all numbers | `jetbrains-mono-400.woff2`, `jetbrains-mono-700.woff2` |
| Montserrat | brand | `montserrat-700.woff2` |

`font-display: swap` on every face, `font-style: normal`, and a `unicode-range`
limited to the Latin, Latin-1 Supplement, Latin Extended-A, general punctuation,
currency, superscript, arrow, mathematical operator and geometric shape blocks -
the repertoire this site's copy, statistics and separators actually use. The
frozen family stacks keep their existing system fallbacks, so a file that is
missing, blocked or still downloading renders in the fallback face rather than
in nothing. No font request leaves this origin at runtime.

**The route table in `docs/contracts.md` remains authoritative.** This ADR
changes prop surfaces only. It does not add, remove, rename or re-home a route,
and where any other artefact disagrees with the route table - including the
`/matchups/<slug>` spelling that appears in prose elsewhere in that document -
the route table wins.

## Alternatives considered

**A `notice` slot on `Card` instead of `minCellN`.** Rejected: it moves the
disclosure's markup into every call site, so the same sentence is written once
per page, and `SampleSizeNotice`'s own semantics stop being the single source of
the rule.

**A required `lookup` on `BuildList`.** Rejected: it breaks every existing call
site, including the ones rendering a build table for a champion with no game
data, and it makes the component's rendering depend on an aggregate that may not
be there.

**`local()` first in the `src` list.** Rejected: it makes rendering depend on
what happens to be installed on the reader's machine, so the same page can look
different per machine and the self-hosted file can be silently unused.

**Per-subset files with a dozen `@font-face` blocks.** Rejected: the files are
placed by the frontend workstream under `web/public/fonts/`, and a broad
`unicode-range` per weight gets the only benefit that matters here - a browser
does not download a face whose range the page never uses - without committing
that workstream to a file naming scheme per subset.

**A third-party font CDN.** Rejected: it is a third-party request on every page
load, which the project forbids, and it makes first paint depend on a host
outside this deployment.

## Consequences

- `minCellN` has an effect only alongside `sampleN`; with `minCellN` absent the
  card's markup and styling are byte-identical to the frozen behaviour.
  Rendering a notice for a missing `n` would have meant printing a sample size
  of zero, which is worse than printing nothing.
- `BuildList`'s fan-out is now per key: a build whose items are partly unknown
  renders icons and chips in the same row.
- The fonts are not yet on disk; until `web/public/fonts/` is populated, every
  page renders in the fallback stacks it already used. `web/src/styles/` needs
  no further change when the files land.
- `docs/design-system.md` catalogues the gap that is closed and the gap that
  remains (no `astro check` in `web/package.json`).
