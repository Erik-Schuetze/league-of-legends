import type { Role } from '../types/agg';

// Role slugs and labels. The slug map mirrors aggmodel.roleSlugs, which is what
// the route table and the artifact file names both use, so /tier-list/mid and
// matchups/mid.json cannot drift apart.
//
// It is restated rather than derived because the generated TypeScript carries
// the enum values but not their URL spelling, and because a missing slug must
// be a build error rather than a silent `undefined` in a link.

export const ROLES: readonly Role[] = ['TOP', 'JUNGLE', 'MID', 'BOTTOM', 'SUPPORT'] as const;

const ROLE_SLUGS: Record<Role, string> = {
  TOP: 'top',
  JUNGLE: 'jungle',
  MID: 'mid',
  BOTTOM: 'bottom',
  SUPPORT: 'support',
};

const ROLE_LABELS: Record<Role, string> = {
  TOP: 'Top',
  JUNGLE: 'Jungle',
  MID: 'Mid',
  BOTTOM: 'Bottom',
  SUPPORT: 'Support',
};

export function roleSlug(role: Role): string {
  const slug = ROLE_SLUGS[role];
  if (!slug) throw new Error(`no URL slug for role ${String(role)}`);
  return slug;
}

export function roleLabel(role: Role): string {
  return ROLE_LABELS[role] ?? String(role);
}

export function roleFromSlug(slug: string): Role | undefined {
  return ROLES.find((role) => ROLE_SLUGS[role] === slug);
}

/** Every (role, slug) pair, in the canonical order, for getStaticPaths. */
export function roleRoutes(): Array<{ role: Role; slug: string }> {
  return ROLES.map((role) => ({ role, slug: roleSlug(role) }));
}
