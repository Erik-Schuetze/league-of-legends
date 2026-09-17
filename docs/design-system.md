# Design system

**Retired 2026-09-18.** This document describes the Astro tree in `web/` - three
stylesheets and eleven `.astro` components - and that tree was deleted on
2026-09-18 (the retirement is recorded in `docs/contracts.md` sections 3 and 5).
The design language did not change with it: the stylesheets and the font files
are still the ones the site serves, checked in under `internal/webtier/assets/`,
and the component markup is now the Go templates under
`internal/webtier/templates/`. What is gone is the toolchain, so every `.astro`
path, `npm run` command and `web/dist` size below is a record of the port rather
than a live instruction. Everything else - the tokens, the cascade and its
lessons, the contrasts, the type scale, the accessibility rules - still holds of
the bytes the tier serves today.

The visual language of the site and the component library that renders it: plain
CSS, no CSS framework, no client-side framework, and - the property the port had
to preserve - no runtime dependency to fetch. The tier that renders these pages
is Go: the standard library, the module's own packages, and the metrics client
for `/metrics`.

Everything here was frozen by `docs/contracts.md`: component names, file paths,
prop names and prop types. Changing any of them needed an ADR in
`docs/decisions/`. The additive extensions recorded in
`docs/decisions/ADR-006-component-api-extensions.md` (`Card.minCellN`,
`BuildList.lookup`, self-hosted fonts) are the only changes that surface ever
took. The freeze is now executable rather than documentary:
`internal/webtier/frozen_tokens_test.go` fails when the settled token set, the
contrast floors or the inline order change, and
`internal/webtier/a11y_contract_test.go` does the same for the accessibility
rules in §3 below.

## One import

The layout imported one stylesheet:

```astro
import '../styles/tokens.css';
```

`tokens.css` starts with `@import './global.css'` and `@import './fonts.css'`, so
that single import pulled in the whole surface. Nothing else needed importing and
no page needed to know that the system is split across three files.

The build resolved those three imports into one file, and the tier still serves
that file byte for byte as its only `<link rel="stylesheet">`:
`internal/webtier/assets/astro/JsonLd.BEq7AnVK.css`, published at the path the
static site used (`/_astro/JsonLd.BEq7AnVK.css`, `internal/webtier/assets.go`).
So the property survives the retirement: one stylesheet reference per page,
however many sheets it was authored as. The rules that were component-scoped are
a second embedded file inlined into the document, and the frozen layer is a third
inlined after it - see "Cascade" below.

## Cascade: who owns what

`BaseLayout.astro` imported `tokens.css` and then
`src/layouts/fallback/base.css` (the last-resort renderers used when a
component is unavailable). The build emitted both into one file in that order, so
a plain selector in the second sheet beats an equal-specificity declaration in
the design system *silently*. Two rules there did exactly that and flattened the
look on every content route:

| Inherited from `fallback/base.css` | What it cost |
| --- | --- |
| `body { background: …; padding: 0 0 3rem; line-height: 1.5 }` | the `background` shorthand erased the 27px graph-paper field, and the `padding` erased the 4rem clearance under the fixed navbar |
| `body { color: … }` | the page field's ink |
| `a:focus-visible { outline: 0; box-shadow: 0 0 0 2px … }` | replaced the design system's ring site-wide, including on the navy footer, where that blue is 2.47:1 |
| `main { max-width: 72rem; padding: 0 1rem }` | capped every page at 1152px instead of the frozen `--content-max` (1200px) |

All four are gone. The shell's content column now lives on `main` in the served
baseline - `main{max-width:var(--content-max);padding:0 var(--space-5);margin:0 auto}`,
which is the same rule the static tree carried in `global.css` - and
`fallback/base.css` styled only the `fallback-*` classes it rendered itself, which
is what its own header comment always claimed. The frozen layer scrolls the
fallback table itself (`.fallback-data-table { display: block; overflow-x: auto }`
in `internal/webtier/assets/css/components.css`), because the system deliberately
carries no `overflow-x: hidden` on the page field to clip it. That layer also
supplies the mono display face to the fallback table's header and to its
right-aligned (numeric) cells, mirroring what the `DataTable` component did with
`th` and `.num`, so a fallback table does not set its column headers in the body
face.

The lesson for anyone editing these sheets survives the retirement and now has
three sheets in that order rather than two: the served baseline, the inlined
scoped chunk, and the frozen layer inlined last. If a rule in a *later*
stylesheet restates a *bare* element property an earlier one already sets, it
wins. Scope it to a class, or put it in `global.css`. The load order is asserted
rather than assumed - `TestFrozenLayerIsInlinedLast` in
`internal/webtier/frozen_tokens_test.go` fails if the frozen layer stops being the
last inline block, and `TestFrozenLayerWinsTheAriaCurrentTie` fails if a rule that
exists only to win a specificity tie stops winning it. The rule itself - that no
selector in the ported fallback sheet names a bare element - has no assertion
any more: it was the old Node token check's, and that check went with the
toolchain.

## Why three files

`docs/contracts.md` says `tokens.css` declares thirteen names at `:root` "and no
others". The system also needs semantic and density tokens, and `@font-face` is
not a token at all. Rather than widen the frozen block, the scales lived in
`global.css` and the font declarations in `fonts.css`, both of which
`tokens.css` imports.

| File | Holds | Now at |
| --- | --- | --- |
| `web/src/styles/tokens.css` | the thirteen frozen tokens, and nothing else | inside the embedded baseline sheet |
| `web/src/styles/global.css` | reset, blueprint grid, semantic scales, spacing, density, type scale, print and motion rules | inside the embedded baseline sheet |
| `web/src/styles/fonts.css` | the `@font-face` rules for the three self-hosted families | inside the embedded baseline sheet |

The build resolved the imports, so the three are no longer separate files on
disk: they are concatenated into `internal/webtier/assets/astro/JsonLd.BEq7AnVK.css`,
which `internal/webtier/assets.go` serves as the baseline. The two sheets the
freeze owns are separate files in their own right
(`internal/webtier/assets/css/design-tokens.css` and `components.css`), and they
are inlined into the document rather than linked.

## The frozen tokens

Values lifted verbatim from plan section 7.1.

| Token | Value | Role |
| --- | --- | --- |
| `--bg-color` | `#efdbbf` | page field |
| `--surface` | `#f1eae0` | raised surfaces; light text on dark. The reference design called this `--bg-light`, which states a surface as if it were a theme; the served baseline already declares `--surface`, so the frozen layer kept that name (`TestFrozenDesignTraps` fails if `--bg-light` returns) |
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
measured contrast, and `TestFrozenTokenContrast` in
`internal/webtier/frozen_tokens_test.go` recomputes every pair the served sheets
actually put together from the token values and fails if one drops below its
threshold. It replaced `web/scripts/check-design-tokens.mjs`, which ran the
same arithmetic at `npm run build` time. The pairs are measured against both
`#efdbbf` (the page field) and `#f1eae0` (a raised surface).

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

| Family | Role | Files under `internal/webtier/assets/fonts/` |
| --- | --- | --- |
| Inter | body copy | `inter-400.woff2`, `inter-600.woff2`, `inter-700.woff2` |
| JetBrains Mono | headings and all numbers | `jetbrains-mono-400.woff2`, `jetbrains-mono-700.woff2` |
| Montserrat | brand | `montserrat-700.woff2` |

Each face also carries a `unicode-range` limited to the Latin, Latin-1
Supplement, Latin Extended-A, general punctuation, superscript, currency, arrow,
mathematical operator and geometric shape blocks, so a face a page never uses is
not downloaded. The woff2 files themselves and their OFL licence notices
(`OFL-Inter.txt`, `OFL-JetBrainsMono.txt`, `OFL-Montserrat.txt`) were placed by
`web/scripts/fetch-fonts.mjs`, which resolved the *latin* subset of each weight
from the official source once, by hand, and wrote it into `web/public/fonts/`
with the family's OFL 1.1 notice beside it; no build ever downloaded a font. The
files now live in `internal/webtier/assets/fonts/` and are embedded in the
binary. All six faces and all three notices are in place: the faces are served
from `/fonts/` as `font/woff2` and the notices as `text/plain`, all http 200,
in the same tree the routes are allow-listed from (`internal/webtier/assets.go`),
which `internal/webtier/server_test.go` asserts. The old Node token check also
failed if a face lost its file, its `font-display: swap` or its licence notice;
nothing asserts those two declarations now, and a face whose file went missing
would panic at first read through the allow-list rather than serve a 404. A face
that is missing, blocked or still in flight is not an error either way, because
every frozen family stack ends in its system fallback and the page renders in that
instead. No
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
| `.ds-panel` | one raised surface at the head of a page: `--surface` plus the wide `--shadow-md` (2px ring and a 12px solid offset). `Card` is the compact version at 8px, for repeated items |
| `.ds-table-scroll` | horizontal scroller for a table wider than the column; focusable, so a keyboard user can scroll it |
| `.ds-num` | a figure: `--font-mono` and `tabular-nums` |
| `.ds-visually-hidden` | available to a screen reader, invisible on screen |
| `.ds-navbar` | the fixed masthead's hit area; the bar's own box is taller than what it paints |
| `.ds-footer`, `.ds-on-dark` | marks a surface painted in `--primary-color`, so the focus ring switches to the light `--accent-on-dark` |

`main` itself already carries the content column, so a page needs
`.ds-container` only when it wants the extra bottom space.

## Components

Eleven components lived in `web/src/components/`, each a dependency-free
`.astro` file that rendered complete, correct markup with no client script. The
retirement kept the component boundary and the names and re-expressed the markup
as Go templates: the same eleven are `{{ define }}` blocks parsed from
`internal/webtier/templates/*.tmpl` and `templates/pages/*.tmpl`
(`internal/webtier/render.go`), and the prop lists below are still the contract
`docs/contracts.md` recorded. Two differences a reader should expect: the define
names are lower camel case and not always the file name (`tableIsland`,
`dsBuildList`, `tierBadge`, `sampleSizeNotice`), and three of the eleven are the
no-script fallbacks for the two islands (`fallbackDataTable`, `fallbackStatValue`,
`fallbackTierBadge`), which live in the same layer now that nothing is hydrated
from a component tree.

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

Three client files hold the runtime. The names below are the sources the build
took them from; what is served is the built chunk in
`internal/webtier/assets/astro/`, whose name carries the content hash:

| File | Loads | Served size |
| --- | --- | --- |
| `island-boot.client.ts` | with the page, `type="module"` | 1.7 kB shared chunk (`preload-helper.DJSjwBkS.js`) |
| `table-island.client.ts` | when the table is within 200px of the viewport | 2.0 kB (`table-island.client.6YN-J6Z7.js`) |
| `heatmap-island.client.ts` | when the matrix is within 200px of the viewport | 2.5 kB (`heatmap-island.client.Ba_X43a2.js`) |

The per-island boot script is 157 or 163 bytes and does nothing but call the
loader, which watches with an `IntersectionObserver` and imports the runtime
dynamically. No framework, no npm dependency, no data in the chunks: the
runtimes read the rendered DOM. The boot chunk keeps an `.astro` segment in its
served name (`TableIsland.astro_astro_type_script_index_0_lang.UqXLqAt9.js`),
which is the only place the retired build layer is still visible.

Accessibility is part of the markup, not a later pass. Table headers carry
`scope`; cell values carry letter grades or explicit signs; island toolbars are
hidden until hydration and announced through `role="status"`; the matrix is
keyboard operable with a roving tab stop, arrow keys, Home, End, Enter and Space;
`prefers-reduced-motion` removes every transition.

The five items the spec listed as *fix, do not inherit* are each asserted, so
they cannot be lost again by a later edit. The "where it lives" column still names
the source file the rule was written in; since the retirement those sources are
folded into the served baseline (`internal/webtier/assets/astro/JsonLd.BEq7AnVK.css`)
and the two frozen sheets in `internal/webtier/assets/css/`.

| Item | Where it lives | Asserted by |
| --- | --- | --- |
| `prefers-reduced-motion: reduce` neutralises motion | `global.css` | `a11y_contract_test.go` (§3.1) |
| explicit `:focus-visible` ring | `global.css`, recoloured to `--accent-on-dark` inside a dark surface | `a11y_contract_test.go` (§3.2) |
| ink and focus contrast, recomputed from the tokens | `global.css` | `frozen_tokens_test.go`, `TestFrozenTokenContrast` |
| `tabular-nums` on numeric and table cells | `global.css` | `a11y_contract_test.go` (§3.4) |
| no `overflow-x: hidden` on the page field | `global.css` (deliberate absence, with the reasoning in a comment) | `a11y_contract_test.go` and `TestFrozenDesignTraps` |
| `fallback/base.css` may not restate a bare element | `fallback/base.css` (scoped to `fallback-*`) | nothing. This was the Node token check's rule only, and that check went with the toolchain; the scoping is now a property of the ported bytes with no assertion behind it |

## Reproducing the checks

The checks are Go tests now. `npm ci && npm run build` in a `web/` tree is not
runnable - the tree was deleted on 2026-09-18 - so the properties it used to
assert are asserted against the served output instead, which is a stronger place
to assert them because it is the bytes a reader receives:

```sh
go test ./internal/webtier/                       # the whole surface
go test ./internal/webtier/ -run 'TestFrozen' -v  # the design freeze, with each ratio printed
```

| Property | Test |
| --- | --- |
| the token set, the contrast floors, the inline order, the byte ceiling | `frozen_tokens_test.go` |
| the accessibility contract on the served pages, §3.1-§3.8 | `a11y_contract_test.go` |
| the stylesheets and font routes resolve and carry their content types | `assets_test.go` |
| every page and the aggregate artifacts agree | `view_explore_test.go` |
| the islands and their fixtures, and `/riot.txt` | `fixtures_test.go` |

The ratios quoted above are the output of `TestFrozenTokenContrast`, which
recomputes them from the declarations in the served sheets rather than from the
token literals, and which carries one deliberately failing pair as a positive
control so the arithmetic cannot pass by never being reached.

What is no longer asserted: the fonts' `font-display: swap` and licence notices,
and the node-only "no bare element selector" rule. Both were the Node check's, and
nothing in the Go tier replaced them.

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

Still open, and neither one blocks anything now:

4. **`Nav` matchup links and the route table.** The contract's prose spelled the
   matchup route `/matchups/<slug>` while the route table in the same document
   and plan section 6.4 listed `/matchups/<role>`. `Nav.astro` already emitted
   `/matchups/<role>`, which is the shape the route table and the served pages
   used, and the served tier confirms it: `/matchups/mid` answers 200 while
   `/matchups/<slug>` was never a route. There was no code change to make; the
   stale `/matchups/<slug>` spelling is the only artefact that disagrees, it is
   still in `docs/contracts.md`, and `docs/contracts.md` is not writable from this
   workstream. Reported, not fixed.
5. **`astro check` was not runnable.** `@astrojs/check` was never a devDependency
   and running it prompted to install it; `tsc --noEmit` was the available
   substitute and reported no errors. Both went with the tree, so the gap is
   closed by retirement rather than by a decision. The Go tier type-checks the
   templates only where a test exercises the render.
