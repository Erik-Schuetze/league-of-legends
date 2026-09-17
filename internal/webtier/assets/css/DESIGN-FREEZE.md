# The frozen design layer

Lane B (design freeze). Two sheets implement it:

| file | job |
| --- | --- |
| `design-tokens.css` | the frozen `:root` — every value in the served visual layer |
| `components.css` | the frozen component layer — rules that apply the tokens |

`assets.go:frozenCSS()` concatenates them and `templates/shell.tmpl` inlines the
result as the **last** `<style>` in `<head>`, after the Astro-scoped chunk.
Load order is part of the contract: a rule here wins a specificity tie against
the served baseline, so the layer never has to out-specify it.

The prose lives here rather than in the sheets because a `<style>` block ships
in every HTML document and a browser discards CSS comments. Keeping the reasons
next to the rules cost **11,884 of 19,646 bytes (60%)** of the layer, and it put
prose naming tokens (`--bg-color`) between `:root{` and `}`, which broke
comment-unaware token parsing. Both problems are gone; the reasons are not.

Authority: `design-tokens.md` §1 (the paste-ready block extracted from the
owner's site) plus plan §7.1 and §7.2. `design-tokens.md` §4 lists the traps.

## What "frozen" means, checkably

`frozen_tokens_test.go` holds the contract:

* `TestFrozenTokenContract` — every token declared in `design-tokens.css` is
  either consumed by a rule in the served sheets, or listed in
  `unconsumedTokens` with a reason. A token cannot be added, dropped, or
  silently stop being used without a test failing.
* `TestFrozenTokenContrast` — every foreground/background pair the served
  sheets actually pair, measured with WCAG 2.x relative luminance.
* `TestFrozenDesignTraps` — §4's traps.
* `TestFrozenLayerIsInlinedLast` — the load order above.
* `TestFrozenLayerStaysLean` — the layer stays inside a byte and comment budget.
  It is inlined into every document, so its bytes are paid on every page view.
  This test is why the prose lives in this file: it fails if a block comment
  grows back past half the layer, if the whole layer passes 16,000 B, or if a
  nested `/*` makes the comment count unbalanced.

Run them with `go test ./internal/webtier/ -run TestFrozen -v`.

`a11y_contract_test.go` holds §3's eight mandatory fixes, each with a
mutation-based positive control that must be rejected.

## Divergence register

Every place the served values differ from §1, and why. A divergence that is not
in this list is drift.

* **`--text-muted`: served `#615f57`, §1 `#666`.** Adopted the served value, on
  §1's own advice: §1 calls `#666` "marginal — prefer darker for small text".
  Measured, `#666` is **4.25:1** on `--bg-color`, i.e. it fails AA on the page
  field, while `#615f57` measures **4.74:1** on the field and **5.36:1** on
  `--surface`. Adopting §1 verbatim would be an accessibility regression, so
  §1's recommendation is followed instead of its literal.
* **`--dur-fast` / `--ease`: §1 has `0.3s` and `ease`.** The served `0.12s` is a
  §7.2 #1 density departure — a 0.3s table-row hover reads as broken in a
  spreadsheet — and is registered as such. Easing is *not* covered by §7.2, so
  §1's literal `ease` is adopted and `--ease-out` becomes an alias of it.
* **`--lh-tight` / `--line-tight`: §1's `1.4` is the prose leading**; the served
  `1.25` is the §7.2 #1 density leading for tables and stat rows. Both are
  declared because they are two different jobs, not one drifting value.
* **`--shell-max` / `--content-max`: §1's `1400px` is the outer shell**, the
  served content column is `1200px`. Widening the column is a layout change that
  belongs to the owner's preview verdict, so `--shell-max` is declared (the
  authoritative value is available to later work) and `--content-max` keeps
  rendering 1200px.
* **`--bp-mobile`: declared and listed in `unconsumedTokens`.** A CSS media query
  cannot take a `var()`, so this token documents the breakpoint for template and
  JS code rather than being a value a rule can use.
* **`--float-amp`: declared and listed in `unconsumedTokens`.** §7.2 #4 permits
  the floating-tile motion on the landing page only. The freeze does **not** add
  motion to any page before the owner has seen the preview: a missing animation
  is not a defect, and adding one is exactly the kind of unrequested change this
  phase exists to prevent.
* **`--ring`: declared and listed in `unconsumedTokens`.** It is a second spelling
  of `--bw-rule` + `--ring-color` for the same 2px ring. The served layer uses
  the outline form, and consuming both invites the two drifting apart.
* **`--bg-light` was renamed `--surface`.** The served baseline sheet at
  `assets/astro/JsonLd.BEq7AnVK.css` already declares `--surface`, so the frozen
  layer keeps that name.

## components.css: why each rule exists

Every rule is one of three things, and names which:

* **(a)** an application of one of the four idioms in `design-tokens.md` §2;
* **(b)** one of the eight mandatory accessibility fixes in §3;
* **(c)** a unification of a value the served markup hard-codes in two places
  (`design-tokens.md` §4: "use the tokens above so the new app cannot mix 8px
  and 12px offsets").

A rule that cannot name one of those three does not belong in a freeze. Nothing
here changes page copy or the information a route carries: it changes how
already-emitted markup renders, which is what makes the preview a preview rather
than a rewrite.

### (c) The motion literals the served markup hard-codes

`a.ds-card:hover` and `.ds-card:hover` both write `6px 6px 0 0` and
`translate(2px,2px)` inline. Naming them is the whole point of §4: 6px is a
deliberate 8px → 6px press offset, and it must not be confused with
`--shadow-offset-sm` by whoever edits it next. Hence `--offset-badge`,
`--offset-press`, `--press-shift` in `components.css`'s own `:root`.

`.ds-card:hover` is then restated from those tokens: same specificity, later in
the cascade, identical pixels.

### (b) A11y 1 — `prefers-reduced-motion`

The baseline sheet carries the `@media (prefers-reduced-motion: reduce)` block,
but it neutralises transition/animation *duration* only. `transition` on
`.ds-card` is therefore already covered; the two things it does not cover are
`scroll-behavior: smooth` and the press transform, so both are stated here as
well rather than assumed.

### (b) A11y 2 — one focus ring for the whole served surface

The baseline declares `:focus-visible`, but the off-screen skip link is the
exception in practice: it is a 1px-tall box parked at `left:-9999px` and
`.fallback-skip` is answered by a `:focus` rule further down the sheet **that
carries no outline at all** (only `background`, `z-index`, `padding`, `top`,
`left`). So the *first* tab stop on every page is the one control that does not
use the shared ring. Restating it with the token ring makes the first tab stop
look like every other tab stop.

### (b) A11y 6 — landmarks and the skip link

The shell already renders `<header>` / `<main>` / `<footer>` / `<nav
aria-label>`. What it does not have is room above the fixed navbar, so the
current literal `padding-top: 4rem` is replaced by the token that names that
distance.

### (b) A11y 3 — `aria-current="page"`

The shell emits it; the served styles answer it with a colour change only, which
is a hue-only signal. Adding the underline makes the current page readable
without colour — the same rule §2 item 3 applies to data.

### (a) Item 1 — square corners are the absence of a declaration

That is exactly the trap §4 warns about: a framework default would silently round
these. Every corner that has a border is therefore zeroed explicitly, so the
square reads as a decision rather than as "nothing set a radius".

The served tier-list routes use the scoped card/badge layer; the **fallback**
layer (which every non-tier route renders through) does not — it is a plain 2px
border with no offset. Those rules give the fallback components the same
identity with no markup change.

The grade badge is the clearest case: it is the element the whole tier list is
about and it currently has **no per-grade rule at all**, so every grade renders
identically.

Two spellings reach that element: the Go `tierBadge` component emits
`grade-splus`, and `fallbackTierBadge` emits `fallback-tier-badge--s-plus`.
Both are kept so the split cannot silently drop the S+ tint again; the
duplication is pinned by `TestFrozenTokenContract`.

The link lists are the landing page's content: on `/` they are the entire body.
They are emitted as `<ul><li><a>`, so the tile can be drawn on the `<a>` without
touching the markup.

### (a) Item 2 — monospace for every number, label and eyebrow

The baseline sets the monospace family on `th`/`td` of the fallback table but
leaves `caption` inheriting the body face, and leaves the numeric cells with the
markup's own alignment. `tabular-nums` is §3 item 4.

### (a) Item 3 — a scale built inside the palette, never hue alone

The fallback matrix on `/champions/*` states availability in a bare cell; the
value carrier is the hatch, so the hatch is the token and the tint is the
reinforcement. Every consumer also carries a sign or a letter.

### (c) Prose

`/about`, the legal pages and `/disclaimer` carry no component hooks at all: they are
`<article>` with `<h1>`/`<h2>`/`<p>`/`<ul>`/`<ol>`/`<code>`. A measured line
length and the shared code treatment are the whole layer they need.

The measure goes on the **text elements** rather than on `<article>`, because
every data route wraps its table in the same `<article>`: constraining the
article would shrink the heatmap and the tier list, which is the opposite of the
intent. This was a real bug during development — `main article { max-width }`
silently narrowed every data table until it was narrowed to
`main article > p/ul/ol/blockquote`.

## Selector budget

No selector in either sheet is more specific than the served baseline it has to
beat, so the layer stays override-able by a single later rule.

## Measured cost

Served per document, 13 route families, `curl` + `gzip -9`, regenerated by
`files/design-preview/css-bytes.py` (raw) and
`files/design-preview/route-sweep.sh` (status and layer proof) and recorded in
`files/design-preview/frozen/cssbytes.txt`:

| | raw | gzip |
| --- | --- | --- |
| base sheet `/_astro/JsonLd.BEq7AnVK.css` (separate, cacheable asset) | 10,645 | 2,973 |
| Astro-scoped chunk, common families (inlined) | 19,301 | 3,144 |
| Astro-scoped chunk, champion families (inlined) | 20,836 | 3,393 |
| **frozen layer (inlined, every route)** | **14,178** | **5,015** |
| CSS total without the freeze | 29,946 | 5,570 |
| CSS total with the freeze | 44,124 | 10,055 |
| champion families with the freeze | 45,659 | 10,299 |

Both columns are the sheets concatenated in load order and gzipped as one
stream, which is how the browser receives them.

Read the raw column as exact and the gzip column as ±0.3%: `gzip -9` stores the
input's filename in the header, so the same bytes report 2,973, 2,981, 2,984 or
2,993 depending on how the tool was invoked. The convention here is Python's
`gzip.compress(bytes, 9)` — no filename, `mtime` 0 — which is reproducible. Four
invocations of one byte-identical asset are recorded in
`files/design-preview/frozen/gzip-header-drift.txt`.

Keeping the reasons next to the rules cost 11,884 B of the 19,646 B the layer
first shipped — 60% of a payload every visitor downloads and no browser reads.
Moving them here cut the layer to 14,178 B raw and the CSS transfer delta from
**+112%** to **+80.5%** (champion routes +78.0%).

The layer is inlined rather than served as a hashed asset so the §3 fixes are
unconditional: a frozen sheet that failed to load would take the focus ring and
the skip-link ring with it, and an accessibility fix that depends on a second
request is not a fix. Serving it as `/_astro/<name>.<hash>.css` would make it
cacheable — it would still win the cascade, because equal-specificity rules
follow document order, and `astroAssetAlias` in `assets.go` already handles a
stale hash — at the cost of that guarantee. Measured, the inlined block costs
4,699–4,822 gzip bytes per document, so caching it would save ~4.7 KB gzip on
every page view after the first. That trade is recorded here for the owner
rather than taken unilaterally, because it changes what `/_astro` serves, which
the per-family cutover depends on.

### Marginal cost, measured the way a browser pays it

Summing separately-gzipped blocks overstates the cost, because the inline
styles arrive inside the HTML document and that document is gzipped as one
stream. The number that reaches a transfer budget is the marginal size of the
frozen block inside the compressed document — re-gzip the served bytes with the
block removed and subtract:

| family | html gzip | without the freeze | marginal | marginal % |
| --- | --- | --- | --- | --- |
| home | 12,103 | 7,281 | 4,822 | +66.2% |
| tier-list-mid | 13,667 | 8,904 | 4,763 | +53.5% |
| matchups-mid | 18,624 | 13,818 | 4,806 | +34.8% |
| about | 14,654 | 9,902 | 4,752 | +48.0% |
| legal-privacy | 13,859 | 9,099 | 4,760 | +52.3% |

Reproduce with
`MARGINAL=1 python3 files/design-preview/css-bytes.py <preview-dir>`; the full
13-family table is appended in `files/design-preview/frozen/cssbytes.txt`.
Uncompressed the block is a flat 14,178 B on every route.
