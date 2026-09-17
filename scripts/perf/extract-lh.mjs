#!/usr/bin/env node
// Extract the numbers plan.md 7.4 budgets from raw Lighthouse JSON reports.
//
// Usage:
//   node scripts/perf/extract-lh.mjs [--json] <report.json> [more.json ...]
//
// Thresholds (plan.md 7.4):
//   Lighthouse mobile preset, simulated throttling, cold cache
//   performance >= 90, accessibility = 100, best-practices >= 95, seo >= 95
//   LCP <= 2500 ms, CLS <= 0.1, TBT <= 200 ms, INP <= 200 ms (lab, when emitted)
//   HTML <= 150 KB uncompressed and <= 40 KB gzipped per route
//   total first-load transfer <= 300 KB uncompressed
//
// Scores within 3 points of a threshold are flagged MARGINAL so the caller knows
// the run must be repeated to separate signal from measurement noise.

import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';

const KB = 1024;
const T = {
  perf: 90,
  a11y: 100,
  a11yIsExact: true,
  bp: 95,
  seo: 95,
  lcpMs: 2500,
  cls: 0.1,
  tbtMs: 200,
  inpMs: 200,
  htmlRawBytes: 150 * KB,
  htmlGzipBytes: 40 * KB,
  firstLoadBytes: 300 * KB,
};

const argv = process.argv.slice(2);
const asJson = argv.includes('--json');
const files = argv.filter((a) => !a.startsWith('--'));

if (files.length === 0) {
  console.error('usage: node scripts/perf/extract-lh.mjs [--json] <report.json> ...');
  process.exit(2);
}

const audit = (lh, id) => lh.audits?.[id] ?? {};
const num = (lh, id) => {
  const v = audit(lh, id).numericValue;
  return typeof v === 'number' ? v : null;
};
const score = (lh, id) => {
  const v = lh.categories?.[id]?.score;
  return typeof v === 'number' ? Math.round(v * 100) : null;
};

// PASS / FAIL / MARGINAL against a lower-is-better or higher-is-better bound.
function grade(value, bound, { higherIsBetter, exact = false, tolerance }) {
  if (value === null || value === undefined) return 'NOT-VERIFIED';
  if (exact) return value === bound ? 'PASS' : 'FAIL';
  const ok = higherIsBetter ? value >= bound : value <= bound;
  if (!ok) return 'FAIL';
  const margin = higherIsBetter ? value - bound : bound - value;
  return margin <= tolerance ? 'MARGINAL' : 'PASS';
}

// Committed reports are gzip-compressed to keep the repository small; the bytes are
// still the unmodified Lighthouse output. Plain .json works too.
function readReport(file) {
  const buf = readFileSync(file);
  return (file.endsWith('.gz') ? gunzipSync(buf) : buf).toString('utf8');
}

function extract(file) {
  const lh = JSON.parse(readReport(file));
  const url = lh.finalDisplayedUrl || lh.requestedUrl || '';
  const route = url.replace(/^https?:\/\/[^/]+/, '') || '/';

  const reqs = audit(lh, 'network-requests').details?.items ?? [];
  const doc = reqs.find((r) => r.resourceType === 'Document');
  const htmlRaw = doc ? doc.resourceSize : null;
  const htmlGzip = doc ? doc.transferSize : null;
  const firstLoad = reqs.length
    ? reqs.reduce((a, r) => a + (r.resourceSize || 0), 0)
    : null;
  const jsReqs = reqs.filter((r) => r.resourceType === 'Script');

  // Lighthouse does not emit a lab INP in navigation mode; report presence honestly.
  const inpAudit = audit(lh, 'interaction-to-next-paint');
  const inp = typeof inpAudit.numericValue === 'number' ? inpAudit.numericValue : null;
  const inpStatus = inpAudit.scoreDisplayMode ?? 'absent';

  const rec = {
    file,
    route,
    scores: {
      performance: score(lh, 'performance'),
      accessibility: score(lh, 'accessibility'),
      bestPractices: score(lh, 'best-practices'),
      seo: score(lh, 'seo'),
    },
    metrics: {
      lcpMs: num(lh, 'largest-contentful-paint'),
      cls: num(lh, 'cumulative-layout-shift'),
      tbtMs: num(lh, 'total-blocking-time'),
      fcpMs: num(lh, 'first-contentful-paint'),
      speedIndexMs: num(lh, 'speed-index'),
      inpMs: inp,
      inpStatus,
    },
    weight: {
      htmlRawBytes: htmlRaw,
      htmlGzipBytes: htmlGzip,
      firstLoadBytes: firstLoad,
      jsBytes: jsReqs.reduce((a, r) => a + (r.resourceSize || 0), 0),
      jsFiles: jsReqs.length,
      requestCount: reqs.length,
      thirdPartyHosts: [...new Set(reqs
        .filter((r) => r.url && !/^http:\/\/127\.0\.0\.1/.test(r.url))
        .map((r) => new URL(r.url).host))].sort(),
    },
    failingAudits: Object.entries(lh.audits ?? {})
      .filter(([, a]) => a.score !== null && a.score < 1 && a.scoreDisplayMode === 'binary'
        && a.score < 1)
      .map(([id]) => id),
    grades: {
      performance: grade(score(lh, 'performance'), T.perf, { higherIsBetter: true, tolerance: 3 }),
      accessibility: grade(score(lh, 'accessibility'), T.a11y, { exact: true, higherIsBetter: true }),
      bestPractices: grade(score(lh, 'best-practices') === null ? null : score(lh, 'best-practices'), T.bp, { higherIsBetter: true, tolerance: 3 }),
      seo: grade(score(lh, 'seo'), T.seo, { higherIsBetter: true, tolerance: 3 }),
      lcp: grade(num(lh, 'largest-contentful-paint'), T.lcpMs, { higherIsBetter: false, tolerance: T.lcpMs * 0.1 }),
      cls: grade(num(lh, 'cumulative-layout-shift'), T.cls, { higherIsBetter: false, tolerance: T.cls * 0.1 }),
      tbt: grade(num(lh, 'total-blocking-time'), T.tbtMs, { higherIsBetter: false, tolerance: T.tbtMs * 0.1 }),
      htmlRaw: grade(htmlRaw, T.htmlRawBytes, { higherIsBetter: false, tolerance: T.htmlRawBytes * 0.05 }),
      htmlGzip: grade(htmlGzip, T.htmlGzipBytes, { higherIsBetter: false, tolerance: T.htmlGzipBytes * 0.05 }),
      firstLoad: grade(firstLoad, T.firstLoadBytes, { higherIsBetter: false, tolerance: T.firstLoadBytes * 0.05 }),
    },
  };
  rec.overall = Object.values(rec.grades).some((g) => g === 'FAIL')
    ? 'FAIL'
    : Object.values(rec.grades).some((g) => g === 'MARGINAL') ? 'MARGINAL' : 'PASS';
  return rec;
}

const records = files.map(extract);

if (asJson) {
  console.log(JSON.stringify(records, null, 2));
} else {
  const f1 = (v) => (v === null || v === undefined ? 'n/a' : v.toFixed(1));
  const kb = (v) => (v === null || v === undefined ? 'n/a' : (v / KB).toFixed(1));
  console.log('| route | perf | a11y | bp | seo | LCP ms | CLS | TBT ms | INP ms | HTML KiB | HTML gz KiB | 1st-load KiB | js files | overall |');
  console.log('| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |');
  for (const r of records) {
    const s = r.scores;
    const m = r.metrics;
    const w = r.weight;
    console.log(`| \`${r.route}\` | ${s.performance} | ${s.accessibility} | ${s.bestPractices} | ${s.seo} `
      + `| ${f1(m.lcpMs)} | ${m.cls === null ? 'n/a' : m.cls.toFixed(3)} | ${f1(m.tbtMs)} | ${f1(m.inpMs)} `
      + `| ${kb(w.htmlRawBytes)} | ${kb(w.htmlGzipBytes)} | ${kb(w.firstLoadBytes)} | ${w.jsFiles} | ${r.overall} |`);
  }
  console.log('\ngrades:');
  for (const r of records) {
    const bad = Object.entries(r.grades).filter(([, g]) => g !== 'PASS').map(([k, g]) => `${k}=${g}`);
    console.log(`  ${r.route.padEnd(26)} ${r.overall.padEnd(9)} ${bad.join(' ') || 'all within budget'}`);
  }
}
