import type { Window } from '../types/agg';
import { integer } from './format';
import type { SiteData } from './site';

// Structured data, built from the same values the page renders.
//
// The rules applied here are the rules applied everywhere else in this site:
// a structured-data statement is only emitted when the artifact supports it.
// With no published snapshot there is no Dataset to describe, so the page
// claims a WebPage and nothing more.

export interface JsonLd {
  '@context': 'https://schema.org';
  '@type': string;
  [key: string]: unknown;
}

const SITE_NAME = 'LoL Stats';

/**
 * An absolute URL for a page, from `astro.config.mjs`'s `site`. Pages and
 * structured data must agree about what a page's own URL is, so both go through
 * here rather than each formatting it again.
 */
export function absoluteUrl(path: string, site: URL | undefined, current: URL): string {
  return new URL(path, site ?? current).href;
}

export function jsonLdWebsite(site: SiteData, url: string, description: string): JsonLd {
  return {
    '@context': 'https://schema.org',
    '@type': 'WebSite',
    name: SITE_NAME,
    url,
    description,
    inLanguage: 'en',
    publisher: { '@type': 'Organization', name: SITE_NAME },
    ...(site.state === 'live' ? {} : { isAccessibleForFree: true }),
  };
}

export function jsonLdWebPage(url: string, name: string, description: string): JsonLd {
  return {
    '@context': 'https://schema.org',
    '@type': 'WebPage',
    url,
    name,
    description,
    inLanguage: 'en',
    isPartOf: { '@type': 'WebSite', name: SITE_NAME, url: '/' },
  };
}

export interface DatasetInput {
  url: string;
  name: string;
  description: string;
  patch: string;
  sourceWindow: Window;
  generatedAt: string;
  minCellN: number;
  sampleSize: number;
  /** What each row measures, in the reader's terms. */
  variables: Array<{ name: string; unit: string }>;
}

export function jsonLdDataset(input: DatasetInput): JsonLd {
  return {
    '@context': 'https://schema.org',
    '@type': 'Dataset',
    name: input.name,
    description: input.description,
    url: input.url,
    inLanguage: 'en',
    isAccessibleForFree: true,
    dateModified: input.generatedAt,
    temporalCoverage: `${input.sourceWindow.from.slice(0, 10)}/${input.sourceWindow.to.slice(0, 10)}`,
    creator: { '@type': 'Organization', name: SITE_NAME },
    variableMeasured: input.variables.map((variable) => ({
      '@type': 'PropertyValue',
      name: variable.name,
      unitText: variable.unit,
    })),
    measurementTechnique:
      `Aggregated from Riot MATCH-V5 match records for patch ${input.patch}; ` +
      `rates are published only for cells holding at least n = ${integer(input.minCellN)} games.`,
    size: `${integer(input.sampleSize)} games in the published snapshot`,
    citation: 'League of Legends and Riot Games are trademarks or registered trademarks of Riot Games, Inc.',
  };
}

/** Serialised for a <script type="application/ld+json"> tag. */
export function jsonLdScript(value: JsonLd | JsonLd[]): string {
  return JSON.stringify(value).replace(/</g, '\\u003c');
}
