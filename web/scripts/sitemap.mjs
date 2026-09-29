// Builds the single site-wide sitemap.
//
// web/sitemap.xml stays the hand-kept source of truth for the landing page
// (its <url> blocks are copied verbatim, including lastmod). The docs
// sitemap that Astro writes is read, its URLs are added, and the result is
// one <urlset>. Google's guidance is that one file is fine below 50,000 URLs
// and 50 MB, and a sitemap index only earns its keep beyond that.
//
// lastmod for a docs URL is written only when the page's frontmatter has a
// truthful date (`lastUpdated`, or a blog post's `date`). A guessed lastmod
// is worse than none: Google ignores a sitemap whose dates it cannot trust.
import { loadPages } from '../docs-site/scripts/lib/pages.mjs';

/** Values of every `<loc>` in a sitemap document. */
export function locs(xml) {
  return [...xml.matchAll(/<loc>\s*([^<]+?)\s*<\/loc>/g)].map((m) => m[1]);
}

/** The `<url>...</url>` blocks of a sitemap, verbatim. */
function urlBlocks(xml) {
  return [...xml.matchAll(/[ \t]*<url>[\s\S]*?<\/url>/g)].map((m) => m[0]);
}

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;');

/**
 * @param {string} landingXml contents of web/sitemap.xml
 * @param {string} docsXml contents of the Astro docs sitemap (sitemap-0.xml)
 * @returns {string} the merged sitemap
 */
export function mergeSitemap(landingXml, docsXml) {
  const lastmodByUrl = new Map(loadPages().filter((p) => p.lastmod).map((p) => [p.url, p.lastmod]));
  const landing = urlBlocks(landingXml);
  const seen = new Set(locs(landingXml));
  const docs = [];
  for (const loc of locs(docsXml).sort()) {
    if (seen.has(loc)) continue;
    seen.add(loc);
    const lm = lastmodByUrl.get(loc);
    docs.push(`  <url>\n    <loc>${esc(loc)}</loc>${lm ? `\n    <lastmod>${lm}</lastmod>` : ''}\n  </url>`);
  }
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">',
    ...landing,
    ...docs,
    '</urlset>',
    '',
  ].join('\n');
}
