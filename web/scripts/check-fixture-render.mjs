#!/usr/bin/env node
// Rendered cross-view check for the built demo site.
//
// The artifact checker proves the JSON agrees with itself. This one proves the
// HTML the site actually serves agrees with it: the tier list, the champion
// page and the matchup board are three views of one cell, and a page that has
// no published cell must say so instead of showing a number.
//
// It also checks the tree it renders: the payload has to be readable and every
// artifact the manifest lists has to be on disk before a page can be compared to
// it, and every detail artifact the newest patch ships has to be the one its
// champion page renders - the page prefers the artifact and silently falls back
// to the tier list cell, so a lost artifact looks like a page saying less rather
// than a page saying something wrong.
//
//   node web/scripts/check-fixture-render.mjs [--dist <built dir>] [--root web/src/fixtures/v1]
//
// Defaults to web/dist and web/src/fixtures/v1, so it runs unchanged from any
// cwd. Exit status is non-zero when any view contradicts another.

import { readFileSync, readdirSync, existsSync } from 'node:fs';
import { basename, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROLES = ['top', 'jungle', 'mid', 'bottom', 'support'];

/** `web/`, derived from this file's own location so the defaults do not depend on the caller's cwd. */
const WEB_ROOT = fileURLToPath(new URL('..', import.meta.url));

// Rendered by ChampionBody.astro only when the champion's detail artifact has a
// role entry with builds in it, so its absence on a role page is the render-time
// signal that the page fell back to the tier list cell.
const ARTIFACT_SECTION = '<h2>Items, runes and spells</h2>';

function parseArgs(argv) {
  const out = {
    // The real product: the directory `astro build` writes. This is what the
    // postbuild hook in web/package.json checks, so the default has to be the
    // shipped build rather than a hand-made copy of one.
    dist: join(WEB_ROOT, 'dist'),
    root: join(WEB_ROOT, 'src', 'fixtures', 'v1'),
    expectPages: null,
  };
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === '--dist' && argv[i + 1]) out.dist = resolve(argv[i + 1]);
    else if (argv[i] === '--root' && argv[i + 1]) out.root = resolve(argv[i + 1]);
    else if (argv[i] === '--expect-pages' && argv[i + 1]) out.expectPages = Number(argv[i + 1]);
    else if (argv[i].startsWith('--')) throw new Error(`unknown flag ${argv[i]}`);
  }
  return out;
}

function readPage(dist, route) {
  const file = join(dist, route, 'index.html');
  return existsSync(file) ? readFileSync(file, 'utf8') : null;
}

function readJson(file) {
  return JSON.parse(readFileSync(file, 'utf8'));
}

/** A read that reports instead of throwing, so one bad file does not become a stack trace. */
function readJsonFile(file) {
  let text;
  try {
    text = readFileSync(file, 'utf8');
  } catch (error) {
    return { value: null, error: `cannot be read (${error.message})` };
  }
  try {
    return { value: JSON.parse(text), error: null };
  } catch (error) {
    return { value: null, error: `is not valid JSON (${error.message})` };
  }
}

function listJson(dir) {
  try {
    return readdirSync(dir)
      .filter((name) => name.endsWith('.json'))
      .sort()
      .map((name) => join(dir, name));
  } catch {
    return [];
  }
}

/** Every .json file under `dir`, so the whole tree can be checked for readability. */
function listJsonDeep(dir) {
  let entries;
  try {
    entries = readdirSync(dir, { withFileTypes: true });
  } catch {
    return [];
  }
  const files = [];
  for (const entry of entries) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) files.push(...listJsonDeep(path));
    else if (entry.isFile() && entry.name.endsWith('.json')) files.push(path);
  }
  return files.sort();
}

/** Tier list rows carry their own numbers as data attributes. */
function tierListRows(html) {
  const rows = [];
  for (const chunk of html.split('<tr ').slice(1)) {
    const attr = (name) => chunk.match(new RegExp(`${name}="([^"]*)"`))?.[1] ?? null;
    if (attr('data-v-champion') === null) continue;
    const href = chunk.match(/href="\/champions\/([a-z0-9]+)\/?(?:[a-z]+)?"/);
    rows.push({
      name: attr('data-v-champion'),
      slug: href ? href[1] : null,
      n: Number(attr('data-v-n')),
      winRate: attr('data-v-win_rate') === null ? null : Number(attr('data-v-win_rate')),
      pickRate: attr('data-v-pick_rate') === null ? null : Number(attr('data-v-pick_rate')),
      banRate: attr('data-v-ban_rate') === null ? null : Number(attr('data-v-ban_rate')),
      tier: chunk.match(/class="badge grade-[a-z+]*" data-tier="([^"]+)"/)?.[1] ?? null,
    });
  }
  return rows;
}

/** The champion page states one cell: games, win rate and tier. */
function championPageStats(html) {
  const stat = html.match(
    /Win rate<\/span><span class="fallback-stat__value">([\d.]+)%<\/span><span class="fallback-stat__n">n = ([\d,]+)<\/span>/,
  );
  if (!stat) return null;
  const tier = html.match(/title="Tier ([A-Z+]+), n = ([\d,]+) games"/);
  return { winRatePct: Number(stat[1]), n: Number(stat[2].replace(/,/g, '')), tier: tier ? tier[1] : null };
}

function matchupBoard(html) {
  const ids = (pattern) => [...html.matchAll(pattern)].map((match) => Number(match[1]));
  const axis = [
    ...new Set([
      ...ids(/<th scope="col" class="column" data-champion="(\d+)"/g),
      ...ids(/<th scope="row" class="row-head" data-champion="(\d+)"/g),
    ]),
  ];
  // A cell the site withheld is rendered as `cell missing` with no rate; a
  // published cell carries the pair's games in data-n.
  const cells = [...html.matchAll(/<td class="([^"]*)" data-n="(\d+)"[^>]*>/g)].map((match) => ({
    classes: match[1].split(/\s+/),
    n: Number(match[2]),
  }));
  const published = cells.filter((cell) => !cell.classes.includes('self') && !cell.classes.includes('missing'));
  const absentWithRate = [...html.matchAll(/<td class="[^"]*\b(?:self|missing)\b[^"]*"[^>]*>(?:(?!<\/td>).)*?%/g)];
  return { axis, cells: published.map((cell) => cell.n), absentWithRate: absentWithRate.length };
}

function finish(dist, pages, checks) {
  const total = checks.reduce((sum, check) => sum + check.failures.length, 0);
  process.stdout.write(`rendered fixture check: ${dist}\n`);
  process.stdout.write(`  pages: ${pages.length}\n`);
  for (const check of checks) {
    process.stdout.write(`  ${check.failures.length === 0 ? 'ok  ' : 'FAIL'} ${check.name} (${check.checked} checked)\n`);
    for (const failure of check.failures.slice(0, 5)) process.stdout.write(`         ${failure}\n`);
    if (check.failures.length > 5) process.stdout.write(`         ... and ${check.failures.length - 5} more\n`);
  }
  process.stdout.write(`  total failures: ${total}\n`);
  process.exitCode = total === 0 ? 0 : 1;
}

function main() {
  const { dist, root, expectPages } = parseArgs(process.argv.slice(2));

  const failures = [];
  const checks = [];
  const record = (name, checked, list) => checks.push({ name, checked, failures: list });

  let manifest;
  let champions;
  try {
    manifest = readJson(join(root, 'manifest.json'));
    champions = readJson(join(root, 'static', manifest.latest?.ddragon_version ?? '16.18.1', 'champions.json'));
  } catch (error) {
    record('the fixture tree has a readable manifest and champion list', 1, [`${root} is not the tree this site can be read from: ${error.message}`]);
    finish(dist, [], checks);
    return;
  }
  const idBySlug = new Map(champions.champions.map((champion) => [champion.slug, champion.id]));
  const slugById = new Map(champions.champions.map((champion) => [champion.id, champion.slug]));

  // The guard compares the served pages against the payload, so the payload has
  // to be whole first: a tree that has lost a file it still advertises, or that
  // carries JSON nothing can parse, would otherwise be compared against as if it
  // were intact - and the champion page's silent fall back to the tier list cell
  // means a missing detail artifact does not show up in the HTML as a blank.
  const treeFailures = [];
  const partitions = manifest.partitions ?? [];
  for (const file of listJsonDeep(root)) {
    const { error } = readJsonFile(file);
    if (error) treeFailures.push(`${relative(root, file)} ${error}`);
  }
  let treeChecked = 0;
  const latestArtifacts = new Map();
  for (const partition of partitions) {
    const dir = join(root, 'p', partition.patch, partition.region, String(partition.queue), partition.bracket);
    const listed = [...(partition.champions ?? [])].sort((a, b) => a - b);
    const onDisk = new Map();
    for (const file of listJson(join(dir, 'champions'))) {
      const { value, error } = readJsonFile(file);
      if (error) {
        treeFailures.push(`patch ${partition.patch} champions/${basename(file)} ${error}`);
        continue;
      }
      const fileId = Number(basename(file, '.json'));
      if (fileId !== value.champion_id) {
        treeFailures.push(
          `patch ${partition.patch}: champions/${basename(file)} holds champion_id ${value.champion_id}, ` +
            'but the site reads the artifact named by the champion id',
        );
      }
      onDisk.set(value.champion_id, value);
    }
    treeChecked += listed.length + onDisk.size;
    for (const id of listed) {
      if (!onDisk.has(id)) treeFailures.push(`patch ${partition.patch}: the manifest lists champion ${id}, no champions/${id}.json exists`);
    }
    for (const id of [...onDisk.keys()].sort((a, b) => a - b)) {
      if (!listed.includes(id)) {
        treeFailures.push(`patch ${partition.patch}: champions/${id}.json exists, the manifest does not list champion ${id}`);
      }
    }
    if (partition.patch === manifest.latest?.patch) for (const [id, artifact] of onDisk) latestArtifacts.set(id, artifact);
  }
  record('the artifact tree the site was built from is intact', treeChecked, treeFailures);

  const pages = [];
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) walk(path);
      else if (entry.name.endsWith('.html')) pages.push(path);
    }
  };
  walk(dist);
  const pageFailures = [];
  if (expectPages !== null && pages.length !== expectPages) {
    pageFailures.push(`built ${pages.length} pages, expected ${expectPages}`);
  }
  const withoutBanner = pages.filter((page) => !readFileSync(page, 'utf8').includes('illustrative'));
  for (const page of withoutBanner.slice(0, 5)) pageFailures.push(`${page.replace(dist, '')} does not carry the preview labelling`);
  record('every page is built and labelled', pages.length, pageFailures);

  const latest = manifest.latest.patch;
  const tierLists = new Map();
  const linkFailures = [];
  let linkChecked = 0;
  for (const role of ROLES) {
    for (const [route, patch] of [[`/tier-list/${role}/`, latest], ...manifest.partitions.map((p) => [`/patch/${p.patch}/tier-list/${role}/`, p.patch])]) {
      const html = readPage(dist, route.slice(1));
      if (!html) {
        linkFailures.push(`${route} was not built`);
        continue;
      }
      const rows = tierListRows(html);
      if (patch === latest) tierLists.set(role, rows);
      for (const row of rows) {
        linkChecked += 1;
        if (!row.slug) {
          linkFailures.push(`${route}: ${row.name} has no champion link`);
          continue;
        }
        const target = readPage(dist, `champions/${row.slug}/${role}`);
        if (!target) {
          linkFailures.push(`${route}: ${row.name} links to /champions/${row.slug}/${role}/ which was not built`);
          continue;
        }
        const stats = championPageStats(target);
        if (!stats) {
          linkFailures.push(`${route}: ${row.name} (n=${row.n}) links to /champions/${row.slug}/${role}/, which publishes no cell`);
          continue;
        }
        if (patch === latest && (stats.n !== row.n || Math.abs(stats.winRatePct - Math.round(row.winRate * 10000) / 100) > 0.005)) {
          linkFailures.push(
            `${route}: ${row.name} [n=${row.n} ${(row.winRate * 100).toFixed(2)}%] != champion page [n=${stats.n} ${stats.winRatePct}%]`,
          );
        }
        if (patch === latest && row.tier && stats.tier !== row.tier) {
          linkFailures.push(`${route}: ${row.name} tier ${row.tier} != champion page tier ${stats.tier}`);
        }
      }
    }
  }
  record('tier list row, champion page link target and champion page agree', linkChecked, linkFailures);

  // The other direction: a champion page that shows a cell must be tiered.
  const championFailures = [];
  let championChecked = 0;
  for (const [role, rows] of tierLists) {
    if (!rows) continue;
    const listed = new Set(rows.map((row) => row.slug));
    const champs = readdirSync(join(dist, 'champions'));
    for (const slug of champs) {
      const html = readPage(dist, `champions/${slug}/${role}`);
      if (!html) continue;
      championChecked += 1;
      const stats = championPageStats(html);
      if (stats && !listed.has(slug)) {
        championFailures.push(`/champions/${slug}/${role}/ publishes n=${stats.n}, the ${role} tier list omits it`);
      }
      if (!stats && listed.has(slug)) {
        championFailures.push(`/champions/${slug}/${role}/ publishes nothing, the ${role} tier list lists it`);
      }
    }
  }
  record('champion page presence matches the tier list', championChecked, championFailures);

  // The role board of the matchup page is the tier list's champion set.
  const boardFailures = [];
  let boardChecked = 0;
  for (const role of ROLES) {
    const html = readPage(dist, `matchups/${role}`);
    if (!html) {
      boardFailures.push(`/matchups/${role}/ was not built`);
      continue;
    }
    const board = matchupBoard(html);
    const rows = tierLists.get(role) ?? [];
    const listed = new Set(rows.map((row) => idBySlug.get(row.slug)).filter((id) => id !== undefined));
    boardChecked += board.axis.length + listed.size;
    for (const id of board.axis) {
      if (!listed.has(id)) boardFailures.push(`/matchups/${role}/: axis names ${id}, the tier list publishes no cell for it`);
    }
    for (const id of listed) {
      if (!board.axis.includes(id)) boardFailures.push(`/matchups/${role}/: tier list publishes ${id}, the axis omits it`);
    }
    if (board.absentWithRate > 0) {
      boardFailures.push(`/matchups/${role}/: ${board.absentWithRate} cells render a rate while carrying no sample`);
    }
    for (const n of board.cells) {
      if (n < manifest.latest.min_cell_n) {
        boardFailures.push(`/matchups/${role}/: a cell publishes n=${n}, below the floor ${manifest.latest.min_cell_n}`);
      }
    }
  }
  record('matchup role board is the tier list champion set', boardChecked, boardFailures);

  // Nothing rendered may carry a rate under the floor.
  const floorFailures = [];
  let floorChecked = 0;
  for (const role of ROLES) {
    for (const rows of [tierLists.get(role) ?? []]) {
      for (const row of rows) {
        floorChecked += 1;
        if (!(row.n >= manifest.latest.min_cell_n)) {
          floorFailures.push(`/tier-list/${role}/: ${row.name} publishes n=${row.n}, below the floor`);
        }
        if (!row.tier) floorFailures.push(`/tier-list/${role}/: ${row.name} publishes a rate with no tier`);
      }
    }
  }
  record('no rendered cell is below the floor', floorChecked, floorFailures);

  // The champion page reads its cell from the detail artifact first and falls
  // back to the tier list cell, so an artifact that exists has to be the one the
  // page renders - a page that quietly lost its artifact still looks right while
  // publishing less than the tree does.
  const artifactPageFailures = [];
  let artifactPagesChecked = 0;
  for (const [id, artifact] of latestArtifacts) {
    const slug = slugById.get(id);
    for (const entry of artifact.roles ?? []) {
      artifactPagesChecked += 1;
      const route = slug ? `champions/${slug}/${String(entry.role).toLowerCase()}` : null;
      if (!route) {
        artifactPageFailures.push(`champion ${id} has a detail artifact but no slug, so its page has no route`);
        continue;
      }
      const html = readPage(dist, route);
      if (!html) {
        artifactPageFailures.push(`/${route}/ was not built, but the tree publishes champion ${id}'s detail artifact`);
        continue;
      }
      const stats = championPageStats(html);
      if (!stats) {
        artifactPageFailures.push(`/${route}/ publishes no cell, but its artifact publishes n=${entry.stats.n}`);
        continue;
      }
      if (!html.includes(ARTIFACT_SECTION)) {
        artifactPageFailures.push(
          `/${route}/ renders no items, runes or spells, so it fell back to the tier list cell: ` +
            `the tree ships champion ${id}'s detail artifact, which the page is supposed to render`,
        );
      }
      const expectedPct = Math.round(entry.stats.win_rate * 10000) / 100;
      if (stats.n !== entry.stats.n || Math.abs(stats.winRatePct - expectedPct) > 0.005) {
        artifactPageFailures.push(
          `/${route}/ [n=${stats.n} ${stats.winRatePct}%] != champion ${id} artifact [n=${entry.stats.n} ${expectedPct}%]`,
        );
      }
    }
  }
  record('champion page renders its detail artifact', artifactPagesChecked, artifactPageFailures);

  finish(dist, pages, checks);
}

main();