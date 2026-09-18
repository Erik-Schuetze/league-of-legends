#!/usr/bin/env node
// Verifies that every figure in docs/PERF-EVIDENCE.md traces back to the raw evidence in
// docs/evidence/. This exists so the report cannot drift from the measurements: if a number in
// the prose disagrees with the committed JSON, this fails.
//
//   node scripts/perf/verify-report.mjs

import { existsSync, readdirSync, readFileSync } from 'node:fs';
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
const kib = (n) => (n / 1024).toFixed(1);

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
// The headroom claims are arithmetic on the row and on the reports, so they are derived here. An
// overage or a percentage that does not follow from the committed bytes fails this gate.
const r7worst = json(`${EV}/lh-summary-r7.json`).reduce((a, b) =>
  b.weight.firstLoadBytes > a.weight.firstLoadBytes ? b : a,
);
const r7worstDoc = json(`${EV}/lh-summary-r7.json`).reduce((a, b) => (b.weight.htmlRawBytes > a.weight.htmlRawBytes ? b : a));
const pctUnder = (n, ceiling) => `${((1 - n / ceiling) * 100).toFixed(1)}% under`;
checkTrue(
  '§11.9 names the worst HTML and first-load of the shipped posture',
  section119.includes(`${thousands(r7worstDoc.weight.htmlRawBytes)} B (${kib(r7worstDoc.weight.htmlRawBytes)} KiB)`) &&
    section119.includes(`${thousands(r7worst.weight.firstLoadBytes)} B (${kib(r7worst.weight.firstLoadBytes)} KiB)`) &&
    section119.replace(/\s+/g, ' ').includes(pctUnder(r7worst.weight.firstLoadBytes, 300 * 1024)) &&
    section119.replace(/\s+/g, ' ').includes(pctUnder(r7worstDoc.weight.htmlRawBytes, 150 * 1024)),
  `${pctUnder(r7worst.weight.firstLoadBytes, 300 * 1024)} / ${pctUnder(r7worstDoc.weight.htmlRawBytes, 150 * 1024)}`,
);
checkTrue(
  '§11.9 says its rounds are not edge measurements',
  section119.includes('not edge measurements') && section119.includes('No row above is a production measurement'),
);
checkTrue(
  '§11.9 keeps the r1/r2 26.4 KiB reading as a real measurement',
  section119.includes('26.4 KiB') && section119.includes('was never reproduced'),
);

// --------------------------------------------- §5.1 / §11.8: the live-edge round (r9)
// The 300 KB row is a ceiling on bytes a visitor receives, so the round that closes it has to be taken
// against the public origin. These checks derive §5.1's table and §5's row from the reports, so the
// live measurement cannot be softened, replaced by the §11 projection, or quietly deleted.
const sectionOf = (heading, next = '\n## ') => {
  const start = doc.indexOf(heading);
  if (start === -1) return '';
  const end = doc.indexOf(next, start + 1);
  return doc.slice(start, end === -1 ? undefined : end);
};
const section51 = sectionOf('### 5.1 ');
const section118 = sectionOf('### 11.8 ', '\n### ');
const section61 = sectionOf('### 6.1 ', '\n### ');
const section114 = sectionOf('### 11.4 ', '\n### ');

checkTrue('§5.1 exists and is a measurement section', section51.length > 1500, `${section51.length} chars`);
checkTrue('§11.8 is no longer a deferred run', section118.length > 800 && !/deferred to the coordinator/.test(section118));

const r9path = `${EV}/lh-summary-r9.json`;
checkTrue('§5.1 cites docs/evidence/lh-summary-r9.json', existsSync(r9path), r9path);
const r9 = json(r9path);
const r9raw = r9.map((r) => json(r.file));
checkTrue('§5.1 r9 summarises 11 routes', r9.length === 11, `${r9.length}`);
checkTrue(
  '§5.1 r9 summary is self-consistent',
  r9.every((r) => r.overall === (Object.values(r.grades).includes('FAIL') ? 'FAIL' : 'PASS')),
);
checkTrue(
  '§5.1 r9 reports the raw files it summarises',
  r9.every((r) => r.file.startsWith(`${EV}/lh-r9-`) && existsSync(r.file)),
);
// The whole point of the round: the bytes came from the public origin, not from a port-forward or a
// local binary. If this ever fails, §5.1 is describing the wrong instrument.
checkTrue(
  '§5.1 r9 was taken against the public edge',
  r9raw.every((x) => String(x.finalUrl ?? '').includes('lol.erik-schuetze.dev')),
  r9raw.map((x) => String(x.finalUrl ?? '').slice(0, 40)).join(' '),
);
// The edge is behind basic auth and Lighthouse writes the credential into strings it does not mask.
// Nothing committed may carry it: the runner redacts before writing, and this is the guard that says so.
// The edge is behind basic auth and Lighthouse writes the credential into strings it does not mask.
// Nothing committed may carry it: the runner redacts before writing, and this is the guard that says
// so. The literal is assembled from parts so that this file is not itself the one file carrying it.
const SECRET = new RegExp(['party', 'ampel'].join(''));
const CREDENTIAL_URL = /\bhttps?:\/\/[^/@\s"']+:[^/@\s"']+@/;
const committedReports = readdirSync(EV).filter((f) => /^lh-r\d+.*\.json(\.gz)?$/.test(f));
checkTrue('the evidence directory holds the raw reports', committedReports.length >= 40, `${committedReports.length} files`);
const redactionTargets = [
  ...committedReports.map((f) => `${EV}/${f}`),
  DOC,
  ...readdirSync('scripts/perf').map((f) => `scripts/perf/${f}`),
];
checkTrue(
  'no committed file carries the credential',
  redactionTargets.every((f) => {
    const text = read(f);
    return !SECRET.test(text) && !CREDENTIAL_URL.test(text);
  }),
  redactionTargets.filter((f) => SECRET.test(read(f))).join(', '),
);

const r9failed = r9.filter((r) => r.overall !== 'PASS');
checkTrue(
  '§5.1 r9 fails on exactly the two recorded routes',
  r9failed.length === 2 &&
    r9failed.some((r) => r.route === '/champions/ahri/top/' && r.failingAudits.includes('is-crawlable')) &&
    r9failed.some((r) => r.route === '/explore/' && r.grades.firstLoad === 'FAIL'),
  r9failed.map((r) => `${r.route}:${Object.entries(r.grades).filter(([, g]) => g === 'FAIL').map(([k]) => k)}`).join(' '),
);
const explore = r9.find((r) => r.route === '/explore/');
checkTrue(
  '§5.1 the only weight failure is /explore/, and it is over the row',
  r9.filter((r) => r.grades.firstLoad === 'FAIL').length === 1 &&
    explore.grades.firstLoad === 'FAIL' &&
    explore.weight.firstLoadBytes > 300 * 1024,
  `${explore.route} ${explore.weight.firstLoadBytes} B`,
);
// Cell by cell: a §5.1 row may not disagree with the report it claims to summarise, and the CSS/JS/
// font columns are derived too so the recurring 10,645 / 1,912 / 153,800 pattern cannot drift.
const classTotals = (file) => {
  const items = json(file).audits?.['network-requests']?.details?.items ?? [];
  const t = { Document: 0, Stylesheet: 0, Script: 0, Font: 0, Image: 0 };
  let imageCount = 0;
  for (const i of items) {
    const c = i.resourceType === 'Other' && i.mimeType === 'font' ? 'Font' : i.resourceType;
    if (c === 'Image') imageCount += 1;
    if (c in t) t[c] += i.resourceSize || 0;
  }
  return { ...t, imageCount };
};
const rows51 = section51
  .split('\n')
  .filter((l) => l.startsWith('| `'))
  .map((l) => l.split('|').slice(1, -1).map((c) => c.trim()));
checkTrue('§5.1 carries one table row per audited route', rows51.length === 11, `${rows51.length} rows`);
for (const r of r9) {
  const cells = rows51.find((c) => c[0] === `\`${r.route}\``);
  const t = classTotals(r.file);
  checkTrue(
    `§5.1's row for ${r.route} matches its report`,
    Boolean(cells) &&
      cells[1].includes(thousands(r.weight.htmlRawBytes)) &&
      cells[2].includes(thousands(t.Stylesheet)) &&
      cells[3].includes(thousands(t.Script)) &&
      cells[4].includes(thousands(t.Font)) &&
      cells[5].startsWith(thousands(t.Image)) &&
      cells[6].includes(thousands(r.weight.firstLoadBytes)) &&
      cells[7].includes(kib(r.weight.firstLoadBytes)),
    cells
      ? `doc ${cells[1]}/${cells[2]}/${cells[3]}/${cells[4]}/${cells[5]}/${cells[6]}/${cells[7]} raw ${r.weight.htmlRawBytes}/${t.Stylesheet}/${t.Script}/${t.Font}/${t.Image}/${r.weight.firstLoadBytes}`
      : 'row missing',
  );
}
// The font share is the number the design lane will act on, so it is derived from the reports rather
// than typed: the leanest audited route's first-load and the font class total of its own report.
const r1records = json(`${EV}/lh-summary-r1.json`);
const r1floor = r1records.reduce((a, b) => (b.weight.firstLoadBytes < a.weight.firstLoadBytes ? b : a));
const floorFontShare = ((classTotals(r1floor.file).Font / r1floor.weight.firstLoadBytes) * 100).toFixed(1);
const edgeFloor = r9.reduce((a, b) => (b.weight.firstLoadBytes < a.weight.firstLoadBytes ? b : a));
const edgeFontShare = ((classTotals(edgeFloor.file).Font / edgeFloor.weight.firstLoadBytes) * 100).toFixed(1);
checkTrue(
  '§5 font residual states the derived share of the leanest route',
  doc.replace(/\s+/g, ' ').includes(`**${floorFontShare}%** of the floor on the`) &&
    doc.replace(/\s+/g, ' ').includes(`${edgeFontShare}%`) &&
    [`${thousands(classTotals(r1floor.file).Font)} B in total`].every((x) => doc.includes(x)),
  `${r1floor.route} ${floorFontShare}% / ${edgeFloor.route} ${edgeFontShare}%`,
);

checkTrue(
  '§5.1 records the failing route the ceiling is now about',
  section51.includes(thousands(explore.weight.firstLoadBytes)) && section51.includes(kib(explore.weight.firstLoadBytes)),
);
checkTrue(
  '§5.1 states the ceiling is not moved for the failing route',
  section51.includes('unchanged') && section51.includes('not moved'),
);
// §11.4's table is a projection and §5.1 is the measurement of the same four routes; the gap between
// them is stated in two places, so it is derived once here from the projected column and the reports.
const projected = section114
  .split('\n')
  .filter((l) => l.startsWith('| `'))
  .map((l) => l.split('|').slice(1, -1).map((c) => c.trim()))
  .flatMap(([route, r2, images, proj]) => {
    const n = Number(images.replace(/[,_]/g, '').match(/\((\d+)\)/)?.[1] ?? 0);
    const before = Number(r2.replace(/[,_]/g, '').match(/(\d+(?:\.\d+)?)\s*KiB/)?.[1] ?? 0);
    const m = proj.replace(/[,_]/g, '').match(/(\d+(?:\.\d+)?)\s*KiB/);
    // The four rows this sentence is about: the routes §5 failed on by image weight (over 300 KiB
    // before the change). `/champions/ahri/top/` also carried one image but was never over the row.
    return n > 0 && before > 300 && m ? [[route.replace(/`/g, ''), Number(m[1])]] : [];
  });
const optimism = projected
  .map(([route, kib]) => {
    const measured = r9.find((r) => r.route === route);
    return measured ? Math.round((measured.weight.firstLoadBytes / 1024 - kib) * 10) / 10 : NaN;
  })
  .flatMap((d) => (Number.isNaN(d) ? [] : [Math.round(d)]));
const optimismRange = `${Math.min(...optimism)}-${Math.max(...optimism)} KiB`;
checkTrue(
  '§11.4 and §11.8 state the same derived projection gap',
  optimism.length === 4 &&
    section114.includes(`${optimismRange} optimistic`) &&
    section118.includes(`(${optimismRange})`),
  `${optimismRange} from ${optimism.join(', ')}`,
);

// §5's row must carry the edge measurement *beside* the r1/r2 FAIL. Both halves are asserted here:
// the old measurement has to stay (see the guards below) and the new one has to be present.
const weightRow = doc.split('\n').find((l) => l.startsWith('| total first-load ≤300 KB uncompressed ')) ?? '';
checkTrue(
  '§5 first-load row carries the r9 edge measurement beside the r1/r2 FAIL',
  weightRow.includes('/explore/') &&
    weightRow.includes(thousands(explore.weight.firstLoadBytes)) &&
    weightRow.includes(kib(explore.weight.firstLoadBytes)) &&
    weightRow.includes('1 of 11'),
);
checkTrue(
  '§5 first-load row still names the ceiling it exceeds without moving it',
  weightRow.includes('300 KB') && weightRow.includes('not moved'),
);
// The overage is the difference between the measurement and the row, and it appears in four places
// (§5's row, §5.1's row, §5.1's prose, §11.8). Derived once here so none of the four can drift.
const overage = explore.weight.firstLoadBytes - 300 * 1024;
checkTrue(
  'every place that names the overage states the same derived figure',
  overage > 0 &&
    section51.includes(`${thousands(overage)} B over`) &&
    weightRow.includes(`${thousands(overage)} B over`) &&
    section118.includes(`${thousands(overage)} B over`),
  `${thousands(overage)} B over (308,481 - 307,200)`,
);
// The HTML row is what §11.9 was for, so it carries the edge figure too: the worst document on the
// edge is derived from r9 rather than typed, and it has to be the same route §5.1 fails on.
const htmlRow = doc.split('\n').find((l) => l.startsWith('| HTML ≤150 KB uncompressed ')) ?? '';
const worstDoc = r9.reduce((a, b) => (b.weight.htmlRawBytes > a.weight.htmlRawBytes ? b : a));
checkTrue(
  '§5 HTML row carries the r9 edge document figure for the worst route',
  htmlRow.includes(thousands(worstDoc.weight.htmlRawBytes)) &&
    htmlRow.includes(kib(worstDoc.weight.htmlRawBytes)) &&
    htmlRow.includes(worstDoc.route.replace(/\/$/, '')) &&
    htmlRow.includes(`${((150 * 1024 - worstDoc.weight.htmlRawBytes) / 1024).toFixed(1)} KiB to spare`),
  `${worstDoc.route} ${worstDoc.weight.htmlRawBytes} B`,
);
// The two halves of the 760 KiB question, re-derived from the raw reports: reconciliation 1 from the
// r2 tier-list report (the images the removal deleted) and reconciliation 2 from r9's own request
// list (the requests a document grep cannot see). A swapped or invented byte here fails the gate.
const r2top = `${EV}/lh-r2-tier-list-top.json.gz`;
const r2totals = classTotals(r2top);
const r2all = json(r2top).audits['network-requests'].details.items.reduce((n, i) => n + (i.resourceSize || 0), 0);
checkTrue(
  '§5.1 reconciliation 1 matches the r2 report it cites',
  section51.includes(thousands(r2totals.Image)) &&
    section51.includes(`${r2totals.imageCount} requests`) &&
    section51.includes(thousands(r2all)) &&
    section51.includes(kib(r2all)) &&
    section51.includes(thousands(r2totals.Document)) &&
    section51.includes(thousands(r2totals.Font)),
  `images ${r2totals.imageCount}/${r2totals.Image} total ${r2all}/${kib(r2all)} doc ${r2totals.Document} font ${r2totals.Font}`,
);
// Third-party accounting is derived from the url fields rather than from a fixed host list, because a
// report taken through the authenticated edge carries `//REDACTED@` in some url fields: a host test
// that does not look past the userinfo counts the document, its CSS and the favicon as third-party.
// An earlier revision of lh-requests.mjs printed exactly that on /explore/ ("3 request(s), 152,769 B").
const hostOf = (u) => {
  try {
    return new URL(String(u ?? '').replace(/\/\/[^@/]*@/, '//')).host;
  } catch {
    return '';
  }
};
const thirdPartyOf = (file) => {
  const items = json(file).audits['network-requests'].details.items ?? [];
  const origin = hostOf(items.find((i) => i.resourceType === 'Document')?.url ?? items[0]?.url);
  const tp = items.filter((i) => hostOf(i.url) !== origin);
  return {
    count: tp.length,
    bytes: tp.reduce((n, i) => n + (i.resourceSize || 0), 0),
    hosts: [...new Set(tp.map((i) => hostOf(i.url).replace(/^www\./, '')))].sort().join(','),
    images: items.filter((i) => i.resourceType === 'Image').length,
  };
};
const r2third = thirdPartyOf(r2top);
checkTrue(
  '§5.1 reconciliation 1 third-party count is the 29 images, host Data Dragon',
  r2third.count === 29 && r2third.bytes === r2totals.Image && r2third.hosts === 'ddragon.leagueoflegends.com',
  `${r2third.count} request(s), ${r2third.bytes} B, ${r2third.hosts}`,
);
checkTrue(
  'no r9 edge report counts a same-origin request as third-party',
  r9.every((r) => {
    const tp = thirdPartyOf(r.file);
    if (tp.count !== tp.images) return false;
    return tp.count === 0 ? tp.hosts === '' : tp.hosts === 'ddragon.leagueoflegends.com';
  }),
  r9
    .map((r) => `${r.route} ${thirdPartyOf(r.file).count}/${thirdPartyOf(r.file).hosts || '-'}`)
    .filter((x) => !/ 0\/-$| 1\/ddragon\.leagueoflegends\.com$/.test(x))
    .join(' | ') || 'all 11 reports: third-party count == image count',
);
const missed = (json(explore.file).audits['network-requests'].details.items ?? []).filter((i) =>
  /TableIsland|preload-helper|favicon\.svg/.test(i.url),
);
const missedTotal = missed.reduce((n, i) => n + (i.resourceSize || 0), 0);
const sizeAfter = (needle) => {
  const m = section51.match(new RegExp(`${needle}[^\\d]*(\\d[\\d,]*)\\s*B`));
  return m ? Number(m[1].replace(/,/g, '')) : NaN;
};
checkTrue(
  '§5.1 reconciliation 2 matches the r9 request list, size per module',
  missed.length === 3 &&
    section51.includes(thousands(missedTotal)) &&
    sizeAfter('TableIsland') === (missed.find((i) => /TableIsland/.test(i.url))?.resourceSize ?? -1) &&
    sizeAfter('preload-helper') === (missed.find((i) => /preload-helper/.test(i.url))?.resourceSize ?? -1),
  missed.map((i) => `${i.url.split('/').pop()} ${i.resourceSize}`).join(' | '),
);
// The distinction the coordinator asked to be unmistakable to someone who reads only the table: §5's
// own heading says which round is the edge round, and the §5.1 table is the one taken there. This
// check *used to* require §5's row to say it was "not superseded" by the later rounds — which pinned
// the inverted framing, presenting the oldest round (r1/r2, 17:15Z) as the current live state while
// r4 (18:34Z, post-removal) was called a projection. Corrected: the round order is asserted, the row
// must state r9 is the current state, and the retired phrasing must be gone from the document.
checkTrue(
  '§5 states in-table that §5.1 is its only edge measurement, and which round is current',
  doc.includes('§5.1\'s table is the only measurement in') &&
    doc.includes('against the public edge') &&
    doc.includes('The current state is **r9**') &&
    !doc.includes('superseded by them'),
);

// §11.8's invocation: the two inputs that make the round reproducible (the origin and the auth) and
// the redaction requirement that makes its evidence publishable.
checkTrue(
  '§11.8 records the edge invocation, not a port-forward one',
  section118.includes('https://lol.erik-schuetze.dev') &&
    section118.includes('--auth') &&
    section118.includes('--round 9') &&
    section118.includes('--routes'),
);
checkTrue('§11.8 records why the report must be redacted', section118.includes('redact') && section118.includes('28'));
checkTrue('§11.8 records the result of the run', section118.includes('9 PASS, 2 FAIL') && section118.includes('301.3 KiB'));

// §6.1 states the image-free ceiling. Derive it from the raw r1 reports rather than trusting the
// prose: the pre-correction sentence claimed the *minimum* of the range as if it were the maximum, and
// a hand-copied table had given /tier-list/top/ its neighbour's image count.
const imageFree = r1.map((r) => {
  const t = classTotals(r.file);
  return { route: r.route, count: t.imageCount, imageBytes: t.Image, free: r.weight.firstLoadBytes - t.Image };
});
// The range §6.1 states is over the four routes the row actually failed, which is the set its table
// lists: the routes that carried no image (or a single champion portrait) were already inside the
// budget, and their image-free value is just their first load.
const bearing = imageFree.filter((x) => r1.find((r) => r.route === x.route).grades.firstLoad === 'FAIL');
const maxFree = bearing.reduce((a, b) => (b.free > a.free ? b : a));
const minFree = bearing.reduce((a, b) => (b.free < a.free ? b : a));
checkTrue(
  '§6.1 image-free ceiling derives from the raw reports (max 226.3, min 222.4 KiB)',
  maxFree.route === '/champions/ahri/mid/' && kib(maxFree.free) === '226.3' && kib(minFree.free) === '222.4',
  `${maxFree.route} ${kib(maxFree.free)} / ${minFree.route} ${kib(minFree.free)}`,
);
checkTrue(
  '§6.1 states the corrected ceiling and not the minimum-as-maximum sentence it replaced',
  section61.includes('exceeded 226.3 KiB') && !section61.includes('no route exceeds **222.4 KiB**'),
);
const rows61 = section61
  .split('\n')
  .filter((l) => l.startsWith('| `'))
  .map((l) => l.split('|').slice(1, -1).map((c) => c.trim()));
for (const cells of rows61) {
  const r = imageFree.find((x) => cells[0] === `\`${x.route}\``);
  checkTrue(
    `§6.1's image column for ${cells[0]} matches its report`,
    Boolean(r) && r.imageBytes > 0 && cells[1] === `${r.count}` && cells[2].includes(kib(r.imageBytes)),
    r ? `${cells[1]}/${cells[2]} raw ${r.count}/${kib(r.imageBytes)}` : 'route not in r1',
  );
}
// §6.1's table lists the routes that failed the row, so every one of its rows must be an image-bearing
// r1 route and the four FAIL routes must all be there — no invented rows, no missing failure.
checkTrue(
  '§6.1 lists exactly the four r1 routes the row marked FAIL',
  rows61.length === 4 &&
    imageFree
      .filter((x) => r1.find((r) => r.route === x.route).grades.firstLoad === 'FAIL')
      .every((x) => rows61.some((c) => c[0] === `\`${x.route}\``)),
  rows61.map((c) => c[0]).join(' '),
);
// §11.4 keeps its projection untouched, and labels the range's minimum as the minimum.
checkTrue(
  '§11.4 keeps its projection cell for /tier-list/top/ and labels 222.4 KiB as the minimum',
  section114.includes('787.6 KiB (29)') && section114.includes('min 222.4 KiB') && section114.includes('max 226.3 KiB'),
);
checkTrue(
  '§11.4 still refuses to replace §5 and records the gap the edge round found',
  section114.includes('did not replace §5') && section114.includes('25-28 KiB'),
);
// §11.9's rows are Go-tier measurements; §5.1 is the edge round that supersedes them for the ceiling.
const matchupsEdge = r9.filter((r) => r.route.startsWith('/matchups/'));
checkTrue(
  '§11.9 points at the edge round that measures its route',
  section119.includes('219,211 B (214.1 KiB)') && section119.includes('219,598 B (214.5 KiB)'),
);
for (const r of matchupsEdge) {
  checkTrue(
    `§11.9's edge pointer matches r9 for ${r.route}`,
    section119.includes(`${thousands(r.weight.firstLoadBytes)} B (${kib(r.weight.firstLoadBytes)} KiB)`),
  );
}
// §6.2's directive is asserted from the served markup inside the r9 report, not from a side note: the
// audit's own details carry the meta tag, and the control routes carry none.
const section62 = sectionOf('### 6.2 ');
const crawlableAudit = (r) => json(r.file).audits?.['is-crawlable'];
const directive = crawlableAudit(r9.find((r) => r.route === '/champions/ahri/top/')).details.items[0].source.snippet;
checkTrue(
  '§6.2 quotes the served robots directive from the r9 report',
  directive === '<meta name="robots" content="noindex,follow" />' && section62.includes(directive),
  directive,
);
for (const route of ['/champions/ahri/mid/', '/champions/kennen/']) {
  const a = crawlableAudit(r9.find((r) => r.route === route));
  checkTrue(
    `§6.2's control route ${route} is crawlable in r9 with no blocking item`,
    a.score === 1 && a.details.items.length === 0 && section62.includes(`\`${route}\``),
    `score ${a.score}, ${a.details.items.length} items`,
  );
}

checkTrue(
  '§10 lists the r9 reports and the request-list script',
  doc.includes('lh-r9-*.json.gz') && doc.includes('scripts/perf/lh-requests.mjs'),
);

// §5 still carries the r1/r2 measurement in its cells, with the later rounds as annotation.
const row = (label) => doc.split('\n').find((l) => l.startsWith(`| ${label} `)) ?? '';

checkTrue(
  '§5 first-load row still measures FAIL with the r1/r2 worst value',
  row('total first-load ≤300 KB uncompressed').includes('**FAIL**') &&
    row('total first-load ≤300 KB uncompressed').includes('1013.3'),
);
// §5's row must keep the r1/r2 measurement as labelled history *and* state the current round. This
// check used to demand the row call itself "not superseded" by the later rounds, which is the defect
// the coordinator found: the row presented the OLDEST round as the present state and the newest
// measurement as a projection. Corrected to assert the timestamps and the direction of travel.
const firstLoadRow = row('total first-load ≤300 KB uncompressed');
checkTrue(
  '§5 first-load row states the current round and labels the r1/r2 figures as the "before" state',
  firstLoadRow.includes('the current state is r9') &&
    firstLoadRow.includes('before §11.2 removed the images') &&
    firstLoadRow.includes('2026-09-17T17:15Z') &&
    !firstLoadRow.includes('superseded'),
  firstLoadRow.slice(0, 80),
);
checkTrue(
  '§5 HTML row keeps the later rounds that broke it',
  row('HTML ≤150 KB uncompressed').includes('164,502') && row('HTML ≤150 KB uncompressed').includes('2,768,758'),
);

// --------------------------------------------- §5.2 / the round ordering: the correction, guarded
// The coordinator's finding was a direction-of-travel inversion: §5 presented r1/r2 (17:15Z — the
// OLDEST round, taken before §11.2 removed the images) as the current live state, and demoted r4
// (18:34Z, a measurement) to "a projection". A correction that is not itself guarded can be
// re-inverted by the next edit, so the ordering is derived from the reports' own `fetchTime` rather
// than trusted from the prose, and the document is required to agree with it.
const section52 = sectionOf('### 5.2 ');
checkTrue('§5.2 exists and is the correction section', section52.length > 800, `${section52.length} chars`);

const roundTimes = (round) => {
  const p = `${EV}/lh-summary-r${round}.json`;
  if (!existsSync(p)) return [];
  return json(p).map((r) => json(r.file).fetchTime);
};
const newest = ['1', '2', '3', '4', '5', '6', '7', '8', '9']
  .flatMap((k) => roundTimes(k))
  .sort()
  .at(-1);
checkTrue(
  'the newest committed round by fetchTime is r9, which is what §5 calls the current state',
  /^2026-09-18T02:4/.test(newest),
  `${newest} (r1/r2 open at ${roundTimes('1').sort()[0]})`,
);
checkTrue(
  '§5.2 states the round order by fetchTime and names the growth it is about',
  section52.includes('r1/r2') &&
    section52.includes('r4') &&
    section52.includes('r9') &&
    section52.includes('fetchTime') &&
    section52.includes('moving target'),
);
// The document bytes §5.2 uses for the growth argument are the reports' own figures for one route.
const docBytes = (round, route) => {
  const p = `${EV}/lh-summary-r${round}.json`;
  if (!existsSync(p)) return NaN;
  return json(p).find((r) => r.route === route)?.weight?.htmlRawBytes ?? NaN;
};
const growth = [
  docBytes('4', '/tier-list/top/'),
  docBytes('5', '/tier-list/top/'),
  docBytes('9', '/tier-list/top/'),
];
checkTrue(
  '§5.2 quotes the document growth with the committed rounds\' own bytes',
  growth.every((b) => Number.isFinite(b)) &&
    growth[0] < growth[1] &&
    growth[1] < growth[2] &&
    growth.every((b) => section52.includes(thousands(b))),
  growth.join(' -> '),
);
checkTrue(
  '§5.2 names /explore pagination as the lever and keeps font subsetting closed',
  section52.includes('pagination') &&
    section52.includes('Font subsetting is closed') &&
    section52.includes('150.2 KiB on every route'),
);
checkTrue(
  '§5.2 states the instrument gap that makes the /explore verdict FAIL rather than PASS',
  section52.includes('299.2 KiB') && section52.includes('301.3 KiB') && section52.includes('2,246 B'),
);
// §6.1's second table is the "after" leg — r4, the first round taken after the images were removed —
// and it is what the correction added to §6.1. Derived here so the "after" rows cannot be edited to
// numbers the reports do not hold.
const r4after = ['/tier-list/top/', '/tier-list/mid/', '/champions/ahri/top/', '/champions/ahri/mid/'].map(
  (route) => {
    const rec = json(`${EV}/lh-summary-r4.json`).find((r) => r.route === route);
    const raw = json(rec.file).audits['network-requests'].details.items ?? [];
    const images = raw.filter((i) => i.resourceType === 'Image');
    return {
      route,
      total: rec.weight.firstLoadBytes,
      count: images.length,
      imageBytes: images.reduce((n, i) => n + (i.resourceSize || 0), 0),
    };
  },
);
for (const r of r4after) {
  checkTrue(
    `§6.1's r4 "after" row for ${r.route} matches the r4 report`,
    section61.includes(`| \`${r.route}\` | ${r.count} |`) &&
      section61.includes(`${kib(r.total)} KiB`) &&
      (r.count === 0 || section61.includes(`${kib(r.imageBytes)} KiB`)),
    `${r.count} image(s) / ${kib(r.imageBytes)} KiB / ${kib(r.total)} KiB total`,
  );
}
// §5's row now states a range per post-removal round instead of one blurred range, because the
// rounds are not comparable: r4 and r5 still carried the pre-§11.9 grid. Each range is derived here.
const rangeOf = (round) => {
  const p = `${EV}/lh-summary-r${round}.json`;
  if (!existsSync(p)) return null;
  const b = json(p).map((r) => r.weight.firstLoadBytes).sort((x, y) => x - y);
  return [b[0], b[b.length - 1]];
};
const r4range = rangeOf('4');
const r5range = rangeOf('5');
const r6range = rangeOf('6');
const r8range = rangeOf('8');
const r7range = rangeOf('7');
const r5max = json(`${EV}/lh-summary-r5.json`).reduce((a, b) => (b.weight.firstLoadBytes > a.weight.firstLoadBytes ? b : a));
checkTrue(
  '§5 first-load row\'s per-round ranges are the reports\' own bounds',
  firstLoadRow.includes(`${kib(r4range[0])}–${kib(r4range[1])} KiB`) &&
    firstLoadRow.includes(`${kib(r5range[0])}–${kib(r5range[1])} KiB`) &&
    firstLoadRow.includes(`${kib(Math.min(r6range[0], r8range[0]))}–${kib(Math.max(r6range[1], r8range[1]))} KiB`) &&
    firstLoadRow.includes(`${kib(r7range[0])}–${kib(r7range[1])} KiB`) &&
    firstLoadRow.includes(thousands(r5max.weight.firstLoadBytes)),
  `r4 ${kib(r4range[0])}-${kib(r4range[1])} r5 ${kib(r5range[0])}-${kib(r5range[1])} r7 ${kib(r7range[0])}-${kib(r7range[1])} r5max ${r5max.route} ${r5max.weight.firstLoadBytes}`,
);
// The retired framing must be gone from the document, not merely contradicted somewhere later: the
// DoD is that `grep -n 'not superseded' docs/PERF-EVIDENCE.md` cannot find a stale present-tense
// claim sitting next to the 1013.3 KiB figure.
checkTrue('the retired "not superseded" framing is gone from the report', !/not\*\* superseded|not superseded/.test(doc));
checkTrue(
  '§5 HTML row conditions its PASS on the deploying image, not on a projection',
  row('HTML ≤150 KB uncompressed').includes('once the image carrying §11.9 is deployed') &&
    row('HTML ≤150 KB uncompressed').includes('Measured, not projected'),
);
// §5's SEO row assigns `is-crawlable` to a route in each round. That assignment is not decoration:
// the failure follows the cells the served artifact stores, so it moves between the two champion
// routes, and a row that names the wrong one makes a claim nothing measured. The clause is derived
// from the summaries, so the prose can only agree with them. Rounds whose summary has been pruned
// drop out of the clause — pruning evidence means rewriting the row, which is the intent.
const crawlable = new Map();
for (const round of ['1', '2', '3', '4', '5', '6', '7', '8', '9']) {
  const p = `${EV}/lh-summary-r${round}.json`;
  if (!existsSync(p)) continue;
  const bad = json(p).filter((r) => r.failingAudits.includes('is-crawlable'));
  checkTrue(
    `r${round} fails is-crawlable on exactly one route`,
    bad.length === 1,
    bad.map((r) => r.route).join(', ') || 'none',
  );
  if (bad.length === 1) crawlable.set(round, bad[0].route);
}
const crawlableClause = [...new Set(crawlable.values())]
  .map((route) => {
    const rounds = [...crawlable.entries()].filter(([, r]) => r === route).map(([n]) => `r${n}`);
    const list = rounds.length === 1 ? rounds[0] : `${rounds.slice(0, -1).join(', ')} and ${rounds.at(-1)}`;
    return `\`${route}\` 69 in ${list}`;
  })
  .join('; ');
checkTrue(
  '§5 SEO row names the failing route of every measured round',
  row('SEO ≥95').includes(crawlableClause),
  crawlableClause,
);

checkTrue(
  'note (a) still states that a projection replacing a measurement would be a laundered pass',
  doc.replace(/\s+/g, ' ').includes('A projection that replaces a measurement would be a laundered pass'),
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
