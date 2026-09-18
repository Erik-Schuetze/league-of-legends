#!/usr/bin/env node
/**
 * Contrast audit for docs/frontend/tokens.css.
 *
 * Parses the token file, resolves var() references, then checks every
 * foreground/background pair the design actually uses against WCAG 2.1.
 * Run from anywhere:  node docs/frontend/tools/contrast-audit.mjs
 *
 * Exits non-zero when a pair that must pass AA does not, so it can be wired
 * into a check lane later.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const tokensPath = join(here, "..", "tokens.css");
const css = readFileSync(tokensPath, "utf8");

/** Collect `--name: value;` declarations, ignoring the override blocks at the
 *  end of the file. */
function readTokens(source) {
  const root = source.slice(0, source.indexOf("@media"));
  const out = new Map();
  const re = /(--[a-z0-9-]+)\s*:\s*([^;]+);/gi;
  for (const m of root.matchAll(re)) out.set(m[1], m[2].trim());
  return out;
}

const tokens = readTokens(css);

function resolve(name, depth = 0) {
  if (depth > 10) throw new Error(`var cycle at ${name}`);
  const raw = tokens.get(name);
  if (raw === undefined) throw new Error(`unknown token ${name}`);
  const v = raw.match(/^var\((--[a-z0-9-]+)\)$/i);
  return v ? resolve(v[1], depth + 1) : raw;
}

const hex = (name) => resolve(name);

function srgbToLin(c) {
  c /= 255;
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

function parse(value) {
  const h = String(value).trim();
  const m = h.match(/^#([0-9a-f]{6})$/i);
  if (m) {
    const n = parseInt(m[1], 16);
    return [(n >> 16) & 255, (n >> 8) & 255, n & 255, 1];
  }
  const rgb = h.match(/^rgb\(\s*(\d+)\s+(\d+)\s+(\d+)\s*(?:\/\s*([\d.]+)\s*)?\)$/i);
  if (rgb) return [+rgb[1], +rgb[2], +rgb[3], rgb[4] === undefined ? 1 : +rgb[4]];
  throw new Error(`unsupported colour value: ${value}`);
}

/** Composite an alpha colour over an opaque one. */
function over(fg, bg) {
  const [r, g, b, a] = parse(fg);
  const [br, bgc, bb] = parse(bg);
  const f = (c, d) => Math.round(c * a + d * (1 - a));
  return `rgb(${f(r, br)} ${f(g, bgc)} ${f(b, bb)})`;
}

function luminance(value) {
  const [r, g, b] = parse(value);
  return 0.2126 * srgbToLin(r) + 0.7152 * srgbToLin(g) + 0.0722 * srgbToLin(b);
}

function ratio(fg, bg) {
  const a = luminance(fg);
  const b = luminance(bg);
  const [hi, lo] = a > b ? [a, b] : [b, a];
  return (hi + 0.05) / (lo + 0.05);
}

function verdict(r, need) {
  if (!need) return "exempt";
  if (need === "ui") return r >= 3 ? "AA (1.4.11, 3:1)" : "FAIL";
  if (r >= 7) return "AAA";
  if (r >= 4.5) return "AA";
  return "FAIL";
}

tokens.set("--sig-up-tint-over-cream", over(resolve("--sig-up-tint"), resolve("--c-surface")));
tokens.set("--sig-down-tint-over-cream", over(resolve("--sig-down-tint"), resolve("--c-surface")));

/* Every pair the interface ships. Requirement is "text" (4.5:1), "ui"
   (3:1, non-text boundaries per 1.4.11) or null (documented exemption). */
const pairs = [
  ["body text on cream", "--c-text", "--c-surface", "text"],
  ["body text on sand", "--c-text", "--c-sand", "text"],
  ["strong text on cream", "--c-text-strong", "--c-surface", "text"],
  ["strong text on sand", "--c-text-strong", "--c-sand", "text"],
  ["muted text on cream", "--c-text-muted", "--c-surface", "text"],
  ["muted text on sand", "--c-text-muted", "--c-sand", "text"],
  ["accent text on cream", "--c-text-accent", "--c-surface", "text"],
  ["accent text on sand", "--c-text-accent", "--c-sand", "text"],
  ["accent-strong text on sand", "--c-text-accent-strong", "--c-sand", "text"],
  ["cream on accent fill", "--c-text-on-accent", "--c-blue", "text"],
  ["cream on ink fill", "--c-text-on-ink", "--c-surface-inverse", "text"],
  ["cream on win fill", "--c-text-on-up", "--sig-up", "text"],
  ["cream on loss fill", "--c-text-on-down", "--sig-down", "text"],
  ["win text on cream", "--sig-up", "--c-surface", "text"],
  ["win text on sand", "--sig-up", "--c-sand", "text"],
  ["win text on tinted row", "--sig-up", "--sig-up-tint-over-cream", "text"],
  ["loss text on cream", "--sig-down", "--c-surface", "text"],
  ["loss text on sand", "--sig-down", "--c-sand", "text"],
  ["loss text on tinted row", "--sig-down", "--sig-down-tint-over-cream", "text"],
  ["unknown text on cream", "--sig-unknown", "--c-surface", "text"],
  ["control border on cream", "--c-control-border", "--c-surface", "ui"],
  ["control border on sand", "--c-control-border", "--c-sand", "ui"],
  ["disabled text on cream", "--c-text-disabled", "--c-surface", null],
  ["hairline on cream", "--c-rule", "--c-surface", null],
];

console.log("| pair | ratio | verdict |");
console.log("| --- | --- | --- |");
let failures = 0;
for (const [label, fg, bg, need] of pairs) {
  const r = ratio(hex(fg), hex(bg));
  const v = verdict(r, need);
  if (v === "FAIL") failures++;
  console.log(`| ${label} | ${r.toFixed(2)}:1 | ${v} |`);
}

/* Luminance separation: the evidence for "colour never carries meaning alone". */
console.log("\n| hue pair | luminance contrast |");
console.log("| --- | --- |");
for (const [a, b] of [
  ["--c-blue", "--sig-up"],
  ["--c-blue", "--sig-down"],
  ["--sig-up", "--sig-down"],
]) {
  console.log(`| ${a} vs ${b} | ${ratio(hex(a), hex(b)).toFixed(2)}:1 |`);
}

if (failures) {
  console.error(`\n${failures} required pair(s) fail their threshold.`);
  process.exit(1);
}
console.log("\nAll required pairs pass AA.");
