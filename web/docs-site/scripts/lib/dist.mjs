// Shared helpers for the checks that read the built docs (docs-site/dist).
import fs from 'node:fs';
import path from 'node:path';
import { parse } from 'node-html-parser';

import { BASE, SITE } from '../../site.config.mjs';
import { SITE_ROOT } from './pages.mjs';

export const DIST = path.join(SITE_ROOT, 'dist');
export const WEB = path.resolve(SITE_ROOT, '..');

/** Collects problems and prints them, so every check reports the same way. */
export class Report {
  constructor(name) {
    this.name = name;
    this.errors = [];
    this.warnings = [];
  }
  error(where, msg) {
    this.errors.push(`${where}: ${msg}`);
  }
  warn(where, msg) {
    this.warnings.push(`${where}: ${msg}`);
  }
  /** Prints the outcome and returns the exit code (0 or 1). */
  finish(summary = '') {
    for (const w of this.warnings) console.warn(`  warn  ${w}`);
    for (const e of this.errors) console.error(`  FAIL  ${e}`);
    const status = this.errors.length ? 'FAILED' : 'ok';
    console.log(`${this.name}: ${status}${summary ? ` (${summary})` : ''}${this.warnings.length ? `, ${this.warnings.length} warning(s)` : ''}`);
    return this.errors.length ? 1 : 0;
  }
}

export function requireDist() {
  if (!fs.existsSync(path.join(DIST, 'index.html'))) {
    console.error('docs-site/dist is missing. Run `npm run build` first.');
    process.exit(2);
  }
}

/** Every built HTML page: { file, rel, urlPath, html, root }. 404 is included. */
export function builtPages() {
  const out = [];
  const walk = (dir) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) {
        // Pagefind writes its own fragment pages; they are not site pages.
        if (e.name === 'pagefind' || e.name === '_astro') continue;
        walk(p);
      } else if (e.name.endsWith('.html')) {
        const rel = path.relative(DIST, p).split(path.sep).join('/');
        const clean = rel.replace(/(^|\/)index\.html$/, '').replace(/\.html$/, '');
        const html = fs.readFileSync(p, 'utf8');
        out.push({ file: p, rel, urlPath: clean ? `${BASE}/${clean}` : BASE, html, root: parse(html) });
      }
    }
  };
  walk(DIST);
  return out.sort((a, b) => a.rel.localeCompare(b.rel));
}

/** The JSON-LD documents of a page. Parse errors are returned as { error }. */
export function jsonLd(page) {
  return page.root.querySelectorAll('script[type="application/ld+json"]').map((s) => {
    try {
      return { data: JSON.parse(s.rawText) };
    } catch (e) {
      return { error: e.message };
    }
  });
}

/** Flattens documents into their typed nodes. */
export function ldNodes(docs) {
  const nodes = [];
  for (const d of docs) {
    if (!d.data) continue;
    const g = d.data['@graph'];
    if (Array.isArray(g)) nodes.push(...g);
    else nodes.push(d.data);
  }
  return nodes;
}

export const typesOf = (node) => [].concat(node['@type'] ?? []);

export { SITE, BASE };
