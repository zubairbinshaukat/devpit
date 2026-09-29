// One 1200x630 social image per docs page and blog post, drawn at build time
// with astro-og-canvas (CanvasKit, no browser). Served at /docs/og/<id>.png,
// which routeData.ts advertises as og:image and twitter:image.
import { getCollection } from 'astro:content';
import { OGImageRoute } from 'astro-og-canvas';

const entries = await getCollection('docs');

const pages: Record<string, { title: string; description: string }> = Object.fromEntries(
  entries
    .filter((e) => e.id !== '404')
    .map((e) => [
      e.id,
      {
        title: e.data.ogTitle ?? e.data.seoTitle ?? e.data.title,
        description: e.data.description ?? '',
      },
    ]),
);
// One shared image for the blog list, tag, author and page-number pages that
// starlight-blog injects (they are not content entries).
pages.blog = {
  title: 'Devpit blog',
  description: 'Short guides and fixes for Windows developers.',
};

export const { getStaticPaths, GET } = await OGImageRoute({
  param: 'route',
  pages,
  getImageOptions: (_path, page) => ({
    title: page.title,
    description: page.description,
    bgGradient: [
      [12, 12, 20],
      [24, 24, 37],
    ],
    border: { color: [111, 211, 232], width: 14, side: 'inline-start' },
    padding: 72,
    logo: { path: '../logo-mark.png', size: [84] },
    font: {
      title: { color: [237, 237, 242], families: ['Geist'], weight: 'SemiBold', size: 68, lineHeight: 1.1 },
      description: { color: [162, 162, 176], families: ['Geist'], weight: 'Normal', size: 32, lineHeight: 1.35 },
    },
    fonts: ['./src/fonts/Geist-SemiBold.ttf', './src/fonts/Geist-Regular.ttf'],
  }),
});
