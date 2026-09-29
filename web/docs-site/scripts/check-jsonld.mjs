// Validates every JSON-LD block in the built docs against what Google's
// structured-data documentation requires or recommends for the types used:
//
//   SoftwareApplication  required: name, and one of offers / aggregateRating /
//                        review. We add applicationCategory and operatingSystem
//                        (recommended) and FORBID aggregateRating and review:
//                        Devpit has no real ratings, and fake ones violate
//                        Google's policy.
//   FAQPage              required: mainEntity of Question(name) with
//                        acceptedAnswer(Answer.text). The questions must also
//                        be visible on the page.
//   BreadcrumbList       required: itemListElement of ListItem(position, name);
//                        item (URL) required on every entry but the last.
//   Article / TechArticle / BlogPosting
//                        no required property; recommended author, headline,
//                        image, datePublished, dateModified. We require
//                        headline (<= 110 chars) and author, and check that
//                        dates are ISO 8601 and image URLs are absolute.
//
// Google's note on FAQ rich results: since 2023 they are shown only for
// well-known government and health sites. The markup is still valid and other
// search engines and assistants read it, so it is kept and validated.
import { BASE, Report, SITE, builtPages, jsonLd, ldNodes, requireDist, typesOf } from './lib/dist.mjs';
import { loadPages } from './lib/pages.mjs';

requireDist();
const r = new Report('check-jsonld');

const ISO = /^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:\d{2})?)?$/;
const isAbs = (u) => typeof u === 'string' && /^https:\/\//.test(u);
const nonEmpty = (s) => typeof s === 'string' && s.trim().length > 0;

const frontmatter = new Map(loadPages().map((p) => [p.urlPath, p]));
let blocks = 0;

for (const page of builtPages()) {
  const where = page.rel;
  if (page.rel === '404.html') continue;
  const docs = jsonLd(page);
  blocks += docs.length;

  for (const d of docs) {
    if (d.error) r.error(where, `JSON-LD does not parse: ${d.error}`);
    else if (d.data['@context'] !== 'https://schema.org') r.error(where, 'JSON-LD @context must be https://schema.org');
  }
  const nodes = ldNodes(docs);
  const ofType = (t) => nodes.filter((n) => typesOf(n).includes(t));
  const canonical = `${SITE}${page.urlPath}`;

  // Breadcrumbs: exactly one per page.
  const crumbs = ofType('BreadcrumbList');
  if (crumbs.length !== 1) r.error(where, `expected exactly one BreadcrumbList, found ${crumbs.length}`);
  for (const c of crumbs) {
    const items = c.itemListElement;
    if (!Array.isArray(items) || items.length < 1) {
      r.error(where, 'BreadcrumbList has no itemListElement');
      continue;
    }
    items.forEach((it, i) => {
      if (!typesOf(it).includes('ListItem')) r.error(where, `breadcrumb ${i + 1} is not a ListItem`);
      if (it.position !== i + 1) r.error(where, `breadcrumb ${i + 1} has position ${it.position}`);
      if (!nonEmpty(it.name)) r.error(where, `breadcrumb ${i + 1} has no name`);
      const last = i === items.length - 1;
      if (!last && !isAbs(it.item)) r.error(where, `breadcrumb ${i + 1} needs an absolute item URL`);
      if (it.item !== undefined && !isAbs(it.item)) r.error(where, `breadcrumb ${i + 1} item is not an absolute URL`);
    });
  }

  // SoftwareApplication: docs home only, honest fields only.
  const apps = ofType('SoftwareApplication');
  if (page.urlPath === BASE && apps.length !== 1) r.error(where, `docs home needs one SoftwareApplication, found ${apps.length}`);
  if (page.urlPath !== BASE && apps.length) r.error(where, 'SoftwareApplication belongs on the docs home only');
  for (const a of apps) {
    if (!nonEmpty(a.name)) r.error(where, 'SoftwareApplication has no name');
    if (!a.offers && !a.aggregateRating && !a.review) r.error(where, 'SoftwareApplication needs offers, aggregateRating or review');
    if (a.aggregateRating || a.review) r.error(where, 'SoftwareApplication must not carry ratings or reviews Devpit does not have');
    if (a.offers) {
      if (a.offers.price === undefined || !nonEmpty(a.offers.priceCurrency)) r.error(where, 'Offer needs price and priceCurrency');
    }
    for (const f of ['applicationCategory', 'operatingSystem']) {
      if (!nonEmpty(a[f])) r.error(where, `SoftwareApplication is missing ${f}`);
    }
    if (a.downloadUrl && !isAbs(a.downloadUrl)) r.error(where, 'downloadUrl is not absolute');
  }

  // FAQPage: present exactly when the frontmatter has `faq`, visible on the page.
  const fm = frontmatter.get(page.urlPath);
  const faqPages = ofType('FAQPage');
  const wantsFaq = Array.isArray(fm?.data.faq) && fm.data.faq.length > 0;
  if (faqPages.length > 1) r.error(where, 'more than one FAQPage');
  if (wantsFaq && faqPages.length !== 1) r.error(where, 'frontmatter has faq but the page has no FAQPage');
  if (!wantsFaq && faqPages.length) r.error(where, 'FAQPage without a faq frontmatter array');
  const text = page.root.text.replace(/\s+/g, ' ');
  for (const f of faqPages) {
    const qs = f.mainEntity;
    if (!Array.isArray(qs) || !qs.length) r.error(where, 'FAQPage has no mainEntity');
    for (const q of qs ?? []) {
      if (!typesOf(q).includes('Question') || !nonEmpty(q.name)) r.error(where, 'FAQ entry is not a Question with a name');
      const ans = q.acceptedAnswer;
      if (!ans || !typesOf(ans).includes('Answer') || !nonEmpty(ans.text)) r.error(where, `FAQ "${q.name}" has no acceptedAnswer text`);
      else {
        if (/<[a-z][^>]*>|\*\*|\]\(/i.test(ans.text)) r.error(where, `FAQ answer for "${q.name}" is not plain text`);
        if (!text.includes(q.name.replace(/\s+/g, ' ')) || !text.includes(ans.text.replace(/\s+/g, ' '))) {
          r.error(where, `FAQ "${q.name}" is not visible on the page`);
        }
      }
    }
  }

  // Articles.
  for (const t of ['Article', 'TechArticle', 'BlogPosting']) {
    for (const a of ofType(t)) {
      if (!nonEmpty(a.headline)) r.error(where, `${t} has no headline`);
      else if (a.headline.length > 110) r.error(where, `${t} headline is ${a.headline.length} chars (Google: 110 max)`);
      if (!a.author) r.error(where, `${t} has no author`);
      for (const auth of [].concat(a.author ?? [])) if (!nonEmpty(auth.name)) r.error(where, `${t} author has no name`);
      for (const f of ['datePublished', 'dateModified']) {
        if (a[f] !== undefined && !ISO.test(a[f])) r.error(where, `${t} ${f} is not ISO 8601: ${a[f]}`);
      }
      for (const img of [].concat(a.image ?? [])) if (!isAbs(typeof img === 'string' ? img : img.url)) r.error(where, `${t} image is not an absolute URL`);
      if (a.url && a.url !== canonical) r.error(where, `${t} url ${a.url} does not match canonical ${canonical}`);
    }
  }
  const isTroubleshootingLeaf = page.urlPath.startsWith(`${BASE}/troubleshooting/`);
  if (isTroubleshootingLeaf && fm?.data.schemaType !== 'none' && !ofType('TechArticle').length && !ofType('Article').length) {
    r.error(where, 'troubleshooting page has no TechArticle');
  }
  if (page.urlPath.startsWith(`${BASE}/blog/`) && fm && !ofType('BlogPosting').length) {
    r.error(where, 'blog post has no BlogPosting');
  }

  // Blog posts: a publisher that resolves on the page, a dateModified, and the
  // same breadcrumb trail as every other page (Devpit > Docs > Blog > post).
  for (const post of ofType('BlogPosting')) {
    const pub = post.publisher;
    if (!pub) r.error(where, 'BlogPosting has no publisher');
    else if (!nonEmpty(pub.name)) {
      // A bare {"@id"} only counts when a node with that id is on the page.
      const target = nodes.find((n) => n !== pub && n['@id'] === pub['@id'] && nonEmpty(n.name));
      if (!target) r.error(where, 'BlogPosting publisher does not resolve on the page: inline the Person or Organization');
    }
    if (!post.dateModified) r.error(where, 'BlogPosting has no dateModified');
    if (!post.datePublished) r.error(where, 'BlogPosting has no datePublished');
    const trail = crumbs[0]?.itemListElement?.map((it) => it.name) ?? [];
    if (trail.length !== 4 || trail[0] !== 'Devpit' || trail[1] !== 'Docs' || trail[2] !== 'Devpit blog') {
      r.error(where, `blog breadcrumb must be Devpit > Docs > Devpit blog > post, found ${trail.join(' > ')}`);
    }
  }
}

process.exit(r.finish(`${blocks} JSON-LD blocks`));
