// Checks the assembled site in web/dist (run `node web/scripts/build.mjs`
// first): the merged sitemap lists exactly the built pages plus the landing
// page, robots.txt points at it and allows the docs, llms.txt matches the
// docs pages, and the landing page still has its own tags.
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';

import { BASE, Report, SITE, WEB, builtPages } from './lib/dist.mjs';

const r = new Report('check-site');
const dist = path.join(WEB, 'dist');
const read = (f) => (fs.existsSync(path.join(dist, f)) ? fs.readFileSync(path.join(dist, f), 'utf8') : null);
/** The landing page's canonical URL (web/home.html, served at /home). */
const LANDING = `${SITE}/home`;

if (!fs.existsSync(path.join(dist, 'home.html'))) {
  console.error('web/dist is missing. Run `node web/scripts/build.mjs` first.');
  process.exit(2);
}

// Sitemap.
const sitemap = read('sitemap.xml') ?? '';
const locs = [...sitemap.matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => m[1]);
if (new Set(locs).size !== locs.length) r.error('sitemap.xml', 'has duplicate URLs');
if (!locs.includes(LANDING)) r.error('sitemap.xml', 'is missing the landing page (/home)');
if (locs.includes(`${SITE}/`)) r.error('sitemap.xml', 'lists / which redirects to /home; list /home only');
for (const l of locs) if (l.endsWith('.html') || l.endsWith('/')) r.error('sitemap.xml', `unclean URL ${l}`);
// noindex pages (tag, author and page-number archives of the blog) are not listed.
const built = builtPages()
  .filter((p) => p.rel !== '404.html' && !/noindex/i.test(p.root.querySelector('meta[name="robots"]')?.getAttribute('content') ?? ''))
  .map((p) => `${SITE}${p.urlPath}`);
for (const u of built) if (!locs.includes(u)) r.error('sitemap.xml', `is missing ${u}`);
for (const l of locs) if (l !== LANDING && !built.includes(l)) r.error('sitemap.xml', `lists ${l} which is not a built page`);
if (locs.length > 50000) r.error('sitemap.xml', 'over 50,000 URLs: switch to a sitemap index');
if (/sitemap-(index|\d+)\.xml/.test(sitemap)) r.error('sitemap.xml', 'must be a plain urlset');
for (const f of fs.readdirSync(path.join(dist, 'docs'))) {
  if (/^sitemap/.test(f)) r.error('dist/docs', `stray ${f}: there must be one sitemap`);
}

// robots.txt.
const robots = read('robots.txt') ?? '';
if (!new RegExp(`^Sitemap:\\s*${SITE}/sitemap\\.xml\\s*$`, 'm').test(robots)) r.error('robots.txt', 'must contain the Sitemap line');
if (/^Disallow:\s*\/(docs)?\/?\s*$/m.test(robots)) r.error('robots.txt', 'must not disallow the docs');

if (/^Disallow:\s*\/home\/?\s*$/m.test(robots)) r.error('robots.txt', 'must not disallow /home');

// Landing page is intact and lives at /home only (vercel.json redirects / to it).
const landing = read('home.html') ?? '';
const original = fs.readFileSync(path.join(WEB, 'home.html'), 'utf8');
if (landing !== original) r.error('home.html', 'dist copy differs from web/home.html');
if (!/href="\/docs"/.test(original)) r.warn('home.html', 'has no link to /docs');
if (fs.existsSync(path.join(dist, 'index.html'))) r.error('index.html', 'must not exist: the landing page is /home');
const canonical = /<link rel="canonical" href="([^"]+)"/.exec(original)?.[1];
if (canonical !== LANDING) r.error('home.html', `canonical is ${canonical}, expected ${LANDING}`);
if (/(og:url" content|"url": |"@id": )"https:\/\/devpit\.zubyr\.dev\/(#[^"]*)?"/.test(original)) {
  r.error('home.html', 'og:url or JSON-LD still points at / (use /home)');
}
if (/<meta name="robots" content="[^"]*noindex/i.test(original)) r.error('home.html', 'is noindex');
const redirects = JSON.parse(fs.readFileSync(path.join(WEB, 'vercel.json'), 'utf8')).redirects ?? [];
const root = redirects.find((x) => x.source === '/');
if (!root || root.destination !== '/home' || !root.permanent) r.error('vercel.json', 'must permanently redirect / to /home');

// llms.txt: committed copy is fresh, and every docs page is listed in the
// built one by its Markdown twin (the docs home is /docs/index.md).
const gen = spawnSync(process.execPath, [path.join(WEB, 'scripts', 'gen-llms.mjs'), '--check'], { encoding: 'utf8' });
if (gen.status !== 0) r.error('web/llms.txt', 'is stale, run: node web/scripts/gen-llms.mjs --write');
const llms = read('llms.txt') ?? '';
for (const u of built) {
  if (u.startsWith(`${SITE}${BASE}/blog/`)) continue; // blog tags, authors and pages are not listed
  if (u === `${SITE}${BASE}/blog`) continue;
  const md = u === `${SITE}${BASE}` ? `${u}/index.md` : `${u}.md`;
  if (!llms.includes(`](${md})`)) r.error('llms.txt', `does not list ${md}`);
}
const posts = builtPages().filter((p) => /^\/docs\/blog\/(?!tags\/|authors\/|\d+$)[^/]+$/.test(p.urlPath));
for (const p of posts) if (!llms.includes(`](${SITE}${p.urlPath}.md)`)) r.error('llms.txt', `does not list ${SITE}${p.urlPath}.md under Optional`);
if (!/^# .+\n\n> .+/.test(llms)) r.error('llms.txt', 'must start with an H1 and a blockquote summary (llmstxt.org)');
if (!/^## Optional$/m.test(llms)) r.error('llms.txt', 'has no "## Optional" section');
if (!read('llms-full.txt')) r.error('llms-full.txt', 'missing');

process.exit(r.finish(`${locs.length} sitemap URLs`));
