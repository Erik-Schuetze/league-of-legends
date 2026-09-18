# LoL Stats - frontend design guide

This guide is normative. It describes what the LoL Stats frontend looks like and
how it behaves, so that implementation agents can build screens without
re-inventing decisions. Read `tokens.css` beside it: the token file holds the
values, this guide holds the rules.

**Scope.** This directory is a design deliverable, not the product frontend.
`mockup/` is a review build that proves the rules are implementable; it is not
shipped.

| Document | Answers |
| --- | --- |
| `README.md` (this file) | Principles, foundations, page composition, what to do when something is missing |
| `components.md` | Every component: anatomy, API, states, do/don't |
| `a11y.md` | Accessibility contract, verified contrast table, keyboard and motion rules |
| `responsive.md` | Breakpoints and the per-breakpoint layout contract |
| `tokens.css` | The values. Import it; never restate a value |
| `tools/contrast-audit.mjs` | Recomputes the contrast table from `tokens.css` |
| `mockup/` | SvelteKit review build: gallery, landing, explorer, legal stubs |
| `screenshots/reference/` | The owner's captures of the reference design |
| `screenshots/mockups/` | Headless renders of the review build, one per route |
| `../decisions/ADR-012-frontend-design-system.md` | Why the stack and the design system are shaped this way |

---

## 1. The design in one paragraph

The product is a **retro-blue instrument panel on warm paper**. The page sits on
sand (`--c-sand`), a faint 27px grid is printed into it, and content sits on
cream cards (`--c-cream`) that are **hard-edged and appear to float**: a 2px
retro-blue ring with a matching, unblurred blue block offset down-right, no
radius anywhere. Type is monospaced for anything the interface says in its own
voice (headings, labels, numerals) and Inter for prose. Interaction is
signalled by blue; data direction is signalled by two derived hues (deep teal
and rust) plus a glyph and a word, never by colour alone.

The mood is flat, quiet and legible: most of the surface is paper, most of the
contrast comes from type, and the only decoration is the offset shadow and the
grid.

## 2. Principles

1. **Paper first.** Cream and sand carry the screen. Blue is an event, not a
   background. If a screen is more than roughly one-fifth blue, something is
   over-signalled.
2. **The shadow is the elevation, and the shadow is the accent.** Ring and
   offset block share a hexcode. There are exactly two elevations. Do not
   invent a third, do not blur, do not round.
3. **Flat means flat.** No gradients on surfaces, no soft shadows, no 1px grey
   dividers standing in for structure. Structure comes from the ring, from a
   sunken well, or from space.
4. **Numbers are the product.** Statistics are read, compared and copied. That
   means monospaced tabular numerals everywhere a number can appear, right
   alignment on numeric columns, and a visible sample size beside every
   statistic.
5. **Honest by construction.** The design cannot express a statistic it does
   not have. Suppressed, missing, stale and unpublished data have their own
   visible treatments, and no state is ever drawn as `0` or as an empty cell.
6. **Never colour alone.** Measured luminance separation between accent blue,
   teal and rust is ≤1.24:1. Every meaning carried by hue is also carried by a
   glyph, a sign, or a word.
7. **Keyboard is a first-class input.** Every interactive element is reachable,
   visibly focused, and operable without a pointer.
8. **Self-contained renders.** No external requests: no CDN, no remote font,
   no analytics, no third-party embed. Fonts ship with the app.

## 3. Colour

### 3.1 Core palette

| Token | Value | Role |
| --- | --- | --- |
| `--c-ink` | `#0b162a` | Deepest navy. Headings, inverse surfaces, strong rules |
| `--c-blue` | `#1b4bc6` | Retro blue. **Interaction only**: rings, shadows, links, focus, primary fills |
| `--c-sand` | `#efdbbf` | Page background |
| `--c-cream` | `#f1eae0` | Raised surface: cards, tables, nav |
| `--c-graphite` | `#242a2b` | Body text |

### 3.2 The accent rule

Blue means *"you can act on this"* or *"this is the current position"*. It is
never used to mean *"good"*, *"bad"*, *"more"* or *"less"*. A stat that is
merely large stays graphite.

### 3.3 Data semantics

Direction gets its own two hues, derived from the reference palette and
validated in `a11y.md`:

| Token | Value | Meaning |
| --- | --- | --- |
| `--sig-up` | `#0f5f52` | Win, above baseline |
| `--sig-down` | `#9e4a22` | Loss, below baseline |
| `--sig-flat` | `--c-graphite` | At baseline, no direction |
| `--sig-unknown` | `#6f6a60` | Not published, not computed, not comparable |

Each has an 8% tint (`--sig-*-tint`) for row backgrounds. Tinted rows must keep
their text at ≥4.5:1; the audit table in `a11y.md` proves the shipped pairs.

**Mandatory pairing.** Any use of `--sig-up`/`--sig-down` must also carry one
of: a sign (`+`/`−`), an arrow glyph, the words *win*/*loss* (or the column
header's declared direction), or a distinct fill shape in a chart. Colour is
the third cue, never the first.

### 3.4 Tints and lines

`--tint-accent` (7%) marks inert/inset backgrounds and zebra rows;
`--tint-accent-strong` (14%) marks selected rows and active tabs.
`--c-rule` is a decorative hairline only and is explicitly exempt from
contrast minimums, because it never carries meaning alone. `--c-control-border`
(`#6f6a60`) is the border of things you can operate and clears 3:1 on both
surfaces.

## 4. Elevation

```
--shadow-card : 0 0 0 2px var(--c-blue), 8px 8px 0 var(--c-blue)
--shadow-hero : 0 0 0 2px var(--c-blue), 12px 12px 0 var(--c-blue)
```

- Exactly two steps. Card for anything in a flow; hero for the one element a
  page is *about* (a landing hero, a single-KPI focus card). Never two heroes
  on one screen.
- Zero blur, zero spread softening, zero radius. The ring is the border; the
  offset block is the depth.
- Sans shadow is the third, *absence* state: sunken wells and table bodies.
  Absence is not elevation, so it is not a third step.
- **Hover lifts toward the shadow**: box translates `-3px, -3px` while the
  shadow shortens to `--offset-hover` (6px). The element appears to rise; the
  shadow does not grow.
- Never nest two shadowed surfaces directly. A card inside a card uses
  `--shadow-inset`, a 2px `--c-rule` inset line, or `--c-surface-sunken`.

## 5. Typography

| Family | Token | Used for |
| --- | --- | --- |
| JetBrains Mono | `--font-mono` / `--font-heading` | Headings, table headers, numerals, labels, chips, buttons, captions |
| Inter | `--font-body` | Paragraphs, descriptions, help text |
| Montserrat 700 | `--font-brand` | The wordmark and nothing else |

- **Headings are monospaced.** This is the voice of the design; do not switch a
  heading to Inter for a softer look.
- **Sizes are the fluid `--step-*` scale.** Do not write `font-size` in `px`.
- **Numerals are tabular, always.** `font-feature-settings: var(--num-features)`.
  Columns of numbers that jitter are a defect.
- **Labels are uppercase at `--step--2` with `--ls-wide`.** Sentence case
  inside a label breaks the pattern.
- **Measure** is `--measure` (56rem) for prose, `--measure-narrow` (40rem) for
  ledes and dialog bodies. Never run prose to the shell width.
- Line height: `--lh-tight` headings, `--lh-snug` table cells and labels,
  `--lh-body` prose.

## 6. Space and layout

- One scale: `--space-1` … `--space-9`. Every padding, gap and rhythm uses it.
- The **27px grid** (`--grid-cell`) is the base rhythm. It is printed into the
  sand as `--grid-line` at 6% blue and is decorative - never align content to
  it in a way that breaks responsive reflow.
- The **shell** is `--shell-max` (1400px) with `--gutter` at the sides.
- The **nav** is `--nav-height` (4rem) and translucent cream with a gradient
  fade (`--bg-fade-50` → `--bg-fade-0`) under it, so content scrolls beneath
  rather than under a hard bar.
- Full per-breakpoint rules live in `responsive.md`. Summary: 480 / 768 / 1024 /
  1400, and **only 768 changes structure**.

## 7. Motion

Motion is decoration with two jobs: acknowledge a pointer, and let a decorative
tile drift. Nothing else moves.

- `--dur-fast` (120ms) state flips, `--dur-base` (200ms) the lift,
  `--dur-slow` (400ms) drawers and dialogs.
- **Float** (`--float-amp`, one of `--float-dur-a|c`) is for decorative tiles
  only. Never float a control, never float anything a user must click
  accurately.
- Under `prefers-reduced-motion: reduce`, lifts and floats are disabled by the
  token block and stay disabled. No component re-adds motion.
- Never animate a number changing. Values appear; they do not count up.

## 8. Page composition

Every page is the same skeleton, so a reader learns one frame:

```
┌ skip link ────────────────────────────────────────────────┐
│ nav (fixed, cream, gradient fade)                          │
├───────────────────────────────────────────────────────────┤
│ page header: eyebrow · h1 · lede · coverage stamp          │
│   ── callouts, filters or a filter bar ──                  │
│   ── the body: cards, tables, heatmaps ──                  │
│   ── the honesty footer: legend, suppression summary,      │
│      provenance line                                       │
├─ footer: legal sentences · contact · build stamp ──────────┤
└───────────────────────────────────────────────────────────┘
```

Rules:

1. **Each page declares its own coverage.** The coverage stamp
   (`patch`, `region`, `queue`, `bracket`, `generated_at`, `source_window`)
   appears in the page header, not in a settings panel and not only in the
   footer. A reader must never have to guess what population a number covers.
2. **Filters live at the top of the page body**, in a filter bar, and are
   reflected in the URL. An explorer view is a link.
3. **The non-endorsement notice with its disclaimer link is on every page.**
   This is a compliance requirement; place it in the footer.
4. **Honesty footer.** Any page with statistics ends with the legend, the
   suppression summary where cells were withheld, and the provenance line when
   the data is not live.
5. **One hero per page.** Everything else is a card.
6. Legal pages use the narrow measure and no data components.

### 8.1 The URL space

A small set of canonical paths names the page; the partition and the filters live
in the query string. `docs/decisions/ADR-013-url-space.md` holds the reasoning
and the rejected alternatives.

| Path | Page |
| --- | --- |
| `/` | landing |
| `/explore` | the data explorer |
| `/champion/<slug>` | one champion, every role and both directions of matchup |
| `/about` | where the numbers come from |
| `/disclaimer` | the non-endorsement notice, standalone |
| `/legal/terms` | terms |
| `/legal/privacy` | privacy |

| Parameter | Value | Default, and omitted from the URL |
| --- | --- | --- |
| `patch` | a patch that has a published partition | the `patch` of `manifest.latest` |
| `region` | a region code, e.g. `EUW` | the `region` of `manifest.latest` |
| `queue` | a queue id; `420` is Ranked Solo/Duo | the `queue` of `manifest.latest` |
| `bracket` | a `Bracket` | the `bracket` of `manifest.latest` |
| `role` | repeatable; a `Role` | every role |
| `tier` | repeatable; a `Tier` | every tier |
| `sort` | a `DataTable` column key | the view's own default |
| `dir` | `asc` or `desc` | `asc` |

The four partition parameters default to the fields `manifest.latest` reports, so
a bare `/explore` follows the newest published partition rather than a patch
number written into the code.

`patch`, `region`, `queue`, `bracket`, `role`, `tier` and `sort` are never a path
segment. A parameter at its default is left out, so the bare path is the default
view and exactly one URL denotes each view. An unpublished value falls back to
the default and the page states which partitions exist; it is not a 404. The
artifact tree under `/agg` is data, not a route: it carries no chrome and nothing
links to it.

## 9. Component usage rules

The full inventory and per-component specs are in `components.md`. The rules
that decide most questions:

- Reach for the **smallest component that carries the meaning**. A KPI is a
  `StatTile`, not a card containing a chart.
- Any number rendered as a statistic carries a `SampleAnnotation` showing `n`.
  There is no "close enough" exception and no configurable opt-out.
- Any statistic that can be absent is rendered through the *data-honesty
  family* (`components.md` §6) and never as `0`, `-` alone, or a blank cell.
- A lone dash means **not applicable** - the quantity cannot exist for that row -
  and never **unknown**. It always carries a visually-hidden expansion, so a
  screen reader hears the reason rather than punctuation. Unknown is
  `withheld`, plus the minimum that was not met.
- Any list of rows that can be large is a `DataTable` with a sticky header,
  tabular numerals and a declared sort state.
- Every control that filters exposes: a visible label, a value, and a clear
  action. Filter chips are removable individually and all at once.

## 10. Writing style in the interface

- Labels name the thing: *Win rate*, not *Performance*. Units live in the
  header or the annotation, not the cell: `52.4` under `Win rate %`, not
  `52.4%` in every row.
- Sentence case for sentences, uppercase for chrome labels (see §5).
- Say what was withheld: *"412 of 8,905 cells withheld (n < 30)"*. Do not say
  *"some data unavailable"*.
- Never say *live*, *real-time* or *accurate* unless the data is exactly that.
  Say when it was generated and what window it covers.
- Empty states are one sentence of what happened plus one action.
  *"No champions match these filters."* + *Clear filters*.

## 11. When the guide is silent

1. Look for the closest existing component in `components.md` and extend it
   with a documented variant rather than inventing a parallel component.
2. If a value is needed that no token provides, add a token to `tokens.css`
   with a comment explaining the case. Do not inline the value in a component.
3. If a colour pair is new, run `node docs/frontend/tools/contrast-audit.mjs`
   with the pair added and record the result in `a11y.md`.
4. If the answer changes what a screen means (a new data state, a new
   comparison), stop and ask the owner; that is a product decision, not a
   styling one.

## 12. Verification contract

A frontend change is done when:

- `npm run build` and `npm run check` are clean in `docs/frontend/mockup`.
- `node docs/frontend/tools/contrast-audit.mjs` exits 0.
- Every numeric cell in the change carries an `n`.
- Every new interactive element has a visible focus ring and a ≥44×44px
  target at the mobile breakpoint (`a11y.md` §5).
- Renders at 480 / 768 / 1024 / 1400 show no horizontal overflow.
- No request leaves the origin.

## 13. Decisions taken with the owner

Settled on 2026-09-18. These were open in the first review of this guide; each
now has an answer, and changing one is an owner decision rather than a styling
one.

| Subject | Decision |
| --- | --- |
| URL space | Canonical paths with the filter state in query parameters - section 8.1, and `docs/decisions/ADR-013-url-space.md` for the reasoning |
| Win/loss hues | `--sig-up #0f5f52` and `--sig-down #9e4a22` are kept as specified and as measured in `a11y.md` |
| Tier badge encoding | Typographic weight plus an accent tint, as specified in section 5. Six tinted fills are rejected: six saturated fills make tier the loudest thing on the page |
| Wordmark | Stays a placeholder. `docs/compliance.md` and ADR-009 keep the name *LoL Stats*, and no lockup is final |
| Does `/gallery` survive into the product? | No. It is a design artefact: it stays in the review app and is not in the route table |
