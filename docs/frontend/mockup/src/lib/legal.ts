/**
 * Frozen strings from docs/compliance.md. These are approved wording: copy them
 * character for character. Nothing on the request path may alter them, and no
 * page may paraphrase them (a second weaker copy is exactly the drift the
 * compliance rules exist to prevent).
 *
 * Both the mockup and the future frontend must render these as constants.
 */

export const siteName = "LoL Stats";

export const trademarkSentence =
  "League of Legends and Riot Games are trademarks or registered trademarks of Riot Games, Inc.";

export const nonEndorsementNotice =
  "This project is not endorsed by Riot Games and does not reflect the views or " +
  "opinions of Riot Games or anyone officially involved in producing or managing " +
  "Riot Games properties. Riot Games and all associated properties are trademarks " +
  "or registered trademarks of Riot Games, Inc.";

export const notAffiliatedSentence =
  "This site is not affiliated with Riot Games, Inc., is not authorised, " +
  "sponsored or approved by Riot Games, and is not an official source of League of " +
  "Legends statistics.";

export const freeAndUngatedSentence =
  "Every page on this site is free and ungated: there is no account, no login, no " +
  "paywall and no rate-limited teaser, and none is planned.";

export const noRatingSentence =
  "This site does not compute, store or display an MMR, ELO or any other skill " +
  "rating, and does not offer a calculator for one.";

export const derivedOnlySentence =
  "This site publishes derived aggregate statistics only. It does not resell Riot " +
  "data, does not serve the raw Riot API responses behind its aggregates, and " +
  "publishes no per-player record, account, summoner name or match history.";

export const operatorLine =
  "Erik Schuetze, a private individual resident in the European Union, who operates " +
  "this site as a non-commercial hobby project.";

/** Deployment may override with LOLSTATS_CONTACT_EMAIL; unset is not an error. */
export const contactEmail = "lolstats@erik-schuetze.dev";

export const contactLine =
  "Questions about these pages, about the data, or about your rights under the " +
  "GDPR go to the mailbox above. That mailbox is the contact route rather than a " +
  "postal address, because the operator is a private individual.";

export const effectiveLine =
  "Effective 17 September 2026. Last updated 17 September 2026.";
