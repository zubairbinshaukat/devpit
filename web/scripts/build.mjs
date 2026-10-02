// Builds the whole site into web/dist: the static landing page exactly as it
// is in web/, plus the Astro docs under /docs.
//
//   node scripts/build.mjs           (Vercel: buildCommand, root directory web)
//
// Vercel keeps building the serverless functions in web/api itself (they are
// not copied here), applies web/vercel.json (cleanUrls, rewrites, headers)
// and serves this dist folder as the static output. Only dist is served, so
// the docs sources, node_modules and these scripts are never public.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { buildLlms, buildLlmsFull } from './gen-llms.mjs';
import { buildTwins, verifyTwins } from './md-twins.mjs';
import { mergeSitemap } from './sitemap.mjs';

const WEB = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const DOCS = path.join(WEB, 'docs-site');
const DIST = path.join(WEB, 'dist');

/** Top-level names in web/ that are NOT part of the static site. */
const NOT_STATIC = new Set([
  'api', // Vercel functions, built by Vercel
  'docs-site', // Astro project, its build output is copied to dist/docs
  'scripts',
  'dist',
  'node_modules',
  'package.json', // build tooling, not a public file
  'package-lock.json',
  'vercel.json',
  'sitemap.xml', // regenerated below as the merged sitemap
  'llms.txt', // regenerated below from the docs pages
]);

function run(cmd, args, cwd) {
  const r = spawnSync(cmd, args, { cwd, stdio: 'inherit', shell: false });
  if (r.status !== 0) {
    console.error(`\nbuild: "${cmd} ${args.join(' ')}" failed`);
    process.exit(r.status ?? 1);
  }
}

// 1. The docs (Astro, base /docs).
const astro = path.join(DOCS, 'node_modules', 'astro', 'bin', 'astro.mjs');
if (!fs.existsSync(astro)) {
  console.error('build: docs dependencies are missing. Run: npm ci --prefix web/docs-site');
  process.exit(1);
}
run(process.execPath, [astro, 'build'], DOCS);

// 2. Fresh dist with the landing files.
fs.rmSync(DIST, { recursive: true, force: true });
fs.mkdirSync(DIST, { recursive: true });
for (const entry of fs.readdirSync(WEB, { withFileTypes: true })) {
  if (NOT_STATIC.has(entry.name) || entry.name.startsWith('.')) continue;
  fs.cpSync(path.join(WEB, entry.name), path.join(DIST, entry.name), { recursive: true });
}

// 3. The docs under /docs.
const docsDist = path.join(DOCS, 'dist');
fs.cpSync(docsDist, path.join(DIST, 'docs'), { recursive: true });

// Vercel serves /404.html for every unknown URL on the site, so give the whole
// site the docs' friendly 404 (it uses absolute /docs/_astro asset paths).
fs.copyFileSync(path.join(DIST, 'docs', '404.html'), path.join(DIST, '404.html'));

// 4. One sitemap for the site. The docs' own sitemap files are removed so
// crawlers see a single source, the one robots.txt names.
const landing = fs.readFileSync(path.join(WEB, 'sitemap.xml'), 'utf8');
const docsSitemap = fs.readFileSync(path.join(DIST, 'docs', 'sitemap-0.xml'), 'utf8');
fs.writeFileSync(path.join(DIST, 'sitemap.xml'), mergeSitemap(landing, docsSitemap));
for (const f of fs.readdirSync(path.join(DIST, 'docs'))) {
  if (/^sitemap-.*\.xml$/.test(f)) fs.rmSync(path.join(DIST, 'docs', f));
}

// 5. A Markdown twin of every docs page: /docs/<page>.md (the docs home is
// /docs/index.md). vercel.json serves them as text/markdown with noindex;
// they are not in the sitemap. A component the converter does not know is an
// error here, so raw JSX can never ship in a twin.
let twins;
try {
  twins = buildTwins();
} catch (e) {
  console.error(`\nbuild: Markdown twin: ${e.message}`);
  process.exit(1);
}
for (const t of twins) {
  const file = path.join(DIST, t.rel);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, t.text);
}

// 6. llms.txt and llms-full.txt from the same pages.
fs.writeFileSync(path.join(DIST, 'llms.txt'), buildLlms());
fs.writeFileSync(path.join(DIST, 'llms-full.txt'), buildLlmsFull());

// 7. Every page has its twin, no twin has JSX left, and every link in the
// twins and in llms.txt is absolute and points at something that exists.
const problems = verifyTwins(DIST);
if (problems.length) {
  for (const p of problems) console.error(`  FAIL  ${p}`);
  console.error(`\nbuild: ${problems.length} problem(s) in the Markdown twins or llms.txt`);
  process.exit(1);
}

const count = (dir) => fs.readdirSync(dir, { recursive: true }).filter((f) => f.endsWith('.html')).length;
console.log(`\nbuild: web/dist ready (${count(DIST)} HTML files, ${twins.length} Markdown twins, docs under /docs)`);
