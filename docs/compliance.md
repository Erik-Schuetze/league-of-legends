# Compliance

Riot's rules are a launch gate, not a review step. This page is the checklist
and the evidence log. A checkpoint is either satisfied with a recorded artifact,
or it is open; there is no "probably fine".

Nothing in this project is live yet, so every checkpoint below is **open**
except where a row says otherwise.

## Checkpoints

| Trigger | Required action | Status | Evidence |
| --- | --- | --- | --- |
| Before any public launch | Terms of Service, Privacy Policy and the non-endorsement disclaimer published; `riot.txt` hosted at the registered domain; the free tier genuinely free and ungated; no MMR/ELO calculator anywhere; no data-broker behaviour | open | Disclaimer is rendered by `web/src/components/Footer.astro` on every page (contract frozen in `docs/contracts.md` section 3); the other pages do not exist yet |
| Before enabling any scraper | robots.txt and ToS reviewed and recorded in `source_toggles` with a `review_due_at` date, plus a documented decision | open | `source_toggles` table exists in `sql/migrations/0001_init.up.sql`; no toggle is enabled |
| Before publishing a new derived dataset or adding a game mode | Confirm Riot has not restricted publication of that data - Riot polices display, not only API access | open | No dataset beyond the v1 tier list, champion detail and matchup artifacts |
| On every Riot policy update | Policies are explicitly amendable; review on a fixed cadence and record the outcome | open | No review has been run; `docs/data-sources.md` lists the source pages to re-read |
| Before using any Riot asset | Riot Press Kit and permitted static data only; no champion art, splash art or marks beyond that | open | Static sync is planned to use Data Dragon icons only |
| Dependency changes | Maintain a licence inventory; run `make vuln` on dependency changes | open | `make vuln` target exists; no inventory file yet |
| If monetisation is ever considered | Stop and re-read Riot's transformative-use test before adding anything paid | not applicable | No monetisation is planned or implemented |

## Standing constraints

These are properties of the design rather than steps, and a change that breaks
one of them is a decision that needs an ADR:

- **No request-time Riot API access from the site.** The site reads
  pre-computed artifacts. A visitor's page view never causes a Riot API call.
- **No MMR, ELO or skill-rating calculator.** Not in v1, not on the backlog.
- **No paid tier and no gating.** The published data is free and unauthenticated.
- **`n` is published on every statistic**, and thin cells are suppressed rather
  than shown. See `docs/contracts.md` section 1.
- **Rank attribution is disclosed as a snapshot.** The tier and division in a
  frontier entry are where a PUUID was discovered, not where it is now.
- **Secrets are never committed and never baked into an image.** The Riot key is
  an environment variable read by `internal/config`; `deploy/*/secret.yaml` is
  gitignored.

## Open compliance work

1. Draft the Terms of Service, Privacy Policy and disclaimer copy - blocked on
   the site name and domain (open question 1 in the plan).
2. Host `riot.txt` once a production key application is started. Note that Riot
   verifies the file from the domain being registered, so the domain must exist
   first.
3. Decide the public-preview posture while a production key application is
   pending: the plan's position is that the public site serves static seed data
   and Data Dragon content only, clearly labelled as a preview, with real crawled
   data unpublished until the key is approved. This is an owner decision and is
   not yet recorded as an ADR.
4. Build the licence inventory and wire `make vuln` into CI on dependency
   changes.

## Non-endorsement disclaimer text

The wording is rendered by `Footer.astro`; the approved sentence is frozen here
so the component and the legal pages cannot drift apart:

> This project is not endorsed by Riot Games and does not reflect the views or
> opinions of Riot Games or anyone officially involved in producing or managing
> Riot Games properties. Riot Games and all associated properties are trademarks
> or registered trademarks of Riot Games, Inc.

Changing this wording is a compliance change, not a copy change.
