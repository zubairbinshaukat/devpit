// Checks that every internal link and asset a built page references exists:
// stylesheets, scripts, images, fonts, the OG images and internal <a href>
// targets (including in-page #anchors). External links are not fetched.
//
// Internal links must also be clean: no ".html" and no trailing slash, because
// Vercel would redirect them (see the check inside the loop).
//
// Root-level files (favicons, /install, /sitemap.xml ...) live outside the
// Astro output, so they are checked against web/ (the landing sources) and
// the list of generated files.
import fs from 'node:fs';
import path from 'node:path';

import { BASE, DIST, Report, SITE, WEB, builtPages, requireDist } from './lib/dist.mjs';

requireDist();
const r = new Report('check-assets');

/** Served at the site root but not present in web/ as a source file. */
const GENERATED_ROOT = new Set(['/', '/install', '/sitemap.xml', '/llms.txt', '/llms-full.txt']);

function existsInDocs(p) {
  // p is like /docs/a/b: try file, .html, index.html.
  const rel = p.slice(BASE.length).replace(/^\//, '');
  return [rel, `${rel}.html`, path.join(rel, 'index.html')].some((c) => {
    const f = path.join(DIST, decodeURIComponent(c));
    return fs.existsSync(f) && fs.statSync(f).isFile();
  });
}

function existsInRoot(p) {
  if (GENERATED_ROOT.has(p)) return true;
  const f = path.join(WEB, decodeURIComponent(p));
  return fs.existsSync(f) && fs.statSync(f).isFile();
}

let refs = 0;
const pages = builtPages();
for (const page of pages) {
  const ids = new Set(page.root.querySelectorAll('[id]').map((e) => e.getAttribute('id')));
  const targets = [];
  for (const [sel, attr] of [
    ['link[href]', 'href'],
    ['script[src]', 'src'],
    ['img[src]', 'src'],
    ['source[src]', 'src'],
    ['a[href]', 'href'],
    ['meta[property="og:image"]', 'content'],
    ['meta[name="twitter:image"]', 'content'],
  ]) {
    for (const el of page.root.querySelectorAll(sel)) {
      const v = el.getAttribute(attr);
      if (v) targets.push({ v, tag: sel });
    }
  }
  for (const { v, tag } of targets) {
    if (/^(mailto:|tel:|javascript:|data:)/.test(v)) continue;
    refs++;
    let url;
    try {
      url = new URL(v, `${SITE}${page.urlPath}`);
    } catch {
      r.error(page.rel, `unparseable URL ${v}`);
      continue;
    }
    if (url.origin !== SITE) continue; // external
    // Vercel serves clean URLs (cleanUrls, trailingSlash false) and answers
    // ".../foo.html" and ".../foo/" with a 308. No internal link may need one.
    if (tag === 'a[href]' || tag === 'link[href]') {
      if (/\.html$/.test(url.pathname)) r.error(page.rel, `internal link ${v} ends in .html (use the clean URL)`);
      else if (url.pathname.length > 1 && url.pathname.endsWith('/')) r.error(page.rel, `internal link ${v} has a trailing slash`);
    }
    const p = url.pathname.replace(/\/$/, '') || '/';
    if (v.startsWith('#')) {
      if (v.length > 1 && !ids.has(v.slice(1))) r.error(page.rel, `anchor ${v} does not exist on the page`);
      continue;
    }
    if (p === BASE || p.startsWith(`${BASE}/`)) {
      if (!existsInDocs(p)) r.error(page.rel, `${v} does not exist in the built docs`);
    } else if (!existsInRoot(p)) {
      r.error(page.rel, `${v} does not exist at the site root`);
    }
  }
}

process.exit(r.finish(`${refs} references in ${pages.length} pages`));
