#!/usr/bin/env node
// Regenerates the checked-in Data Dragon projections under web/src/data/.
//
// The files are committed so that the site builds with no network access and so
// that a Data Dragon release cannot silently change what a page renders between
// two builds of the same commit. Re-run this deliberately, review the diff and
// commit it - the same rule the generated TypeScript follows.
//
// Only permitted static data is read: the version list, champion.json,
// item.json, runesReforged.json and summoner.json. No champion art, splash art
// or lore is fetched or stored, and no image is vendored - every emitted icon
// value is a Data Dragon CDN URL that the browser requests directly.
//
// Outputs (name + icon only, so the files stay small):
//   src/data/champions.json  StaticChampions-shaped list for the site's pages
//   src/data/items.json      { "<numeric id>": { name, icon } }
//   src/data/runes.json      { "<numeric id>": { name, icon } }
//   src/data/spells.json     { "<numeric id>": { name, icon } }
//
//   node web/scripts/fetch-ddragon.mjs [--version 16.18.1] [--out-dir src/data] [--out <champions path>]

import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const CDN = 'https://ddragon.leagueoflegends.com';
const LOCALE = 'en_US';
const here = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(here, '..');

function parseArgs(argv) {
  const args = {
    version: '',
    outDir: resolve(webRoot, 'src/data'),
    out: '',
    only: '',
  };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--version') args.version = argv[i + 1] ?? '';
    else if (arg === '--out-dir') args.outDir = resolve(process.cwd(), argv[i + 1] ?? '');
    else if (arg === '--out') args.out = resolve(process.cwd(), argv[i + 1] ?? '');
    else if (arg === '--only') args.only = argv[i + 1] ?? '';
    else if (arg === '-h' || arg === '--help') {
      console.log(
        'usage: node web/scripts/fetch-ddragon.mjs [--version <ddragon version>] [--out-dir <dir>] [--out <champions.json>] [--only champions,items,runes,spells]',
      );
      process.exit(0);
    }
  }
  return args;
}

async function getJson(url) {
  const res = await fetch(url, {
    headers: { accept: 'application/json', 'user-agent': 'lolstats-static-sync/1.0 (+https://lolstats.example)' },
  });
  if (!res.ok) throw new Error(`${url} returned HTTP ${res.status}`);
  return res.json();
}

// Mirrors aggmodel.ChampionSlug: lowercase, ASCII letters and digits only.
// Kept as a separate implementation on purpose - this file has to stay runnable
// with no Go toolchain, and the rule is small enough to restate.
function slugOf(ddragonKey) {
  return ddragonKey.toLowerCase().replace(/[^a-z0-9]/g, '');
}

/**
 * One entry per line rather than fully expanded: these files are generated
 * reference data that a reader only ever diffs, and indenting 864 items into
 * four lines each tripled the checked-in size for no readability gain.
 */
function keyedJson(value) {
  const lines = Object.entries(value).map(([id, entry]) => `  ${JSON.stringify(id)}: ${JSON.stringify(entry)}`);
  return `{\n${lines.join(',\n')}\n}\n`;
}

/** name + icon, keyed by numeric id as a string, so lookups cannot miss on type. */
function keyed(rows) {
  const out = {};
  for (const row of rows.sort((a, b) => a.id - b.id)) out[String(row.id)] = { name: row.name, icon: row.icon };
  return out;
}

async function write(relPath, value) {
  const path = resolve(relPath);
  await mkdir(dirname(path), { recursive: true });
  const body = keyedJson(value);
  await writeFile(path, body, 'utf8');
  console.log(`wrote ${Object.keys(value).length} entries, ${body.length} bytes, to ${path}`);
}

const args = parseArgs(process.argv.slice(2));
const wanted = args.only ? new Set(args.only.split(',').map((s) => s.trim())) : null;
const wants = (kind) => !wanted || wanted.has(kind);

const versions = await getJson(`${CDN}/api/versions.json`);
if (!Array.isArray(versions) || versions.length === 0) throw new Error('versions.json was empty');
const version = args.version || versions[0];

if (wants('champions')) {
  const payload = await getJson(`${CDN}/cdn/${version}/data/${LOCALE}/champion.json`);
  const champions = Object.values(payload.data ?? {});
  if (champions.length === 0) throw new Error(`champion.json for ${version} contained no champions`);

  const rows = champions
    .map((c) => ({
      id: Number.parseInt(c.key, 10),
      key: c.id,
      slug: slugOf(c.id),
      name: c.name,
      icon: `${CDN}/cdn/${version}/img/champion/${c.id}.png`,
      // Data Dragon publishes no role or lane membership. Left empty rather than
      // guessed: the aggregate artifacts (tierlist cells, Champion.roles) are the
      // only honest source for which roles a champion was played in, and a
      // fabricated list here would become a published claim.
      roles: [],
    }))
    .filter((c) => Number.isFinite(c.id))
    .sort((a, b) => a.id - b.id);

  const seenSlugs = new Map();
  for (const c of rows) {
    const clash = seenSlugs.get(c.slug);
    if (clash) throw new Error(`slug collision: ${clash} and ${c.key} both map to ${c.slug}`);
    seenSlugs.set(c.slug, c.key);
  }

  const out = { ddragon_version: version, champions: rows };
  const path = args.out || join(args.outDir, 'champions.json');
  await mkdir(dirname(resolve(path)), { recursive: true });
  const body = `${JSON.stringify(out, null, 2)}\n`;
  await writeFile(path, body, 'utf8');
  console.log(`wrote ${rows.length} champions for Data Dragon ${version} to ${path}`);
}

if (wants('items')) {
  const payload = await getJson(`${CDN}/cdn/${version}/data/${LOCALE}/item.json`);
  const items = Object.entries(payload.data ?? {})
    .map(([id, item]) => ({
      id: Number.parseInt(id, 10),
      name: item?.name ?? '',
      // Item icons live under img/item; image.full is the file name.
      icon: `${CDN}/cdn/${version}/img/item/${item?.image?.full ?? `${id}.png`}`,
    }))
    .filter((item) => Number.isFinite(item.id) && item.name !== '');
  if (items.length === 0) throw new Error(`item.json for ${version} contained no items`);
  await write(join(args.outDir, 'items.json'), keyed(items));
}

if (wants('runes')) {
  const trees = await getJson(`${CDN}/cdn/${version}/data/${LOCALE}/runesReforged.json`);
  if (!Array.isArray(trees) || trees.length === 0) throw new Error(`runesReforged.json for ${version} was empty`);
  // Every rune is reachable by walking each tree's slots; the icon field is a
  // path relative to /cdn/img, not a complete URL.
  const runes = [];
  for (const tree of trees) {
    for (const slot of tree?.slots ?? []) {
      for (const rune of slot?.runes ?? []) {
        const id = Number.parseInt(rune?.id, 10);
        if (!Number.isFinite(id) || !rune?.name || !rune?.icon) continue;
        runes.push({ id, name: rune.name, icon: `${CDN}/cdn/img/${rune.icon}` });
      }
    }
  }
  if (runes.length === 0) throw new Error(`runesReforged.json for ${version} contained no runes`);
  await write(join(args.outDir, 'runes.json'), keyed(runes));
}

if (wants('spells')) {
  const payload = await getJson(`${CDN}/cdn/${version}/data/${LOCALE}/summoner.json`);
  const spells = Object.values(payload.data ?? {})
    .map((spell) => ({
      // In summoner.json the map key is the Data Dragon name and `key` is the
      // numeric id the match payload uses, which is the one builds carry.
      id: Number.parseInt(spell?.key, 10),
      name: spell?.name ?? '',
      icon: `${CDN}/cdn/${version}/img/spell/${spell?.image?.full ?? ''}`,
    }))
    .filter((spell) => Number.isFinite(spell.id) && spell.name !== '' && spell.icon.endsWith('.png'));
  if (spells.length === 0) throw new Error(`summoner.json for ${version} contained no spells`);
  await write(join(args.outDir, 'spells.json'), keyed(spells));
}
