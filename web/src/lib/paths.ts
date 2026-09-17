import type { Bracket, Role } from '../types/agg';
import { roleSlug } from './roles';

// Path templates for the agg/v1 tree, mirroring internal/aggmodel/paths.go.
//
// They are restated here rather than fetched from the Go package because the
// site build must run with no Go toolchain. The two copies are kept honest by
// docs/contracts.md section 4, which freezes the layout, and by the fact that a
// wrong path here fails loudly (no manifest, no pages) rather than subtly.

export const URL_PREFIX = '/agg';
export const VERSION_DIR = 'v1';

/** The manifest is the tree's entry point and the only file the site needs to decide its state. */
export const MANIFEST_REL = `${VERSION_DIR}/manifest.json`;

/** One published partition: the four path elements between `p/` and an artifact. */
export interface Seg {
  patch: string;
  region: string;
  queue: number;
  bracket: Bracket;
}

export function segDir(seg: Seg): string {
  return `${VERSION_DIR}/p/${seg.patch}/${seg.region}/${seg.queue}/${seg.bracket}`;
}

export function tierListRel(seg: Seg): string {
  return `${segDir(seg)}/tierlist.json`;
}

export function championRel(seg: Seg, championId: number): string {
  return `${segDir(seg)}/champions/${championId}.json`;
}

export function matchupsRel(seg: Seg, role: Role): string {
  return `${segDir(seg)}/matchups/${roleSlug(role)}.json`;
}

export function staticDir(ddragonVersion: string): string {
  return `${VERSION_DIR}/static/${ddragonVersion}`;
}

export function staticChampionsRel(ddragonVersion: string): string {
  return `${staticDir(ddragonVersion)}/champions.json`;
}

export function staticItemsRel(ddragonVersion: string): string {
  return `${staticDir(ddragonVersion)}/items.json`;
}

export function staticRunesRel(ddragonVersion: string): string {
  return `${staticDir(ddragonVersion)}/runes.json`;
}

export function staticSummonerSpellsRel(ddragonVersion: string): string {
  return `${staticDir(ddragonVersion)}/summoner-spells.json`;
}

export function staticPatchesRel(ddragonVersion: string): string {
  return `${staticDir(ddragonVersion)}/patches.json`;
}

/**
 * The URL an artifact is served at. The tree is mounted at URL_PREFIX by the
 * static server, so a page that wanted to fetch an artifact would use this -
 * which v1 deliberately never does. It is kept because it is the canonical
 * spelling used by the provenance note on /about.
 */
export function artifactUrl(rel: string): string {
  return `${URL_PREFIX}/${rel}`;
}

/** Segments a partition the way the tree does. */
export function segOf(partition: {
  patch: string;
  region: string;
  queue: number;
  bracket: Bracket;
}): Seg {
  return {
    patch: partition.patch,
    region: partition.region,
    queue: partition.queue,
    bracket: partition.bracket,
  };
}
