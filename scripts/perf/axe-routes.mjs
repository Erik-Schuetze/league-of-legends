#!/usr/bin/env node
// Run axe-core (WCAG 2.1 A/AA tags) against the served routes and record raw results.
//
//   node scripts/perf/axe-routes.mjs --base http://127.0.0.1:18921 --out docs/evidence / /about/
//
// axe-core and puppeteer-core are expected in PERF_TOOLS_DIR (default /tmp/a11y-tools),
// never in package.json. Chrome is taken from CHROME_PATH.

import { createRequire } from 'node:module';
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import path from 'node:path';

const argv = process.argv.slice(2);
function flag(name, fallback) {
  const i = argv.indexOf(name);
  return i === -1 ? fallback : argv[i + 1];
}
const TOOLS = process.env.PERF_TOOLS_DIR || '/tmp/a11y-tools';
const CHROME = process.env.CHROME_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const BASE = flag('--base', 'http://127.0.0.1:18921');
const OUT = flag('--out', 'docs/evidence');
const ROUTES = argv.filter((a, i) => a.startsWith('/') && !argv[i - 1]?.startsWith('--'));

if (ROUTES.length === 0) {
  console.error('usage: node scripts/perf/axe-routes.mjs --base <url> --out <dir> /route/ ...');
  process.exit(2);
}

const require = createRequire(path.join(TOOLS, 'package.json'));
const puppeteer = require('puppeteer-core');
const AXE_SOURCE = readFileSync(path.join(TOOLS, 'node_modules/axe-core/axe.min.js'), 'utf8');

mkdirSync(OUT, { recursive: true });

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: 'new',
  args: ['--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage'],
});

const summary = [];
for (const route of ROUTES) {
  const slug = route.replace(/^\/|\/$/g, '').replace(/\//g, '_') || 'home';
  const page = await browser.newPage();
  await page.setViewport({ width: 412, height: 915, deviceScaleFactor: 2.625, isMobile: true, hasTouch: true });
  await page.goto(BASE + route, { waitUntil: 'networkidle0', timeout: 60000 });
  await page.evaluate(AXE_SOURCE);
  const res = await page.evaluate(() => axe.run(document, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] },
    resultTypes: ['violations', 'incomplete'],
  }));
  const file = path.join(OUT, `axe-${slug}.json`);
  writeFileSync(file, JSON.stringify(res, null, 1));

  const bad = res.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
  const incompleteBad = res.incomplete.filter((v) => v.impact === 'serious' || v.impact === 'critical');
  const row = {
    route,
    file,
    axeVersion: res.testEngine?.version,
    rulesEvaluated: res.passes.length + res.violations.length + res.incomplete.length + res.inapplicable.length,
    violations: res.violations.length,
    seriousOrCritical: bad.length,
    incomplete: res.incomplete.length,
    incompleteSeriousOrCritical: incompleteBad.map((v) => `${v.id}(${v.nodes.length})`).join(' '),
    badRules: bad.map((v) => `${v.impact}:${v.id}(${v.nodes.length})`).join(' '),
  };
  summary.push(row);
  console.log(`${route.padEnd(24)} violations=${row.violations} serious+critical=${row.seriousOrCritical} `
    + `incomplete=${row.incomplete} [${row.incompleteSeriousOrCritical}]${row.badRules ? ' BAD ' + row.badRules : ''}`);
  await page.close();
}

writeFileSync(path.join(OUT, 'axe-summary.json'), JSON.stringify(summary, null, 2));
await browser.close();

const failed = summary.filter((r) => r.seriousOrCritical > 0);
console.log(`\naxe: ${summary.length} routes, ${failed.length} with serious/critical violations (axe ${summary[0]?.axeVersion})`);
process.exit(failed.length ? 1 : 0);
