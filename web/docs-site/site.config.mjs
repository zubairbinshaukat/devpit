// Facts and navigation shared by astro.config.mjs and the build scripts, so
// nothing is written twice. Plain data only: no Astro imports here.

/** Canonical origin of the whole site (landing page and docs). */
export const SITE = 'https://devpit.zubyr.dev';

/** URL prefix the docs are served under. Keep in sync with astro.config.mjs. */
export const BASE = '/docs';

export const REPO = 'https://github.com/zubairbinshaukat/devpit';

/** Sidebar. Slugs are content ids under src/content/docs (no extension). */
export const sidebar = [
  { label: 'Start here', items: ['getting-started'] },
  {
    label: 'Features',
    items: [
      { slug: 'features', label: 'All features' },
      'features/free-up-disk-space',
      'features/fix-stuck-ports',
      'features/install-developer-apps',
      'features/update-everything',
      'features/network-tools',
      'features/git-and-ssh',
      'features/settings',
      'features/share-files',
    ],
  },
  {
    label: 'Reference',
    items: ['keyboard-and-mouse', 'command-line', 'safety-and-privacy', 'whats-new'],
  },
  {
    label: 'Troubleshooting',
    items: [{ slug: 'troubleshooting', label: 'All problems' }, { autogenerate: { directory: 'troubleshooting' } }],
  },
];
