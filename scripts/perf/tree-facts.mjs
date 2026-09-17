#!/usr/bin/env node
// Deterministic weight and structure facts over every served route, with no browser.
//
//   node scripts/perf/tree-facts.mjs --base http://127.0.0.1:18921 --out docs/evidence
//
// Routes come from the origin's sitemap.xml. Reports: per-page uncompressed and
// gzipped HTML bytes, mean and largest page, pages missing lang, images without
// alt, duplicate DOM ids, heading level skips, island and script counts.

import { createRequire } from 'node:module';
import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';

const argv = process.argv.slice(2);
const flag = (name, fallback) => {
  const i = argv.indexOf(name);
  return i === -1 ? fallback : argv[i + 1];
};
const TOOLS = process.env.PERF_TOOLS_DIR || '/tmp/a11y-tools';
const BASE = flag('--base', 'http://127.0.0.1:18921');
const OUT = flag('--out', 'docs/evidence');
const CONCURRENCY = Number(flag('--concurrency', '8'));

const cheerio = createRequire(path.join(TOOLS, 'package.json'))('cheerio');
const KB = 1024;

const sitemap = await (await fetch(`${BASE}/sitemap.xml`)).text();
const routes = [...sitemap.matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => new URL(m[1]).pathname);
// The origin serves the slash-suffixed form (that is what rel=canonical advertises),
// so measure that form even when the sitemap lists the path without a trailing slash.
const probe = (route) => (route === '/' || route.endsWith('/') ? route : `${route}/`);

mkdirSync(OUT, { recursive: true });
console.log(`sitemap: ${routes.length} routes`);

const facts = { base: BASE, routeCount: routes.length, pages: [], totals: {} };
let done = 0;

async function inspect(sitemapRoute) {
  const route = probe(sitemapRoute);
  const res = await fetch(BASE + route);
  if (!res.ok) return { route, sitemapRoute, status: res.status };
  const body = await res.text();
  if (!body.includes('data-state=')) return { route, sitemapRoute, status: `200 but not an HTML page` };
  const gzip = zlib.gzipSync(Buffer.from(body, 'utf8'), { level: 9 }).length;
  const $ = cheerio.load(body);

  const canonical = $('link[rel=canonical]').attr('href') ?? null;
  const robotsMeta = $('meta[name=robots]').attr('content') ?? null;

  const htmlLang = $('html').attr('lang') ?? null;
  const images = $('img').length;
  const imagesWithoutAlt = $('img').filter((_, el) => $(el).attr('alt') === undefined).length;
  const imagesEmptyAlt = $('img').filter((_, el) => $(el).attr('alt') === '').length;

  const ids = $('[id]').map((_, el) => $(el).attr('id')).get();
  const counts = new Map();
  for (const id of ids) counts.set(id, (counts.get(id) ?? 0) + 1);
  const duplicateIds = [...counts.entries()].filter(([, n]) => n > 1).map(([id, n]) => `${id}x${n}`);

  const levels = $('h1,h2,h3,h4,h5,h6').map((_, el) => Number(el.tagName[1])).get();
  const skips = [];
  for (let i = 1; i < levels.length; i++) {
    if (levels[i] - levels[i - 1] > 1) skips.push(`h${levels[i - 1]}->h${levels[i]}`);
  }

  const islands = $('[data-island]').length;
  const scripts = $('script[src]').length;
  const blockingScripts = $('script[src]').filter((_, el) => {
    const type = ($(el).attr('type') ?? '').toLowerCase();
    return type !== 'module' && $(el).attr('defer') === undefined && $(el).attr('async') === undefined;
  }).length;

  done += 1;
  if (done % 100 === 0) console.log(`  ${done}/${routes.length}`);

  return {
    route,
    sitemapRoute,
    status: res.status,
    bytes: Buffer.byteLength(body, 'utf8'),
    gzipBytes: gzip,
    contentEncodingServed: res.headers.get('content-encoding'),
    htmlLang,
    robotsMeta,
    images,
    imagesWithoutAlt,
    imagesEmptyAlt,
    duplicateIds,
    headingSkips: skips,
    h1Count: $('h1').length,
    islands,
    scripts,
    blockingScripts,
    title: $('title').text().slice(0, 80),
    canonical,
    dataState: /data-state="([^"]*)"/.exec(body)?.[1] ?? null,
  };
}

const queue = [...routes];
await Promise.all(Array.from({ length: CONCURRENCY }, async () => {
  while (queue.length) {
    const route = queue.shift();
    facts.pages.push(await inspect(route));
  }
}));

const ok = facts.pages.filter((p) => typeof p.bytes === 'number');
const sum = (list) => list.reduce((a, b) => a + b, 0);
const largest = ok.reduce((a, b) => (b.bytes > a.bytes ? b : a), ok[0]);
const byBytes = [...ok].sort((a, b) => b.bytes - a.bytes).slice(0, 10);

facts.totals = {
  pagesMeasured: ok.length,
  nonHtmlOrError: facts.pages.filter((p) => typeof p.bytes !== 'number').length,
  htmlBytesTotal: sum(ok.map((p) => p.bytes)),
  htmlBytesMean: Math.round(sum(ok.map((p) => p.bytes)) / ok.length),
  gzipBytesTotal: sum(ok.map((p) => p.gzipBytes)),
  gzipBytesMean: Math.round(sum(ok.map((p) => p.gzipBytes)) / ok.length),
  largestPage: { route: largest.route, bytes: largest.bytes, gzipBytes: largest.gzipBytes },
  pagesMissingLang: ok.filter((p) => !p.htmlLang).map((p) => p.route),
  langValues: [...new Set(ok.map((p) => p.htmlLang))],
  pagesWithImages: ok.filter((p) => p.images > 0).length,
  imagesTotal: sum(ok.map((p) => p.images)),
  imagesWithoutAltTotal: sum(ok.map((p) => p.imagesWithoutAlt)),
  imagesEmptyAltTotal: sum(ok.map((p) => p.imagesEmptyAlt)),
  pagesWithDuplicateIds: ok.filter((p) => p.duplicateIds.length).map((p) => `${p.route} ${p.duplicateIds.join(',')}`),
  pagesWithHeadingSkips: ok.filter((p) => p.headingSkips.length).map((p) => `${p.route} ${p.headingSkips.join(',')}`),
  pagesWithoutH1: ok.filter((p) => p.h1Count !== 1).map((p) => `${p.route} h1x${p.h1Count}`),
  pagesWithIslands: ok.filter((p) => p.islands > 0).length,
  maxIslandsPerPage: ok.reduce((a, b) => Math.max(a, b.islands), 0),
  scriptSrcTotal: sum(ok.map((p) => p.scripts)),
  pagesWithScripts: ok.filter((p) => p.scripts > 0).length,
  blockingScriptsTotal: sum(ok.map((p) => p.blockingScripts)),
  dataStates: [...new Set(ok.map((p) => p.dataState))],
  robotsMetaDistribution: Object.entries(ok.reduce((a, p) => {
    const k = p.robotsMeta ?? '<absent>';
    a[k] = (a[k] ?? 0) + 1;
    return a;
  }, {})).map(([k, v]) => `${k}=${v}`),
  sitemapUrlsWithoutTrailingSlash: ok.filter((p) => p.sitemapRoute !== p.route).length,
  sitemapFormDiffersFromCanonical: ok.filter((p) => {
    if (!p.canonical) return true;
    const c = new URL(p.canonical).pathname;
    return c !== p.sitemapRoute;
  }).length,
  sitemapListsNoindexedPages: ok.filter((p) => (p.robotsMeta ?? '').includes('noindex')).length,
  canonicalsNotOnOwnHost: ok.filter((p) => p.canonical && !p.canonical.startsWith('https://lol.erik-schuetze.dev/')).length,
  top10Largest: byBytes.map((p) => `${p.route} ${p.bytes}B/${p.gzipBytes}B gz`),
};

writeFileSync(path.join(OUT, 'tree-facts.json'), JSON.stringify(facts, null, 1));
console.log(JSON.stringify(facts.totals, null, 2));
