#!/usr/bin/env node
// Verifies that every figure in docs/PERF-EVIDENCE.md traces back to the raw evidence in
// docs/evidence/. This exists so the report cannot drift from the measurements: if a number in
// the prose disagrees with the committed JSON, this fails.
//
//   node scripts/perf/verify-report.mjs

import { existsSync, readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';

const DOC = process.env.PERF_DOC ?? 'docs/PERF-EVIDENCE.md';
const EV = process.env.PERF_EVIDENCE_DIR ?? 'docs/evidence';

const read = (p) => (p.endsWith('.gz') ? gunzipSync(readFileSync(p)) : readFileSync(p)).toString('utf8');
const json = (p) => JSON.parse(read(p));

let checks = 0;
let failures = 0;

function check(label, actual, expected) {
  checks++;
  const a = String(actual);
  const e = String(expected);
  if (a !== e) {
    failures++;
    console.error(`  FAIL  ${label}\n          doc: ${e}\n          raw: ${a}`);
  }
}

function checkTrue(label, cond, detail = '') {
  checks++;
  if (!cond) {
    failures++;
    console.error(`  FAIL  ${label}${detail ? ` (${detail})` : ''}`);
  }
}

const doc = readFileSync(DOC, 'utf8');
const r1 = json(`${EV}/lh-summary-r1.json`);
const r2 = json(`${EV}/lh-summary-r2.json`);
const axe = json(`${EV}/axe-summary.json`);
const tree = json(`${EV}/tree-facts.json`);
const pages = tree.pages ?? tree.routes;

const byRoute = (arr) => new Map(arr.map((o) => [o.route, o]));
const R1 = byRoute(r1);
const R2 = byRoute(r2);

// ---------------------------------------------------------------- §4 per-route table
const section = (title, next) => {
  const start = doc.indexOf(`## ${title}`);
  const end = doc.indexOf(`## ${next}`);
  return start === -1 || end === -1 ? '' : doc.slice(start, end);
};
const s4 = section('4.', '5.');

const tableRows = s4
  .split('\n')
  .filter((l) => /^\|\s*`\/[^`]*`\s*\|/.test(l))
  .map((l) =>
    l
      .split('|')
      .slice(1, -1)
      .map((c) => c.trim().replace(/`/g, '')),
  );

checkTrue('§4 has one row per audited route', tableRows.length === 9, `found ${tableRows.length}`);
checkTrue('§4 covers exactly the routes present in the raw summaries', R1.size === 9 && R2.size === 9);

for (const row of tableRows) {
  const [name, perf, a11y, bp, seo, axeSC, lcp, cls, tbt, htmlKiB, gzKiB, firstKiB, jsKiB] = row;
  const a = R1.get(name);
  const b = R2.get(name);
  if (!a || !b) {
    checkTrue(`§4 route ${name} exists in raw summaries`, false);
    continue;
  }
  const pair = (x) => `${x.scores.performance}/${b.scores.performance}`;
  check(`§4 ${name} perf`, `${a.scores.performance}/${b.scores.performance}`, perf.replace(/\*\*/g, ''));
  check(`§4 ${name} a11y`, `${a.scores.accessibility}/${b.scores.accessibility}`, a11y);
  check(`§4 ${name} BP`, `${a.scores.bestPractices}/${b.scores.bestPractices}`, bp);
  check(`§4 ${name} SEO`, `${a.scores.seo}/${b.scores.seo}`, seo.replace(/\*\*/g, ''));

  const axeRow = axe.find((x) => x.route === name);
  check(`§4 ${name} axe serious+critical`, axeRow.seriousOrCritical, axeSC);

  // §4 states LCP as round1–round2, matching the r1/r2 convention of the score columns
  const lcpPair = [Math.round(a.metrics.lcpMs), Math.round(b.metrics.lcpMs)];
  check(`§4 ${name} LCP range`, `${lcpPair[0]}–${lcpPair[1]}`, lcp.replace(/\*\*/g, ''));

  check(`§4 ${name} HTML KiB`, (a.weight.htmlRawBytes / 1024).toFixed(1), htmlKiB);
  check(`§4 ${name} HTML gz KiB (r1)`, (a.weight.htmlGzipBytes / 1024).toFixed(1), gzKiB);
  check(`§4 ${name} first-load KiB`, (a.weight.firstLoadBytes / 1024).toFixed(1), firstKiB.replace(/\*\*/g, ''));

  // JS cell is "<KiB> (<n> files)"; zero-JS routes print "0" rather than "0.0"
  const docJs = Number.parseFloat(jsKiB);
  const rawJs = a.weight.jsBytes / 1024;
  checkTrue(
    `§4 ${name} JS KiB matches raw`,
    Math.abs(docJs - rawJs) < 0.05,
    `doc ${docJs} raw ${rawJs.toFixed(1)}`,
  );
  const docJsFiles = Number.parseInt(jsKiB.match(/\((\d+)/)?.[1], 10);
  check(`§4 ${name} JS file count`, a.weight.jsFiles, docJsFiles);

  // first-load must be byte-identical between rounds, as §4's spread paragraph claims
  check(`§4 ${name} first-load identical across rounds`, a.weight.firstLoadBytes, b.weight.firstLoadBytes);

  const docRow = doc.split('\n').find((l) => l.startsWith(`| \`${name}\``));
  const expectedVerdict = a.overall === 'PASS' && b.overall === 'PASS' ? 'PASS' : 'FAIL';
  checkTrue(
    `§4 ${name} verdict is ${expectedVerdict}`,
    docRow.includes(expectedVerdict) && !(expectedVerdict === 'PASS' && /\*\*FAIL\*\*/.test(docRow)),
    docRow.slice(0, 40),
  );
}

// ---------------------------------------------------------------- §5 scorecard
const scorecard = {
  'Performance ≥90': [Math.min(...r1.map((x) => x.scores.performance)), 90],
  'Accessibility =100': [Math.min(...r1.map((x) => x.scores.accessibility)), 100],
  'Best Practices ≥95': [Math.min(...r1.map((x) => x.scores.bestPractices)), 95],
  'SEO ≥95': [Math.max(...r1.map((x) => x.scores.seo)), 95],
  'LCP ≤2.5s': [Math.max(...r1.concat(r2).map((x) => x.metrics.lcpMs)), 2500],
  'CLS ≤0.1': [Math.max(...r1.concat(r2).map((x) => x.metrics.cls)), 0.1],
  'TBT ≤200ms': [Math.max(...r1.concat(r2).map((x) => x.metrics.tbtMs)), 200],
};
checkTrue('§5 Performance measured min is 99', scorecard['Performance ≥90'][0] === 99);
checkTrue('§5 Accessibility measured min is 100', scorecard['Accessibility =100'][0] === 100);
checkTrue('§5 TBT measured max is 0', scorecard['TBT ≤200ms'][0] === 0);
checkTrue(
  '§5 LCP prose "worst 1,849 ms" matches raw',
  doc.includes(`${Math.round(Math.max(...r1.concat(r2).map((x) => x.metrics.lcpMs))).toLocaleString('en-US')} ms`),
  `raw max ${Math.round(Math.max(...r1.concat(r2).map((x) => x.metrics.lcpMs)))}`,
);
checkTrue('§5 states SEO 69 on exactly one route', r1.filter((x) => x.scores.seo < 95).length === 1);
checkTrue(
  '§5 weight failures are the 4 routes raw data marks FAIL',
  r1.filter((x) => x.grades.firstLoad === 'FAIL').length === 4,
);
checkTrue(
  '§5 first-load worst is 1013.3 KiB',
  doc.includes((Math.max(...r1.map((x) => x.weight.firstLoadBytes)) / 1024).toFixed(1)),
);
checkTrue(
  '§5 axe serious+critical total is 0',
  axe.every((x) => x.seriousOrCritical === 0),
);
checkTrue('§5 axe rule count matches summary', doc.includes(`${axe[0].rulesEvaluated} rules evaluated`));

// ---------------------------------------------------------------- §7 tree facts
const sum = (k) => pages.reduce((n, p) => n + (p[k] || 0), 0);
const num = (n) => n.toLocaleString('en-US');
const largest = pages.reduce((x, p) => (p.bytes > x.bytes ? p : x));

checkTrue('§7 page count matches', doc.includes(num(pages.length)), `raw ${pages.length}`);
checkTrue('§7 total HTML bytes matches', doc.includes(num(sum('bytes'))), `raw ${sum('bytes')}`);
checkTrue('§7 mean HTML bytes matches', doc.includes(num(Math.round(sum('bytes') / pages.length))));
checkTrue('§7 mean gzip bytes matches', doc.includes(num(Math.round(sum('gzipBytes') / pages.length))));
checkTrue(
  '§7 largest page route + bytes match',
  doc.includes(largest.route) && doc.includes(num(largest.bytes)),
  `raw ${largest.route} ${largest.bytes}`,
);
checkTrue('§7 largest page gzip matches', doc.includes(num(largest.gzipBytes)));
checkTrue('§7 images without alt is 0', sum('imagesWithoutAlt') === 0 && doc.includes('0 without an `alt`'));
checkTrue('§7 image totals match', doc.includes(num(sum('images'))) && doc.includes(num(sum('imagesEmptyAlt'))));
checkTrue('§7 pages missing lang is 0', pages.filter((p) => !p.htmlLang).length === 0);
checkTrue(
  '§7 duplicate ids / heading skips are 0 pages',
  pages.every((p) => (p.duplicateIds || []).length === 0 && (p.headingSkips || []).length === 0),
);
checkTrue('§7 every page has exactly one h1', pages.every((p) => p.h1Count === 1));
checkTrue('§7 islands max is 1', Math.max(...pages.map((p) => p.islands)) === 1);
checkTrue(
  '§7 island page count matches',
  pages.filter((p) => p.islands > 0).length === 10 && doc.includes('on 10 of 1,058 pages'),
);
checkTrue('§7 zero blocking scripts', sum('blockingScripts') === 0);
checkTrue(
  '§7 robots counts match',
  doc.includes(`\`index,follow\` ${pages.filter((p) => p.robotsMeta === 'index,follow').length}`) &&
    doc.includes(`\`noindex,follow\` **${pages.filter((p) => p.robotsMeta === 'noindex,follow').length}**`),
);
checkTrue(
  '§7 sitemap slash-less count matches',
  pages.filter((p) => !p.sitemapRoute.endsWith('/')).length === 1057 &&
    doc.includes('1,057 of 1,058 sitemap URLs'),
);
checkTrue('§7 every page reports data-state=live', pages.every((p) => p.dataState === 'live'));

// ---------------------------------------------------------------- §11.9 later measured rounds
// r6-r8 were measured after the /matchups grid was bounded. They are Go-tier rounds, not edge
// rounds, and §11.9 is where that is said. These checks exist for two reasons: so the section's
// figures cannot drift from the reports they cite, and so nobody can replace §5's r1/r2 measurement
// with a later round's better number without this failing.
const thousands = (n) => n.toLocaleString('en-US');

const section119 = (() => {
  const start = doc.indexOf('### 11.9 ');
  if (start === -1) return '';
  const next = doc.indexOf('\n## ', start + 1);
  return doc.slice(start, next === -1 ? undefined : next);
})();
checkTrue('§11.9 exists', section119.length > 1000, `${section119.length} chars`);

for (const round of ['6', '7', '8']) {
  const p = `${EV}/lh-summary-r${round}.json`;
  if (!existsSync(p)) continue;
  const records = json(p);
  const passed = records.filter((r) => r.overall === 'PASS').length;
  const failed = records.filter((r) => r.overall !== 'PASS');
  checkTrue(
    `r${round} summary is self-consistent`,
    records.every((r) => r.overall === (Object.values(r.grades).includes('FAIL') ? 'FAIL' : 'PASS')),
  );
  checkTrue(
    `r${round} reports the raw files it summarises`,
    records.every((r) => r.file.startsWith(`${EV}/lh-r${round}-`) && existsSync(r.file)),
  );
  // The only route allowed to fail in these rounds is the §6.2 SEO one; a second failure would mean
  // §11.9's tables are describing a different run than the reports do.
  checkTrue(
    `r${round} fails on nothing but §6.2's route`,
    failed.every((r) => r.route === '/champions/ahri/mid/' && r.failingAudits.includes('is-crawlable')),
    failed.map((r) => `${r.route}:${Object.entries(r.grades).filter(([, g]) => g === 'FAIL').map(([k]) => k)}`).join(' '),
  );
  if (round !== '6') {
    checkTrue(`§11.9 quotes r${round}'s pass count`, section119.includes(`${passed} of ${records.length} routes PASS`));
    // Each measured round must show up in §11.9's tables: the fixture round in the "fixture after"
    // column and the live round in the "live after" column of the byte table, plus its own column pair
    // in the Lighthouse table. Cell-by-cell, so the units and the ordering are checked too.
    const cellsOf = (l) => l.split('|').slice(1, -1).map((c) => c.trim());
    const byteTable = (() => {
      const h = section119.indexOf('| route | fixture before | fixture after');
      return h === -1 ? [] : section119.slice(h).split('\n').slice(2, 7).map(cellsOf);
    })();
    const lhTable = (() => {
      const h = section119.indexOf('| route | r7 fixture: HTML raw');
      return h === -1 ? [] : section119.slice(h).split('\n').slice(2, 7).map(cellsOf);
    })();
    checkTrue('§11.9 has both measured tables', byteTable.length === 5 && lhTable.length === 5);
    const byteCol = round === '7' ? 2 : 4;
    const lhCols = round === '7' ? [1, 3] : [6, 7];
    for (const r of records.filter((x) => x.route.startsWith('/matchups/'))) {
      const byteRow = byteTable.find((c) => c[0] === `\`${r.route}\``);
      const lhRow = lhTable.find((c) => c[0] === `\`${r.route}\``);
      const raw = `${thousands(r.weight.htmlRawBytes)} B`;
      const first = `${thousands(r.weight.firstLoadBytes)} B`;
      checkTrue(
        `§11.9's tables carry ${r.route} as measured in r${round}`,
        Boolean(byteRow) &&
          Boolean(lhRow) &&
          byteRow[byteCol].includes(raw) &&
          lhRow[lhCols[0]].includes(raw) &&
          lhRow[lhCols[1]].includes(first),
        byteRow ? `byte:${byteRow[byteCol]} lh:${lhRow[lhCols[0]]}/${lhRow[lhCols[1]]} raw:${raw}/${first}` : 'row missing',
      );
    }
  }
}
checkTrue(
  '§11.9 names the worst HTML and first-load of the shipped posture',
  section119.includes('74.1 KiB') &&
    section119.includes('245,229 B (239.5 KiB)') &&
    section119.includes('19.7% under'),
);
checkTrue(
  '§11.9 says its rounds are not edge measurements',
  section119.includes('not edge measurements') && section119.includes('No row above is a production measurement'),
);
checkTrue(
  '§11.9 keeps the r1/r2 26.4 KiB reading as a real measurement',
  section119.includes('26.4 KiB') && section119.includes('was never reproduced'),
);

// §5 still carries the r1/r2 measurement in its cells, with the later rounds as annotation.
const row = (label) => doc.split('\n').find((l) => l.startsWith(`| ${label} `)) ?? '';
checkTrue(
  '§5 first-load row still measures FAIL with the r1/r2 worst value',
  row('total first-load ≤300 KB uncompressed').includes('**FAIL**') &&
    row('total first-load ≤300 KB uncompressed').includes('1013.3'),
);
checkTrue(
  '§5 first-load row still refuses the §11 projection',
  row('total first-load ≤300 KB uncompressed').includes('not** superseded by them') ||
    row('total first-load ≤300 KB uncompressed').includes('**not** superseded by them'),
  row('total first-load ≤300 KB uncompressed').slice(0, 80),
);
checkTrue(
  '§5 HTML row keeps the later rounds that broke it',
  row('HTML ≤150 KB uncompressed').includes('164,502') && row('HTML ≤150 KB uncompressed').includes('2,768,758'),
);
checkTrue(
  '§5 HTML row conditions its PASS on the deploying image, not on a projection',
  row('HTML ≤150 KB uncompressed').includes('once the image carrying §11.9 is deployed') &&
    row('HTML ≤150 KB uncompressed').includes('Measured, not projected'),
);
checkTrue(
  'note (a) still states that a projection replacing a measurement would be a laundered pass',
  doc.includes('A projection that replaces a measurement would be a\nlaundered pass'),
);
checkTrue(
  'note (b) still records the coordinator withdrawing the fixture-only instruction',
  doc.includes('The coordinator withdrew') && doc.includes('LOLSTATS_AGG_FIXTURES=only'),
);

// ---------------------------------------------------------------- JS budget
const jsTotal = r1.reduce((n, x) => n + x.weight.jsBytes, 0);
checkTrue('total JS is far under the 50 KiB budget', jsTotal < 50 * 1024, `${jsTotal} B`);

console.log(
  failures === 0
    ? `verify-report: OK — ${checks} checks passed, 0 failures`
    : `verify-report: ${failures} of ${checks} checks FAILED`,
);
process.exit(failures === 0 ? 0 : 1);
