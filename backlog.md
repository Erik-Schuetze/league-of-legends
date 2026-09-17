# Backlog

Work that has been deliberately deferred, with enough context to pick it up
without re-deriving the decision. Everything here is out of scope for v1. If
something is not here and not in the plan, it does not exist yet.

## Blocked on external input

**Record the public-preview posture as an ADR.** While a production key
application is pending, the public site should serve static seed data and Data
Dragon content only, clearly labelled as a preview. The reasoning is written in
`docs/architecture.md` and `docs/compliance.md`, but it is an owner decision and
has not been accepted as an ADR. Needed before the first deploy.

**Site name and domain.** Drives Caddy configuration, TLS and the `riot.txt` URL
that Riot verifies - and `riot.txt` must be served from the domain being
registered, so the domain has to exist first.

**Off-site backup media.** `restic` to an external drive or a second machine,
with a *tested* restore. The raw archive is non-regenerable after Riot's
two-year retention window, so until this exists a fire or theft is a total loss.
A tested restore is a launch gate.

**Cloudflare free tier, yes or no.** The only real mitigation for the
home-upload-bandwidth-is-the-CDN problem at zero cost, but it puts a managed
third party in the request path. Explicit opt-in, never a default.

**Charts on the champion page in v1.** Raises the island budget from one per
page to two. Answer before the champion page work starts.

## Deferred by design

**Rank-bracket segmentation.** `bracket` is `all` in v1. The path segment exists
from day one so that adding `emerald_plus`, `platinum_plus` and so on is
additive rather than a URL break. Gated behind G0.3 and G0.6: the sample has to
be big enough for per-bracket cells to clear `min_cell_n`, and snapshot rank
attribution has to be disclosed. Implementation is a new build per bracket plus
a manifest entry; nothing in the artifact contract changes.

**Patch-over-patch trends.** Would need the aggregate tree to be readable as a
time series rather than a set of snapshots, so it is a new artifact plus a new
route, not a change to `tierlist.json`.

**Player profiles.** A different product with a different privacy surface. Not
on the roadmap.

**Esports data (GRID Open Access, Oracle's Elixir).** Genuinely interesting and
legally clean, and the fallback if ADR-001's reversal trigger fires. Deferred
because pro-play statistics answer a different question from "what should I build
on this champion in Silver".

**Optional scraping cross-check.** Designed for behind `source_toggles`, disabled
by default, never load-bearing, never u.gg or leagueofgraphs, never bypassing a
challenge. Enabling it requires a recorded robots.txt and ToS review with a
`review_due_at` date. ADR-001 explains why it is not the spine.

**Match timelines and skill orders.** `skill_orders` is in the
`ChampionRole` contract and ships empty. Timelines are a second request per match
for data only this section uses, and Riot retains them for one year rather than
two. Adding them is a `RiotClient` method plus an archive prefix plus a real
sample-size analysis - a contract change with an ADR.

**Search.** A third island. Only worth it if a search feature survives into v1,
and it is a page-title and champion-name search or it is not worth the JS.

## Operational, once there is something to operate

- Prometheus alerts for crawl staleness, build staleness, elevated 429/403 rates,
  key age, per-cell sample size falling below the floor, raw-volume disk usage
  and build duration approaching the nightly window.
- `docs/runbooks/` currently holds no files. Ingest-down, rebuild-aggregates,
  key-rotation and restore-raw are all needed before the first deploy.
- A licence inventory for the dependency graph, wired to `make vuln`.
- A tested restore of Postgres from a logical dump.

## Explicitly not planned

MMR, ELO or any skill-rating calculator; a paid tier or any gating of the
published data; data brokerage; per-request database access from the public site;
enabling any scraper as a load-bearing source; and publishing a statistic without
its `n`.
