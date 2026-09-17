# ADR-008: No third-party ingestion; the scraper stays disabled behind a review date

- Status: accepted
- Date: 2026-09-17
- Decision: D1, made concrete; answers plan open question 4

## Context

D1 makes the Riot API the spine of the project and leaves third-party scraping as
an optional cross-check. Plan section 3.4 establishes that
robots-permissive is not the same as permitted, and that Riot's terms cover
derived data and display, not only API access. Plan section 15 question 4 asks
whether the cross-check is wanted at all.

The failure mode to design against is not a dramatic one. It is that a scraper
gets added quietly because it is convenient for a feature, and the project then
depends on a source it cannot defend. Aggregators such as `op.gg`, `u.gg`,
`lolalytics`, `jungler.gg` and `replays.lol` all publish their own derivation of
Riot data, and republishing that derivation is not a way around Riot's terms - it
adds a second party's terms on top of them.

## Decision

**No third-party source is ingested, and no scraper exists in the crawl.** The
position is recorded in `docs/data-sources.md` and enforced in three places:

1. **Absence, not a flag.** `sql/migrations/0001_init.up.sql` defines
   `source_toggles (source, enabled NOT NULL DEFAULT false, decided_by,
   decided_at, review_due_at, notes)` with a partial index `WHERE enabled = true`,
   and **no row is seeded**. Absence means off. `internal/contract` documents that
   an optional source is off unless a row enables it, and `internal/store/runs.go`
   is the only writer, so enabling one requires a named `decided_by` and a
   `review_due_at`.
2. **Never load-bearing.** The site must work with every optional source off,
   because a switch that breaks the product is not a switch.
3. **A recorded exclusion list.** `u.gg` and `leagueofgraphs.com` are excluded
   permanently. `op.gg`, `lolalytics.com`, `jungler.gg` and `replays.lol` are not
   candidates unless conditions 1-7 in `docs/data-sources.md` are all met.

The review date is **2027-03-17**. It is the date by which the question is
re-decided, not a date on which anything switches on. Until plan question 4 is
answered, the answer to "should we enable it" is no, and the register says the
review is outstanding rather than approved.

Seven conditions must all hold before enablement, and none is sufficient alone: a
recorded decision with an ADR; a recorded robots.txt and ToS review of the
specific target kept as evidence; a conclusion that *republishing the
derivation* is permitted rather than merely that crawling is not forbidden; no
login, no challenge bypass and no user-agent spoofing; crawling hygiene
(descriptive User-Agent with a contact URL, honoured crawl delay, conditional
requests, sitemap-driven discovery, off-peak scheduling, exponential backoff on
`429`/`403`); attribution with the project staying free; and the source staying
non-load-bearing.

## Alternatives considered

**Use an aggregator's numbers to validate our own.** Rejected even as a
cross-check: a validation path that reads a third party is still a path that
depends on one, and it invites the source to become load-bearing once a
discrepancy is interesting. Riot's own API can validate a transform, because the
raw archive is retained and re-readable.

**Scrape only `robots.txt`-permissive targets.** Rejected as a rule, because
permission to crawl is not permission to republish, and the second is what this
project would need.

**Scrape esports or open-data sources instead.** Not rejected on principle -
`oracleselixir` and `grid.gg/open-access` are different cases - but they are
esports data rather than the solo-queue ladder, so they do not serve the v1
product and they would need their own review under trigger 3.

**Keep the toggle enabled-by-default and turn it off in configuration.**
Rejected: an enabled-by-default switch fails open. The safe state has to be the
absence of a row.

## Consequences

- Any v1 feature that would need third-party data has been cut rather than
  deferred behind a temporary scrape.
- The project is slower and more expensive to populate than a scraper-based
  competitor would be. That is the accepted cost.
- The `source_toggles` scaffolding exists and is unexercised. That is deliberate:
  it makes the decision visible rather than leaving the enablement path
  undocumented.
- `docs/compliance.md` trigger 2 stays "not applicable" while no row exists, and
  a future row moves it to "requires a recorded review", which is a checkpoint
  rather than a formality.

## Reversal trigger

The owner answers plan question 4 affirmatively **and** a specific target passes
all seven conditions. A target that requires solving a challenge, spoofing a
User-Agent or logging in is not a candidate at any point.

**Verify in Phase 0:** whether any named target's terms or robots posture have
changed. Second-hand summaries are not evidence; the posture is read first-hand
and recorded at review time.
