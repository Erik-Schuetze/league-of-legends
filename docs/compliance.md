# Compliance

Riot's rules are a launch gate, not a review step. This page is a register, not
an essay: for every checkpoint it records the trigger, the action required, what
is **actually true today**, the artifact that proves it, and what the owner has
to do next. A checkpoint is either satisfied with a recorded artifact or it is
open; there is no "probably fine".

Two facts frame everything below:

- **The site serves real crawled Riot match data**, deliberately, from the owner's
  **development** key behind the password gate - owner decision **D-1**, 2026-09-17.
- **The compliance workstream is waived** - owner decision **D-4**, 2026-09-17; this
  register is the record of what was checked and decided, not an open obligation.

Last reviewed: **2026-09-17**. Next review due: **2026-12-17**.

## How the gate is run

```
make compliance          # or: sh scripts/compliance-check.sh
make compliance-served   # an alias: there is one corpus now, not two
make compliance-negative-control   # one planted violation at a time
make capture-served-pages          # refresh the served corpus from a running tier
make compliance-gnu      # the same gate in a GNU userland, when docker is present
```

`scripts/compliance-check.sh` is a POSIX `sh` script with no network access and
no package manager. **Its corpus is the HTML a running tier served**: `make
compliance` builds `bin/lolstats-web`, starts it on loopback over the checked-in
fixture artifact tree, captures every route its own `/sitemap.xml` names into
`bin/served-pages` with `scripts/capture-served-pages.sh`, and scans that. There
is no build output to produce first, and no Node toolchain involved: the site is
rendered by the Go tier at request time, so what a check has to judge is a
response, not a file on disk. It reads the source tree, and the approved wording
from `internal/webtier/brand.go` and `internal/webtier/site.go` - the constants
themselves, which is why a paraphrase of the notice fails check 6. It prints
`PASS` or `FAIL` per check together with the number of files each scan read, and
exits non-zero if any launch-blocking check fails.

Until 2026-09-18 there were **two** corpora: `web/dist`, the pre-rendered Astro
tree, and `LOLSTATS_SERVED_DIST`, the responses the tier returned. The Astro tree
was deleted - production has served the Go tier since the cutover, and the tree
was dead weight the gate still dragged along - so checks 3, 4, 6, 8 and 11 now
assert over the served responses only. That is the stricter direction, not a
relaxation: `web/dist` carried **no** `<form>` at all, so the half of check 4
about the no-JS path could only have passed vacuously against it.

A build is published in three data states (`demo`, no data, live `riot-match-v5`),
so the gate has to hold in all three. `LOLSTATS_DIST` points the scans at a
different capture instead of the one `make served-pages` just wrote:

```
LOLSTATS_DIST=bin/served-pages-live sh scripts/compliance-check.sh
```

It changes only which files are read, never a rule. Verify every state a release
serves, not just the capture in the checkout.

The file count is not decoration. A check that passes because it scanned nothing
is worse than no check at all, so every scan asserts a floor on how much it read,
and the rating scan additionally fails if it finds **zero** mentions (which would
mean the pattern is wrong, not that the code is clean) and prints the lines it
exempted so a reviewer can see each exemption is a negation in prose. Check 9
proves its pattern by requiring it to match in `internal/contract/contract.go`,
where `puuid` genuinely appears. Check 10 refuses to pass when the fixtures have
been deleted to silence it.

The thirteen checks, in the order the gate runs them:

| # | Check | Fails when |
| --- | --- | --- |
| 0 | The served corpus is present and whole | `bin/served-pages` is missing or holds implausibly few pages, so the later scans would read nothing. It also refuses to run at all (exit 2, not a FAIL) when a page it is about to judge is empty or has no closing `</html>`: a capture that landed while the tier was still being restarted would otherwise report content violations that were really one race. A torn corpus is diagnosed, not scored |
| 1 | No MMR, ELO or rating-like value anywhere | A rating-like identifier, key or column appears in Go, SQL, TS/JS, JSON, HTML or CSS |
| 2 | Only permitted Riot assets | An image reference has an absolute origin other than the Data Dragon CDN |
| 3 | No third-party scripts, embeds or tracking | An executable resource in a built page is not same-origin, or names a tracking service. Amended 2026-09-17: the old "at least one `<script>` per page" floor is gone - a server-rendered page is legitimately script-free. See the amendment below. The served corpus is asserted the same way, and its script-free page count is reported |
| 4 | The free tier is free and ungated | A credential field, auth route, pricing route or paywall appears; or a form is not a no-JS server-side path (`method="get"` with an on-origin `action`); or a named control sits outside a form. Amended 2026-09-17: the old "any `<form>`" rule is gone - the filter bar *is* the no-JS path. See the amendment below. The served corpus is asserted the same way, and every served form must be a GET form on this origin |
| 5 | Verified-site claims are only made when satisfied | A page claims Riot reviewed or endorsed the site, or a `/riot.txt` is published without the token (or vice versa) |
| 6 | The non-endorsement notice is visible, and its wording has not drifted | The frozen sentence is not on `/disclaimer` word for word; a served page states the notice in wording other than the approved constant in `internal/webtier/brand.go`; a served page carries no notice; the renderer stops composing `TrademarkText + " " + NonEndorsementText` into the footer, or the template stops emitting it; or a served page stops linking to `/disclaimer` |
| 7 | The legal pages publish a contact route | Any of the four compliance pages renders with no contact address |
| 8 | The served address is the deployed one | A served page, the sitemap or `robots.txt` carries a reserved placeholder hostname, with or without `LOLSTATS_SITE_URL` set; or the published addresses name more than one origin, or an origin other than `LOLSTATS_SITE_URL`. The tier publishes a stated default instead of a placeholder and refuses a reserved hostname outright, so a misconfigured origin fails a request rather than the deployment |
| 9 | Nothing per-player is published | The artifact schema (`schema/agg.*`) or a served JSON file carries a PUUID, summoner id, account id, Riot id or profile icon id, or the served tree holds a raw-archive path |
| 10 | Committed payloads carry no real player identifier | A fixture identifier is neither the reserved `fixture-` prefix nor the generator's reserved `FIXT` tagline, or the fixtures are deleted |
| 11 | Every served page is a whole document | A served page is truncated, or loses the labelling that discloses its data state |
| 12 | The scan harness cannot mistake its own standard input for a page | A scan that is handed an empty list of paths finds something anyway (grep answering from the runner's stdin), or a scan that is handed a real list finds nothing (a guard that has quietly stopped feeding grep). Both are harness faults that make every scan above report a result it did not earn |

Each `PASS`/`FAIL` line names the number of files or pages the scan read, so a
check that passed vacuously is visible in its own output.

Check 12 exists because the scans have a portable-looking failure mode that only
appears on the CI runner. They hand a NUL-delimited list of paths to
`grep`, and an empty list is treated differently by the two implementations: GNU
`xargs` (Ubuntu, CI) still runs the command once, so `grep -L pattern` executes
with no file operands and reads its own **standard input**; BSD `xargs`/`grep`
(macOS, this machine) read an empty stdin and stay silent. The observed
consequence was a red CI run on a tree that passes locally: two scans over the
live and no-data page lists, both empty because every built page is in the demo
state at build time, reported a phantom `(standard input)` page as "carries no
live banner" and "does not carry 'No sample yet'", and the job exited 2. It is
also the dangerous direction in principle - a pattern that matched the runner's
stdin would have turned a real violation into a pass - so the fix is structural
rather than cosmetic: every scan that reads a list file goes through
`list_grep`, which short-circuits the empty list, and check 12 asserts both halves
of that behaviour. Because a green local run cannot show this class of defect,
`make compliance-gnu` re-runs the same script in a GNU userland
(`debian:12-slim`) when a container runtime is available, and says so when it
skips; it mounts the checkout into the container, so the scans run over the same
served corpus (`bin/served-pages`) under GNU grep as well. There is no second
corpus to carry in: `LOLSTATS_SERVED_DIST` went with the Astro tree, and the
capture path is the gate's default. It runs in CI next to the gate, where the runner's own
GNU userland and the container's non-empty stdin are two different harnesses for
the same script. The half of the control that runs everywhere, including a
checkout with no container runtime, is check 12.

## Amendment: checks 3 and 4, the dynamic-serving amendment

**Amended 2026-09-17, last reviewed 2026-09-17. Reason: the product became a
server-rendered dynamic app, and the plan's original wording would have failed a
correct build. Neither rule was weakened - each was replaced by the invariant it
was standing in for, and both replacements are fail-closed and carry a negative
control.**

The plan wrote checks 3 and 4 for a pre-rendered Astro site, where every page
carries a hydration bundle and no page has a form. The frozen architecture
(`DECISION-dynamic-architecture.md`) replaced that with `lolstats-web`, a Go
tier that renders every route from the published `agg/v1` snapshot at request
time, and both assumptions are false of it:

| Plan wording | What it failed on | Why that is wrong for the tier |
| --- | --- | --- |
| check 3: a built page must carry at least one `<script>` | a page whose `<script>` tag count is 0 | A server-rendered page is legitimately script-free: the served `/champions/ahri` page emits **0** `<script>` tags, and elsewhere the only one is the JSON-LD block. A tag *count* was never the invariant - the invariant is that nothing a page loads can phone home |
| check 4: any `<form>` is a gate | the presence of the `<form>` tag, at all | The filter bar is a `method="get"` form with `action="/tier-list/mid"`, and it is the **no-JS path** for sorting, filtering and paging - the reason every table is complete and readable with JavaScript disabled. Banning the tag would have banned the accessibility mechanism the redesign is built on |

The observed failure was on a mirror of the live served pages, with the
pre-amendment gate (`git show HEAD:scripts/compliance-check.sh`):

```
RESULT: FAIL - 1 launch-blocking violation(s)     # exit 1
FAIL  4. The free tier is genuinely free and ungated: no account, no paywall
      10 <form class="ds-filter-bar ds-print-hidden" action="/patch/16.18/tier-list/mid" method="get" ...>
```

That is one check failing on ten GET forms and nothing else, which is exactly
the "a normal dynamic app is unlaunchable" risk. The amended gate on the same
tree: `RESULT: PASS - 0 launch-blocking violations(s)`, check 3 reporting 480
resource tags across 150 pages, check 4 reporting 10 forms and 0 of 0 named
controls outside a form.

### What replaced the two rules

**Check 3** now asserts the invariant directly: no `<script>`, `<iframe>`,
stylesheet, font preload, module preload, preconnect, `@import` or tracking
service name on any served page may refer to any origin but the one the build
declares for itself (read from the canonical link of the built index, so a
checkout with no site URL configured still knows which absolute references are
its own). The honesty labelling the old floor stood in for is asserted
separately and directly by check 11, which requires every page's banner to match
the data state the page declares. A page with no `<script>` is now simply not a
failure, and it is not treated as evidence of anything either.

**Check 4** keeps the entire original gating pattern - `type="password"`,
`type="email"`, `name="password"`, `name="email"`, sign-in, sign-up, register,
subscribe, pricing and checkout routes, `data-paywall`, "Sign in"/"Sign up" -
and adds three rules in place of the bare-`<form>` alternative:

- **R1** the original gating pattern above, unchanged, so credential fields,
  auth routes, subscription routes and paywalls still fail the build.
- **R2** every form must be a no-JS server-side path: `method="get"` and an
  explicit `action` that is a path on this origin. A form that can change state,
  or that submits off-origin, fails. An action is **required** rather than
  optional so that the destination of every control is checkable in the markup
  instead of being inherited from whatever URL the page was reached by.
- **R3** every *named* control (`<input>`, `<select>`, `<textarea>`, `<button>`
  with a `name`) must sit inside a form. A named control submits a value; if it
  is not in a form it has no server-side path at all. A control with no `name` -
  the islands' own sort and filter widgets - cannot submit anything and is a JS
  enhancement on top of a working page, so it is not this check's subject.

Both amended checks remain fail-closed in the original direction: check 3 still
fails when the scan reads fewer than 100 pages or extracts no resource tag at
all, check 4 still fails when its gating pattern matches less than 2 of the 2
pieces of markup it exists to catch, and both fail when their own controls do not
fire.

### Evidence that the amendment is not a weakening

`make compliance-negative-control` (`scripts/compliance-negative-control.sh`)
copies the served capture (`bin/served-pages`) to a scratch tree, asserts the
unmodified copy **passes**, then plants one violation at a time and asserts the
gate exits non-zero with the expected FAIL text. Last run, on the same corpus the
gate is run against in CI:

```
control 0  PASS  the unmodified scratch copy passes the gate (exit 0) ...
control 1  PASS  googletagmanager                        # third-party script   (check 3)
control 2  PASS  form(s) are not a no-JS server-side path # off-origin POST    (check 4 R2)
control 3  PASS  named control(s) sit outside a form      # dead control       (check 4 R3)
control 4  PASS  gating element(s) found in the served pages # password field  (check 4 R1)
control 5  PASS  a page stripped of every <script> passes
control 6  PASS  live page(s) carry no live banner        # the scan the GNU bug hid (check 11)
control 7  PASS  external resource reference(s) or tracker name(s) found
                        # <script src="https://third-party.example/analytics.js">, an
                        # origin on no denylist anywhere                 (check 3)
control 8  PASS  form(s) are not a no-JS server-side path
                        # a filter-bar-shaped form with a search box and a sort
                        # selector, no method and no action: the control exists and
                        # submits nowhere the server can answer   (check 4 R2)
control 9  PASS  the tier's own no-JS GET filter bar passes (check 4)
RESULT: PASS - 10 negative control(s) held and 0 broken
```

Control 5 is the one that makes the amendment auditable rather than merely
recorded: a page with every `<script>` removed must **pass**, and it does, which
is the property the plan's floor forbade. Controls 7 and 8 are the two the
coordinator asked for in each direction - a page whose *origin* is foreign but
whose hostname is invented (so the tracker-name list cannot be what catches it),
and a form that is shaped exactly like the tier's filter bar while having no
server-side path at all. Control 9 is the opposite direction again: the tier's
real filter bar must pass, so a future tightening cannot quietly reject the page
a reader receives. The gate is run in CI
(`.github/workflows/docker-build.yml`) plain, over the served corpus, and with
the controls, so a future weakening of either rule - or a control that silently
stops planting - fails the build. The same `make` targets also run as the whole
of `.github/workflows/gates.yml` (`Launch gates`), because in the build workflow
they sit after the Go test step: on 2026-09-17 an unrelated parity failure meant
the `verify` job stopped before any of them ran, so the run showed nothing about
the compliance gate either. A launch gate whose result cannot be read while some
other check is red is not evidence, so the two signals are now independent while
still being one definition per gate.

## Amendment 2: the served corpus, and what "interactive" actually means

**Amended 2026-09-17 (the second amendment of that day). Reason: amendment 1 was
written against `web/dist`, which is not the corpus either rule is about, and the
form rule was therefore vacuous - it had nothing to be wrong about. The
replacement asserts the invariant the rule stood in for: the tier's controls are
answered by the server, so they need no JavaScript.**

`web/dist` held 1063 built HTML pages and **zero** `<form>` elements: the sort
and filter bar is rendered by the Go tier, not by the reference tree. So a check
4 that only read `web/dist` would have passed for the wrong reason - it reported
`(0 checked)` - and could not have caught a JS-only control, because the tree it
read has no controls. The same is true in the other direction for check 3: 1038
of the 1063 built pages carry no `<script>` at all, so the old floor was already
failing the *build*, and a served page is no different.

The coordinator measured the live tier directly
(`kubectl -n lolstats port-forward svc/lolstats-go-web 18099:80`, then
`curl -s ... | grep -o '<script' | wc -l`), against the demo build
(`data-state="demo"`):

```
route                          scripts  forms
/                                   1      0
/about                              1      0
/disclaimer                         1      0
/legal/terms                        1      0
/legal/privacy                      1      0
/tier-list/mid                      1      1
/champions/ahri                     0      0     <- the old check 3 failed here
/champions/ahri/mid                 0      0     <- and here
/matchups/mid                       1      0
/patch/16.18/tier-list/mid          1      1     <- the old check 4 failed here
```

Both shapes are legitimate and neither is a defect: `/champions/ahri` is static
content, and the sort/filter controls on a tier list are real server-side forms.
The gate's own capture of the tier's pages (63 pages sampled from the 1067 the
tier's own `/sitemap.xml` advertises, on loopback over the fixture artifact tree)
agrees and is the evidence CI now produces:

```
PASS  the served corpus agrees: all 193 resource tags across 63 served page(s)
      are same-origin, and 59 of them carry no <script> at all
PASS  the served corpus agrees: every one of the 3 form(s) the tier served is a
      GET form on this origin, and every named control is inside one (0 outside)
```

The 3 forms are the filter bars on `/tier-list/<role>/` and
`/patch/<ver>/tier-list/<role>/`; the 59 script-free pages are the champion
pages. A corpus smaller than 8 pages, or one from which the check extracts
nothing, **fails**: a scan that passes because it read nothing is the failure
mode the floors exist for.

### The invariant, stated

The rule that replaced "must contain a form" is a property of the *server*, not
of the markup: **every control the page offers must change the document without
JavaScript.** `scripts/verify-serving.sh` check 4 asserts it directly and
dynamically - it parses the values the served `<select>` elements themselves
offer, requests each one, and requires at least two distinct documents. Live, on
the running tier (`/tier-list/mid/`, unfiltered digest `fbcdb46f93eba14e`):

```
PASS  the filter is a GET form to its own path:
      <form class="ds-filter-bar ds-print-hidden" action="/tier-list/mid" method="get" ...>
PASS  the sort is server-side (4 values of 'sort', 4 distinct documents):
      sort=champion:4288935b946455fc  sort=tier:c0e3d3674345d0da
      sort=n:d6e10e76dac1329a         sort=win_rate:fbcdb46f93eba14e
PASS  the direction is server-side (2 values of 'dir', 2 distinct documents):
      dir=asc:8a15677ec6beb33f  dir=desc:fbcdb46f93eba14e
PASS  the page size is server-side (3 values of 'per', 3 distinct documents):
      per=0:fbcdb46f93eba14e  per=25:ba64c9cc1f8adb59  per=50:1ea03bb3daf98162
PASS  the text filter is server-side (1 value of 'q', 2 distinct documents):
      unfiltered:fbcdb46f93eba14e  q=xerath:ca0e664f928d3a4c
```

Note what this does not trust. The values are read out of the page rather than
hardcoded, so a rename in the template changes what is probed instead of quietly
probing a parameter the tier ignores - an earlier version of this check probed
`?sort=games`, a parameter that does not exist, and passed on the identical
document returned for it. A parameter with one value is only accepted when it
differs from the unfiltered page, which is why the text filter is compared
against the bare page rather than against another filtered one.

### `/riot.txt` - a decision, not a gap

`/riot.txt` returns **404** on the running tier, and that is correct: no Riot
site-verification token is configured, so the file the token would go in is not
offered. A placeholder would be a false claim of verification. The reference
build returns 404 for the same reason.

**Decision: the absence of `/riot.txt` is not a gate.** It is a documented open
operational requirement, owned by the deploy lane, that becomes satisfiable only
after the domain owner starts a production-key application and is issued a token
(see checkpoint 1 and gap 3 below). What the gate does instead is fail-closed on
the two states that *are* code: gate check 5 passes when no token is configured
and no `/riot.txt` is published, and it fails when a `/riot.txt` appears without
a configured token, or when a configured token is not published, or when any page
claims Riot has verified the site while no token is configured. The claim is
gated; the errand is not invented. The tier's own half is proved in code, not by
argument: `internal/webtier/fixtures_test.go` fails if `/riot.txt` is published
without a token and fails if it is not published once
`LOLSTATS_RIOT_VERIFICATION_TOKEN` is set. That assertion moved there on
2026-09-18, from the `parity_test.go` this paragraph used to name - see gap 7.

## Checkpoint register

The seven triggers are from plan section 13. "Status" is the state today, with
the reason, not an aspiration.

### 1. Before any public launch

**Required action.** Terms of Service and Privacy Policy published; the
non-endorsement disclaimer visible; `riot.txt` hosted; the free tier genuinely
free and ungated; no MMR/ELO calculator anywhere; no data-broker behaviour.

**Status: partially met - 5 of 6 satisfied, `riot.txt` pending.**

| Sub-requirement | Status | Reason and evidence |
| --- | --- | --- |
| Terms of Service published | met | `internal/webtier/templates/pages/terms.body.tmpl` is served at `/legal/terms`; the page carries the 13 required sections, from acceptable use to a "Governing law" clause and an explicit Riot non-endorsement section |
| Privacy Policy published | met | `internal/webtier/templates/pages/privacy.body.tmpl` is served at `/legal/privacy` |
| Non-endorsement disclaimer visible | met | `internal/webtier/templates/pages/disclaimer.tmpl` is served at `/disclaimer`; 4 of 4 compliance pages render the frozen sentence verbatim, and every page in the served corpus links to `/disclaimer` |
| `riot.txt` hosted | **pending, and deliberately not a gate** | `astro.config.mjs` publishes `dist/riot.txt` only when `LOLSTATS_RIOT_VERIFICATION_TOKEN` is set, and the tier republishes it the same way. It is unset, so **no `riot.txt` exists and none is offered** - a placeholder would be a false claim, and the live tier returns 404 for it. The token is issued to the domain owner after they start a production-key application, so this is owner action, not code work. Gate check 5 is the gate that fires on a *false* claim, not on the honest absence - see the `/riot.txt` decision in amendment 2 |
| Free tier genuinely free and ungated | met | gate check 4: no password or email field, no sign-in, registration, subscription or checkout route, no paywall in any page of the served corpus; every form the tier serves is a `method="get"` form with an on-origin `action` (3 of them, all filter bars) whose named controls are inside them, and every control it offers provably changes the document without JavaScript (digests in amendment 2) - see the amendments below |
| No MMR/ELO calculator anywhere | met | gate check 1: 1370 files scanned, 4 rating mentions, all 4 exempt negations of the standing prohibition, 0 rating-like identifiers or keys. The count moves as the other workstreams add files; the run in the evidence log, not this number, is the evidence |
| No data-broker behaviour | met | gate check 9: the published artifact schema (`schema/agg.d.ts`, generated from `internal/aggmodel`, and `agg.schema.json`) declares no PUUID and no served JSON file carries one; the served corpus contains no raw-archive path |

**Next step (owner).** None. **Superseded 2026-09-17:** the owner answered plan
question 6 by choosing publication - real crawled Riot data is served from the
**development** key behind the existing password gate (**D-1**) - and **waived**
the compliance workstream (**D-4**). The paragraph below is the position that held
until then, kept as history.

**Next step (owner), as at the time.** Ratify the preview posture and then start the
production-key application. The position the site implements - the preview stays
up, labelled as a preview, and real crawled data is not published until a key is
approved - is recorded in
`docs/decisions/ADR-010-public-preview-posture.md` and in
`docs/data-sources.md`. The ADR is this workstream's interpretation of plan
question 6; it is accepted as a project decision but has not been confirmed by
the product owner, so it is listed as a gap below.

### 2. Before enabling any scraper

**Required action.** `robots.txt` and ToS reviewed and recorded in
`source_toggles` with a `review_due_at` date, plus a documented decision.

**Status: not applicable - no scraper is enabled, and none is implemented.**
`sql/migrations/0001_init.up.sql:117` defines `source_toggles (source, enabled,
decided_by, decided_at, review_due_at, notes)` with `enabled NOT NULL DEFAULT
false` and a partial index `WHERE enabled = true`. **No row is seeded**, so
absence means off; `internal/contract/contract.go` documents that optional
sources are off unless a row enables them, and `internal/store/runs.go` is the
only writer. The design position is in `docs/data-sources.md`.

**Next step (owner).** Answer plan open question 4 - whether scraping is wanted
at all. Until that is answered, the review date below is a reminder to re-decide,
not a plan to enable. The conditions under which it may ever be enabled, and the
targets that are permanently excluded, are recorded in `docs/data-sources.md`.

### 3. Before publishing a new derived dataset or adding a game mode

**Required action.** Confirm Riot has not restricted publication of that data -
Riot polices display, not only API access.

**Status: met for the v1 artifact set; the trigger is live for anything new.**
The frozen route table in `docs/contracts.md` section 1.3 covers a tier list,
champion detail, matchups and the legal pages. Nothing beyond that is published.
`internal/aggmodel/schema.go` and `fixtures/agg/agg.schema.json` are the whole
published shape, and gate checks 1 and 9 re-prove on every run that it carries no
rating-like value and nothing per-player.

**Next step (owner).** Nothing to do until a new dataset or game mode is
proposed. When one is, it needs a written check against Riot's display policy and
an ADR before it ships; re-run `make compliance` afterwards.

### 4. On every Riot policy update

**Required action.** Policies are explicitly amendable; review on a fixed cadence
and record the outcome.

**Status: pending - no review has been run, and the cadence is now set.**

| Review | Date due | Scope | Outcome |
| --- | --- | --- | --- |
| First scheduled policy review | **2026-12-17** | Re-read the General Policies, the LoL policy and the API Terms; diff against the assumptions listed in `docs/data-sources.md`; re-check the no-MMR and no-data-broker clauses; confirm Data Dragon is still permitted static data | not yet run |
| Then quarterly | 2027-03-17, 2027-06-17, ... | As above | - |

Riot's General Policies were last published 2025-05-29 and are amendable at any
time, so a calendar cadence is the only reliable trigger. Two things besides the
calendar force an out-of-band review: any change to the Riot API Terms, and any
notice from Riot. The crawler's User-Agent carries a contact URL, so Riot can
reach the operator without publishing a changelog entry.

**Next step (owner).** Run the 2026-12-17 review and record the outcome in the
table above. If the no-data-broker or no-MMR clause has changed, re-run
`make compliance` and treat a failure as launch-blocking.

### 5. Before using any Riot asset

**Required action.** Riot Press Kit and permitted static data only; no champion
art, splash art or marks beyond that.

**Status: met.** The only Riot assets are Data Dragon static data (champion,
item, rune and summoner-spell names and icons, plus numeric ids), checked into
the repository under `projection/` and embedded at `internal/webtier/data/*.json`
(`internal/webtier/data.go`; `TestEmbeddedProjectionsMatchTheRepository` fails if
the two drift). No build-time fetch remains - `web/scripts/fetch-ddragon.mjs`
went with the Astro tree on 2026-09-18. Gate check 2 scans image references in
the **served** HTML and CSS and confirms every absolute image origin is
`https://ddragon.leagueoflegends.com`; the captured corpus of 2026-09-18 carries
1038 `<img>` tags on that origin and no other absolute image origin at all. No champion art, splash
art, loading screen, logo or Riot mark is loaded from anywhere else, and the
favicon is a local file.

**Next step (owner).** None. Any future asset needs a Press Kit check and a
recorded permission before it is added, and gate check 2 will fail the build if
it comes from an unpermitted origin.

### 6. Dependency changes

**Required action.** Licence inventory maintained; `make vuln` on dependency
changes.

**Status: partially met - `make vuln` exists, no licence inventory exists.**

| Sub-requirement | Status | Reason and evidence |
| --- | --- | --- |
| Vulnerability scan on dependency changes | met | `make vuln` runs `govulncheck` (v1.8.0, pinned); the Go module graph is the only compiled dependency |
| Licence inventory | **not met** | No inventory file and no `make` target produce one. The site itself has **no npm runtime dependencies** and no `package.json` dependencies beyond the Astro toolchain, which keeps the exposure small, but "small" is not a record |

**Next step (owner).** Generate a licence inventory for the Go module graph and
the npm toolchain, commit it, and add a check that fails when a new dependency
appears without a recorded licence. Until then this row stays open, and any new
dependency should be treated as unrecorded.

### 7. If monetisation is ever considered

**Required action.** Stop and re-read Riot's transformative-use test before
adding anything paid.

**Status: not applicable.** No monetisation is planned or implemented. Gate
check 4 confirms there is no checkout route, no subscription route and no pricing
page in the built site, so the free-tier requirement and the no-monetisation
posture are the same fact today.

**Next step (owner).** Nothing while the posture holds. Any paid feature re-opens
checkpoints 1, 3 and 5 at once: Riot's transformative-use test applies to a paid
derivative as much as to a free one, and the "no data broker" clause is about
selling access rather than about price.

## Launch-blocking claims, and how each is proved

Every claim below is reproducible offline with no network and no `npm install`.
The automated form is `make compliance`; the commands are given so a reviewer can
run the claim in isolation.

### The free tier is genuinely free and ungated

No account, no login, no paywall, no rate-limited teaser, no email capture. Every
primary route in the frozen route table (`/`, `/tier-list/<role>`,
`/champions/<slug>`, `/matchups/<role>`, `/about` and the three legal pages)
renders its substantive content for an anonymous reader, and all of it renders
**without JavaScript** - the tables are server-rendered and the island only adds
sorting and filtering.

Evidence: gate check 4 scans the whole captured corpus (1063 pages in the
2026-09-18 capture) for a password or email field,
a `name="password"`/`name="email"` field, a login, sign-in, sign-up, register,
subscribe, pricing or checkout route, `data-paywall` or a "Sign in"/"Sign up"
link, and finds none; it separately requires that every form in those pages is a
no-JS GET form with an on-origin action and that every named control sits inside
one, which is what keeps a form from becoming a gate by accident (the 2026-09-17
amendment below). There is no auth code in the repository, no session
cookie, and the deployment has no identity provider: `deploy/base/web/` serves
the tier's HTTP surface through Caddy, and `caddyfile.yaml` contains no
`basic_auth`, `forward_auth` or other authentication directive. Twenty
`<input type="search">` elements exist and are deliberately excluded from the
pattern - they are the islands' own same-origin table filters, they carry no
`name` and therefore submit nothing, they filter data the reader has already
been served in full, and the tables are complete and readable with JavaScript
disabled.

### No MMR, ELO or rating-like value is computed, stored or displayed anywhere

This is a hard Riot prohibition, so it is checked as an absence across every
language the project uses, and the check refuses to pass vacuously.

Reproduce:

```
sh scripts/compliance-check.sh          # check 1
```

What it scans: every `*.go`, `*.sql`, `*.ts`, `*.js`, `*.mjs`, `*.astro`,
`*.json`, `*.html` and `*.css` file under the repository root, excluding
`node_modules`, `.git`, `.agent-artifacts` and `.astro`. That includes the
captured served corpus under `bin/served-pages`, so the scan covers what is
actually served as well as what is written. **1370 files** on the last run; the
count grows as the other workstreams add files, so the recorded run is the
evidence and the number is orientation only. The scan fails if it reads fewer
than 200 files, and fails again if it finds no rating mention at all: a scan that
read nothing, or that cannot match the standing prohibition, is reported as a
broken pattern rather than as clean code.

What it finds: **4 lines mention a rating, and all 4 are negations** of the hard
prohibition - the standing `NoRatingText` constant in `internal/webtier/brand.go`,
its use on the About page, and the same sentence as it appears in the served
`/about` and `/disclaimer` HTML. A line is exempt only when a negation word
precedes the token on the same line, and the exempt lines are printed so a
reviewer can read them rather than trust them. **Zero rating-like identifiers,
keys or columns exist** - the scan also looks for the declaration and key forms
(`mmr`, `elo`, `rating`, `skill_rating`, `matchmaking_rating`, `player_rating`,
`hidden_rating`) with explicit non-identifier delimiters.

Coverage: Go (the control plane, the crawler, the aggregator and the tier that
renders the pages), SQL (the migrations, including the view and column names),
the served HTML and CSS, and the aggregate artifact schema in
`schema/agg.d.ts` (generated by `go run ./cmd/gen-types` from
`internal/aggmodel`) plus `fixtures/agg/agg.schema.json`. Lead-by-lead, there is no field, no column, no
view and no page that could carry such a value, so none can be displayed.

### The privacy policy describes what the implementation actually does

`/legal/privacy` is written from the implementation rather than from a template,
so each of its claims is checkable. The site processes exactly one thing - an
ordinary web-server access log - and the policy says so rather than claiming that
nothing is collected.

| Claim | How it was checked | Result |
| --- | --- | --- |
| No cookies, and none set on the site's behalf | `grep -rniE 'set-cookie\|set_cookie\|cookie' deploy/` and `grep -rniE 'document\.cookie' internal/webtier/` | no match in either |
| No analytics, advertising, tracking pixel or third-party embed | gate check 3 over the whole captured corpus (1063 pages in the 2026-09-18 capture) | no executable third-party resource; every script, embed and preconnect is same-origin |
| No accounts, logins, forms or user submissions | gate check 4 | no form, credential field or auth route |
| Nothing stored on the device | nothing in the served corpus and nothing in the tier: `grep -rniE 'document\.cookie\|localStorage\|sessionStorage' internal/webtier/ bin/served-pages` returns no match | absent |
| The access log is the only processing | `deploy/base/web/caddyfile.yaml:80` - `log { output stdout }`, and `grep -rniE 'fluent\|vector\|promtail\|filebeat\|logstash' deploy/` | logs go to container stdout; **no log shipper, no log store and no retention configuration exists**, which is why the policy says the practical retention is days, until the container is replaced |
| The Data Dragon icon request is disclosed | the privacy policy names `ddragon.leagueoflegends.com`, states that it receives the visitor's IP address and user agent, that Riot Games is established in the United States, and that this is therefore a transfer outside the EEA | disclosed rather than omitted |

The policy also discloses a near miss deliberately: the operator runs a
self-hosted Umami analytics instance elsewhere on the same home cluster at a
different hostname. This site loads no script from it and sends it nothing. That
is stated on the page, because a reader who discovered the instance themselves
would reasonably wonder, and a disclosure that only covers what is definitely
fine is not a disclosure.

### No data-broker behaviour

The site serves derived aggregates only. It does not resell or expose the raw
archive, and it publishes no per-player identifiable data.

Evidence: gate check 9. The raw archive - verbatim Riot payloads - lives on the
cluster and is never fetched by a visitor. The deployed artifact root is `/agg`;
no served page links to or fetches anything under it (the only occurrence of the
string `/agg` in the corpus of 2026-09-18 is the About page's prose describing
the pipeline), the corpus contains **no** JSON file at all and no path matching
`*/raw/*` or `*/archive/*`. The published artifact schema declares no
PUUID, no summoner id, no account id, no Riot id and no profile icon id, and the
check proves its pattern works by requiring it to match in
`internal/contract/contract.go`, where the crawler genuinely stores a PUUID
(6 lines). That field is addressed by MATCH-V5, which is why the control plane
holds it and the published surface does not.

Per-player data is not published in any other form either: there is no summoner
or profile route in the frozen route table, and no aggregate type carries a
player dimension. The aggregates are role, champion, rank bracket, region, queue
and patch - never a person. Committed payloads carry only synthetic identifiers
(gate check 10: 13 fixture files, 655 identifier values, all either the reserved
`fixture-` prefix or the reserved `FIXT` Riot ID tagline that
`internal/aggregate/fixture_test.go` writes when it regenerates them),
and `fixtures/README.md` states that nothing in the directory is real Riot data.

### Only permitted Riot assets are used

Data Dragon static data (names, icons, numeric ids) and nothing else: no champion
art, no splash art, no loading screens, no Riot marks.

Reproduce against the captured served corpus (`make served-pages` refreshes it):

```
grep -rhoE 'https?://[A-Za-z0-9.-]+' bin/served-pages --include='*.html' --include='*.css' \
  | tr '[:upper:]' '[:lower:]' | sort -u
```

Every origin that appears in an image, icon, `og:image`, `twitter:image`,
`<source>` or CSS `url()` position is `ddragon.leagueoflegends.com/cdn/`.
Gate check 2 is the automated form: it extracts those references from the served
HTML and CSS, then removes the Data Dragon origin and fails if anything remains.
The captured corpus of 2026-09-18 carries 1038 `<img>` tags pointing at the Data
Dragon CDN and **zero** other absolute image origins; nothing else is loaded from
a third party, and the remaining references are same-origin.

No build fetches Data Dragon any more. The static data is checked in under
`projection/`, embedded by `internal/webtier/data.go`, and the frozen tree
reserves `/agg/v1/static/<ddragon_version>/` for it (`docs/contracts.md` section
4; the serving gate warns if that prefix is absent from the served root). The
tier reads its own copy at render time and makes no outbound request, so no
visitor's page view causes a Riot request; the build-time
`web/scripts/fetch-ddragon.mjs` went with the Astro tree on 2026-09-18.

## Standing constraints

These are properties of the design rather than steps, and a change that breaks
one of them is a decision that needs an ADR:

- **No request-time Riot API access from the site.** The site reads pre-computed
  artifacts. A visitor's page view never causes a Riot API call.
- **No MMR, ELO or skill-rating calculator.** Not in v1, not on the backlog, and
  gate check 1 fails the build if one appears in any form.
- **No paid tier.** ~~No gating~~: the published data is free and needs no
  account, but the site currently sits behind a password gate while it serves
  real data from the development key (**D-1**, and the requirement is waived with
  the rest of the workstream by **D-4**, 2026-09-17).
- **`n` is published on every statistic,** and thin cells are suppressed rather
  than shown. See `docs/contracts.md` section 1.
- **The data state is disclosed on the page, not inferred by the reader.** Every
  page carries the patch, region, queue and bracket it was built from, plus the
  aggregate manifest's `source` (`demo`, `riot-match-v5`, or no data), rendered
  from the tier's own constants and build metadata (`internal/webtier/brand.go`,
  `internal/webtier/view_feeds.go` and the page templates).
- **Rank attribution is disclosed as a snapshot.** The tier and division in a
  frontier entry are where a PUUID was discovered, not where it is now.
- **Secrets are never committed and never baked into an image.** The Riot key is
  an environment variable read by `internal/config`; `deploy/*/secret.yaml` is
  gitignored.

## Honest gaps and known weaknesses

Recorded here rather than smoothed over, because a register that only lists
successes is not a register.

1. ~~**The frozen non-endorsement sentence is verbatim on 4 pages, not on all
   1063.**~~ **Closed.** The footer served its own paraphrase on all 1063 pages
   while the exact frozen sentence reached only the four compliance pages. Both
   footers now render `NonEndorsementText` from `internal/webtier/brand.go`, the
   constant that took over the string `web/src/lib/legal.ts` used to hold, so the
   approved sentence is served byte for byte on every page in every
   data state, and gate check 6 fails any page that states the notice in other
   wording. The `web/src/layouts/fallback/Footer.astro` wording quoted here
   before the fix is no longer published anywhere, and the file itself went with
   the Astro tree on 2026-09-18.
2. ~~**The build names a reserved placeholder hostname.**~~ **Closed in the
   code, open in the deployment.** With `LOLSTATS_SITE_URL` unset the 1063 built
   pages used to carry `lolstats.example.invalid` in their canonicals and the
   sitemap, disagreeing with the legal copy. `web/astro.config.mjs` now publishes
   `https://lol.erik-schuetze.dev` - the address this deployment is served from -
   and logs that it fell back, and it refuses a reserved or relative value with a
   build error instead of publishing a wrong canonical. Gate check 8 **fails** on
   a reserved hostname with or without the variable. What remains open is not code:
   the deployed job still does not set `LOLSTATS_SITE_URL`
   (`deploy/base/config.yaml`), so the release depends on the deliberate default
   rather than on a declared value.
3. **`riot.txt` cannot be published yet - and is not a gate.** Reported under
   checkpoint 1, with the decision recorded under amendment 2: the honest
   absence is a documented open operational requirement owned by the deploy lane,
   and gate check 5 gates the *claim*, not the errand. The token is issued to the
   domain owner.
4. **No licence inventory exists.** Reported under checkpoint 6.
5. **No off-site backup of the raw archive exists.** The archive is the project's
   only non-regenerable asset, because Riot retains matches for two years and
   timelines for one. A tested off-site restore is a launch gate and it is not
   mine to build; `scripts/backup-verify.sh` verifies restores but no off-site
   medium has been chosen (plan open question 3).
6. ~~**The public-preview posture was this workstream's reading, not a decision
   the owner had confirmed.**~~
   **Closed 2026-09-17.** The owner answered plan question 6 by choosing
   publication: real crawled Riot data is served from the **development** key
   behind the existing password gate (**D-1**), and the compliance workstream is
   **waived** (**D-4**). `docs/decisions/ADR-010-public-preview-posture.md` keeps
   its original posture as history and now carries `Status: Superseded`; the value
   that holds is in `deploy/base/web/go-deployment.yaml`. What the gap recorded
   was that the posture was this workstream's interpretation rather than the
   owner's decision - that is the part that changed.
7. **The render-parity reference was out of step with the served design layer -
   closed 2026-09-18, by retiring the reference rather than re-freezing it.** The
   byte-parity gate and its live variant were retired on 2026-09-17 (`8e23d67`,
   which deleted `internal/webtier/parity_test.go` and `live_parity_test.go`), and
   the pre-rendered Astro tree they compared against was deleted on 2026-09-18
   (`65f2983`). Nothing in the repository now carries a `TestRenderParity`, a
   `make test-parity` target, a `WEB_DIST_*` variable, or a mutation control:
   `scripts/parity-mutation-control.sh` went with them. Two consequences a reader
   should take from this item: the two resolution options it named are moot,
   because there is no second corpus left to re-freeze against, and the design
   authority is now the Go tier's own executable assertions
   (`internal/webtier/frozen_tokens_test.go`, `a11y_contract_test.go`) rather than
   a byte comparison with a tree no deployment renders from. The `/riot.txt`
   assertion the parity test carried was not dropped with it: it moved to
   `internal/webtier/fixtures_test.go` (see the `/riot.txt` decision above).
   The part of this item that was not parity - a red `Test` step stopping the
   compliance steps behind it - was fixed on 2026-09-17 by giving the launch gates
   their own workflow, and the parity half of that cause is gone with the gate.

   *The finding as recorded on 2026-09-17, kept because it is the only traceable
   record of the five runs it cites, and because those runs are the evidence that
   motivated the retirement. Every clause in it describes what was true that day:*

   > `internal/webtier/parity_test.go` compared the tier's bytes with `web/dist`,
   > and the served sheet on `main` no longer matched it: the served
   > `internal/webtier/assets/astro/JsonLd.BEq7AnVK.css` said
   > `--surface:#f1eae0` (and emitted `.ds-panel{background-color:var(--surface)}`)
   > where `web/dist/_astro/JsonLd.BEq7AnVK.css` said `--bg-light:#f1eae0`, and
   > `shell.tmpl` appended the frozen `assets/css/*` layer after it. Every route
   > therefore mismatched at offset ~1700 and the `Test` step failed on `main` in
   > runs [35266202608](https://github.com/Erik-Schuetze/league-of-legends/actions/runs/35266202608),
   > [35266466674](https://github.com/Erik-Schuetze/league-of-legends/actions/runs/35266466674),
   > [35266653629](https://github.com/Erik-Schuetze/league-of-legends/actions/runs/35266653629),
   > [35267893161](https://github.com/Erik-Schuetze/league-of-legends/actions/runs/35267893161)
   > and [35269826778](https://github.com/Erik-Schuetze/league-of-legends/actions/runs/35269826778)
   > - identical at `8524dfe` and at the gates commit `0a247c6`, and reproducible
   > locally with `make test-parity` (exit 2). It was the gate working, not a
   > flake, and it was invisible while the parity tests ran before `npm run build`
   > and skipped. Until it was resolved, the `verify` job stopped at `Test` and
   > the compliance steps after it did not run in CI.


## Non-endorsement disclaimer text

The wording lives in one place, `internal/webtier/brand.go` (`NonEndorsementText`),
and this page quotes it rather than restating it. All four compliance pages render
it, and both footers in the tier's templates
(`internal/webtier/templates/components.tmpl` and the page shells)
render it instead of carrying a paraphrase, so the site and this register cannot
drift apart.

> This project is not endorsed by Riot Games and does not reflect the views or
> opinions of Riot Games or anyone officially involved in producing or managing
> Riot Games properties. Riot Games and all associated properties are trademarks
> or registered trademarks of Riot Games, Inc.

Gate check 6 reads the sentence back out of `internal/webtier/brand.go` and requires it
to appear word for word on the built `/disclaimer` page. Changing this wording is
a compliance change, not a copy change.

## Compliance changes made on 2026-09-17 and 2026-09-18

- The four compliance pages were written: `/about`, `/legal/terms`,
  `/legal/privacy` and `/disclaimer`, all importing the shared strings from
  `web/src/lib/legal.ts` (operator identity, contact address, effective date,
  non-endorsement sentence, data-source sentence). The contact address is
  configured by the `LOLSTATS_CONTACT_EMAIL` environment variable, documented in
  that file, with a working default. (Those strings are `internal/webtier/brand.go`
  constants and the `internal/webtier/templates/pages/*.tmpl` bodies now; the
  Astro files they were written in went with the tree on 2026-09-18.)
- `EFFECTIVE_DATE` is a single constant and "last updated" is derived from it, so
  there is no second date to forget.
- `scripts/compliance-check.sh` and `make compliance` were added, together with
  a negative control (`.agent-artifacts/compliance-negative-probe.sh`) that
  plants one violation at a time and asserts the gate exits non-zero for each.
  Each plant is asserted to have landed before the gate is run, the clean tree is
  asserted to pass (so a probe cannot pass for the wrong reason), and the scratch
  tree is copied from a snapshot of the built site that is asserted page-for-page
  against the source. The observed result is 29 probes holding and 0 broken; the
  full output is section 8 of `.agent-artifacts/compliance-verification.log`.
  A verified-site claim is only accepted when `/riot.txt` is published and holds
  the configured `LOLSTATS_RIOT_VERIFICATION_TOKEN`; both states are probed.
- `docs/data-sources.md` was extended with the dated source register, the
  retention and rate-limit facts that drive the design, and the scraping
  position.
- The provenance copy was made conditional on the data state. A build renders
  `demo`, no data, or `riot-match-v5` from the manifest's `source`, and the
  champion pages, `/about`, the landing page, the privacy policy, the table and
  matchup notes and the JSON-LD datasets now read their claims from that state:
  only a `riot-match-v5` build describes MATCH-V5, and the tier's JSON-LD builder
  (`internal/webtier/view_dataset.go`) returns its `errNotLive` error rather than
  publish a Dataset `measurementTechnique` in any other state.
  Gate check 11 asserts that every built page carries the labelling its declared
  state requires, so a page cannot lose its banner or claim a state it is not in.
- **Checks 3 and 4 were amended on 2026-09-17** for the server-rendered tier,
  and `scripts/compliance-negative-control.sh` / `make
  compliance-negative-control` were added as the standing proof that the
  amendment is not a weakening: one planted violation at a time, each of which
  the amended gate must still reject, plus a page stripped of every `<script>`,
  which must pass. See "Amendment: checks 3 and 4, the dynamic-serving
  amendment" above. Both the gate and its controls run in CI.
- **Check 12 and the GNU/BSD divergence were added on 2026-09-17**, after CI
  run
  [35251786691](https://github.com/Erik-Schuetze/league-of-legends/actions/runs/35251786691)
  failed the compliance step on a tree that passes locally. The cause was a
  pre-existing defect in the gate, not in the site: `xargs -0 grep -L` over an
  empty path list reads the runner's stdin on GNU and reports `(standard input)`
  as a page that lost its banner. Ten scan call sites now go through `list_grep`,
  which short-circuits an empty list, and check 12 fails the gate if either an
  empty list or a real list stops behaving. `make compliance-gnu` reproduces the
  CI userland locally. Evidence: `bin/gnu-BEFORE.txt` (the old script under GNU
  grep: `RESULT: FAIL - 2 launch-blocking violation(s)`, both `(standard input)`),
  `bin/gnu-AFTER.txt` (`RESULT: PASS - 0 launch-blocking violations`) and
  `bin/gnu-MUT1.txt` / `bin/gnu-MUT2.txt` (the two harness mutants, each rejected
  by check 12). `scripts/compliance-negative-control.sh` gained a seventh control,
  a demo page re-declared live with no live banner, which proves the `-L` scan
  still fails on a genuinely bad page now that the empty list is short-circuited.
- Three decisions were recorded: `docs/decisions/ADR-008-no-third-party-ingestion.md`,
  `ADR-009-operator-identity-and-governing-law.md` and
  `ADR-010-public-preview-posture.md`.
- **The gate's second corpus became its only corpus when the Astro tree in `web/`
  was deleted on 2026-09-18.** Production has served the Go tier since the
  cutover, and a gate whose reference corpus is a tree no deployment renders from
  is worse than one with no second corpus, because it looks like coverage. The
  halves of checks 3, 4, 6, 8 and 11 that read `web/dist` are gone, the served
  corpus is captured by `make served-pages` (which `make compliance` depends on),
  and checks 6 and 11 now read the Go sources and the served responses. Nothing
  was relaxed: `web/dist` carried zero `<form>` elements, so check 4's form rule
  could only ever have passed vacuously against it. The record of the original
  two-corpus amendment is kept [[#Amendment 2]] below.
- **The gate gained a second corpus, on 2026-09-17.** `web/dist` held zero
  `<form>` elements - the tier renders the filter bar - so check 4's form rule
  was vacuous against the only corpus it read, and check 3's script-free count
  was 1038 of 1063 in the same direction. `scripts/capture-served-pages.sh` now
  captures the HTML a running tier returns (routes discovered from the tier's own
  `/sitemap.xml`, every response asserted `200` with an honest `Content-Length`
  and an HTML `Content-Type`) into `LOLSTATS_SERVED_DIST`, and checks 3 and 4
  asserted over both corpora. `make compliance-served` ran the whole thing on
  loopback with the fixture artifact tree, so CI ran it too. (`web/dist` and the
  second corpus are both gone as of the entry above; the capture path is now
  `bin/served-pages` and the variable that points a scan at it is
  `LOLSTATS_DIST`.) The invariant that
  replaced "a page contains a form" is that every control the page offers changes
  the document **without JavaScript**, asserted dynamically by
  `scripts/verify-serving.sh` check 4 against the live tier. See "Amendment 2"
  above, which also records the `/riot.txt` decision: absence is a documented
  open operational requirement, not a gate.
- **The negative-control suite grew from seven controls to ten**, adding the two
  the coordinator asked for - a fabricated third-party analytics `<script>` on an
  origin no denylist knows, and a filter-bar-shaped form with no server-side
  path - plus the opposite direction, the tier's own filter bar, which must pass.
  `make compliance-negative-control` and `make compliance-gnu` (now including the
  captured served corpus) both run in CI.
- **The launch gates moved into their own workflow, on 2026-09-17.**
  `.github/workflows/gates.yml` (`Launch gates`) runs `make verify-serving-local`,
  `make compliance`, `make compliance-negative-control`, `make compliance-served`
  (then an alias of `compliance`; it is still listed, which costs nothing) and
  `make compliance-gnu` as a job of its own. The reason is attribution, not
  convenience: in `docker-build.yml` these steps run after the Go test step, and a
  red `Test` step - which is what happened on 2026-09-17, for a design-layer
  parity mismatch owned by another lane - stops the job before any compliance
  result is produced. `docker-build.yml` still carries every step, because its
  `verify` job is what stands between a commit and a published image; the
  workflow file's header records this. The immediate consequence is that the
  compliance evidence for that date is readable even while the parity gate was
  red - a gate that existed only until 2026-09-18, when it was retired with the
  reference tree; see gap 7 and the next entry.
- **The parity gate gained a mutation control, on 2026-09-17.** **Deleted
  2026-09-18** with the gate it controlled (`8e23d67`): both
  `scripts/parity-mutation-control.sh` and `make parity-mutation-control` are gone,
  and the reference tree the control rewrote in place no longer exists. The record
  is kept because it is the evidence that the ordering fix worked while the gate
  was live. What the control did while it existed: it
  proved the parity tests *ran* (`make test-parity` failed on `SKIP`, and a design
  token rename did redden `TestRenderParity` in CI), but not that they still
  *compared*: a comparison that had been neutered - both renderers swapping in the
  same wrong bytes, say - would have reported `PASS`. The control closed that: the
  reference page was mutated
  (`lang="en"` -> `lang="zz"` in `web/dist/index.html`), the gate had to exit
  non-zero, the `home` route had to be the case that failed, the mutation had to
  appear on the reference side of the first-difference excerpt, and the page had
  to come back byte for byte (hash-compared). Observed: 6 controls held, 0 broken
  (`bin/parity-mutation-pass.log`). Two further paths were exercised by hand: an
  interrupted run was recovered by restoring the validated backup so the page was
  never left mutated, and a backup that did not look like an original was
  *refused* rather than written over the tree. The control was also controlled:
  with a stub `make` that exited 0 - a gate that no longer compared anything - the
  control reported `RESULT: FAIL - 3 control(s) held, 3 broken`
  (`bin/parity-mutation-stub.log`). It ran as the last step of `Launch gates`,
  after every scan that read the reference tree, because it rewrote it in place.
