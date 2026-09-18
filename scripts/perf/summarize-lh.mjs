#!/usr/bin/env node
// Wrap scripts/perf/extract-lh.mjs into the committed lh-summary-rN.json schema.
//
//   node scripts/perf/summarize-lh.mjs 7
//
// extract-lh.mjs knows every threshold and derivation; this adds only the "file" field each record
// in docs/evidence/lh-summary-r*.json carries, so a summary names the reports it came from. Gzip the
// raw reports first if they are to be archived (extract-lh reads .json and .json.gz alike) — the
// committed summaries name the .gz paths.
//
// Regenerating an existing round must reproduce its committed summary byte for byte; verified for
// round 6, which is why this is trusted for the later rounds rather than reimplementing the maths.

import { execFileSync } from 'node:child_process';
import { readdirSync, writeFileSync } from 'node:fs';

const round = process.argv[2];
const dir = process.argv[3] ?? 'docs/evidence';
if (!round) {
  console.error('usage: node scripts/perf/summarize-lh.mjs <round> [evidence-dir]');
  process.exit(2);
}

const files = readdirSync(dir)
  .filter((f) => f.startsWith(`lh-r${round}-`) && (f.endsWith('.json') || f.endsWith('.json.gz')))
  .sort();
if (files.length === 0) {
  console.error(`no raw reports for round ${round} in ${dir}`);
  process.exit(2);
}

const stdout = execFileSync(
  'node',
  ['scripts/perf/extract-lh.mjs', '--json', ...files.map((f) => `${dir}/${f}`)],
  { encoding: 'utf8', maxBuffer: 1 << 28 },
);

const records = JSON.parse(stdout).map((record, i) => ({ file: `${dir}/${files[i]}`, ...record }));
const out = `${dir}/lh-summary-r${round}.json`;
writeFileSync(out, `${JSON.stringify(records, null, 2)}\n`);

const passed = records.filter((r) => r.overall === 'PASS').length;
console.log(`${out}: ${records.length} routes, ${passed} PASS`);
for (const r of records.filter((x) => x.overall !== 'PASS')) {
  const bad = Object.entries(r.grades)
    .filter(([, g]) => g !== 'PASS')
    .map(([k, g]) => `${k}=${g}`);
  console.log(`  ${r.route} ${r.overall}: ${bad.join(' ')}`);
}
