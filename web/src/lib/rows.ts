import type { Build, Cell, ChampionRole, MatchupCell, Matchups, StaticChampion, TierList } from '../types/agg';
import { cellAvailability, matchupResult, type CellAvailability } from './artifacts';
import { integer, plusMinus } from './format';
import { roleFromSlug, roleLabel } from './roles';
import type { SiteData } from './site';

// Rows and columns for the frozen DataTable API (docs/contracts.md section 3).
//
// The table is a presentation component: it sorts and formats, it does not
// decide what is publishable. That decision is made here, once, so a withheld
// rate cannot be rendered as a number by one page and as a gap by another.
//
// A withheld value is the string 'withheld' rather than null, because the
// DataTable is allowed to render null as an ordinary missing value and this is
// not an ordinary missing value: it is a rate that exists and that this site
// chooses not to publish. The count `n` is still shown, since it is a count
// rather than an estimate.

export type CellValue = string | number | null;

export type Row = Record<string, CellValue>;

export interface Column {
  key: string;
  label: string;
  align?: 'start' | 'end';
  sortable?: boolean;
  format?: 'text' | 'percent' | 'integer' | 'decimal';
  digits?: number;
}

export const WITHHELD = 'withheld';
export const NO_SAMPLE = 'no sample';

/** What a rate column shows for a cell that may not be published. */
export function rateValue(cell: { n: number; win_rate: number }, minCellN: number, pick: 'win_rate' | 'pick_rate' | 'ban_rate' | 'ci95_half_width') {
  const availability = cellAvailability(cell, minCellN);
  if (availability === 'no-sample') return NO_SAMPLE;
  if (availability === 'withheld') return WITHHELD;
  const value = (cell as unknown as Record<string, number>)[pick];
  if (pick === 'ci95_half_width') return plusMinus(value);
  return value;
}

/**
 * A tier is a function of the win rate and the pick rate, so a cell that was
 * never measured has no tier to show. Suppressing it here keeps an artifact
 * that carries a stale tier next to n = 0 from rendering "Tier A" for a
 * champion with no games.
 */
export function tierValue(cell: { n: number; tier?: string }): CellValue {
  if (!Number.isFinite(cell.n) || cell.n <= 0) return NO_SAMPLE;
  return cell.tier ?? null;
}

export function columnAvailabilityNote(minCellN: number, suppressedCells: number | null): string {
  const suppressed =
    suppressedCells && suppressedCells > 0
      ? ` ${integer(suppressedCells)} further cells were withheld by the aggregator for being below the threshold.`
      : '';
  return `A rate is published only when its cell has at least n = ${integer(minCellN)} games; thinner cells read "${WITHHELD}" and show their count only.${suppressed}`;
}

export function tierListColumns(includeRole: boolean): Column[] {
  const columns: Column[] = [
    { key: 'champion', label: 'Champion', sortable: true },
    { key: 'tier', label: 'Tier', sortable: true },
    { key: 'n', label: 'Games (n)', align: 'end', sortable: true, format: 'integer' },
    { key: 'win_rate', label: 'Win rate', align: 'end', sortable: true, format: 'percent' },
    { key: 'pick_rate', label: 'Pick rate', align: 'end', sortable: true, format: 'percent' },
    { key: 'ban_rate', label: 'Ban rate', align: 'end', sortable: true, format: 'percent' },
    { key: 'ci95_half_width', label: '95% interval', align: 'end', sortable: true, format: 'text' },
  ];
  return includeRole ? [columns[0], { key: 'role', label: 'Role', sortable: true }, ...columns.slice(1)] : columns;
}

export function tierListRows(cells: readonly Cell[], site: SiteData, minCellN: number, includeRole: boolean): Row[] {
  const rows: Row[] = [];
  for (const cell of cells) {
    const row: Row = {
      champion: site.championById.get(cell.champion_id)?.name ?? `Champion ${cell.champion_id}`,
      tier: tierValue(cell),
      n: cell.n,
      win_rate: rateValue(cell, minCellN, 'win_rate'),
      pick_rate: rateValue(cell, minCellN, 'pick_rate'),
      ban_rate: rateValue(cell, minCellN, 'ban_rate'),
      ci95_half_width: rateValue(cell, minCellN, 'ci95_half_width'),
    };
    if (includeRole) row.role = roleLabel(cell.role);
    rows.push(row);
  }
  return rows;
}

/** Rows the champion page shows: every role the champion was played in this snapshot. */
export function championRoleRows(roles: readonly ChampionRole[], site: SiteData, minCellN: number): Row[] {
  return roles.map((entry) => ({
    role: roleLabel(entry.role),
    tier: tierValue(entry.stats),
    n: entry.stats.n,
    win_rate: rateValue(entry.stats, minCellN, 'win_rate'),
    pick_rate: rateValue(entry.stats, minCellN, 'pick_rate'),
    ban_rate: rateValue(entry.stats, minCellN, 'ban_rate'),
    ci95_half_width: rateValue(entry.stats, minCellN, 'ci95_half_width'),
  }));
}

export const championRoleColumns: Column[] = [
  { key: 'role', label: 'Role', sortable: true },
  { key: 'tier', label: 'Tier', sortable: true },
  { key: 'n', label: 'Games (n)', align: 'end', sortable: true, format: 'integer' },
  { key: 'win_rate', label: 'Win rate', align: 'end', sortable: true, format: 'percent' },
  { key: 'pick_rate', label: 'Pick rate', align: 'end', sortable: true, format: 'percent' },
  { key: 'ban_rate', label: 'Ban rate', align: 'end', sortable: true, format: 'percent' },
  { key: 'ci95_half_width', label: '95% interval', align: 'end', sortable: true, format: 'text' },
];

/** One champion's matchups, as seen by that champion: the artifact stores each pair once. */
export function championMatchupRows(
  matchups: Matchups,
  championId: number,
  site: SiteData,
  minCellN: number,
): Row[] {
  const rows: Row[] = [];
  for (const cell of matchups.cells) {
    const result = matchupResult(cell, championId);
    if (!result) continue;
    const availability = cellAvailability({ n: result.n }, minCellN);
    rows.push({
      opponent: site.championById.get(otherId(cell, championId))?.name ?? `Champion ${otherId(cell, championId)}`,
      n: result.n,
      win_rate: availability === 'published' ? result.winRate : availability === 'withheld' ? WITHHELD : NO_SAMPLE,
      ci95_half_width: availability === 'published' ? plusMinus(cell.ci95_half_width) : WITHHELD,
    });
  }
  return rows;
}

function otherId(cell: MatchupCell, championId: number): number {
  return cell.champion_id === championId ? cell.opponent_id : cell.champion_id;
}

export const matchupColumns: Column[] = [
  { key: 'opponent', label: 'Opponent', sortable: true },
  { key: 'n', label: 'Games (n)', align: 'end', sortable: true, format: 'integer' },
  { key: 'win_rate', label: 'Win rate', align: 'end', sortable: true, format: 'percent' },
  { key: 'ci95_half_width', label: '95% interval', align: 'end', sortable: true, format: 'text' },
];

export interface MatrixCell {
  n: number;
  winRate: number;
  availability: CellAvailability;
}

export interface MatchupMatrix {
  champions: StaticChampion[];
  get: (rowChampionId: number, columnChampionId: number) => MatrixCell | null;
}

/**
 * The role's matchup artifact as a matrix, champion against champion.
 *
 * aggmodel emits each ordered pair once, lower champion id first (see
 * MatchupCell in internal/aggmodel/model.go), and the reader mirrors it. So a
 * lookup either finds the stored direction or finds the stored opposite
 * direction, whose complement is arithmetic rather than an estimate. Keys are
 * directional, so the two cases cannot be confused.
 */
export function matchupMatrix(matchups: Matchups, site: SiteData, minCellN: number): MatchupMatrix {
  const champions: StaticChampion[] = [];
  for (const id of matchups.champions) {
    const champion = site.championById.get(id);
    if (champion) champions.push(champion);
    else champions.push({ id, key: String(id), slug: String(id), name: `Champion ${id}`, icon: '', roles: [] });
  }

  const index = new Map<string, MatchupCell>();
  for (const cell of matchups.cells) index.set(pairKey(cell.champion_id, cell.opponent_id), cell);

  return {
    champions,
    get(rowChampionId: number, columnChampionId: number): MatrixCell | null {
      if (rowChampionId === columnChampionId) return null;
      const direct = index.get(pairKey(rowChampionId, columnChampionId));
      if (direct) {
        return {
          n: direct.n,
          winRate: direct.win_rate,
          availability: cellAvailability(direct, minCellN),
        };
      }
      const transpose = index.get(pairKey(columnChampionId, rowChampionId));
      if (transpose) {
        return {
          n: transpose.n,
          winRate: 1 - transpose.win_rate,
          availability: cellAvailability(transpose, minCellN),
        };
      }
      return null;
    },
  };
}

/** Directed key: `champion_id:opponent_id` in the emitted order, never sorted. */
function pairKey(championId: number, opponentId: number): string {
  return `${championId}:${opponentId}`;
}

/** Builds in the order the aggregator published them: already sorted by n descending. */
export function buildRows(builds: readonly Build[], limit: number, minCellN: number): Row[] {
  return builds.slice(0, limit).map((build) => ({
    build: build.label,
    n: build.n,
    win_rate: rateValue(build, minCellN, 'win_rate'),
  }));
}

export const buildColumns: Column[] = [
  { key: 'build', label: 'Build', sortable: false },
  { key: 'n', label: 'Games (n)', align: 'end', sortable: true, format: 'integer' },
  { key: 'win_rate', label: 'Win rate', align: 'end', sortable: true, format: 'percent' },
];

/** A link to every champion page for a role, so a reader has a route into the detail pages. */
export function championLinks(champions: readonly StaticChampion[], role: string): Array<{ name: string; href: string }> {
  return champions.map((champion) => ({
    name: champion.name,
    href: `/champions/${champion.slug}/${role}`,
  }));
}

/** The label the artifact-free pages use for a role, e.g. "mid" becomes "Mid". */
export function roleCrumb(slug: string): string {
  const role = roleFromSlug(slug);
  return role ? roleLabel(role) : slug;
}
