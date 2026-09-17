import type { APIRoute } from 'astro';
import { routeList } from '../lib/routes';

// sitemap.xml, generated from the same route list the build renders.
//
// Written by hand rather than by pulling in @astrojs/sitemap: the route set is
// already known at build time, the XML is a dozen lines, and one fewer
// dependency is one fewer thing to audit for a site that must stay small.

function escapeXml(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

export const GET: APIRoute = ({ site }) => {
  const origin = site ?? new URL('https://lolstats.example.invalid');

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
