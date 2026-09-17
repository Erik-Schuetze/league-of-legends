import type { APIRoute } from 'astro';
import { siteData } from '../lib/site';

// A robots.txt that allows everything and points at the generated sitemap. It
// is an endpoint rather than a file in public/ so that the Sitemap line is
// derived from the configured canonical host (astro.config.mjs) and cannot
// disagree with the sitemap's own URLs.

export const GET: APIRoute = ({ site }) => {
  const origin = (site ?? new URL('https://lolstats.example.invalid')).origin;
  const data = siteData();

  const body = [
    '# Every page here is public, static and meant to be indexed.',
    'User-agent: *',
    'Allow: /',
    '',
    `Sitemap: ${origin}/sitemap.xml`,
    '',
    `# Snapshot state at build time: ${data.state}.`,
    '# Structurally identical pages are never emitted twice: the patch-specific',
    '# tier list of the newest patch declares the latest tier list as canonical.',
    '',
  ].join('\n');

  return new Response(body, {
    headers: { 'content-type': 'text/plain; charset=utf-8' },
  });
};
