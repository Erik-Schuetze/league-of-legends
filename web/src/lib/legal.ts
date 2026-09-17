// The compliance surface's words, in one place.
//
// docs/compliance.md freezes the non-endorsement sentence and says that
// changing it is a compliance change rather than a copy change. The footer and
// the legal pages therefore read the same constant rather than each carrying a
// paraphrase, and a paraphrase cannot drift into the served HTML.
//
// Three things in this file are load-bearing beyond their prose:
//
//   * NON_ENDORSEMENT_TEXT and TRADEMARK_TEXT are single-line string literals on
//     purpose. `scripts/compliance-check.sh` extracts the first of them with sed
//     and asserts the built pages contain it, so re-wrapping the literal breaks
//     the gate rather than the copy.
//   * Every date on every legal page derives from EFFECTIVE_DATE. There is one
//     date in the repository and the pages print it; nothing is written twice.
//   * The address the site is served from and the operator's contact address are
//     configuration with a documented fallback, because a hardcoded hostname or
//     mailbox is the kind of claim that survives review and then turns out to be
//     untrue.

import type { DataState } from './site';

/** The approved non-endorsement sentence. Do not reword without a compliance change. */
export const NON_ENDORSEMENT_TEXT = 'This project is not endorsed by Riot Games and does not reflect the views or opinions of Riot Games or anyone officially involved in producing or managing Riot Games properties. Riot Games and all associated properties are trademarks or registered trademarks of Riot Games, Inc.';

/** The trademark sentence the brief asks for, naming League of Legends explicitly. */
export const TRADEMARK_TEXT = 'League of Legends and Riot Games are trademarks or registered trademarks of Riot Games, Inc.';

/** Plan section 13 requires the visible "not affiliated" statement as well as the non-endorsement one. */
export const NOT_AFFILIATED_TEXT = 'This site is not affiliated with Riot Games, Inc., is not authorised, sponsored or approved by Riot Games, and is not an official source of League of Legends statistics.';

/** The free-and-ungated claim, in the words every page uses for it. */
export const FREE_TIER_TEXT = 'Every page on this site is free and ungated: there is no account, no login, no paywall and no rate-limited teaser, and none is planned.';

/** The standing Riot prohibition the compliance gate enforces mechanically. */
export const NO_RATING_TEXT = 'This site does not compute, store or display an MMR, ELO or any other skill rating, and does not offer a calculator for one.';

/** The no-data-broker sentence. Riot's General Policies forbid the broker role explicitly. */
export const NO_BROKER_TEXT = 'This site publishes derived aggregate statistics only. It does not resell Riot data, does not expose the raw archive it ingests, and publishes no per-player record, account, summoner name or match history.';

/**
 * The operator, and the controller of the little personal data this site does
 * touch (a web-server access log). The name is the copyright holder recorded in
 * LICENSE; the jurisdiction is deliberately described rather than asserted,
 * because the operator's legal identity is still an open question in the plan
 * (section 15, question 1) and a guessed country in a governing-law clause is
 * worse than an accurate general one.
 */
export const OPERATOR_NAME = 'Erik Schuetze';

/** The operator's identity as a legal page states it. */
export const OPERATOR_IDENTITY = `${OPERATOR_NAME}, a private individual resident in the European Union, who operates this site as a non-commercial hobby project.`;

/** The site's display name. */
export const SITE_NAME = 'LoL Stats';

/**
 * The site's own address.
 *
 * `LOLSTATS_SITE_URL` is the same variable `astro.config.mjs` reads to build
 * canonical URLs, the sitemap and robots.txt, so the address the legal pages
 * print cannot disagree with the address the pages claim as canonical - the one
 * place where two honest values could otherwise diverge. The fallback is the
 * hostname this deployment is actually served on (the `lol` record and the Caddy
 * site block in the `homecluster` repository), not a documentation placeholder.
 */
export const SITE_URL_ENV = 'LOLSTATS_SITE_URL';
export const FALLBACK_SITE_URL = 'https://lol.erik-schuetze.dev';

/**
 * The operator's contact address, and the only channel the legal pages offer.
 *
 * Configured by `LOLSTATS_CONTACT_EMAIL` at build time; the constant below is the
 * address this deployment publishes when the build is given nothing else. It is
 * on the operator's own domain, so it is a mailbox the operator controls.
 * `scripts/compliance-check.sh` fails if a legal page renders with no contact
 * address at all - a privacy policy with no contact route is a defective privacy
 * policy, not a cosmetic gap.
 */
export const CONTACT_EMAIL_ENV = 'LOLSTATS_CONTACT_EMAIL';
export const OPERATOR_CONTACT_EMAIL = 'lolstats@erik-schuetze.dev';

/**
 * How /riot.txt is published.
 *
 * Riot's site-verification check fetches the token Riot issued to the domain
 * owner at the site's own root, and it must find that token and nothing else:
 * a comment, a placeholder string or a leading blank line fails the check.
 *
 * The token is a credential issued per domain, so it is not checked into this
 * repository - it is read from the environment by the build. When
 * LOLSTATS_RIOT_VERIFICATION_TOKEN is set, this build writes dist/riot.txt
 * containing the trimmed token and exactly one trailing newline, and publishes
 * nothing else at that path. When it is unset, no /riot.txt is published at
 * all: an absent file is an honest "not verified yet", whereas a placeholder file
 * would be a false claim that could be scraped as if it had passed.
 *
 * The wire-up is in astro.config.mjs (`astro:build:done`), which is the only
 * place in the build that knows the output directory.
 */
export const RIOT_TOKEN_ENV = 'LOLSTATS_RIOT_VERIFICATION_TOKEN';

function env(name: string): string {
  if (typeof process === 'undefined' || !process.env) return '';
  return (process.env[name] ?? '').trim();
}

function hostOf(url: string): string | null {
  try {
    return new URL(url).host;
  } catch {
    return null;
  }
}

/** The site's configured address, with the documented fallback applied. */
export const SITE_URL = env(SITE_URL_ENV) === '' ? FALLBACK_SITE_URL : env(SITE_URL_ENV);

/** The hostname the legal pages print, so that "this site" has an address. */
export const SITE_HOST = hostOf(SITE_URL) ?? hostOf(FALLBACK_SITE_URL) ?? 'this domain';

/** The contact address every legal page publishes. */
export const CONTACT_EMAIL = env(CONTACT_EMAIL_ENV) === '' ? OPERATOR_CONTACT_EMAIL : env(CONTACT_EMAIL_ENV);

/** True when this build was given a verification token. Read once, at build time. */
export function riotVerificationConfigured(): boolean {
  return env(RIOT_TOKEN_ENV) !== '';
}

/**
 * What the legal pages may say about Riot's verification requirement. It never
 * asserts that verification has happened unless this build actually published
 * the token, which is the only thing the site can know from its own output.
 */
export function riotVerificationNote(configured: boolean = riotVerificationConfigured()): string {
  return configured
    ? `Riot's site-verification token was configured for this build, so https://${SITE_HOST}/riot.txt is ` +
        'published and the verified-site requirement can be checked from the domain itself. That is a file Riot ' +
        'fetches, not a statement that Riot has reviewed or endorsed this project.'
    : `The verified-site requirement is not met yet: this build was not given Riot's site-verification token, so ` +
        `https://${SITE_HOST}/riot.txt is not published and Riot has not verified this site. The token is issued to ` +
        'the domain owner, not to this repository, so the requirement is met only by a build that is given it.';
}

/**
 * The single date constant for the whole legal surface.
 *
 * Change this one value when any of the four pages changes materially, and the
 * "last updated" line on all four moves with it. There is deliberately no second
 * date to forget.
 */
export const EFFECTIVE_DATE = '2026-09-17';

/** Derived from EFFECTIVE_DATE, never written a second time. */
export const LAST_UPDATED_DATE = EFFECTIVE_DATE;

const MONTHS = [
  'January',
  'February',
  'March',
  'April',
  'May',
  'June',
  'July',
  'August',
  'September',
  'October',
  'November',
  'December',
];

/**
 * "2026-09-17" -> "17 September 2026". Formatted by hand rather than through
 * Intl for the same reason lib/format.ts groups numbers itself: the build must
 * not depend on the machine's locale to print a date.
 */
export function legalDate(iso: string): string {
  const [year, month, day] = iso.split('-');
  const index = Number.parseInt(month ?? '', 10) - 1;
  const name = MONTHS[index];
  if (!year || !day || !name) return iso;
  return `${Number.parseInt(day, 10)} ${name} ${year}`;
}

export const EFFECTIVE_DATE_LABEL = legalDate(EFFECTIVE_DATE);
export const LAST_UPDATED_LABEL = legalDate(LAST_UPDATED_DATE);

/** The version line every legal page carries, so a reader can date what they read. */
export const VERSION_LINE = `Effective ${EFFECTIVE_DATE_LABEL}. Last updated ${LAST_UPDATED_LABEL}.`;

/**
 * The operator's contact route, as a sentence the pages can drop in. The
 * address itself is in CONTACT_EMAIL so a page can also render it as a mailto.
 */
export const CONTACT_ROUTE = `Questions about these pages, about the data, or about your rights under the GDPR go to ${CONTACT_EMAIL}. That mailbox is the contact route rather than a postal address, because the operator is a private individual.`;

/**
 * Where the numbers on this build come from, in the reader's terms. Derived from
 * the build's data state rather than from a flag, so that a page cannot claim
 * ingested match data while the manifest says the snapshot is a preview.
 */
export function dataSourceSentence(state: DataState): string {
  switch (state) {
    case 'live':
      return 'The statistics are this project\'s own aggregates of ranked solo-queue match records fetched from Riot\'s MATCH-V5 API under a registered Riot API key. They measure a sample of that data, and they are not Riot\'s own figures.';
    case 'demo':
      return 'This build publishes no Riot match data. Its numbers are a labelled preview generated to exercise the layout, and the aggregate manifest this build read declares its source as "demo".';
    case 'no-data':
      return 'This build publishes no Riot match data because no aggregate snapshot exists yet. The statistics routes render an explicit empty state rather than numbers.';
  }
}

/**
 * Whether this build has any ingested Riot match data at all. Exported because
 * the pages say "no match data has been ingested yet" in several places and they
 * must all mean the same thing.
 */
export function publishesMatchData(state: DataState): boolean {
  return state === 'live';
}
