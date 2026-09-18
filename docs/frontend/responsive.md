# Responsive contract

Normative. The site has four breakpoints and **only one of them changes
structure**. Everything wider than 768px is the same layout at different
densities, which is what keeps the design honest: there is no third and fourth
arrangement to get wrong.

Audience: implementation agents. Values come from `tokens.css`; behaviour is
verified by `mockup/tools/check-mockup.mjs` at 1440 / 768 / 480.

---

## 1. The breakpoints

| Key | Query | Device class | What changes |
| --- | --- | --- | --- |
| - | default (`< 480px`) | phone portrait | single column, compact chrome |
| `phone-landscape` | `min-width: 480px` | phone landscape, small tablet | gutters, up to 2 columns in card grids |
| `tablet` (`--bp-sm`) | `min-width: 768px` | tablet, small laptop | **structure**: filter rail appears, nav collapses to one row, footer becomes columns, tables go inline, `sm` control sizes allowed |
| `laptop` (`--bp-md`) | `min-width: 1024px` | laptop | two-column hero, split-with-rail, wider measures, `roomy` row density |
| `desktop shell` (`--bp-lg`) | `min-width: 1400px` | desktop | shell caps at `--shell-max`; only whitespace grows |

Breakpoints are **documented, not tokenised**: CSS custom properties cannot be
used inside media queries. If you need one in a component, hardcode the number
and name it in a comment.

Mobile-first. Write the narrow rule as the default and add `min-width` upward,
except for the touch floor (§5), which is a `max-width: 767px` override because
it *restores* a size the compact variant removed.

## 2. Horizontal rules

1. **No page ever scrolls horizontally.** Only two things may scroll inside
   themselves: a wide table (`.table-scroll`) and the mobile nav link strip.
2. Every grid child that holds text gets `min-inline-size: 0` - otherwise a long
   champion name or a wide table forces the track wider than the viewport.
3. Grids use `repeat(auto-fit, minmax(<floor>, 1fr))` so they collapse without a
   media query. The media queries only set the floor: `11rem` for the landing
   tile wall, `14rem` for stat rows, `20rem` for card pairs.
4. Prose caps at `--measure` (56rem) and legal pages at `--measure-narrow`
   (40rem). The shell caps at 1400px. A measure is a reading device, not a
   layout device - never stretch a paragraph to fill the shell.

## 3. Per-breakpoint layout

### 3.1 Shell

| | `< 480` | `480+` | `768+` | `1024+` | `1400+` |
| --- | --- | --- | --- | --- | --- |
| gutter | `--space-4` | `--space-5` | `--space-5` | `--space-6` | `--space-6` |
| shell | 100% | 100% | 100% | 100% | max 1400px |
| page padding-block | `--space-5` | `--space-6` | `--space-7` | `--space-8` | `--space-8` |

`.shell` supplies the inline gutter and the max width; a page never re-declares
either.

### 3.2 Navigation

One component, two arrangements. `SiteNav` at `≥ 768px` is a single row: brand,
links, CTA. At `≤ 767px` the bar wraps into **three rows in one sticky band**:

1. row 1 - brand (44px target) and CTA (`margin-inline-start: auto`)
2. row 2 - the link strip, `order: 3`, `flex-basis: 100%`,
   `overflow-x: auto`

The links **scroll sideways** rather than wrapping into a tall stack. A wrapped
list would push the page content below the fold on every route; a horizontal
strip keeps one row of navigation always visible. Every strip link carries the
44px touch floor (both axes) with `justify-content: center`.

The bar is `position: sticky` with a cream→transparent gradient, so content
scrolls *under* it rather than behind a hard edge. Because it is sticky, every
fragment target sets `scroll-margin-block-start` to clear it (`app.css`,
`-- Anchors --`).

### 3.3 Page header

| | `< 768` | `768+` |
| --- | --- | --- |
| eyebrow / h1 / lede | stacked, full width | stacked, capped at `--measure` |
| meta + coverage stamp | below the lede, wrapping | right of the lede where it fits |
| actions | full-width buttons | inline buttons |
| h1 size | `--step-4` floor | `--step-4` up to `--step-5` on the hero |

`PageHeader` renders the `h1` (or an `h2` with `level={2}`, for a specimen
inside the gallery). It never renders two.

### 3.4 Filter bar and the explorer

This is the one place where structure really changes.

| | `< 768` | `768+` |
| --- | --- | --- |
| controls | hidden behind a **"Filters" drawer trigger** | laid out inline, wrapping |
| applied filters | chips row, always visible | chips row, always visible |
| filter summary | `role="status"`, one line | same |
| result table | scrolls inside its container, sticky header | full width, `roomy` density |
| honesty footer | stacked | stacked, wider |

The drawer trigger is the only control visible in the narrow filter area, and it
opens the same controls the wide layout renders inline - one source of truth for
the control set, two presentations. Filters are always reflected in the URL, so
a filtered view survives a rotation, a reload and a paste.

### 3.5 Split with rail

`/gallery` and any future reference page use a contents rail.

| | `< 1024` | `1024+` |
| --- | --- | --- |
| rail | collapsed into the page flow, above the body | `--rail-width` sticky column, left of the body |
| body | full width | `minmax(0, 1fr)` |

`--rail-width: clamp(7rem, 8.5vw, 10rem)`. The rail is `align-items: start` and
sticky below `--nav-height`; it must never be the only route to a section, so it
sits alongside a normal heading hierarchy rather than replacing it.

### 3.6 Footer

| | `< 768` | `768+` |
| --- | --- | --- |
| columns | **single column** | multi-column grid |
| links | 44px rows (`display: inline-flex; min-block-size: var(--touch-target)`) | inline rows, 24px floor |
| legal sentences | stacked, full width | stacked, capped at `--measure` |
| build stamp | last, muted | last, muted |

The footer carries the Riot non-endorsement notice and the contact link on every
route, at every width. It is not collapsible.

## 4. Tables

| | `< 768` | `768+` |
| --- | --- | --- |
| container | `.table-scroll`, `overflow-x: auto` | no inner scroll unless the column set demands it |
| header | sticky on the scroll container | sticky |
| numeric columns | right-aligned, tabular numerals | same |
| density | `--row-y` (`0.5rem`) | `--row-y-roomy` (`0.75rem`) |
| sort control | real `<button>` in `<th>`, ≥ 44×44 | `sm` size allowed, ≥ 24×24 |
| column priority | lowest-priority columns may be omitted; **`n` is never omitted** | all columns |

Two rules that outrank density:

1. **The sample annotation never drops.** If the viewport cannot hold the `n`
   column, drop a metric column instead.
2. **No cell is truncated without a full value.** Truncation is a CSS
   `text-overflow` affordance with the complete string in `title`/accessible
   name, never a data-hiding `slice()`.

## 5. Type, space and touch at each width

- Type is fluid: every `--step-*` is a `clamp()`, so nothing needs a per
  breakpoint font-size. The clamp bounds are chosen so that body text lands at
  15px at 480px and 17px at 1400px.
- Space comes from the one scale; the per-breakpoint table in §3.1 shows where
  the scale is stepped up. Do not invent intermediate padding.
- **`--touch-target: 44px`.** Below 768px, every control and every standalone
  link keeps a 44×44 box. Compact sizes are a desktop density affordance: the
  component may keep its smaller type and padding, but it grows its box in its
  own `max-width: 767px` block. This is deliberately per-component, because a
  global rule cannot out-specify scoped component CSS.

The full list of components with touch floors, and why each was needed, is in
`a11y.md` §5. The general rule for a new component: if you add a variant whose
box is under 44px at its smallest, add the matching floor block in the same
file.

## 6. Motion across widths

Motion is not width-dependent. The only interaction-aware behaviour is the
hover lift, which is a pointer affordance and is not reachable on touch - so
touch users get the same information from the ring and the tint. Under
`prefers-reduced-motion: reduce` the tokens zero the durations and the lift for
every width (`a11y.md` §7).

## 7. Verification

`mockup/tools/check-mockup.mjs` audits each of the seven routes at 1440 / 768 /
480 and asserts, per width:

- no horizontal overflow (document scroll width ≤ viewport + 1px)
- every target meets the floor for that width
- the `h1`, `main`, skip link and landmark set survive at every width
- the page renders to a finite height (catches a collapsed or runaway layout)
- no sample annotation disappears at 480px

Renders are written to `docs/frontend/screenshots/mockups/<route>-{desktop,mobile}.png`
for review. The gallery page exceeds 12000px and is clipped there; that is the
capture cap, not a layout defect.

## 8. Known gaps

1. No real-device testing. Renders come from headless Chrome at three widths.
2. No landscape-phone pass at 480×320 specifically; the 480 audit uses the
   height the content needs.
3. Container queries are not used yet. If a component ever needs to react to its
   *container* rather than the viewport - a stat tile inside a narrow rail, for
   instance - that is the right tool and it must be added as a documented
   decision, not as a fourth layout hack.
