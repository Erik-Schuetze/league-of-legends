# Component inventory and specs

Normative. This is the toolbox implementation agents build from. Every component
listed here exists in `mockup/src/lib/components/`, is rendered with every state
in `/gallery`, and is verified by `mockup/tools/check-mockup.mjs`.

Read `README.md` first for the design language, then this file for the parts.

---

## 1. How to read this file

- **Props** are the component's public surface, in source order. `?` means
  optional. A prop not listed is not public - do not reach into internals.
- **States** use one shared vocabulary, defined in §7.
- **Rule** is the one thing that most often gets done wrong. Where a component
  has more than one, the rules are prose underneath the table row's group.
- A component that is not here does not exist yet. Adding one: follow §9.
- Every component is a **flat box**: `--radius: 0`, a `--ring` outline and one
  hard offset shadow. Nothing in this toolbox is rounded except inline code.

---

## 2. Shell and navigation

| Component | Props | States | Rule |
| --- | --- | --- | --- |
| `SiteNav` | - | desktop row · mobile 3-row wrap | Links scroll sideways below 768px; never wrap into a stack |
| `SiteFooter` | `buildStamp` | desktop columns · mobile single column | Non-endorsement notice and contact link are on **every** route, at **every** width |
| `PageHeader` | `eyebrow`, `title`, `lede`, `meta`, `actions`, `labelledby`, `level`, `<children>` | full · compact | It owns the page `h1`. `level={2}` exists only for a specimen inside `/gallery` |
| `SectionHeader` | `title`, `aside`, `level`, `id` | plain · with aside action | Takes `level` so heading order never skips |
| `Breadcrumbs` | `items` | ≥ 2 levels · single level (hidden) | Last item is not a link; it is the current page |
| `Pagination` | `page`, `pageCount`, `total`, `onchange`, `label` | enabled · disabled edge · single page (hidden) | Always announces total results alongside the page number |

**`PageHeader` anatomy.** eyebrow (uppercase mono, `--ls-wide`) · `h1` ·
lede (`--step-1`, capped at `--measure`) · meta line (patch, region, queue,
bracket as `Chip`s) · actions. The **coverage stamp belongs in the page header**,
not in a settings panel and not only in the footer. A reader must never have to
guess what population a number covers.

**`SiteNav` is sticky, not fixed.** It sits in flow, so page content scrolls
under a cream→transparent gradient rather than behind a hard bar. Because it is
sticky, every fragment target sets `scroll-margin-block-start` to clear it. An
in-page link that lands under the nav is a bug.

---

## 3. Surfaces and overlays

| Component | Props | States | Rule |
| --- | --- | --- | --- |
| `Card` | `as`, `interactive`, `flush`, `labelledby`, `<children>` | static · interactive | Elevation 1 only (`--shadow-card`). Nesting a card in a card is forbidden |
| `Well` | `tone`, `labelledby`, `<children>` | `sunken` · `paper` | Recessed content. Carries no shadow and is never interactive |
| `FloatingTile` | `size`, `float`, `phase`, `sunken`, `label`, `<children>` | static · floating (`a`/`b`/`c`) | **Decorative only.** Never wrap a control or a value a user needs |
| `Callout` | `tone`, `label`, `actions`, `<children>` | `note` · `caution` · `honesty` | The `honesty` tone is reserved for data-limitation statements |
| `Banner` | `tone`, `dismissible`, `onclose`, `<children>` | `info` · `success` · `warning` · `danger` · dismissed | Page-level and rare. Announce `warning`/`danger` as `role="alert"`; `info`/`success` as `role="status"` |
| `Disclosure` | `title`, `open`, `summary`, `<children>` | collapsed · expanded | Use for optional depth. Never hide the coverage stamp or the `n` in one |
| `Tabs` | `items`, `value`, `onchange`, `panel`, `label` | selected · unselected · panel | Tabs switch **views of the same data**, never different filters |
| `Dialog` | `open`, `title`, `description`, `footer`, `<children>` | open · closed | Focus moves in, is trapped, and returns to the trigger. `aria-modal="true"` |
| `Drawer` | `side`, `title`, `open`, `footer`, `<children>` | open · closed | The mobile filter presentation. Same control set as the inline bar |
| `Tooltip` | `text`, `<children>` | hover · focus | Never the only label. A tooltip is a hint, never a value |
| `Popover` | `label`, `ariaLabel`, `align`, `<children>` | open · closed | Trigger carries `aria-expanded`; Escape closes |
| `Toast` | `items`, `ondismiss`, `<children>` | queued · dismissed | `action` is `{ label, href?, onclick? }`; with no `href` it renders a `<button>`, never `href="#"` |

**The two elevations.** There are exactly two: card (`--offset-card`, 8px) and
hero (`--offset-hero`, 12px). Hover travels the box **toward its own shadow**
(`--lift`, 3px) and reduces the offset to `--offset-hover` (6px) - it does not
grow the shadow. Never invent a third offset.

**Overlay dismissal is uniform.** Escape closes the topmost overlay. Dialog and
Drawer also close on their close button and (Dialog) the scrim. Tooltip closes
on blur and pointer leave. Nothing closes only on an outside click, because that
is unreachable by keyboard.

---

## 4. Controls

| Component | Props | States | Rule |
| --- | --- | --- | --- |
| `Button` | `variant`, `size`, `href`, `busy`, `disabled`, `type`, `ariaLabel`, `onclick`, `<children>` | default · hover · focus · active · `busy` · disabled · with `href` | `busy` is not `disabled`: a busy button stays announced, a disabled button drops out of the tab order |
| `IconButton` | `label`, `hint`, `size`, `pressed`, `disabled`, `onclick`, `<children>` | default · `pressed` · disabled | `label` is mandatory and becomes the accessible name. An icon alone is never accepted |
| `Chip` | `label`, `tone`, `size`, `value` | `neutral` · `accent` · `up` · `down` · `unknown` | Read-only metadata. For a *removable* filter use `FilterChip` |
| `FilterChip` | `label`, `value`, `onremove` | applied · hover · focus | Removal is a real button with the filter name in its label ("Remove filter: region EUW") |
| `FilterBar` | `<children>`, `chips`, `summary`, `sticky` | inline (≥ 768) · drawer trigger (< 768) · applied | Controls live in one place and are presented twice; never duplicate the control set |
| `SegmentedControl` | `options`, `value`, `onchange`, `name`, `label`, `size` | selected · unselected · focused | Needs `name`; use for 2–5 mutually exclusive options, not for filters with more |
| `Checkbox` | `checked`, `label`, `hint`, `count`, `name`, `value`, `disabled`, `onchange` | unchecked · checked · indeterminate · disabled | Carries `count` where the option is a facet - a reader should see the size before ticking it |
| `RadioGroup` | `options`, `value`, `onchange`, `name`, `label`, `stacked` | selected · unselected · disabled | Groups get a real `fieldset`/`legend`; never a row of `Button`s pretending |
| `Switch` | `checked`, `label`, `hint`, `onLabel`, `offLabel`, `disabled`, `onchange` | on · off · disabled | Applies **immediately**, does not require a submit. A setting that needs a submit is a checkbox |
| `Select` | `label`, `options`, `value`, `onchange`, `id`, `hint`, `note` | default · open · disabled · with note | The `note` is for a constraint the reader must know ("published patches only") |
| `MultiSelect` | `label`, `options`, `value`, `onchange`, `placeholder` | empty · 1 selected · many selected · all · open | Always show how many are selected and offer *clear all* |
| `Combobox` | `label`, `options`, `value`, `onchange`, `placeholder`, `emptyMessage`, `id` | closed · open · filtered · no matches · selected | Standard `<input>` + listbox; arrows move, Enter selects, Escape closes |
| `SearchField` | `label`, `value`, `placeholder`, `id`, `debounce`, `onsearch` | idle · typing · results · no results | Debounced. Search must never fire per keystroke against a large table |
| `RangeSlider` | `label`, `value`, `min`, `max`, `step`, `format`, `onchange`, `hint` | default · dragging · at bound | Show the current value as text; a slider alone cannot be read accurately |
| `PatchRangePicker` | `available`, `from`, `to`, `onchange` | single patch · range · no data in range | Only offers patches that exist in `manifest.json`; never a free date entry |
| `CopyButton` | `value`, `label`, `variant` | idle · copied · failed | Announce the copy with `role="status"`; keep the original label available to the reader |
| `ExportMenu` | `formats`, `scope`, `filename`, `rowCount`, `onexport`, `footnote` | closed · open · exporting | The `footnote` states **what the export contains** (scope, row count, applied filters, patch). An export that cannot say what it holds is not finished |

**Control sizing.** `md` is the default. `sm` exists for desktop table headers
and dense toolbars. Below 768px every variant restores a 44×44 box in its own
stylesheet (`a11y.md` §5). The label may shrink; the box does not.

**Destructive actions.** The `danger` variant is for reverse-only actions. Any
action that discards work asks inside a `Dialog` naming the thing being
discarded, with the count when there is one.

---

## 5. Data display

| Component | Props | States | Rule |
| --- | --- | --- | --- |
| `DataTable` | `columns`, `rows`, `row`, `caption`, `sort`, `onsort`, `state`, `dense`, `sticky` | `ready` · `loading` · empty (via `EmptyState`) · sorted asc · sorted desc | `state="loading"` renders skeleton rows and marks the table `data-loading="true"`, which is the only way a numeric cell may appear without its `n` - and it is a placeholder, not a value |
| `StatTile` | `label`, `value`, `unit`, `note`, `n`, `minCellN`, `withheld`, `delta`, `loading`, `href` | default · with delta · withheld · loading · linked | A tile is one number. Two numbers are two tiles |
| `DeltaIndicator` | `value`, `unit`, `baseline`, `inverted`, `decimals`, `size` | up · down · flat · unknown | Always renders the sign and an arrow, and is read as "+1.2 points". Never colour alone |
| `BarMeter` | `value`, `label`, `text`, `reference`, `polarity`, `n`, `minCellN`, `withheld`, `size` | default · `high-good` · `low-good` · `neutral` · withheld | `polarity` decides which end is good; the bar never implies good/bad on its own |
| `HeatmapMatrix` | `rows`, `columns`, `pivot`, `spread`, `caption`, `legend`, `format` | value · suppressed · absent | Reads as a real `<table>`. Each cell prints its value; suppressed and absent cells print **text**, not just a fill |
| `TierBadge` | `tier`, `note`, `size` | S+ … D · unranked | The **letter is the encoding**; tint and weight are redundancy. Six tinted fills were rejected as a rainbow |
| `RoleBadge` | `role`, `variant`, `size` | `word` · `glyph` | Uppercase mono, no colour coding - roles are categories, not signals |
| `ChampionLockup` | `name`, `slug`, `id`, `href`, `size`, `published` | linked · static · unpublished | Unpublished champions stay visible and are marked, never silently dropped |
| `SampleAnnotation` | `n`, `minCellN`, `withheld`, `of`, `size` | n shown · under minimum · withheld | Expands `n` to a readable phrase for assistive tech. It is not optional decoration |

### 5.1 The `n` rule

Every statistic carries its sample size. Concretely:

- `StatTile` takes `n` (and `minCellN`); it renders `SampleAnnotation`.
- `BarMeter` and `HeatmapMatrix` cells take it too.
- `DataTable` numeric columns pair with an `n` column. **If the viewport cannot
  hold the `n` column, drop a metric column instead.**
- The only permitted number without an `n` is a placeholder inside a
  `data-loading="true"` region, and that region carries a loading label.

There is no opt-out prop and no "close enough" case.

### 5.2 The lone dash

Only three things may render a dash, and each is a different thing:

| Mark | Means | Where |
| --- | --- | --- |
| `withheld` | the cell exists and is under `min_cell_n` | any data component, always with the expansion |
| `-` | **not applicable**: the quantity cannot exist for this row. Always paired with a visually-hidden reason | `DataTable` cells, `HeatmapMatrix` legend |
| `DeltaIndicator`'s flat glyph | no change against the baseline | `DeltaIndicator` only, `aria-hidden`, with the word in the accessible name |

A dash is never a stand-in for *unknown* and never a zero. If a tile has no
value, do not render the tile: render the `EmptyState` that explains why. A dash
where an explanation belongs is the bug this rule exists to prevent.

### 5.3 `DataTable` contract

- Real `<table>`, `<caption>` or an adjacent `SectionHeader`, `<th scope="col">`.
- Sortable headers are `<button>` inside `<th>`; the sorted column carries
  `aria-sort="ascending" | "descending"`.
- Numeric columns set `numeric` **and** get tabular numerals. The unit lives in
  the header (`Win rate %`), never repeated in every cell.
- The header is sticky and the container scrolls, so the page never scrolls
  horizontally.
- Loading, empty and error are three distinct states with three distinct
  components: `Skeleton` rows, `EmptyState`, `ErrorState`. A blank table is a
  bug in all three cases.

### 5.4 `HeatmapMatrix` specifics

- The pivot (`pivot` props) is the reference the tint is measured against, and
  the legend states it.
- Tint is a **secondary** read: magnitude is stepped at 8/16/26% of the signal
  hue, and the number is printed in the cell.
- Suppressed cells render the word *withheld* plus a visually-hidden expansion
  ("EUW withheld: fewer games than the minimum"). Absent cells are a different
  state from suppressed and read differently.
- The legend has five entries: well below / near / well above the pivot,
  withheld, and absent.

---

## 6. Data-honesty family

This family is first class, not a utility. The site publishes derived statistics
from a sample, with a minimum cell size, and sometimes has nothing to publish at
all. That is a normal state of the product and it has components.

| Component | Props | States | Rule |
| --- | --- | --- | --- |
| `CoverageStamp` | `patch`, `region`, `queue`, `bracket`, `generatedAt`, `sourceWindow`, `minCellN`, `suppressedCells`, `buildRunId`, `variant` | `stamp` · `card` | Lives in the page header. Every page with statistics carries one |
| `ProvenanceLine` | `source`, `detail`, `variant` | `inline` · `block` | Mandatory whenever the data is not the published production slice (demo, fixture, backfill) |
| `StalenessIndicator` | `generatedAt`, `cadenceDays`, `now`, `variant` | fresh · stale · unknown | Computed against the declared cadence, never against the wall clock alone |
| `SuppressionSummary` | `suppressedCells`, `minCellN`, `publishedCells`, `unpublishedPairs`, `density` | none suppressed · some suppressed · all suppressed | Says the numbers: *"412 of 8,905 cells withheld (n < 30)"*, not "some data unavailable" |
| `NoPublishedData` | `availablePatches`, `requested`, `region`, `queue`, `bracket`, `suggestedHref`, `suggestedLabel` | nothing in the slice · slice exists elsewhere | Offered from `manifest.json` partitions, and always gives one way forward |
| `EmptyState` | `what`, `activeFilters`, `onclear`, `availableCount`, `variant` | `table` · `page` | One sentence of what happened, plus one action. Lists the active filters that caused it |
| `ErrorState` | `title`, `body`, `detail`, `onretry`, `fallbackHref`, `fallbackLabel`, `staleData` | error · error with stale data shown | `staleData` is a distinct state: showing an old number during an outage is allowed **only** with the staleness stated |
| `Skeleton` | `label`, `shape`, `rows` | `rows` · `card` · `text` | Always carries a label; a shimmer with no announced meaning is noise. Steady, not flashing |

**`EmptyState` vs `NoPublishedData`.** They are different facts and must not be
merged:

- `EmptyState` - *the data exists, your filters exclude all of it.* The action is
  to change the filters.
- `NoPublishedData` - *there is nothing published for this slice.* The action is
  to go to a slice that has data, named from the manifest.

Rendering "no data" for both is the single most common honesty bug on a
statistics site.

**Order of the honesty footer.** Legend → suppression summary (only where cells
were withheld) → staleness → provenance line (only when the data is not the
production slice). Above 768px it may sit in two columns; the reading order
stays the same.

---

## 7. The state matrix

Every data-bearing component must have a defined presentation for every row in
this table. Where a row does not apply, the component does not need to do
anything, but the *omission must be deliberate*.

| State | Meaning | Presentation |
| --- | --- | --- |
| default | real value in range | value + `SampleAnnotation` |
| loading | value not yet known | `Skeleton` of the component's own shape, labelled, `data-loading="true"` |
| empty | filters exclude everything | `EmptyState` naming `what` and listing `activeFilters` |
| no-published-data | nothing published for the slice | `NoPublishedData` from `manifest.json` partitions |
| suppressed | cell exists but is under `min_cell_n` | the word *withheld* + expansion, never a grey fill alone |
| error | fetch failed | `ErrorState` with retry; `staleData` if an old value is shown |
| stale | value older than the declared cadence | value shown + `StalenessIndicator` |
| narrow viewport | ≤ 767px | structure change (`responsive.md`), **never** a dropped `n` |
| reduced motion | user preference | no float, no lift; values unaffected |
| forced colours | high-contrast mode | ring preserved as `CanvasText`, decoration dropped |

Registered `data-*` hooks used by the checker and by styling. Use these names;
do not invent parallel ones:

| Attribute | Component | Meaning |
| --- | --- | --- |
| `data-loading` | `StatTile`, `DataTable` | values below are placeholders |
| `data-withheld` | `StatTile`, `BarMeter`, `SampleAnnotation` | cell suppressed |
| `data-tone` | `Chip`, `BarMeter`, `HeatmapMatrix` | signal channel |
| `data-size` | `Button`, `Chip`, `IconButton`, `BarMeter`, `SegmentedControl` | density |
| `data-missing` | `HeatmapMatrix` cell | `suppressed` vs `absent` |
| `data-magnitude` | `HeatmapMatrix` cell | 1–3 tint step |

---

## 8. Composition recipes

Assemble screens from these; do not improvise a parallel structure.

**A page with statistics**

```
PageHeader (h1 + lede + meta chips + CoverageStamp)
  Callout tone="honesty"            when the slice needs a caveat
  FilterBar                          sticky, URL-synced
  SectionHeader + DataTable          or StatTile grid, or HeatmapMatrix
  honesty footer: legend → SuppressionSummary → StalenessIndicator → ProvenanceLine
SiteFooter                           non-endorsement + contact + build stamp
```

**A KPI row** - 2–4 `StatTile`s in `repeat(auto-fit, minmax(14rem, 1fr))`.
Never more than four; a fifth number is a table.

**A champion page** - `Breadcrumbs`, `ChampionLockup` in the header, `RoleBadge`
for the selected role, `SegmentedControl` for role or metric, `BarMeter` rows
with `n`, `DeltaIndicator` against the pivot, `HeatmapMatrix` of champion × role,
`ProvenanceLine`, `CoverageStamp`.

**A filter that returns nothing** - keep the `FilterBar` mounted, show
`EmptyState` inside the result region (not on the page), keep the chips visible,
and never clear the URL silently.

**Anything that can be absent** - reach for §6 before reaching for `Callout`.

---

## 9. Not in this toolbox

Deliberately absent, with the reason:

| Not built | Why | If it is needed |
| --- | --- | --- |
| Any chart library wrapper | every visualisation here is a table, a bar or a matrix; a charting runtime would be a second design language | add a documented variant of `BarMeter`/`HeatmapMatrix`, not a library |
| Carousel / auto-rotating anything | auto-advancing content fails 2.2.2 and the honesty rules | a `Tabs` or a list |
| Modal wizard / multi-step form | no multi-step flow exists in a data explorer | ask; that is a product decision |
| Date picker (free) | patches, not dates, are the unit of time in this data | `PatchRangePicker` |
| Toast queue manager | one `Toast` region per page is enough | do not add a global queue without a decision record |
| Icon set | the mockup uses inline SVG per component | vendor a set once, then document it in `README.md` §11 |

Adding a component: name it, give it the props and states above, render it in
`/gallery` with every state, add it to §2–§6 of this file, add the touch floor
if its smallest variant is under 44px, and re-run the checker. A component that
is not in `/gallery` is not finished. `/gallery` here means the review app in
`docs/frontend/mockup`, which is a design artefact and not a product route
(`README.md` §13).
