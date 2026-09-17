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

## Cascade: who owns what

`BaseLayout.astro` imports `tokens.css` and then
`src/layouts/fallback/base.css` (the last-resort renderers used when a
component is unavailable). Astro emits both into one bundle in that order, so a
plain selector in the second sheet beats an equal-specificity declaration in the
design system *silently*. Two rules there did exactly that and flattened the look
on every content route:

| Inherited from `fallback/base.css` | What it cost |
| --- | --- |
| `body { background: …; padding: 0 0 3rem; line-height: 1.5 }` | the `background` shorthand erased the 27px graph-paper field, and the `padding` erased the 4rem clearance under the fixed navbar |
| `body { color: … }` | the page field's ink |
| `a:focus-visible { outline: 0; box-shadow: 0 0 0 2px … }` | replaced the design system's ring site-wide, including on the navy footer, where that blue is 2.47:1 |
| `main { max-width: 72rem; padding: 0 1rem }` | capped every page at 1152px instead of the frozen `--content-max` (1200px) |

All four are gone. The shell's content column now lives in `global.css` (`main`),
`base.css` styles only the `fallback-*` classes it renders itself — which is what
its own header comment always claimed — and the fallback table scrolls itself
(`.fallback-data-table { display: block; overflow-x: auto }`), because the system
deliberately carries no `overflow-x: hidden` on the page field to clip it. The
same sheet now supplies the mono display face to that table's header and to its
right-aligned (numeric) cells, mirroring what `DataTable.astro` does with `th`
and `.num`: the 865 champion pages carry the only tables on the site, and they
were the last surface setting a column header in the body face.

The lesson for anyone editing these sheets: if a rule in a *later* stylesheet
restates a *bare* element property the design system already sets, it wins. Scope
it to a class, or put it in `global.css`. `npm run check:tokens` enforces this:
any selector in `fallback/base.css` that names an element without a class, id or
pseudo-class in the same compound selector fails the build.

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
measured contrast in `web/scripts/check-design-tokens.mjs`, which recomputes
every pair from the token values on each `npm run build` and fails the build if
one drops below its threshold. The pairs are measured against both `#efdbbf`
(the page field) and `#f1eae0` (a raised surface).

One pair in the reference design is deliberately *not* inherited. There the
accent is the *hover* ink, so pointing at a link moves it from `--text-color`
(12.20:1 on a surface) down to `--accent-color` (6.13:1): the state a pointer
activates is the least legible one. Here it is inverted — a link is
`--accent-color` at rest (5.42:1 on the page field, 6.13:1 on a surface, AA both)
and `:hover` moves it to `--primary-color` (13.38:1), so no interactive state is
worse than the default state.

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
(`OFL-Inter.txt`, `OFL-JetBrainsMono.txt`, `OFL-Montserrat.txt`) were placed by
`web/scripts/fetch-fonts.mjs`, which resolves the *latin* subset of each weight
from the official source once, by hand, and writes it into `web/public/fonts/`
with the family's OFL 1.1 notice beside it; a build never downloads a font. All
six files and all three notices are in place, served from `/fonts/` with
`font/woff2` and http 200, and `check:tokens` fails if a face loses its file, its
`font-display: swap`, or its family's licence notice. A face whose file is
missing, blocked or still in flight is not an error, because every frozen family
stack ends in its system fallback and the page renders in that instead. No
`local()` source precedes the self-hosted file, so rendering does not depend on
what happens to be installed on the reader's machine.

## Motion

The reference design animates with `ease` over `0.3s`; this system keeps the
mechanical feel but not the duration, because motion here only ever confirms a
pointer. `--ease-out` (`ease-out`) and `--dur-fast` (120ms) are the only two
motion values in the system, and components use them rather than inventing a
duration. Under `prefers-reduced-motion: reduce` every transition and animation
is forced to `0.001ms` with `!important`, so nothing moves and nothing is left
half-way through a transition.

## Utilities a page can adopt

These are the classes `global.css` offers a page that wants to use the system
without re-implementing it. Each is opt-in: nothing in the current pages depends
on one, so adopting them is not a restructure.

| Class | What it is for |
| --- | --- |
| `.ds-container` | the page's content column: `--content-max` (1200px), centred, `--space-5` side padding, `--space-8` below |
| `.ds-panel` | one raised surface at the head of a page: `--bg-light` plus the wide `--shadow-md` (2px ring and a 12px solid offset). `Card` is the compact version at 8px, for repeated items |
| `.ds-table-scroll` | horizontal scroller for a table wider than the column; focusable, so a keyboard user can scroll it |
| `.ds-num` | a figure: `--font-mono` and `tabular-nums` |
| `.ds-visually-hidden` | available to a screen reader, invisible on screen |
| `.ds-navbar` | the fixed masthead's hit area; the bar's own box is taller than what it paints |
| `.ds-footer`, `.ds-on-dark` | marks a surface painted in `--primary-color`, so the focus ring switches to the light `--accent-on-dark` |

`main` itself already carries the content column, so a page needs
`.ds-container` only when it wants the extra bottom space.

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

The five items the spec listed as *fix, do not inherit* are each asserted, so
they cannot be lost again by a later edit:

| Item | Where it lives | Asserted by |
| --- | --- | --- |
| `prefers-reduced-motion: reduce` neutralises motion | `global.css` | token check |
| explicit `:focus-visible` ring | `global.css`, recoloured to `--accent-on-dark` inside a dark surface | token check |
| ink and focus contrast, recomputed from the tokens | `global.css` | token check, 19 pairs |
| `tabular-nums` on numeric and table cells | `global.css` | token check |
| no `overflow-x: hidden` on the page field | `global.css` (deliberate absence, with the reasoning in a comment) | token check |
| `fallback/base.css` may not restate a bare element | `fallback/base.css` (scoped to `fallback-*`) | token check |

## Reproducing the checks

```sh
cd web && npm ci && npm run build   # must exit 0; runs the checks below around astro build
cd web && npm run check:tokens      # palette, idioms, motion, contrast, fonts - source only
cd web && npm run check:islands     # the two islands stay progressive enhancement
cd web && npm run check:fixtures    # the pages and the aggregate artifacts agree
```

`check:tokens` is wired into `build`, so a palette or idiom regression fails the
build rather than the deployment. The ratios above are the output of
`npm run check:tokens`, which recomputes them from the token literals.

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
