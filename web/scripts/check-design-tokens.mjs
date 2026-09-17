#!/usr/bin/env node
/*
 * Design-token contract check.
 *
 * The ported visual language from erik-schuetze.dev is held together by three
 * promises that are easy to break by accident and expensive to notice:
 *
 *   1. docs/contracts.md section 3 freezes the palette that tokens.css declares
 *      at :root, and every other file reads it instead of hard-coding a colour.
 *      A typo in a `var()` name is invisible in CSS - the declaration is simply
 *      dropped - so a dangling reference silently un-styles a page.
 *   2. The look is square corners, a 2px accent ring and a *solid* offset
 *      shadow. A blurred shadow reintroduces the soft drop shadow the port
 *      removed.
 *   3. The page field must not clip its own content horizontally: upstream
 *      carries `overflow-x: hidden` to hide an off-canvas decoration, and copied
 *      here it would cut the right-hand columns off a wide stat table.
 *   4. layouts/fallback/base.css is imported after tokens.css, so any bare
 *      element selector in it beats the design system on source order. That has
 *      already cost the page field, the navbar clearance and the focus ring
 *      once; the check refuses to let it happen again.
 *
 * This check asserts all four plus the accessibility items the port had to fix
 * rather than inherit (reduced motion, a visible focus ring, tabular figures),
 * the published contrast ratios recomputed from the token values, and the
 * self-hosted font set. It reads the source, not the build, so it runs in the
 * same second as the edit that broke it.
 *
 * Run it directly, or via `npm run check:tokens`.
 */

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const srcDir = path.join(webRoot, 'src');
const stylesDir = path.join(srcDir, 'styles');

/** The thirteen names docs/contracts.md section 3 freezes, with their values. */
const FROZEN_TOKENS = {
  '--bg-color': '#efdbbf',
  '--bg-light': '#f1eae0',
  '--primary-color': '#0b162a',
  '--accent-color': '#1b4bc6',
  '--text-color': '#242a2b',
  '--grid-line': 'rgba(27, 75, 202, 0.06)',
  '--grid-size': '27px',
  '--border-width': '2px',
  '--shadow-offset-sm': '8px',
  '--shadow-offset-md': '12px',
  '--radius': '0',
};

/** Families with no literal value to compare: presence is the contract. */
const FROZEN_FAMILIES = ['--font-heading', '--font-body', '--font-mono'];

const failures = [];
const notes = [];

function fail(message) {
  failures.push(message);
}

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const full = path.join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, out);
    else out.push(full);
  }
  return out;
}

/** Comments are not code: a token named in prose is not a token in use. */
function stripComments(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, '');
}

const files = walk(srcDir);
const cssFiles = files.filter((f) => f.endsWith('.css'));
const astroFiles = files.filter((f) => f.endsWith('.astro'));

/**
 * `.astro` files carry a scoped `<style>` block, and `--name:` inside one is a
 * declaration like any other. Both file kinds are therefore read the same way.
 */
const sources = new Map();
for (const file of [...cssFiles, ...astroFiles]) {
  sources.set(path.relative(webRoot, file), stripComments(readFileSync(file, 'utf8')));
}

// ---------------------------------------------------------------------------
// 1. The frozen palette in tokens.css, and nothing beside it.
// ---------------------------------------------------------------------------

const tokensCss = stripComments(readFileSync(path.join(stylesDir, 'tokens.css'), 'utf8'));
const rootBlock = tokensCss.match(/:root\s*\{([\s\S]*?)\}/);
if (!rootBlock) {
  fail('src/styles/tokens.css has no :root block');
} else {
  const declared = new Map();
  for (const line of rootBlock[1].split('\n')) {
    const match = line.match(/^\s*(--[a-z0-9-]+)\s*:\s*([^;]+);/);
    if (match) declared.set(match[1], match[2].trim());
  }

  const expected = [...Object.keys(FROZEN_TOKENS), ...FROZEN_FAMILIES].sort();
  const actual = [...declared.keys()].sort();

  for (const name of expected) {
    if (!declared.has(name)) fail(`tokens.css :root is missing the frozen token ${name}`);
  }
  for (const name of actual) {
    if (!expected.includes(name)) {
      fail(
        `tokens.css :root declares ${name}, which docs/contracts.md section 3 does not ` +
          'freeze; the extension layer belongs in global.css',
      );
    }
  }
  for (const [name, value] of Object.entries(FROZEN_TOKENS)) {
    if (declared.has(name) && declared.get(name).toLowerCase() !== value) {
      fail(`tokens.css ${name} is ${declared.get(name)}, expected ${value}`);
    }
  }
  notes.push(`tokens.css :root declares ${actual.length} frozen names (expected ${expected.length})`);
}

// ---------------------------------------------------------------------------
// 2. Every var(--x) used anywhere resolves somewhere.
// ---------------------------------------------------------------------------

const defined = new Set();
for (const css of sources.values()) {
  for (const match of css.matchAll(/(--[a-z0-9-]+)\s*:/g)) defined.add(match[1]);
}

const dangling = new Map();
for (const [file, css] of sources) {
  for (const match of css.matchAll(/var\(\s*(--[a-z0-9-]+)/g)) {
    if (!defined.has(match[1])) {
      if (!dangling.has(match[1])) dangling.set(match[1], new Set());
      dangling.get(match[1]).add(file);
    }
  }
}
for (const [name, where] of dangling) {
  fail(`var(${name}) is referenced but never declared - used in ${[...where].sort().join(', ')}`);
}
notes.push(`${defined.size} custom properties declared across src/, every var() reference resolves`);

// ---------------------------------------------------------------------------
// 3. Hard-edged idiom: solid offset shadows only, square corners, no clipping.
// ---------------------------------------------------------------------------

const BLUR_ALLOWED = [
  // The masthead's frosted panel is upstream's own backdrop-filter, not a
  // shadow; the spec forbids blurred shadows, not translucency.
  'backdrop-filter',
];

for (const [file, css] of sources) {
  for (const match of css.matchAll(/box-shadow\s*:\s*([^;}]+)/g)) {
    if (/\bblur\(/.test(match[1])) {
      fail(`${file}: box-shadow uses blur("${match[1].trim()}") - the system is hard-edged`);
    }
  }
  // `outline` and `box-shadow` take different value grammars, and a ring token
  // written for one is silently dropped by the other, which is how a keyboard
  // focus indicator disappears without a single console warning.
  for (const match of css.matchAll(/(?:^|[;{]\s*)outline\s*:\s*([^;}]+)/gm)) {
    const value = match[1].trim();
    // Three or more leading length tokens is box-shadow geometry; `outline: 0`
    // and `outline: none` are ordinary and stay allowed.
    if (/^-?[\d.][^\s]*(\s+-?[\d.][^\s]*){2,}/.test(value)) {
      fail(`${file}: outline: ${value} uses box-shadow syntax, which CSS drops silently`);
    }
  }
  // `backdrop-filter` is excluded by the leading separator in the pattern:
  // upstream's frosted masthead is part of the look, and the spec forbids
  // blurred *shadows*, not translucency.
  for (const match of css.matchAll(/(?:^|[;{]\s*)filter\s*:\s*([^;}]+)/gm)) {
    if (/\bblur\(/.test(match[1]) && !BLUR_ALLOWED.some((ok) => file.includes(ok))) {
      fail(`${file}: filter: ${match[1].trim()} - the system is hard-edged`);
    }
  }
  if (/overflow(-x)?\s*:\s*hidden/.test(css) && /^(src\/styles|src\/layouts)\//.test(file)) {
    const hits = [...css.matchAll(/([^{}]+)\{[^}]*overflow(-x)?\s*:\s*hidden[^}]*\}/g)].map((m) =>
      m[1].trim().split('\n').pop().trim(),
    );
    for (const selector of hits) {
      // A scroll box, a clipped decoration or a visually-hidden node is fine;
      // clipping the page field is what cuts a wide table in half.
      if (/^(html|body|\.ds-container)$/.test(selector)) {
        fail(`${file}: ${selector} { overflow-x: hidden } clips wide stat tables`);
      }
    }
  }
}

// Radius is zero everywhere bar the one documented button exception.
const radiusOk = new Set(['--radius-btn']);
for (const [file, css] of sources) {
  for (const match of css.matchAll(/border-radius\s*:\s*([^;}]+)/g)) {
    const value = match[1].trim();
    if (value === 'var(--radius-btn)' || value === 'var(--radius)' || value === '0') continue;
    fail(`${file}: border-radius: ${value} - the system has square corners`);
  }
}

// ---------------------------------------------------------------------------
// 4. The accessibility items the port fixes instead of inherits.
// ---------------------------------------------------------------------------

const globalCss = sources.get('src/styles/global.css') ?? '';
if (!/@media\s*\(prefers-reduced-motion:\s*reduce\)/.test(globalCss)) {
  fail('global.css has no prefers-reduced-motion: reduce block');
}
if (!/:focus-visible/.test(globalCss)) {
  fail('global.css has no explicit :focus-visible rule');
}
if (!/font-variant-numeric:\s*tabular-nums/.test(globalCss)) {
  fail('global.css does not set tabular numerals on table cells');
}

// The accent ring is 6.13:1 on the surface but 2.47:1 on the navy masthead and
// footer, so a dark surface must be given the light ring (WCAG 2.2 SC 1.4.11).
if (!/--accent-on-dark/.test(globalCss) || !/outline-color:\s*var\(--accent-on-dark\)/.test(globalCss)) {
  fail('global.css does not re-colour the focus ring inside a dark surface');
}

// ---------------------------------------------------------------------------
// 5. Contrast, recomputed from the token values on every build.
// ---------------------------------------------------------------------------

/*
 * docs/design-system.md publishes a contrast ratio for every foreground/
 * background pair the system introduces, and the two the port had to fix are
 * the ones easiest to lose again: the accent as link ink (6.13:1 on a surface,
 * AA but not AAA) and the light focus ring on the navy masthead and footer
 * (WCAG 2.2 SC 1.4.11 wants 3:1 for a focus indicator; the accent itself is
 * only 2.47:1 there). A palette edit that quietly drops one of these below its
 * threshold fails the build instead of shipping.
 */

const colorTokens = new Map();
for (const css of [tokensCss, globalCss]) {
  for (const match of css.matchAll(/(--[a-z0-9-]+)\s*:\s*(#[0-9a-fA-F]{3,8})\s*;/g)) {
    colorTokens.set(match[1], match[2].toLowerCase());
  }
  // One level of aliasing is all the palette uses (`--tier-ink: var(--...`).
  for (const match of css.matchAll(/(--[a-z0-9-]+)\s*:\s*var\((--[a-z0-9-]+)\)\s*;/g)) {
    const target = colorTokens.get(match[2]);
    if (target) colorTokens.set(match[1], target);
  }
}

function luminance(hex) {
  const full = hex.length === 4 ? `#${[...hex.slice(1)].map((c) => c + c).join('')}` : hex;
  const [r, g, b] = [1, 3, 5].map((i) => Number.parseInt(full.slice(i, i + 2), 16) / 255);
  const channel = (c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

function contrast(fg, bg) {
  const [a, b] = [luminance(fg), luminance(bg)].sort((x, y) => y - x);
  return (a + 0.05) / (b + 0.05);
}

/** `[foreground, background, minimum, what the minimum protects]` */
const CONTRAST_PAIRS = [
  ['--text-color', '--bg-color', 4.5, 'body copy on the page field'],
  ['--text-color', '--bg-light', 4.5, 'body copy on a raised surface'],
  ['--primary-color', '--bg-color', 4.5, 'headings on the page field'],
  ['--primary-color', '--bg-light', 4.5, 'headings on a raised surface'],
  ['--accent-color', '--bg-color', 4.5, 'link ink on the page field'],
  ['--accent-color', '--bg-light', 4.5, 'link ink on a raised surface'],
  ['--text-muted', '--bg-color', 4.5, 'muted metadata on the page field'],
  ['--text-muted', '--bg-light', 4.5, 'muted metadata on a raised surface'],
  ['--bg-light', '--primary-color', 4.5, 'inverted text on the masthead and footer'],
  ['--text-on-dark-muted', '--primary-color', 4.5, 'muted inverted text'],
  ['--accent-on-dark', '--primary-color', 4.5, 'accent ink inside a dark surface'],
  ['--accent-on-dark', '--primary-color', 3, 'the focus ring inside a dark surface (SC 1.4.11)'],
  ['--accent-color', '--bg-color', 3, 'the focus ring on the page field (SC 1.4.11)'],
  ['--accent-color', '--bg-light', 3, 'the focus ring on a raised surface (SC 1.4.11)'],
  ['--tier-ink', '--tier-splus-bg', 4.5, 'the tier letter on the darkest grade'],
  ['--tier-ink', '--tier-d-bg', 4.5, 'the tier letter on the lightest grade'],
  ['--tier-ink', '--dev-up-bg-strong', 4.5, 'a heatmap figure on the strong deviation fill'],
  ['--dev-up-fg', '--bg-color', 4.5, 'an above-even figure on the page field'],
  ['--dev-up-fg', '--bg-light', 4.5, 'an above-even figure on a raised surface'],
];

const unreadable = [];
for (const [fgName, bgName, minimum, what] of CONTRAST_PAIRS) {
  const fg = colorTokens.get(fgName);
  const bg = colorTokens.get(bgName);
  if (!fg || !bg) {
    fail(`contrast pair ${fgName} on ${bgName} cannot be checked: no literal value found`);
    continue;
  }
  const ratio = contrast(fg, bg);
  const rounded = ratio.toFixed(2);
  if (ratio < minimum) {
    unreadable.push(`${fgName} on ${bgName} is ${rounded}:1, needs ${minimum}:1 (${what})`);
  } else {
    notes.push(`contrast ${rounded}:1  ${fgName} on ${bgName} (${what})`);
  }
}
for (const message of unreadable) fail(message);

// ---------------------------------------------------------------------------
// 6. The fallback stylesheet may not restate the design system.
// ---------------------------------------------------------------------------

/*
 * BaseLayout imports tokens.css and then layouts/fallback/base.css, so an
 * equal-specificity rule in the second sheet wins on source order. That is how
 * `body { background: …; padding: … }` flattened the page field and the navbar
 * clearance on every route, and how `a:focus-visible { outline: 0 }` replaced
 * the focus ring site-wide. The file's own header says it only ever targets its
 * own class names, so hold it to that: a selector with no class or id in it
 * cannot be about a fallback renderer.
 */

const fallbackBase = sources.get('src/layouts/fallback/base.css') ?? '';
if (!fallbackBase) fail('src/layouts/fallback/base.css is missing, so the check cannot see it');
for (const block of fallbackBase.matchAll(/([^{}]+)\{/g)) {
  for (const selector of block[1].split(',')) {
    const trimmed = selector.trim();
    if (!trimmed || trimmed.startsWith('@') || trimmed === ':root') continue;
    if (trimmed.includes('.') || trimmed.includes('#') || trimmed.startsWith(':')) continue;
    fail(
      `layouts/fallback/base.css styles the bare "${trimmed}" element, which loads after the ` +
        'design system and silently overrides it; scope it to a fallback class',
    );
  }
}

// ---------------------------------------------------------------------------
// 7. Fonts: self-hosted, subset, and actually present.
// ---------------------------------------------------------------------------

const fontsCss = sources.get('src/styles/fonts.css') ?? '';
if (/fonts\.(googleapis|gstatic)\.com/.test(fontsCss)) {
  fail('fonts.css fetches from Google Fonts at runtime; the OFL faces are self-hosted');
}
if (/\blocal\(/.test(fontsCss)) {
  fail('fonts.css declares a local() source, so rendering depends on the reader\'s machine');
}
const faces = [...fontsCss.matchAll(/@font-face\s*\{[\s\S]*?\}/g)];
if (faces.length === 0) fail('fonts.css declares no @font-face');
for (const face of faces) {
  const url = face[0].match(/url\(['"]?([^'")]+)['"]?\)/);
  if (!url) continue;
  const target = url[1].startsWith('/') ? path.join(webRoot, 'public', url[1]) : null;
  if (target && !existsSync(target)) fail(`fonts.css references ${url[1]}, which is not in web/public`);
  if (!/font-display:\s*swap/.test(face[0])) fail(`@font-face for ${url[1]} has no font-display: swap`);
}
notes.push(
  `${faces.length} @font-face rules self-hosted with font-display: swap and no local() fallback`,
);

// OFL 1.1 requires the licence to ship with the files, so every family the
// stylesheet declares must have its notice beside it in web/public/fonts/.
const fontDir = path.join(webRoot, 'public', 'fonts');
const licences = existsSync(fontDir)
  ? readdirSync(fontDir)
      .filter((name) => /^OFL-.*\.txt$/.test(name))
      .map((name) => [name, readFileSync(path.join(fontDir, name), 'utf8')])
  : [];
const families = new Set(
  [...fontsCss.matchAll(/font-family\s*:\s*['"]([^'"]+)['"]/g)].map((match) => match[1]),
);
for (const family of families) {
  const notice = licences.find(([, text]) => text.includes(family));
  if (!notice) fail(`web/public/fonts has no OFL notice for the ${family} family`);
  else notes.push(`OFL notice ${notice[0]} covers ${family}`);
}
// And it must be the OFL this port relies on, not some other licence text.
for (const [name, text] of licences) {
  if (!/SIL OPEN FONT LICENSE Version 1\.1/.test(text)) {
    fail(`web/public/fonts/${name} is not an OFL 1.1 notice`);
  }
}

// No third-party origin may be reached from the stylesheets at all.
for (const [file, css] of sources) {
  for (const match of css.matchAll(/https?:\/\/(?!lol\.erik-schuetze\.dev)[^'")\s]+/g)) {
    if (/\.(woff2?|ttf|otf|css)/.test(match[0]) || /fonts\./.test(match[0])) {
      fail(`${file} loads ${match[0]} from a third party`);
    }
  }
}

// ---------------------------------------------------------------------------

for (const note of notes) console.log(`ok   ${note}`);
if (failures.length > 0) {
  console.error(`\ndesign tokens: ${failures.length} failure(s)\n`);
  for (const message of failures) console.error(`  - ${message}`);
  process.exit(1);
}
console.log('\ndesign tokens: ok');
