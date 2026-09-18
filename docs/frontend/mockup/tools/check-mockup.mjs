#!/usr/bin/env node
/**
 * Mockup verification: renders every route from the static build in headless
 * Chrome, writes the review screenshots, and asserts the contract in
 * docs/frontend/README.md §12.
 *
 *   node tools/check-mockup.mjs               # all routes, all widths
 *   node tools/check-mockup.mjs --no-shots    # assertions only
 *   node tools/check-mockup.mjs --route=explore
 *
 * Requires `npm run build` first: the assertions run against `build/`, which is
 * exactly what a deploy serves. No dev server, no network beyond loopback.
 */

import { createServer } from "node:http";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { extname, join, resolve } from "node:path";
import puppeteer from "puppeteer-core";

const ROOT = resolve(import.meta.dirname, "..");
const BUILD = join(ROOT, "build");
const SHOTS = resolve(ROOT, "..", "screenshots", "mockups");

const CHROME =
  process.env.CHROME_PATH ??
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

const WIDTHS = [
  { name: "desktop", width: 1440, height: 960, shot: true },
  { name: "tablet", width: 768, height: 1024, shot: false },
  { name: "mobile", width: 480, height: 900, shot: true },
];

const ROUTES = [
  { slug: "home", path: "/" },
  { slug: "explore", path: "/explore" },
  { slug: "gallery", path: "/gallery" },
  { slug: "about", path: "/about" },
  { slug: "disclaimer", path: "/disclaimer" },
  { slug: "terms", path: "/legal/terms" },
  { slug: "privacy", path: "/legal/privacy" },
];

/** Max screenshot height. Chrome cannot rasterise an arbitrarily tall page. */
const MAX_SHOT_HEIGHT = 12000;

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".woff2": "font/woff2",
  ".txt": "text/plain; charset=utf-8",
  ".map": "application/json; charset=utf-8",
};

const args = process.argv.slice(2);
const shotsEnabled = !args.includes("--no-shots");
const only = args.find((a) => a.startsWith("--route="))?.slice("--route=".length);

/* -- static server ---------------------------------------------------------
   ~40 lines instead of a dependency, and it serves the real build output. */

async function serve() {
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, "http://localhost");
    let pathname = decodeURIComponent(url.pathname);
    if (pathname.endsWith("/")) pathname += "index.html";
    const base = join(BUILD, pathname.replace(/^\/+/, ""));
    /* /explore and /legal/terms are directories on disk but are requested
       without a trailing slash, so fall back to their index.html. */
    const candidates = [base, join(base, "index.html")];
    if (!base.startsWith(BUILD)) {
      res.writeHead(403).end("forbidden");
      return;
    }
    for (const file of candidates) {
      try {
        const body = await readFile(file);
        res.writeHead(200, { "content-type": MIME[extname(file)] ?? "application/octet-stream" });
        res.end(body);
        return;
      } catch {
        continue;
      }
    }
    res.writeHead(404, { "content-type": "text/plain" }).end(`not found: ${pathname}`);
  });
  await new Promise((done) => server.listen(0, "127.0.0.1", done));
  const { port } = server.address();
  return { origin: `http://127.0.0.1:${port}`, close: () => new Promise((d) => server.close(d)) };
}

/* -- check bookkeeping ---------------------------------------------------- */

const results = [];
function check(ok, scope, name, detail = "") {
  results.push({ ok: Boolean(ok), scope, name, detail: ok ? "" : detail });
}
function expectEqual(actual, expected, scope, name, detail = "") {
  check(
    actual === expected,
    scope,
    name,
    `expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}${detail ? ` — ${detail}` : ""}`,
  );
}
function expectAtLeast(actual, minimum, scope, name) {
  check(actual >= minimum, scope, name, `expected >= ${minimum}, got ${actual}`);
}

/* -- per-page audit ------------------------------------------------------- */

/** Runs in the page. Returns raw measurements; all judgement stays in Node. */
function auditPage() {
  const q = (selector) => Array.from(document.querySelectorAll(selector));
  const visible = (el) => el.offsetParent !== null && el.getBoundingClientRect().height > 0;

  const ids = q("[id]").map((el) => el.id);
  const duplicateIds = [...new Set(ids.filter((id, i) => ids.indexOf(id) !== i))];

  const brokenAnchors = q('a[href^="#"]')
    .map((a) => a.getAttribute("href"))
    .filter((href) => href.length > 1 && !document.getElementById(decodeURIComponent(href.slice(1))));

  const emptyAnchors = q('a[href=""], a[href="#"]').length;

  const missingLabelTargets = q("[aria-labelledby]")
    .filter((el) =>
      el
        .getAttribute("aria-labelledby")
        .split(/\s+/)
        .every((id) => !document.getElementById(id)),
    )
    .map((el) => el.tagName + "." + el.className);

  /* A statistic without a sample or a note is the failure this site exists to
     avoid: "141" alone, with no statement of what was counted. A tile that is
     still loading is exempt: it has no number yet, only a placeholder. */
  const bareStats = q(".stat")
    .filter((el) => el.dataset.loading !== "true")
    .filter((el) => !el.querySelector(".n") && !el.querySelector(".note"))
    .map((el) => el.querySelector(".label")?.textContent?.trim() ?? "(unlabelled)");

  const resources = performance
    .getEntriesByType("resource")
    .map((entry) => {
      try {
        return new URL(entry.name).origin;
      } catch {
        return "inline";
      }
    })
    .filter((origin) => !origin.startsWith("http://127.0.0.1"));

  return {
    h1Count: q("h1").length,
    mainCount: q("main").length,
    lang: document.documentElement.lang,
    title: document.title,
    hasSkipLink: Boolean(document.querySelector(".skip-link")),
    duplicateIds,
    brokenAnchors,
    emptyAnchors,
    missingLabelTargets,
    bareStats,
    externalResourceOrigins: [...new Set(resources)],
    imagesWithoutAlt: q("img").filter((img) => !img.hasAttribute("alt")).length,
    svgsWithoutLabel: q("svg").filter(
      (svg) =>
        !svg.hasAttribute("aria-hidden") &&
        !svg.hasAttribute("aria-label") &&
        !svg.querySelector("title"),
    ).length,
    nAnnotations: q(".n").length,
    hasCoverageStamp: Boolean(document.querySelector(".stamp")),
    hasProvenance: Boolean(document.querySelector(".provenance")),
    noticePresent: document.body.innerText.includes("not endorsed by Riot Games"),
    contactPresent: document.body.innerText.includes("lolstats@erik-schuetze.de"),
    visibleText: document.body.innerText.trim().length,
    height: document.documentElement.scrollHeight,
    /* Targets. Controls must clear 44x44 (WCAG 2.5.5, the design's own mobile
       contract). Text links are held to 24x24 (WCAG 2.5.8 AA) unless the link
       is inline in a sentence or is the only interactive thing in its
       container, which is the spacing exception the success criterion allows. */
    smallTargets: (() => {
      const controlish = (el) =>
        el.matches(
          "button, select, textarea, summary, [role='switch'], [role='radio'], [role='checkbox'], nav a, a.btn, a.chip, a.lockup, a.close, a.action, a.card-link, a.brand, a.page-link",
        );
      const aloneInContainer = (el) =>
        !el.parentElement ||
        el.parentElement.querySelectorAll("a, button, input, select").length === 1;

      const seen = new Set();
      const offenders = [];
      const candidates = q(
        "a, button, input, select, textarea, summary, [role='switch'], [role='radio'], [role='checkbox']",
      );
      for (const el of candidates) {
        if (el.type === "hidden") continue;
        /* A stretched link paints its clickable area over the whole card, so
           the target is the host box, not the inline anchor. */
        const host = el.classList.contains("stretch-link")
          ? (el.closest(".stretch-host") ?? el)
          : el;
        const target = host === el ? (el.closest("label") ?? el) : host;
        if (seen.has(target) || !visible(target)) continue;
        seen.add(target);
        const box = target.getBoundingClientRect();
        const control = controlish(target) || controlish(el);
        if (!control) {
          /* Text links: WCAG 2.5.8 wants 24x24 unless the sentence-inline or
             spacing exception applies. The spacing exception covers a lone
             link in a paragraph or list item, which is how every prose link
             here is laid out, so this only fires on dense link runs. */
          if (getComputedStyle(el).display === "inline" || aloneInContainer(target)) continue;
        }
        const min = control ? 44 : 24;
        if (box.width < min || box.height < min) {
          offenders.push({
            kind: control ? "control" : "link",
            tag: target.tagName,
            cls: String(target.className).replace(/svelte-\w+/g, "").trim().slice(0, 32),
            text: (target.textContent ?? "").replace(/\s+/g, " ").trim().slice(0, 28),
            w: Math.round(box.width),
            h: Math.round(box.height),
          });
        }
      }
      return offenders.slice(0, 40);
    })(),
    scripts: q("script[src]").map((s) => s.getAttribute("src")).filter((src) => /^https?:/.test(src)),
  };
}

function auditReducedMotion() {
  const offenders = [];
  for (const el of document.querySelectorAll("*")) {
    if (el.offsetParent === null && el.tagName !== "HTML" && el.tagName !== "BODY") continue;
    const cs = getComputedStyle(el);
    const animation = cs.animationName !== "none" && parseFloat(cs.animationDuration) > 0.01;
    const transition =
      cs.transitionProperty !== "none" && parseFloat(cs.transitionDuration) > 0.01;
    if (animation || transition) {
      offenders.push({
        tag: el.tagName,
        cls: String(el.className).slice(0, 48),
        animation: animation ? `${cs.animationName} ${cs.animationDuration}` : undefined,
        transition: transition ? `${cs.transitionProperty} ${cs.transitionDuration}` : undefined,
      });
    }
  }
  return offenders.slice(0, 10);
}

async function auditFocus(page) {
  await page.evaluate(() => {
    document.body.setAttribute("tabindex", "-1");
    document.body.focus();
  });
  await page.keyboard.press("Tab");
  return page.evaluate(() => {
    const el = document.activeElement;
    if (!el || el === document.body) return { found: false };
    const cs = getComputedStyle(el);
    return {
      found: true,
      tag: el.tagName,
      cls: String(el.className).slice(0, 48),
      text: (el.textContent ?? "").trim().slice(0, 40),
      focusVisible: el.matches(":focus-visible"),
      boxShadow: cs.boxShadow,
      outline: `${cs.outlineWidth} ${cs.outlineStyle}`,
      /* A ring is either a box-shadow containing a spread, or an outline. The
         one thing that must never happen is neither. */
      ringPresent:
        (cs.boxShadow !== "none" && /px/.test(cs.boxShadow)) ||
        (cs.outlineStyle !== "none" && parseFloat(cs.outlineWidth) >= 2),
    };
  });
}

/* -- main ----------------------------------------------------------------- */

if (!existsSync(BUILD)) {
  console.error("build/ is missing. Run `npm run build` first.");
  process.exit(2);
}

const server = await serve();
const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: true,
  defaultViewport: { width: 1440, height: 960, deviceScaleFactor: 1 },
  args: ["--no-sandbox", "--hide-scrollbars", "--force-color-profile=srgb"],
});

const requestedOrigins = new Set();
const failedResponses = [];

try {
  if (shotsEnabled) await mkdir(SHOTS, { recursive: true });

  for (const route of ROUTES) {
    if (only && route.slug !== only) continue;

    for (const width of WIDTHS) {
      const scope = `${route.slug}@${width.width}`;
      const page = await browser.newPage();
      await page.setViewport({
        width: width.width,
        height: width.height,
        deviceScaleFactor: 1,
        isMobile: width.width <= 480,
        hasTouch: width.width <= 480,
      });

      page.on("request", (request) => {
        const url = request.url();
        if (/^(data|blob|about):/.test(url)) return;
        try {
          requestedOrigins.add(new URL(url).origin);
        } catch {
          requestedOrigins.add(url);
        }
      });
      page.on("response", (response) => {
        if (response.status() >= 400) {
          failedResponses.push(`${response.status()} ${response.url()}`);
        }
      });
      page.on("pageerror", (error) => {
        check(false, scope, "no uncaught page errors", String(error));
      });

      await page.goto(`${server.origin}${route.path}`, { waitUntil: "networkidle0" });
      await page.evaluate(() => document.fonts.ready);

      const audit = await page.evaluate(auditPage);

      expectEqual(audit.h1Count, 1, scope, "exactly one h1");
      expectEqual(audit.mainCount, 1, scope, "exactly one main landmark");
      check(audit.lang !== "", scope, "html[lang] is set", "lang is empty");
      check(audit.title.length > 10, scope, "document title is descriptive", audit.title);
      check(audit.hasSkipLink, scope, "skip link is present");
      expectEqual(audit.duplicateIds.length, 0, scope, "no duplicate element ids");
      expectEqual(audit.brokenAnchors.length, 0, scope, "every in-page link resolves", audit.brokenAnchors.join(", "));
      expectEqual(audit.emptyAnchors, 0, scope, "no empty or '#' hrefs");
      expectEqual(audit.missingLabelTargets.length, 0, scope, "every aria-labelledby resolves", audit.missingLabelTargets.join(", "));
      expectEqual(audit.imagesWithoutAlt, 0, scope, "every image has alt text");
      expectEqual(audit.svgsWithoutLabel, 0, scope, "every svg is labelled or hidden");
      expectEqual(audit.externalResourceOrigins.length, 0, scope, "no cross-origin requests", audit.externalResourceOrigins.join(", "));
      expectEqual(audit.scripts.length, 0, scope, "no third-party script tags", audit.scripts.join(", "));
      expectEqual(audit.bareStats.length, 0, scope, "every statistic carries a sample or a note", audit.bareStats.join(" | "));
      check(audit.noticePresent, scope, "Riot non-endorsement notice is rendered");
      check(audit.contactPresent, scope, "contact route is rendered");
      expectAtLeast(audit.visibleText, 400, scope, "page has real content");

      /* Width-specific contract. */
      if (width.width <= 480) {
        expectEqual(audit.smallTargets.length, 0, scope, "interactive targets are >= 44x44", audit.smallTargets.map((t) => `${t.tag}.${t.cls} ${t.w}x${t.h}`).join(", "));
      }
      if (route.slug === "explore" || route.slug === "gallery") {
        /* The explorer's initial viewport carries three published samples (two
           stat tiles and the table footer); the gallery shows five. */
        const minSamples = route.slug === "gallery" ? 5 : 3;
        expectAtLeast(audit.nAnnotations, minSamples, scope, "sample annotations are rendered (n)");
        check(audit.hasCoverageStamp, scope, "coverage stamp is present");
      }
      if (route.slug !== "home") {
        check(audit.hasProvenance, scope, "provenance line is present");
      }

      /* Keyboard focus, once per width, on the first tabbable element. */
      const focus = await auditFocus(page);
      check(focus.found, scope, "first Tab moves focus into the page");
      if (focus.found) {
        check(focus.focusVisible, scope, "focused element matches :focus-visible", `${focus.tag}.${focus.cls}`);
        check(focus.ringPresent, scope, "focused element draws a ring", `${focus.boxShadow} | ${focus.outline}`);
      }

      if (shotsEnabled && width.shot) {
        const clipHeight = Math.min(audit.height, MAX_SHOT_HEIGHT);
        await page.screenshot({
          path: join(SHOTS, `${route.slug}-${width.name}.png`),
          clip: { x: 0, y: 0, width: width.width, height: clipHeight },
          captureBeyondViewport: true,
        });
      }

      await page.close();
    }
  }

  /* Reduced motion: one pass over the densest page. The token layer zeroes the
     durations and the components drop the float, so nothing may still be
     animating or transitioning. */
  const rmPage = await browser.newPage();
  await rmPage.setViewport({ width: 1440, height: 960, deviceScaleFactor: 1 });
  await rmPage.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
  await rmPage.goto(`${server.origin}/gallery`, { waitUntil: "networkidle0" });
  const rmAudit = await rmPage.evaluate(auditReducedMotion);
  expectEqual(rmAudit.length, 0, "gallery@reduced-motion", "nothing animates or transitions", rmAudit.map((o) => `${o.tag}.${o.cls} ${o.animation ?? ""}${o.transition ?? ""}`).join(" | "));
  const rmLift = await rmPage.evaluate(() => {
    const el = document.querySelector(".card.interactive");
    if (!el) return "no interactive card rendered";
    return getComputedStyle(el).transitionDuration;
  });
  check(rmLift !== "no interactive card rendered", "gallery@reduced-motion", "interactive card specimen exists");
  await rmPage.close();

  check(
    requestedOrigins.size === 1 && requestedOrigins.has(server.origin),
    "network",
    "every request stayed on the origin",
    [...requestedOrigins].join(", "),
  );
  expectEqual(failedResponses.length, 0, "network", "no failed responses", failedResponses.join(", "));
} finally {
  await browser.close();
  await server.close();
}

/* -- report --------------------------------------------------------------- */

const failures = results.filter((r) => !r.ok);
const byScope = new Map();
for (const result of results) {
  if (!byScope.has(result.scope)) byScope.set(result.scope, []);
  byScope.get(result.scope).push(result);
}

for (const [scope, scoped] of byScope) {
  const bad = scoped.filter((r) => !r.ok);
  const mark = bad.length === 0 ? "PASS" : "FAIL";
  console.log(`${mark}  ${scope.padEnd(24)} ${scoped.length - bad.length}/${scoped.length} checks`);
  for (const failure of bad) {
    console.log(`        - ${failure.name}${failure.detail ? `: ${failure.detail}` : ""}`);
  }
}

console.log(
  `\n${results.length - failures.length}/${results.length} checks passed across ${byScope.size} scope(s).`,
);
if (shotsEnabled) console.log(`Screenshots: ${SHOTS}`);

process.exit(failures.length === 0 ? 0 : 1);
