# Compliance

Riot's rules are a launch gate, not a review step. This page is a register, not
an essay: for every checkpoint it records the trigger, the action required, what
is **actually true today**, and what the owner has to do next. A checkpoint is
either satisfied with a recorded artifact or it is open; there is no "probably
fine".

**Nothing is served today.** The Go presentation tier was deleted on 2026-09-18
(`docs/decisions/ADR-011-retire-the-web-tier.md`), and with it the automated
compliance gate that scanned the pages it rendered. What is left is a crawl and
data layer: an ingest worker, a nightly aggregation job, and the artifact
contract between them. So this page has changed shape. It no longer records the
result of a gate that runs on every commit, because there is no longer a gate and
nothing to scan. It records **the obligations themselves**, as written
requirements that any future serving layer must satisfy before it is exposed
publicly, together with the approved wording that must be used verbatim when it
is.

Two facts frame everything below:

- **The data layer still holds real crawled Riot match data**, deliberately,
  ingested from the operator's **development** key - owner decision **D-1**,
  2026-09-17.
- **The compliance workstream is waived** - owner decision **D-4**, 2026-09-17.
  This register is the record of what was checked and decided, not an open
  obligation, and waiving it is why a gate that could only ever fail is gone
  rather than left red.

Last reviewed: **2026-09-18**. Next review due: **2026-12-17**.

## The obligations that survive

These were the checks the deleted gate ran. They are kept here as requirements
because they are Riot policy and Riot policy applies to a display layer whenever
one exists again - it was never a property of the code that was deleted. A future
serving layer is non-conformant until each of these holds, and the checkpoint
register below says who has to act.

| # | Requirement | Fails when |
| --- | --- | --- |
| 1 | No MMR, ELO or rating-like value anywhere | A rating-like identifier, key, column or displayed value appears in Go, SQL, TS/JS, JSON, HTML or CSS |
| 2 | Only permitted Riot assets | An image reference has an absolute origin other than the Data Dragon CDN. Champion art, splash art, loading screens, logos and Riot marks beyond Data Dragon static data are not permitted at all |
| 3 | No third-party scripts, embeds or tracking | An executable resource on a page is not same-origin, or names a tracking service. A server-rendered page is legitimately script-free, and a page with no `<script>` is not evidence of anything either way |
| 4 | The free tier is free and ungated, and every control works without JavaScript | A credential field, auth route, pricing route or paywall appears; or a form is not a no-JS server-side path (`method="get"` with an on-origin `action`); or a named control sits outside a form |
| 5 | Verified-site claims are only made when satisfied | A page claims Riot reviewed or endorsed the site, or `/riot.txt` is published without the token, or a configured token is not published |
| 6 | The non-endorsement notice is visible, in the approved wording | The frozen sentence is not on the disclaimer page word for word, or any page states the notice in wording other than the approved text below, or a page stops linking to the disclaimer |
| 7 | The legal pages publish a contact route | Any of the four compliance pages renders with no contact address |
| 8 | The published address is the deployed one | A page, the sitemap or `robots.txt` carries a reserved placeholder hostname, or the published addresses name more than one origin |
| 9 | Nothing per-player is published | The artifact schema (`schema/agg.*`) or a served JSON file carries a PUUID, summoner id, account id, Riot id or profile icon id, or the served tree holds a raw-archive path |
| 10 | Committed payloads carry no real player identifier | A fixture identifier is neither the reserved `fixture-` prefix nor the generator's reserved `FIXT` tagline |
| 11 | Every served page is a whole document that discloses its data state | A page is truncated, or loses the labelling that discloses whether it is `demo`, `riot-match-v5` or no-data |
| 12 | A check that reads nothing must fail | A scan passes because it read zero files or zero pages. Every scan must floor its own input and report how much it read |

Three properties of the deleted gate are worth keeping even though the gate
itself is gone, because they are what made its results worth anything:

- **Fail closed.** A scan that read nothing failed rather than passed, an absent
  precondition was a failure and not a skip, and a `WARN` that still exited `0`
  was treated as a defect in the gate. Three separate gates had to be repaired on
  2026-09-17 for exactly that shape.
- **Every rule had a negative control.** A rule that cannot fail is not a rule, so
  each check had a planted violation it was required to reject - and, in the other
  direction, the real page it was written about had to pass.
- **Counts are dated readings, not invariants.** The figures this page used to
  quote (page counts, resource tags, forms) were one capture on one day. Where a
  number was a floor it said so; nothing else was a contract, and a count that
  moved was not a regression.

## The approved wording

This section is the **source of truth** for the published legal text. The strings
below were ported verbatim from the Astro tree's `web/src/lib/legal.ts` when that
tree was deleted on 2026-09-17, and they lived in the tier's `brand.go` constants
until the tier was deleted on 2026-09-18. They are reproduced here in full,
character for character, because that file is gone and history is not a source of
truth for a future implementer. **These are the published words, not a paraphrase
of them**: a second weaker copy of any of them is exactly the drift this section
exists to prevent.

Whatever serves the site must render these strings as constants. Nothing on the
request path may alter them.

> **League of Legends and Riot Games are trademarks or registered trademarks of Riot Games, Inc.**

Trademark sentence, for the footer.

> **This project is not endorsed by Riot Games and does not reflect the views or
> opinions of Riot Games or anyone officially involved in producing or managing
> Riot Games properties. Riot Games and all associated properties are trademarks
> or registered trademarks of Riot Games, Inc.**

The non-endorsement notice. It appears on every page; see the dedicated section
below.

> **This site is not affiliated with Riot Games, Inc., is not authorised,
> sponsored or approved by Riot Games, and is not an official source of League of
> Legends statistics.**

> **Every page on this site is free and ungated: there is no account, no login, no
> paywall and no rate-limited teaser, and none is planned.**

> **This site does not compute, store or display an MMR, ELO or any other skill
> rating, and does not offer a calculator for one.**

> **This site publishes derived aggregate statistics only. It does not resell Riot
> data, does not serve the raw Riot API responses behind its aggregates, and
> publishes no per-player record, account, summoner name or match history.**

The site name is **"LoL Stats"**.

### Operator identity and contact

Erik Schuetze, a private individual resident in the European Union, who operates
this site as a non-commercial hobby project.

The mailbox the compliance pages publish is `lolstats@erik-schuetze.dev`. The
deployment may override it with the `LOLSTATS_CONTACT_EMAIL` environment
variable; an unset variable is not an error and falls back to that address.

> **Questions about these pages, about the data, or about your rights under the
> GDPR go to <the mailbox above>. That mailbox is the contact route rather than a
> postal address, because the operator is a private individual.**

### Effective date and version line

The frozen sentence is:

> **Effective 17 September 2026. Last updated 17 September 2026.**

Both dates derive from a single `2026-09-17` constant, so there is no second date
to forget. Changing any wording on this page, or that date, is a compliance change
and not a copy change.

### The verified-site note

Riot's verified-site requirement is met only by a deployment that is given Riot's
site-verification token, which is issued to the domain owner and not to a
repository. Until then, the honest text is:

> **The verified-site requirement is not met yet: this build was not given Riot's
> site-verification token, so <site>/riot.txt is not published and Riot has not
> verified this site. The token is issued to the domain owner, not to this
> repository, so the requirement is met only by a build that is given it.**

A build that has a token publishes `/riot.txt` and drops the "not met yet"
wording. Nothing publishes a placeholder file, and no check may fail merely
because the token is absent - see gap 3.

## What was removed, and when

Recorded so that a reader who finds a reference to the gate - in an old commit, a
CI run log, or an ADR - knows what happened to it and does not go looking for it.

The gate was a POSIX `sh` script plus its negative controls, driven by `make`
targets and run as steps of the `verify` job in
`.github/workflows/docker-build.yml`. All of it was deleted on 2026-09-18
(`docs/decisions/ADR-011-retire-the-web-tier.md`): the thirteen checks, the
capture of the HTML a running tier served, the serving-contract harness and its
static-probe control, the four live-posture controls, the GNU-userland re-run and
its fail-closed control, the ten planted-violation controls, the `compliance`,
corpus-capture and gate-control make targets, and the CI steps that ran them.
The four data-layer scripts were kept.

The corpus went with it. The gate scanned the pages a running tier returned, and
there is no running tier; that capture lived under `bin/`, was never committed,
and is not an artifact anything produces now. Two earlier corpora - a
pre-rendered Astro tree and a mirror of it - had already been retired on
2026-09-17 and 2026-09-18.

Nothing about the deletions was a relaxation of a rule. Where a rule was changed
before the gate was retired it was replaced by the invariant it stood for and the
replacement was asserted in both directions, which is why the obligations above
are stated as properties ("every control works without JavaScript") rather than as
the markup patterns the first draft of the plan contained.

Three defects the gate found in itself are worth keeping, because each is a
failure mode a future check will meet again:

- **A `WARN` that still exits `0` is not a check.** The serving contract used to
  warn about a Data Dragon prefix it could not find and exit green, which is how
  a frozen contract outlived the served reality it described.
- **An absent precondition must fail, not skip.** A target whose container
  runtime was missing printed `skipped: docker is not installed` and exited `0`,
  so the half of the gate it guarded could disappear while the CI step stayed
  green.
- **`xargs -0 grep` over an empty path list reads the caller's stdin on GNU.** A
  scan handed no files reported `(standard input)` as a page that had lost its
  banner and turned a green tree red in CI while passing locally on BSD. Every
  scan that reads a list must short-circuit the empty list, which is obligation
  12.

Full history, including the run logs and commit links, is in this file's own git
history and in the change log at the end of this page.

## Checkpoint register

The seven triggers are below. "Status" is the state today, with the reason, not
an aspiration.

### 1. Before any public launch

**Required action.** Terms of Service and Privacy Policy published; the
non-endorsement disclaimer visible; `riot.txt` hosted; the free tier genuinely
free and ungated; no MMR/ELO calculator anywhere; no data-broker behaviour.

**Status: deferred with the serving layer. Nothing is exposed, so nothing is
launched.** The requirement does not lapse, it is simply not reachable while
there is no serving layer. The approved text for all four legal pages is
preserved verbatim above, which is the part that would otherwise have been lost
with the tier.

| Sub-requirement | Status | Reason |
| --- | --- | --- |
| Terms of Service published | carried forward | A future serving layer must publish a terms page carrying the 13 sections the retired tier's did, from acceptable use to a "Governing law" clause and an explicit Riot non-endorsement section. The retired page's text is not preserved word for word; the mandatory content is |
| Privacy Policy published | carried forward | The policy must be written from the implementation rather than a template, and must disclose the Data Dragon icon request and the fact that it transfers the visitor's IP address outside the EEA. The retired policy is described in the launch-claims section of this page's history |
| Non-endorsement disclaimer visible | carried forward | The approved sentence above must appear on every page, and every page must link to the disclaimer page |
| `riot.txt` hosted | **pending, and deliberately not a gate** | No token is configured, so no `riot.txt` exists and none is offered - a placeholder would be a false claim. The token is issued to the domain owner after they start a production-key application, so this is owner action, not code work. A check must fire on a *false* claim and never on the honest absence; see gap 3 |
| Free tier genuinely free and ungated | carried forward | No account, no login, no paywall, no rate-limited teaser, no email capture, and every control answered by the server without JavaScript. Obligation 4 |
| No MMR/ELO calculator anywhere | met | The prohibition holds across every language the project uses and is asserted as an absence with a floored scan. The artifact schema in `schema/` is the whole published shape and carries no rating-like field. Obligation 1 |
| No data-broker behaviour | met | The artifact contract declares no PUUID, no summoner id, no account id, no Riot id and no profile icon id, and the aggregates are role, champion, rank bracket, region, queue and patch - never a person. The raw archive is not published or exposed. Obligation 9 |

**Next step (owner).** None while nothing is served. **Superseded 2026-09-17:** the
owner answered plan question 6 by choosing publication - real crawled Riot data is
served from the **development** key behind the existing password gate (**D-1**) -
and **waived** the compliance workstream (**D-4**). The paragraph below is the
position that held until then, kept as history.

**Next step (owner), as at the time.** Ratify the preview posture and then start the
production-key application. The position the site implemented - the preview stays
up, labelled as a preview, and real crawled data is not published until a key is
approved - is recorded in `docs/decisions/ADR-010-public-preview-posture.md` and
in `docs/data-sources.md`.

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
`internal/aggmodel/schema.go` and `schema/agg.schema.json` are the whole
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
table above.

### 5. Before using any Riot asset

**Required action.** Riot Press Kit and permitted static data only; no champion
art, splash art or marks beyond that.

**Status: met.** The only Riot assets in the project are Data Dragon static data
(champion, item, rune and summoner-spell names and icons, plus numeric ids),
checked into the repository under `projection/` and regenerated from the public
Data Dragon CDN rather than fetched at build time. No champion art, splash art,
loading screen, logo or Riot mark is present. Obligation 2 is the rule for a
future serving layer: every absolute image origin must be
`https://ddragon.leagueoflegends.com`, and nothing else may be loaded from a
third party.

**Next step (owner).** None. Any future asset needs a Press Kit check and a
recorded permission before it is added.

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
cookie, and the deployment has no identity provider: `lolstats-web` serves the
HTTP surface itself (`deploy/base/web/go-deployment.yaml`; the shared edge Caddy
lives in the `homecluster` repository and only terminates TLS for it), and
neither `internal/webtier/` nor the manifests under `deploy/base/web/` contains
`basic_auth`, `forward_auth` or any other authentication directive. Twenty
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
`internal/aggmodel`) plus `schema/agg.schema.json`. Lead-by-lead, there is no field, no column, no
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
| The access log is the only processing | `internal/webtier/server.go` - the tier's `slog` logger writes to the container's own stream (`os.Stderr`), one line per request only at `LOLSTATS_LOG_LEVEL=debug` (the deployed value is `info`, so a served page writes nothing), and `grep -rniE 'fluent\|vector\|promtail\|filebeat\|logstash' deploy/` | logs go to the container's stdout/stderr; **no log shipper, no log store and no retention configuration exists**, which is why the policy says the practical retention is days, until the container is replaced |
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
a third party, and the remaining references are same-origin. (That 1038 is the
reading of that day's corpus, not a floor - see "Every count in this page is a
dated reading" above.)

No build fetches Data Dragon any more. The static data is checked in under
`projection/`, embedded by `internal/webtier/data.go`, and the frozen tree
reserves `/agg/v1/static/<ddragon_version>/` for it (`docs/contracts.md` section
4). The tier reads its own copy at render time and makes no outbound request, so
no visitor's page view causes a Riot request; the build-time
`web/scripts/fetch-ddragon.mjs` went with the Astro tree on 2026-09-18.

**The serving gate asserts which of the two states that prefix is in, and fails
on anything else** (2026-09-17; it used to print a warning and still exit 0, which
let a frozen contract outlive the served reality it described). Both states are
contract: *published* - `200` at exactly `public, max-age=3600` with an honest
`Content-Length` and a JSON body; *unpublished* - the reserved prefix answers
`404` with `Cache-Control: no-store`, never an invented `200` and never a
cacheable `404`. Live on 2026-09-17 the deployed tier is in the second state, and
the gate says so rather than warning: no version is published under
`/agg/v1/static/` and every probe answers `404` + `no-store`, which is gap 8.

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
successes is not a register. Gaps that were about the deleted tier are recorded in
"What was removed, and when" and in this page's git history; the ones below are
still live.

1. **No serving layer exists, so every display-layer obligation is unmet by
   default.** Obligations 2 through 11 are properties of a rendered page, and
   there are no rendered pages. They are written down so that the first person to
   build one starts from the requirements rather than rediscovering them, and so
   that "nothing is served" is not mistaken for "everything is compliant".
2. **`riot.txt` cannot be published yet - and is not a gate.** Reported under
   checkpoint 1: the honest absence is a documented open operational requirement
   owned by the deploy lane, and any check must gate the *claim*, not the errand.
   The token is issued to the domain owner after they start a production-key
   application. A check that failed while the token was absent would fail for a
   reason no code change can fix.
3. **No licence inventory exists.** Reported under checkpoint 6. The frontend
   toolchain went with the tier, so the npm half of the inventory is gone and the
   problem shrinks to the Go module graph - which is smaller, not solved.
4. **No off-site backup of the raw archive exists.** The archive is the project's
   only non-regenerable asset, because Riot retains matches for two years and
   timelines for one. A tested off-site restore is a launch gate and it is not
   this workstream's to build; `scripts/backup-verify.sh` verifies restores but no
   off-site medium has been chosen (plan open question 3). The drill fails closed
   on an empty source rather than reporting a vacuous pass, which was fixed on
   2026-09-17. What remains genuinely open is the medium, not the drill's honesty.
5. **The Data Dragon projection is reserved in the contract and nothing publishes
   it.** `docs/contracts.md` section 4 freezes a static prefix at
   `public, max-age=3600`; no publisher writes it. This was a gap when a tier
   rendered from its own embedded copy and the served prefix answered `404` with
   `no-store`; it is now simply an unused reservation in the artifact contract.
   Anything that publishes or serves that prefix must pick one of the two states
   the contract defines and stay in it.
6. **No automated check of any obligation above remains.** The deletions were
   deliberate and the workstream is waived, but the consequence is real: this page
   is now the only enforcement, and a document is not a gate. A future serving
   layer should reintroduce checks for obligations 1 through 11 before it is
   exposed publicly, and should give each one a negative control.

## Non-endorsement disclaimer text

The wording is reproduced in full above, in "The approved wording", because the
file it used to live in was deleted with the tier. It is quoted here as well
because this is the sentence Riot's policy is actually about:

> This project is not endorsed by Riot Games and does not reflect the views or
> opinions of Riot Games or anyone officially involved in producing or managing
> Riot Games properties. Riot Games and all associated properties are trademarks
> or registered trademarks of Riot Games, Inc.

The retired site rendered it on every page from a single constant, and a check
read that constant back and required it on the disclaimer page word for word.
Changing this wording is a compliance change, not a copy change.

## Change log

Newest first. Entries about the deleted gate are kept because they record why the
obligations above are written the way they are; the run logs and commit links they
used to cite are in this page's git history.

- **2026-09-18 - the gate, its corpus and the web tier were deleted, and this
  page was rewritten to hold the obligations instead of the results.** The tier
  was retired in `docs/decisions/ADR-011-retire-the-web-tier.md`; the approved
  wording it held was copied verbatim into "The approved wording" above before the
  file was removed, which is what the ADR names as the mitigation for the risk
  that a later frontend would rediscover Riot's rules from scratch. The thirteen
  checks became obligations 1 through 12, the negative controls and their
  fail-closed rules became the design notes in "The obligations that survive", and
  the two amendment sections - both of which existed to explain why a pre-rendered
  Astro site's rules did not fit a server-rendered tier - were dropped, because
  neither rule nor tier exists now. The checkpoint register survived intact apart
  from its statuses. The `/riot.txt` decision was kept as gap 2: absence is a
  documented open requirement and never a gate.
- **2026-09-18 - the second corpus became the only corpus when the Astro tree in
  `web/` was deleted.** Production had served the Go tier since the cutover, and a
  gate whose reference corpus is a tree no deployment renders from is worse than
  one with no second corpus, because it looks like coverage. Nothing was relaxed,
  and the reason is the one to remember: the Astro tree carried zero `<form>`
  elements, so the form rule could only ever have passed vacuously against it.
- **2026-09-18 - the live half of the banner check gained a control, because the
  corpus CI scanned could not exercise it.** Every page in a fixture-backed
  capture declares the demo source, so the live-banner scan saw an empty list
  there: the half that had broken was the half a green CI run could not show. The
  control rewrote only the manifest's `source` field in a scratch copy, served it
  from a loopback build with no cluster and no network, and required the check to
  pass while naming the live pages it read *and* to fail, naming a real page path,
  on one live page stripped of its banner. A second control proved the first one
  fails closed when the tier is unbuilt or not in the live posture. This is the
  general shape of a control worth keeping: it asserts the *pass* direction with
  evidence, not only that a failure can be provoked.
- **2026-09-17 - three gates that could not fail were repaired.** They share one
  cause: a precondition that is absent produced a green run instead of a red one.
  (a) The serving contract's Data Dragon check reported an unpublished prefix as a
  `WARN` and still exited `0`, so a frozen contract could outlive the served
  reality it described. (b) The GNU-userland re-run printed `skipped: docker is
  not installed` and exited `0`, so that half of the gate could disappear while
  the CI step stayed green; it now depends on a guard that fails closed with a
  reason, and a control proves it by hiding the runtime from `PATH` for real. (c)
  A parity control the class was reported against no longer existed, so it was not
  revived - what was fixed is the shape it left behind. (d)
  `scripts/backup-verify.sh` printed `WARN skipped: the source has no matches
  rows, so a full comparison is vacuous.` and exited `0`, so the drill printed
  `PASS dump and restore agree (6 tables, 0 row(s) in matches)` over a dump it had
  compared against nothing. The warning is now a `FAIL` and the `warn()` helper is
  gone from the script by design, so a third defect of the same shape cannot be
  added without deciding against the rule on purpose. That script is one of the
  four the demolition kept, and entry (d) is the only one of the four still in the
  tree.
- **2026-09-17 - a red CI step was attributed to the gate and was a harness
  fault.** CI failed with "1 live page carries no live banner" while the same
  output said `0 live` pages had been scanned. Both cannot be true, and
  `(standard input)` is not a path in any corpus: the path lists handed to `grep
  -L` were empty, GNU `xargs` runs the command even for an empty list, and `grep`
  with no file operand read the runner's redirected stdin and reported it as a
  page. BSD/macOS `grep` reads an empty stdin and prints nothing, which is why the
  gate was green locally and red only in CI. Nothing was edited to make the
  complaint go away; every scan that reads a list was routed through a
  short-circuit for the empty list, and a check was added that fails if either an
  empty list or a real list stops behaving. This is obligation 12.
- **2026-09-17 - checks 3 and 4 were amended for a server-rendered site.**
  Neither rule was weakened; each was replaced by the invariant it stood in for,
  and both replacements were fail-closed and carried a negative control. The plan
  required at least one `<script>` per page and banned the `<form>` tag. A
  server-rendered page is legitimately script-free, and the filter bar *was* the
  no-JS path - banning the tag would have banned the accessibility mechanism the
  design was built on. The replacements are what obligations 3 and 4 say now: the
  invariant is that nothing a page loads can phone home, and that every control
  the page offers changes the document without JavaScript.
- **2026-09-17 - the compliance pages were written and the register was
  extended.** `/about`, `/legal/terms`, `/legal/privacy` and `/disclaimer` were
  written from a single set of shared strings (operator identity, contact address,
  effective date, non-endorsement sentence, data-source sentence), with the
  contact address overridable by `LOLSTATS_CONTACT_EMAIL` and a working default.
  `docs/data-sources.md` gained the dated source register, the retention and
  rate-limit facts that drive the design, and the scraping position. Those strings
  are the "approved wording" above; the pages themselves went with the tier.
- **2026-09-17 - the D-1/D-4 decisions superseded the public-preview posture.**
  The owner answered plan question 6 by choosing publication, and waived the
  compliance workstream. `docs/decisions/ADR-010-public-preview-posture.md` keeps
  its original posture as history and carries `Status: Superseded`. Two decisions
  from the same workstream are unaffected:
  `docs/decisions/ADR-008-no-third-party-ingestion.md` and
  `docs/decisions/ADR-009-operator-identity-and-governing-law.md`.
