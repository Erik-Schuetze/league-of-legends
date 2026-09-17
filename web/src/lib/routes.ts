import { ROLES, roleSlug } from './roles';
import { championRolePages } from './page';
import { siteData } from './site';

// Every URL the build publishes, in one place.
//
// sitemap.xml is generated from this list rather than from a crawl of the
// output directory, so a route that is published cannot be missing from the
// sitemap and a route that is not published cannot be advertised. robots.txt
// points at that sitemap, so the two files agree by construction.
//
// The statistics routes are listed only when a snapshot exists, because that is
// also when they are rendered: with no snapshot the statistics pages are
// deliberate empty states and ask not to be indexed, and advertising an empty
// table in a sitemap would be dishonest about what the site has.

export interface Route {
  path: string;
  /** Content last changed when the snapshot it was rendered from was generated. */
  lastmod?: string;
  priority: number;
  changefreq: 'daily' | 'weekly' | 'monthly';
}

export function routeList(): Route[] {
  const site = siteData();
  const lastmod = site.manifestGeneratedAt ? site.manifestGeneratedAt.slice(0, 10) : undefined;
  const dataRoute = (path: string, priority: number): Route => ({
    path,
    lastmod,
    priority,
    changefreq: 'daily',
  });
  const page = (path: string, priority: number): Route => ({
    path,
    priority,
    changefreq: 'monthly',
  });

  const routes: Route[] = [
    page('/', 1),
    page('/about', 0.6),
    page('/disclaimer', 0.3),
    page('/legal/terms', 0.3),
    page('/legal/privacy', 0.3),
  ];

  if (site.latest === null) return routes;

  for (const role of ROLES) {
    const slug = roleSlug(role);
    routes.push(dataRoute(`/tier-list/${slug}`, 0.9));
    routes.push(dataRoute(`/matchups/${slug}`, 0.8));
  }

  for (const patch of site.patches) {
    for (const role of ROLES) {
      routes.push(dataRoute(`/patch/${patch}/tier-list/${roleSlug(role)}`, 0.7));
    }
  }

  for (const champion of site.champions) {
    routes.push(dataRoute(`/champions/${champion.slug}`, 0.6));
  }
  for (const entry of championRolePages()) {
    routes.push(dataRoute(`/champions/${entry.champion.slug}/${roleSlug(entry.role)}`, 0.5));
  }

  return routes;
}
