#!/usr/bin/env node
// Downloads the self-hosted webfont files whose @font-face rules live in
// web/src/styles/fonts.css, plus one SIL Open Font License notice per family.
//
//   node scripts/fetch-fonts.mjs
//   node scripts/fetch-fonts.mjs --only inter-400.woff2 --out-dir public/fonts
//
// Only official sources are used, and nothing is fetched at runtime by the
// built site:
//
//   * the woff2 files come from the Google Fonts stylesheet (fonts.googleapis.com),
//     resolved exactly as a browser would resolve them, so the bytes are the
//     files Google publishes for that family, weight and subset;
//   * the licence notices come from the upstream google/fonts repository, which
//     is where each family's OFL.txt is maintained.
//
// The files are committed and served from this origin at /fonts/<file>.woff2.
// A build never downloads anything: this script is run by a human, and the
// output is reviewed and checked in.
//
// Subsets: the *latin* subset only. It carries Basic Latin, Latin-1 Supplement
// and the general punctuation the site's copy uses (apostrophes in champion
// names, the en dash in ranges, the middot in separators). fonts.css declares
// one @font-face per weight - not per subset - so a per-subset file naming
// scheme is deliberately not introduced here.

import { mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const WEB_ROOT = resolve(fileURLToPath(new URL('.', import.meta.url)), '..');

/** A browser UA is required: the CSS API serves woff2 only to clients that ask for it. */
const BROWSER_UA =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36';

const STYLESHEET = (family, weight) =>
  `https://fonts.googleapis.com/css2?family=${family}:wght@${weight}&display=swap`;

/** The upstream google/fonts directory that holds each family's OFL.txt. */
const LICENCE_DIR = {
  Inter: 'inter',
  'JetBrains Mono': 'jetbrainsmono',
  Montserrat: 'montserrat',
};

const LICENCE_URL = (family) =>
  `https://raw.githubusercontent.com/google/fonts/main/ofl/${LICENCE_DIR[family]}/OFL.txt`;

/** Exactly the file names web/src/styles/fonts.css declares. */
const FACES = [
  { file: 'inter-400.woff2', family: 'Inter', weight: 400 },
  { file: 'inter-600.woff2', family: 'Inter', weight: 600 },
  { file: 'inter-700.woff2', family: 'Inter', weight: 700 },
  { file: 'jetbrains-mono-400.woff2', family: 'JetBrains Mono', weight: 400 },
  { file: 'jetbrains-mono-700.woff2', family: 'JetBrains Mono', weight: 700 },
  { file: 'montserrat-700.woff2', family: 'Montserrat', weight: 700 },
];

/** The subset block to take from the stylesheet. */
const SUBSET = 'latin';

function argValue(name) {
  const index = process.argv.indexOf(name);
  return index === -1 ? undefined : process.argv[index + 1];
}

const outDir = resolve(WEB_ROOT, argValue('--out-dir') ?? 'public/fonts');
const only = argValue('--only');
const faces = only ? FACES.filter((face) => face.file === only) : FACES;

if (faces.length === 0) {
  console.error(`no face matches --only ${only}`);
  process.exit(1);
}

async function get(url, as) {
  const response = await fetch(url, { headers: as === 'css' ? { 'user-agent': BROWSER_UA } : {} });
  if (!response.ok) throw new Error(`${url} -> HTTP ${response.status}`);
  return as === 'css' ? response.text() : Buffer.from(await response.arrayBuffer());
}

/**
 * The woff2 URL for one family/weight/subset, read out of the stylesheet the
 * way a browser reads it: each block is introduced by a `/* subset *​/` comment.
 */
function subsetUrl(css, subset) {
  const blocks = css.split('@font-face').slice(1);
  for (const block of blocks) {
    const comment = /\/\*\s*([a-z-]+)\s*\*\//.exec(block);
    if (!comment || comment[1] !== subset) continue;
    const url = /url\((https:\/\/[^)]+\.woff2)\)/.exec(block);
    if (url) return url[1];
  }
  return null;
}

mkdirSync(outDir, { recursive: true });

const downloaded = new Map(); // upstream URL -> file on disk
let totalBytes = 0;
let duplicatedBytes = 0;

for (const face of faces) {
  const css = await get(STYLESHEET(face.family, face.weight), 'css');
  const url = subsetUrl(css, SUBSET);
  if (!url) throw new Error(`${face.family} ${face.weight}: no ${SUBSET} face in the stylesheet`);

  const previous = downloaded.get(url);
  if (previous) {
    // Variable families (Inter is one) publish a single file that serves every
    // weight. It is copied to each declared name rather than downloaded again,
    // because the stylesheet wants one file per weight.
    writeFileSync(resolve(outDir, face.file), previous.bytes);
    duplicatedBytes += previous.bytes.length;
    console.log(`${face.file}\t${previous.bytes.length} B\tcopy of ${previous.file} (same upstream file)`);
    continue;
  }

  const bytes = await get(url);
  writeFileSync(resolve(outDir, face.file), bytes);
  downloaded.set(url, { file: face.file, bytes });
  totalBytes += bytes.length;
  console.log(`${face.file}\t${bytes.length} B\t${url}`);
}

for (const family of new Set(faces.map((face) => face.family))) {
  const url = LICENCE_URL(family);
  const bytes = await get(url);
  const file = `OFL-${family.replace(/ /g, '')}.txt`;
  writeFileSync(resolve(outDir, file), bytes);
  totalBytes += bytes.length;
  console.log(`${file}\t${bytes.length} B\t${url}`);
}

console.log(
  `\n${faces.length} faces + ${new Set(faces.map((f) => f.family)).size} licences in ${outDir}: ` +
    `${totalBytes} B written, ${duplicatedBytes} B of that copied from an identical upstream file.`,
);
