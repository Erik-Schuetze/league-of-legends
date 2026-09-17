#!/usr/bin/env node
// Check what the island budget in plan.md 7.4 actually buys: how many islands a page
// has, how they are loaded, and whether they boot without console errors.
//
//   node scripts/perf/island-runtime.mjs --base http://127.0.0.1:18921 /tier-list/mid/
//
// A page whose island script is requested with 200 but never initializes passes a
// static "islands <= 2, both deferred" check while delivering no enhancement, so
// this records the runtime outcome alongside the markup facts.

import { createRequire } from 'node:module';
import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';

const argv = process.argv.slice(2);
const flag = (n, f) => (argv.indexOf(n) === -1 ? f : argv[argv.indexOf(n) + 1]);
const TOOLS = process.env.PERF_TOOLS_DIR || '/tmp/a11y-tools';
const CHROME = process.env.CHROME_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const BASE = flag('--base', 'http://127.0.0.1:18921');
const OUT = flag('--out', '.agent-artifacts');
const ROUTES = argv.filter((a, i) => a.startsWith('/') && !argv[i - 1]?.startsWith('--'));

const require = createRequire(path.join(TOOLS, 'package.json'));
const puppeteer = require('puppeteer-core');
mkdirSync(OUT, { recursive: true });

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: 'new',
  args: ['--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage'],
});

const results = [];
for (const route of ROUTES) {
  const page = await browser.newPage();
  await page.setViewport({ width: 412, height: 915, isMobile: true, hasTouch: true });
  const consoleErrors = [];
  const pageErrors = [];
  const scriptResponses = [];
  page.on('console', (m) => { if (m.type() === 'error') consoleErrors.push(m.text()); });
  page.on('pageerror', (e) => pageErrors.push(String(e)));
  page.on('response', (r) => {
    if (r.url().endsWith('.js')) scriptResponses.push({ url: r.url(), status: r.status() });
  });

  await page.goto(BASE + route, { waitUntil: 'networkidle0', timeout: 60000 });
  const before = await page.evaluate(() => Array.from(document.querySelectorAll('[data-island]'))
    .map((el) => ({ kind: el.getAttribute('data-island'), booted: el.getAttribute('data-island-booted'), ready: el.getAttribute('data-island-ready') })));
  await new Promise((r) => setTimeout(r, 3000));
  const after = await page.evaluate(() => {
    const el = document.querySelector('[data-island]');
    return {
      islands: Array.from(document.querySelectorAll('[data-island]')).map((n) => ({
        kind: n.getAttribute('data-island'),
        attrs: Array.from(n.attributes).map((a) => a.name),
      })),
      scripts: Array.from(document.querySelectorAll('script[src]')).map((s) => ({
        src: s.getAttribute('src'), type: s.getAttribute('type') ?? null,
        defer: s.hasAttribute('defer'), async: s.hasAttribute('async'),
      })),
      firstIslandChildCount: el ? el.querySelectorAll('*').length : null,
      tableRows: document.querySelectorAll('table tbody tr').length,
    };
  });

  const record = { route, before, after, consoleErrors, pageErrors, scriptResponses };
  results.push(record);
  console.log(`${route.padEnd(22)} islands=${before.length} booted=${before.map((b) => b.booted).join(',')} `
    + `ready=${before.map((b) => b.ready).join(',')} rows=${after.tableRows} consoleErrors=${consoleErrors.length} pageErrors=${pageErrors.length}`);
  if (consoleErrors.length) consoleErrors.slice(0, 3).forEach((e) => console.log(`    console: ${e.slice(0, 160)}`));
  if (pageErrors.length) pageErrors.slice(0, 3).forEach((e) => console.log(`    pageerror: ${e.slice(0, 160)}`));
  await page.close();
}

writeFileSync(path.join(OUT, 'island-runtime.json'), JSON.stringify(results, null, 2));
await browser.close();
