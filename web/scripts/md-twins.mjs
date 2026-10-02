// Markdown twins: every docs, troubleshooting and blog page also served as
// plain Markdown at the same URL plus ".md", so an AI agent can read one
// small page instead of parsing HTML or pulling llms-full.txt.
//
//   https://devpit.zubyr.dev/docs/features/accounts      the HTML page
//   https://devpit.zubyr.dev/docs/features/accounts.md   its Markdown twin
//   https://devpit.zubyr.dev/docs/index.md               the docs home
//
// The twins are made from the content sources (src/content/docs), not from
// the built HTML: the sources already are Markdown, so converting the few MDX
// parts is simpler and more faithful than turning Starlight's HTML (sidebar,
// expressive-code markup, pictures) back into Markdown.
//
// web/scripts/build.mjs writes them into web/dist/docs and then runs
// verifyTwins(); docs-site/scripts/check-markdown.mjs runs the same checks.
// gen-llms.mjs builds llms-full.txt from the same conversion, so both agree.
//
// Every component the site uses is converted below. An unknown component, an
// expression attribute, an `export`, or any HTML/JSX tag left over is a build
// error, so new MDX can never leak raw JSX into a twin.
import fs from 'node:fs';
import path from 'node:path';

import { SITE_ROOT, loadPages } from '../docs-site/scripts/lib/pages.mjs';
import { BASE, SITE } from '../docs-site/site.config.mjs';

/** Path of a page's twin relative to the site root (web/dist). */
export const twinRel = (p) => `${BASE.slice(1)}/${p.id}.md`;
/** Public URL of a page's twin. */
export const twinUrl = (p) => `${SITE}${BASE}/${p.id}.md`;

const oneLine = (s) => String(s ?? '').replace(/\s+/g, ' ').trim();

/** Screenshot alt text by name, from shots.json and shots.content.json. */
function loadShots() {
  const alts = new Map();
  for (const f of fs.readdirSync(SITE_ROOT).filter((n) => /^shots(\..+)?\.json$/.test(n))) {
    for (const s of JSON.parse(fs.readFileSync(path.join(SITE_ROOT, f), 'utf8'))) alts.set(s.name, s.alt);
  }
  return alts;
}

// ---- code protection --------------------------------------------------------
// Fenced blocks and inline code are swapped for placeholders before anything
// is converted, so nothing inside code is ever rewritten.

const PH = (n) => `\u0000${n}\u0000`;

/** Replaces code with placeholders. Returns { text, store }. */
export function protect(src) {
  const store = [];
  const lines = src.split(/\r?\n/);
  const out = [];
  for (let i = 0; i < lines.length; i++) {
    const m = /^(\s*)(`{3,}|~{3,})/.exec(lines[i]);
    if (!m) {
      out.push(lines[i]);
      continue;
    }
    const [, indent, fence] = m;
    const close = new RegExp(`^\\s*${fence[0] === '`' ? '`' : '~'}{${fence.length},}\\s*$`);
    const block = [lines[i]];
    let j = i + 1;
    while (j < lines.length && !close.test(lines[j])) block.push(lines[j++]);
    if (j >= lines.length) throw new Error(`unclosed code fence: ${lines[i].trim()}`);
    block.push(lines[j]);
    // The placeholder keeps the indentation; restore() puts it back on every line.
    store.push(block.map((l) => (l.startsWith(indent) ? l.slice(indent.length) : l.trimStart())).join('\n'));
    out.push(indent + PH(store.length - 1));
    i = j;
  }
  const text = out.join('\n').replace(/(`+)(?!`)([^\n]*?[^`\n])\1(?!`)/g, (code) => {
    store.push(code);
    return PH(store.length - 1);
  });
  return { text, store };
}

/** Puts code back. A block on a line that starts with ">" or spaces gets that prefix on every line. */
export function restore(text, store) {
  return text.replace(/\u0000(\d+)\u0000/g, (_, n, offset, s) => {
    const code = store[Number(n)];
    if (!code.includes('\n')) return code;
    const before = s.slice(s.lastIndexOf('\n', offset - 1) + 1, offset);
    const prefix = /^[\s>]*$/.test(before) ? before : '';
    return code
      .split('\n')
      .map((l, i) => (i === 0 ? l : l ? prefix + l : prefix.trimEnd()))
      .join('\n');
  });
}

// ---- JSX --------------------------------------------------------------------

/** Parses the attributes of a tag starting at `pos` (just after the name). */
function parseTag(text, pos, name) {
  const attrs = {};
  let i = pos;
  for (;;) {
    while (/\s/.test(text[i] ?? '')) i++;
    if (i >= text.length) throw new Error(`<${name}> is not closed`);
    if (text.startsWith('/>', i)) return { attrs, end: i + 2, selfClosing: true };
    if (text[i] === '>') return { attrs, end: i + 1, selfClosing: false };
    const m = /^[A-Za-z_:][\w:.-]*/.exec(text.slice(i));
    if (!m) throw new Error(`<${name}>: cannot read attributes near "${text.slice(i, i + 20)}"`);
    const key = m[0];
    i += key.length;
    if (text[i] !== '=') {
      attrs[key] = true;
      continue;
    }
    i++;
    const q = text[i];
    if (q === '"' || q === "'") {
      const end = text.indexOf(q, i + 1);
      attrs[key] = text.slice(i + 1, end);
      i = end + 1;
    } else if (q === '{') {
      let depth = 0;
      let j = i;
      for (; j < text.length; j++) {
        if (text[j] === '{') depth++;
        else if (text[j] === '}' && --depth === 0) break;
      }
      const expr = text.slice(i + 1, j).trim();
      if (/^(true|false)$/.test(expr)) attrs[key] = expr === 'true';
      else if (/^-?\d+(\.\d+)?$/.test(expr)) attrs[key] = Number(expr);
      else if (/^(["'])[^"']*\1$/.test(expr)) attrs[key] = expr.slice(1, -1);
      else throw new Error(`<${name} ${key}={${expr}}>: expression attributes cannot be turned into Markdown; use a plain string`);
      i = j + 1;
    } else {
      throw new Error(`<${name}>: unquoted attribute ${key}`);
    }
  }
}

/** Finds the matching </name> from `pos`, counting nested <name> tags. */
function findClose(text, pos, name) {
  const re = new RegExp(`<(/?)${name.replace('.', '\\.')}(?=[\\s/>])`, 'g');
  re.lastIndex = pos;
  let depth = 1;
  for (let m; (m = re.exec(text)); ) {
    if (m[1]) {
      if (--depth === 0) return { start: m.index, end: text.indexOf('>', m.index) + 1 };
    } else if (!parseTag(text, m.index + m[0].length, name).selfClosing) depth++;
  }
  throw new Error(`<${name}> has no closing </${name}>`);
}

/** Removes the common indentation of a block of lines. */
function dedent(s) {
  const lines = s.replace(/^\n+|\s+$/g, '').split('\n');
  const widths = lines.filter((l) => l.trim()).map((l) => /^[ \t]*/.exec(l)[0].length);
  const cut = widths.length ? Math.min(...widths) : 0;
  return lines.map((l) => l.slice(cut)).join('\n');
}

const block = (s) => `\n\n${s.trim()}\n\n`;

/** One paragraph per item, joined into a list item's text. */
function itemText(s) {
  return dedent(s)
    .split(/\n\s*\n/)
    .map((para) => para.split('\n').map((l) => l.trim()).join(' '))
    .join('\n\n  ');
}

const ASIDE_LABEL = { note: 'Note', tip: 'Tip', caution: 'Caution', danger: 'Danger' };

function quote(kind, title, body) {
  const label = ASIDE_LABEL[kind];
  if (!label) throw new Error(`unknown aside type "${kind}"`);
  const head = `**${title ? `${label}: ${title}` : label}**`;
  return [head, '', ...dedent(body).split('\n')].map((l) => (l ? `> ${l}` : '>')).join('\n');
}

/**
 * Every component the site uses, and the Starlight ones that are easy to
 * read as Markdown. Anything else fails the build.
 */
const COMPONENTS = {
  // A screenshot: its alt text from the manifests, in italics.
  Shot(a, _children, ctx) {
    const alt = a.alt ?? ctx.shots.get(a.name);
    if (!alt) throw new Error(`<Shot name="${a.name}"> is not in a shots manifest`);
    return `*Screenshot: ${oneLine(alt)}${a.caption ? ` (${oneLine(a.caption)})` : ''}*`;
  },
  // Hub pages: the same list SectionList.astro renders, in the same order.
  SectionList(a, _children, ctx) {
    const list = ctx.pages
      .filter((p) => p.id.startsWith(`${a.dir}/`))
      .sort(
        (x, y) =>
          (x.data.sidebar?.order ?? 1000) - (y.data.sidebar?.order ?? 1000) ||
          String(x.data.sidebar?.label ?? x.title).localeCompare(String(y.data.sidebar?.label ?? y.title)),
      );
    if (!list.length) throw new Error(`<SectionList dir="${a.dir}"> lists no pages`);
    return block(list.map((p) => `- [${p.title}](${p.url}): ${oneLine(p.description)}`).join('\n'));
  },
  // A grid of cards becomes one list; each card is one item.
  CardGrid: (_a, children) => block(dedent(children).replace(/\n\s*\n(?=- )/g, '\n')),
  Card: (a, children) => `- **${a.title}**: ${itemText(children)}`,
  LinkCard: (a, _children, ctx) => `- [${a.title}](${ctx.abs(a.href)})${a.description ? `: ${oneLine(a.description)}` : ''}`,
  Aside: (a, children) => block(quote(a.type ?? 'note', a.title, children)),
  Steps: (_a, children) => block(dedent(children)),
  Tabs: (_a, children) => block(children),
  TabItem: (a, children) => block(`**${a.label}**\n\n${dedent(children)}`),
  Badge: (a) => `(${a.text})`,
  LinkButton: (a, children, ctx) => `[${oneLine(children)}](${ctx.abs(a.href)})`,
};

function convertJsx(text, ctx) {
  let out = '';
  let i = 0;
  const re = /<([A-Z][A-Za-z0-9.]*)(?=[\s/>])/g;
  for (;;) {
    re.lastIndex = i;
    const m = re.exec(text);
    if (!m) return out + text.slice(i);
    out += text.slice(i, m.index);
    const name = m[1];
    const handler = COMPONENTS[name];
    if (!handler) throw new Error(`unknown component <${name}>: add a Markdown conversion for it in web/scripts/md-twins.mjs`);
    const tag = parseTag(text, m.index + m[0].length, name);
    let children = '';
    let after = tag.end;
    if (!tag.selfClosing) {
      const close = findClose(text, tag.end, name);
      children = convertJsx(text.slice(tag.end, close.start), ctx);
      after = close.end;
    }
    const md = handler(tag.attrs, children, ctx);
    // A component inside a list item keeps the item's indentation on every line.
    const before = out.slice(out.lastIndexOf('\n') + 1);
    const prefix = /^[ \t]*$/.test(before) ? before : '';
    out += md
      .split('\n')
      .map((l, k) => (k === 0 || !l ? l : prefix + l))
      .join('\n');
    i = after;
  }
}

// ---- the whole page ---------------------------------------------------------

function asides(text) {
  const out = [];
  let open = null;
  for (const line of text.split('\n')) {
    const start = /^\s*:::(\w+)(?:\[([^\]]*)\])?(?:\{[^}]*\})?\s*$/.exec(line);
    if (!open && start) {
      open = { kind: start[1], title: start[2], body: [] };
    } else if (open && /^\s*:::\s*$/.test(line)) {
      out.push('', quote(open.kind, open.title, open.body.join('\n')), '');
      open = null;
    } else if (open) open.body.push(line);
    else out.push(line);
  }
  if (open) throw new Error(`:::${open.kind} is not closed`);
  return out.join('\n');
}

/** Makes one link target absolute. Throws on a relative one. */
function absolute(target, page) {
  if (/^(https?:|mailto:)/.test(target)) return target;
  if (target.startsWith('//')) return `https:${target}`;
  if (target.startsWith('/')) return `${SITE}${target}`;
  if (target.startsWith('#')) return `${page.url}${target}`;
  throw new Error(`relative link "${target}": write it root-relative, /docs/...`);
}

/** The Markdown body of one page (MDX parts converted, links absolute). */
function convertBody(page, ctx) {
  const { text: guarded, store } = protect(page.body);
  const abs = (t) => absolute(String(t), page);
  let t = guarded
    .replace(/^import\s[\s\S]*?\sfrom\s+['"][^'"]+['"];?[ \t]*$/gm, '')
    .replace(/^import\s+['"][^'"]+['"];?[ \t]*$/gm, '')
    .replace(/\{\/\*[\s\S]*?\*\/\}/g, '');
  if (/^export\s/m.test(t)) throw new Error('`export` in MDX cannot be turned into Markdown');
  t = convertJsx(t, { ...ctx, abs });
  t = t.replace(/<kbd>([\s\S]*?)<\/kbd>/g, (_, k) => `\`${k}\``);
  const left = /<\/?[A-Za-z][\w.:-]*(\s[^<>]*)?\/?>/.exec(t);
  if (left) throw new Error(`HTML or JSX left in the Markdown: ${left[0]}`);
  t = asides(t)
    .replace(/\\([{}])/g, '$1')
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, (_, alt) => `*Image: ${oneLine(alt)}*`)
    .replace(/\]\(([^)\s]+)((?:\s+"[^"]*")?)\)/g, (_, target, title) => `](${abs(target)}${title})`)
    .replace(/^(\s*\[[^\]]+\]:\s*)(\S+)/gm, (_, lead, target) => `${lead}${abs(target)}`);
  return restore(t, store)
    .replace(/[ \t]+$/gm, '')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

/** The full Markdown twin of one page. */
export function pageMarkdown(page, ctx) {
  const parts = [`# ${oneLine(page.title)}`, '', oneLine(page.description), ''];
  if (page.section === 'blog' && page.data.date) {
    const d = page.data.date instanceof Date ? page.data.date.toISOString().slice(0, 10) : String(page.data.date).slice(0, 10);
    parts.push(`*Published ${d}.*`, '');
  }
  const body = convertBody(page, ctx);
  if (body) parts.push(body, '');
  const faq = Array.isArray(page.data.faq) ? page.data.faq : [];
  if (faq.length) {
    parts.push('## Common questions', '');
    for (const qa of faq) parts.push(`### ${oneLine(qa.question)}`, '', oneLine(qa.answer), '');
  }
  const related = Array.isArray(page.data.related) ? page.data.related : [];
  if (related.length) {
    parts.push('## Related', '');
    for (const id of related) {
      const r = ctx.byId.get(id);
      if (!r) throw new Error(`related page "${id}" does not exist`);
      parts.push(`- [${oneLine(r.title)}](${r.url}): ${oneLine(r.description)}`);
    }
    parts.push('');
  }
  parts.push('---', '', `Canonical page: ${page.url}`, '');
  return parts.join('\n');
}

/** Every page with its twin: [{ page, rel, url, text }]. Throws with the page id on a conversion error. */
export function buildTwins(pages = loadPages()) {
  const ctx = { pages, byId: new Map(pages.map((p) => [p.id, p])), shots: loadShots() };
  return pages.map((page) => {
    try {
      return { page, rel: twinRel(page), url: twinUrl(page), text: pageMarkdown(page, ctx) };
    } catch (e) {
      throw new Error(`${page.id}: ${e.message}`);
    }
  });
}

// ---- checks -----------------------------------------------------------------

/**
 * Resolves a URL on this site to a file in web/dist, the way Vercel serves it
 * (cleanUrls, the /install rewrite). Returns the file path or null.
 */
function resolveSiteUrl(dist, url) {
  const u = new URL(url);
  let p = decodeURIComponent(u.pathname);
  if (p === '/install') p = '/install.ps1';
  const candidates = p === '/' ? ['/index.html'] : [p, `${p}.html`, `${p}/index.html`];
  for (const c of candidates) {
    const f = path.join(dist, c);
    if (fs.existsSync(f) && fs.statSync(f).isFile()) return f;
  }
  return null;
}

const htmlIds = new Map();
function hasAnchor(file, id) {
  if (!htmlIds.has(file)) {
    const html = fs.readFileSync(file, 'utf8');
    htmlIds.set(file, new Set([...html.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])));
  }
  return htmlIds.get(file).has(decodeURIComponent(id));
}

/** Every link in a Markdown text, outside code. */
function linksOf(md) {
  const { text } = protect(md);
  return [...text.matchAll(/\]\(([^)\s]+)/g)].map((m) => m[1]);
}

/** Problems with one link target: must be absolute and, on this site, exist. */
function checkLink(dist, target) {
  if (!/^(https?:|mailto:)/.test(target)) return `link "${target}" is not absolute`;
  if (!target.startsWith(`${SITE}/`) && target !== SITE) return null;
  const file = resolveSiteUrl(dist, target);
  if (!file) return `link ${target} points to a page that does not exist`;
  const hash = new URL(target).hash.slice(1);
  if (hash && file.endsWith('.html') && !hasAnchor(file, hash)) return `link ${target}: no #${hash} on that page`;
  return null;
}

/**
 * Checks the twins in web/dist and the links in llms.txt and llms-full.txt.
 * Returns a list of problems (empty when all is well).
 */
export function verifyTwins(dist, pages = loadPages()) {
  const errors = [];
  for (const p of pages) {
    const file = path.join(dist, twinRel(p));
    if (!fs.existsSync(file)) {
      errors.push(`${p.id}: no Markdown twin at /${twinRel(p)}`);
      continue;
    }
    const md = fs.readFileSync(file, 'utf8');
    const where = `/${twinRel(p)}`;
    const { text } = protect(md);
    if (!md.startsWith(`# ${oneLine(p.title)}\n`)) errors.push(`${where}: does not start with "# ${p.title}"`);
    if (!md.trimEnd().endsWith(p.url)) errors.push(`${where}: last line is not the canonical URL ${p.url}`);
    if (/^\s*(import|export)\s/m.test(text)) errors.push(`${where}: still has an import or export line`);
    const tag = /<\/?[A-Za-z][\w.:-]*(\s[^<>]*)?\/?>/.exec(text);
    if (tag) errors.push(`${where}: still has a tag ${tag[0]}`);
    if (/\{\/\*/.test(text)) errors.push(`${where}: still has an MDX comment`);
    if (/^\s*:::/m.test(text)) errors.push(`${where}: still has a ::: aside`);
    if (/\u0000/.test(md)) errors.push(`${where}: has an unrestored code placeholder`);
    for (const l of linksOf(md)) {
      const problem = checkLink(dist, l);
      if (problem) errors.push(`${where}: ${problem}`);
    }
  }
  for (const f of ['llms.txt', 'llms-full.txt']) {
    const file = path.join(dist, f);
    if (!fs.existsSync(file)) {
      errors.push(`${f}: missing`);
      continue;
    }
    for (const l of linksOf(fs.readFileSync(file, 'utf8'))) {
      const problem = checkLink(dist, l);
      if (problem) errors.push(`${f}: ${problem}`);
    }
  }
  return errors;
}
