// Checks the Markdown twins in web/dist (run `node web/scripts/build.mjs`
// first; the build runs the same checks and fails on them too):
//
//   - every docs, troubleshooting and blog page has /docs/<page>.md
//   - a twin starts with "# <title>" and ends with its canonical HTML URL
//   - no twin has an import/export line, a JSX or HTML tag, an MDX comment
//     or a ::: aside left outside code
//   - every link in a twin, llms.txt and llms-full.txt is absolute, and a
//     link on this site points at a file (and #anchor) that exists
//   - no twin is listed in the sitemap (they are served noindex)
//   - vercel.json serves them as text/markdown with X-Robots-Tag: noindex
import fs from 'node:fs';
import path from 'node:path';

import { verifyTwins } from '../../scripts/md-twins.mjs';
import { Report, WEB } from './lib/dist.mjs';
import { loadPages } from './lib/pages.mjs';

const r = new Report('check-markdown');
const dist = path.join(WEB, 'dist');
if (!fs.existsSync(path.join(dist, 'index.html'))) {
  console.error('web/dist is missing. Run `node web/scripts/build.mjs` first.');
  process.exit(2);
}

const pages = loadPages();
for (const e of verifyTwins(dist, pages)) r.error('twins', e);

const sitemap = fs.existsSync(path.join(dist, 'sitemap.xml')) ? fs.readFileSync(path.join(dist, 'sitemap.xml'), 'utf8') : '';
if (/<loc>[^<]*\.md<\/loc>/.test(sitemap)) r.error('sitemap.xml', 'lists a Markdown twin; twins are noindex and stay out of the sitemap');

const vercel = JSON.parse(fs.readFileSync(path.join(WEB, 'vercel.json'), 'utf8'));
const rule = (vercel.headers ?? []).find((h) => h.source === '/docs/(.*)\\.md');
const header = (k) => rule?.headers.find((h) => h.key.toLowerCase() === k.toLowerCase())?.value;
if (!rule) r.error('vercel.json', 'has no header rule for /docs/(.*)\\.md');
else {
  if (header('Content-Type') !== 'text/markdown; charset=utf-8') r.error('vercel.json', '*.md under /docs must be served as text/markdown; charset=utf-8');
  if (!/noindex/.test(header('X-Robots-Tag') ?? '')) r.error('vercel.json', '*.md under /docs must send X-Robots-Tag: noindex');
}

process.exit(r.finish(`${pages.length} twins`));
