// @ts-check
import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { defineConfig } from 'astro/config';

// The canonical host has to exist before Riot can verify riot.txt, so it is
// configuration rather than a constant: the build derives canonical URLs,
// sitemap.xml and robots.txt from LOLSTATS_SITE_URL, and web/src/lib/legal.ts
// derives the address the legal pages print from the same variable.
//
// A missing variable must not produce a publicly-wrong canonical, so this build
// does not publish a placeholder under any circumstances:
//
//   * unset or blank - it publishes DEFAULT_SITE_URL, the address this
//     deployment is served from, and says on stdout that the default was used.
//     The value is the same one legal.ts falls back to, so the canonicals and
//     the address the legal pages print cannot disagree.
//   * set but unusable - not an absolute http(s) URL, a reserved hostname such
//     as *.invalid, or carrying a path/query/fragment: the build throws, and a
//     failed build publishes nothing at all.
const DEFAULT_SITE_URL = 'https://lol.erik-schuetze.dev';

/** Reserved for documentation and testing (RFC 2606, RFC 6761): never a real deployment. */
const RESERVED_HOST = /^(?:localhost|(?:.*\.)?(?:invalid|test|example)|(?:.*\.)?example\.(?:com|net|org))$/i;

/** @param {string} raw */
function resolveSiteUrl(raw) {
  const configured = raw.trim();
  if (configured === '') {
    console.log(
      `[site] LOLSTATS_SITE_URL is not set; publishing ${DEFAULT_SITE_URL}, the address this deployment is served from. ` +
        'Set LOLSTATS_SITE_URL to publish a different origin.',
    );
    return DEFAULT_SITE_URL;
  }

  let parsed;
  try {
    parsed = new URL(configured);
  } catch {
    throw new Error(
      `LOLSTATS_SITE_URL=${JSON.stringify(configured)} is not an absolute URL. Set it to the origin the site is served ` +
        `from, for example ${DEFAULT_SITE_URL}.`,
    );
  }
  if (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') {
    throw new Error(`LOLSTATS_SITE_URL=${configured} is not an http(s) origin, so it cannot be a canonical host.`);
  }
  if (RESERVED_HOST.test(parsed.hostname)) {
    throw new Error(
      `LOLSTATS_SITE_URL=${configured} names the reserved host ${parsed.hostname}. Publishing it would put a wrong ` +
        `canonical on every page, so this build refuses it; use ${DEFAULT_SITE_URL} or the real deployment origin.`,
    );
  }
  if (parsed.pathname !== '/' || parsed.search !== '' || parsed.hash !== '') {
    throw new Error(
      `LOLSTATS_SITE_URL=${configured} carries a path, query or fragment. It must be an origin alone, such as ` +
        `${DEFAULT_SITE_URL}.`,
    );
  }
  return parsed.origin;
}

const site = resolveSiteUrl(process.env.LOLSTATS_SITE_URL ?? '');

/**
 * Riot's site verification, which is the one file in the output that is not a
 * page.
 *
 * Riot fetches `https://<domain>/riot.txt` and requires the issued token and
 * nothing else in it. The token is a credential, so it is not in the repository:
 * it arrives in the environment, and this hook writes the file after the build
 * has finished - which is also the only moment the output directory is known.
 * With no token set, nothing is written and no `/riot.txt` exists in the output
 * at all; the legal pages then say the requirement is outstanding rather than
 * implying it has been met (see web/src/lib/legal.ts).
 *
 * The token is deliberately not logged: build logs are not a place for a
 * credential. Only its presence and the byte count are reported.
 */
const riotVerification = {
  name: 'riot-site-verification',
  hooks: {
    /** @param {{ dir: URL }} args */
    'astro:build:done': ({ dir }) => {
      const token = (process.env.LOLSTATS_RIOT_VERIFICATION_TOKEN ?? '').trim();
      if (token === '') {
        console.log('[riot] LOLSTATS_RIOT_VERIFICATION_TOKEN is not set: no /riot.txt is published.');
        return;
      }
      const file = join(fileURLToPath(dir), 'riot.txt');
      writeFileSync(file, `${token}\n`);
      console.log(`[riot] published /riot.txt (${Buffer.byteLength(token) + 1} bytes) from LOLSTATS_RIOT_VERIFICATION_TOKEN.`);
    },
  },
};

// The site is a pre-rendered snapshot of the agg/v1 tree. Static output is not a
// preference here: every route must render without JavaScript, and the only
// interactive parts are explicitly hydrated islands inside the pages.
export default defineConfig({
  site,
  output: 'static',
  build: {
    // One directory per route keeps the nginx ingress rules trivial.
    format: 'directory',
  },
  trailingSlash: 'ignore',
  devToolbar: { enabled: false },
  integrations: [riotVerification],
});

