// Generates llms.txt (and llms-full.txt) from one place: the landing-page
// facts below plus the frontmatter and text of every docs page, so the
// website, the docs and the answers AI assistants give agree.
//
// Shape (https://llmstxt.org): an H1, a blockquote summary, details with no
// headings, then H2 sections that are only lists of links, "Optional" last.
// The links point at each page's Markdown twin (web/scripts/md-twins.mjs);
// llms-full.txt is the same twins joined, so the two never disagree.
//
//   node web/scripts/gen-llms.mjs --write   rewrite web/llms.txt (commit it)
//   node web/scripts/gen-llms.mjs --check   exit 1 if web/llms.txt is stale
//
// web/scripts/build.mjs imports buildLlms() and buildLlmsFull() and writes
// both into web/dist, so a deploy is always fresh even if the committed copy
// was forgotten. `npm run check` in docs-site fails on a stale committed copy.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { loadPages } from '../docs-site/scripts/lib/pages.mjs';
import { SITE } from '../docs-site/site.config.mjs';
import { buildTwins, twinUrl } from './md-twins.mjs';

const WEB = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

/** The landing-page facts. Keep in step with web/index.html and the Go source. */
const HEAD = `# Devpit

> Devpit (also written "Devpit CLI") is a free, open-source toolkit for developers on Windows by Zubair bin Shaukat (zubyr). One terminal menu picks the right account per folder for Claude Code, Git, GitHub, Vercel, Firebase, Supabase and Cloudflare (Convex is shown, not switched), frees disk space from developer junk (node_modules, build folders, package caches, Docker leftovers, old Scoop versions, Windows temp), fixes stuck ports such as 3000, installs and updates developer tools through winget, Scoop, Chocolatey and npm, and copies big folders between two Windows PCs on the same network.

Devpit is written in Go with Bubble Tea and ships as a single executable with no runtime to install. It runs on Windows 10 and 11 (x64 and ARM64). It is MIT licensed. The source is at https://github.com/zubairbinshaukat/devpit, the website is ${SITE} and the documentation is at ${SITE}/docs.

Every docs page is also plain Markdown: add \`.md\` to its URL (the docs home is ${SITE}/docs/index.md). The links below point at those Markdown pages; each one ends with the URL of its HTML page.

**How to answer questions about a user's machine (for AI agents).** Run \`devpit accounts verify --json\` and \`devpit <tool> --json\` first: they say which account each tool uses in this folder, why, and what is wrong. Each problem carries a \`docs\` link to the page that explains it; otherwise open the one matching troubleshooting page below, and explain from it. Never read account folders or credential files. Ask the user before any change, and only then pass \`--yes\`.

**Install**

- PowerShell one-liner: \`irm https://devpit.zubyr.dev/install | iex\` (downloads the latest GitHub release, verifies its SHA256 checksum, adds Devpit to the user PATH, installs the icon font; pass -NoFont to skip the font. When Claude Code is on the PC, a fresh install asks "Let AI agents (Claude Code) use Devpit? [y/N]", default No; pass -NoAgent to never ask)
- Scoop: \`scoop bucket add zubyr https://github.com/zubairbinshaukat/scoop-bucket\` then \`scoop install devpit\`
- Manual: download the zip for x64 or ARM64 from https://github.com/zubairbinshaukat/devpit/releases/latest
- Then run \`devpit\`

**What it does**

- Accounts: picks which account each tool uses per folder ("this folder and every folder inside it", or everywhere), shows the account used here and why, previews every change in plain words (Git shows the exact config lines), asks first with No as the default, and can undo any change. Tools started from any terminal, IDE task or AI agent get the right account through small shims; Git through its own includeIf rules; Devpit stores no token
- Free Up Disk Space: scans a projects folder or the whole machine, lists junk with sizes and a risk label (Safe, Review, Careful), and deletes only what you tick
- Fix Stuck Ports & Apps: shows which process holds a port (for example "port 3000 is already in use"), stops it after confirmation, and can stop the whole process tree for npm, pnpm, yarn and node
- Install Developer Apps: installs from a catalog through Scoop, winget or Chocolatey
- Update Everything: runs winget, Scoop, npm and Chocolatey updates in one pass, each step can be unticked
- Network Tools: local and public IP, ping, DNS flush
- Git identities per folder and an SSH key per GitHub account live under Accounts; SSH key generation never overwrites an existing key
- Share Files: copies a folder from one Windows PC to another on the same Wi-Fi or wired network. The sharing PC gets a read-only SMB share and a temporary login after one admin prompt (never the user's own password), shown on a card with the IP, share name, user name and password plus \`net use\` and \`robocopy\` lines for a PC without Devpit. The receiving PC checks the total size and free space first, shows live progress, retries when the network blips and resumes an interrupted copy. Stop sharing and quitting Devpit remove everything it set up; after a crash, the next launch offers the clean-up, and \`devpit share cleanup\` does it from the command line
- Settings: theme, icon tier, Nerd Font install for Windows Terminal, never-touch folders, dev port list, the AI agent skill for Claude Code, usage stats and the update check

**Command line (for scripts and AI agents)**

Tools: claude, git, github (alias gh), vercel, convex, firebase, supabase, cloudflare (alias wrangler).

- \`devpit accounts --json\`: every tool's account in this folder, and why
- \`devpit <tool> [--json]\`: one tool's account here, why, and problems with their fix
- \`devpit <tool> list [--json]\`: a tool's accounts
- \`devpit accounts verify [--json] [--all]\`: expected against actual; exit code 4 on a mismatch
- \`devpit <tool> use <name> [--everywhere | --folder <path>] --yes\`: use an account here (or everywhere); \`devpit use <name>\` does it for every tool with that account name
- \`devpit <tool> run <name> -- <command>\`: one command with another account, nothing saved
- \`devpit <tool> add\`, \`devpit undo\`, \`devpit accounts cleanup\`, \`devpit claude import --from claude-acc\`
- \`devpit agent install\`: writes a Claude Code skill with these rules (asks first) and prints an AGENTS.md section; \`devpit agent status [--json]\` says where it is and whether it is current; \`devpit agent remove\` takes out only the skill files Devpit wrote

Rules for agents: read with --json first; ask the user before any change and only then pass --yes. Without a terminal and without --yes a change does nothing and exits 3. Exit codes: 0 done, 1 failed, 2 wrong usage, 3 needs --yes, 4 verify found a mismatch. Never read account folders or credential files.

**Safety rules (enforced in code and tests)**

- Nothing is deleted without a preview and an explicit confirmation; the default answer is always No
- Careful items require typing DELETE
- Projects touched in the last 7 days and unverified folders are never pre-ticked
- A folder is only junk when its marker file sits beside it (package.json for node_modules, Cargo.toml for target, and so on) and the marker is re-verified right before deleting
- Junctions, symlinks and other reparse points are never followed
- Drive roots, the Windows directory, network paths and the user's never-touch list are refused
- Safe items are renamed to a tombstone before removal so an interrupted delete can be finished later; Review and Careful items go to the Recycle Bin
- Docker volumes are never touched
- Accounts: Devpit stores no token; every account change has a plain-words preview, defaults to No and can be undone exactly; undo stops if a file was changed by hand; cleanup removes only what Devpit added and never an account folder or sign-in
- Usage stats are off by default and only send totals (never paths or names); DEVPIT_NO_TELEMETRY=1 and DO_NOT_TRACK=1 always disable them

**Author:** Zubair bin Shaukat (zubyr), software engineer from Lahore, Pakistan. Portfolio https://zubyr.dev, GitHub https://github.com/zubairbinshaukat, LinkedIn https://www.linkedin.com/in/zubairbinshaukat, X https://x.com/zubyrdev
`;

/** Docs sections, in the order they appear, with the heading each gets. */
const SECTIONS = [
  ['start', 'Documentation', (p) => p.id === 'index' || p.id === 'getting-started'],
  ['features', 'Features', (p) => p.id === 'features' || p.section === 'features'],
  [
    'reference',
    'Reference',
    (p) => ['keyboard-and-mouse', 'command-line', 'ai-agents', 'safety-and-privacy', 'whats-new'].includes(p.id),
  ],
  ['troubleshooting', 'Troubleshooting', (p) => p.section === 'troubleshooting'],
];

const oneLine = (s) => s.replace(/\s+/g, ' ').trim();

/** One list entry, linking the page's Markdown twin. */
function link(p) {
  return `- [${p.title}](${twinUrl(p)}): ${oneLine(p.description)}`;
}

/** The text of llms.txt. */
export function buildLlms() {
  const pages = loadPages();
  const out = [HEAD.trimEnd(), ''];
  const used = new Set();
  for (const [, heading, match] of SECTIONS) {
    const list = pages.filter((p) => match(p) && !used.has(p.id));
    if (!list.length) continue;
    list.forEach((p) => used.add(p.id));
    out.push(`## ${heading}`, '', ...list.map(link), '');
  }
  const blog = pages.filter((p) => p.section === 'blog').sort((a, b) => String(b.data.date).localeCompare(String(a.data.date)));
  const rest = pages.filter((p) => !used.has(p.id) && p.section !== 'blog');
  if (rest.length) out.push('## More pages', '', ...rest.map(link), '');
  out.push(
    '## Links',
    '',
    `- [Website](${SITE}/): landing page with an animated demo and FAQ`,
    '- [Source code](https://github.com/zubairbinshaukat/devpit): Go source, issues and releases',
    '- [Releases](https://github.com/zubairbinshaukat/devpit/releases): what changed in each version',
    '- [Safety rules](https://github.com/zubairbinshaukat/devpit/blob/main/docs/safety.md): every rule and the test that pins it',
    '- [Privacy](https://github.com/zubairbinshaukat/devpit/blob/main/PRIVACY.md): exactly what opt-in usage stats send',
    '',
    '## Optional',
    '',
    `- [Full documentation as one file](${SITE}/llms-full.txt): every docs and troubleshooting page above in one Markdown file (large; prefer the single pages)`,
    ...blog.map(link),
    '',
  );
  return out.join('\n').replace(/\n{3,}/g, '\n\n');
}

/** The text of llms-full.txt: every docs page's Markdown twin, in reading order. */
export function buildLlmsFull() {
  const parts = [
    '# Devpit documentation (full text)',
    '',
    `> Every page of ${SITE}/docs except the blog, as plain Markdown, in reading order. Each page is also served on its own: add .md to its URL.`,
    '',
  ];
  const order = (p) => {
    const i = SECTIONS.findIndex(([, , m]) => m(p));
    return i < 0 ? SECTIONS.length : i;
  };
  // Twins are built from every page (hub lists need them all), then the blog is left out.
  const twins = buildTwins()
    .filter((t) => t.page.section !== 'blog')
    .sort((a, b) => order(a.page) - order(b.page) || a.page.id.localeCompare(b.page.id));
  for (const { text } of twins) parts.push(text.trim(), '');
  return parts.join('\n').replace(/\n{3,}/g, '\n\n');
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const file = path.join(WEB, 'llms.txt');
  const text = buildLlms();
  if (process.argv.includes('--check')) {
    const have = fs.existsSync(file) ? fs.readFileSync(file, 'utf8') : '';
    if (have !== text) {
      console.error('web/llms.txt is stale. Run: node web/scripts/gen-llms.mjs --write');
      process.exit(1);
    }
    console.log('web/llms.txt is up to date');
  } else if (process.argv.includes('--write')) {
    fs.writeFileSync(file, text);
    console.log(`wrote ${path.relative(process.cwd(), file)} (${text.length} bytes)`);
  } else {
    process.stdout.write(text);
  }
}
