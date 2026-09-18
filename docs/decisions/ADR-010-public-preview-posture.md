# ADR-010: A labelled public preview while the production key application is pending

> **SUPERSEDED 2026-09-17 by owner decisions D-1 and D-4.** The owner answered
> plan question 6 and chose the opposite posture: real crawled Riot match data
> from his **development** key is published and **the site serves it** (D-1),
> deliberately, behind the existing password gate; and the compliance workstream
> is **waived** (D-4). The decision text below is kept unchanged as the record of
> what held until that date; it is no longer the posture the site deploys. The
> value that now holds is in `deploy/base/web/go-deployment.yaml`. Register
> entries: `docs/compliance.md`, checkpoint 1 next step and gap 6.
>
> **And the site itself was retired on 2026-09-18**
> (`ADR-011-retire-the-web-tier.md`): the Deployment named just above,
> `web/src/lib/legal.ts`, the served templates and `scripts/compliance-check.sh`
> are all deleted, so every path and target the text below cites points into
> something that no longer exists, and nothing serves anything today. What
> survives this record is the wording and the requirement it protected, both of
> which are in `docs/compliance.md`.

- Status: Superseded (2026-09-17)
- Date: 2026-09-17
- Decision: new; interprets R2 and answers plan open question 6

## Context

Riot requires a live, publicly reachable site with published Terms of Service, a
Privacy Policy, the non-endorsement disclaimer and a hosted `riot.txt` before a
production-key application will be reviewed, and that review takes weeks to
months. A development key expires every 24 hours and is not a basis for a public
crawl. So there is a period - possibly a long one - in which the project must be
publicly reachable, compliant, and unable to publish real Riot match data.

The project also has a second, sharper problem: the site renders a full,
believable-looking tier list. Numbers on a page are read as claims. If a
preview's numbers are indistinguishable from live numbers, the site has made a
false statement about data it does not have, and it has done so in the one area
where Riot's rules are least forgiving.

Plan section 15 question 6 asks the owner to confirm the personal-key
interpretation used in risk R2. This ADR records what the project does in the
absence of that confirmation, chosen so that the answer cannot make it wrong.

## Decision

**A public preview build is served, and every page states its own data state.**

The site is publicly reachable during the key-application period, because that
reachability is itself a requirement of the application. It publishes the
preview aggregates and Data Dragon static data. **It does not publish crawled
match data until a production key is approved**, and no page claims that Riot has
verified, reviewed, approved or endorsed it.

The data state is derived from the aggregate manifest rather than from a build
flag. ADR-005 makes `source` a required field on `manifest.json` and on every
envelope, with exactly two legal values. `web/src/lib/site.ts` computes
`DataState = 'no-data' | 'demo' | 'live'` from `manifest.source`:
`riot-match-v5` is live, anything else is `demo`, and no manifest is `no-data`.
The banner, the About page and the legal pages all read that one value, so a page
cannot claim ingested match data while the manifest says the snapshot is a
preview.

Four properties are non-negotiable at every state:

1. **The state is stated, not implied.** A preview says it is a preview, in
   visible copy, on every route.
2. **No false compliance claims.** `web/src/lib/legal.ts` decides what may be
   said about Riot's verified-site requirement from whether *this build* was
   given `LOLSTATS_RIOT_VERIFICATION_TOKEN`. Unset means the site says the
   requirement is not met yet. Nothing in the copy can override that.
3. **No silent upgrade.** Moving from `demo` to `riot-match-v5` changes the
   manifest and therefore the copy. There is no second place to remember.
4. **The prohibition holds in all states.** No MMR, ELO or rating-like value is
   computed, stored or displayed at any data state; `make compliance` fails the
   build if one appears.

`scripts/compliance-check.sh` check 5 enforces property 2 by matching only the
positive form of a verification claim, and check 8 refuses to let a build that
was given `LOLSTATS_SITE_URL` keep the reserved placeholder hostname.

## Alternatives considered

**Publish nothing until the key is approved.** Rejected: Riot will not review an
application from a site that is not live, so this is a deadlock rather than a
safe default.

**Publish the preview without labelling it, and label it later.** Rejected. It
is the same false claim with a shorter duration, and it is the claim Riot is
least likely to accept.

**Serve the preview with a decorative "beta" badge rather than a data-state
statement.** Rejected: "beta" describes maturity, not provenance. It would leave
the reader unable to tell whether the numbers are real, which is the whole
question.

**Gate the preview behind a password until the key arrives.** Rejected: it would
mean the free tier is not ungated at launch, and it would fail the Riot
requirement that the site be publicly reachable.

## Consequences

- The preview is a genuine product page, not a placeholder, so it has to be good
  enough to be read as the site - which is why the four compliance pages carry
  real terms, a real privacy policy and real provenance rather than clauses to be
  filled in later.
- The manifest is the switch, so the transition to live data is a data change
  rather than a copy change, and it cannot be half-done.
- `docs/compliance.md` carries this as an open item until the owner answers
  question 6, and the register says so rather than assuming consent.
- The R2 risk stays open: a refused or revoked key leaves the site permanently
  on preview data, which is a worse but still honest product.

## Reversal trigger

The owner confirms or rejects the interpretation in plan question 6, a production
key is granted, or Riot's policy changes in a way that bears on preview-only
publication. If the interpretation is rejected, the response is to take the site
private again rather than to publish unlabelled data.

**Verify in Phase 0:** the wording of the current LoL policy and API Terms on
publication of non-API-derived data, re-read on the quarterly cadence recorded in
`docs/compliance.md`.
