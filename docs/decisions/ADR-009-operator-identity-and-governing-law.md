# ADR-009: Operator identity, the contact route, and a deliberately unnamed governing law

- Status: accepted
- Date: 2026-09-17
- Decision: partial; plan question 1 settled, then narrowed

## Context

Plan question 1 asks for a site name and domain. A legal surface needs more than
that: it needs a named person accountable for the service, an address to reach
them, and a jurisdiction whose law governs the terms. The operator is a private
individual running a non-commercial hobby project from a homelab in the European
Union, which makes the GDPR applicable and makes publishing a home address the
obvious wrong answer.

The tempting shortcut is an imprint-style clause naming the operator's country.
The problem is that the country has not been decided as a matter of published
legal identity, and a governing-law clause that names the wrong jurisdiction is
worse than one that is slightly general: it is a false statement in the clause a
reader would rely on if something went wrong.

## Decision

**Name the operator, publish a mailbox, and do not name a member state yet.**

`web/src/lib/legal.ts` is the single source of truth for all of it, so the four
compliance pages cannot disagree. That file was deleted with the Astro tree, then
with the Go tier that ported it into `internal/webtier/brand.go`, and finally that
tier went on 2026-09-18, so the constants below now live verbatim as the approved
wording in `docs/compliance.md`, which is the single source of truth:

- `OPERATOR_NAME = 'Erik Schuetze'` and `OPERATOR_IDENTITY = 'Erik Schuetze, a
  private individual resident in the European Union, who operates this site as a
  non-commercial hobby project.'`
- `SITE_NAME = 'LoL Stats'`, with `FALLBACK_SITE_URL =
  'https://lol.erik-schuetze.dev'` answering question 1 in the absence of a
  different answer. The build reads `LOLSTATS_SITE_URL` first, so the deployed
  address wins when the coordinator passes it.
- The contact route is an email address, `lolstats@erik-schuetze.dev`, set by
  `OPERATOR_CONTACT_EMAIL` and overridable at build time by
  `LOLSTATS_CONTACT_EMAIL`. The override exists so that changing the address is a
  deployment change rather than a code change. A mailbox rather than a postal
  address is deliberate: the operator is a private individual and the GDPR's
  practical requirements are met by a reachable contact route, not by a street
  address on a public page.
- The governing-law clause reads: the law of the EU member state in which the
  operator resides, with that member state's courts having jurisdiction over
  disputes that a consumer is not entitled to bring locally **and** an express
  preservation of the reader's mandatory consumer protections and local forum
  rights.

The terms page states *why* the member state is unnamed, so the omission reads as
a recorded open item rather than an oversight. `scripts/compliance-check.sh`
check 7 required the contact address to appear on all four compliance pages, so
the route cannot quietly disappear.

> **Note, 2026-09-18.** That script was deleted with the web tier
> (`ADR-011-retire-the-web-tier.md`), so the requirement above has no check behind
> it any more. The decision itself is unchanged: the operator identity and the
> contact address are the approved wording in `docs/compliance.md`, and obligation
> 7 there is what a future serving layer has to satisfy.

## Alternatives considered

**Name a member state now.** Rejected: choosing one to fill a clause would be
deciding the operator's published legal identity by accident, and the clause most
likely to be relied on is the worst place to guess.

**Publish a postal address.** Rejected: unnecessary for the service, and it
converts a hobby site into a permanent public record of where a private
individual lives.

**Use a web form or an issue tracker as the contact route.** Rejected: a form is
a submission path this site otherwise does not have, and it would create the
personal data the privacy policy currently does not process. A repository issue
tracker is not a channel for a data-protection request.

**Two contact addresses, one for data protection and one general.** Rejected for
a project of this size: a single monitored mailbox is more likely to be read, and
a second address nobody monitors is worse than none.

**No governing-law clause at all.** Rejected: silence does not help a reader, and
the clause costs nothing if it preserves consumer protections explicitly.

## Consequences

- Plan question 1 is answered in the code for the purposes of the legal surface,
  with `LOLSTATS_SITE_URL` as the override. The coordinator should pass it, or
  the canonical URLs and the legal copy will name different hosts.
- The member state is an open item the owner must settle. Until then it appears
  in `docs/compliance.md` and the terms page states the omission openly.
- The GDPR position in `/legal/privacy` is built on the same identity: an EU
  operator, no accounts, no analytics, no cookies, one ordinary web-server log.
  Because the site processes no personal data of its own, the policy says so
  plainly and points at the contact route for the log rather than inventing an
  export or erasure workflow for data that does not exist.
- Changing the contact address is one constant or one environment variable, and
  gate check 7 verifies the result.

## Reversal trigger

The owner settles the published legal identity or the member state, moves
jurisdiction, changes the site name or the domain, or begins processing personal
data the privacy policy does not describe. Any of those re-opens this ADR and the
governing-law clause with it.

**Verify in Phase 0:** whether the owner wants the member state named, and
whether the site name and domain in `FALLBACK_SITE_URL` are the final ones.
