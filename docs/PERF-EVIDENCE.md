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
| SEO ≥95 | 100 on 8 of 9; **69** on `/champions/ahri/top/` | **FAIL** (1 route) — and it is the only row here that fails for a reason other than weight: **`/champions/ahri/top/` 69 in r1, r2 and r5; `/champions/ahri/mid/` 69 in r3, r4, r6, r7 and r8** (`failingAudits: ["is-crawlable"]` in every one of those rounds, and every other sampled route 100 in every one of them). Which of the two roles goes *noindex* follows the cells the served artifact stores rather than the route, so the failure **moves between the pair instead of closing**: `/champions/ahri/mid/` is a route r1/r2 measured at 100, and r5 measured *it* at 100 while failing the mirror. Compare the first-load row below, which fails for weight — this one fails with every byte under budget. See §6.2 (fix routed to another lane; measured here, not fixed here) |
| LCP ≤2.5 s | worst 1,849 ms | **PASS** (margin 651 ms) |
| CLS ≤0.1 | worst 0.002 | **PASS** |
| TBT ≤200 ms | 0 ms on every route and round | **PASS** |
| INP ≤200 ms | not emitted by Lighthouse in navigation mode | **NOT MEASURED** |
| HTML ≤150 KB uncompressed | worst audited 65.4 KiB; worst of all 1,058 routes 66.8 KiB | **PASS** — of the r1/r2 revisions of those routes. Later rounds measured `/matchups/{mid,top}/` at **164,502 B** (r3/r4) and **2,768,758 B** (r5) on the same tier. **Measured after §11.9 bounded the grid** — worst **75,888 B / 74.1 KiB** (r7, shipped fixture posture, 11 routes) and **65,453 B / 63.9 KiB** (r8, live, 8 routes) — so the row holds on both postures **once the image carrying §11.9 is deployed**; it does not hold on the tier as it runs today. Measured, not projected. See note (b) |
| HTML ≤40 KB gzipped | worst as served on the wire 10.2 KiB (`/tier-list/top/`, Lighthouse); worst across all 1,058 routes 9.5 KiB (`/about/` = 9,731 B). The same page is 9.3 KiB when re-gzipped with zlib defaults, i.e. the origin's own gzip output runs ~8% larger than zlib — the audited on-wire figure is the conservative one | **PASS** |
| islands ≤2/page, both deferred | max **1** island/page, on 10 of 1,058 pages; 0 blocking scripts | **PASS** |
| total first-load ≤300 KB uncompressed | 187.3–199.4 KiB on 4 routes; **429.7 / 447.7 / 939.0 / 1013.3 KiB** on 5 routes | **FAIL** (4 routes) — this row is *measured live*; the lower §11 figures are a **projection**, and this row is **not** superseded by them. Measured (not projected) after §11.9 bounded the grid, on the same tier: worst **245,229 B / 239.5 KiB** (r7, shipped fixture posture, 11 routes) and **239,631 B / 234.0 KiB** (r8, live, 8 routes), i.e. 19.7% under the row — but still **not the edge**, so this row is not closed until §11.8's post-cutover run. See note (a), and note (b) for the two `/matchups/*` routes whose later revisions measured **2,938,099 B** before §11.9 bounded the grid |
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

**Note (b) — `/matchups/*` is a real FAIL on the tier the edge will dial, not a fixture artifact.**
This document's own live r1/r2 run measured `/matchups/{mid,top}/` at 26.4 KiB of HTML and 187.3 KiB
first-load, and that is why the 150 KiB HTML row passes above. **Later measurements of the same route
on the Go tier did not reproduce it**: r3/r4 measured the document at 164,502 B and r5 at
**2,768,758 B** with **2,938,099 B** first-load (the ladder in note (a); §11.9 has the cause). Both
are over the 150 KiB ceiling, and the r5 figure is 9.8× the 300 KB first-load budget — on the live
posture, which is the tier the cutover points at. The cause is a quadratic matrix, not markup: the
grid rendered `pool × pool` cells (r5: 164 champions in the artifact, **1** published cell, 26,896
`<td>`), because the frame was sized by the champion list rather than by the cells the artifact
stores. §11.9 bounds the grid to a window of the pool and measures the result on both postures.

An earlier revision of this note called the local 160.6 KiB figure "posture-dependent… do not chase
them as defects" and told later readers to treat it as a fixture artifact. **The coordinator withdrew
that instruction** once r5 showed the same defect at live scale, and the withdrawal is recorded here
rather than silently dropped: the fixture page *is* the shipped product whenever the tier runs
`LOLSTATS_AGG_FIXTURES=only`, which is the posture the Go tier is deployed with.

**How the defect is now bounded, and what was measured after bounding it** (§11.9): the grid renders a
window of the champions the artifact *stores a cell for*, not its champion list. Measured after that
change on both postures: r7 — the shipped fixture posture, 11 routes, including all five matchup roles —
**10/11 PASS, worst matchup document 74.1 KiB and worst first-load 239.5 KiB**; r8 (live, 8 routes)
**7/8 PASS, `/matchups/mid/` 49,870 B / 214.1 KiB first-load**. The single failing route in both rounds
is `/champions/ahri/mid/` on the SEO row, which is the §6.2 defect, not weight. All of those are Go-tier
measurements; the edge still dials the static tier, so §11.8 remains the measurement that closes the
traffic ceiling.

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

# verify every number in this document against the raw evidence (185 checks; exit 1 on drift)
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
| `docs/evidence/lh-r5-*.json.gz` | 8 raw Lighthouse reports behind §11.9's ladder row and §5's notes: the live round against the port-forward to the pod, taken by another lane. Same format and same "nothing is stripped" rule as the r1/r2 set |
| `docs/evidence/lh-r6-*.json.gz`, `docs/evidence/lh-r7-*.json.gz`, `docs/evidence/lh-r8-*.json.gz` | 27 raw Lighthouse reports behind §5's notes and §11.9: r6/r8 the live posture through a local binary holding the pod's own artifacts, r7 the shipped fixture posture (`LOLSTATS_AGG_FIXTURES=only`, 11 routes, all five matchup roles). **Go-tier rounds, not edge rounds** — see §11.8 for the measurement that closes the traffic ceiling |
| `docs/evidence/lh-summary-r5.json` … `docs/evidence/lh-summary-r8.json` | machine-readable extraction of every figure in §11.9 and §5's notes, regenerated from the `.gz` reports above with `scripts/perf/summarize-lh.mjs` |
| `docs/evidence/axe-*.json`, `docs/evidence/axe-summary.json` | 9 raw axe-core results + summary |
| `docs/evidence/tree-facts.json` | per-route bytes/lang/alt/ids/headings/islands/robots for all 1,058 routes |
| `docs/evidence/island-runtime.json` | island boot evidence, console errors, script inventory |
| `scripts/perf/lighthouse-routes.sh` | Lighthouse runner with the positive control |
| `scripts/perf/extract-lh.mjs` | raw report → §7.4 budget verdicts |
| `scripts/perf/verify-report.mjs` | re-derives every figure in this document from the raw evidence and exits non-zero on any disagreement |
| `scripts/perf/summarize-lh.mjs` | wraps `extract-lh.mjs --json` into the `lh-summary-rN.json` schema (it adds the `file` field each record carries); reproduces the committed `lh-summary-r6.json` byte for byte and `lh-summary-r5.json` byte for byte apart from the `.gz` paths, which is why the later rounds were summarised with it rather than by hand |
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

- **`/matchups/{mid,top}/` were a real FAIL on the deployed tier, and this change did not fix them
  then.** Both routes carry zero images, so §11.2's A/B left them byte-identical (r3 ≡ r4: 164,502 B
  document, 14,919 B gzipped, 1,776 DOM elements, 12 requests — identical in both reports). That
  identity is why this section originally read them as a fixture artifact and told later readers not
  to chase them. **That reading was wrong and the coordinator withdrew it**: the Go tier is deployed
  serving `LOLSTATS_AGG_FIXTURES=only`, so the fixture page is the shipped product, and the r5 round —
  the same tier, now in the live posture — measured `/matchups/mid/` at 2,768,758 B of HTML and
  2,938,099 B first-load. §11.9 fixes it and measures both postures. Nothing here depended on the old
  reading: the four routes §11.2 does fix are fixed by removing images, which is orthogonal.
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
# Round 9 is the next free number, and taking a fresh one matters: r5 is another lane's live round
# against the port-forward and r6-r8 are this lane's post-change rounds (r6/r8 live through a local
# binary holding the pod's artifacts, r7 the shipped fixture posture), all cited as evidence in §5's
# notes and §11.9's tables. Reusing a round number overwrites them.
bash scripts/perf/lighthouse-routes.sh --round 9 --base https://lol.erik-schuetze.dev --out docs/evidence

gzip -9 docs/evidence/lh-r9-*.json                        # archive the raw reports
node scripts/perf/summarize-lh.mjs 9                      # -> docs/evidence/lh-summary-r9.json
node scripts/perf/extract-lh.mjs docs/evidence/lh-r9-*.json.gz   # read the first-load column
node scripts/perf/verify-report.mjs                       # 185 checks at this writing, exit 1 on drift
```

Then read the r9 first-load column and **either** write it into §5's row **or** leave the row FAIL —
whichever the measurement says, with no projection substituted for it. §11.3/§11.4 then become history
rather than evidence, and the r3/r4 reports (and this section's arithmetic) can be pruned in the same
commit. Nothing in `scripts/perf/` needs to change for the cutover: `--base` is the only input that
moves, and every script above is committed and syntax-clean. `summarize-lh.mjs` is the wrapper that
gives the summaries their `file` field; it reproduces the committed `lh-summary-r6.json` byte for
byte, which is why it — rather than a fresh reimplementation of the arithmetic — is the one the
post-cutover run should use.

### 11.9 `/matchups/*` is bounded by a window of the artifact's cells, not by its champion pool

Added 2026-09-18 by the R15 lane, on the coordinator's correction in §11.6. Same lane, same budget row
(HTML ≤150 KB, first-load ≤300 KB), different cause from §11.2's images: this one is markup volume, not
asset volume.

**The ladder, and why it is a ladder.** The same route, on the same tier, measured three different sizes
with no change to the template between them:

| round | posture | tier measured | `/matchups/mid/` document | cells | source |
| --- | --- | --- | --- | --- | --- |
| r1/r2 | live | deployed image, port-forward 18921 | 26.4 KiB | not counted | §4 |
| r3/r4 | fixture (`LOLSTATS_AGG_FIXTURES=only`) | local binary, 18921 | 164,502 B | 784 | §11.3, `lh-r{3,4}-matchups-mid.json.gz` |
| r5 | live | port-forward 18921 | **2,768,758 B** (first-load 2,938,099 B) | 26,896 | `lh-r5-matchups-mid.json.gz` |
| r6 | live | local reconstruction, 18945 | 49,870 B | 4 | `lh-r6-matchups-mid.json.gz` |
| r7 | fixture (the shipped posture) | local binary, 18946 | 75,166 B | 144 | `lh-r7-matchups-mid.json.gz` |
| r8 | live | local reconstruction, 18945 | 49,870 B | 4 | `lh-r8-matchups-mid.json.gz` |

r6-r8 carry this change; r1-r5 do not. **Every row was measured against the Go tier** — r1/r2/r5 through
the coordinator's port-forward to the pod, r3/r4/r6/r7/r8 against a local binary holding the same
artifacts — **none of them through the public edge.** The edge still dials the older static tier, so no
row here is a production traffic measurement; §11.8 is where that measurement lives.

**The cause, verified rather than inferred.** The artifact the pod serves today
(`/agg/v1/p/16.18/EUW/420/all/matchups/mid.json`) lists **164 champions and stores 1 cell**
(`min_cell_n` 100, `suppressed_cells` 521, `generated_at` 2026-09-18T00:17:29Z). The template sized its
frame from that champion list, so it rendered `164 × 164 = 26,896` `<td>` elements — 26,895 of them the
dash for a pair nobody measured — at 99 B each. That is the whole 2.8 MB; the cells are the page.
Two checks separate "the pool did this" from "the markup is fat":

1. **Pruning the pool shrinks the page by exactly the missing cells.** Cutting the live mid artifact's
   champion list to 36 (keeping its 1 stored cell) rendered 784 cells in 180,839 B — the *same per-cell
   cost* as the fixture posture — while the unpruned artifact renders 2,768,758 B. Nothing about the
   markup changed; only the frame did.
2. **Cell cost is posture-invariant.** Measured on both postures by summing the `<td>` markup:
   a dash cell is **99 B**, a published cell **219-225 B** (`n`, a tone class and the `aria-label` that
   spells the pairing out). The `<td class="cell missing" data-n="0" …>` string is byte-identical on
   the live and fixture tiers.

So the 26.4 KiB reading in r1/r2 was not a fixture artifact and not a measurement error: it was an
**earlier artifact revision** whose champion list was small, on the same tier, with the same template.
That is what makes this a budget defect rather than a data wobble — a page whose size is a function of
the artifact's champion list has no size at all.

**The change** (`internal/webtier/view_matchups.go`, `templates/pages/matchups.tmpl`,
`templates/components_heatmap.tmpl`):

| bound | value | why that value |
| --- | --- | --- |
| rows and columns | the champions the artifact's **cells** name (`matrixAxis`), not its champion list | the frame is the published data; a champion with no cell is a link, not a row |
| window | **12** per axis (`DefaultMatrixPer`), `?per=` up to **30** (`MaxMatrixPer`) | 144 cells = 32 KB of cells at 225 B; measured default view 75,166 B on the shipped posture. 20 per axis (400 cells) measured 135,764 B on the fixture corpus and would leave 2,093 B of the 300 KiB first-load row on the heaviest role — see "what did not work" |
| filtered pages | **276** cells (`MaxMatrixCells`), rows trimmed by `matrixFit` | the widest filter the shipped corpus can produce: `?q=a` matches 23 of the mid role's 28 champions, and 23 columns × 12 rows = 276. Verified by sweeping `?q=<a-z>` across all five roles: the widest match anywhere is that 23 |
| the rest of the matrix | a pager (`?page=`, `nojsPager`) plus the champion links for the **whole pool**, and a note that says what was left out | windowing must not make a champion unreachable, and a bounded grid must not read as a complete one |

Three disclosures were added to the note under the grid — how many of the role's pairings the artifact
stores, that the rows and columns are the champions it stores a cell for, and how many of the pool's
links are outside the window — because a windowed matrix with no note is a page that lies about being
complete. `published` was also double-counting: it counted each published pair once per mirror cell, so
it read as twice the artifact's stored pairs.

**Measured, both postures** (raw HTML bytes, `curl --compressed`, document resource only; "before" = the
tree as it stood when r3/r4 were taken, "after" = this change):

| route | fixture before | fixture after | live before (pod) | live after |
| --- | --- | --- | --- | --- |
| `/matchups/mid/` | 171,383 B (784 cells) | **75,166 B** (144) | 2,768,758 B (26,896) | **49,870 B** (4) |
| `/matchups/top/` | 135,291 B (484) | **74,797 B** (144) | 33,881 B (0) | 33,881 B (0) |
| `/matchups/jungle/` | 216,183 B (1,089) | **75,693 B** (144) | 33,923 B (0) | 33,923 B (0) |
| `/matchups/bottom/` | 243,111 B (1,156) | **75,888 B** (144) | 2,100,737 B (20,164) | **68,068 B** (100) |
| `/matchups/support/` | 151,018 B (576) | **74,973 B** (144) | 2,609,770 B (25,281) | **56,527 B** (36) |
| worst shape the URL can ask for | — | 101,564 B (`?per=200`, clamped to 276 cells) | — | 68,068 B |

The fixture column is the shipped product wherever the tier runs `LOLSTATS_AGG_FIXTURES=only`, which is
what `deploy/base/config.yaml:86` sets for the deployment the edge will dial. Worst default view after
the change: **75,888 B (74.1 KiB)** against the 150 KiB row, and roughly 245 KB of first-load against the
300 KB row (74.1 KiB of document + the 165.4 KiB of fonts, CSS and JS that every route carries, §11.3).
The two rows this route was failing are the two rows it now passes with ~1.7-1.9× of margin.

**The cross-check that makes the live column admissible.** The live "after" figures are from a local
binary serving a copy of the pod's own artifacts (`LOLSTATS_AGG_ROOT`, recipe below). Before the change
that copy rendered **byte for byte** what the pod served on all five roles (mid 2,768,758, bottom
2,100,737, support 2,609,770, top 33,881, jungle 33,923), so the posture is the pod's posture and the
after figures are the same measurement repeated with this change applied — not a reconstruction guess.

**Lighthouse, measured on both postures after the change.** r7 is the first round this project has
measured in the posture the deployment actually ships (`LOLSTATS_AGG_FIXTURES=only`), and the first to
sample all five matchup roles; r8 repeats the live posture. Commands and targets in "Reproduce" below.
**Both are Go-tier measurements — a local binary serving the same artifacts — not edge measurements.**
Scores are `perf / a11y / bp / seo`.

| route | r7 fixture: HTML raw | gz | first-load | scores | r7 verdict | r8 live: HTML raw | first-load | scores | r8 verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `/matchups/mid/` | **75,166 B (73.4 KiB)** | 12.1 KiB | **244,507 B (238.8 KiB)** | 99/100/100/100 | **PASS** | 49,870 B (48.7 KiB) | 219,211 B (214.1 KiB) | 99/100/100/100 | **PASS** |
| `/matchups/top/` | 74,797 B (73.0 KiB) | 12.0 KiB | 244,138 B (238.4 KiB) | 99/100/100/100 | **PASS** | 33,881 B (33.1 KiB) | 198,660 B (194.0 KiB) | 99/100/100/100 | **PASS** |
| `/matchups/jungle/` | 75,693 B (73.9 KiB) | 12.1 KiB | 245,034 B (239.3 KiB) | 99/100/100/100 | **PASS** | not sampled | — | — | — |
| `/matchups/bottom/` | 75,888 B (74.1 KiB) | 12.1 KiB | 245,229 B (239.5 KiB) | 99/100/100/100 | **PASS** | not sampled | — | — | — |
| `/matchups/support/` | 74,973 B (73.2 KiB) | 11.9 KiB | 244,314 B (238.6 KiB) | 99/100/100/100 | **PASS** | not sampled | — | — | — |
| `/champions/ahri/mid/` | 34,592 B | 7.6 KiB | 227,882 B | 99/100/100/**69** | **FAIL** (SEO) | 34,660 B | 227,950 B | 99/100/100/**69** | **FAIL** (SEO) |
| all other sampled routes | 42,362-64,345 B | 9.5-11.4 KiB | 207,141-231,036 B | 99/100/100/100 | PASS | 42,390-65,453 B | 207,169-232,144 B | 99/100/100/100 | PASS |

**r7: 10 of 11 routes PASS. r8: 7 of 8 routes PASS.** The only failing route in either round is
`/champions/ahri/mid/`, on the SEO row, `failingAudits: ["is-crawlable"]` — the defect §6.2 escalated and
this lane was told to measure rather than fix. It is not a weight failure and this change did not touch
it. Every matchup role now sits **19.7% under** the 300 KB first-load row (worst 245,229 B) and **50.6%
under** the 150 KB HTML row (worst 75,888 B), on the posture the edge will dial, including the three
roles (`jungle`, `support`, `bottom`) no earlier round had ever sampled.

**Reproduce** (the exact commands behind the numbers above; ports are arbitrary, each server must be
built from this tree):

```bash
go build -o bin/web-fixture ./cmd/webtier                     # fixture posture = shipped posture
LOLSTATS_AGG_FIXTURES=only LOLSTATS_WEB_ADDR=127.0.0.1:18946 ./bin/web-fixture &
curl -s --compressed http://127.0.0.1:18946/matchups/mid/ | wc -c                       # 75166
scripts/perf/lighthouse-routes.sh --round 7 --base http://127.0.0.1:18946

# live posture: the pod's own artifacts, copied out of the cluster (a live run cannot use the
# fixtures tree — see "what did not work" 4). $AGG below is that copy.
LOLSTATS_AGG_ROOT=$AGG LOLSTATS_WEB_ADDR=127.0.0.1:18945 ./bin/web-live &
curl -s --compressed http://127.0.0.1:18945/matchups/mid/ | wc -c                       # 49870
scripts/perf/lighthouse-routes.sh --round 8 --base http://127.0.0.1:18945

# the bound, as a test rather than a comment: fetches 7 shapes and fails on bytes served
go test -count=1 ./internal/webtier/ -run TestServedMatrixFitsThePlanBudgetByMeasurement -v
```

**The bound is enforced by a measurement, not by the comment above it.**
`TestServedMatrixFitsThePlanBudgetByMeasurement` fetches `/matchups/{mid,bottom,top}/` plus
`?q=a`, `?page=2`, `?per=30&page=2` and `?q=riven&page=2` from the fixture tier and fails if any
served document exceeds **100,000 B** (default view) or **150,000 B** (a query that asks for the
widest filter the URL can express). The measured worst of those seven shapes is 101,286 B. The unit
costs the window is built on are measured too, not assumed: **225 B** for a published cell, **99 B**
for a dash cell, **33,881-50,716 B** of page furniture (chrome, links, legend).

**What did not work** (the useful part, and the reason several numbers above are what they are):

1. **A window of 20 per axis (400 cells).** Computed from the first cell measurements, it looked
   affordable. Measured, the heaviest fixture role rendered 135,764 B and its first-load would have
   been 305,107 B against the 300 KiB (307,200 B) row — **2,093 B of headroom on the one role the
   harness's route set does not sample.** That measurement, not taste, is why the default is 12.
2. **`MaxMatrixCells = 240`.** This lane's own test caught it: `matrixFit(23, 12) = 10, want 12` — a
   23-column filter cannot page 12 rows out of 240 cells, so a filtered page would have paged
   differently from the grid it filters. 276 is the smallest value that keeps a complete row, and it
   is also the corpus worst case (see the table above). The failing assertion is the evidence that the
   value is derived rather than chosen.
3. **`Query.Href` cannot address a page of this grid.** It emits `?page=` only when `?per=` is present,
   so page 2's "Previous" link rendered `href=""`. Found by fetching the link and comparing it to the
   page it claimed to be, not by reading the template; `matrixPageHref` builds the href explicitly.
4. **A live-posture local run cannot use the fixtures tree.** `LOLSTATS_AGG_FIXTURES=only` forces the
   demo posture, and a demo tree pointed at a live tier is refused with 503
   (`internal/webtier/artifacts.go`). The honest live posture needs the pod's artifacts copied out
   (`LOLSTATS_AGG_ROOT`), which is what r8 and the live column above did.
5. **Dropping the `data-astro-cid-...` attributes** would have removed ~663 KB from the 2.8 MB page
   (27 B per cell) — the single biggest byte win available on it. **Rejected**: those attributes are
   the frozen layer's CSS selectors, and rewriting 189 + 200 selector sites to save bytes on a route
   the window already fixes is a styling-contract change, not a budget fix.
6. **`published` counted every published pair twice**, once per mirror cell, so the note under the grid
   reported double what the artifact stores. Found by comparing the rendered note against the
   artifact's own stored pairs, and fixed by counting the deduplicated pair set.
7. **Two theories about the r5 measurement were wrong**, and are recorded as wrong: the 2,768,758 B
   page was not a cached copy of a stale artifact (a fresh local fetch of the pod's artifacts
   reproduced it byte for byte) and the fixtures tree had not been overwritten by another lane (its
   sha256 and mtimes were unchanged; the "same timestamp" that suggested it was the demo tree's fixed
   `generated_at` stamp). Both were settled by fetching and hashing, not by argument.
8. **The r1/r2 26.4 KiB reading was never reproduced.** Three later rounds on the same route and tier
   measured 164,502 B (r3/r4), 2,768,758 B (r5) and now 75,166/49,870 B. The number was real when
   measured; it is not a property of the route, which is the whole point of this section.

**What this section does NOT establish.** No row above is a production measurement. Every one was
taken against the Go tier — r7/r8 against a local binary holding the same artifacts, r3/r4 the same,
r1/r2/r5 through the coordinator's port-forward — and the public edge still dials the older static
tier. The traffic ceiling §7.4 states is a *served-bytes* ceiling, so §11.8's post-cutover run against
the real edge remains the measurement that closes it.
