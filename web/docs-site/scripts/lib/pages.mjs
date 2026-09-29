// Reads every docs page from src/content/docs without Astro, so the build
// scripts (sitemap merge, llms.txt, checks) share one view of the content.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import YAML from 'yaml';

import { BASE, SITE } from '../../site.config.mjs';

export const SITE_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
export const CONTENT_DIR = path.join(SITE_ROOT, 'src', 'content', 'docs');

/** All content files, recursively, as absolute paths. */
function walk(dir) {
  const out = [];
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) out.push(...walk(p));
    else if (/\.(md|mdx)$/.test(e.name)) out.push(p);
  }
  return out;
}

/** Splits `---` frontmatter from the body. */
export function splitFrontmatter(text) {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(text);
  if (!m) return { data: {}, body: text };
  return { data: YAML.parse(m[1]) ?? {}, body: m[2] };
}

/** yyyy-mm-dd for a Date or a date-like string, else undefined. */
function isoDay(v) {
  if (v instanceof Date && !Number.isNaN(v.getTime())) return v.toISOString().slice(0, 10);
  if (typeof v === 'string' && /^\d{4}-\d{2}-\d{2}/.test(v)) return v.slice(0, 10);
  return undefined;
}

/**
 * Every page: id (content id), url path with base and no trailing slash,
 * absolute url, frontmatter, body, and a truthful lastmod (frontmatter
 * `lastUpdated`, else a blog post's `date`, else none).
 */
export function loadPages() {
  const pages = [];
  for (const file of walk(CONTENT_DIR).sort()) {
    const rel = path.relative(CONTENT_DIR, file).split(path.sep).join('/');
    const id = rel.replace(/\.(md|mdx)$/, '');
    const { data, body } = splitFrontmatter(fs.readFileSync(file, 'utf8'));
    if (data.draft === true) continue;
    const clean = id.replace(/\/index$/, '').replace(/^index$/, '');
    const urlPath = clean ? `${BASE}/${clean}` : BASE;
    const section = id === 'index' ? 'home' : id.split('/')[0];
    pages.push({
      id,
      file,
      section,
      urlPath,
      url: `${SITE}${urlPath}`,
      data,
      body,
      title: String(data.title ?? ''),
      description: String(data.description ?? ''),
      lastmod: isoDay(data.lastUpdated) ?? (section === 'blog' ? isoDay(data.date) : undefined),
    });
  }
  return pages;
}
