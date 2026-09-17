#!/usr/bin/env node
/*
 * Static guard for the island bootstrap contract.
 *
 * The two islands broke silently once already: `island-boot.client.ts` called
 * `module.init(root)` while the runtimes exported `initTableIsland` /
 * `initHeatmapIsland`. Everything looked fine - the loader ran, the chunk was
 * fetched - but the entry point was `undefined`, so `data-island-ready` was
 * never set and the table and heatmap were inert.
 *
 * The loader now takes the entry point as a function (`pick`), which makes the
 * mismatch a type error *inside TypeScript files*. The call sites, however,
 * live in Astro `<script>` blocks, and this project has no @astrojs/check, so
 * `tsc --noEmit` never looks at them. This script is that missing check: it
 * reads every `startIslands(...)` call, resolves the module the call imports
 * dynamically, and asserts the entry point the call hands over is really
 * exported by that module.
 *
 * Run from `web/`: node scripts/check-island-contract.mjs
 * Exits non-zero and prints one line per violation. `npm run build` runs it, so
 * both `make web-build` and the CI "Build site" step fail on a violation, and
 * `npm run check:islands` runs it on its own.
 *
 * It also fails when it verified fewer calls than the islands have: a guard that
 * scanned nothing reports success on a broken tree, which is the failure mode
 * it exists to prevent.
 *
 * A call may carry an explicit type argument, `startIslands<Module>(...)`. Those
 * are calls like any other and are checked; the type-argument list is skipped by
 * counting brackets, so it may contain nested brackets, a function type or a
 * comment. The declaration that a list like this reads exactly like
 * (`export function startIslands<Module>(`) is skipped by name.
 */

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const sourceRoot = join(webRoot, 'src');

const walk = (dir) =>
  readdirSync(dir).flatMap((entry) => {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) return walk(path);
    return /\.(astro|ts|mts|js|mjs)$/.test(path) ? [path] : [];
  });

const NAME = 'startIslands';

const skipSpace = (text, index) => {
  let i = index;
  while (i < text.length && /\s/.test(text[i])) i += 1;
  return i;
};

/*
 * `index` is at a `<`. Returns the index just past the `>` that closes it, or
 * -1 when this is not a type-argument list after all. Whitespace, strings,
 * comments, parentheses (a function type: `<(root: HTMLElement) => void>`) and
 * nested angle brackets are all part of one; `;` and braces are not, and a list
 * with one of those in it is not a list this guard can read, so it says so by
 * returning -1 rather than guessing.
 */
const skipTypeArguments = (text, index) => {
  let depth = 0;
  let quote = null;
  let i = index;
  while (i < text.length) {
    const char = text[i];
    if (quote !== null) {
      if (char === '\\') i += 1;
      else if (char === quote) quote = null;
    } else if (char === "'" || char === '"' || char === '`') {
      quote = char;
    } else if (char === '/' && text[i + 1] === '/') {
      const end = text.indexOf('\n', i);
      i = end === -1 ? text.length : end;
    } else if (char === '/' && text[i + 1] === '*') {
      const end = text.indexOf('*/', i);
      i = end === -1 ? text.length : end + 1;
    } else if (char === '<') {
      depth += 1;
    } else if (char === '=' && text[i + 1] === '>') {
      i += 1; // the `>` of an arrow is not a bracket: `<(root: T) => void>`
    } else if (char === '>') {
      depth -= 1;
      if (depth === 0) return i + 1;
      if (depth < 0) return -1;
    } else if (char === ';' || char === '{' || char === '}') {
      return -1;
    }
    i += 1;
  }
  return -1;
};

/*
 * Every `startIslands(...)` call in `text`, as `{ index, open }`.
 *
 * A call may carry an explicit type-argument list: `startIslands<Module>(...)`,
 * or one with nested brackets, parens and a function type inside. The pair of
 * regular expressions this replaces could express neither the nesting nor the
 * balance, and required a `(` immediately after the name, so a call written
 * with a type argument was invisible to both: a call site that carried one and
 * picked a symbol the module does not export passed the guard with exit 0,
 * having counted one call of two. The list is skipped by counting brackets
 * instead, and the declaration (`export function startIslands<Module>(`, which
 * reads exactly like such a call) is skipped by name.
 */
const callSites = (text) => {
  const sites = [];
  const name = new RegExp(`\\b${NAME}\\b`, 'g');
  let match;
  while ((match = name.exec(text)) !== null) {
    if (/function\s+$/.test(text.slice(0, match.index))) continue;
    let i = skipSpace(text, match.index + NAME.length);
    if (text[i] === '<') {
      const end = skipTypeArguments(text, i);
      if (end === -1) continue;
      i = skipSpace(text, end);
    }
    if (text[i] === '(') sites.push({ index: match.index, open: i });
  }
  return sites;
};

/** Handles the one shape the islands use: `(sel, () => import('./x'), (m) => m.y)`. */
const argumentPattern =
  /^\(\s*(?:'([^']*)'|"([^"]*)")\s*,\s*\(\s*\)\s*=>\s*import\s*\(\s*(?:'([^']+)'|"([^"]+)")\s*\)\s*,\s*\(\s*([A-Za-z_$][\w$]*)\s*\)\s*=>\s*([A-Za-z_$][\w$]*)\s*\.\s*([A-Za-z_$][\w$]*)\s*,?\s*\)/;

/** One bootstrap call per island: the table and the heatmap. */
const expectedBootstrapCalls = 2;

const violations = [];
let checked = 0;

for (const file of walk(sourceRoot)) {
  const text = readFileSync(file, 'utf8');

  for (const site of callSites(text)) {
    const where = `${relative(webRoot, file)}:${text.slice(0, site.index).split('\n').length}`;
    const match = argumentPattern.exec(text.slice(site.open));
    if (match === null) {
      violations.push(
        `${where}: startIslands call does not name the entry point it hands over ` +
          '(expected `startIslands(selector, () => import(...), (module) => module.<export>)`)',
      );
      continue;
    }

    checked += 1;
    const selector = match[1] ?? match[2];
    const specifier = match[3] ?? match[4];
    const [moduleName, moduleName2, symbol] = [match[5], match[6], match[7]];

    if (moduleName !== moduleName2) {
      violations.push(
        `${where}: pick argument renames "${moduleName}" to "${moduleName2}"; expected (m) => m.<export>`,
      );
    }
    if (!specifier.startsWith('.')) {
      violations.push(`${where}: dynamic import of "${specifier}" is not a relative module, cannot verify`);
      continue;
    }

    const target = resolve(dirname(file), specifier).replace(/\.ts$/, '');
    let targetFile = null;
    for (const candidate of [`${target}.ts`, `${target}.mts`, `${target}.js`, `${target}.mjs`]) {
      try {
        if (statSync(candidate).isFile()) {
          targetFile = candidate;
          break;
        }
      } catch {
        /* keep looking */
      }
    }
    if (!targetFile) {
      violations.push(`${where}: dynamically imported module "${specifier}" does not exist`);
      continue;
    }

    const targetSource = readFileSync(targetFile, 'utf8');
    const exportPattern = new RegExp(
      `export\\s+(?:async\\s+)?(?:function|const|let|var|class)\\s+${symbol}\\b`,
    );
    const aliasedPattern = new RegExp(`export\\s*\\{[^}]*\\b${symbol}\\b[^}]*\\}`);
    if (!exportPattern.test(targetSource) && !aliasedPattern.test(targetSource)) {
      violations.push(
        `${where}: selector "${selector}" picks "${symbol}", which ` +
          `${relative(webRoot, targetFile)} does not export`,
      );
    }
  }
}

if (violations.length > 0) {
  console.error('island contract: %d violation(s)\n', violations.length);
  for (const violation of violations) console.error('  ' + violation);
  console.error('\nThe loader must call a symbol the runtime module actually exports.');
  process.exit(1);
}

if (checked < expectedBootstrapCalls) {
  console.error('island contract: %d bootstrap call(s) verified, expected %d\n', checked, expectedBootstrapCalls);
  console.error('  Each island bootstraps through one `startIslands(...)` call, and there are two islands');
  console.error('  (table and heatmap). Fewer calls than that means either the calls are gone, or they');
  console.error('  changed shape until this guard stopped recognising them - and a guard that verified');
  console.error('  nothing would otherwise report success on exactly the tree it exists to reject.');
  console.error('  Raise `expectedBootstrapCalls` in this file when you add an island.');
  process.exit(1);
}

console.log(`island contract: ok (${checked} bootstrap call(s) verified)`);
