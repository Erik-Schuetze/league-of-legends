# ADR-001: Riot API as the data spine, scraping as an optional cross-check

- Status: accepted
- Date: 2026-09-17
- Decision: D1

## Context

The product is a League of Legends statistics site: win rates, pick rates, ban
rates, builds and matchups by champion, role and patch. Every number on it has to
come from somewhere, and there are only three candidate grounds:

1. The official Riot API - ACCOUNT-V1, LEAGUE-V4 and MATCH-V5 - plus Data Dragon
   and Community Dragon for static data.
2. Scraping aggregators that already publish the same numbers.
3. Official esports data, which is pro-play only.

Option 1 has a hard cost that options 2 and 3 do not: a single crawl is capped by
Riot's rate limits, so building a credible sample takes weeks of steady, polite
crawling rather than an afternoon of HTTP.

## Decision

The Riot API is the spine. ACCOUNT-V1 supplies PUUIDs, LEAGUE-V4 seeds the crawl
frontier from the ranked ladder, MATCH-V5 supplies the matches themselves. Static
data comes from Data Dragon, with Community Dragon as a supplement.

Scraping of robots-permissive aggregators is designed for but **disabled by
default, never load-bearing, and never a substitute for a missing API field**.
It is a cross-check on a number we already publish, gated behind
`source_toggles` with a recorded review and a `review_due_at` date.

## Alternatives considered

**Scraper-first ingestion.** Cheaper to write, and it produces a full dataset in
days rather than weeks. Rejected because it makes the product's existence depend
on undocumented, bot-protected third-party endpoints. The project would inherit
a contractual liability and a permanent maintenance liability - every site
redesign breaks the pipeline - for a portfolio project that has no reason to
carry either. It also cannot be the foundation for anything the aggregators do
not already publish.

**Official esports data only (GRID Open Access, Oracle's Elixir).** Legally the
cleanest option and genuinely interesting data. Rejected as the spine because it
answers a different question: pro-play statistics say what the top few thousand
players do, which is not what a visitor looking up a champion build in Silver
wants.

**Hybrid: scrape to bootstrap, switch to the API later.** Rejected because the
bootstrap data would have to be discarded or disclosed separately, and the
crawler - the actual engineering work - would still have to be written.

## Consequences

- The dataset grows at the rate Riot's limits allow. A first credible patch takes
  weeks of crawl time, not days.
- The rate limiter becomes the most dangerous component in the project: it is
  what stands between a bug and a revoked key. It is owned in-house and reviewed
  adversarially (see D10).
- The raw archive becomes non-regenerable after two years, so it is treated as
  primary data with a tested off-site restore as a launch gate.
- Publishable statistics are constrained by what MATCH-V5 summaries actually
  contain. Skill orders would need timelines, which v1 does not fetch, so the
  section ships empty.

## Reversal trigger

If a production key is refused, delayed indefinitely, or revoked. The fallback is
not "scrape harder" - it is to narrow to a source that is permitted, most
plausibly official esports data, and reframe the product around pro play. The
raw archive, the aggregate contract and the site would survive that change; only
the ingest side would be replaced.

A second, milder trigger: Riot changes the match payload in a way that removes a
field a v1 feature depends on. That is handled by shipping a new transform
version and rebuilding derived data from the archive, not by revising this
decision.
