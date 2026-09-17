// @ts-check
import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { defineConfig } from 'astro/config';

// The canonical host is an open decision (backlog.md: "Site name and domain"),
// and it has to exist before Riot can verify riot.txt, so it is configuration
// rather than a constant: the build derives canonical URLs, sitemap.xml and
// robots.txt from LOLSTATS_SITE_URL. The placeholder is a reserved TLD, so a
// build that forgets to set it is obviously wrong in the output rather than
// quietly pointing at somebody else's domain.
const site = process.env.LOLSTATS_SITE_URL ?? 'https://lolstats.example.invalid';

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

