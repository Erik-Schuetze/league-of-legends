# Design system

The visual language of the site and the component library that renders it. Three
stylesheets and eleven components, plain CSS, no dependency beyond Astro itself.

Everything here is frozen by `docs/contracts.md`: component names, file paths,
prop names and prop types. Changing any of them needs an ADR in
`docs/decisions/`. The additive extensions recorded in
`docs/decisions/ADR-006-component-api-extensions.md` (`Card.minCellN`,
`BuildList.lookup`, self-hosted fonts) are the only changes to that surface so
far, and the route table in `docs/contracts.md` remains authoritative.

## One import

A layout imports one stylesheet:

```astro
import '../styles/tokens.css';
```

`tokens.css` starts with `@import './global.css'` and `@import './fonts.css'`, so
that single import pulls in the whole surface. Nothing else needs importing and
no page needs to know that the system is split across three files.

## Why three files

`docs/contracts.md` says `tokens.css` declares thirteen names at `:root` "and no
others". The system also needs semantic and density tokens, and `@font-face` is
not a token at all. Rather than widen the frozen block, the scales live in
`global.css` and the font declarations in `fonts.css`, both of which
`tokens.css` imports.

| File | Holds |
| --- | --- |
| `web/src/styles/tokens.css` | the thirteen frozen tokens, and nothing else |
| `web/src/styles/global.css` | reset, blueprint grid, semantic scales, spacing, density, type scale, print and motion rules |
| `web/src/styles/fonts.css` | the `@font-face` rules for the three self-hosted families |

## The frozen tokens

Values lifted verbatim from plan section 7.1.

| Token | Value | Role |
| --- | --- | --- |
| `--bg-color` | `#efdbbf` | page field |
| `--bg-light` | `#f1eae0` | raised surfaces; light text on dark |
| `--primary-color` | `#0b162a` | structure, headers, footer |
| `--accent-color` | `#1b4bc6` | links, edges, focus ring |
| `--text-color` | `#242a2b` | body copy |
| `--font-heading` | `'JetBrains Mono', ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace` | headings |
| `--font-body` | `'Inter', system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif` | body copy |
| `--font-mono` | `'JetBrains Mono', ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace` | all statistics |
| `--grid-line` | `rgba(27, 75, 202, 0.06)` | blueprint grid stroke |
| `--grid-size` | `27px` | blueprint grid pitch |
| `--border-width` | `2px` | every edge |
| `--shadow-offset-sm` | `8px` | hard offset shadow, compact surfaces |
| `--shadow-offset-md` | `12px` | hard offset shadow, page framing |
| `--radius` | `0` | no radius anywhere |

`--radius-btn` (5px) in `global.css` is the single exception, and only for the
filter bar's submit button.

## Semantic scales

Both scales are built inside the palette and never encode meaning by hue alone.

**Tier grades** paint `--tier-{splus,s,a,b,c,d}-bg` under `--tier-ink`, which is
`--primary-color`: `d` 13.78:1, `c` 12.23:1, `b` 10.41:1, `a` 8.75:1, `s` 7.32:1,
`s+` 6.06:1. Every badge also prints the letter, so the tint is redundant
decoration. The grade is the value.

**Win-rate deviation** paints `--dev-up-bg`, `--dev-down-bg` or `--dev-empty-bg`.
Below-even and not-published fills carry a hatch (`--dev-up-hatch`,
`--dev-down-hatch`, `--dev-empty-hatch`) so the two directions separate in
greyscale, and every value prints an explicit `+` or `-`. Not-published cells
print the words `withheld` or `no sample` rather than a zero or a blank.

All foreground/background pairs introduced by this system are listed with their
measured contrast in `.agent-artifacts/contrast-report.txt`, checked against both
`#efdbbf` and `#f1eae0`.

## Density

Plan section 7.2: tables and lists run tight, headings and page framing stay
generous. `--row-pad-y` and `--row-pad-x` are the default table rhythm;
`--row-pad-y-dense` (3px) applies to `.ds-dense` tables and `.ds-build-list`.
`--fs-xs` through `--fs-3xl` are the original scale for headings, and
`--space-1` through `--space-8` space the page itself.

## Type

`font-variant-numeric: tabular-nums` applies to `table`, `th`, `td`, `output`,
`time` and `.ds-num`, so columns of figures line up.

**Fonts are self-hosted.** Plan section 7.1 names three families: Inter for body
copy, JetBrains Mono for headings and all numbers, Montserrat for the brand. The
reference design links Google Fonts, which is a third-party fetch on every page
load and is not allowed here, so the same three faces are declared in
`fonts.css` with `@font-face` and served from this origin at `/fonts/`, one file
per weight, `font-display: swap`:

| Family | Role | Files under `web/public/fonts/` |
| --- | --- | --- |
| Inter | body copy | `inter-400.woff2`, `inter-600.woff2`, `inter-700.woff2` |
| JetBrains Mono | headings and all numbers | `jetbrains-mono-400.woff2`, `jetbrains-mono-700.woff2` |
| Montserrat | brand | `montserrat-700.woff2` |

Each face also carries a `unicode-range` limited to the Latin, Latin-1
Supplement, Latin Extended-A, general punctuation, superscript, currency, arrow,
mathematical operator and geometric shape blocks, so a face a page never uses is
not downloaded. The woff2 files themselves and their OFL licence notices
(`OFL-Inter.txt`, `OFL-JetBrainsMono.txt`, `OFL-Montserrat.txt`) are placed by
the frontend workstream; all six are in place and served from `/fonts/` with
`font/woff2` and http 200, which `.agent-artifacts/font-fetch.py` checks. A face
whose file is missing, blocked or still in flight is not an error, because every
frozen family stack ends in its system fallback and the page renders in that
instead. No `local()` source precedes the self-hosted file, so rendering does
not depend on what happens to be installed on the reader's machine.

## Components

Eleven components in `web/src/components/`, each a dependency-free `.astro` file
that renders complete, correct markup with no client script.

| Component | Props (from `docs/contracts.md`) | Notes |
| --- | --- | --- |
| `Nav.astro` | `patch`, `active?`, `roles?` | masthead, role links, active state |
| `Footer.astro` | `patch`, `generatedAt?`, `sourceWindow?` | provenance and compliance links |
| `Card.astro` | `title`, `href?`, `tone?`, `sampleN?`, `minCellN?` | compact stat card |
| `DataTable.astro` | `columns`, `rows`, `caption?`, `initialSortKey?`, `initialSortDir?`, `emptyMessage?`, `dense?` | static, server-sorted |
| `TierBadge.astro` | `tier`, `n?` | letter plus tint |
| `StatValue.astro` | `value`, `format?`, `digits?`, `label?`, `n?`, `unavailable?` | tabular figures |
| `SampleSizeNotice.astro` | `n`, `minCellN`, `suppressedCells?`, `role?` | thin or withheld sample |
| `FilterBar.astro` | `filters`, `action?`, `method?`, `hidden?` | plain GET form, works without script |
| `BuildList.astro` | `title`, `builds`, `kind`, `limit?`, `emptyMessage?`, `lookup?` | items, runes, spells |
| `TableIsland.astro` | `tierList`, `champions`, `role?`, `initialSortKey?`, `initialSortDir?` | sortable and filterable |
| `HeatmapIsland.astro` | `matchups`, `champions`, `role`, `minCellN` | matchup matrix |

### Islands

`TableIsland` and `HeatmapIsland` are progressive enhancement, per plan
section 7.3. At most two islands per page, both non-essential: the server renders
the full sorted table, every matrix cell, the legend and the status lines. With
JavaScript disabled the pages are complete; the scripts only add sorting,
filtering, hover and tap detail on top of that markup.

Three client files hold the runtime:

| File | Loads | Size in `web/dist` |
| --- | --- | --- |
| `island-boot.client.ts` | with the page, `type="module"` | 1.7 kB shared chunk |
| `table-island.client.ts` | when the table is within 200px of the viewport | 2.0 kB |
| `heatmap-island.client.ts` | when the matrix is within 200px of the viewport | 2.5 kB |

The per-island boot script is about 140 bytes and does nothing but call the
loader, which watches with an `IntersectionObserver` and imports the runtime
dynamically. No framework, no npm dependency, no data in the chunks: the
runtimes read the rendered DOM.

Accessibility is part of the markup, not a later pass. Table headers carry
`scope`; cell values carry letter grades or explicit signs; island toolbars are
hidden until hydration and announced through `role="status"`; the matrix is
keyboard operable with a roving tab stop, arrow keys, Home, End, Enter and Space;
`prefers-reduced-motion` removes every transition.

## Reproducing the checks

```sh
cd web && npm ci && npm run build          # must exit 0
python3 .agent-artifacts/contrast-check.py # reads the tokens from the stylesheets
python3 .agent-artifacts/nojs-check.py     # curl of the built HTML, no JS engine
```

## Gaps owned elsewhere

These are cases where the frozen prop surface could not express what the page
needed. Nothing was widened silently: each was either reported or, where the
coordinator authorised it, recorded as an additive extension in
`docs/decisions/ADR-006-component-api-extensions.md`.

Closed by ADR-006 (additive only, no rename, no removal):

1. **Icons in `BuildList`.** `Build.key` is `number[]` with no vendor image URL,
   so the component rendered numeric chips. It now takes an optional
   `lookup?: BuildLookup`; a resolved key renders as an icon and an unresolved
   key still renders as the numeric chip. Without the prop the markup is
   unchanged.
2. **`Card` composing `SampleSizeNotice`.** Required `sampleN` and `minCellN`
   together; `Card` now takes `minCellN?: number` and composes the notice when
   both are present, keeping the bare `n` line when they are not.
3. **Fonts.** Decided and implemented as self-hosted `@font-face`, as described
   under Type above. Nothing further is needed from this workstream when the
   woff2 files land.

Still open:

4. **`Nav` matchup links and the route table.** The contract's prose spells the
   matchup route `/matchups/<slug>` while the route table in the same document
   and plan section 6.4 list `/matchups/<role>`. `Nav.astro` already emits
   `/matchups/<role>`, which is the shape the route table and the built pages
   use, so there is no code change to make; the stale `/matchups/<slug>` spelling
   is the only artefact that disagrees, and `docs/contracts.md` is not writable
   from this workstream.
5. **`astro check` is not runnable.** `@astrojs/check` is not a devDependency and
   running it prompts to install it. `tsc --noEmit` is the available substitute
   and reports no errors.
