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
| HTML ≤40 KB gzipped | worst audited 10.2 KiB (LH) / 8.2 KiB (curl); worst of all routes 7.9 KiB | **PASS** |
| islands ≤2/page, both deferred | max **1** island/page, on 10 of 1,058 pages; 0 blocking scripts | **PASS** |
| total first-load ≤300 KB uncompressed | 187.3–199.4 KiB on 4 routes; **429.7 / 447.7 / 939.0 / 1013.3 KiB** on 5 routes | **FAIL** (4 routes) |
| zero axe serious+critical | 0 on all 9 routes (axe 4.13.0, 63 rules evaluated) | **PASS** |

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
| Largest audited route | `/champions/ahri/mid/` — 66,966 B (65.4 KiB), gzipped 8,446 B |
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
| `docs/evidence/axe-*.json`, `docs/evidence/axe-summary.json` | 9 raw axe-core results + summary |
| `docs/evidence/tree-facts.json` | per-route bytes/lang/alt/ids/headings/islands/robots for all 1,058 routes |
| `docs/evidence/island-runtime.json` | island boot evidence, console errors, script inventory |
| `scripts/perf/lighthouse-routes.sh` | Lighthouse runner with the positive control |
| `scripts/perf/extract-lh.mjs` | raw report → §7.4 budget verdicts |
| `scripts/perf/axe-routes.mjs` | axe-core runner |
| `scripts/perf/tree-facts.mjs` | browserless byte/structure crawler |
| `scripts/perf/island-runtime.mjs` | island boot + console-error check |
