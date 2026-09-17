#!/usr/bin/env node
// Regenerates the checked-in demo fixture tree at web/src/fixtures/v1.
//
// The fixtures exist so that the site can be built and inspected when no
// aggregate snapshot has been published, and so that the layout can be
// exercised in CI without a live crawl. They are NOT statistics: every value is
// produced by a simulated window, the manifest declares source: "demo", and the
// site renders a high-contrast preview banner on every page that reads them.
// Nothing here is measured from a game.
//
// The tree is *consistent* because it is derived from one measurement per
// (window, role, champion) pair. The simulation below plays a fixed number of
// games in a synthetic 80-champion pool and tallies, per champion and per pair,
// exactly what the aggregate engineer tallies from a real crawl: games, wins
// and bans. Every artifact - the tier list, the champion page, the matchup
// matrix - is then a projection of that one tally through the same publish
// policy internal/aggregate applies (a tally below min_cell_n is withheld;
// pick_rate is n/(2*matches); ban_rate is bans/matches; the tier is graded on
// the cell's own win rate against the window baseline). Because the views are
// projections of one tally, a champion's win rate cannot differ between the
// tier list and its champion page, and a champion the matrix can render is a
// champion the tier list published. The previous-patch partition is a slice of
// the same simulated window, not an independent draw, so a cross-patch link
// lands on a page that either agrees or explains itself.
//
// The script is deterministic: regenerating it produces byte-identical files,
// so a fixture change shows up as a real diff rather than as churn.
// web/scripts/check-fixture-consistency.mjs asserts every invariant above.
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
  // The contract's window is a pair of days, not a pair of instants: the
  // verifier parses both ends as YYYY-MM-DD and says so when they are not.
  source_window: { from: '2026-09-08', to: '2026-09-14' },
  previous_window: { from: '2026-09-01', to: '2026-09-07' },
  min_cell_n: 500,
  // How many games each simulated window contains. Published sample sizes are
  // derived from these, so `matches` is the only place the magnitude of the
  // numbers comes from. Each game fields two distinct champions, so a champion's
  // pick_rate is n/(2*matches) and the sum of every cell's n is 2*matches.
  matches: 600000,
  previous_matches: 480000,
};

// Mirrors internal/aggregate/tier.go: the bands are percentage points above the
// window baseline win rate. Every simulated game has exactly one winner, so the
// baseline a tier is graded against is exactly 0.5 - the number
// internal/aggregate measures from its own tallies.
const BASELINE_WIN_RATE = 0.5;
const TIER_BANDS = [
  ['S+', 2.0],
  ['S', 1.2],
  ['A', 0.6],
  ['B', -0.6],
  ['C', -1.5],
  ['D', Number.NEGATIVE_INFINITY],
];

/** Mirrors internal/aggregate: the published interval is 0.98/sqrt(n). */
const CONFIDENCE_HALF_WIDTH_FACTOR = 0.98;

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

/**
 * The roles each champion is published in, assigned across the whole roster
 * rather than one champion at a time. A per-champion hash leaves one board with
 * half the champions of another, and a board of sixteen concentrates the same
 * role's pick share on a couple of names.
 *
 * Set once by main(), because the assignment is a property of the roster.
 */
let rolesById = new Map();

function rolesFor(champion) {
  return rolesById.get(champion.id) ?? [];
}

function assignRoles(champions) {
  const assignment = new Map(champions.map((champion) => [champion.id, []]));
  const byHash = (pass) =>
    [...champions].sort(
      (a, b) => hash(`role:${pass}:${a.id}`) - hash(`role:${pass}:${b.id}`) || a.id - b.id,
    );
  for (const pass of [0, 1]) {
    byHash(pass).forEach((champion, i) => {
      const role = ROLES[(i + pass) % ROLES.length];
      const roles = assignment.get(champion.id);
      if (!roles.includes(role)) roles.push(role);
    });
  }
  return assignment;
}

/** mulberry32, seeded from the stable string hash above. */
function prng(label) {
  let a = hash(label);
  return function next() {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * A champion's share of the role's picks. Skewed deliberately: the thin tail
 * falls under min_cell_n and is withheld, which is the suppression path the
 * site has to render.
 */
function popularityWeight(championId, role) {
  const u = unit(`pop:${championId}:${role}`);
  return 0.004 + u * u * 1.6;
}

/**
 * A champion's win probability against an even opponent in this role.
 * Irwin-Hall(3) keeps most champions near even and puts a few in the tails,
 * which is what a published tier ladder is shaped like.
 */
function strengthOf(championId, role) {
  const z = 2 * (unit(`s1:${championId}:${role}`) + unit(`s2:${championId}:${role}`) + unit(`s3:${championId}:${role}`) - 1.5);
  return 0.5 + z * 0.024;
}

/** Distinct games the champion was banned in, over the same window. */
function bansFor(patch, championId, matches) {
  const u = unit(`ban:${patch}:${championId}`);
  return Math.min(matches, Math.round(matches * (0.002 + u * u * 0.18)));
}

/**
 * Plays `matches` games of one role in one window, both teams fielding a
 * champion from `roster`, and returns the window's tallies: one record per
 * champion and one per unordered pair. This is the single measurement every
 * published view is derived from.
 */
function simulate(windowTag, role, roster, matches) {
  const rand = prng(`sim:${windowTag}:${role}`);
  const ids = roster.map((champion) => champion.id);
  const cum = [];
  const strengths = [];
  let total = 0;
  for (let i = 0; i < ids.length; i += 1) {
    total += popularityWeight(ids[i], role);
    cum.push(total);
    strengths.push(strengthOf(ids[i], role));
  }
  const counts = new Map(ids.map((id) => [id, { n: 0, wins: 0 }]));
  const pairs = new Map();
  const draw = (exclude) => {
    for (;;) {
      const r = rand() * total;
      let lo = 0;
      let hi = cum.length - 1;
      while (lo < hi) {
        const mid = (lo + hi) >> 1;
        if (cum[mid] <= r) lo = mid + 1;
        else hi = mid;
      }
      if (lo !== exclude) return lo;
    }
  };
  for (let game = 0; game < matches; game += 1) {
    const a = draw(-1);
    const b = draw(a);
    const aWins = rand() < strengths[a] / (strengths[a] + strengths[b]);
    const tallyA = counts.get(ids[a]);
    const tallyB = counts.get(ids[b]);
    tallyA.n += 1;
    tallyB.n += 1;
    if (aWins) tallyA.wins += 1;
    else tallyB.wins += 1;
    const low = Math.min(ids[a], ids[b]);
    const high = Math.max(ids[a], ids[b]);
    const key = `${low}:${high}`;
    const pair = pairs.get(key) ?? { champion_id: low, opponent_id: high, n: 0, wins: 0 };
    pair.n += 1;
    if (aWins === (ids[a] === low)) pair.wins += 1;
    pairs.set(key, pair);
  }
  return { counts, pairs: [...pairs.values()] };
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

function tierForRate(winRate) {
  const diffPP = (winRate - BASELINE_WIN_RATE) * 100;
  const band = TIER_BANDS.find(([, minPP]) => diffPP >= minPP);
  return band ? band[0] : 'D';
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

/**
 * One manifest `partitions[]` entry.
 *
 * Deliberately not the envelope. The envelope carries `schema` and `source` so
 * every artifact can state its own provenance, but the manifest's Partition
 * declares neither - the manifest states the source once, for the tree - and the
 * schema the verifier checks against sets additionalProperties: false. Spreading
 * the envelope here therefore publishes two properties the contract does not
 * declare, which is a problem rather than harmless duplication.
 */
function partitionEntry(partition, details) {
  return {
    patch: partition.patch,
    region: partition.region,
    queue: partition.queue,
    bracket: partition.bracket,
    generated_at: DEMO.generated_at,
    source_window: partition.source_window,
    min_cell_n: DEMO.min_cell_n,
    suppressed_cells: partition.suppressed_cells,
    ...details,
  };
}

function sortCells(cells) {
  cells.sort((a, b) => {
    if (a.role !== b.role) return ROLES.indexOf(a.role) - ROLES.indexOf(b.role);
    if (a.win_rate !== b.win_rate) return b.win_rate - a.win_rate;
    return a.champion_id - b.champion_id;
  });
  return cells;
}

/**
 * Projects a window's tallies onto the published cell shape, applying the policy
 * internal/aggregate applies: a tally with no games is dropped, a tally below
 * min_cell_n is withheld - and a withheld cell carries no tier, no rates and no
 * interval, because there is nothing behind them - and every other cell carries
 * rates derived from its own tally.
 */
function windowCells(windowTag, role, roster, matches, tallies) {
  const published = [];
  let suppressed = 0;
  for (const champion of roster) {
    const tally = tallies.counts.get(champion.id);
    if (!tally || tally.n === 0) continue;
    if (tally.n < DEMO.min_cell_n) {
      suppressed += 1;
      continue;
    }
    const winRate = round(tally.wins / tally.n, 4);
    published.push({
      champion_id: champion.id,
      role,
      n: tally.n,
      wins: tally.wins,
      win_rate: winRate,
      pick_rate: round(tally.n / (2 * matches), 4),
      ban_rate: round(bansFor(windowTag, champion.id, matches) / matches, 4),
      tier: tierForRate(winRate),
      ci95_half_width: round(CONFIDENCE_HALF_WIDTH_FACTOR / Math.sqrt(tally.n), 4),
    });
  }
  return { cells: sortCells(published), suppressed };
}

function tierList(partition, cells) {
  return { ...envelope(partition), cells };
}

/**
 * Splits a role's published sample into `count` rows that together cover
 * `coverageLo`-`coverageHi` of it. No row is the whole sample, so a build row
 * can never claim more games than the cell it belongs to.
 */
function shareRows(seed, count, roleN, coverageLo, coverageHi) {
  const weights = [];
  for (let i = 0; i < count; i += 1) weights.push(0.4 + unit(`${seed}:sh:${i}`));
  const totalWeight = weights.reduce((sum, weight) => sum + weight, 0);
  const coverage = coverageLo + unit(`${seed}:cov`) * (coverageHi - coverageLo);
  return weights.map((weight, i) => ({
    i,
    n: Math.max(1, Math.min(roleN - 1, Math.round((roleN * coverage * weight) / totalWeight))),
  }));
}

function buildRows(kind, seed, table, roleN) {
  return shareRows(seed, table.length, roleN, 0.35, 0.6)
    .map(({ i, n }) => {
      const wins = Math.round(n * (0.4 + unit(`${seed}:w:${i}`) * 0.18));
      return {
        kind,
        key: table[i],
        label: table[i].join('-'),
        n,
        wins,
        win_rate: round(wins / n, 4),
      };
    })
    .sort((a, b) => b.n - a.n);
}

const SKILL_ORDERS = ['Q-E-W', 'E-Q-W', 'W-Q-E'];

function skillOrderRows(seed, roleN) {
  return shareRows(seed, SKILL_ORDERS.length, roleN, 0.2, 0.35)
    .map(({ i, n }) => {
      const wins = Math.round(n * (0.44 + unit(`${seed}:w:${i}`) * 0.12));
      return { order: SKILL_ORDERS[i], n, wins, win_rate: round(wins / n, 4) };
    })
    .sort((a, b) => b.n - a.n);
}

/**
 * A champion artifact carries exactly the roles the partition published a cell
 * for, so a champion page can never show a role the tier list withheld.
 */
function championArtifact(partition, champion, publishedRoles, cellsByRole) {
  const roles = publishedRoles.map((role) => {
    const stats = cellsByRole.get(role).get(champion.id);
    return {
      role,
      stats,
      items: buildRows('items', `item:${champion.id}:${role}`, ITEM_BUILDS[role], stats.n),
      runes: buildRows('runes', `rune:${champion.id}:${role}`, RUNE_BUILDS[role], stats.n),
      spells: buildRows('spells', `spell:${champion.id}:${role}`, SPELL_BUILDS[role], stats.n),
      skill_orders: skillOrderRows(`so:${champion.id}:${role}`, stats.n),
    };
  });
  return {
    ...envelope(partition),
    champion_id: champion.id,
    champion_slug: champion.slug,
    roles,
  };
}

/**
 * The matchup artifact for a role. Its `champions` axis is exactly the champions
 * the tier list published for that role - the axis is what the site renders as
 * the board, so a name on the board is a name with a published sample - and its
 * cells are the pairs of that axis that cleared the floor. A pair is rendered by
 * the site whenever both of its endpoints are on the axis, so the axis and the
 * cells have to be derived from the one tally to stay in step.
 */
function matchupArtifact(partition, role, publishedIds, pairs) {
  const axis = [...publishedIds].sort((a, b) => a - b);
  const onAxis = new Set(axis);
  const cells = pairs
    .filter(
      (pair) =>
        onAxis.has(pair.champion_id) && onAxis.has(pair.opponent_id) && pair.n >= DEMO.min_cell_n,
    )
    .map((pair) => {
      const winRate = round(pair.wins / pair.n, 4);
      return {
        champion_id: pair.champion_id,
        opponent_id: pair.opponent_id,
        n: pair.n,
        wins: pair.wins,
        win_rate: winRate,
        ci95_half_width: round(CONFIDENCE_HALF_WIDTH_FACTOR / Math.sqrt(pair.n), 4),
      };
    });
  cells.sort((a, b) => a.champion_id - b.champion_id || a.opponent_id - b.opponent_id);
  return { ...envelope(partition), role, champions: axis, cells };
}

function main() {
  const { out } = parseArgs(process.argv.slice(2));
  const source = JSON.parse(readFileSync(CHAMPIONS_FILE, 'utf8'));
  const champions = source.champions;

  const population = partitionChampions(champions, 80);
  const previousSlice = new Set(partitionChampions(champions, 40).map((champion) => champion.id));
  rolesById = assignRoles(champions);

  const latestPartition = {
    patch: DEMO.patch,
    region: DEMO.region,
    queue: DEMO.queue,
    bracket: DEMO.bracket,
    source_window: DEMO.source_window,
    suppressed_cells: 0,
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

  // One simulation per (window, role). The previous window plays the same
  // population as the current one, so its pick rates are comparable and its
  // cells are a slice of one snapshot rather than an independent draw.
  const latestCells = [];
  const previousCells = [];
  const latestByRole = new Map();
  const previousByRole = new Map();
  const latestPairs = new Map();
  let latestSuppressed = 0;
  let previousSuppressed = 0;

  for (const role of ROLES) {
    const roster = population.filter((champion) => rolesFor(champion).includes(role));
    const latestWindow = simulate(DEMO.patch, role, roster, DEMO.matches);
    const previousWindow = simulate(DEMO.previous_patch, role, roster, DEMO.previous_matches);

    const published = windowCells(DEMO.patch, role, roster, DEMO.matches, latestWindow);
    latestCells.push(...published.cells);
    latestSuppressed += published.suppressed;
    latestByRole.set(role, new Map(published.cells.map((cell) => [cell.champion_id, cell])));
    latestPairs.set(role, latestWindow.pairs);

    // The previous partition keeps the part of its slice that the current
    // snapshot also publishes, so no page of one patch links to a page of the
    // other that has nothing to say about the champion.
    const previousRoster = roster.filter(
      (champion) => previousSlice.has(champion.id) && latestByRole.get(role).has(champion.id),
    );
    const previousPublished = windowCells(
      DEMO.previous_patch,
      role,
      previousRoster,
      DEMO.previous_matches,
      previousWindow,
    );
    previousCells.push(...previousPublished.cells);
    previousByRole.set(role, new Map(previousPublished.cells.map((cell) => [cell.champion_id, cell])));
    previousSuppressed += previousPublished.suppressed;
  }

  latestPartition.suppressed_cells = latestSuppressed;
  previousPartition.suppressed_cells = previousSuppressed;
  const latestTierList = tierList(latestPartition, sortCells(latestCells));
  const previousTierList = tierList(previousPartition, sortCells(previousCells));

  const idsOf = (cells) => [...new Set(cells.map((cell) => cell.champion_id))].sort((a, b) => a - b);
  const partitions = [
    partitionEntry(latestPartition, {
      cells_published: latestTierList.cells.length,
      build_run_id: 2,
      git_sha: '0000000000000000000000000000000000000002',
      champions: idsOf(latestTierList.cells),
      matchup_roles: ROLES,
    }),
    partitionEntry(previousPartition, {
      cells_published: previousTierList.cells.length,
      build_run_id: 1,
      git_sha: '0000000000000000000000000000000000000001',
      champions: idsOf(previousTierList.cells),
      matchup_roles: [],
    }),
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

  // Champion detail exists for every champion a partition published a cell for,
  // in every partition that published one - not just for a subset of the latest
  // patch. The manifest lists those champions as `champions`, and the tree's own
  // verifier requires exactly one artifact per listed id (and rejects an
  // unlisted file), so a champion with a published cell and no artifact leaves
  // the manifest describing a file that does not exist. The site would not show
  // it: /champions/<slug>/<role>/ prefers the artifact and otherwise falls back
  // to the tier-list cell, which is why only the verifier sees the gap.
  for (const { partition, byRole } of [
    { partition: latestPartition, byRole: latestByRole },
    { partition: previousPartition, byRole: previousByRole },
  ]) {
    const publishedRolesFor = (champion) => rolesFor(champion).filter((role) => byRole.get(role).has(champion.id));
    for (const champion of population) {
      const publishedRoles = publishedRolesFor(champion);
      if (publishedRoles.length === 0) continue;
      writeJson(
        join(base(partition), 'champions', `${champion.id}.json`),
        championArtifact(partition, champion, publishedRoles, byRole),
      );
    }
  }
  for (const role of ROLES) {
    const publishedIds = [...latestByRole.get(role).keys()];
    writeJson(
      join(base(latestPartition), 'matchups', `${ROLE_SLUGS[role]}.json`),
      matchupArtifact(latestPartition, role, publishedIds, latestPairs.get(role)),
    );
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
    `wrote demo fixtures to ${out} (patch ${DEMO.patch}: ${latestTierList.cells.length} cells from ` +
      `${DEMO.matches} simulated games, ${latestSuppressed} withheld; patch ${DEMO.previous_patch}: ` +
      `${previousTierList.cells.length} cells, ${previousSuppressed} withheld; ` +
      `${partitions[0].champions.length + partitions[1].champions.length} champion artifacts, ` +
      `${ROLES.length} matchup artifacts)\n`,
  );
}

main();
