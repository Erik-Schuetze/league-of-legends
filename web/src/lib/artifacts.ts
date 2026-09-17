import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';

import type {
  Cell,
  Champion,
  Manifest,
  MatchupCell,
  Matchups,
  Partition,
  Role,
  StaticChampions,
  StaticItems,
  StaticPatches,
  StaticRunes,
  StaticSummonerSpells,
  TierList,
} from '../types/agg';
import {
  MANIFEST_REL,
  championRel,
  matchupsRel,
  staticChampionsRel,
  staticItemsRel,
  staticPatchesRel,
  staticRunesRel,
  staticSummonerSpellsRel,
  tierListRel,
  type Seg,
} from './paths';
import { assertArtifact } from './schema-check';
import { WEB_ROOT } from './web-root';

// Everything the site knows about the aggregate tree, read from disk at build
// time. There is no runtime fetch anywhere in the site: a page renders what
// this module read, and the built HTML is the whole product.

/** aggmodel.SchemaVersion. A reader that does not understand an artifact's schema must not read it. */
export const SCHEMA_VERSION = 1;

/** `../agg` relative to web/, which is the plan's default (`$LOLSTATS_AGG_ROOT`). */
export const DEFAULT_AGG_ROOT = resolve(WEB_ROOT, '..', 'agg');

/** The checked-in demo tree used when no aggregate root is configured or reachable. */
export const FIXTURE_ROOT = resolve(WEB_ROOT, 'src', 'fixtures');

/** The checked-in Data Dragon projection, the last resort for champion identity. */
export const STATIC_CHAMPION_DATA = resolve(WEB_ROOT, 'src', 'data', 'champions.json');

export type FixturesMode = 'auto' | 'off' | 'only';

export interface RootCandidate {
  dir: string;
  /** How this root is described on /about and in the build log. */
  label: string;
  /** True when the operator configured it explicitly rather than by default. */
  explicit: boolean;
}

function envFlag(name: string): string | undefined {
  const raw = process.env[name];
  return raw && raw.trim() !== '' ? raw.trim() : undefined;
}

export function fixturesMode(): FixturesMode {
  const value = envFlag('LOLSTATS_AGG_FIXTURES')?.toLowerCase();
  if (value === 'off' || value === 'only') return value;
  return 'auto';
}

/**
 * The roots to try, in order.
 *
 * An explicitly configured `LOLSTATS_AGG_ROOT` is honoured as-is: the operator
 * who points the build at the deployed aggregate mount gets that mount, and an
 * empty mount means "no data yet" rather than a silent substitution of demo
 * fixtures. Only the default root falls back to the checked-in fixtures, which
 * is what makes a fresh clone build something to look at.
 */
export function candidateRoots(): RootCandidate[] {
  const explicit = envFlag('LOLSTATS_AGG_ROOT');
  const primary: RootCandidate = {
    dir: explicit ? resolve(explicit) : DEFAULT_AGG_ROOT,
    label: explicit ? 'lolstats agg root (LOLSTATS_AGG_ROOT)' : 'default agg root (../agg)',
    explicit: Boolean(explicit),
  };
  const fixtures: RootCandidate = { dir: FIXTURE_ROOT, label: 'checked-in demo fixtures', explicit: false };

  switch (fixturesMode()) {
    case 'only':
      return [fixtures];
    case 'off':
      return [primary];
    default:
      return explicit ? [primary] : [primary, fixtures];
  }
}

export interface ResolvedRoot {
  dir: string;
  label: string;
  explicit: boolean;
  manifest: Manifest;
  /**
   * The manifest's `source` field, when the producer declares one. It is read
   * defensively rather than from the generated type because declaring where a
   * snapshot came from is additive: an older manifest simply does not say, and
   * "does not say" must render as unverified rather than as real match data.
   */
  source: string | null;
}

function readJson(absolute: string): unknown | undefined {
  if (!existsSync(absolute)) return undefined;
  const text = readFileSync(absolute, 'utf8');
  try {
    return JSON.parse(text);
  } catch (error) {
    throw new Error(`${absolute} is not valid JSON: ${(error as Error).message}`);
  }
}

function readSource(manifest: unknown): string | null {
  if (!manifest || typeof manifest !== 'object') return null;
  const value = (manifest as Record<string, unknown>).source;
  return typeof value === 'string' && value.trim() !== '' ? value : null;
}

let resolved: ResolvedRoot | null | undefined;

/** The first candidate root that has a manifest, or null when none does. */
export function resolveRoot(): ResolvedRoot | null {
  if (resolved !== undefined) return resolved;

  for (const candidate of candidateRoots()) {
    const manifestFile = join(candidate.dir, MANIFEST_REL);
    if (!existsSync(manifestFile)) continue;

    const manifest = assertArtifact<Manifest>('Manifest', readJson(manifestFile), manifestFile);
    if (manifest.schema !== SCHEMA_VERSION) {
      throw new Error(
        `${manifestFile} declares agg schema ${manifest.schema}, this site build understands ${SCHEMA_VERSION}`,
      );
    }
    resolved = { ...candidate, manifest, source: readSource(readJson(manifestFile)) };
    return resolved;
  }

  resolved = null;
  return resolved;
}

/** True when any candidate root exists on disk at all, for the /about provenance note. */
export function aggregateRootsPresent(): string[] {
  return candidateRoots()
    .filter((candidate) => existsSync(candidate.dir))
    .map((candidate) => candidate.dir);
}

function readArtifact<T>(definition: string, relative: string): T | null {
  const root = resolveRoot();
  if (!root) return null;
  const absolute = join(root.dir, relative);
  const raw = readJson(absolute);
  if (raw === undefined) return null;

  const artifact = assertArtifact<T & { schema?: number }>(definition, raw, absolute);

  // Envelope artifacts declare the schema they were written with; the static
  // Data Dragon projection does not. A declared schema the site does not
  // understand is a hard failure, because silently reading a v2 artifact with a
  // v1 reader is exactly the "renders blanks" failure this check exists to stop.
  if (typeof artifact.schema === 'number' && artifact.schema !== SCHEMA_VERSION) {
    throw new Error(`${absolute} declares agg schema ${artifact.schema}, this site build understands ${SCHEMA_VERSION}`);
  }
  return artifact;
}

export function loadTierList(seg: Seg): TierList | null {
  return readArtifact<TierList>('TierList', tierListRel(seg));
}

export function loadChampion(seg: Seg, championId: number): Champion | null {
  return readArtifact<Champion>('Champion', championRel(seg, championId));
}

export function loadMatchups(seg: Seg, role: Role): Matchups | null {
  return readArtifact<Matchups>('Matchups', matchupsRel(seg, role));
}

export function loadStaticChampions(ddragonVersion: string): StaticChampions | null {
  return readArtifact<StaticChampions>('StaticChampions', staticChampionsRel(ddragonVersion));
}

export function loadStaticPatches(ddragonVersion: string): StaticPatches | null {
  return readArtifact<StaticPatches>('StaticPatches', staticPatchesRel(ddragonVersion));
}

export function loadStaticItems(ddragonVersion: string): StaticItems | null {
  return readArtifact<StaticItems>('StaticItems', staticItemsRel(ddragonVersion));
}

export function loadStaticRunes(ddragonVersion: string): StaticRunes | null {
  return readArtifact<StaticRunes>('StaticRunes', staticRunesRel(ddragonVersion));
}

export function loadStaticSummonerSpells(ddragonVersion: string): StaticSummonerSpells | null {
  return readArtifact<StaticSummonerSpells>('StaticSummonerSpells', staticSummonerSpellsRel(ddragonVersion));
}

/**
 * The Data Dragon version the aggregate tree published static data for, if any.
 *
 * The tree is synced by version directory, so there can be more than one during
 * a release overlap. Highest version wins, compared numerically - a string
 * comparison would rank 16.9.1 above 16.18.1.
 */
export function staticVersionOnDisk(): string | null {
  const root = resolveRoot();
  if (!root) return null;
  const dir = join(root.dir, 'v1', 'static');
  if (!existsSync(dir) || !statSync(dir).isDirectory()) return null;

  const versions = readdirSync(dir).filter((entry) => statSync(join(dir, entry)).isDirectory());
  if (versions.length === 0) return null;
  return versions.sort(compareVersions)[versions.length - 1];
}

function compareVersions(a: string, b: string): number {
  const left = a.split('.').map((part) => Number.parseInt(part, 10) || 0);
  const right = b.split('.').map((part) => Number.parseInt(part, 10) || 0);
  for (let i = 0; i < Math.max(left.length, right.length); i += 1) {
    const diff = (left[i] ?? 0) - (right[i] ?? 0);
    if (diff !== 0) return diff;
  }
  return a.localeCompare(b);
}

/** The checked-in Data Dragon projection, which the build works from offline. */
export function loadCheckedInChampions(): StaticChampions {
  const raw = readJson(STATIC_CHAMPION_DATA);
  if (raw === undefined) {
    throw new Error(
      `${STATIC_CHAMPION_DATA} is missing. Run "npm run data:champions" to regenerate it from Data Dragon.`,
    );
  }
  const data = assertArtifact<StaticChampions>('StaticChampions', raw, STATIC_CHAMPION_DATA);
  return data;
}

// --------------------------------------------------------------------------
// Cell discipline: `n` travels with every rate, and a thin cell is withheld.
// --------------------------------------------------------------------------

export type CellAvailability = 'published' | 'no-sample' | 'withheld';

/**
 * Decides whether a cell's rate may be displayed.
 *
 * The contract (docs/contracts.md section 1) says every cell carries `n` and
 * that cells below `min_cell_n` are suppressed and counted rather than emitted.
 * A cell with n = 0 is legitimate in the artifact - the tier list includes
 * champions with no games in the window so a reader cannot confuse "absent"
 * with "silent" - but its win rate is not a statistic and must not be shown as
 * one. A cell that slipped through below the floor is shown as withheld rather
 * than as noise, because publishing a thin number is the failure this whole
 * design is arranged to avoid.
 */
export function cellAvailability(cell: { n: number }, minCellN: number): CellAvailability {
  if (!Number.isFinite(cell.n) || cell.n <= 0) return 'no-sample';
  if (minCellN > 0 && cell.n < minCellN) return 'withheld';
  return 'published';
}

/** Published cells only, in artifact order. */
export function publishableCells(
  cells: readonly Cell[],
  minCellN: number,
): Array<{ cell: Cell; availability: CellAvailability }> {
  return cells.map((cell) => ({ cell, availability: cellAvailability(cell, minCellN) }));
}

/** The sample size to show next to a rate, or null when the rate is withheld. */
export function publishedN(cell: { n: number }, minCellN: number): number | null {
  return cellAvailability(cell, minCellN) === 'published' ? cell.n : null;
}

/** Champion ids the tier list carries a cell for, in artifact order, deduplicated. */
export function championIdsIn(tierList: TierList): number[] {
  const seen = new Set<number>();
  const out: number[] = [];
  for (const cell of tierList.cells) {
    if (!seen.has(cell.champion_id)) {
      seen.add(cell.champion_id);
      out.push(cell.champion_id);
    }
  }
  return out;
}

/** Cells for one role, in artifact order. */
export function cellsForRole(tierList: TierList, role: Role): Cell[] {
  return tierList.cells.filter((cell) => cell.role === role);
}

/** The mirror of a matchup cell: the artifact stores each pair once, lower id first. */
export function matchupResult(cell: MatchupCell, championId: number): { n: number; winRate: number } | null {
  if (cell.champion_id === championId) return { n: cell.n, winRate: cell.win_rate };
  if (cell.opponent_id === championId) return { n: cell.n, winRate: 1 - cell.win_rate };
  return null;
}

/** Partitions deduplicated by patch, newest first, for the patch switcher. */
export function patchesFromManifest(manifest: Manifest): Partition[] {
  const byPatch = new Map<string, Partition>();
  for (const partition of [manifest.latest, ...manifest.partitions]) {
    const existing = byPatch.get(partition.patch);
    if (!existing || existing.generated_at < partition.generated_at) byPatch.set(partition.patch, partition);
  }
  return [...byPatch.values()].sort((a, b) => (a.patch < b.patch ? 1 : a.patch > b.patch ? -1 : 0));
}
