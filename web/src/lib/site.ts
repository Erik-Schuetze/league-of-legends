import type { Manifest, Partition, StaticChampion, StaticPatches, Window } from '../types/agg';
import {
  loadCheckedInChampions,
  loadStaticChampions,
  loadStaticPatches,
  patchesFromManifest,
  resolveRoot,
  staticVersionOnDisk,
} from './artifacts';
import { windowLabel } from './format';

// The site's view of its own data: which of the three states it is in, what it
// is allowed to claim about the numbers, and the champion identity index used
// to turn ids into names and icons.
//
// The state is decided from the manifest and nothing else. There is no fourth
// "maybe" state: either there is no manifest, or the manifest declares the
// snapshot as demo data, or it declares it as crawled Riot match data. Anything
// else is treated and labelled as unverified, which is the only honest reading
// of a manifest that does not say where its numbers came from.

export type DataState = 'no-data' | 'demo' | 'live';

/** The `source` value a real aggregate build writes (Riot MATCH-V5). */
export const LIVE_SOURCE = 'riot-match-v5';
/** The `source` value the aggregate build's demo subcommand writes. */
export const DEMO_SOURCE = 'demo';

const DEFAULT_DDRAGON_VERSION = '16.18.1';

export interface SiteData {
  state: DataState;
  /** `manifest.source`, when the producer declares one. */
  source: string | null;
  /** False when the manifest is silent or unknown, so the numbers are unverified. */
  sourceRecognised: boolean;
  rootDir: string | null;
  rootLabel: string;
  /** The validated manifest, when a root provided one. The site's state comes from here and nowhere else. */
  manifest: Manifest | null;
  manifestGeneratedAt: string | null;
  partitions: Partition[];
  latest: Partition | null;
  /** Published patches, newest first. Empty when nothing is published. */
  patches: string[];
  ddragonVersion: string;
  champions: StaticChampion[];
  championById: Map<number, StaticChampion>;
  championBySlug: Map<string, StaticChampion>;
  minCellN: number | null;
  suppressedCells: number | null;
  cellsPublished: number | null;
  sourceWindow: Window | null;
}

export interface StateBanner {
  state: DataState;
  heading: string;
  body: string;
  href: string;
  /** The exact wording is asserted by the verification script, so it lives here once. */
  previewText: string | null;
}

export const PREVIEW_TEXT = 'PREVIEW - illustrative data generated to exercise the layout, not real match statistics';
export const NO_DATA_HEADING = 'No sample yet';
export const UNVERIFIED_PREVIEW_TEXT =
  'PREVIEW - the manifest does not declare where this snapshot came from, so its numbers are unverified';

function mergeChampions(
  checkedIn: StaticChampion[],
  artifact: StaticChampion[] | null,
): StaticChampion[] {
  const byId = new Map<number, StaticChampion>();
  for (const champion of checkedIn) byId.set(champion.id, champion);

  // The artifact is authoritative where it is complete, because it is what the
  // aggregate build intended this snapshot to be read against. The checked-in
  // projection fills any gap, so a champion that predates a Data Dragon release
  // still has a name instead of an id.
  for (const champion of artifact ?? []) {
    const existing = byId.get(champion.id);
    if (!existing) {
      byId.set(champion.id, champion);
      continue;
    }
    byId.set(champion.id, {
      id: champion.id,
      key: champion.key || existing.key,
      slug: champion.slug || existing.slug,
      name: champion.name || existing.name,
      icon: champion.icon || existing.icon,
      roles: champion.roles.length > 0 ? champion.roles : existing.roles,
    });
  }
  return [...byId.values()].sort((a, b) => a.id - b.id);
}

let cached: SiteData | undefined;

export function siteData(): SiteData {
  if (cached) return cached;

  const root = resolveRoot();
  const checkedIn = loadCheckedInChampions();

  const ddragonVersion = staticVersionOnDisk() ?? checkedIn.ddragon_version ?? DEFAULT_DDRAGON_VERSION;
  const artifactStatic = loadStaticChampions(ddragonVersion)?.champions ?? null;
  const champions = mergeChampions(checkedIn.champions, artifactStatic);

  const championById = new Map<number, StaticChampion>();
  const championBySlug = new Map<string, StaticChampion>();
  for (const champion of champions) {
    championById.set(champion.id, champion);
    championBySlug.set(champion.slug, champion);
  }

  const manifest = root?.manifest ?? null;
  const source = root?.source ?? null;
  const sourceRecognised = source === DEMO_SOURCE || source === LIVE_SOURCE;
  const state: DataState = !manifest ? 'no-data' : source === LIVE_SOURCE ? 'live' : 'demo';

  const ordered = manifest ? patchesFromManifest(manifest) : [];

  cached = {
    state,
    source,
    sourceRecognised,
    rootDir: root?.dir ?? null,
    rootLabel: root?.label ?? 'no aggregate root reachable',
    manifest,
    manifestGeneratedAt: manifest?.generated_at ?? null,
    partitions: manifest?.partitions ?? [],
    latest: manifest?.latest ?? null,
    patches: ordered.map((partition) => partition.patch),
    ddragonVersion,
    champions,
    championById,
    championBySlug,
    minCellN: manifest?.latest.min_cell_n ?? null,
    suppressedCells: manifest?.latest.suppressed_cells ?? null,
    cellsPublished: manifest?.latest.cells_published ?? null,
    sourceWindow: manifest?.latest.source_window ?? null,
  };
  return cached;
}

/** The published patches artifact, when the tree has one. */
export function staticPatches(): StaticPatches | null {
  return loadStaticPatches(siteData().ddragonVersion);
}

/**
 * The one place the three states turn into words. Rendered on every page by
 * BaseLayout, so a preview snapshot cannot be mistaken for real statistics on
 * any route, and the deployed no-data state reads as deliberate rather than
 * broken.
 */
export function stateBanner(site: SiteData): StateBanner {
  switch (site.state) {
    case 'no-data':
      return {
        state: 'no-data',
        heading: NO_DATA_HEADING,
        body:
          'No aggregate snapshot has been published for this site yet, so there are no match statistics to show. ' +
          'Every page renders its real layout with an explicit empty state rather than placeholder numbers. ' +
          'What will be published, and how it is computed, is described on the provenance page.',
        href: '/about',
        previewText: null,
      };
    case 'demo':
      return {
        state: 'demo',
        heading: site.sourceRecognised ? 'Preview build' : 'Preview build - snapshot source not declared',
        body: site.sourceRecognised ? PREVIEW_TEXT : UNVERIFIED_PREVIEW_TEXT,
        href: '/about',
        previewText: site.sourceRecognised ? PREVIEW_TEXT : UNVERIFIED_PREVIEW_TEXT,
      };
    case 'live':
      return {
        state: 'live',
        heading: 'Published snapshot',
        body: '',
        href: '/about',
        previewText: null,
      };
  }
}

/** The provenance sentence shown in the page body and in structured data. */
export function provenanceLine(site: SiteData): string {
  if (!site.latest) return 'Nothing published yet: no aggregate snapshot exists.';
  const window = site.latest.source_window;
  return (
    `Patch ${site.latest.patch}, ${site.latest.region}, queue ${site.latest.queue}, bracket ${site.latest.bracket}. ` +
    `Source window ${windowLabel(window.from, window.to)}, generated ${site.latest.generated_at}.`
  );
}

/** Champion display name for an id, falling back to the id so nothing renders blank. */
export function championName(site: SiteData, championId: number): string {
  return site.championById.get(championId)?.name ?? `Champion ${championId}`;
}

export function championSlug(site: SiteData, championId: number): string | null {
  return site.championById.get(championId)?.slug ?? null;
}

/**
 * An icon URL. Data Dragon icons are referenced from Riot's CDN and never
 * vendored into the repository, so a relative path in an artifact is resolved
 * against the CDN for the version in use.
 */
export function iconUrl(icon: string | undefined, ddragonVersion: string): string | null {
  if (!icon) return null;
  if (/^https?:\/\//i.test(icon)) return icon;
  const path = icon.replace(/^\/+/, '');
  return `https://ddragon.leagueoflegends.com/cdn/${ddragonVersion}/${path.startsWith('img/') ? path : `img/champion/${path}`}`;
}
