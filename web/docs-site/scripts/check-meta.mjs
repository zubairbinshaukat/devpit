// Fails the build on SEO basics: every page has a unique <title>, a unique
// meta description of a sensible length, a clean canonical URL, social tags
// with an image that exists, and one <h1>.
import fs from 'node:fs';
import path from 'node:path';

import { BASE, DIST, Report, SITE, builtPages, requireDist } from './lib/dist.mjs';

requireDist();
const r = new Report('check-meta');

const TITLE_MAX = 60; // roughly what Google shows before truncating
const DESC_MIN = 70;
const DESC_MAX = 160;

const titles = new Map();
const descriptions = new Map();
const meta = (root, sel) => root.querySelector(sel)?.getAttribute('content')?.trim();
const pages = builtPages();

for (const p of pages) {
  const where = p.rel;
  if (p.rel === '404.html') {
    // Served for every unknown URL: no address of its own, never indexed.
    const rb = meta(p.root, 'meta[name="robots"]') ?? '';
    if (!/noindex/i.test(rb)) r.error(where, '404 page must be noindex');
    if (p.root.querySelector('link[rel="canonical"]')) r.error(where, '404 page must not have a canonical link');
    if (meta(p.root, 'meta[property="og:url"]')) r.error(where, '404 page must not have og:url');
    if (p.root.querySelector('link[rel="sitemap"]')) r.error(where, '404 page must not link the sitemap');
    if (!meta(p.root, 'meta[property="og:image"]')) r.error(where, '404 page needs og:image');
    if (!meta(p.root, 'meta[name="twitter:image"]')) r.error(where, '404 page needs twitter:image');
    continue;
  }
  const { root } = p;

  const title = root.querySelector('title')?.text.trim() ?? '';
  if (!title) r.error(where, 'missing <title>');
  else {
    if (title.length > TITLE_MAX) r.error(where, `title is ${title.length} chars (max ${TITLE_MAX}): "${title}"`);
    if (titles.has(title)) r.error(where, `duplicate title with ${titles.get(title)}: "${title}"`);
    titles.set(title, where);
  }

  const desc = meta(root, 'meta[name="description"]') ?? '';
  if (!desc) r.error(where, 'missing meta description');
  else {
    if (desc.length < DESC_MIN) r.error(where, `description is ${desc.length} chars (min ${DESC_MIN}): "${desc}"`);
    if (desc.length > DESC_MAX) r.error(where, `description is ${desc.length} chars (max ${DESC_MAX}): "${desc}"`);
    if (descriptions.has(desc)) r.error(where, `duplicate description with ${descriptions.get(desc)}`);
    descriptions.set(desc, where);
  }

  const expected = `${SITE}${p.urlPath}`;
  const canonical = root.querySelector('link[rel="canonical"]')?.getAttribute('href');
  if (!canonical) r.error(where, 'missing canonical link');
  else if (canonical !== expected) r.error(where, `canonical is ${canonical}, expected ${expected}`);
  if (canonical && (/\.html$/.test(canonical) || (canonical.endsWith('/') && canonical !== `${SITE}/`))) {
    r.error(where, `canonical must be the clean, slash-less URL: ${canonical}`);
  }
  const ogUrl = meta(root, 'meta[property="og:url"]');
  if (ogUrl !== expected) r.error(where, `og:url is ${ogUrl}, expected ${expected}`);

  for (const prop of ['og:title', 'og:description', 'og:image', 'og:type', 'og:site_name']) {
    if (!meta(root, `meta[property="${prop}"]`)) r.error(where, `missing ${prop}`);
  }
  for (const name of ['twitter:card', 'twitter:image']) {
    if (!meta(root, `meta[name="${name}"]`)) r.error(where, `missing ${name}`);
  }
  if (meta(root, 'meta[property="og:image:width"]') !== '1200' || meta(root, 'meta[property="og:image:height"]') !== '630') {
    r.error(where, 'og:image must be declared 1200x630');
  }
  const image = meta(root, 'meta[property="og:image"]');
  if (image) {
    if (!image.startsWith('https://')) r.error(where, `og:image is not an absolute https URL: ${image}`);
    else if (image.startsWith(`${SITE}${BASE}/`)) {
      const file = path.join(DIST, image.slice(`${SITE}${BASE}/`.length));
      if (!fs.existsSync(file)) r.error(where, `og:image file does not exist: ${image}`);
    }
  }

  const h1 = root.querySelectorAll('h1').length;
  if (h1 !== 1) r.error(where, `expected exactly one <h1>, found ${h1}`);
  if (!root.querySelector('html')?.getAttribute('lang')) r.error(where, 'missing <html lang>');
  const robots = meta(root, 'meta[name="robots"]') ?? '';
  const thin = /^blog\/(tags\/|authors\/|\d+\.html)/.test(p.rel);
  if (/noindex/i.test(robots) && !thin) r.error(where, 'page is noindex');
  if (thin && !/noindex/i.test(robots)) r.error(where, 'thin blog archive page should be noindex');
  const draft = /\(draft\)| draft$|^Draft/i.test(title) || /Draft page that is being written/.test(desc);
  if (draft && process.env.ALLOW_DRAFTS !== '1') r.warn(where, 'looks like a draft page');
}

process.exit(r.finish(`${pages.length - 1} pages`));
