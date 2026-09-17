import type { APIRoute } from 'astro';
import { FALLBACK_SITE_URL } from '../lib/legal';
import { routeList } from '../lib/routes';

// sitemap.xml, generated from the same route list the build renders.
//
// The origin is the canonical host astro.config.mjs resolved (LOLSTATS_SITE_URL, or
// the deliberate default when the variable is unset). The local fallback only ever
// fires if `site` is somehow missing; it names the same host on purpose, so that a
// misconfigured build can never publish a URL that is not the site's real address.
//
// Written by hand rather than by pulling in @astrojs/sitemap: the route set is
// already known at build time, the XML is a dozen lines, and one fewer
// dependency is one fewer thing to audit for a site that must stay small.

function escapeXml(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

export const GET: APIRoute = ({ site }) => {
  const origin = site ?? new URL(FALLBACK_SITE_URL);

  const entries = routeList()
    .map((route) => {
      const url = new URL(route.path, origin).href;
      const lastmod = route.lastmod ? `<lastmod>${route.lastmod}</lastmod>` : '';
      return (
        '  <url>' +
        `<loc>${escapeXml(url)}</loc>` +
        lastmod +
        `<changefreq>${route.changefreq}</changefreq>` +
        `<priority>${route.priority.toFixed(1)}</priority>` +
        '</url>'
      );
    })
    .join('\n');

  const body = [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">',
    entries,
    '</urlset>',
    '',
  ].join('\n');

  return new Response(body, {
    headers: { 'content-type': 'application/xml; charset=utf-8' },
  });
};
