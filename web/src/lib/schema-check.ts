import { readFileSync } from 'node:fs';
import { resolve as resolvePath } from 'node:path';

import { WEB_ROOT } from './web-root';

// Runtime validation of every artifact the site reads, against the JSON Schema
// that `make types` generates next to web/src/types/agg.d.ts.
//
// The site's guarantee is that a schema change breaks the build instead of
// rendering blanks, and TypeScript alone cannot give that: a cast of parsed
// JSON is a promise, not a check. So each artifact is validated before it is
// cast, and a violation throws - which fails the Astro build with the offending
// file and field named.
//
// Only the subset of JSON Schema draft 2020-12 that the generator emits is
// implemented: $ref, type, enum, required, properties, items. `additionalProperties`
// is deliberately NOT enforced, so an additive field (the manifest's `source`,
// for example) is accepted by an older site build rather than rejected.

type Node = {
  $ref?: string;
  type?: string;
  enum?: unknown[];
  required?: string[];
  properties?: Record<string, Node>;
  items?: Node;
};

interface RootSchema extends Node {
  $defs?: Record<string, Node>;
}

const SCHEMA_FILE = resolvePath(WEB_ROOT, 'src', 'types', 'agg.schema.json');

let root: RootSchema | undefined;

function schema(): RootSchema {
  if (!root) {
    root = JSON.parse(readFileSync(SCHEMA_FILE, 'utf8')) as RootSchema;
    if (!root.$defs) throw new Error(`${SCHEMA_FILE}: no $defs section, the generator output is not usable`);
  }
  return root;
}

function resolve(ref: string, rootSchema: RootSchema): Node | undefined {
  const prefix = '#/$defs/';
  return ref.startsWith(prefix) ? rootSchema.$defs?.[ref.slice(prefix.length)] : undefined;
}

function check(node: Node, value: unknown, path: string, rootSchema: RootSchema, errors: string[]): void {
  if (node.$ref) {
    const target = resolve(node.$ref, rootSchema);
    if (!target) {
      errors.push(`${path}: unresolved schema reference ${node.$ref}`);
      return;
    }
    check(target, value, path, rootSchema, errors);
    return;
  }

  if (node.enum && !node.enum.some((allowed) => allowed === value)) {
    errors.push(`${path}: ${JSON.stringify(value)} is not one of ${node.enum.map((v) => JSON.stringify(v)).join(', ')}`);
  }

  switch (node.type) {
    case 'object': {
      if (value === null || typeof value !== 'object' || Array.isArray(value)) {
        errors.push(`${path}: expected an object, found ${describe(value)}`);
        return;
      }
      const obj = value as Record<string, unknown>;
      for (const key of node.required ?? []) {
        if (!(key in obj)) errors.push(`${path}: missing required field "${key}"`);
      }
      for (const [key, sub] of Object.entries(node.properties ?? {})) {
        if (key in obj) check(sub, obj[key], `${path}.${key}`, rootSchema, errors);
      }
      return;
    }
    case 'array': {
      if (!Array.isArray(value)) {
        errors.push(`${path}: expected an array, found ${describe(value)}`);
        return;
      }
      if (node.items) value.forEach((entry, i) => check(node.items as Node, entry, `${path}[${i}]`, rootSchema, errors));
      return;
    }
    case 'integer':
      if (typeof value !== 'number' || !Number.isInteger(value)) {
        errors.push(`${path}: expected an integer, found ${describe(value)}`);
      }
      return;
    case 'number':
      if (typeof value !== 'number' || !Number.isFinite(value)) {
        errors.push(`${path}: expected a number, found ${describe(value)}`);
      }
      return;
    case 'string':
      if (typeof value !== 'string') errors.push(`${path}: expected a string, found ${describe(value)}`);
      return;
    case 'boolean':
      if (typeof value !== 'boolean') errors.push(`${path}: expected a boolean, found ${describe(value)}`);
      return;
    case 'null':
      if (value !== null) errors.push(`${path}: expected null, found ${describe(value)}`);
      return;
    default:
      // A node with only an enum, or no constraint at all, is already handled.
      return;
  }
}

function describe(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'an array';
  return typeof value;
}

/**
 * Validates a parsed artifact against the named schema definition and returns
 * it typed. Throws - failing the build - when the artifact does not conform.
 */
export function assertArtifact<T>(definition: string, value: unknown, label: string): T {
  const rootSchema = schema();
  const node = rootSchema.$defs?.[definition];
  if (!node) throw new Error(`agg/v1 schema has no definition "${definition}"`);

  const errors: string[] = [];
  check(node, value, definition, rootSchema, errors);
  if (errors.length > 0) {
    throw new Error(`${label} does not match agg/v1 schema "${definition}":\n  - ${errors.join('\n  - ')}`);
  }
  return value as T;
}
