# Data sources

Where every datum comes from, under what terms, and what has actually been
verified. The Phase 0 gate table at the end of this page is the evidence: each gate
is reported there as run and passing, run and failing, or explicitly waived with the
authority for the waiver named. No gate is left unstated, and none of the four that
cannot be run without a Riot API key is reported as passing.

**Last reviewed: 2026-09-17. Next review due: 2026-12-17** (same quarterly
calendar as the Riot policy review in `docs/compliance.md`, because a source is
only permissible for as long as the terms that permit it hold).

## The sanctioned primary source, and why

**Riot's own APIs are the only primary source.** `ACCOUNT-V1`, `LEAGUE-V4` and
`MATCH-V5` are the sanctioned way to obtain the ladder and match data this site
publishes, and using them is what keeps the project inside the Riot API Terms
rather than arguing about them afterwards. Three properties make the API
strictly better than any alternative, not merely safer:

1. **It is authoritative.** A match read from MATCH-V5 is the match Riot
   recorded. A number scraped from an aggregator is that aggregator's
   interpretation of the match, one derivation further from the source, with no
   way to check it.
2. **It is addressable and attributable.** MATCH-V5 returns structured fields -
   `teams[].bans`, `participants[].item0..item6`, `perks.styles`,
   `summoner1Id`/`summoner2Id`, `teamPosition`, `win` - so the aggregation is a
   transform over declared fields rather than a parse of someone else's markup.
   A scraper breaks whenever the target redesigns.
3. **It is permitted.** Data Dragon ships the static data, and the API Terms
   grant an application the access it needs provided the application does not
   become a data broker, an MMR calculator or a monetised derivative.

The cost is real and accepted: a development key expires every 24 hours, rate
limits are read from response headers rather than from documentation, and a
production key requires a live, compliant site first. That is the trade the
project makes deliberately.

## Sources in v1

| Source | Provides | Basis | Load-bearing |
| --- | --- | --- | --- |
| Riot MATCH-V5 | Match summaries: participants, champions, roles, items, runes, summoner spells, win, team bans | Riot API Terms; development key under the General Policies | Yes |
| Riot LEAGUE-V4 | Ranked ladder entries per tier and division, used to seed the crawl frontier | Riot API Terms | Yes |
| Riot ACCOUNT-V1 | PUUID resolution and account identifiers | Riot API Terms | Yes |
| Data Dragon | Champion, item, rune and summoner spell static data plus patch versions | Riot's permitted static data / press kit | Yes |
| Community Dragon | **Not used.** Would supply supplementary static assets where Data Dragon is incomplete | Community-run mirror of Riot static data | No - no reference exists in the source tree, and gate check 2 fails any image origin other than the Data Dragon CDN, so enabling it is a deliberate compliance change rather than a code tweak |

Third-party aggregator scraping is **not a source in v1**. It is designed for
behind the `source_toggles` switch, disabled by default, never load-bearing, and
subject to the enablement checkpoint in `docs/compliance.md`. The full position,
including the permanently excluded targets and the date the decision must be
re-reviewed, is in "Scraping: disabled by default" below.

## What the raw archive retains

Every Riot API response is written verbatim and compressed to
`raw/riot/<api>/v<n>/dt=YYYY-MM-DD/` before the control plane is updated. The
`dt=` component is the **fetch date**, not the game date, so a late-arriving
match lands in the partition of the run that fetched it and a partition stays
append-only and complete.

This matters more than a cache would: Riot retains matches for two years and
timelines for one. Once a payload ages out of the API it is unrecoverable, so
the archive is primary data and not disposable. It is the project's only
long-horizon asset and it is the reason a transform change is additive rather
than destructive.

Known limitation: no off-site copy exists yet. A fire or theft is a total loss of
a non-regenerable asset, and a tested off-site restore is a launch gate.

## Retention and rate limits, and what they force

Two facts about Riot's API shape almost every other decision.

**Retention is finite and asymmetric.** Riot retains matches for **two years**
and match timelines for **one**. A payload that ages out is unrecoverable, so the
archive is primary data rather than a cache. Consequences:

- The raw archive is append-only and never pruned. A partition is the **fetch
  date**, not the game date, so a late-arriving match lands in the partition of
  the run that fetched it and a partition stays complete.
- The first run has to capture as much as it usefully can, because the window it
  is racing is closing rather than opening.
- A transform change is additive rather than destructive: reprocessing the
  archive is free, re-fetching it is impossible.
- Anything derived from timelines has a **one-year** horizon, not two. A feature
  that needs timeline depth has to be justified against a shrinking window.

**Rate limits are read, never assumed.** The published changelog is stale, so the
limits that actually apply are read from `X-App-Rate-Limit` and
`X-Method-Rate-Limit` on every response; `internal/config` holds only two
configured values, the conservative cap used before the first response arrives
and the ceiling the adaptive limiter must never exceed. They are deliberately far
below a development key's real allowance, because an over-eager limiter costs
crawl throughput while a wrong one costs the key. `KeyExpiresAt` exists for the
key-age alert that the 24-hour development-key expiry makes necessary, and a
deliberate over-rate is expected to produce `429` with `Retry-After`, which the
crawler must honour rather than retry through.

## Scraping: disabled by default

**No scraper is enabled. No third party's data is ingested, derived from or
republished anywhere in this project.** That is the state today, not a policy
that has yet to be applied: there is no scraper code path in the crawl, the
`source_toggles` table exists in `sql/migrations/0001_init.up.sql` with
`enabled boolean NOT NULL DEFAULT false` and **no row is seeded**, so absence
means off, and `internal/contract` documents that an optional source is off
unless a row enables it. Enabling one also requires a named `decided_by` and a
`review_due_at`, because both columns exist for exactly this purpose.

### The position on the named targets

Candidate targets were probed first-hand (2026-09-17). The register below covers
all eight named in the brief, whether named as usable or as forbidden, because a
target that was *rejected* still needs its rejection recorded.

| Target | Position | Reason |
| --- | --- | --- |
| `u.gg` | **Excluded permanently** | Everything, including its `robots.txt`, is behind a 403 Cloudflare challenge. Reaching it would mean circumventing an access control, which moves the exposure from a contract dispute to an access-control fact pattern. The brief says "do not build on this" and that stands |
| `leagueofgraphs.com` | **Excluded permanently** | Its `robots.txt` explicitly disallows `/api/*`, `/*match/*`, `/*summoner/*`, `/*live/*` and `/*search/*`, and pages return 403. A crawling prohibition is an explicit refusal, not an absence of permission |
| `op.gg` | Not permitted today; **excluded in practice** | Robots-permissive, and it publishes a tolerance policy for non-commercial, attributed, low-volume crawling - so crawling is not the obstacle. Republishing its derivation of Riot's data is, and that is the question that matters |
| `lolalytics.com` | Not permitted today; **excluded in practice** | Permissive `robots.txt` (only `AmazonAdBot` rules). Same reason as `op.gg`: its numbers are its own derivation of Riot data, not Riot data |
| `jungler.gg` | Not permitted today; **excluded in practice** | Permissive `robots.txt` with a sitemap. Same reason |
| `mobalytics.gg` | Not permitted today; **excluded in practice** | `Allow: /` with narrow exceptions. Same reason |
| `deeplol.gg` | Not permitted today; **excluded in practice** | `Allow: /` plus a sitemap. Same reason |
| `replays.lol` | Not permitted today; **excluded in practice** | Same category: another party's derived dataset, with no licence to republish it |
| **All of the above** | **Not ingested today** | No scraper exists in the crawl at all, so no named target is currently read, derived from or republished anywhere in this project |

Two distinct reasons produce those two verdicts, and conflating them is the
usual error. `u.gg` and `leagueofgraphs.com` are excluded because reaching them
at all requires overriding an explicit refusal. The other six are excluded even
though nothing forbids crawling them, because **robots-permissive is not the same
as permitted**: a target that does not forbid crawling has not thereby granted a
licence to republish its derivation of someone else's data. The question that
matters is not "can this be fetched" but "may this be republished". Riot's terms
also cover derived data and display, not merely API access, so republishing
another aggregator's numbers is not a way around them.

### Review due

The decision itself is recorded in
`docs/decisions/ADR-008-no-third-party-ingestion.md`, together with the seven
conditions that must all hold before enablement.

| Field | Value |
| --- | --- |
| `source` | *(none)* - no row is inserted, because the decision is not to ingest |
| `enabled` | `false`, the column default, and by the absence of a row |
| `decided_by` | *(unset)* - a decision to ingest would have to name one |
| `review_due_at` | **2027-03-17** - the date by which the question is re-decided, not a date on which anything switches on |
| Next action | Answer plan open question 4: is scraping wanted at all? Question 4 has not been answered, so the answer to "enable it" is no |

### Conditions under which it may ever be enabled

All of the following, and none of them is sufficient alone. This list is the
enablement checkpoint in `docs/compliance.md`, trigger 2.

1. **A named decision.** A row in `source_toggles` with `decided_by`,
   `decided_at` and a `review_due_at`, plus an ADR recording the reasoning.
2. **A recorded robots.txt and ToS review** of the specific target, kept as the
   evidence rather than summarised.
3. **The target is not on the permanently excluded list**, and the review
   concludes that republishing its derivation is permitted - not merely that
   crawling is not forbidden.
4. **No login, no challenge bypass, no user-agent spoofing.** Never solve or work
   around a Cloudflare or Turnstile challenge, never present a browser
   User-Agent, never touch anything behind an account. A target that requires any
   of those is not a candidate at all.
5. **Crawling hygiene:** a descriptive User-Agent carrying a contact URL, the
   declared crawl delay honoured, conditional requests, sitemap-driven
   discovery, off-peak scheduling and exponential backoff on `429` or `403`.
6. **Attribution and non-monetisation.** The source is cited, and the project
   stays free - the no-monetisation posture is what keeps a derived dataset
   defensible, and adding a paid tier would re-open this whole page.
7. **It is never load-bearing.** The site must keep working with the source
   switched off, because the switch has to be usable.

## Data Dragon at build time

Data Dragon is fetched **at build time**, not
at request time and not by the crawler. It used to be a build step of the Astro
site (`web/scripts/fetch-ddragon.mjs`, deleted with `web/` on 2026-09-18); the
fetch is a manual one whose result is committed once, in `projection/*.json`. It
used to be committed twice, the second copy being the Go tier's embedded fallback
(`internal/webtier/data/*.json`, deleted with the tier on 2026-09-18), and a
drift test between the two copies was named in a comment and never existed. With
the tier gone there is one copy and the drift risk is gone with it. Updating it
needs
network access; nothing in the deployed pipeline does, and a reader's page view
never causes a Riot request.
The fetched data is Riot's permitted static data only - champion, item, rune and
summoner-spell names, icons and numeric ids, plus the patch version list. No
champion art, splash art, loading screen or Riot mark is fetched or shipped;
obligation 2 of `docs/compliance.md` is the standing rule that no image reference
may name an origin other than `ddragon.leagueoflegends.com/cdn/`.

## Attribution and disclosure

- **Rank is a snapshot, not a live property.** A frontier entry records the tier
  and division a PUUID was discovered at. A player who climbs does not move in
  our data. This is disclosed on the site, and it is the reason per-rank pages
  are gated behind G0.6.
- **Sample sizes are published.** Every cell carries `n`; cells below
  `min_cell_n` are suppressed and counted rather than shown. See
  `docs/contracts.md` section 1.
- **No MMR, ELO or skill-rating calculator.** Not in v1 and not on the roadmap.
  It is a hard Riot prohibition, and obligation 1 of `docs/compliance.md` is the
  standing rule: a rating-like identifier, key, column or displayed value must not
  appear in Go, SQL, TS, JSON, HTML or CSS.
- **The free tier is free and ungated.** No account, no paywall, no data
  brokerage.
- **Riot does not endorse this project.** The non-endorsement notice is the
  approved sentence itself rather than a paraphrase of it. It used to be one
  constant, `NonEndorsementText` in `internal/webtier/brand.go`, rendered by the
  footer and the legal pages and read by the compliance gate; the tier and the
  gate were deleted on 2026-09-18, so the sentence now lives as the approved
  wording in `docs/compliance.md`, which is the authority on when it has to
  appear and in what words. See that page for the exact coverage rather than a
  summary of it.
- **The data state is stated on the page.** Every page discloses its patch,
  region, queue, bracket and the aggregate manifest's `source` - `demo`,
  `riot-match-v5`, or no data - so a reader is never told that preview numbers
  are live ones.

## Phase 0 gates

Each gate has a pass condition and a defined response to failure. Gates G0.1 to
G0.3 can invalidate the product concept, so they run first and sequentially;
G0.4 to G0.6 run in parallel afterwards.

| Gate | Question | Pass condition | If it fails | Status |
| --- | --- | --- | --- | --- |
| G0.1 Data access | Does a development key actually return what v1 needs? | ACCOUNT-V1, LEAGUE-V4 and MATCH-V5 reachable; a real payload confirmed to contain `teams[].bans`, `participants[].item0..item6`, `perks.styles`, `summoner1Id`/`summoner2Id`, `teamPosition`, `individualPosition` and `win` | Drop the unsupported feature from v1 rather than infer it; record the finding here | waived 2026-09-17 - no key existed then, and the gate has not been re-run since the development key arrived (**D-1**); see the waiver note below |
| G0.2 Rate-limit reality | Are limits observable and adaptive behaviour possible? | `X-App-Rate-Limit` and `X-Method-Rate-Limit` observed on live responses; a deliberate over-rate produces 429 with `Retry-After` | Fall back to a conservatively configured static limiter with a large safety margin, and document the assumption | waived 2026-09-17 - no key existed then, and the gate has not been re-run since the development key arrived (**D-1**); part of the fallback is built, see the waiver note below |
| G0.3 Statistical sufficiency | Can a personal-key crawl produce credible numbers? | A bounded sample of EUW ranked-solo matches yields a role-level, rank-aggregated grid whose median cell reaches roughly +/-2% | Publish fewer champions or a coarser role set; defer rank brackets and say so on the site | waived 2026-09-17 - no key existed then, and the gate has not been re-run since the development key arrived (**D-1**); see the waiver note below |
| G0.4 Pipeline feasibility | Does raw-to-artifact fit a nightly window, and what does it cost in disk? | A sample archive is read by DuckDB and produces `tierlist.json` well inside the nightly budget; measured compressed bytes per match and projected storage for a year | Reduce the retention window, tighten the sample, or move to a weekly cadence with a documented trade-off | waived 2026-09-17 by the owner - a measured fail, not a pass; no realistic input fits the shipped 1 GiB; see the G0.4 waiver note below |
| G0.5 Serving feasibility | Can Caddy do the caching job required? | Caddy built with `cache-handler` via `xcaddy` demonstrates a cache hit, brotli compression and correct immutable headers on hashed assets | Serve with plain `file_server` and adjust the performance budget honestly | pass 2026-09-17, one documented deviation; see the G0.5 note below |
| G0.6 Rank attribution | Is the snapshot-drift limitation acceptable? | LEAGUE-V4 seeding works, and the wording that discloses snapshot attribution is drafted | Publish no per-rank pages in v1; ship a single rank-aggregated view | waived 2026-09-17 - no key existed then, and the gate has not been re-run since the development key arrived (**D-1**); part of the fallback is drafted, see the waiver note below |
| G0.7 Legal posture | Is every source's basis written down and defensible? | This page records each source's legal basis; the compliance checklist and disclaimer text are drafted; the scrape toggle is designed with a `review_due_at` field | Remove the source from the design rather than argue for it | pass 2026-09-17 - artefacts verified; see the G0.7 note below |
| G0.8 Key path | Is the path to a production key realistic and started early? | Compliance pages and `riot.txt` planned, and the application timing understood as weeks to months | Treat the production key as unavailable and design v1 permanently around a smaller scope | pass 2026-09-17 - see the G0.8 note below |

### Gate runs, 2026-09-17

Four of these gates can be settled without a Riot API key. Two of them, G0.4 and
G0.5, were **run**; the other two, G0.7 and G0.8, were **assessed** against the
artefacts their pass conditions name. The remaining four were not run at all, so they
are **waived, not passed**. G0.4 is a fifth waiver and a different kind of one: it *was*
run and it measured a fail, the fail is left standing in full below, and the owner
waives it with that measurement as the reason. Either way, a waiver is a decision with
a named authority, it is not evidence, and nothing below presents one as the other.

Raw transcripts are bulky and are not committed; they live under a gitignored
directory - `.agent-artifacts/g04/g04-raw.log` for the first pipeline run,
`.agent-artifacts/g04/verify-raw.log` - with the per-run `.agent-artifacts/g04/verify-*.out`
and `verify-rss-*.log` beside it - for the pipeline re-run, and
`.agent-artifacts/g05/g05-raw.log` for the serving run - and every figure quoted
here is a line of output in one of them.

**G0.4 Pipeline feasibility - fail, then waived by the owner (see the waiver note at the
end of this subsection; the waiver does not make it a pass).** The input was **synthetic,
not a real raw
archive**: there is no raw archive in the repository and no key to make one, so the
run cloned the hand-written fixture payloads in
`fixtures/agg/raw/riot/match-v5/dt=*/matches.jsonl` (hand-written per
`fixtures/agg/README.md`), rewrote their match ids, timestamps and winners, and
converted them with the project's own archive recipe. **Nothing in this measurement
describes real Riot payload sizes**, so this gate cannot be settled on it.

**This is the first of two runs and its numbers are superseded.** It measured the
engine before its `memory_limit` was pinned, so nothing below describes the current
code. It is kept on record because it is why the engine changed, and because its time
and disk figures are what the re-run is compared against. The re-run is the next
sub-section, and it is the one that carries the verdict.

- The aggregate build runs: with the pinned DuckDB 1.4.5 (`make duckdb`) it produced
  a `tierlist.json` and a full partition for 20 000 cloned matches in 6.7 seconds and
  for 100 000 in 118 seconds, about 1.2 ms per match. The nightly window is
  `activeDeadlineSeconds: 7200` (`deploy/base/jobs/aggregate.yaml:29`), so the
  documented 500 000-match patch projects to roughly ten minutes. **Time is not what
  fails.**
- Compressed size: 235.9 bytes per match at fixture payload size, 2 946.9 bytes per
  match when payloads are inflated to the ~100 KiB summary size this page assumes for
  a real match. At 500 000 matches per patch and about 26 patches a year that is
  ~3 GB/year and ~38 GB/year. The plan's own real-world figure (~7-8 GB per patch,
  ~15 KB/match compressed) implies ~196 GB/year and the synthetic corpus cannot
  confirm it - a template clone compresses far better than varied JSON - so the low
  end is a floor the real archive will exceed.
- **Memory is what failed.** The engine shells out to `duckdb -batch` with no
  `memory_limit` and no thread cap (`internal/aggregate/engine.go:179`) and hands every
  part in the window to a single statement, so DuckDB sizes itself against the host and
  not the container: its own default limit here is 12.7 GiB, four times the 3 Gi limit
  (`deploy/base/jobs/aggregate.yaml:68`). That statement alone peaked at 3.59 GiB for
  20 000 fixture-size matches and 8.00 GiB for 100 000 - already over the limit - and
  with payloads at the documented real size the four-day window dies outright with
  `Out of Memory Error: failed to allocate data of size 2.0 MiB (12.7 GiB/12.7 GiB
  used)`, on just 5 712 matches, after the engine itself had reached 7.84 GiB. The
  three-day window built but peaked at 6.17 GiB, twice the limit, and the one- and
  two-day windows produced a build that failed the minimum cell sample rather than
  failing on memory. A nightly job killed for memory publishes no `tierlist.json` at
  all, so "produces `tierlist.json` well inside the nightly budget" is not met at
  fixture payload sizes either. The gate **fails**; it is not waived, because what it
  measures can be measured without a key even though the input is not real.
- The fixture-scale path is healthy: `go test ./internal/aggregate/ -run Fixture`
  passes, and the synthetic run did publish suppressed-cell counts rather than widening
  cells below the minimum sample.

This gate's own fallback - reduce retention, tighten the sample, go weekly - does not
address the cause the first run measured, which is engine memory rather than the window.
What that measurement pointed at was a `memory_limit`, a thread cap and per-part batching
in `internal/aggregate/engine.go`. That change has since been made, and the re-run below
measures it: the ceiling is now real, and the gate is still a fail for a narrower reason.

#### G0.4 re-run, 2026-09-17: the ceiling holds, the window still does not fit

The first run's cause - no `memory_limit`, so DuckDB sized its buffer manager from the
host - has since been fixed in `internal/aggregate/engine.go`: every statement is pinned
with `memory_limit`, `threads`, a spill `temp_directory` and `max_temp_directory_size`,
the spill directory is probed at open so an unwritable one fails loudly, and the CLI runs
`-bail` so a rejected `SET` cannot fall back to the host default. `deploy/base/config.yaml`
ships `LOLSTATS_AGG_DUCKDB_MEMORY_LIMIT: 2GiB`, `THREADS: "2"`, `TEMP_DIR: /tmp`,
`MAX_TEMP_SIZE: 10GiB`, and the pod is `readOnlyRootFilesystem: true` with an `emptyDir`
at `/tmp`. **The fix is real and it is in force.** It also did not settle the gate, and
this sub-section is a re-run of the gate rather than a review of the fix: what follows is
measured against the shipped defaults, on a 10-CPU/16 GiB host, against the pod's 3 Gi
limit (`deploy/base/jobs/aggregate.yaml:68`), with every DuckDB child wrapped so peak RSS
is attributed to the engine statement rather than to the build's process tree
(`.agent-artifacts/g04/verify-run.sh`, `verify-rss-*.log`, `verify-*.out`, and the
side-by-side transcript `verify-raw.log`).

- **The ceiling is in force, read back from DuckDB itself - not inferred from the
  source.** `.agent-artifacts/g04/verify-probe.sh` captures the engine's own stdin script
  and replays it with `SELECT current_setting(...)` appended, so these values come out of
  the statement the engine actually ran. At the shipped defaults both invocations printed
  `limit=1.0 GiB threads=2 temp_directory=<host temp>/duckdb-spill
  max_temp_directory_size=10.0 GiB` - `memory_limit` and `threads` are the shipped values,
  and all four settings reach the statement instead of being left to DuckDB. Driving the
  four environment variables moves the same readback (`limit=1.4 GiB threads=3
  temp_directory=<dir>/duckdb-spill max_temp_directory_size=4.0 GiB`, build still ok), so
  the plumbing works in both directions. The temp path in the first readback is the
  engine's own fallback (`os.TempDir()` plus `/duckdb-spill`, `engine.go:199`), because
  this local build ran without the deployment config overlay; `TEMP_DIR: /tmp` is the
  pod-side value, and the override run shows a configured value arriving verbatim. The
  same host with DuckDB's defaults reports exactly the first run's behaviour:
  `default limit=12.7 GiB threads=10 temp_directory=.tmp`.
- **At fixture payload size the ceiling bites inside the corpus itself.** 20 000 cloned
  matches (~9.9 KiB payloads, 14 parts, 4.72 MB) built in 5.7 s, published 390/395 cells
  and peaked at 2.139 GiB - inside the 3 Gi pod, with ~0.86 GiB to spare. 50 000 and
  100 000 matches (both 4 and 5 parts) stopped with DuckDB's own
  `Out of Memory Error: failed to allocate data of size 16.0 MiB (1008.7 MiB/1.0 GiB
  used)` after 0.15 s, peak 0.85-0.98 GiB, exit 1. The 500 000-match patch such a window
  covers is an order of magnitude larger still, so at fixture payload size the ceiling is
  reached at a tenth of the patch - and fixture payload size is ten times smaller than the
  ~100 KiB payload this page assumes for a real match.
- **At the payload size this page assumes for a real match, not even one day fits.** With
  ~100 KiB payloads of realistic width (99.9 KiB per match, a handful of long fields
  rather than filler leaves) a **one-day** window of 1 428 matches dies on the first
  statement at the shipped 1 GiB, at 1 500 MiB, and at 2 GiB; a 14-day window of 20 000
  such matches dies at 1 GiB and at 2 GiB (`verify-narrow1d*.out`, `verify-narrow14d*.out`).
  Narrowing the window is therefore not a lever at this payload size, which is the one
  thing the first run left open.
- **The failure is now bounded, loud and diagnostic - that is the real improvement.**
  Every failure above is DuckDB's own accounting, returned in 0.15-0.33 s as exit 1, at a
  peak RSS never above 1.1 GiB and never near the 3 Gi limit: no kernel kill, no silent
  fallback, the applied bounds logged at start-up, the build run recorded `status: failed`
  and no `tierlist.json` published. Bounded, loud, honest failure is still failure.
- **Raising the limit moves the wall instead of removing it.** At 2 GiB the 100 000-match
  fixture-size build completes when the corpus is rotated into 5 parts of 20 000, and peaks
  at **4.087 GiB** - past the pod - while the same 100 000 in 4 parts of 25 000 still fails
  at 2 GiB. The knob that admits more input is the knob that puts the job back outside the
  container.
- **Where the memory goes, measured on the engine's own statement.** Replaying the captured
  first statement (`.agent-artifacts/g04/probe-capture/script-0.sql`, the guarded envelope
  in `internal/aggregate/extract.go`) with a row `LIMIT` shows the shipped 1 GiB holds
  **400 such rows and not 600**: ~2 MiB of buffer per 100 KiB payload, because the
  `CASE WHEN json_valid(payload) THEN json_extract_string(...)` envelope pays a full
  per-row JSON parse. Nothing in that statement is unnesting participants yet, so the
  ceiling is reached before aggregation starts. The fix's own summary agrees: peak tracks
  payload bytes in flight.
- **Disk: same kind of number as the first run, still synthetic.** 235.9 bytes per match at
  fixture payload size and 3 137.7 bytes per match at ~90 KiB payload size, on the same
  kind of cloned corpus. The plan's ~15 KB/match (~196 GB/year) is still not confirmable
  without a real archive, and the ~100 KiB archive used above compresses far better than
  varied JSON, so no disk figure is quoted from it.
- **Time is still not what fails.** 20 000 fixture-size matches in 5.7 s and 100 000 in
  34.7 s is 0.28-0.35 ms per match against a 7 200 s deadline.

**Verdict: G0.4 is still a fail, and for a narrower reason than either the first run or
the fix suggested.** The ceiling is enforced and the failure mode is now a bounded,
diagnostic `Out of Memory Error` instead of a kernel kill, but no input that resembles the
gate's own premise fits: on the ~400-row statement ceiling measured above, a 500 000-match
patch is roughly 1 250 such statements, a single day of ~100 KiB payloads exceeds every
limit that keeps the job inside the 3 Gi pod, and 20 000 fixture-size matches - the largest
corpus that still builds - already needs 2.1 GiB. It is not a pass, and no part of this is
evidence that the real archive fits: the input is still synthetic and its bytes still say
nothing about real Riot payload size.

The fix's recommended follow-up was chunking the extract per partition or window-day
(`internal/aggregate/extract.go`). The re-run refines that: the statement that fails runs
against **one** day-partition, so a chunk the size of a day is already too large. The chunk
has to be smaller than a day - row ranges, or a cheaper per-row JSON path than a guarded
`json_*` call per field on the whole document. That is build code under its own review and
is not changed here.

#### G0.4 waiver, 2026-09-17: a measured fail, waived by the owner

Everything above is the measurement, and it stands unchanged: **G0.4 does not pass, and this
waiver does not make it pass.** What follows is the decision, which is a different kind of
statement, and it is labelled as one.

- **What is waived.** The memory half of the pass condition - that a sample archive is read
  by DuckDB and produces `tierlist.json` well inside the nightly budget. The time half is
  **met, not waived** (20 000 fixture-size matches in 5.7 s, 100 000 in 34.7 s, 0.28-0.35 ms
  per match, against `activeDeadlineSeconds: 7200` at `deploy/base/jobs/aggregate.yaml:29`).
  The disk half is **unmeasured rather than failed**, because compressed bytes per real match
  cannot be measured without real payloads.
- **The evidence this waiver rests on**, quoted from the runs above and not rounded in the
  waiver's favour:
  - at the shipped 1 GiB limit, a 20 000-match window of ~9.9 KiB payloads **builds** and
    peaks at **2.139 GiB**; two independently written harnesses agree to within the margin
    they measure (~2.09 GiB and ~2.139 GiB). They are different measures - peak RSS versus
    DuckDB's own accounting - and this note does not conflate them;
  - at the ~99.9 KiB payload width this page assumes for a real match, a **single day's**
    window of ~1 428 matches dies at the shipped 1 GiB, at 1 500 MiB, and at **2 GiB**, so
    there is no limit that still fits the 3 Gi pod at which one day of realistic input fits;
  - the engine's **first** statement exhausts 1 GiB at roughly **400** rows of that width
    (~2 MiB of buffer per 100 KiB payload), so the ceiling is hit before aggregation begins.
- **Why the gate's own documented response to failure does not apply.** That response is
  "reduce the retention window, tighten the sample, or move to a weekly cadence with a
  documented trade-off", and all three shrink the input window while the measurement above is
  that a **one-day** window is already too large. The fallback therefore cannot be invoked as
  if it addressed the measured cause. No entry in the risk register covers the
  aggregate engine's memory ceiling either, so this waiver is that risk's record rather
  than a reference to an existing one.
- **What must not be read into this waiver.** "Chunk per window-day" - the follow-up recorded
  above - is **insufficient**: the statement that fails already runs against one day-partition,
  so a chunk the size of a day is a chunk the size of the failure. The chunk has to be
  **sub-day** (row ranges, or a statement per part), or the per-row JSON envelope has to get
  cheaper than a guarded `json_*` call per field per row. A waiver that repeated the
  insufficient fix as the way out would be a false statement.
- **The caveat that caps everything above.** Every payload width here is **synthetic**. There
  was no Riot API key at the time, so no real payload-size distribution existed to measure, and the
  independent verifier of this gate could not measure one either and said so. (A
  **development** key arrived later the same day - **D-1**, 2026-09-17 - and real crawled
  matches are now served, but this gate has not been re-run against that distribution, so
  the caveat still stands as written.) If real payloads
  are narrower than ~100 KiB this waiver is measured against a corpus wider than reality; if
  they are wider, reality is worse than the numbers above. Neither direction is known.
- **Exit condition - what lifts this waiver.** Any one of these, measured rather than argued:
  1. the one-day, ~1 428-match, ~99.9 KiB case builds and publishes `tierlist.json` at the
     shipped 1 GiB limit without DuckDB's `Out of Memory Error`, with peak RSS recorded; or
  2. a real (non-synthetic) payload-size distribution is measured from a real archive and the
     nightly window at that distribution fits the shipped limits on the pinned DuckDB; or
  3. the extraction path is reworked to sub-day chunks, or to a cheaper per-row JSON path, and
     this gate's pass condition is re-run successfully at the shipped defaults.
  Until one of those is measured, G0.4 stays waived - and section 14's "all Phase 0 gates pass
  or are explicitly waived" is then satisfied here by the waiver, not by a pass.
- **Authority.** The repository owner, **Erik Schuetze**, accepts the residual risk. This
  Phase 0 work is executed on the owner's behalf under an explicit delegation, and a residual
  risk accepted by a delegate is the owner's; the authority is named as the owner rather than
  the agent that measured the failure. The delegation is what makes the risk the owner's, and
  it is also what bounds this waiver: the owner has not personally reviewed the runs above,
  and a one-line instruction from the owner reverses this waiver and returns G0.4 to `fail`.
- **Date.** 2026-09-17, the date of the runs this waiver rests on.
- **Review.** Re-open when the exit condition above becomes measurable, and at the latest when
  a production or development key exists - which is also what unblocks G0.1, G0.2, G0.3 and
  G0.6.

**G0.5 Serving feasibility - pass, with one deviation and one fix.** Demonstrated
against the real image (`build/caddy/Dockerfile`: Caddy 2.10.2 built with
`cache-handler` v0.16.0 and `storages/otter` v0.0.18 through `xcaddy`) serving a copy
of the real `web/dist` build, 1063 pages, over local HTTP.

> **Retired 2026-09-18.** The Astro build, `web/`, `build/caddy/Dockerfile` and the
> inner Caddy were deleted on 2026-09-17, and the Go tier that replaced them went on
> 2026-09-18 (`docs/decisions/ADR-011-retire-the-web-tier.md`), so **every path
> cited in this subsection points into a tree that no longer exists and nothing
> serves anything at all**. The measurements below stand as the record of what was
> run on 2026-09-17; they are not a description of anything deployed. What survived
> the deletion is the conclusion: the storage module returned a byte-exact *prefix*
> of a cached page, so a cache that can serve a truncated page is worse than no
> cache. The serving script that used to be run against the tier is gone too, so
> none of this is asserted by anything now.

- **Cache hit.** Two identical requests: `Cache-Status: Souin; fwd=uri-miss; stored`,
  then `Cache-Status: Souin; hit; ttl=59; ... detail=OTTER`.
- **Compression.** A client advertising `Accept-Encoding: zstd, br, gzip` receives
  `Content-Encoding: zstd`; one advertising gzip only receives `Content-Encoding:
  gzip`. The gate's literal word "brotli" is **not achievable with this image**:
  `caddy list-modules` lists `http.encoders.gzip` and `http.encoders.zstd` and no
  brotli encoder. That is the deviation already recorded at
  `deploy/base/web/caddyfile.yaml:76-83`, and zstd is the stronger of the two on these
  payloads, so the deviation is in the gate's wording rather than in the capability it
  was testing.
- **Immutable headers on hashed assets were missing and were fixed here.** Nothing in
  the tree set `immutable` or a long `max-age` on `/_astro/*` before this run, so the
  gate could not have passed as written. `deploy/base/web/caddyfile.yaml:118-128` now
  sets `Cache-Control: public, max-age=31536000, immutable` for that path, and a cached
  hit reports `ttl=31535983`.
- **The cap, reported as asked.** `max_cacheable_body_bytes 2048`
  (`deploy/base/web/caddyfile.yaml:64`) applies to the upstream body, so **HTML is not
  cached at all**: all 1063 pages are larger than it (mean 30.2 KiB, median 27.0 KiB,
  largest 234 121 bytes at `web/dist/matchups/bottom/index.html`), every page request is
  answered `fwd=uri-miss; detail=UPSTREAM-RESPONSE-TOO-LARGE` straight from disk, and
  only small assets are stored - 4 of the 6 `_astro` files (157, 163, 1 717 and 2 026
  bytes) plus `favicon.svg`, while the two larger ones (2 466 and 9 982 bytes) are
  served but never cached. Repeated page requests returned HTML byte-identical to
  `web/dist`.
- **The premise that a too-small cap poisons the cache is inverted here.** Raising the
  cap to 200 000 on the same image and content does not make HTML cacheable, it makes
  cached HTML *truncated*: a cached hit on `/about/` returned 3 790 bytes of a
  40 079-byte page - a byte-exact prefix of the real file, with both `PREVIEW` banner
  occurrences gone - and likewise `/champions/ahri/` (28 558 -> 3 790) and
  `/tier-list/support/` (56 299 -> 3 789), while the same URL served in full again once
  its 60-second entry expired, so a reader cannot tell which they received. That
  3 790-byte boundary is not the cap (which was 200 000) and not a compressed
  representation (zstd of the index page is 7 976 bytes): it is a fixed limit inside the
  storage module, reached at a different page size only by chance. **The
  shipped 2048-byte cap is load-bearing**: it is what keeps HTML out of a cache that
  would return damaged pages, and raising it would need the storage defect fixed first.
- `sh scripts/verify-serving.sh http://127.0.0.1:8095` exits 0 against this image.

**G0.7 Legal posture - pass.** Every artefact the pass condition names exists, line by
line:

- the source register with a legal basis per source - this page, above;
- the compliance checklist - `docs/compliance.md`, whose checkpoint register
  covers all seven plan triggers with a status, a reason and an artifact each;
- the disclaimer text - rendered by both footers on all 1063 built pages when this
  pass ran, and required word for word on `/disclaimer` by the compliance gate of
  the day, which scanned the served corpus for the marker `not endorsed by Riot
  Games`. That gate was deleted on 2026-09-18 and the pages it scanned were deleted
  with the tier, so the requirement now lives as approved wording plus obligation 6
  in `docs/compliance.md`. The text has moved twice: at the time of this pass it
  was the constant `web/src/lib/legal.ts` held, then the Go tier's
  `internal/webtier/brand.go`, and now `docs/compliance.md`.
- the `review_due_at` field on the scrape toggle -
  `sql/migrations/0001_init.up.sql:122`, upserted and read by
  `internal/store/runs.go:124` and `:158`.

**Verdict: pass.** The pass condition asks this page to *record* each source's legal
basis, and asks for a checklist and disclaimer text that are *drafted* and a scrape
toggle that is *designed* with a `review_due_at` field. All three are file-level
deliverables and all three exist, which the bullets above show line by line.

This page previously said "G0.7 stays 'not yet run' until the site is reachable and
the checklist is walked against it". That reading is withdrawn here: it adds a
deployment requirement the pass condition does not state, and the condition it sets -
walking the checklist against a live site - is a launch check rather than a Phase 0
gate question. The parts of it that cannot be closed without a deployment (`riot.txt`,
the retention setting on the live database, the sub-processor list in the live privacy
page) are tracked as checkpoint 1 in `docs/compliance.md` and are pending there.

**G0.8 Key path - pass.** The compliance pages exist and build - `/about`,
`/legal/terms`, `/legal/privacy`, `/disclaimer` - and the application timing is
recorded as weeks to months (`docs/decisions/ADR-010-public-preview-posture.md:11-12`; that ADR is
superseded 2026-09-17 by **D-1**/**D-4**, but the application-timing estimate it records is not the
part that changed - the key is still an application and still takes that long).
`riot.txt` is "planned" in the only sense that is honest: the tier used to
publish `/riot.txt` when and only when `LOLSTATS_RIOT_VERIFICATION_TOKEN` is set
(`internal/webtier/brand.go:44`, served from `cmd/lolstats-web/main.go`; before
2026-09-18 it was `web/astro.config.mjs`), and both that binary and the tier were
deleted on 2026-09-18, so **nothing publishes it today and no code would**. What
that mechanism enforced is preserved as obligation 5 of `docs/compliance.md` - a
page may claim Riot reviewed or endorsed the site only when the claim is true. The
disclaimer and terms wording recorded why absence is the correct state - Riot's
check reads that URL for the token and nothing else, so a placeholder is a false claim
of verification while an absent file is an honest "not verified yet". The token is
issued to the domain owner once a production application is under way, so publishing
the file is an owner action rather than code work, and a built-and-switched-off
mechanism is what "planned" should mean. This gate's own fallback is a posture that
ADR-010 already implements. **Superseded 2026-09-17:** the fallback this paragraph
names is no longer the posture the site deploys - the owner chose publication
(**D-1**) and waived the compliance workstream (**D-4**), so that fallback is
implemented nowhere. The gate's pass condition is untouched by that, and the
paragraph above is kept as the position that held until then.

**Waived gates: G0.1, G0.2, G0.3, G0.6.** None of these can be run without a Riot API
key, and none of them had one when it was waived. **Superseded 2026-09-17:** a
**development** key is now wired - the Secret `lolstats-riot`, referenced by the
ingest tier - and the pipeline serves real crawled matches from it (**D-1**), so the
ground these waivers were recorded on - that the environment lacked any Riot key
at all - no longer holds, and the "follows a public preview" reading of
`ADR-010` is superseded too (**D-4**). What holds the four open is narrower and is
still true: **none of them was re-run against that key**, so none of them has
evidence, and a waiver with no evidence is what they always were. The four
`ADR-010` and risk-R2 citations below are therefore the authority **as at the waiver
date**, kept as history: ADR-010 is superseded, and risk R2 is accepted by the owner
rather than open. They are **waived, not passed**. **G0.4 is waived on a different basis and is not a member of this
list** - all four below were never run, while G0.4 *was* run and *failed*; it is waived,
with its measured fail left standing, in its own note above and in the paragraph after
this list. Per gate, with the authority named:

- **G0.1 Data access.** Cannot run: ACCOUNT-V1, LEAGUE-V4 and MATCH-V5 all require a
  key, so no real payload can be inspected. Authority: the gate's own response to
  failure - "drop the unsupported feature from v1 rather than infer it; record the
  finding here" - with
  `docs/decisions/ADR-010-public-preview-posture.md` (superseded) and the accepted risk
  R2.
  The design's answer is that a field nobody has observed is not inferred, so no v1
  feature ships on one; with no payload observed at all, that is a design statement
  and not evidence, and it is not presented as evidence. The only payloads in the tree
  are the `fixtures/agg/` fixtures, which were written by hand to contain the fields
  this page requires, so they cannot confirm the field-presence half of this gate.
- **G0.2 Rate-limit reality.** Cannot run: the gate reads limits from live response
  headers and no live response is obtainable, so neither `X-App-Rate-Limit` /
  `X-Method-Rate-Limit` observation nor the 429-with-`Retry-After` behaviour can be
  seen. Authority: the gate's own fallback - "fall back to a conservatively configured
  static limiter with a large safety margin, and document the assumption" - with
  ADR-010 (superseded) and risk R2 (accepted). **The fallback is partly satisfied in
  code, stated separately
  from the waiver:** the limiter has a hard ceiling that response headers cannot raise
  (`internal/riot/limiter.go:21-31` and `:74-78`, clamped by `clampTo` at `:326`) and
  it starts from `DevelopmentKeyWindows()`, 20/s and 100 per 2 minutes so about
  0.83 req/s binding (`internal/riot/headers.go:49-83`), which is that conservative
  static limiter. What stays unevidenced is that the assumption matches reality, and
  only a key can settle that.
- **G0.3 Statistical sufficiency.** Cannot run: a bounded EUW ranked-solo crawl needs a
  key, so no median cell width can be measured. Authority: the gate's own fallback -
  "publish fewer champions, top-N only, or a coarser role set; defer rank brackets and
  say so on the site" - with ADR-010 (superseded) and risk R2 (accepted).
  **Separately from the waiver**, the
  guard that fallback depends on is built and was exercised in the G0.4 run: with a
  minimum cell sample of 100, 5 cells were suppressed and counted at 20 000 matches
  and 0 at 100 000 as the sample filled in, and the build fails closed rather than
  publishing a cell it cannot support. That shows the guard works; it says
  nothing about whether a real crawl reaches the target, which is why this stays a
  waiver.
- **G0.6 Rank attribution.** Cannot run: LEAGUE-V4 seeding, which is the observable
  half, needs a key. Authority: the gate's own fallback - "publish no per-rank pages in
  v1; ship a single rank-aggregated view" - with ADR-010 (superseded) and risk R2
  (accepted). The wording half
  was drafted in the deleted tier: `internal/webtier/prose.go` wrote the sentence a
  rate-bearing page appended when its rates were not measurements of real games
  (`dataSourceSentence`, rendered as `SourceSentence`), and the artifacts it read
  still disclose the manifest's `source` as described above. That Go file went with
  the tier on 2026-09-18, so the wording is now a requirement of a future reader
  rather than a thing that exists. Drafted wording is not a working
  seeding path, so the gate is waived.

One further gate is waived on a different basis, and it is the only one in this document
that was measured and still failed. **G0.4 Pipeline feasibility** was **run on
2026-09-17 and it does not pass**: at the ~99.9 KiB payload width this page assumes for a
real match, a single day's window of ~1 428 matches exhausts the shipped 1 GiB, then
1 500 MiB, then 2 GiB, and the engine's first statement alone exhausts 1 GiB at roughly
400 such rows. It is nonetheless **waived rather than left as a bare `fail`**, because
section 14 requires every Phase 0 gate to pass **or be explicitly waived**, and a bare
`fail` is neither. The waiver is not a pass and does not pretend to be one: the failing
measurement, the reason the gate's own fallback cannot address it, the caveat that **every
payload width was synthetic** (no Riot API key existed then, so no real payload-size distribution
could be measured, and the independent verifier of this gate could not measure one either),
and the measured exit condition are all in the G0.4 waiver note above. Authority: the owner,
**Erik Schuetze**, who accepts the residual risk - this Phase 0 work is executed on the
owner's behalf under an explicit delegation, so the authority is named as the owner rather
than as the agent that measured the failure. Date: 2026-09-17.

## Version-sensitive facts to re-verify in Phase 0

Riot production rate limits (read from response headers, never hardcoded - the
published changelog is stale), the Postgres major version, the DuckDB release to
pin, TimescaleDB's licence split if it is ever reconsidered, the Astro major
version, and every third-party bundle-size comparison that influenced a frontend
decision.

## Primary sources

`developer.riotgames.com/docs/portal` (rate limits, key types);
`developer.riotgames.com/docs/lol` (LoL policy, no-data-broker clause);
`developer.riotgames.com/policies/general` (General Policies);
`support-developer.riotgames.com` "API Terms and Conditions" and "Production Key
Applications" (live site, ToS, Privacy Policy and `riot.txt` required);
`riotgames.com/en/terms-of-service`; `riotgames.com/en/legal` (Riot IP policy);
`hextechdocs.dev/crawling-matches-using-the-riot-games-api/` (crawl patterns);
`riot-api-libraries.readthedocs.io/en/latest/specifics.html` (retention windows,
seed data); `ddragon.leagueoflegends.com/api/versions.json`;
`communitydragon.org/documentation`.
