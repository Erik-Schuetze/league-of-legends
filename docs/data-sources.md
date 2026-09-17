# Data sources

Where every datum comes from, under what terms, and what has actually been
verified. The Phase 0 gate table at the end of this page is the evidence; until
a gate is run its status is **not yet run**.

## Sources in v1

| Source | Provides | Basis | Load-bearing |
| --- | --- | --- | --- |
| Riot MATCH-V5 | Match summaries: participants, champions, roles, items, runes, summoner spells, win, team bans | Riot API Terms; development key under the General Policies | Yes |
| Riot LEAGUE-V4 | Ranked ladder entries per tier and division, used to seed the crawl frontier | Riot API Terms | Yes |
| Riot ACCOUNT-V1 | PUUID resolution and account identifiers | Riot API Terms | Yes |
| Data Dragon | Champion, item, rune and summoner spell static data plus patch versions | Riot's permitted static data / press kit | Yes |
| Community Dragon | Supplementary static assets where Data Dragon is incomplete | Community-run mirror of Riot static data | Optional |

Third-party aggregator scraping is **not a source in v1**. It is designed for
behind the `source_toggles` switch, disabled by default, never load-bearing, and
subject to the enablement checkpoint in `docs/compliance.md`. Enabling it
requires a recorded robots.txt and ToS review with a `review_due_at` date.

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

## Attribution and disclosure

- **Rank is a snapshot, not a live property.** A frontier entry records the tier
  and division a PUUID was discovered at. A player who climbs does not move in
  our data. This is disclosed on the site, and it is the reason per-rank pages
  are gated behind G0.6.
- **Sample sizes are published.** Every cell carries `n`; cells below
  `min_cell_n` are suppressed and counted rather than shown. See
  `docs/contracts.md` section 1.
- **No MMR, ELO or skill-rating calculator.** Not in v1 and not on the roadmap.
- **The free tier is free and ungated.** No account, no paywall, no data
  brokerage.
- **Riot does not endorse this project.** The non-endorsement disclaimer is
  rendered by `Footer.astro` on every page.

## Phase 0 gates

Each gate has a pass condition and a defined response to failure. Gates G0.1 to
G0.3 can invalidate the product concept, so they run first and sequentially;
G0.4 to G0.6 run in parallel afterwards.

| Gate | Question | Pass condition | If it fails | Status |
| --- | --- | --- | --- | --- |
| G0.1 Data access | Does a development key actually return what v1 needs? | ACCOUNT-V1, LEAGUE-V4 and MATCH-V5 reachable; a real payload confirmed to contain `teams[].bans`, `participants[].item0..item6`, `perks.styles`, `summoner1Id`/`summoner2Id`, `teamPosition`, `individualPosition` and `win` | Drop the unsupported feature from v1 rather than infer it; record the finding here | not yet run |
| G0.2 Rate-limit reality | Are limits observable and adaptive behaviour possible? | `X-App-Rate-Limit` and `X-Method-Rate-Limit` observed on live responses; a deliberate over-rate produces 429 with `Retry-After` | Fall back to a conservatively configured static limiter with a large safety margin, and document the assumption | not yet run |
| G0.3 Statistical sufficiency | Can a personal-key crawl produce credible numbers? | A bounded sample of EUW ranked-solo matches yields a role-level, rank-aggregated grid whose median cell reaches roughly +/-2% | Publish fewer champions or a coarser role set; defer rank brackets and say so on the site | not yet run |
| G0.4 Pipeline feasibility | Does raw-to-artifact fit a nightly window, and what does it cost in disk? | A sample archive is read by DuckDB and produces `tierlist.json` well inside the nightly budget; measured compressed bytes per match and projected storage for a year | Reduce the retention window, tighten the sample, or move to a weekly cadence with a documented trade-off | not yet run |
| G0.5 Serving feasibility | Can Caddy do the caching job required? | Caddy built with `cache-handler` via `xcaddy` demonstrates a cache hit, brotli compression and correct immutable headers on hashed assets | Serve with plain `file_server` and adjust the performance budget honestly | not yet run |
| G0.6 Rank attribution | Is the snapshot-drift limitation acceptable? | LEAGUE-V4 seeding works, and the wording that discloses snapshot attribution is drafted | Publish no per-rank pages in v1; ship a single rank-aggregated view | not yet run |
| G0.7 Legal posture | Is every source's basis written down and defensible? | This page records each source's legal basis; the compliance checklist and disclaimer text are drafted; the scrape toggle is designed with a `review_due_at` field | Remove the source from the design rather than argue for it | not yet run |
| G0.8 Key path | Is the path to a production key realistic and started early? | Compliance pages and `riot.txt` planned, and the application timing understood as weeks to months | Treat the production key as unavailable and design v1 permanently around a smaller scope | not yet run |

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
