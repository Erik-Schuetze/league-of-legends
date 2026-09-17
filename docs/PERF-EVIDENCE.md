# PERF-EVIDENCE — Go tier measured against the plan.md §7.4 budget

Measured 2026-09-17 17:12–17:21 UTC by an independent measurement agent. Raw reports in
`docs/evidence/`; scripts in `scripts/perf/`.

**This document records measurements. It changes no product behaviour.**

## 1. What was measured, and what was not

| Item | Value |
| --- | --- |
| Tier | Go server-rendered tier (`lolstats-go-web`), reached via `kubectl port-forward svc/lolstats-go-web 18921:80` |
| Origin | `http://127.0.0.1:18921` |
| Pod image measured | `ghcr.io/erik-schuetze/league-of-legends:latest` @ `sha256:d4e136ddda522238ddc1976afb713173b4c7a1a576a80f28edf3a0e150555f5f` |
| Repo state | measurements are of the **deployed image**, not of repo HEAD. HEAD advanced to `8f3a2a1` mid-measurement (see §2) |
| Corpus | 1,058 routes (origin `sitemap.xml`) |
| Audited routes | 9 (the brief's mandated five, plus a second role for the same champion, a second indexable champion role, and a second tier-list/matchup role) |
| Not measured | static Astro/Caddy tier, and production network latency (see §8) |

## 2. Instrument and positive control

A dead port-forward and a missing route are indistinguishable, so every phase began with a
positive control on `/`.

```
$ curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18921/     # 200
$ curl -s http://127.0.0.1:18921/ | grep -o 'data-state="[^"]*"'     # data-state="live"
```

Two instrument failures occurred during this work and both were caught by that control:

1. The first `kubectl port-forward` **never served a single request**: it exited immediately with
   `bind: address already in use` because a sibling agent's forward already held 18921. Round 1
   succeeded only because that foreign forward was alive. When the sibling's shell exited, its
   forward died and two in-flight runs (`tree-facts`, Lighthouse round 2) failed with
   `ECONNREFUSED`. Those results were **discarded and re-run** against a forward this agent owns
   (re-verified: `lsof -nP -iTCP:18921 -sTCP:LISTEN` → this agent's `kubectl`).
2. The deployment rolled over mid-measurement (pods `86645db7bc-*` → `86c6b9fb86-*`, ~17:17 UTC).
   This was checked rather than assumed: the served bytes are **identical** across the rollout —
   `/tier-list/mid/` `Content-Length: 61020` / `Etag: "f1e21a880d9ed10b7a75e40ffac6d24b"` before
   *and* after, and `/` `Content-Length: 35514`. Round 1 and round 2 weigh byte-for-byte the same
   (§4), so no measurement crosses the rollout.

Lighthouse 12.8.2, `formFactor: mobile`, `throttlingMethod: simulate` (Lantern),
`rttMs: 150`, `throughputKbps: 1638.4`, `cpuSlowdownMultiplier: 4`, cold cache by construction
(the CLI launches a fresh Chrome with a temporary profile per run).

**Lab INP is not emitted.** Lighthouse 12.8.2 in navigation mode emits no
`interaction-to-next-paint` audit at all (verified: the audit key is absent from the raw JSON), so
the §7.4 INP ≤200 ms line cannot be graded from these reports. It is **NOT MEASURED**, not passed.

## 3. Posture measured

`data-state="live"` on **all 1,058 routes** (`docs/evidence/tree-facts.json` →
`totals.dataStates: ["live"]`), and `LOLSTATS_AGG_FIXTURES=off` on the running deployment. Sample
window `2026-09-04`–`2026-09-17`, patch 16.18, "cells published from n = 100 games · 511 cells
withheld as too thin".

**Every number below is a live-snapshot measurement. None of it describes a fixtures/preview
posture.**

## 4. Per-route results

Scores are `round1/round2` so the spread is visible. `axe` is serious+critical violations.
HTML and first-load are uncompressed bytes as reported by Lighthouse's `network-requests`
audit; `gz` is the document's on-wire gzipped size. Verdict is against the whole §7.4 row (§5).

| route | perf | a11y | BP | SEO | axe s+c | LCP ms | CLS | TBT ms | HTML KiB | HTML gz KiB | 1st-load KiB | JS KiB | verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `/` | 99/100 | 100/100 | 100/100 | 100/100 | 0 | 1672–1222 | 0.001 | 0 | 34.7 | 7.6 | 195.6 | 0 (0 files) | PASS |
| `/tier-list/mid/` | 100/100 | 100/100 | 100/100 | 100/100 | 0 | 1368–1372 | 0.002 | 0 | 59.6 | 9.8 | **939.0** | 1.9 (2) | **FAIL** (weight) |
| `/tier-list/top/` | 100/100 | 100/100 | 100/100 | 100/100 | 0 | 1369–1387 | 0.002 | 0 | 62.9 | 10.2 | **1013.3** | 1.9 (2) | **FAIL** (weight) |
| `/champions/ahri/mid/` | 100/99 | 100/100 | 100/100 | 100/100 | 0 | 1694–1689 | 0.000 | 0 | 65.4 | 8.5 | **429.7** | 0 (0) | **FAIL** (weight) |
| `/champions/garen/top/` | 100/99 | 100/100 | 100/100 | 100/100 | 0 | 1232–1845 | 0.000 | 0 | 64.3 | 8.5 | **447.7** | 0 (0) | **FAIL** (weight) |
| `/champions/ahri/top/` | 100/100 | 100/100 | 100/100 | **69/69** | 0 | 1219–1231 | 0.000 | 0 | 27.1 | 5.8 | 215.8 | 0 (0) | **FAIL** (SEO) |
| `/matchups/mid/` | 100/99 | 100/100 | 100/100 | 100/100 | 0 | 1238–1849 | 0.002 | 0 | 26.4 | 5.8 | 187.3 | 0 (0) | PASS |
| `/matchups/top/` | 100/100 | 100/100 | 100/100 | 100/100 | 0 | 1225–1228 | 0.002 | 0 | 26.4 | 5.8 | 187.3 | 0 (0) | PASS |
| `/about/` | 100/100 | 100/100 | 100/100 | 100/100 | 0 | 1708–1243 | 0.001 | 0 | 38.5 | 10.1 | 199.4 | 0 (0) | PASS |

**Spread observed over two rounds:** Performance ±1 point, Accessibility/Best-Practices/SEO
±0 points, LCP 3–610 ms, CLS ±0.001, TBT 0 ms, and **first-load transfer byte-identical on all
nine routes** (`939/939`, `1013/1013`, `430/430`, `448/448`, `199/199`, `187/187`). Byte weight
is deterministic; only timings move.

No score fell within 3 points of a threshold, so no re-run was needed to separate signal from
noise: the only score miss is SEO 69 (26 points below), and the weight misses are 43%–238% over
budget. The two rounds are reported anyway because timings do move (LCP by up to 610 ms) and
because the namespace was shared with other agents' pods.

## 5. §7.4 scorecard

| plan §7.4 threshold | Measured | Verdict |
| --- | --- | --- |
| Performance ≥90 | 99–100, all 9 routes × 2 rounds | **PASS** (margin ≥9) |
| Accessibility =100 | 100, all 9 routes × 2 rounds | **PASS** |
| Best Practices ≥95 | 100, all 9 routes × 2 rounds | **PASS** |
| SEO ≥95 | 100 on 8 of 9; **69** on `/champions/ahri/top/` | **FAIL** (1 route) |
| LCP ≤2.5 s | worst 1,849 ms | **PASS** (margin 651 ms) |
| CLS ≤0.1 | worst 0.002 | **PASS** |
| TBT ≤200 ms | 0 ms on every route and round | **PASS** |
| INP ≤200 ms | not emitted by Lighthouse in navigation mode | **NOT MEASURED** |
| HTML ≤150 KB uncompressed | worst audited 65.4 KiB; worst of all 1,058 routes 66.8 KiB | **PASS** |
| HTML ≤40 KB gzipped | worst as served on the wire 10.2 KiB (`/tier-list/top/`, Lighthouse); worst across all 1,058 routes 9.5 KiB (`/about/` = 9,731 B). The same page is 9.3 KiB when re-gzipped with zlib defaults, i.e. the origin's own gzip output runs ~8% larger than zlib — the audited on-wire figure is the conservative one | **PASS** |
| islands ≤2/page, both deferred | max **1** island/page, on 10 of 1,058 pages; 0 blocking scripts | **PASS** |
| total first-load ≤300 KB uncompressed | 187.3–199.4 KiB on 4 routes; **429.7 / 447.7 / 939.0 / 1013.3 KiB** on 5 routes | **FAIL** (4 routes) — this row is *measured live*; the lower §11 figures are a **projection**, and this row is **not** superseded by them. See note (a) |
| zero axe serious+critical | 0 on all 9 routes (axe 4.13.0, 63 rules evaluated) | **PASS** |

**Note (a) — every cell above is a measurement; §11's smaller numbers are arithmetic, not a re-measurement.**
The figures in §5 and §6 come from the live r1/r2 runs in §4, taken through the deployed tier (§9).
§11, a later lane's change, reports first-load figures of 218.9 / 213.6 / 228.7 KiB on the three
affected routes and 222.4–226.3 KiB for the same routes projected from the committed baseline. Those
are a fixture-posture A/B pair plus a subtraction of the image bytes from the r2 reports; **this table
is deliberately not rewritten to them.** The §7.4 first-load budget therefore stands **FAIL** in its
last live measurement (429.7–1013.3 KiB) and is **not known to hold in production** until the
post-cutover run in §11.8 measures the real edge. A projection that replaces a measurement would be a
laundered pass; §11.4 restates this on its own side.

**Note (b) — the HTML row and `/matchups/*` are posture-dependent; do not chase them as defects.**
The 150 KiB HTML ceiling passes live (worst audited 65.4 KiB). The same route renders very differently
in local fixture posture: `/matchups/mid/` is 26.4 KiB of HTML in the committed live run
(`lh-r2-matchups-mid`) and 160.6 KiB against local fixtures. That difference is what puts
`/matchups/{mid,top}/` over the first-load ceiling locally even though **both routes carry zero
images**; §11.3 reports them unchanged before and after §11.2 for that reason. Fixture-posture HTML is
not this budget's failure and not a real defect in the served pages.

**Residual for the design lane (recorded, not actioned): the font payload.**
150.2 KiB over 6 requests, incurred on **every** route, so it is ~78% of the 188–199 KiB image-free
floor and the next reduction has to come from there. It is 6 self-hosted woff2 files in
`internal/webtier/assets/fonts/` — `inter-400/600/700` (35,056 / 36,384 / 36,300 B),
`jetbrains-mono-400/700` (7,368 / 7,484 B) and `montserrat-700` (31,208 B), 153,800 B in total —
declared by `@font-face` in the copied `internal/webtier/assets/astro/…css`. They are same-origin and
compliant (gate check 3 passes); subsetting or dropping a weight is a design-lane call, not this row.

## 6. The two failures, with causes

### 6.1 First-load transfer exceeds 300 KB on every route that shows a champion image

Baseline on every page (fonts + CSS + HTML, no images): **195.6 KiB** at `/`. The two image-bearing
templates then add third-party champion images that are hotlinked from
`https://ddragon.leagueoflegends.com`:

| route | images loaded | image bytes | + fonts | + document | = first load |
| --- | --- | --- | --- | --- | --- |
| `/tier-list/top/` | 26 | 716.6 KiB (transfer 733.3 KiB) | 150.2 KiB | 62.9 KiB | **1013.3 KiB** |
| `/tier-list/mid/` | 26 | 716.6 KiB (transfer 733.3 KiB) | 150.2 KiB | 59.6 KiB | **939.0 KiB** |
| `/champions/garen/top/` | 26 | 222.5 KiB (transfer 239.1 KiB) | 150.2 KiB | 64.3 KiB | **447.7 KiB** |
| `/champions/ahri/mid/` | 22 | 203.4 KiB | 150.2 KiB | 65.4 KiB | **429.7 KiB** |

Excluding third-party images, no route exceeds **222.4 KiB** and the budget would pass everywhere.
The failure is therefore **entirely third-party image weight**, in two compounding parts:

1. **Hotlinking** (`F-P3`): images are fetched from a third-party CDN rather than the origin, so
   they are outside any origin-side byte control and outside the tier's own caching.
2. **Wrong rendition** (`F-P3`): the page asks for `.../img/champion/Diana.png`, a 128×128 PNG of
   24,762 B, and renders it in a **24×24** box (`width="24" height="24"`). That is 16,384 source
   pixels to fill 576 display pixels — about **28× more pixels than the layout uses**, ~717 KiB
   for 26 rows. A 24–48 px asset (or a sprite) would make this budget pass on its own.

`loading="lazy"` and `alt=""` (decorative, name is adjacent text) are already correct; laziness
does not help because these images are inside/near the viewport at 412×915.

### 6.2 SEO 69 on `/champions/ahri/top/` is an intentional `noindex`, not a broken page

The route serves `<meta name="robots" content="noindex,follow">`, so Lighthouse's `is-crawlable`
audit scores 0 (weight 4.04 of 5) and the category lands at 69. The page explains itself:

> "Ahri has no published cell in top in this snapshot, so this page reports nothing rather than an
> estimate. A rate is published only for a cell with at least n = 100 games. Nothing here is
> estimated or back-filled from a thinner sample."

`noindex` is **data-driven**, and was verified as such: `/champions/ahri/mid/` and
`/champions/garen/top/` are `index,follow` and score SEO 100, while `/champions/ahri/{top,jungle,
bottom,support}/` and `/champions/annie/*` are `noindex,follow`. So this is a correct thin-content
policy, and the budget breach is a **grading artefact**: a knowingly-`noindex` page cannot score
95+ on Lighthouse's SEO category.

**Escalation for the coordinator:** either exempt routes the tier itself marks `noindex` from the
SEO threshold (and record that exemption in §7.4), or run the SEO leg only over the 270 indexable
routes. Do not "fix" this by making a no-data page indexable.

## 7. Deterministic facts from the built tree (no browser)

All 1,058 sitemap routes fetched over HTTP; `docs/evidence/tree-facts.json`.

| Fact | Value |
| --- | --- |
| Pages | 1,058 (0 non-HTML, 0 errors), all `data-state="live"` |
| Total HTML | 34,555,778 B |
| Mean HTML | 32,661 B (31.9 KiB); mean gzipped 5,833 B (5.7 KiB) |
| Largest HTML page | `/champions/yorick/top/` — **68,367 B (66.8 KiB)**, gzipped 8,129 B |
| Largest audited route | `/champions/ahri/mid/` — 66,966 B (65.4 KiB); gzipped 8,446 B as served on the wire (Lighthouse), 8,001 B re-gzipped with zlib |
| Total JS | 1,912 B in 2 files, and **only on 10 of 1,058 pages** (the 5 tier-list + 5 patch tier-list routes): `TableIsland...js` 157 B + `preload-helper.DJSjwBkS.js` 1,755 B |
| Blocking scripts | 0 (the single island entry is `type="module"`) |
| Pages missing `lang` | **0** (all 1,058 are `lang="en"`) |
| Images | 11,757 total; **0 without an `alt` attribute**; 260 with `alt=""` (decorative) |
| Duplicate DOM ids | **0 pages** |
| Heading-level skips | **0 pages**; exactly one `<h1>` on every page |
| Islands | max **1** island/page; 10 pages have one — island budget satisfied |
| `meta robots` | `index,follow` 270 · `noindex,follow` **788** |
| Sitemap vs canonical | 1,057 of 1,058 sitemap URLs omit the trailing slash that `rel=canonical` uses, and return **200 with no redirect**; 788 sitemap URLs are self-declared `noindex` |

Island runtime check (`docs/evidence/island-runtime.json`): the tier-list island **does** boot on
this tier — `data-island-booted=true`, `data-island-ready=true`, 26/29 table rows, 0 console
errors. This contradicts the earlier static-tier finding that islands were dead
(`e.init is not a function`); the defect was in the static tier's asset pipeline, not here.

## 8. What this method does not cover

* **Production network latency.** The origin is a localhost port-forward. Timings come from
  Lighthouse's simulated mobile throttling (rtt 150 ms, 1638.4 Kbps, CPU ×4), not from the real
  origin. §7.4's Performance ≥90 rather than ≥95 is justified by the residential uplink; that
  justification is untested here.
* **Lab INP** — not emitted (§2).
* **Lighthouse's 10 manual accessibility audits** (focus order, focus traps, managed focus,
  landmarks, off-screen content, custom control labels/roles). A score of 100 does not cover them.
* **axe `color-contrast` is `incomplete`, not passing**: 17–232 nodes per route, "Element's
  background color could not be determined due to a background gradient" (`.brand`, `header.ds-navbar`).
  Automated tooling cannot resolve these; 0 violations is a weaker claim than "no contrast defects".
* **Third-party image bytes depend on ddragon's CDN** at measurement time; the origin bytes do not.
* **Namespace contention**: the `lolstats` namespace hosted other agents' short-lived pods
  throughout, which is one reason two rounds are reported rather than one.

## 9. Reproduce

```bash
# tooling, deliberately outside package.json (see Needs)
mkdir -p /tmp/a11y-tools && npm --prefix /tmp/a11y-tools install lighthouse puppeteer-core axe-core cheerio

# origin (must outlive the shell that starts it)
kubectl -n lolstats port-forward --address 127.0.0.1 svc/lolstats-go-web 18921:80

export CHROME_PATH="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

# Lighthouse mobile preset, 2 rounds; each script run asserts 200 on / first
bash scripts/perf/lighthouse-routes.sh --round 1 --out docs/evidence
bash scripts/perf/lighthouse-routes.sh --round 2 --out docs/evidence

# the raw reports are committed gzip-compressed (see §10); read them directly, or
gunzip -c docs/evidence/lh-r1-home.json.gz | jq '.categories.performance.score'

# extract every figure in §4/§5 straight from the committed raw reports
node scripts/perf/extract-lh.mjs docs/evidence/lh-r1-*.json.gz
node scripts/perf/extract-lh.mjs --json docs/evidence/lh-r1-*.json.gz > docs/evidence/lh-summary-r1.json

# verify every number in this document against the raw evidence (146 checks; exit 1 on drift)
node scripts/perf/verify-report.mjs

# axe-core, WCAG 2.1 A/AA, 412x915
node scripts/perf/axe-routes.mjs --base http://127.0.0.1:18921 --out docs/evidence \
  / /tier-list/mid/ /tier-list/top/ /champions/ahri/mid/ /champions/ahri/top/ \
  /champions/garen/top/ /matchups/mid/ /matchups/top/ /about/

# deterministic tree facts + island runtime
node scripts/perf/tree-facts.mjs   --base http://127.0.0.1:18921 --out docs/evidence
node scripts/perf/island-runtime.mjs --base http://127.0.0.1:18921 --out docs/evidence \
  /tier-list/mid/ /tier-list/top/ /champions/ahri/mid/ /
```

## 10. Evidence files

| Path | Contents |
| --- | --- |
| `docs/evidence/lh-r1-*.json.gz`, `docs/evidence/lh-r2-*.json.gz` | 18 raw Lighthouse reports (9 routes × 2 rounds), byte-exact and unmodified, committed gzip-compressed because the plain form is 16.3 MiB and would triple this repository (`.git` is 6.3 MiB, largest existing tracked file 158 KiB). Read with `gunzip -c <file>.gz` or pass the `.gz` paths to `extract-lh.mjs`, which decompresses. Nothing is stripped: scores, metrics, audits and `network-requests` are all present. |
| `docs/evidence/lh-summary-r1.json`, `docs/evidence/lh-summary-r2.json` | machine-readable extraction of every figure in §4, regenerated from the `.gz` reports above |
| `docs/evidence/lh-r3-*.json.gz`, `docs/evidence/lh-r4-*.json.gz` | 16 raw Lighthouse reports behind §11: the same-posture before/after pair for the R15 first-load change (r3 pre-change control, r4 with §11.2 applied). Same format and same "nothing is stripped" rule as the r1/r2 set |
| `docs/evidence/lh-summary-r3.json`, `docs/evidence/lh-summary-r4.json` | machine-readable extraction of every figure in §11, regenerated from the `.gz` reports above |
| `docs/evidence/axe-*.json`, `docs/evidence/axe-summary.json` | 9 raw axe-core results + summary |
| `docs/evidence/tree-facts.json` | per-route bytes/lang/alt/ids/headings/islands/robots for all 1,058 routes |
| `docs/evidence/island-runtime.json` | island boot evidence, console errors, script inventory |
| `scripts/perf/lighthouse-routes.sh` | Lighthouse runner with the positive control |
| `scripts/perf/extract-lh.mjs` | raw report → §7.4 budget verdicts |
| `scripts/perf/verify-report.mjs` | re-derives every figure in this document from the raw evidence and exits non-zero on any disagreement |
| `scripts/perf/axe-routes.mjs` | axe-core runner |
| `scripts/perf/tree-facts.mjs` | browserless byte/structure crawler |
| `scripts/perf/island-runtime.mjs` | island boot + console-error check |

## 11. R15: the first-load row, closed without copying Riot art

Added 2026-09-17 by the R15 lane. §1-§10 are the measurement lane's findings. The only edits this lane
has made inside them are **note (a), note (b) and the font residual directly under §5's table** — added
on the coordinator's instruction so no reader can mistake this section's projections for a
measurement — plus the renumbering of the citations in §11.1. **No measured figure in §5 or §6 was
changed.**

§6.1 states the fix for the failing first-load row as "a 24-48 px asset (or a sprite)". Both are Riot
champion art **served from this origin**, and the compliance material forbids that, so the fix had to
come from the render side instead: the first server-rendered payload no longer contains a Riot image
per table row. This section records the rule, the change, what it measures, and what it does not fix.

### 11.1 The compliance rule: rehosting, resizing or spriting is not permitted

`docs/compliance.md` §5, "Before using any Riot asset" → "Required action" (currently lines 431-433;
quoted as it stood when this lane decided on 2026-09-17, before the next day's citation-correction
commit renumbered the section — the wording of the rule itself is unchanged):

> ### 5. Before using any Riot asset
>
> **Required action.** Riot Press Kit and permitted static data only; no champion art, splash art or
> marks beyond that.

`docs/compliance.md` §5, "Status: met" (currently lines 435-445). At the time of this lane's change the
same passage read "the current build has **2378** `<img>` tags on that origin" — that is the
**pre-change** count. The corrected text now carries the post-change 1038, so the compliance record,
§11.3 and §11.7 agree on the figure:

> **Status: met.** The only Riot assets are Data Dragon static data (champion, item, rune and
> summoner-spell names and icons, plus numeric ids) [...] Gate check 2 scans image references in the
> **served** HTML and CSS and confirms every absolute image origin is
> `https://ddragon.leagueoflegends.com`; the captured corpus of 2026-09-18 carries 1038 `<img>` tags
> on that origin and no other absolute image origin at all.

`docs/compliance.md` §5, "Next step (owner)" (currently lines 447-450):

> **Next step (owner).** None. Any future asset needs a Press Kit check and a recorded permission
> before it is added, and gate check 2 will fail the build if it comes from an unpermitted origin.

`docs/data-sources.md:46-47` puts the same boundary on any served copy: Data Dragon is "Riot's
permitted static data / press kit", while Community Dragon is "**Not used.** ... gate check 2 fails any
image origin other than the Data Dragon CDN, so enabling it is a deliberate compliance change rather
than a code tweak".

Enforcement is `scripts/compliance-check.sh` check 2 (`DD_ORIGIN_URL` at line 212, the origin diff at
lines 281-284, cited on 2026-09-17 as lines 255-262 before the file grew): every origin in the served
HTML and CSS is compared against `DD_ORIGIN_URL` and any other absolute image origin fails the build.
What the build fetched at build time was JSON only — `web/scripts/fetch-ddragon.mjs` wrote champion,
item, rune and spell records whose `icon` fields are URL strings; no PNG bytes entered the repository.
(2026-09-18: the Astro tree and that script were deleted after this change landed; the same static data
now ships checked in under `projection/` and `internal/webtier/data/`, embedded by
`internal/webtier/data.go` — `docs/compliance.md` §5.) And nothing in
the tree records a Press Kit permission (`grep -rn "Press Kit" docs/` returns the rule above and
nothing else), which is the permission §5 "Next step (owner)" requires before a new asset may be added.

**Branch taken: 3, and branch 2 is not permitted.** Resizing, re-encoding, spriting or rehosting the
champion PNGs, and any origin-side fetch-and-resize cache (which would also multiply load on Riot's
CDN), are all art this origin would serve without a recorded permission. That is a licence question
worth more than a byte budget, so no asset is copied and no transform is introduced.

### 11.2 The change: no Riot image in the first server-rendered payload

Two elements carried the per-row image, and both renderers had to move together (see §11.5):

| element | was | is |
| --- | --- | --- |
| tier-list row (`tableIsland`, `internal/webtier/templates/components.tmpl:8`) | `<img src="{{ .IconURL }}" alt="" width="24" height="24" loading="lazy" decoding="async">` | removed; the row's name link carries the identity |
| build list key (`dsBuildList`, `components.tmpl:11`) | `<img class="icon" src="{{ .Icon }}" alt="{{ .Name }}" ...>` | `<span class="chip">{{ .Name }}</span>` — the name stays visible text instead of alt text |

`loading="lazy"` was already on both, and §6.1 correctly predicted it would not help: the images sit in
or near the viewport at 412x915, and the harness loads 22-29 of them on the two tier lists. Laziness
was never a lever here, so the element itself is what goes.

### 11.3 Measured, A/B on one posture (`docs/evidence/lh-r3-*` before, `lh-r4-*` after)

Both runs: `LOLSTATS_AGG_FIXTURES=only LOLSTATS_WEB_ADDR=127.0.0.1:18921`, mobile preset, harness
`scripts/perf/lighthouse-routes.sh`, extraction `scripts/perf/extract-lh.mjs`. KiB are the harness's own
first-load column (sum of uncompressed resource sizes).

| route | first load before | images before | first load after | images after | fonts | CSS | JS |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `/tier-list/mid/` | 985.0 KiB | 761.5 KiB (28) | **218.9 KiB** | 0 (0) | 150.2 KiB (6) | 10.4 | 1.9 |
| `/tier-list/top/` | 805.4 KiB | 588.1 KiB (22) | **213.6 KiB** | 0 (0) | 150.2 KiB (6) | 10.4 | 1.9 |
| `/champions/ahri/top/` | 543.8 KiB | 339.5 KiB (18) | **228.7 KiB** | 27.8 KiB (1) | 150.2 KiB (6) | 10.4 | 0.0 |
| `/matchups/mid/` | 326.0 KiB | 0 (0) | 326.0 KiB | 0 (0) | 150.2 KiB (6) | 10.4 | 4.5 |
| `/matchups/top/` | 290.8 KiB | 0 (0) | 290.8 KiB | 0 (0) | 150.2 KiB (6) | 10.4 | 4.5 |
| `/champions/ahri/mid/` | 215.8 KiB | 27.8 KiB (1) | 215.8 KiB | 27.8 KiB (1) | 150.2 KiB (6) | 10.4 | 0.0 |
| `/about/` | 200.1 KiB | 0 (0) | 200.1 KiB | 0 (0) | 150.2 KiB (6) | 10.4 | 0.0 |
| `/` | 195.6 KiB | 0 (0) | 195.6 KiB | 0 (0) | 150.2 KiB (6) | 10.4 | 0.0 |

Every route that had no per-row image is byte-for-byte identical before and after, at every resource
type; the three that had them lose exactly the image bytes and nothing else. The one image that
remains on a champion page is its own portrait (28 KiB), which that route already afforded.

No other §7.4 row moves: performance 98-99 before and after, accessibility **100**, best practices
**100**, SEO 100 except the recorded intentional `noindex` on `/champions/ahri/mid/` (§6.2), LCP worst
2,106 ms, CLS worst 0.002, TBT 0 ms on every route and round.

### 11.4 Projected on the committed baseline: every route clears 300 KB

The failure row in §5 is the live posture (§9: measured through the deployed tier), which needs the
cluster's published artifacts and cannot be reproduced against a local binary. Taking the committed
raw reports (`docs/evidence/lh-r2-*.json.gz`) and removing the ddragon images this change removes —
the image column, nothing else:

| route | r2 first load | ddragon images | projected |
| --- | --- | --- | --- |
| `/tier-list/top/` | 1013.3 KiB | 787.6 KiB (29) | **225.7 KiB** |
| `/tier-list/mid/` | 939.0 KiB | 716.6 KiB (26) | **222.4 KiB** |
| `/champions/garen/top/` | 447.7 KiB | 222.5 KiB (26) | **225.2 KiB** |
| `/champions/ahri/mid/` | 429.7 KiB | 203.4 KiB (22) | **226.3 KiB** |
| `/champions/ahri/top/` | 215.8 KiB | 27.8 KiB (1) | 188.0 KiB |
| `/about/` | 199.4 KiB | 0 (0) | 199.4 KiB |
| `/` | 195.6 KiB | 0 (0) | 195.6 KiB |
| `/matchups/mid/` | 187.3 KiB | 0 (0) | 187.3 KiB |
| `/matchups/top/` | 187.3 KiB | 0 (0) | 187.3 KiB |

Maximum 226.3 KiB against a 300 KiB ceiling, and this reproduces §6.1's own "no route exceeds
222.4 KiB" figure from the same raw reports.

**Read this as arithmetic, not as the budget's verdict.** It does not replace §5 (see note (a) there):
§5's first-load row is the last live measurement and stays **FAIL**, and the budget is not known to
hold in production until the post-cutover run in §11.8 replaces the row with an r5 measurement. What
this section establishes is narrower and sufficient for the decision it was taken for: on these routes
the third-party images were the *whole* of the overage, so the compliant fix addresses all of it.

The residual is the baseline every route shares: fonts (150.2 KiB, 6 requests) plus document plus CSS.
That is ~78% of the 188-199 KiB image-free floor, which leaves roughly 110 KB of headroom under a
300 KiB ceiling and puts the next reduction squarely in the font payload — recorded for the design lane
under §5 note on fonts, not actioned here. The images are no longer part of the problem.

### 11.5 The parity pin is why both renderers moved, and what that cost

`internal/webtier/parity_test.go` requires the Go tier's bytes to equal `web/dist` (the Astro build
output, `.gitignore:24`) for every published route, byte for byte. Changing only the Go template fails
it:

```
--- FAIL: TestRenderParity/tier-list-mid (0.03s)
    parity_test.go:160: render mismatch for tier-list-mid (60183 reference bytes, 55600 rendered bytes)
--- FAIL: TestRenderParity/champions-ahri-top (0.00s)
    parity_test.go:160: render mismatch for champions-ahri-top (44259 reference bytes, 40921 rendered bytes)
```

So the change lands in both renderers and the reference is rebuilt: `web/src/components/TableIsland.astro`
(the row `<img>`), `web/src/components/BuildList.astro` (the key `<img>`), the two matching
definitions in `internal/webtier/templates/components.tmpl`, then `npm run build` in `web/`. With both
sides moved, `go test ./internal/webtier/...` passes unchanged — the pin is untouched, no case was
weakened, and the Astro scope hashes (`data-astro-cid-*`) are stable across the edit, so no other
route's markup changed.

**Since then (2026-09-18), the pin and its reference tree were retired, and that does not change what
the quote above shows.** `web/` was deleted (`65f2983`, its build could not run) and the byte-parity
gate with it (`8e23d67`, `internal/webtier/parity_test.go` is gone), so the two `web/src/...` paths and
`go test -run TestRenderParity` no longer exist to re-run. The failure above was real and reproduced on
2026-09-17 while the tree was present, and the coordinator re-ran the same command against the
still-present reference before the retirement and got `ok`. What survives the retirement is the
coordination requirement itself: the change had to be made in the Go renderer **and** its Astro twin,
and the only reason the twins agree today is that they were changed together. See
`docs/architecture.md` for the retired-toolchain correction.

`web/src/components/BuildList.astro`'s `.icon` rule is now unused. It is left in place deliberately:
removing it moves shared stylesheet bytes on 1,063 pages, and that is a design-lane call, not this row.

### 11.6 What this does not fix, and what did not work

- **`/matchups/mid/` 326.0 KiB and `/matchups/top/` 290.8 KiB are posture-dependent, and are not this
  row, not this change, and not a defect in the served pages.** Both routes carry zero images, and both
  measure identically before and after. In this local fixture posture the matchup board renders 160.6
  KiB of HTML where the committed live run served 26.4 KiB (same route, `lh-r2-matchups-mid`), which is
  what puts the pair over the first-load and 150 KiB HTML ceilings *here*; §5 records those routes at
  187.3 KiB live with the HTML row passing at 65.4 KiB worst. Treat the local figure as a fixture
  artifact: someone reading a fixture report later should not go hunting for an image or a markup
  regression on those two routes, because there is none. This change neither causes nor closes them.
- **Server-side pagination cannot keep the icons.** §7.5 paginates patch archives, not the live tier
  list, and the arithmetic forbids it anyway: §6.1 measures one champion image at 24,762 B, so a
  10-row page would still spend ~240 KiB on images alone against a floor (fonts + document + CSS) of
  188-199 KiB. Dropping from 26 rows to 2 is not a tier list.
- **Client-side lazy loading or a lazy-loader** is excluded by §7.2 (primary content must be
  JavaScript-free) and would trade the weight failure for a correctness failure. The harness confirms
  laziness is not a lever: 22-29 images load with `loading="lazy"` already present.
- **Rehost, resize, sprite, or an origin-side resize cache** is the compliance problem in §11.1; not
  implemented.
- **First attempt inside an isolated copy** replaced the build-list `<img>` with nothing. That lost the
  item names, which only existed as `alt` text, so the shipped change renders the name as a text chip
  instead — a deliberate, visible-information-preserving fix rather than a silent a11y regression.

### 11.7 Reproduce

```bash
# before / after pair, same posture, same harness (r3 = pre-change control, r4 = this change)
LOLSTATS_AGG_FIXTURES=only LOLSTATS_WEB_ADDR=127.0.0.1:18921 ./bin/lolstats-web &
bash scripts/perf/lighthouse-routes.sh --round 3 --base http://127.0.0.1:18921 --out docs/evidence
# apply the change in §11.2 to both renderers, rebuild web/dist, restart, then
bash scripts/perf/lighthouse-routes.sh --round 4 --base http://127.0.0.1:18921 --out docs/evidence

node scripts/perf/extract-lh.mjs docs/evidence/lh-r4-*.json.gz        # the after table
node scripts/perf/verify-report.mjs                                   # §1-§10 figures still agree
```

The r3/r4 raw reports are committed for the same reason r1/r2 are (see §10) and can be pruned once a
cutover measurement supersedes them.

### 11.8 The deferred live run: the exact invocation, post-cutover

**Not run by this lane, and deliberately not bought with a fixture measurement.** The definitive
instrument for a 300 KB *traffic* ceiling is the real edge after the cutover, so the live run is
deferred to the coordinator who schedules it, not dropped. When the edge dials the Go tier, this is
the mechanical invocation — no port-forward, no fixtures, no local binary, just the public origin:

```bash
export CHROME_PATH="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

# BASE URL: https://lol.erik-schuetze.dev  (the site's own canonical origin: every canonical link,
# sitemap.xml and robots.txt entry names it, which compliance gate check 8 asserts)
# Lighthouse mobile preset, 9 routes; the script asserts 200 on / before it starts
bash scripts/perf/lighthouse-routes.sh --round 5 --base https://lol.erik-schuetze.dev --out docs/evidence

# extract every figure from the raw reports, and verify §1-§10 against them
node scripts/perf/extract-lh.mjs --json docs/evidence/lh-r5-*.json.gz > docs/evidence/lh-summary-r5.json
node scripts/perf/extract-lh.mjs docs/evidence/lh-r5-*.json.gz      # read the first-load column
node scripts/perf/verify-report.mjs                                 # 146 checks, exit 1 on drift
```

Then read the r5 first-load column and **either** write it into §5's row **or** leave the row FAIL —
whichever the measurement says, with no projection substituted for it. §11.3/§11.4 then become history
rather than evidence, and the r3/r4 reports (and this section's arithmetic) can be pruned in the same
commit. Nothing in `scripts/perf/` needs to change for the cutover: `--base` is the only input that
moves, and all six scripts are present and syntax-clean at the time of writing.
