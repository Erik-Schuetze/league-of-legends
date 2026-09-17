// How a page obtains the components it composes.
//
// docs/contracts.md section 3 freezes the component API: file paths, names and
// props. The design-system agent owns web/src/components/**; this module is how
// a page asks for a component without hard-coding that it exists yet, and
// without the build failing when it does not.
//
// A page calls `Card()` and renders the result with the frozen props. If
// web/src/components/Card.astro is present it is used; otherwise the page's own
// implementation of the same props is used, so the build always produces a
// complete, correct page. The gap is reported by `componentSource()` and in the
// build log, never by a blank slot.

import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

import { WEB_ROOT } from './web-root';

/** A component as Astro renders it. Props are checked at the call site, per the frozen API. */
export type SiteComponent = any;

const COMPONENT_DIR = join(WEB_ROOT, 'src', 'components');

/**
 * Vite resolves every .astro file under web/src/components at transform time.
 * This is the reason the loader works at all: a plain `import()` of a file that
 * does not exist is a build failure, while a glob with no matches is simply an
 * empty map.
 */
const componentModules = import.meta.glob('../components/*.astro');

const cache = new Map<string, SiteComponent | null>();

function nameOf(specifier: string): string {
  return specifier.replace('../components/', '').replace(/\.astro$/, '');
}

/** The component names available on disk, for the build log and the /about note. */
export function availableComponents(): string[] {
  const fromGlob = Object.keys(componentModules).map(nameOf);
  if (fromGlob.length > 0) return fromGlob.sort();
  if (!existsSync(COMPONENT_DIR)) return [];
  return readdirSync(COMPONENT_DIR)
    .filter((entry) => entry.endsWith('.astro'))
    .map((entry) => entry.replace(/\.astro$/, ''))
    .sort();
}

/**
 * Loads the design-system component of this name, or null when it does not
 * exist yet. A component that exists but throws is also reported as absent: a
 * page that renders through its own implementation is better than a build that
 * dies on somebody else's file.
 */
export async function optionalComponent(name: string): Promise<SiteComponent | null> {
  if (cache.has(name)) return cache.get(name) ?? null;

  const load = componentModules[`../components/${name}.astro`];
  if (!load) {
    cache.set(name, null);
    return null;
  }
  try {
    const module = (await load()) as { default?: SiteComponent };
    const component = module.default ?? null;
    cache.set(name, component);
    return component;
  } catch (error) {
    // eslint-disable-next-line no-console
    console.warn(`[components] ${name}.astro could not be loaded, using the page's own renderer: ${String(error)}`);
    cache.set(name, null);
    return null;
  }
}

/** True when the design system shipped this component, so the fallback is not in use. */
export async function hasComponent(name: string): Promise<boolean> {
  return (await optionalComponent(name)) !== null;
}

// --------------------------------------------------------------------------
// Components that must declare a prop to be usable at all.
// --------------------------------------------------------------------------

const sources = new Map<string, string | null>();

function componentSource(name: string): string | null {
  if (sources.has(name)) return sources.get(name) ?? null;
  const file = join(COMPONENT_DIR, `${name}.astro`);
  const text = existsSync(file) ? readFileSync(file, 'utf8') : null;
  sources.set(name, text);
  return text;
}

/** The body of a component's `interface Props`, or null when it declares none. */
function propsBlock(source: string): string | null {
  const start = source.indexOf('interface Props');
  if (start === -1) return null;
  const open = source.indexOf('{', start);
  if (open === -1) return null;

  let depth = 0;
  for (let index = open; index < source.length; index += 1) {
    const character = source[index];
    if (character === '{') depth += 1;
    else if (character === '}') {
      depth -= 1;
      if (depth === 0) return source.slice(open + 1, index);
    }
  }
  return null;
}

/**
 * True when the component declares this prop.
 *
 * Props are read from the component's own source rather than from its rendered
 * output, because the question is "can this component be asked for this datum
 * at all", and only the declaration answers that at build time.
 */
export function declaresProp(name: string, prop: string): boolean {
  const source = componentSource(name);
  if (source === null) return false;
  const block = propsBlock(source);
  if (block === null) return false;
  // String.raw: a plain template literal collapses `\s` to `s` and the pattern
  // then matches nothing (it threw "Nothing to repeat" on the way in).
  return new RegExp(String.raw`(^|[\s,;{])${prop}\s*\??\s*:`, 'm').test(block);
}

const missing = new Set<string>();
const used = new Set<string>();
const incomplete = new Map<string, string[]>();

/** Loads `name`, recording which implementation the build ended up using. */
export async function componentOr(name: string, fallback: SiteComponent): Promise<SiteComponent> {
  const found = await optionalComponent(name);
  if (found) {
    used.add(name);
    return found;
  }
  missing.add(name);
  return fallback;
}

/**
 * Loads `name` only when it declares every prop in `requiredProps`; otherwise
 * uses `fallback`.
 *
 * A frozen API can add a prop that is the only source of a datum the API itself
 * requires - `BuildList`'s `lookup` is how `Build.key` becomes an item, rune or
 * spell, which is what the frozen description of the component asks for. An
 * implementation that is on disk but cannot render that datum is treated the way
 * an absent one is: the page renders through its own implementation of the same
 * API rather than publishing the part of the API it can satisfy. The moment the
 * design system declares the prop, every call site switches to it with no change
 * here, which is the point of asking for the capability rather than the file.
 */
export async function componentProviding(
  name: string,
  fallback: SiteComponent,
  requiredProps: string[],
): Promise<SiteComponent> {
  const found = await optionalComponent(name);
  if (found && requiredProps.every((prop) => declaresProp(name, prop))) {
    used.add(name);
    return found;
  }

  if (found) {
    const absent = requiredProps.filter((prop) => !declaresProp(name, prop));
    if (!incomplete.has(name)) {
      incomplete.set(name, absent);
      // eslint-disable-next-line no-console
      console.warn(
        `[components] ${name}.astro is present but does not declare ${absent.join(', ')}; ` +
          `the page renders it with its own implementation of the same API.`,
      );
    }
  }
  missing.add(name);
  return fallback;
}

/** The report BaseLayout prints once per build. */
export function componentReport(): { fromDesignSystem: string[]; fromFallback: string[]; incomplete: Record<string, string[]> } {
  return {
    fromDesignSystem: [...used].sort(),
    fromFallback: [...missing].sort(),
    incomplete: Object.fromEntries([...incomplete].sort(([a], [b]) => a.localeCompare(b))),
  };
}
