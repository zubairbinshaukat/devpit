import { BASE } from '../../site.config.mjs';

/**
 * Live-site path of a content id, with the base and without a trailing slash
 * (matches Vercel cleanUrls and the canonical URLs).
 */
export function hrefFor(id: string): string {
  const clean = id.replace(/\/index$/, '').replace(/^index$/, '');
  return clean ? `${BASE}/${clean}` : BASE;
}

/**
 * Turns a link Starlight emitted for build.format "file" (".../foo.html",
 * ".../index.html", "/docs/") into the clean form Vercel serves, so no click
 * goes through a 308 redirect. Query and hash are kept; external URLs and
 * anything that is not an absolute site path are returned untouched.
 */
export function cleanHref(href: string): string {
  if (!href.startsWith('/') || href.startsWith('//')) return href;
  const m = /^([^?#]*)([?#].*)?$/.exec(href);
  if (!m) return href;
  const path = m[1]!.replace(/\/index\.html$/, '').replace(/\.html$/, '').replace(/(?<=.)\/$/, '');
  return `${path || '/'}${m[2] ?? ''}`;
}
