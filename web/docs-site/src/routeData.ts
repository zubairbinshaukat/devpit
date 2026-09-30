/**
 * Route middleware: runs for every docs page (and the pages starlight-blog
 * injects) right before rendering. It adds everything Starlight does not:
 *
 *   - social images (og:image and twitter:image) and a few link tags shared
 *     with the landing page (icons, manifest, theme colour)
 *   - JSON-LD: BreadcrumbList on every page, SoftwareApplication on the docs
 *     home, FAQPage when the page has a `faq` array, TechArticle on
 *     troubleshooting pages, and extra fields on starlight-blog's BlogPosting
 *   - the data for the "Related" block rendered by Footer.astro
 *
 * The JSON-LD it emits is validated after every build by
 * scripts/check-jsonld.mjs, so changes here are caught by `npm run check`.
 */
import { defineRouteMiddleware } from '@astrojs/starlight/route-data';
import { getEntry } from 'astro:content';

// The body font's Latin subset. Preloading it starts the download with the HTML
// instead of after the CSS, which is what keeps the first paint on the real font.
import geistLatin from '@fontsource-variable/geist/files/geist-latin-wght-normal.woff2?url';

import { BASE, SITE } from '../site.config.mjs';
import { cleanHref, hrefFor } from './lib/href';

type HeadEntry = { tag: string; attrs?: Record<string, string | boolean | undefined>; content?: string };
type Json = Record<string, unknown>;

const PERSON_ID = 'https://zubyr.dev/#person';
const APP_ID = `${SITE}/#app`;
const DEFAULT_OG = `${SITE}/og-image.png`;
/** Pages starlight-blog injects; they are not content entries, so they get the default social image. */
const BLOG_VIRTUAL = /^blog(\/(\d+|tags\/.+|authors\/.+))?$/;

/** Where else the author is on the web; the same list the landing page publishes. */
const PERSON_LINKS = [
  'https://zubyr.dev',
  'https://github.com/zubairbinshaukat',
  'https://www.linkedin.com/in/zubairbinshaukat',
  'https://x.com/zubyrdev',
];

const person: Json = {
  '@type': 'Person',
  '@id': PERSON_ID,
  name: 'Zubair bin Shaukat',
  alternateName: ['Zubair Bin Shaukat', 'zubyr'],
  url: 'https://zubyr.dev',
  sameAs: PERSON_LINKS,
};


/** Google shows about 60 characters of a title; longer ones are cut. */
const TITLE_BUDGET = 60;

/**
 * The <title> text: the page title plus the longest site suffix that keeps it
 * within [TITLE_BUDGET]. Never cuts the title itself.
 */
function pageTitle(title: string): string {
  for (const suffix of [' | Devpit Docs', ' | Devpit']) {
    if (title.length + suffix.length <= TITLE_BUDGET) return `${title}${suffix}`;
  }
  return title;
}

function metaHas(head: HeadEntry[], key: 'name' | 'property', value: string): boolean {
  return head.some((h) => h.tag === 'meta' && h.attrs?.[key] === value);
}

function addMeta(head: HeadEntry[], key: 'name' | 'property', value: string, content: string) {
  if (metaHas(head, key, value)) return;
  head.push({ tag: 'meta', attrs: { [key]: value, content } });
}

function parseLd(entry: HeadEntry): Json | undefined {
  if (entry.tag !== 'script' || entry.attrs?.type !== 'application/ld+json' || !entry.content) return undefined;
  try {
    return JSON.parse(entry.content) as Json;
  } catch {
    return undefined;
  }
}

/** All typed nodes in a parsed JSON-LD document (handles a bare node or an @graph). */
function nodesOf(doc: Json): Json[] {
  const graph = doc['@graph'];
  return Array.isArray(graph) ? (graph as Json[]) : [doc];
}

function hasType(node: Json, type: string): boolean {
  const t = node['@type'];
  return Array.isArray(t) ? t.includes(type) : t === type;
}

function crumb(position: number, name: string, item?: string): Json {
  return { '@type': 'ListItem', position, name, ...(item ? { item } : {}) };
}

export const onRequest = defineRouteMiddleware(async (context, next) => {
  // Let every other middleware (starlight-blog's) finish first, so we can see
  // what they already put in <head> and avoid duplicating it.
  await next();

  const route = context.locals.starlightRoute;
  const { entry, head } = route;
  // The docs home has an empty id at render time but is content id "index" in the collection.
  const id = entry.id === '' ? 'index' : entry.id;
  // The icon files live at the site root (the landing page owns them). This
  // also runs for the 404 page, which is served for every unknown URL.
  for (const h of head) {
    if (h.tag === 'link' && h.attrs?.rel === 'shortcut icon') h.attrs.href = '/favicon.ico';
  }

  // build.format "file" makes Starlight emit ".../foo.html" links. Vercel
  // cleanUrls would 308 every one of them, so rewrite the sidebar, the
  // previous/next links and the site title link to the clean form.
  const cleanSidebar = (entries: typeof route.sidebar): void => {
    for (const e of entries) {
      if (e.type === 'link') e.href = cleanHref(e.href);
      else cleanSidebar(e.entries);
    }
  };
  // Blog pages swap the docs sidebar for the blog's own (starlight-blog builds
  // it before this runs); start it with the way back to the docs.
  if (id === 'blog' || id.startsWith('blog/')) {
    route.sidebar.unshift({
      type: 'link',
      label: 'Back to docs',
      href: BASE,
      isCurrent: false,
      badge: undefined,
      attrs: { class: 'dp-sidebar-back' },
    });
  }
  cleanSidebar(route.sidebar);
  if (route.pagination.prev) route.pagination.prev.href = cleanHref(route.pagination.prev.href);
  if (route.pagination.next) route.pagination.next.href = cleanHref(route.pagination.next.href);
  route.siteTitleHref = cleanHref(route.siteTitleHref);
  if (id === '404') {
    // The 404 page is served for every unknown URL, so it has no address of its
    // own: no canonical, no og:url, not indexable, not in the sitemap.
    for (let i = head.length - 1; i >= 0; i--) {
      const h = head[i]!;
      const drop =
        (h.tag === 'link' && (h.attrs?.rel === 'canonical' || h.attrs?.rel === 'sitemap')) ||
        (h.tag === 'meta' && h.attrs?.property === 'og:url');
      if (drop) head.splice(i, 1);
    }
    addMeta(head, 'name', 'robots', 'noindex, follow');
    addMeta(head, 'property', 'og:image', DEFAULT_OG);
    addMeta(head, 'property', 'og:image:alt', 'Devpit, the free Windows terminal app for developers');
    addMeta(head, 'property', 'og:image:width', '1200');
    addMeta(head, 'property', 'og:image:height', '630');
    addMeta(head, 'name', 'twitter:image', DEFAULT_OG);
    return;
  }

  const data = entry.data;
  // With build.format "file" Starlight leaves ".html" in the canonical URL.
  // Vercel serves the clean form, so rewrite it from the request path.
  const cleanPath = context.url.pathname.replace(/(\/index)?(\.html)?\/?$/, '') || BASE;
  const pageUrl = new URL(cleanPath, SITE).href;
  for (const h of head) {
    if (h.tag === 'link' && h.attrs?.rel === 'canonical') h.attrs.href = pageUrl;
    if (h.tag === 'meta' && h.attrs?.property === 'og:url') h.attrs.content = pageUrl;
  }
  const isHome = id === 'index';
  const isBlogVirtual = BLOG_VIRTUAL.test(id);
  const isBlogPost = id.startsWith('blog/') && !isBlogVirtual;
  const isTroubleshooting = id.startsWith('troubleshooting/');

  // ---- titles, descriptions and indexing for the pages starlight-blog injects
  // Tag, author and pagination pages get the site-wide description and a bare
  // title ("ports | Devpit Docs") by default. Give each one its own, and keep
  // the thin ones (one or a few posts) out of the index and the sitemap: they
  // add near-duplicate pages, not new answers. Links on them are still followed.
  let robots = 'index, follow, max-image-preview:large';
  if (isBlogVirtual) {
    const label = String(data.title);
    const page = /^blog\/(\d+)$/.exec(id);
    let blogTitle: string;
    let desc: string;
    if (id === 'blog') {
      blogTitle = 'Devpit blog: Windows developer guides and fixes';
      desc = 'Short guides and fixes for Windows developers from the Devpit blog: disk space, stuck ports, updates, Git, SSH and file sharing.';
    } else if (page) {
      blogTitle = `Devpit blog, page ${page[1]}`;
      desc = `More short guides and fixes for Windows developers from the Devpit blog, page ${page[1]}: disk space, ports, updates and more.`;
      robots = 'noindex, follow';
    } else if (id.startsWith('blog/tags/')) {
      blogTitle = `Devpit blog posts tagged ${label}`;
      desc = `Every Devpit blog post tagged ${label}: short guides and fixes for Windows developers, with steps you can follow.`;
      robots = 'noindex, follow';
    } else {
      blogTitle = `Posts by ${label} | Devpit blog`;
      desc = `Posts by ${label}, the maker of Devpit: guides and fixes for Windows developers, written in plain words.`;
      robots = 'noindex, follow';
    }
    for (const h of head) {
      if (h.tag === 'title') h.content = pageTitle(blogTitle);
      if (h.tag === 'meta' && h.attrs?.property === 'og:title') h.attrs.content = blogTitle;
      if (h.tag === 'meta' && (h.attrs?.name === 'description' || h.attrs?.property === 'og:description')) {
        h.attrs.content = desc;
      }
    }
  } else {
    // Starlight appends " | Devpit Docs". Keep the whole <title> within about
    // 60 characters: use " | Devpit" when that is shorter and fits, else none.
    // A page with a long H1 (a full error message) can give a short `seoTitle`.
    const short = data.seoTitle ? String(data.seoTitle) : undefined;
    for (const h of head) {
      if (h.tag === 'title' && typeof h.content === 'string') {
        h.content = pageTitle(short ?? h.content.replace(/ \| Devpit Docs$/, ''));
      }
      if (short && h.tag === 'meta' && h.attrs?.property === 'og:title') h.attrs.content = short;
    }
  }

  // ---- social image -------------------------------------------------------
  const image = isBlogVirtual ? `${SITE}${BASE}/og/blog.png` : `${SITE}${BASE}/og/${id}.png`;
  const imageAlt = `${data.title} | Devpit docs`;
  addMeta(head, 'property', 'og:image', image);
  addMeta(head, 'property', 'og:image:type', 'image/png');
  addMeta(head, 'property', 'og:image:width', '1200');
  addMeta(head, 'property', 'og:image:height', '630');
  addMeta(head, 'property', 'og:image:alt', imageAlt);
  addMeta(head, 'name', 'twitter:image', image);
  addMeta(head, 'name', 'twitter:image:alt', imageAlt);
  addMeta(head, 'name', 'twitter:creator', '@zubyrdev');
  addMeta(head, 'name', 'author', 'Zubair bin Shaukat');
  addMeta(head, 'name', 'robots', robots);
  if (isHome) {
    // Starlight sets og:type to "article" everywhere; the docs home is a website.
    const t = head.find((h) => h.tag === 'meta' && h.attrs?.property === 'og:type');
    if (t?.attrs) t.attrs.content = 'website';
  }

  // ---- links shared with the landing page ---------------------------------
  head.push(
    { tag: 'link', attrs: { rel: 'preload', as: 'font', type: 'font/woff2', href: geistLatin, crossorigin: '' } },
    // Google Search only shows a favicon that is a multiple of 48px square.
    { tag: 'link', attrs: { rel: 'icon', href: '/icon-192.png', type: 'image/png', sizes: '192x192' } },
    { tag: 'link', attrs: { rel: 'icon', href: '/favicon-32.png', type: 'image/png', sizes: '32x32' } },
    { tag: 'link', attrs: { rel: 'apple-touch-icon', href: '/apple-touch-icon.png' } },
    { tag: 'link', attrs: { rel: 'manifest', href: '/site.webmanifest' } },
    { tag: 'meta', attrs: { name: 'theme-color', content: '#11111b', media: '(prefers-color-scheme: dark)' } },
    { tag: 'meta', attrs: { name: 'theme-color', content: '#eff1f5', media: '(prefers-color-scheme: light)' } },
  );
  // One sitemap for the whole site: web/scripts/build.mjs merges the docs
  // sitemap into /sitemap.xml, so point crawlers there.
  for (const h of head) {
    if (h.tag === 'link' && h.attrs?.rel === 'sitemap') h.attrs.href = `${SITE}/sitemap.xml`;
  }

  // ---- JSON-LD ------------------------------------------------------------
  const existing = head.map(parseLd).filter((d): d is Json => d !== undefined);
  const existingNodes = existing.flatMap(nodesOf);
  // Enrich starlight-blog's BlogPosting: it has no publisher that resolves on
  // the page, no dateModified and no default image, and its own breadcrumb has
  // only two levels. Drop that breadcrumb; the one built below covers
  // Devpit > Docs > Blog > post like every other page.
  let alreadyHasBreadcrumb = existingNodes.some((n) => hasType(n, 'BreadcrumbList'));
  if (isBlogPost) {
    for (const h of head) {
      const doc = parseLd(h);
      if (!doc) continue;
      const graphNodes = nodesOf(doc);
      if (!graphNodes.some((n) => hasType(n, 'BlogPosting'))) continue;
      const kept: Json[] = [];
      for (const node of graphNodes) {
        if (hasType(node, 'BreadcrumbList')) continue;
        if (hasType(node, 'BlogPosting')) {
          // A full Person, not a bare @id: the blog author has another @id, so a
          // reference to the site's person would not resolve on this page.
          node.publisher = { ...person };
          node.image ??= [image];
          node.dateModified ??= node.datePublished;
          // Tie the blog author to the same person the landing page describes.
          for (const a of [node.author ?? []].flat() as Json[]) a.sameAs ??= PERSON_LINKS;
        }
        kept.push(node);
      }
      h.content = JSON.stringify({ '@context': 'https://schema.org', '@graph': kept });
    }
    alreadyHasBreadcrumb = false;
  }

  const graph: Json[] = [];

  if (!alreadyHasBreadcrumb) {
    const items: Json[] = [crumb(1, 'Devpit', `${SITE}/`), crumb(2, 'Docs', `${SITE}${BASE}`)];
    if (!isHome) {
      const parts = id.replace(/\/index$/, '').split('/');
      // Parent sections only when they are real pages (features, troubleshooting, blog).
      for (let i = 1; i < parts.length; i++) {
        const parentId = parts.slice(0, i).join('/');
        const parent = await getEntry('docs', parentId);
        if (parent) items.push(crumb(items.length + 1, String(parent.data.title), `${SITE}${hrefFor(parentId)}`));
        else if (parentId === 'blog') items.push(crumb(items.length + 1, 'Devpit blog', `${SITE}${BASE}/blog`));
      }
      items.push(crumb(items.length + 1, String(data.title), pageUrl));
    }
    graph.push({ '@type': 'BreadcrumbList', itemListElement: items });
  }

  if (isHome) {
    // Same entity id and facts as the landing page, so the two never disagree.
    graph.push({
      '@type': 'SoftwareApplication',
      '@id': APP_ID,
      name: 'Devpit',
      alternateName: 'Devpit CLI',
      url: `${SITE}/`,
      description:
        'Free, open-source terminal app (CLI) for Windows that frees disk space from developer junk, fixes stuck ports and updates developer tools from one menu.',
      applicationCategory: 'DeveloperApplication',
      operatingSystem: 'Windows 10, Windows 11',
      author: { '@id': PERSON_ID },
      license: 'https://opensource.org/licenses/MIT',
      downloadUrl: 'https://github.com/zubairbinshaukat/devpit/releases/latest',
      installUrl: `${SITE}/install`,
      isAccessibleForFree: true,
      offers: { '@type': 'Offer', price: '0', priceCurrency: 'USD' },
      image: `${SITE}/logo.png`,
      screenshot: DEFAULT_OG,
    });
  }

  const wantsArticle = (data.schemaType ?? (isTroubleshooting ? 'TechArticle' : 'none')) !== 'none';
  if (wantsArticle && !isBlogPost) {
    const type = data.schemaType && data.schemaType !== 'none' ? data.schemaType : 'TechArticle';
    const modified = data.lastUpdated instanceof Date ? data.lastUpdated : undefined;
    graph.push({
      '@type': type,
      headline: String(data.seoTitle ?? data.title).slice(0, 110),
      description: data.description,
      url: pageUrl,
      mainEntityOfPage: pageUrl,
      inLanguage: 'en',
      image: [image],
      author: person,
      publisher: { '@id': PERSON_ID },
      ...(data.published ? { datePublished: data.published.toISOString().slice(0, 10) } : {}),
      ...(modified ? { dateModified: modified.toISOString().slice(0, 10) } : {}),
    });
  }

  if (data.faq?.length) {
    graph.push({
      '@type': 'FAQPage',
      '@id': `${pageUrl}#faq`,
      mainEntity: data.faq.map((qa) => ({
        '@type': 'Question',
        name: qa.question,
        acceptedAnswer: { '@type': 'Answer', text: qa.answer },
      })),
    });
  }

  if (graph.length) {
    head.push({
      tag: 'script',
      attrs: { type: 'application/ld+json' },
      content: JSON.stringify({ '@context': 'https://schema.org', '@graph': graph }),
    });
  }

  // ---- Related block ------------------------------------------------------
  if (data.related?.length) {
    const related = [];
    for (const slug of data.related) {
      const e = await getEntry('docs', slug).catch(() => undefined);
      if (!e) throw new Error(`${id}: related slug "${slug}" does not exist`);
      related.push({ href: hrefFor(e.id), title: String(e.data.title), description: e.data.description ?? '' });
    }
    context.locals.devpitRelated = related;
  }
});
