# Changelog

All notable changes to this project are recorded here. Entries are factual and
short. "Breaking" means something that used to work no longer does.

## Unreleased

### Added

- Repository scaffold: `Makefile`, `Dockerfile`, `.golangci.yml`, `go.mod`, MIT
  licence.
- Frozen contracts in `docs/contracts.md`: aggregate artifact shapes, the route
  table, the Go interfaces, the frontend component API and the image contract.
- `internal/contract` - the Go interface surface (`RiotClient`, `RawWriter`,
  `Store`).
- `internal/aggmodel` - artifact types, path builders and a JSON Schema and
  TypeScript emitter.
- `internal/riot` - the frozen Match-V5 DTO subset.
- `internal/config` - environment-driven configuration. All variables are
  prefixed `LOLSTATS_`.
- `internal/obs` - structured logging and the Prometheus metrics registry.
- `sql/migrations/0001_init.up.sql` - the six control-plane tables.
- `cmd/lolstats-ingest` and `cmd/lolstats-aggregate` - compiling subcommand
  skeletons.
- `cmd/gen-types` - generates `schema/agg.d.ts` and `schema/agg.schema.json`
  from the Go structs.
- `docs/architecture.md`, `docs/data-sources.md`, `docs/compliance.md`.
- `docs/decisions/ADR-001` to `ADR-004`.
- `fixtures/` - hand-authored sample payloads with their provenance.
- `lolstats-aggregate build` gained a provenance gate
  (`LOLSTATS_AGG_REQUIRE_PROVENANCE`, `-require-provenance`,
  `internal/aggregate/provenance.go`). With it on, a build that cannot name the
  revision it was built from and the `build_runs` row the run opened fails as
  `unrecorded_provenance` before the extraction runs, instead of publishing
  `build_run_id 0` and `git_sha "unknown"`. The default is `false` and nothing
  in `deploy/` sets the variable, so the deployed nightly build still runs
  without the gate; adding the key to `deploy/base/config.yaml` is what turns it
  on. Fixture builds, offline verification and `demo` are unaffected either way.

### Removed

- The performance-evidence apparatus, on 2026-09-18: `docs/evidence/` (116
  Lighthouse and axe reports, 46 MB of the repository's tracked bytes),
  `docs/PERF-EVIDENCE.md` (1,292 lines) and `scripts/perf/` (8 scripts). No
  target in the `Makefile` and no workflow invoked any of it; the reports
  measured a posture that no longer exists and cannot be regenerated. Nothing
  outside these files referenced them.
- The two evidence monographs and their reproduction scripts:
  `docs/PATCH-ROLLOVER-EVIDENCE.md`, `scripts/verify-patch-rollover.sh`,
  `docs/PUBLISH-LIVENESS-EVIDENCE.md` and
  `scripts/verify-publish-liveness.sh`. They were the only two scripts under
  `scripts/` with no `Makefile` entry point, so there was no supported way to
  run them. The behaviours they recorded are still asserted by code:
  `TestMissingArtifactIs503WithAPage` covers the fail-closed `503`, and
  `docs/runbooks/rebuild-aggregates.md` describes the rollover without citing
  the deleted document.
- `docs/contracts.md` sections 3 and 6, and `docs/design-system.md`,
  `internal/webtier/assets/css/DESIGN-FREEZE.md`. Sections 3 and 6 specified the
  frontend component API of the deleted `web/**` tree and an ownership map for
  concurrent agents, two of whose paths (`internal/aggregator/**`,
  `internal/parquet/**`) never existed. `docs/contracts.md` is still normative
  for the aggregate shapes, the route table, the Go interfaces and the image
  contract (its sections 1, 2, 4 and 5).
- `.github/workflows/gates.yml` (`Launch gates`) - a duplicate of the `verify`
  job in `docker-build.yml`, step for step: the same build, serving-contract,
  compliance, negative-control, gate-control and GNU-userland steps, in a second
  workflow that triggered on the same events, so every push and pull request ran
  all of them twice. The one thing it provided - a gate result that is readable
  while some other check is red - is now provided by `if: always()` on those
  steps, which is where `docs/compliance.md` records the 2026-09-17 incident
  that made them independent.
- `backlog.md` - the deferred-work file. Each item was either already done or a
  plan for a workstream that is no longer being run in parallel.
- `web/**` - the Astro tree (218 files), deleted on 2026-09-18. Production has
  served the Go SSR tier since the cutover: `lolstats-go-web` renders every route
  from the published `agg/v1` snapshot, and the inner Caddy deployment, the
  `site-build` CronJob and the `static-sync` CronJob were deleted from the
  cluster. The published tree lives on NFS and is untouched by this change; the
  tree in this repository was the *source* of a build nothing ran any more, and
  it stayed load-bearing only for CI. Everything it was still needed for was
  moved or retargeted as part of the same change: `web/src/fixtures` ->
  `fixtures/site`, `web/src/data` -> `projection` and `web/src/types` -> `schema`
  moved in the commits ahead of this entry, the compliance gate's reference
  corpus -> the pages a running tier serves (`make served-pages`), the approved
  wording -> `internal/webtier/brand.go` and `site.go` as the source of truth,
  and the workflows and `Makefile` lost every Node, npm and `web/dist` step. The
  retarget commits landed directly after the deletion rather than before it, so
  CI was red for that interval; both commits are recorded on 2026-09-18. No Node
  toolchain is required to build, test or publish this repository any more.
- `internal/webtier/templates/pages/{about,privacy,terms}.body.tmpl` and
  `internal/webtier/gen_body_templates.py` - the reference page bodies and the
  one-shot porting script that produced the Go templates from the Astro build.
  The port is committed (`about.tmpl`, `privacy.tmpl`, `terms.tmpl` are what is
  served) and the input tree `web/dist` is gone, so nothing could re-run it.
  `docs/compliance.md` cited the reference bodies as its evidence for the legal
  pages; it now cites the templates that are actually served.
- `make web-install`, `web-build`, `web-deps` and `web-dist`. The gate's corpus
  is captured from a running tier by `make served-pages`; `make compliance`
  depends on it. `make compliance-served` is kept as an alias of `make
  compliance`, because there is one corpus now instead of two.

### Changed

- Documents that described the deleted static tier as live were corrected on
  2026-09-18, without a behaviour change: the README no longer calls the
  pipeline a scaffold, `CHANGELOG.md`'s own notes no longer say the ingest and
  aggregate subcommands exit 3, `docs/architecture.md` no longer places the
  deployment manifests outside this repository,
  `docs/runbooks/rebuild-aggregates.md` describes the one-step chain the tier
  actually has instead of the `site-build` render that no longer exists,
  `deploy/base/config.yaml` describes `LOLSTATS_AGG_FIXTURES` as the Go tier
  reads it (including that the public tier forces `off` in its own `env`), and
  the Data Dragon projection fault in `internal/webtier/data.go` names a remedy
  that exists - there is no `package.json` in this repository, so the `npm run
  data:champions` it used to print could not be run.
- The documents and comments that still described the retired static tier as
  live were corrected on 2026-09-18, again without a behaviour change.
  `docs/runbooks/site-integrity.md` was deleted - it was 324 lines of
  instructions for a Caddy and a `site-build` job that no longer exist - and the
  `site-build`/`caddyfile.yaml`/`astro.config.mjs` citations behind the compliance
  and data-source evidence rows now point at the Go tier's own code.
  `docs/data-sources.md` keeps its 2026-09-17 serving measurements but marks them
  retired rather than presenting them as the deployed posture. Two statements
  about the tier were simply wrong and are now right: it falls back to
  `DefaultSiteURL` instead of refusing a reserved hostname, and the deployment
  does declare `LOLSTATS_SITE_URL`. The served `/about` page no longer names
  `web/scripts/fetch-ddragon.mjs`, and `internal/webtier/data.go` no longer claims
  a `TestEmbeddedProjectionsMatchTheRepository` drift test that does not exist
  (the two projection copies are byte-equal; nothing tests that they stay so).

- `AGENTS.md` no longer frames the repository as a shared tree edited by several
  concurrent agents, and no longer points at `docs/contracts.md` section 6 for
  the ownership map that section 6 was. Its rules - the README's job, the ADR
  rule, the house style, "before you say it works" - are unchanged.
- The design freeze over the served CSS layer was retired with
  `DESIGN-FREEZE.md`: the token set, the contrast floors, the "is it still
  inlined last" checks and the byte ceiling are still asserted by
  `internal/webtier/frozen_tokens_test.go`, but the test no longer also has to
  keep a prose document in sync, so changing a token value no longer means
  editing a document first.

### Fixed

- The crawl worker wrote nothing while it was parked on Riot's `Retry-After`:
  the loop reports after a pass that fetched, and the branch that waits out a
  rate limit jumped straight back to the top of the loop, so a crawler the
  limiter was holding emitted only a `Debug` line - invisible at
  `LOLSTATS_LOG_LEVEL=info`. The wait branch now makes the same report a fetch
  does, naming the wait it is holding (`paused_on_rate_limit`), and a wait cut
  short by shutdown is not claimed as served.

  **What this entry does not claim.** The change was first justified by a
  heartbeat spacing of 109-121 s measured against `DefaultReportInterval` of
  60 s on 2026-09-18, read as "about half the iterations were waits". That
  reading is wrong and was falsified by measuring the same lines again:
  `matches_retained` advanced 20 -> 98 -> 198 -> 251 across those heartbeats, so
  the loop was fetching throughout and the spacing is the length of a pass, not
  a parked wait. No `paused_on_rate_limit` line appeared in the fifteen minutes
  observed on the `sha-94f71e2` deployment. The reason is arithmetic: a
  suspension is capped by the call's retry wait budget (60 s) while a pass in
  the measured regime takes 60-120 s, so the loop normally returns to the top
  after the suspension has already expired. The branch is reached when a pass
  returns quickly inside a suspension - an empty queue whose claims are all
  deferred - and that state is pinned by
  `TestAPausedCrawlReportsTheWaitItIsHolding` rather than by a production sighting.
  It is kept because the alternative is a state that writes nothing at all.

- `lolstats_riot_key_age_seconds` was scraped as a constant `0`. The only writer
  sat behind a `Age() (time.Duration, bool)` assertion on the crawl worker's
  `Fetcher`, and the only type that satisfied it was the crawl test fake: the
  real client exposed `Keys()` and no `Age`, so the gauge kept its default of
  `0` - the reading of a key rotated a moment ago - while CI stayed green.
  `riot.Client.Age` now answers the assertion through `KeyProvider.AgeKnown`,
  which reports an unknown age rather than `0`, the assertion is pinned at
  compile time on the real type at both ends, and the gauge is a label-less
  vector so the series is *absent* until a known age is published instead of
  defaulting to `0`.

- Three documents described that gauge as deliberately unreadable and were
  stale from the fix onward: `deploy/README.md` called
  `LOLSTATS_RIOT_API_KEY_EXPIRES_AT` "consumed by nothing" and the metric a
  "Dead gauge", `docs/runbooks/enable-alert-delivery.md` said the repair was
  "in flight in another lane" and that no rule may read the gauge, and
  `docs/runbooks/key-rotation.md` / `docs/runbooks/ingest-down.md` sent readers
  to the metric without saying it had only just started working. They now
  record the repair, the measured value (2026-09-18: `1023.833030043` at
  00:28:16Z and `1099.692236706` at 00:29:31Z, against `/readyz
  riot_key_age_seconds 1043 -> 1075`), and the surviving objection that the
  reading is process key lifetime rather than key age. `deploy/README.md` also
  now states the expiry guard's truthful production status: wired
  (`secretKeyRef` with `optional: true` at `deploy/base/ingest/deployment.yaml`)
  but **unarmed**, because Secret `lolstats-riot` carries only `RIOT_API_KEY`.

- Demo fixture data published contradictory numbers. `web/scripts/make-fixtures.mjs`
  drew each number independently, so the tier list, champion page,
  champion-by-role page and matchup matrix disagreed about the same
  (patch, champion, role) cell, matchup matrices named champions no tier list
  published, and 16.17 linked to champion pages whose 16.18 snapshot held no
  games. The generator now replays one simulated corpus of games and derives
  every view as a projection of it, so tier lists, champion pages, role pages
  and matrices agree by construction and the previous window only keeps
  champions the newest snapshot also publishes.
- Demo fixture data shipped cells a real build would drop: cells with `n = 0`
  carrying a tier, pick and ban rate, and matchup pairs below `min_cell_n`. The
  generator now applies the publication policy - no games means no cell, below
  the floor means withheld and counted - and build rows carry a win rate that is
  their own `wins / n` rather than an independently drawn number.
- `web/scripts/check-fixture-consistency.mjs` - reconciles the fixture tree
  across views (tier list, champion artifact, matchup axis and pairs, manifest
  counts) and fails on any cell below the floor or without games.
- `web/scripts/check-fixture-render.mjs` - reconciles a built site instead of the
  JSON: every tier-list link resolves to a champion page that publishes the same
  cell, every matchup role board is the tier list's champion set, and no
  rendered cell is below the floor.
- The demo generator published a manifest it did not back, so the tree failed the
  project's own verifier with 175 problems. It applied `.slice(0, 40)` to the
  champion detail artifacts, so 16.18 advertised 80 champions with 40 files and
  16.17 advertised 40 with none, and it carried the artifact envelope's `schema`
  and `source` into the manifest's `partitions[]`, which the Partition schema does
  not declare. It now writes one `champions/<id>.json` per champion that has a
  published cell in each partition, and its windows are days (`YYYY-MM-DD`), not
  instants, as the contract states. `verify` reports ok, 0 problems.
- Both fixture guards were blind to a missing champion artifact, because they only
  listed the artifacts that were on disk. `check-fixture-consistency.mjs` now
  requires one artifact per listed champion id and no unlisted file, requires the
  file name to be the champion id the site reads it by, and reports unreadable
  JSON as a named failure instead of falling out of a read as an uncaught
  `SyntaxError`; `check-fixture-render.mjs` checks the same closure on the tree it
  renders and fails when a champion page does not render the detail artifact the
  tree ships for it.
- Both guards were manual-only, so a tree that did not back its manifest could
  still ship: a site built from such a tree renders fewer champions than the
  manifest advertises and neither the build nor CI noticed. `web/package.json`
  now runs the tree check as `prebuild` and the rendered check as `postbuild`, so
  `npm run build` - the command CI's `verify` job and the cluster's nightly
  site-build Job both run - fails before publishing. `check-fixture-render.mjs`
  defaults to `web/dist` and `web/src/fixtures/v1`, derived from its own
  location, so it needs no flags wherever it runs.
- The built site made provenance claims the data could not support. In the demo
  and no-data states, 1038 champion pages still said "The numbers come from
  Riot's MATCH-V5 match feed", `/about` described an ingestion and aggregation
  pipeline and an archived append-only MATCH-V5 record while nothing had been
  ingested, and 121 pages emitted JSON-LD with
  `measurementTechnique: "Aggregated from Riot MATCH-V5 match records"` over
  synthetic fixtures. Every provenance sentence, heading, structured-data claim
  and empty-state explanation is now written from the manifest's `source`, so the
  demo state says the numbers are illustrative and that no Riot match data has
  been ingested, the no-data state claims no numbers at all, and only the
  `riot-match-v5` state describes MATCH-V5. `web/src/lib/seo.ts` throws rather
  than emit a Dataset `measurementTechnique` in any other state, so a page that
  forgets the state fails the build.
- The footer served a paraphrase of the non-endorsement notice on all 1063 pages
  while the frozen sentence from `web/src/lib/legal.ts` reached only 4. Both
  footers now render `NON_ENDORSEMENT_TEXT` itself, so the published sentence is
  byte-for-byte the approved one everywhere.
- `scripts/compliance-check.sh` could not see that drift. Check 6 now fails when a
  built page states the non-endorsement notice in wording other than the frozen
  sentence, when any built page carries no notice at all, and when either
  editable footer stops importing `NON_ENDORSEMENT_TEXT`; check 8 now fails on a
  reserved placeholder hostname whether or not `LOLSTATS_SITE_URL` is set, and
  requires every canonical, the sitemap and `robots.txt` to name one origin.
  Each scan's work directory is now named after its process, so two concurrent
  gate runs can no longer delete each other's tally files and report "0 of 0
  pages"; every check re-creates that directory before writing to it, and a run
  that cannot re-create it fails with that reason instead of reporting a clean
  result.
- A build with `LOLSTATS_SITE_URL` unset shipped `https://lolstats.example.invalid`
  in all 1063 canonicals and the sitemap. `web/astro.config.mjs` now publishes
  `https://lol.erik-schuetze.dev`, the address this deployment is served from,
  and logs that it did, and it refuses a reserved hostname or a relative URL with
  a build error rather than publishing a wrong canonical.
- The tier list, champion and matchup pages attributed withheld cells to "the
  aggregator" even in the demo and no-data states, where no aggregation has run
  over the numbers on the page. The sentences now state the publication
  threshold and what the artifact omits, which is true in all three states, so
  no page asserts an aggregation step that produced the data it is showing.
- `scripts/compliance-check.sh` reads its build from `web/dist`. A reviewer who
  has to check a specific data state can now point it at a snapshot with
  `LOLSTATS_DIST`, which changes only which files are read, never a rule.

- A raw archive that could not be written put the crawl into an unbounded hot
  loop: the archive failure was requeued with the same jittered delay and the
  attempt ceiling was never consulted, so 200 rows were re-fetched 34 and 35
  times with `-max-attempts 3` in force and 6860 requeues were logged in two
  minutes against a read-only archive volume. An archive failure now advances
  the attempt budget like any other failure, so the row reaches the ceiling and
  is dead-lettered with the `archive` cause intact, the batch stops as soon as
  its writes fail, and the archive-then-database order is unchanged: a failed
  archive write still leaves `matches` untouched.
- A `Retry-After` longer than the per-call timeout was classified as a shutdown
  and requeued with no delay at all: `Retry-After: 90` against the 10s per-call
  timeout surfaced as `DeadlineExceeded`, the row was logged "job released
  before shutdown" during a run in which no shutdown happened, and eight rows
  were released exactly 10.01s apart - the documented route to a suspended key.
  The 429 now reaches the worker as a rate limit with its `Retry-After` intact
  whether or not the wait outlives the call, and the row is parked until that
  instant, so the wait is real rather than misclassified.
- A dead letter was revived without limit: the queue's conflict clause reset
  `attempts` to 0, so a poison match id that always answers 500 went
  dead -> pending -> claimed -> dead on every rediscovery and cost a fresh
  attempt budget each time (10 -> 20 -> 30 origin requests over three walks for
  one match that can never succeed), with no bound and no visible change in the
  database state. The revival is now counted and bounded
  (`fetch_queue.revivals`, three per row, `0003_revival_budget`), so a
  permanently failing row is retired instead of paying for a Riot call per walk,
  while an explicit `maintain -replay-dead-letters` still returns dead letters
  to the queue, which is how work retired during a key outage is recovered.
- Recovery after a `SIGKILL` depended on the hourly `maintain` cron: the rows a
  killed worker had claimed stayed `claimed`, an unassisted restart recovered
  none of them, the crawl could not resume until `maintain` ran, and its 15m
  grace on an hourly schedule put a crash up to ~75 minutes behind. A starting
  worker now reclaims claims older than the same 15m grace before its first
  poll, so a restart resumes on its own; claims younger than the grace are left
  alone, because a claim that fresh can belong to a live peer.
- A graceful `TERM` stranded the rest of the claimed batch: the row in flight
  was released, the nineteen rows of a twenty-row batch that had not started
  were not, so a plain `SIGTERM` left up to `-job-batch` rows invisible to every
  worker until the claim grace expired. Shutdown now hands the unstarted rows
  back before the loop exits, and the half of the batch that was already
  archived is finished rather than abandoned: the writer refuses a flush on a
  cancelled context, so that final flush - and only it, plus the update that
  closes the rows it made durable - runs on a context that outlives the stop.
  A batch whose flush fails for a real reason is still left claimed, where a
  boot's reclaim picks it up, and a shutdown that lands while a row at the
  attempt ceiling is being fetched releases it for retry rather than retiring it
  against its will.
- Re-walking a match the control plane already held appended a second record
  for it to the raw archive, which has no key and so could not collapse it: the
  live crawl path never asked the `matches` table, whose row is written after
  the payload is archived, whether the match was already stored (the backfill
  path has asked since it was written). A second archive record is a second game
  in every count taken over the archive, so the aggregate published inflated
  rates beside a de-duplicated sample size. The worker now closes a row whose
  match is already recorded without fetching it, so the second record is not
  written in the first place. A store that cannot answer the question is treated
  as "not archived" and the row is fetched, because the answer only decides
  whether a fetch is skipped: one duplicate record costs a wrong number, while
  treating a control-plane outage as "already stored" would drop the fetch of a
  match that may not be archived at all.
- The aggregate published a win rate, pick rate and ban rate counted over a
  duplicated archive population next to an `n` counted over distinct matches:
  `matches_used` was `count(DISTINCT match_id)` while the feature rows, the cell
  tallies and the rate denominators were `count(*)`, so a match archived twice
  produced cells of `n=11` for a nine-match window and a pick rate of 0.6111
  over the same nine games - a published rate its own stated sample size does
  not support, and one a reader cannot detect. The spill of the raw archive is
  now one row per match, keeping the first record in part order, which is the
  copy the `matches` table holds; every statement that reads the archive agrees
  by construction rather than by each one remembering to count distinct. A
  payload whose match id cannot be read keeps a key of its own, so a malformed
  row is still counted and still fails the build closed.

- The matchup heatmap announced a measurement on its diagonal. The cell where a
  champion meets itself on both axes says in its `aria-label` that it is "the
  same champion in both axes", but the detail line the runtime writes above the
  matrix read the cell's `data-n="0"` placeholder and reported "win rate over 0
  games, percentage points versus even" for every matchup board (5 of 5 routes,
  by hover and by arrow key), so the page stated a win rate it cannot back. The
  diagonal is now answered as not applicable - recognised by position and by the
  `.self` class, before any number is read - at the single place the detail text
  is composed, so hover, tap, `focusin`, the arrow keys and `Home`/`End` all say
  that a champion is never matched against itself and never name a rate or a
  delta, and the server-rendered `aria-label` now says the same. The cell still
  prints no number, so the no-JavaScript page is unchanged.
- A click on a table's sort control was dead at five scroll offsets.
  `header.ds-navbar` is fixed at 51px, so at scrollY 550-590 on every table route
  a click at the centre of `[data-ti-dir]` landed on the bar and did nothing at
  all: the direction, the first row and the URL were unchanged (a swallowed click
  does not navigate either). Two surfaces inside the bar covered what they did not
  paint - the bar's own translucent frame, and the Matchups disclosure `summary`,
  whose `::after` caret carries a leading space that extended its box a whole
  character past the label. The bar frame is now `pointer-events: none` with every
  interactive or opaque surface inside it re-enabling itself, the caret is placed
  out of flow at `calc(100% + 0.6em)` so the summary's box stops at its label, and
  the document scroller carries `scroll-padding-top` while fragments, links,
  buttons, `summary`, tabstops and form fields carry `scroll-margin-top`. What the
  change achieved is narrower than "the dead clicks are gone": the bar's inert
  frame no longer takes a click anywhere, and the disclosure's hit area is now
  exactly what it paints - its box plus the caret glyph - so the covered part of
  the control shrank from the whole bar to that box. It cannot make a control that
  a reader has manually scrolled under the fixed bar clickable at the pixels the
  disclosure covers, because that `summary` is interactive, it is legitimately on
  top there, and the control is not sticky (the only `position: fixed` element in
  `web/src` is `Nav.astro`). Independent verification measured 84 scroll offsets
  across the band in both sort states: 27 offsets have part of the control covered
  (scrollY 555-581; 52-61 of its 110 px in `desc` and 52-61 of its 102 px in
  `asc`), no offset leaves it wholly covered, and a click at a covered offset
  aimed at a pixel the bar does not cover is delivered and toggles the direction
  in both states. A centre click is delivered in `desc`, but in `asc` the label is
  8px narrower and its centre (x 463) lands inside the disclosure box, so that
  click opens the Matchups menu instead. The claim that a centre click toggles the
  direction at all 199 sampled offsets was not tested at those offsets at all: the
  measurement sampled the control's centre pixel only, only in the state the page
  loads in, and ran its click test only at the first offset at which that single
  pixel found an occluder - of which it found none. The bar's paint and layout are
  unchanged: the closed bar is pixel-identical to the previous build (0 differing
  pixels at scrollY 0/560/570/580) and the caret's ink does not move.
- The ingest log could not distinguish a throttled crawl from a stopped one, so
  a healthy run read as a stall. Absorbed 429s were logged at WARN
  (`"attempt":1`, once a minute on a development key ridden at its `20:1`
  application ceiling), the probe that would have shown the loop advancing was
  `Debug` (`Log.Debug("limiter state", ...)`) and therefore invisible at
  `LOLSTATS_LOG_LEVEL=info`, and the only other progress lines were `Debug`
  too. One hour of `--tail` output was 100% `WARN "riot rate limited"` on a
  crawler that was fetching ~44 matches a minute, which is the silent staleness
  this pipeline is required to report instead of hiding. The report interval now
  writes one line at Info - `crawl pipeline status` with `matches_retained`,
  `frontier`, `staleness`, the limiter's `advertised` window and
  `effective_rps` - and promotes it to a WARN once `staleness` passes the hour
  the staleness alert holds for (`crawl.StaleWarnAge`), so the numbers that
  freeze in a stall are the ones the log carries. A 429 the call goes on to wait
  out and retry is logged at Debug; a 429 that ends the call (Riot's
  `Retry-After` past the retry wait budget, or the last attempt) is still a
  WARN. The 429 counters are unchanged on both paths, so
  `LolstatsRiotRateLimited` reads what it always did.

- The ingest exited `1` when Postgres was not yet accepting connections at
  startup, so its recovery from a cluster event was the kubelet restarting a
  crashed container - a crash loop with backoff, not a retry - and every event
  that brought Postgres and the ingest up together cost the pipeline the whole
  backoff while `newest_fetched_at` went stale. `store.Open`'s single ping was
  fatal, which is also what the `Ping` doc comment denied: it said a failing ping
  "is not fatal anywhere in the crawler ... the worker retries". The comment was
  false at the only call site that mattered, which is why no gate caught it. The
  startup connect is now a bounded retry - a 60 s code default window
  (`store.Options.ConnectWindow`, negative to opt out), 500 ms doubling to an
  8 s cap, each attempt's ping clamped to what is left of the window - that logs
  one WARN per failed attempt with the attempt number and the wait, one INFO if
  it took more than one, and on a dependency that never arrives one ERROR plus an
  error still carrying the `store: connect:` prefix, so existing greps and alerts
  keep matching. `Ping` is unchanged and `/readyz` stays honest: it still reports
  not-ready while the database is unreachable. The ingest startup probe's
  `failureThreshold` goes 12 -> 24 (60 s -> 120 s) because the metrics listener
  only starts after the connect returns, so the old budget would have killed the
  container as the retry succeeded.

- `/sitemap.xml` and the served pages were computed from two different sources, so
  they disagreed in both directions on the live edge. The page builders decide
  indexability per page; the sitemap ignored them and enumerated its 1058 routes
  from the manifest alone, advertising `/champions/ahri/top` (whose page renders
  `noindex,follow`) and omitting `/explore` (whose page renders `index,follow`).
  `routeList` now filters champion and role routes through the same predicate the
  page builders use - `championOverviewIndexable` and `championRoleIndexable` in
  `view_champion.go`, extracted behaviour-preserving - and appends `/explore`, and
  the package doc comment that claimed the two "agree by construction" states what
  actually holds. `sitemap_invariant_test.go` asserts `in_sitemap ==
  page_is_indexable` in both directions over every route the tier can serve, with
  `/explore` and `/champions/ahri/top` as positive controls; the test fails with
  1707 violations against the old `routeList`. No page's rendered `robots` meta
  changed (1063 routes compared before and after), while the local sitemap went
  from 1063 routes to 247 and the compliance gate's corpus, which is built from
  the sitemap, to 248 captured pages - above every floor. `/robots.txt`'s blanket
  "every page here is public and meant to be indexed" and the 404's note both
  described the unfiltered sitemap and now say what is true.

### Notes

- Every subcommand of `lolstats-ingest` and `lolstats-aggregate` is implemented.
  The scaffold's placeholder exits were removed as the crawl loop, the ingest
  jobs and the DuckDB build step landed; the entries above record those
  changes.
