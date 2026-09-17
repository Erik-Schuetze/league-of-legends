import type { Champion, Matchups, Partition, Role, StaticChampion, TierList } from '../types/agg';
import { loadChampion, loadMatchups, loadTierList, patchesFromManifest } from './artifacts';
import { segOf, type Seg } from './paths';
import { ROLES } from './roles';
import { siteData, type SiteData } from './site';

// What one page needs from the aggregate tree.
//
// Every page starts here, so every page agrees about which snapshot it is
// rendering, which patch it is about, and what it shows when there is no
// snapshot at all. The last case is the deployed cluster's state for the first
// months of the project, so it is a first-class path rather than an error.

export interface Snapshot {
  site: SiteData;
  /** Published patches, newest first. Empty when no snapshot exists. */
  patches: string[];
  /** The patch this page is about, or 'not published' when there is nothing. */
  patch: string;
  partition: Partition | null;
  seg: Seg | null;
  generatedAt: string | undefined;
}

export function snapshot(patch?: string): Snapshot {
  const site = siteData();
  const partitions = site.manifest ? patchesFromManifest(site.manifest) : [];
  const partition = patch
    ? partitions.find((entry) => entry.patch === patch) ?? null
    : site.latest;

  return {
    site,
    patches: partitions.map((entry) => entry.patch),
    patch: partition?.patch ?? 'not published',
    partition,
    seg: partition ? segOf(partition) : null,
    generatedAt: partition?.generated_at,
  };
}

export function tierListFor(snap: Snapshot): TierList | null {
  return snap.seg ? loadTierList(snap.seg) : null;
}

export function championFor(snap: Snapshot, championId: number): Champion | null {
  return snap.seg ? loadChampion(snap.seg, championId) : null;
}

export function matchupsFor(snap: Snapshot, role: TierList['cells'][number]['role']): Matchups | null {
  return snap.seg ? loadMatchups(snap.seg, role) : null;
}

/** True when this page can show more than an empty state. */
export function hasData(snap: Snapshot): boolean {
  return snap.partition !== null;
}

/**
 * Why a page is empty, in the reader's terms rather than the build's. The
 * distinction matters: "no snapshot has been published" and "this snapshot has
 * nothing for this selection" are different facts about the project.
 */
export function emptyReason(snap: Snapshot): string {
  if (!snap.partition) {
    return (
      'No aggregate snapshot has been published yet, so there are no match statistics to read. ' +
      'This page reads the published artifacts at build time and will fill in as soon as one exists.'
    );
  }
  return (
    `The published snapshot for patch ${snap.partition.patch} (${snap.partition.region}, queue ${snap.partition.queue}, ` +
    `bracket ${snap.partition.bracket}) has no artifact for this selection, so nothing is shown rather than an estimate.`
  );
}

/** The canonical patch label used in titles and the footer. */
export function patchLabel(snap: Snapshot): string {
  return snap.patch;
}

/**
 * True while the statistics routes have nothing to say, which is the state the
 * deployed cluster starts in. Those routes then ask search engines not to index
 * an empty table, while the pages that describe the product stay indexable.
 */
export function statisticsNoindex(snap: Snapshot): boolean {
  return snap.partition === null;
}

/** One champion in one role, as a route of its own. */
export interface ChampionRolePage {
  champion: StaticChampion;
  role: Role;
}

/**
 * The champion role pages a build publishes: every published role, plus the
 * roles a champion actually has a cell or a detail artifact for.
 *
 * Taking the union rather than the data alone keeps the address space stable:
 * a reader who bookmarks /champions/annie/top keeps a page when a snapshot
 * arrives that happens to have no Annie top cell, and it renders the empty
 * state instead of disappearing. With no snapshot at all the union is all five
 * roles, so the whole tree exists in the deployed cluster's first state too.
 *
 * The sitemap and the page's getStaticPaths both call this, so the advertised
 * route set and the rendered route set are the same set.
 */
export function championRolePages(patch?: string): ChampionRolePage[] {
  const snap = snapshot(patch);
  const pages: ChampionRolePage[] = [];
  for (const champion of snap.site.champions) {
    // Every champion gets every role: the artifacts can only ever name roles
    // from ROLES, so ROLES is already the union of the two sets.
    for (const role of ROLES) pages.push({ champion, role });
  }
  return pages;
}
