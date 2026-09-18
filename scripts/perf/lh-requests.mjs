#!/usr/bin/env node
// Name every request a Lighthouse run counted, with the byte split by resource class.
//
//   node scripts/perf/lh-requests.mjs docs/evidence/lh-r2-tier-list-top.json.gz
//   node scripts/perf/lh-requests.mjs --class-only docs/evidence/lh-r9-tier-list-top.json.gz
//
// This exists because the first-load budget is a claim about *requests*, and a hand-rolled
// measurement is easy to get wrong in one specific way: the six @font-face targets live inside the
// copied CSS, not in the HTML, so a sum that greps the document for url() misses 150.2 KiB on every
// route. Lighthouse's `network-requests` audit counts what the browser actually fetched, including
// those, and this prints that list so any two sums can be reconciled request by request.
//
// resourceSize is the uncompressed body, which is the unit plan.md 7.4's 300 KB budget is written in
// (the same field extract-lh.mjs sums into its first-load column at scripts/perf/extract-lh.mjs:82).

import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';

const argv = process.argv.slice(2);
const classOnly = argv.includes('--class-only');
const files = argv.filter((a) => !a.startsWith('--'));
if (files.length === 0) {
  console.error('usage: node scripts/perf/lh-requests.mjs [--class-only] <report.json[.gz]> ...');
  process.exit(2);
}

const read = (path) => JSON.parse((path.endsWith('.gz') ? gunzipSync(readFileSync(path)) : readFileSync(path)).toString());

const CLASSES = ['Document', 'Stylesheet', 'Script', 'Font', 'Image', 'Other', 'ThirdParty'];
const klass = (item) => {
  const t = item.resourceType === 'Other' && item.mimeType === 'font' ? 'Font' : item.resourceType;
  return CLASSES.includes(t) ? t : 'Other';
};
// Reports taken through the authenticated edge carry `//REDACTED@` in some url fields (the credential
// is stripped on write, but the userinfo position is left behind). Classification has to look past it,
// or same-origin requests get counted as third-party — which is how this file once reported three
// third-party requests on /explore/, with the document, its CSS and the favicon among them.
const bare = (u) => String(u ?? '').replace(/\/\/[^@/]*@/, '//');
const hostOf = (u) => {
  try {
    return new URL(bare(u)).host;
  } catch {
    return '';
  }
};

let exitCode = 0;
for (const path of files) {
  const report = read(path);
  const items = report.audits?.['network-requests']?.details?.items ?? [];
  if (items.length === 0) {
    console.error(`${path}: no network-requests audit`);
    exitCode = 1;
    continue;
  }
  const doc = items.find((i) => i.resourceType === 'Document') ?? items[0];
  const origin = hostOf(doc.url);
  const totals = {};
  const thirdPartyHosts = new Set();
  let thirdParty = 0;
  let thirdPartyBytes = 0;
  for (const item of items) {
    const c = klass(item);
    totals[c] = (totals[c] ?? 0) + (item.resourceSize || 0);
    if (hostOf(item.url) !== origin) {
      thirdParty += 1;
      thirdPartyBytes += item.resourceSize || 0;
      thirdPartyHosts.add(hostOf(item.url).replace(/^www\./, ''));
    }
  }

  console.log(`# ${path}`);
  console.log(`  requestedUrl ${bare(report.requestedUrl)}`);
  console.log(`  finalUrl     ${bare(report.finalUrl)}`);
  console.log(`  requests ${items.length}  sum(resourceSize) ${items.reduce((n, i) => n + (i.resourceSize || 0), 0)} B`);
  console.log(`  document HTML ${doc.resourceSize} B uncompressed`);

  if (!classOnly) {
    console.log('');
    console.log('  size      transfer  type        url');
    for (const item of [...items].sort((a, b) => (b.resourceSize || 0) - (a.resourceSize || 0))) {
      const url = bare(item.url);
      console.log(
        `  ${String(item.resourceSize ?? 0).padStart(8)}  ${String(item.transferSize ?? 0).padStart(8)}  ${klass(item).padEnd(10)}  ${url}`,
      );
    }
  }

  console.log('');
  for (const c of CLASSES) if (totals[c]) console.log(`  ${c.padEnd(11)} ${String(totals[c]).padStart(8)} B  ${(totals[c] / 1024).toFixed(1)} KiB`);
  console.log(
    `  third-party ${thirdParty} request(s), ${thirdPartyBytes} B${
      thirdPartyHosts.size ? ` (${[...thirdPartyHosts].sort().join(', ')})` : ' (same-origin only)'
    }`,
  );
  console.log('');
}
process.exit(exitCode);
