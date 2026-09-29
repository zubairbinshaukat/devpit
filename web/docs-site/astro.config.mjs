// Devpit docs: Starlight served at https://devpit.zubyr.dev/docs.
//
// URL policy (must match web/vercel.json: cleanUrls true, trailingSlash false):
//   trailingSlash 'never' + build.format 'file' emit docs/foo.html, which Vercel
//   serves at /docs/foo and redirects /docs/foo/ to. The canonical URL of every
//   page therefore has no trailing slash, and so does every sitemap entry.
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

import starlight from '@astrojs/starlight';
import sitemap from '@astrojs/sitemap';
import { defineConfig } from 'astro/config';
import sharp from 'sharp';
import starlightBlog from 'starlight-blog';
import starlightLinksValidator from 'starlight-links-validator';

import { SITE, sidebar } from './site.config.mjs';

// The author photo is a landing-page file. Write a small copy into public/
// (git-ignored) so there is one source of truth, the docs serve it from their
// own folder, and it is not shipped at full size to be drawn 48 pixels wide.
fs.mkdirSync(new URL('./public/', import.meta.url), { recursive: true });
await sharp(fileURLToPath(new URL('../author-avatar.webp', import.meta.url)))
  .resize(128, 128, { fit: 'cover' })
  .webp({ quality: 82 })
  .toFile(fileURLToPath(new URL('./public/author-avatar.webp', import.meta.url)));

export default defineConfig({
  site: SITE,
  base: '/docs',
  trailingSlash: 'never',
  build: { format: 'file' },
  // Astro 7 defaults are fine; no adapter, the output is plain static files.
  devToolbar: { enabled: false },
  vite: {
    build: {
      rollupOptions: {
        // Astro's own "use astro:head-inject" directive triggers this harmless
        // bundler notice once per content page. Drop just that one.
        onwarn(warning, warn) {
          if (warning.code === 'MODULE_LEVEL_DIRECTIVE') return;
          warn(warning);
        },
      },
    },
  },
  integrations: [
    // Registered before Starlight so Starlight does not add its own copy.
    // The 404 page must never be listed.
    sitemap({
      // Not the 404 page, and not the thin blog archive pages (tags, authors,
      // page numbers): routeData.ts marks those noindex, so listing them would
      // contradict it.
      filter: (page) => !/\/404$/.test(page) && !/\/blog\/(tags\/|authors\/|\d+$)/.test(page),
      // The docs home is /docs (no trailing slash), like every other page.
      serialize: (item) => ({ ...item, url: item.url.replace(/(?<=\w)\/$/, '') }),
    }),
    starlight({
      title: 'Devpit Docs',
      description:
        'Guides for Devpit, the free Windows terminal app that frees disk space, fixes stuck ports and updates your developer tools.',
      logo: { src: '../logo-mark.png', alt: 'Devpit' },
      // Rewritten to the site-root file by src/routeData.ts (Starlight would prefix /docs).
      favicon: '/favicon.ico',
      titleDelimiter: '|',
      // Keep the page light: no Expressive Code chrome needed beyond defaults,
      // and Pagefind search is the built-in, static, zero-server option.
      pagefind: true,
      // Code blocks use the same Catppuccin palette as the app itself.
      expressiveCode: {
        themes: ['catppuccin-mocha', 'catppuccin-latte'],
        // Matches --dp-r in src/styles/devpit.css.
        styleOverrides: { borderRadius: '0.8rem' },
      },
      lastUpdated: false,
      credits: false,
      social: [
        { icon: 'github', label: 'GitHub', href: 'https://github.com/zubairbinshaukat/devpit' },
        { icon: 'x.com', label: 'X', href: 'https://x.com/zubyrdev' },
      ],
      editLink: {
        baseUrl: 'https://github.com/zubairbinshaukat/devpit/edit/main/web/docs-site/',
      },
      customCss: [
        '@fontsource-variable/geist',
        '@fontsource-variable/jetbrains-mono',
        './src/styles/devpit.css',
      ],
      routeMiddleware: './src/routeData.ts',
      components: {
        Footer: './src/components/Footer.astro',
        Search: './src/components/Search.astro',
        PageTitle: './src/components/PageTitle.astro',
      },
      sidebar,
      plugins: [
        starlightBlog({
          title: 'Devpit blog',
          prefix: 'blog',
          navigation: 'header-end',
          postCount: 8,
          recentPostCount: 6,
          rss: true,
          structuredData: true,
          metrics: { readingTime: true },
          authors: {
            zubair: {
              name: 'Zubair bin Shaukat',
              title: 'Creator of Devpit',
              // Served from this site's own public/ folder (copied from web/ below), so
              // the blog never waits on a second request path. starlight-blog puts the
              // base in front, giving /docs/author-avatar.webp.
              picture: '/author-avatar.webp',
              url: 'https://zubyr.dev',
            },
          },
        }),
        starlightLinksValidator({
          // Screenshots may be added after the page is written; the check
          // script reports missing shots separately.
          errorOnRelativeLinks: false,
          errorOnInvalidHashes: true,
          errorOnLocalLinks: true,
          // Links to landing-page files and other root-served pages
          // (/install, /, /#faq) are outside this Astro project.
          exclude: ['/', '/install', '/#*', '/llms.txt', '/sitemap.xml', '/robots.txt', '/og-image.png'],
        }),
      ],
    }),
  ],
});
