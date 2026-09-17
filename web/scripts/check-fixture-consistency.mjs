#!/usr/bin/env node
// Cross-view reconciliation for a demo artifact tree.
//
// The site shows one fact in four places: the tier list, the champion page, the
// matchup matrix and the champion-by-role board. They are only consistent if
// they are projections of one measurement, so this checks the published
// artifacts the way each page reads them, and fails when two pages would show
// different numbers for the same (partition, champion, role).
//
// It reads an artifact tree, not the built site, so it runs in a second and can
// gate the generator. Everything it asserts comes from the published payload:
//
//   * a cell is only published when its sample clears partition.min_cell_n, and
//     a cell with no sample carries no tier, no pick rate and no ban rate;
//   * a champion's cell is byte-identical in the tier list and in its champion
//     artifact (the role page prefers the artifact and falls back to the tier
//     list, so both have to say the same thing);
//   * the matchup matrix's champion axis - the board the site renders - is the
//     set of champions the tier list published for that role, and every pair on
//     the board has a published cell for both of its endpoints;
//   * every published champion is in the static champion list, because that is
//     what decides which routes exist at all;
//   * every champion the manifest lists has a `champions/<id>.json` and every
//     file on disk is listed, because the manifest is what the site indexes and
//     the champion route reads the file named by the id;
//   * every artifact in the tree parses, so a tree that cannot be read is a
//     named failure rather than a crash in the middle of a check.
//
//   node web/scripts/check-fixture-consistency.mjs [--root web/src/fixtures/v1]

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { basename, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const WEB_ROOT = fileURLToPath(new URL('..', import.meta.url));
const ROLES = ['TOP', 'JUNGLE', 'MID', 'BOTTOM', 'SUPPORT'];
const TIERS = ['S+', 'S', 'A', 'B', 'C', 'D'];

function parseArgs(argv) {
  const out = { root: join(WEB_ROOT, 'src', 'fixtures', 'v1'), limit: 5, json: false };
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === '--root' && argv[i + 1]) {
      out.root = resolve(argv[i + 1]);
      i += 1;
    } else if (argv[i] === '--limit' && argv[i + 1]) {
      out.limit = Number(argv[i + 1]);
      i += 1;
    } else if (argv[i] === '--json') {
      out.json = true;
    }
  }
  return out;
}

function readJson(file) {
  let text;
  try {
    text = readFileSync(file, 'utf8');
  } catch (error) {
    throw new Error(`cannot read ${file}: ${error.message}`);
  }
  try {
    return JSON.parse(text);
  } catch (error) {
    throw new Error(`cannot read ${file}: not valid JSON (${error.message})`);
  }
}

function readJsonIfPresent(file) {
  try {
    return statSync(file).isFile() ? readJson(file) : null;
  } catch {
    return null;
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

/** Every .json file under `dir`, so a tree can be checked for readability first. */
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

/** Directory names in `dir`, or none if it does not exist. */
function listDirs(dir) {
  try {
    return readdirSync(dir, { withFileTypes: true })
      .filter((entry) => entry.isDirectory())
      .map((entry) => entry.name);
  } catch {
    return [];
  }
}

/**
 * The champion artifact is addressed as `<champion_id>.json` (see
 * web/src/lib/paths.ts), so a file whose name is not its own id is a page that
 * would read whatever happens to sit on the route it asks for.
 */
function artifactFileId(file) {
  return Number(basename(file, '.json'));
}

/** Collects failures per check, so the report can say what was checked at all. */
class Report {
  constructor(limit) {
    this.limit = limit;
    this.checks = new Map();
  }

  /** Records one check, whether or not it failed. */
  record(name, checked, failures) {
    const entry = this.checks.get(name) ?? { name, checked: 0, failures: [] };
    entry.checked += checked;
    for (const failure of failures) entry.failures.push(failure);
    this.checks.set(name, entry);
  }

  fail(name, checked, failure) {
    this.record(name, checked, [failure]);
  }

  get failureCount() {
    let total = 0;
    for (const entry of this.checks.values()) total += entry.failures.length;
    return total;
  }
}

function segOf(partition) {
  return join('p', partition.patch, partition.region, String(partition.queue), partition.bracket);
}

function cellKey(role, championId) {
  return `${role}:${championId}`;
}

function closeTo(a, b) {
  return typeof a === 'number' && typeof b === 'number' && Math.abs(a - b) < 1e-9;
}

function sameCell(a, b) {
  const keys = ['champion_id', 'role', 'n', 'wins', 'win_rate', 'pick_rate', 'ban_rate', 'tier', 'ci95_half_width'];
  return keys.every((key) => (key === 'tier' ? a[key] === b[key] : closeTo(a[key], b[key]) || a[key] === b[key]));
}

function describeCell(cell) {
  return `n=${cell.n} wins=${cell.wins} win_rate=${cell.win_rate} tier=${cell.tier} pick=${cell.pick_rate} ban=${cell.ban_rate}`;
}


/**
 * The published-cell contract, read off the payload alone: a cell below the
 * floor, or with no games at all, is not something the pipeline may emit.
 * Returns a description of the first broken rule, or null.
 */
function cellShapeFailure(cell, minCellN, where) {
  if (!Number.isFinite(cell.n) || !Number.isFinite(cell.wins)) {
    return `${where}: n/wins missing (${describeCell(cell)})`;
  }
  if (cell.n <= 0) return `${where}: published with no games (${describeCell(cell)})`;
  if (cell.n < minCellN) return `${where}: n=${cell.n} is below the floor ${minCellN}`;
  if (cell.wins > cell.n) return `${where}: wins exceed games (${describeCell(cell)})`;
  // Accept the exact ratio or its 4dp rounding, which is what the payload stores.
  if (!(Math.abs(cell.win_rate - cell.wins / cell.n) <= 5e-5)) {
    return `${where}: win_rate ${cell.win_rate} is not wins/n (${cell.wins}/${cell.n})`;
  }
  for (const rate of ['pick_rate', 'ban_rate']) {
    if (cell[rate] === undefined) continue;
    if (!(cell[rate] >= 0 && cell[rate] <= 1)) return `${where}: ${rate} out of range (${cell[rate]})`;
  }
  if (cell.tier !== undefined && !TIERS.includes(cell.tier)) {
    return `${where}: tier ${JSON.stringify(cell.tier)} is not a tier at all`;
  }
  return null;
}

function round4(value) {
  return Math.round(value * 10000) / 10000;
}

/**
 * A cell with no sample must not carry a statistic. This is the fail-closed
 * rule stated the other way round: a number is published only with a sample
 * behind it, so an unsupported cell has to be absent rather than zeroed.
 */
function checkNoUnsupportedNumbers(report, partition, cells) {
  const orphans = [];
  for (const cell of cells) {
    const carries =
      cell.win_rate !== undefined || cell.pick_rate !== undefined || cell.ban_rate !== undefined || cell.tier !== undefined;
    if (!(cell.n > 0) && carries) {
      orphans.push(
        `patch ${partition.patch} ${cell.role} champion ${cell.champion_id}: n=${cell.n} carries tier=${cell.tier} ` +
          `pick=${cell.pick_rate} ban=${cell.ban_rate} win_rate=${cell.win_rate}`,
      );
    }
  }
  report.record('no n=0 cell carries a tier or a rate', cells.length, orphans);
}

/** Every artifact of a partition has to say which partition it describes. */
function checkEnvelope(report, manifest, partition, artifact, name) {
  const failures = [];
  const expect = { patch: partition.patch, region: partition.region, queue: partition.queue, bracket: partition.bracket };
  for (const [key, value] of Object.entries(expect)) {
    if (artifact[key] !== value) failures.push(`${name}: ${key}=${JSON.stringify(artifact[key])} != ${JSON.stringify(value)}`);
  }
  if (artifact.source !== manifest.source) {
    failures.push(`${name}: source=${JSON.stringify(artifact.source)} != manifest ${JSON.stringify(manifest.source)}`);
  }
  if (artifact.min_cell_n !== partition.min_cell_n) {
    failures.push(`${name}: min_cell_n=${artifact.min_cell_n} != partition ${partition.min_cell_n}`);
  }
  report.record('artifact envelope agrees with its partition', 1, failures);
}

/**
 * Reconciles one partition's tier list, champion artifacts and matchup matrices
 * against each other, and returns the per-(champion, role) view agreement count
 * that the report headlines.
 */
function reconcilePartition(report, root, manifest, partition, staticIds) {
  const patch = partition.patch;
  const where = `patch ${patch}`;
  const dir = join(root, segOf(partition));
  const minCellN = partition.min_cell_n ?? 0;
  const tierList = readJson(join(dir, 'tierlist.json'));
  checkEnvelope(report, manifest, partition, tierList, `${where} tierlist`);

  const cells = tierList.cells ?? [];
  checkNoUnsupportedNumbers(report, partition, cells);

  const byKey = new Map();
  const duplicates = [];
  const unresolved = [];
  for (const cell of cells) {
    const key = cellKey(cell.role, cell.champion_id);
    if (byKey.has(key)) duplicates.push(`${where}: two cells for ${cell.role} champion ${cell.champion_id}`);
    byKey.set(key, cell);
    if (!ROLES.includes(cell.role)) unresolved.push(`${where}: role ${JSON.stringify(cell.role)} is not a role`);
    if (!staticIds.has(cell.champion_id)) {
      unresolved.push(`${where}: champion ${cell.champion_id} is not in the static champion list`);
    }
    const shape = cellShapeFailure(cell, minCellN, `${where} ${cell.role} ${cell.champion_id}`);
    report.record('published cell clears the floor', 1, shape ? [shape] : []);
  }
  report.record('tierlist publishes one cell per (role, champion)', cells.length, duplicates);
  report.record('tierlist role and champion ids are resolvable', cells.length, unresolved);

  // The champion page reads its cell from the artifact first and falls back to
  // the tier list, so wherever both exist they have to be the same cell.
  const artifacts = new Map();
  const artifactIds = new Set();
  const misnamedArtifacts = [];
  for (const file of listJson(join(dir, 'champions'))) {
    const artifact = readJson(file);
    checkEnvelope(report, manifest, partition, artifact, `${where} champion ${artifact.champion_id}`);
    artifacts.set(artifact.champion_id, artifact);
    const fileId = artifactFileId(file);
    artifactIds.add(fileId);
    if (fileId !== artifact.champion_id) {
      misnamedArtifacts.push(
        `${where}: champions/${basename(file)} holds champion_id ${artifact.champion_id}, ` +
          `but the site reads champions/<champion_id>.json`,
      );
    }
  }
  report.record('champion artifact file name is its champion id', artifactIds.size, misnamedArtifacts);

  // Every champion the manifest lists has to exist as a file, and every file
  // has to be listed: the manifest is what the site indexes and the tier list
  // is what it publishes, so a gap either way is a champion whose page loses
  // its build rows (listed, no file) or a file nothing points at (file, not
  // listed). The tierlist comparison below cannot see either of those.
  const listedIds = [...(partition.champions ?? [])].sort((a, b) => a - b);
  const missingArtifacts = listedIds
    .filter((id) => !artifactIds.has(id))
    .map((id) => `${where}: manifest lists champion ${id}, no champions/${id}.json exists`);
  const orphanArtifacts = [...artifactIds]
    .filter((id) => !listedIds.includes(id))
    .sort((a, b) => a - b)
    .map((id) => `${where}: champions/${id}.json exists, the manifest does not list champion ${id}`);
  report.record(
    'every listed champion has an artifact and every artifact is listed',
    listedIds.length + artifactIds.size,
    [...missingArtifacts, ...orphanArtifacts],
  );

  let rolesChecked = 0;
  let rowsChecked = 0;
  const mismatch = [];
  const unpublishedRole = [];
  const unsupportedRow = [];
  // Cross-view disagreements deduplicated by the (role, champion) cell they
  // contradict, so one broken fact counts once no matter how often it shows.
  const disagreeing = new Set();
  for (const [championId, artifact] of artifacts) {
    for (const entry of artifact.roles ?? []) {
      rolesChecked += 1;
      const cell = byKey.get(cellKey(entry.role, championId));
      if (!cell) {
        disagreeing.add(`${entry.role}:${championId}`);
        unpublishedRole.push(`${where}: champion ${championId} artifact publishes role ${entry.role}, the tierlist has no cell for it`);
      } else if (!sameCell(cell, entry.stats)) {
        disagreeing.add(`${entry.role}:${championId}`);
        mismatch.push(
          `${where}: champion ${championId} ${entry.role}: champion page [${describeCell(entry.stats)}] ` +
            `!= tierlist [${describeCell(cell)}]`,
        );
      }
      const roleN = cell?.n ?? entry.stats?.n ?? 0;
      for (const [kind, rows] of [
        ['items', entry.items],
        ['runes', entry.runes],
        ['spells', entry.spells],
        ['skill_orders', entry.skill_orders],
      ]) {
        for (const row of rows ?? []) {
          rowsChecked += 1;
          const label = `${kind} ${row.order ?? row.label ?? ''}`.trim();
          // Build rows are sub-samples of their cell, not cells: min_cell_n
          // guards cells. The production demo tree publishes rows below it
          // (min n=66 at min_cell_n=100), so only the row's own shape and its
          // support by the cell are checked here.
          const shape = cellShapeFailure(row, 0, `${where} champion ${championId} ${entry.role} ${label}`);
          if (shape) unsupportedRow.push(shape);
          else if (row.n > roleN) {
            unsupportedRow.push(
              `${where}: champion ${championId} ${entry.role} ${label}: n=${row.n} exceeds the cell's n=${roleN}`,
            );
          }
        }
      }
    }
  }
  report.record('champion page cell equals the tierlist cell', rolesChecked, mismatch);
  report.record('champion page role is published by the tierlist', rolesChecked, unpublishedRole);
  report.record('champion build rows are supported by their cell', rowsChecked, unsupportedRow);

  // The matchup matrix: its axis is the board the site renders, and the site
  // renders a pair whenever both endpoints are on the axis.
  let matrixCells = 0;
  let axisNames = 0;
  const axisDisagreement = [];
  const pairDisagreement = [];
  const matrixShape = [];
  for (const file of listJson(join(dir, 'matchups'))) {
    const matrix = readJson(file);
    checkEnvelope(report, manifest, partition, matrix, `${where} matchups ${matrix.role}`);
    const role = matrix.role;
    const axis = matrix.champions ?? [];
    const onAxis = new Set(axis);
    axisNames += axis.length;
    const published = new Set(cells.filter((cell) => cell.role === role).map((cell) => cell.champion_id));

    for (const id of axis) {
      if (!published.has(id)) {
        disagreeing.add(`${role}:${id}`);
        axisDisagreement.push(`${where} ${role}: matrix names champion ${id}, the tierlist publishes no cell for it`);
      }
      if (!staticIds.has(id)) {
        axisDisagreement.push(`${where} ${role}: matrix names champion ${id}, which is not in the static champion list`);
      }
    }
    for (const id of published) {
      if (!onAxis.has(id)) {
        disagreeing.add(`${role}:${id}`);
        axisDisagreement.push(`${where} ${role}: tierlist publishes champion ${id}, the matrix axis omits it`);
      }
    }

    const seen = new Set();
    for (const cell of matrix.cells ?? []) {
      matrixCells += 1;
      const key = `${cell.champion_id}:${cell.opponent_id}`;
      const reversed = `${cell.opponent_id}:${cell.champion_id}`;
      if (seen.has(key)) matrixShape.push(`${where} ${role}: duplicate pair ${key}`);
      if (seen.has(reversed)) matrixShape.push(`${where} ${role}: pair ${key} is published in both directions`);
      seen.add(key);
      for (const endpoint of [cell.champion_id, cell.opponent_id]) {
        if (!published.has(endpoint)) {
          disagreeing.add(`${role}:${endpoint}`);
          pairDisagreement.push(
            `${where} ${role}: pair ${key} names champion ${endpoint}, which the tierlist does not publish`,
          );
        }
      }
      const shape = cellShapeFailure(cell, minCellN, `${where} ${role} pair ${key}`);
      if (shape) matrixShape.push(shape);
    }
  }
  report.record('matchup axis is the published champion set for the role', axisNames, axisDisagreement);
  report.record('matchup pairs only name published champions', matrixCells, pairDisagreement);
  report.record('matchup pairs clear the floor', matrixCells, matrixShape);

  // What the manifest claims about this partition.
  const publishedIds = [...new Set(cells.map((cell) => cell.champion_id))].sort((a, b) => a - b);
  const manifestFailures = [];
  if (partition.cells_published !== cells.length) {
    manifestFailures.push(`${where}: manifest cells_published=${partition.cells_published} != ${cells.length} published cells`);
  }
  const listed = [...(partition.champions ?? [])].sort((a, b) => a - b);
  if (listed.join(',') !== publishedIds.join(',')) {
    manifestFailures.push(`${where}: manifest champions list (${listed.length}) != the published champion set (${publishedIds.length})`);
  }
  if (partition.suppressed_cells !== undefined && !(partition.suppressed_cells >= 0)) {
    manifestFailures.push(`${where}: suppressed_cells=${partition.suppressed_cells}`);
  }
  report.record('manifest agrees with the artifacts it indexes', 1, manifestFailures);

  return {
    patch,
    cells: cells.length,
    tuplesWithDisagreement: disagreeing.size,
    disagreements: mismatch.length + unpublishedRole.length + axisDisagreement.length + pairDisagreement.length,
  };
}

/** Checks that belong to the tree rather than to one partition. */
function checkSite(report, root, manifest, partitions) {
  const failures = [];
  if (partitions.length === 0) failures.push('manifest lists no partitions, so there is nothing to reconcile');
  if (manifest.latest && partitions[0] && manifest.latest.patch !== partitions[0].patch) {
    failures.push(`manifest.latest is patch ${manifest.latest.patch} but partitions[0] is ${partitions[0].patch}`);
  }
  const latestPatches = partitions.filter((entry) => entry.patch === manifest.latest?.patch);
  if (manifest.latest && (latestPatches.length !== 1 || JSON.stringify(latestPatches[0]) !== JSON.stringify(manifest.latest))) {
    failures.push(
      `manifest.latest (patch ${manifest.latest.patch}) is not exactly one of partitions (${latestPatches.length} matching entries)`,
    );
  }
  report.record('manifest latest is the newest partition', 1, failures);

  const staticDirs = listDirs(join(root, 'static'));
  const championsFile = staticDirs.map((name) => join(root, 'static', name, 'champions.json')).find((file) => readJsonIfPresent(file));
  const staticChampions = championsFile ? readJson(championsFile) : null;
  if (!staticChampions) {
    report.record('static champion list exists', 1, ['no static/<version>/champions.json in the tree']);
    return { staticIds: new Set(), staticSlugs: new Map() };
  }
  const staticFailures = [];
  const staticIds = new Set();
  const staticSlugs = new Map();
  for (const champion of staticChampions.champions ?? []) {
    if (staticIds.has(champion.id)) staticFailures.push(`static list repeats champion id ${champion.id}`);
    staticIds.add(champion.id);
    staticSlugs.set(champion.id, champion.slug);
    if (!champion.slug) staticFailures.push(`static list gives champion ${champion.id} no slug, so it has no route`);
  }
  report.record('static champion list has one route per champion', (staticChampions.champions ?? []).length, staticFailures);

  const patchesFile = staticDirs.map((name) => join(root, 'static', name, 'patches.json')).find((file) => readJsonIfPresent(file));
  const patchList = patchesFile ? (readJson(patchesFile).patches ?? []) : [];
  const patchFailures = [];
  for (const partition of partitions) {
    if (!patchList.includes(partition.patch)) {
      patchFailures.push(
        `patch ${partition.patch} has an artifact tree but is not in the static patch list, so no page can reach it`,
      );
    }
  }
  report.record('every published partition is in the static patch list', partitions.length, patchFailures);
  return { staticIds, staticSlugs };
}

/** The champion artifact's slug is the route the site builds for it. */
function checkChampionSlugs(report, root, partitions, staticSlugs) {
  const failures = [];
  let checked = 0;
  for (const partition of partitions) {
    for (const file of listJson(join(root, segOf(partition), 'champions'))) {
      const artifact = readJson(file);
      checked += 1;
      const slug = staticSlugs.get(artifact.champion_id);
      if (slug === undefined) {
        failures.push(`patch ${partition.patch}: champion artifact ${artifact.champion_id} is not in the static champion list`);
      } else if (slug !== artifact.champion_slug) {
        failures.push(
          `patch ${partition.patch}: champion ${artifact.champion_id} artifact slug ${artifact.champion_slug} != static ${slug}`,
        );
      }
    }
  }
  report.record('champion artifact slug is the route the static list names', checked, failures);
}

/** The matchup files present are exactly the roles the manifest advertises. */
function checkMatchupRoles(report, root, partitions) {
  const failures = [];
  let checked = 0;
  for (const partition of partitions) {
    const advertised = new Set(partition.matchup_roles ?? []);
    const present = listJson(join(root, segOf(partition), 'matchups')).map((file) => readJson(file).role);
    checked += present.length + advertised.size;
    for (const role of advertised) {
      if (!present.includes(role)) failures.push(`patch ${partition.patch}: manifest advertises matchups ${role}, no file exists`);
    }
    for (const role of present) {
      if (!advertised.has(role)) failures.push(`patch ${partition.patch}: matchups ${role} exists, the manifest does not advertise it`);
    }
  }
  report.record('matchup files match the advertised matchup roles', checked, failures);
}

function printReport(report, root, manifest, summaries, limit) {
  const cellsChecked = summaries.reduce((sum, entry) => sum + entry.cells, 0);
  const disagreements = summaries.reduce((sum, entry) => sum + entry.disagreements, 0);
  const brokenFacts = summaries.reduce((sum, entry) => sum + entry.tuplesWithDisagreement, 0);

  process.stdout.write(`fixture consistency: ${root} (source: ${manifest.source})\n`);
  for (const entry of summaries) process.stdout.write(`  patch ${entry.patch}: ${entry.cells} published cells\n`);
  process.stdout.write(`  cells checked: ${cellsChecked}\n`);
  process.stdout.write(`  (patch, champion, role) cells contradicted by another view: ${brokenFacts}\n`);
  process.stdout.write(`  cross-view failures: ${disagreements}\n`);
  process.stdout.write(`  total failures: ${report.failureCount}\n\n`);
  for (const entry of report.checks.values()) {
    const status = entry.failures.length === 0 ? 'ok  ' : 'FAIL';
    process.stdout.write(`  ${status} ${entry.name} (${entry.checked} checked)\n`);
    for (const failure of entry.failures.slice(0, limit)) process.stdout.write(`         ${failure}\n`);
    if (entry.failures.length > limit) {
      process.stdout.write(`         ... and ${entry.failures.length - limit} more\n`);
    }
  }
  return { cellsChecked, disagreements, brokenFacts, total: report.failureCount };
}

/**
 * Nothing can be reconciled before it can be parsed, so a tree with unreadable
 * JSON fails here, naming the file, instead of falling out of a later read as an
 * uncaught SyntaxError with no report attached to it.
 */
function checkTreeIsJson(report, root) {
  const files = listJsonDeep(root);
  const failures = [];
  for (const file of files) {
    try {
      readJson(file);
    } catch (error) {
      failures.push(`${relative(root, file)}: ${error.message}`);
    }
  }
  report.record('every artifact in the tree is readable JSON', files.length, failures);
  return failures.length === 0;
}

function main() {
  const { root, limit, json } = parseArgs(process.argv.slice(2));
  const report = new Report(limit);
  const readable = checkTreeIsJson(report, root);

  let manifest;
  try {
    manifest = readJson(join(root, 'manifest.json'));
  } catch (error) {
    report.record('manifest.json is readable JSON', 1, [error.message]);
    const totals = printReport(report, root, { source: 'unknown' }, [], limit);
    process.exitCode = totals.total === 0 ? 0 : 1;
    return;
  }
  if (!readable) {
    const totals = printReport(report, root, manifest, [], limit);
    process.exitCode = totals.total === 0 ? 0 : 1;
    return;
  }

  const partitions = manifest.partitions ?? [];
  const { staticIds, staticSlugs } = checkSite(report, root, manifest, partitions);
  checkChampionSlugs(report, root, partitions, staticSlugs);
  checkMatchupRoles(report, root, partitions);
  const summaries = [];
  for (const partition of partitions) {
    summaries.push(reconcilePartition(report, root, manifest, partition, staticIds));
  }
  const totals = printReport(report, root, manifest, summaries, limit);
  if (json) process.stdout.write(`\n${JSON.stringify({ root, manifest: manifest.source, partitions: summaries, ...totals })}\n`);
  process.exitCode = totals.total === 0 ? 0 : 1;
}

main();
