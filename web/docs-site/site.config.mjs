// Facts and navigation shared by astro.config.mjs and the build scripts, so
// nothing is written twice. Plain data only: no Astro imports here.

/** Canonical origin of the whole site (landing page and docs). */
export const SITE = 'https://devpit.zubyr.dev';

/** URL prefix the docs are served under. Keep in sync with astro.config.mjs. */
export const BASE = '/docs';

export const REPO = 'https://github.com/zubairbinshaukat/devpit';

/**
 * Sidebar. Slugs are content ids under src/content/docs (no extension).
 * Features follow the app's home menu, in its order: Accounts, Free up disk
 * space, Ports & Network, Install & Update, Share files, Settings. The two
 * parent entries of the menu are sidebar groups over the pages that already
 * existed, so their URLs did not change.
 */
export const sidebar = [
  { label: 'Start here', items: ['getting-started'] },
  {
    label: 'Features',
    items: [
      { slug: 'features', label: 'All features' },
      {
        label: 'Accounts',
        items: [
          { slug: 'features/accounts', label: 'Accounts' },
          'features/accounts-claude-code',
          'features/accounts-git-and-github',
          'features/accounts-other-tools',
        ],
      },
      'features/free-up-disk-space',
      {
        label: 'Ports & Network',
        items: ['features/fix-stuck-ports', 'features/network-tools'],
      },
      {
        label: 'Install & Update',
        items: ['features/install-developer-apps', 'features/update-everything'],
      },
      'features/share-files',
      'features/settings',
    ],
  },
  {
    label: 'Reference',
    items: ['keyboard-and-mouse', 'command-line', 'ai-agents', 'safety-and-privacy', 'whats-new'],
  },
  {
    label: 'Troubleshooting',
    items: [{ slug: 'troubleshooting', label: 'All problems' }, { autogenerate: { directory: 'troubleshooting' } }],
  },
];
