#!/usr/bin/env node
// Regenerates the checked-in demo fixture tree at web/src/fixtures/v1.
//
// The fixtures exist so that the site can be built and inspected when no
// aggregate snapshot has been published, and so that the layout can be
// exercised in CI without a live crawl. They are NOT statistics: every value is
// derived from a hash of the champion id and role, the manifest declares
// source: "demo", and the site renders a high-contrast preview banner on every
// page that reads them. Nothing here is measured from a game.
//
// The script is deterministic: regenerating it produces byte-identical files,
// so a fixture change shows up as a real diff rather than as churn.
//
//   node web/scripts/make-fixtures.mjs [--out <dir>]

import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const WEB_ROOT = fileURLToPath(new URL('..', import.meta.url));
const CHAMPIONS_FILE = join(WEB_ROOT, 'src', 'data', 'champions.json');

const ROLES = ['TOP', 'JUNGLE', 'MID', 'BOTTOM', 'SUPPORT'];
const ROLE_SLUGS = { TOP: 'top', JUNGLE: 'jungle', MID: 'mid', BOTTOM: 'bottom', SUPPORT: 'support' };
const TIERS = ['S+', 'S', 'A', 'B', 'C', 'D'];

/** The window the demo snapshot pretends to cover. Fixed, so the tree is reproducible. */
const DEMO = {
  patch: '16.18',
  previous_patch: '16.17',
  region: 'EUW',
  queue: 420,
  bracket: 'all',
  generated_at: '2026-09-15T04:10:00Z',
  source_window: { from: '2026-09-08T00:00:00Z', to: '2026-09-14T23:59:59Z' },
  previous_window: { from: '2026-09-01T00:00:00Z', to: '2026-09-07T23:59:59Z' },
  min_cell_n: 500,
};

function parseArgs(argv) {
  const out = { out: join(WEB_ROOT, 'src', 'fixtures') };
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === '--out' && argv[i + 1]) {
      out.out = resolve(argv[i + 1]);
      i += 1;
    }
  }
  return out;
}

/** FNV-1a. Stable across runs and platforms, unlike Math.random. */
function hash(text) {
  let h = 0x811c9dc5;
  for (let i = 0; i < text.length; i += 1) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

function pick(seed, lo, hi) {
  return lo + (hash(seed) % (hi - lo + 1));
}

function unit(seed) {
  return hash(seed) / 0xffffffff;
}

function round(value, digits) {
  const scale = 10 ** digits;
  return Math.round(value * scale) / scale;
}

function writeJson(file, value) {
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
}

/** Champions to publish for a partition: a deterministic spread of ids. */
function partitionChampions(champions, count) {
  const step = Math.max(1, Math.floor(champions.length / count));
  const out = [];
  for (let i = 0; i < champions.length && out.length < count; i += step) out.push(champions[i]);
  return out;
}

function rolesFor(champion) {
  const first = ROLES[hash(`role:${champion.id}`) % ROLES.length];
  const second = ROLES[hash(`role2:${champion.id}`) % ROLES.length];
  return second !== first && hash(`dual:${champion.id}`) % 3 === 0 ? [first, second] : [first];
}

function cell(seed, champion, role) {
  const n = unit(`${seed}:n:${champion.id}:${role}`) < 0.04 ? 0 : pick(`${seed}:n:${champion.id}:${role}`, 900, 60000);
  const winRate = round(0.435 + unit(`${seed}:w:${champion.id}:${role}`) * 0.13, 4);
  const wins = Math.round(n * winRate);
  const pickRate = round(0.002 + unit(`${seed}:p:${champion.id}:${role}`) * 0.09, 4);
  const banRate = round(0.002 + unit(`${seed}:b:${champion.id}:${role}`) * 0.14, 4);
  const p = n === 0 ? 0.5 : wins / n;
  return {
    champion_id: champion.id,
    role,
    n,
    wins,
    win_rate: n === 0 ? 0 : round(wins / n, 4),
    pick_rate: pickRate,
    ban_rate: banRate,
    tier: tierFor(winRate, pickRate + banRate),
    ci95_half_width: n === 0 ? 0 : round(1.96 * Math.sqrt((p * (1 - p)) / n), 4),
  };
}

// Simulated build tables, keyed by role the way the aggregate tree keys them.
//
// The ids are the real Data Dragon ids for the items, runes and summoner spells
// they name (the same ids the aggregate engineer's `demo` subcommand emits), so
// the preview exercises the icon plumbing in BuildList rather than the numeric
// chip fallback. Only the counts and rates in this tree are invented; it is
// labelled `source: "demo"` per artifact, and the site renders the PREVIEW
// banner on every route built from it.
const ITEM_BUILDS = {
  TOP: [
    [3078, 3047, 6333, 3053, 3071, 3068, 3026],
    [3078, 3111, 6333, 3053, 3143, 3068, 3026],
    [3078, 3047, 6333, 3053, 3071, 3135, 3026],
  ],
  JUNGLE: [
    [6692, 3047, 6333, 3053, 3071, 3068, 3026],
    [6692, 3111, 6333, 3053, 3143, 3068, 3026],
    [3078, 3047, 6333, 3053, 3071, 3026, 3068],
  ],
  MID: [
    [6655, 3020, 4645, 3089, 3135, 3157, 3165],
    [6655, 3020, 4645, 3089, 3157, 3135, 3165],
    [6653, 3020, 3165, 3089, 3135, 3157, 4645],
  ],
  BOTTOM: [
    [6672, 3006, 3031, 6675, 3036, 3072, 3026],
    [6672, 3006, 3031, 6675, 3036, 3026, 3072],
    [6672, 3006, 6676, 3031, 6675, 3036, 3072],
  ],
  SUPPORT: [
    [3869, 3158, 2065, 3107, 3190, 3222, 4005],
    [3877, 3158, 2065, 3107, 3190, 3222, 4005],
    [3865, 3158, 3870, 3107, 3190, 3222, 4005],
  ],
};

// [primary tree, keystone, secondary tree, three stat shards]
const RUNE_BUILDS = {
  TOP: [
    [8000, 8010, 8400, 5008, 5008, 5001],
    [8000, 8005, 8400, 5005, 5008, 5001],
    [8400, 8437, 8000, 5008, 5001, 5001],
  ],
  JUNGLE: [
    [8000, 8010, 8100, 5005, 5008, 5001],
    [8100, 8112, 8000, 5005, 5008, 5001],
    [8000, 8021, 8300, 5005, 5008, 5001],
  ],
  MID: [
    [8200, 8229, 8100, 5008, 5008, 5001],
    [8100, 8112, 8200, 5008, 5008, 5001],
    [8200, 8214, 8300, 5008, 5007, 5001],
  ],
  BOTTOM: [
    [8000, 8008, 8300, 5005, 5008, 5001],
    [8000, 8021, 8400, 5005, 5008, 5001],
    [8000, 8010, 8200, 5005, 5008, 5001],
  ],
  SUPPORT: [
    [8400, 8465, 8300, 5008, 5002, 5001],
    [8300, 8360, 8400, 5008, 5003, 5001],
    [8400, 8439, 8300, 5008, 5002, 5001],
  ],
};

const SPELL_BUILDS = {
  TOP: [[4, 12], [4, 14], [4, 11]],
  JUNGLE: [[4, 11], [4, 12]],
  MID: [[4, 12], [4, 14], [4, 21]],
  BOTTOM: [[4, 7], [4, 21], [4, 14]],
  SUPPORT: [[4, 14], [4, 3], [4, 7]],
};

function tierFor(winRate, popularity) {
  const score = (winRate - 0.5) / 0.035 + (popularity - 0.06) / 0.06;
  if (score > 1.5) return 'S+';
  if (score > 0.9) return 'S';
  if (score > 0.3) return 'A';
  if (score > -0.3) return 'B';
  if (score > -0.9) return 'C';
  return 'D';
}

function envelope(partition) {
  return {
    schema: 1,
    // Every envelope carries the provenance its reader keys its trust on, so the
    // fixture tree says "demo" per artifact and not only in the manifest.
    source: 'demo',
    patch: partition.patch,
    region: partition.region,
    queue: partition.queue,
    bracket: partition.bracket,
    generated_at: DEMO.generated_at,
    source_window: partition.source_window,
    min_cell_n: DEMO.min_cell_n,
    suppressed_cells: partition.suppressed_cells,
  };
}

function tierList(partition, champions) {
  const cells = [];
  for (const champion of champions) {
    for (const role of rolesFor(champion)) cells.push(cell(`tl:${partition.patch}`, champion, role));
  }
  cells.sort((a, b) => (a.role === b.role ? a.champion_id - b.champion_id : ROLES.indexOf(a.role) - ROLES.indexOf(b.role)));
  return { ...envelope(partition), cells };
}

function build(kind, seed, ids) {
  const n = pick(`${seed}:n`, 400, 9000);
  const winRate = round(0.42 + unit(`${seed}:w`) * 0.18, 4);
  return {
    kind,
    key: ids,
    label: ids.join('-'),
    n,
    wins: Math.round(n * winRate),
    win_rate: winRate,
  };
}

function championArtifact(partition, champion) {
  const roles = rolesFor(champion).map((role) => ({
    role,
    stats: cell(`tl:${partition.patch}`, champion, role),
    items: ITEM_BUILDS[role].map((ids, i) => build('items', `item:${champion.id}:${role}:${i}`, ids)),
    runes: RUNE_BUILDS[role].map((ids, i) => build('runes', `rune:${champion.id}:${role}:${i}`, ids)),
    spells: SPELL_BUILDS[role].map((ids, i) => build('spells', `spell:${champion.id}:${role}:${i}`, ids)),
    skill_orders: ['Q-E-W', 'E-Q-W', 'W-Q-E'].map((order, i) => {
      const n = pick(`so:${champion.id}:${role}:${i}`, 200, 5000);
      const winRate = round(0.44 + unit(`so:${champion.id}:${role}:${i}`) * 0.12, 4);
      return { order, n, wins: Math.round(n * winRate), win_rate: winRate };
    }),
  }));
  return {
    ...envelope(partition),
    champion_id: champion.id,
    champion_slug: champion.slug,
    roles,
  };
}

function matchupArtifact(partition, role, champions) {
  const cells = [];
  for (const champion of champions) {
    for (const opponent of champions) {
      // aggmodel emits one direction per ordered pair, lower champion id first,
      // and the site mirrors it. The fixture does the same so the mirroring path
      // is what the demo build actually exercises.
      if (champion.id >= opponent.id) continue;
      const n = pick(`mu:${partition.patch}:${role}:${champion.id}:${opponent.id}`, 120, 4000);
      const winRate = round(0.38 + unit(`mu:${partition.patch}:${role}:${champion.id}:${opponent.id}`) * 0.24, 4);
      const p = Math.round(n * winRate) / n;
      cells.push({
        champion_id: champion.id,
        opponent_id: opponent.id,
        n,
        wins: Math.round(n * winRate),
        win_rate: Math.round(n * winRate) / n,
        ci95_half_width: round(1.96 * Math.sqrt((p * (1 - p)) / n), 4),
      });
    }
  }
  return { ...envelope(partition), role, champions: champions.map((c) => c.id), cells };
}

function main() {
  const { out } = parseArgs(process.argv.slice(2));
  const source = JSON.parse(readFileSync(CHAMPIONS_FILE, 'utf8'));
  const champions = source.champions;

  const latest = partitionChampions(champions, 80);
  const previous = partitionChampions(champions, 40);
  const matchupPool = latest.slice(0, 20);
  const detailPool = latest.slice(0, 30);

  const latestPartition = {
    patch: DEMO.patch,
    region: DEMO.region,
    queue: DEMO.queue,
    bracket: DEMO.bracket,
    source_window: DEMO.source_window,
    suppressed_cells: 2,
    roles: ROLES,
  };
  const previousPartition = {
    patch: DEMO.previous_patch,
    region: DEMO.region,
    queue: DEMO.queue,
    bracket: DEMO.bracket,
    source_window: DEMO.previous_window,
    suppressed_cells: 0,
    roles: ROLES,
  };

  const latestTierList = tierList(latestPartition, latest);
  const previousTierList = tierList(previousPartition, previous);

  const partitions = [
    {
      ...envelope(latestPartition),
      cells_published: latestTierList.cells.length,
      build_run_id: 2,
      git_sha: '0000000000000000000000000000000000000002',
      champions: [...new Set(latestTierList.cells.map((c) => c.champion_id))],
      matchup_roles: ROLES,
    },
    {
      ...envelope(previousPartition),
      cells_published: previousTierList.cells.length,
      build_run_id: 1,
      git_sha: '0000000000000000000000000000000000000001',
      champions: [...new Set(previousTierList.cells.map((c) => c.champion_id))],
      matchup_roles: [],
    },
  ];

  const manifest = {
    schema: 1,
    generated_at: DEMO.generated_at,
    // Read by the site build to decide which of its three data states to render.
    source: 'demo',
    latest: partitions[0],
    partitions,
  };

  rmSync(join(out, 'v1'), { recursive: true, force: true });
  writeJson(join(out, 'v1', 'manifest.json'), manifest);

  const base = (partition) => join(out, 'v1', 'p', partition.patch, partition.region, String(partition.queue), partition.bracket);
  writeJson(join(base(latestPartition), 'tierlist.json'), latestTierList);
  writeJson(join(base(previousPartition), 'tierlist.json'), previousTierList);

  for (const champion of detailPool) {
    writeJson(join(base(latestPartition), 'champions', `${champion.id}.json`), championArtifact(latestPartition, champion));
  }
  for (const role of ROLES) {
    writeJson(join(base(latestPartition), 'matchups', `${ROLE_SLUGS[role]}.json`), matchupArtifact(latestPartition, role, matchupPool));
  }

  writeJson(join(out, 'v1', 'static', source.ddragon_version, 'champions.json'), {
    ddragon_version: source.ddragon_version,
    champions: champions.map((champion) => ({
      ...champion,
      roles: rolesFor(champion),
    })),
  });
  writeJson(join(out, 'v1', 'static', source.ddragon_version, 'patches.json'), {
    ddragon_version: source.ddragon_version,
    latest: DEMO.patch,
    patches: [DEMO.patch, DEMO.previous_patch, '16.16', '16.15'],
  });

  process.stdout.write(
    `wrote demo fixtures to ${out} (patch ${DEMO.patch}: ${latestTierList.cells.length} cells, ` +
      `${detailPool.length} champion artifacts, ${ROLES.length} matchup artifacts)\n`,
  );
}

main();
