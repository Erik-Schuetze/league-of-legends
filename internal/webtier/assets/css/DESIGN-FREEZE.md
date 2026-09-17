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
comment-unaware token parsing.

Moving the prose here fixed the parsing trap and cut the layer to 14,178 B — and
most of the gain was then given back. The gate that was supposed to hold it said
"comments are less than half the layer", so the answers grew back to **45.6%**
(6,450 of the block's 14,158 characters, 6,470 B) without ever failing. A gate
that cannot fail is not a gate: this lane's strip removed that prose and 28
provably duplicate declarations, and replaced the share with a ban. The layer is
now **6,777 B**. The numbers are in [Measured cost](#measured-cost), and
`TestFrozenLayerStaysLean` now pins the figure this file states, so the table
below cannot go stale without a failing test.

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
* `TestFrozenLayerStaysLean` — the layer carries **no** comment (a ban, not a
  share: a comment is the one thing in the block that cannot change what any
  property computes to, so its correct budget is zero), the whole layer stays
  under 7,800 B, and this file still states the layer's current size. It is
  inlined into every document, so its bytes are paid on every page view.
* `TestFrozenLayerDeclaresNoRedundantToken` — no declaration in the layer
  repeats a value an earlier sheet already provides while nothing in the layer
  reads it. The four that survive are named with their reasons in
  `redundantByDesign`, so the exception is a claim rather than an omission.
* `TestFrozenLayerWinsTheAriaCurrentTie` — the §3 A11y 3 rule, which is present
  in the file *and was still losing*, actually wins the arbitration (see A11y 3
  below). Nothing in the served markup changes when it does, so only computed
  style or arbitration arithmetic can see this class of defect at all.
* `TestStandaloneFaultFormInlinesFrozenLayer` — the one document the shell does
  not wrap inlines the frozen layer too.

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
  §1's literal `ease` is adopted and `--ease-out` becomes an alias of it, which
  is measurable: with the layer inlined, `--ease-out` computes to `ease`;
  without it, the base sheet's `ease-out` shows through.
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

The underline was mandated before this lane and was **in the file and losing**.
The scoped chunk declares
`.link[data-astro-cid-wpvy4v7s][aria-current=page]{border-bottom-color:var(--accent-color)}`
at specificity (0,3,0), and the same chunk declares
`.panel[data-astro-cid-wpvy4v7s] .link[data-astro-cid-wpvy4v7s]{border-bottom-color:#0000}`
at (0,4,0). Inside the nav's `<details>` panel — which is where the route links
are — the (0,4,0) transparent rule wins, so the fix never rendered. A rule that
is present and still loses is invisible to a grep and invisible to a diff of the
served markup; only computed style or the arbitration arithmetic can see it.

Hence the second selector: `.menu .panel .link[aria-current=page]` is (0,4,0), a
**tie** with the defeater, and the frozen layer is inlined last, so document
order decides and the underline is applied. Measured in Chrome on
`/matchups/top`: the panel link's computed `border-bottom-color` is
`rgba(0, 0, 0, 0)` without the frozen layer in both revisions and
`rgb(27, 75, 198)` with it after this change. The freeze's whole contribution to
that route is exactly that one element–property pair.

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

No selector in either sheet out-specifies the served rule it has to beat. One
**ties** it: A11y 3's panel branch above is (0,4,0), against the (0,4,0)
transparent rule that was defeating the mandated underline, and it wins on
document order because the layer is inlined last. Tying rather than beating is
deliberate: it is what keeps the layer overridable by one later rule of the same
weight, which is the property the documented load order exists to provide. The
alternative was leaving a mandated accessibility fix present in the file and not
rendering it, which is the defect this lane was asked to repair.

## What this lane changed

Four measured changes, none of them a redesign:

| # | change | measured before → after |
| --- | --- | --- |
| 1 | the 42 block comments in the two sheets deleted; their reasoning is this file | frozen block 14,178 B → **6,777 B**; the comment text alone was 6,470 B (6,450 of the block's 14,158 characters, 45.6%) |
| 5 | 28 declarations deleted that repeat a value the base sheet already provides and that nothing in the layer reads | 800 B of declaration text (`--print-ink/-paper/-rule` and `--text-muted` kept, see `redundantByDesign`) |
| 2 | A11y 3 extended with `.menu .panel .link[aria-current=page]` so the mandated underline wins its tie | computed `border-bottom-color` on the panel link on `/matchups/top`: `rgba(0, 0, 0, 0)` → **`rgb(27, 75, 198)`**; the layer's total contribution to that route is that one pair |
| 7 | the standalone fault form inlines the frozen layer as its third and last style source | that document 21,352 B → **28,144 B** raw (4,183 → 5,808 gzip); ablating the layer there changes **1,469** computed pairs — 1,426 custom-property pairs and 43 rendered-property pairs over 15 elements — and 33 tokens the other two sheets do not define stop resolving (28 of them are consumed by that document; the other five — `--bp-mobile`, `--bw-pre`, `--dur-hover`, `--float-amp`, `--shell-max` — are declared only there) |

The 7,401 B by which the layer shrank is not simply the 6,470 B of comments plus
the 828 B of duplicate declarations. Reconcile the two sheets separately against
the pre-strip revisions (`diff` of `files/zz-design-impl/before-*.css` against
the served sheets, byte counts not character counts):

| term | bytes |
| --- | --- |
| comment text, both sheets (42 comments) | −6,470 |
| the 28 duplicate declarations, with their lines (`design-tokens.css`) | −828 |
| the lines the comments occupied — **deleted outright, not emptied** (`design-tokens.css` 126, `components.css` 15); 143 B of indentation is what remains once the comment text is subtracted | −143 |
| the A11y 3 selector added to `components.css` (`+67 B` over three lines, less the `27 B` single-selector line it replaced) | +40 |
| **net** | **−7,401** |

The whole-document effect on `/tier-list/top`, which is the route the audit
named: the live capture behind the headline figure
(`files/zz-css-audit/route-tier-list_top.html`, 92,114 B / 92,094 characters)
becomes 84,713 B with the post-strip block in place of the old one — **−7,401 B**,
which is the block and nothing else. A locally rendered `/tier-list/top` moves
66,364 → 58,963 B, the same −7,401 B. Both drop the document's gzip by ~3.3 KB
(live capture 15,753 → 12,462 B).

Nothing else in the layer changed, and the evidence for that is computed style
rather than a diff. A headless-Chrome probe snapshots, for every element on the
route, its computed custom properties and a fixed set of rendered properties,
then re-runs the page with each sheet ablated in turn. Between the two revisions
of the layer the per-route snapshot hashes are **identical on 11 of the 13 route
families** — `tier-list/top`'s 478 elements are equal pair for pair, and ablating
the frozen sheet there changes the same **22,854** computed pairs before and
after, a delta of exactly zero. `matchups-top` and `matchups-mid` differ by
exactly one element, the nav link above, and their frozen-layer ablation count
moves by exactly one pair: 65,343 → 65,344. The one column that moves on every
route is the *base*-sheet ablation (6,597 → 21,156 pairs on `tier-list/top`).
That column is a harness artifact rather than a fact about the layer, because
disabling the base sheet hands every token it defines to whichever sheet
declares it next, so the count depends on the full declaration set of the frozen
sheet; no claim in this file rests on it, and the column the claims do rest on —
the frozen layer's own contribution — is stable to within one pair. Harness in
`files/zz-design-impl/probe.py`, comparison in
`files/zz-design-impl/cmp.py`, raw captures in
`files/zz-design-impl/{before,after}/probe-out/`.

## Measured cost

Served per document, 13 route families, regenerated for this lane by
`files/zz-design-impl/bytes.py` over captured documents, following the method of
`files/design-preview/css-bytes.py` (raw) and `files/design-preview/route-sweep.sh`
(status and layer proof):

| | raw | gzip |
| --- | --- | --- |
| base sheet `/_astro/JsonLd.BEq7AnVK.css` (separate, cacheable asset) | 10,645 | 2,973 |
| Astro-scoped chunk, common families (inlined) | 19,301 | 3,144 |
| Astro-scoped chunk, champion families (inlined) | 20,836 | 3,393 |
| frozen layer before this lane's strip (inlined, every route) | 14,178 | 5,015 |
| **frozen layer now (inlined, every route)** | **6,777** | **1,841** |
| CSS total without the freeze | 29,946 | 5,573 |
| CSS total with the freeze, before the strip | 44,124 | 10,195 |
| **CSS total with the freeze, now** | **36,723** | **6,962** |
| champion families with the freeze, before the strip | 45,659 | 10,432 |
| **champion families with the freeze, now** | **38,258** | **7,193** |

Both columns are the sheets concatenated in load order and gzipped as one
stream, which is how the browser receives them.

Read the raw column as exact and the gzip column as ±0.3%: `gzip -9` stores the
input's filename in the header, so the same bytes report 2,973, 2,981, 2,984 or
2,993 depending on how the tool was invoked. The convention here is Python's
`gzip.compress(bytes, 9)` — no filename, `mtime` 0 — which is reproducible. Four
invocations of one byte-identical asset are recorded in
`files/design-preview/frozen/gzip-header-drift.txt`.

The total rows are re-measured in this lane. The previous revision of this file
recorded them as 10,055 and 10,299 gzip; those sit 140 B and 133 B below the
figures above, and I could not reproduce them from the same three sheets under
any of the conventions in the drift note, so they are re-measured here rather
than carried over.

Keeping the reasons next to the rules cost 11,884 B of the 19,646 B the layer
first shipped — 60% of a payload every visitor downloads and no browser reads.
Moving them here cut the layer to 14,178 B and the CSS transfer delta to
**+82.9%** (champion routes +80.2%); the strip in this lane, which removed what
had grown back plus the duplicate declarations, took the layer to 6,777 B and
the transfer delta to **+24.9%** (champion routes +24.3%).

A note on the figure: the block measured 14,158 by a character count and 14,178
by a byte count, because the comments it carried had 16 non-ASCII characters in
them. The budget in `TestFrozenLayerStaysLean` is a byte budget, so the byte
figure is the one stated here, and the post-strip block is ASCII — 6,777 either
way.

The layer is inlined rather than served as a hashed asset so the §3 fixes are
unconditional: a frozen sheet that failed to load would take the focus ring and
the skip-link ring with it, and an accessibility fix that depends on a second
request is not a fix. Serving it as `/_astro/<name>.<hash>.css` would make it
cacheable — it would still win the cascade, because equal-specificity rules
follow document order, and `astroAssetAlias` in `assets.go` already handles a
stale hash — at the cost of that guarantee. Measured, the inlined block now costs
1,597–1,634 gzip bytes per document, so caching it would save ~1.6 KB gzip on
every page view after the first. That trade is recorded here for the owner rather
than taken unilaterally, because it changes what `/_astro` serves, which the
per-family cutover depends on. The strip moves it the wrong way: a 1.6 KB saving
is a thinner reason to take a guarantee away than the 4.7 KB it was before.

### Marginal cost, measured the way a browser pays it

Summing separately-gzipped blocks overstates the cost, because the inline
styles arrive inside the HTML document and that document is gzipped as one
stream. The number that reaches a transfer budget is the marginal size of the
frozen block inside the compressed document — re-gzip the served bytes with the
block removed and subtract:

| family | html gzip | without the freeze | marginal | marginal % |
| --- | --- | --- | --- | --- |
| home | 8,915 | 7,281 | 1,634 | +22.4% |
| tier-list-mid | 10,513 | 8,904 | 1,609 | +18.1% |
| matchups-mid | 15,448 | 13,818 | 1,630 | +11.8% |
| about | 11,507 | 9,887 | 1,620 | +16.4% |
| legal-privacy | 10,723 | 9,099 | 1,624 | +17.8% |
| tier-list-top (the route above) | 10,008 | 8,411 | 1,597 | +19.0% |

Before the strip the same rows read 4,822 / 4,763 / 4,806 / 4,748 / 4,760 /
4,729 marginal gzip (+66.2% / +53.5% / +34.8% / +48.0% / +52.3% / +56.2%). The
11,884 B of prose that never rendered was, on its own, larger than the frozen
layer it had grown into. Reproduce both columns
with `python3 files/zz-design-impl/bytes.py`; the full 13-family table for both
revisions is in `files/zz-design-impl/before-bytes.txt` and
`files/zz-design-impl/after-bytes.txt`. Uncompressed the block is a flat
6,777 B on every route.
