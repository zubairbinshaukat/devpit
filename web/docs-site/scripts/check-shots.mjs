// Screenshot manifest checks. shots.json (docs pages) and shots.content.json
// (troubleshooting and blog) are the contract with the screenshot tool; pages
// reference shots by name with <Shot name="..." />.
//
//   - each manifest is valid (unique kebab-case names, real alt text, screen,
//     state, cols, rows) and names are unique across both files
//   - every <Shot name> used in a page exists in a manifest (error)
//   - manifest entries no page uses are reported (warning)
//   - shots without an image file yet are listed (warning); DOCS_REQUIRE_SHOTS=1
//     makes them errors. The images are delivered separately and unpacked into
//     src/assets/screens/.
import fs from 'node:fs';
import path from 'node:path';

import { Report } from './lib/dist.mjs';
import { SITE_ROOT, loadPages } from './lib/pages.mjs';

const r = new Report('check-shots');
const strict = process.env.DOCS_REQUIRE_SHOTS === '1' || process.env.SHOTS_STRICT === '1';

const files = fs.readdirSync(SITE_ROOT).filter((f) => /^shots(\..+)?\.json$/.test(f)).sort();
const names = new Set();
for (const file of files) {
  let manifest;
  try {
    manifest = JSON.parse(fs.readFileSync(path.join(SITE_ROOT, file), 'utf8'));
  } catch (e) {
    r.error(file, `is not valid JSON: ${e.message}`);
    continue;
  }
  if (!Array.isArray(manifest)) {
    r.error(file, 'must be a JSON array');
    continue;
  }
  for (const [i, s] of manifest.entries()) {
    const where = `${file}[${i}] ${s?.name ?? ''}`;
    if (typeof s.name !== 'string' || !/^[a-z0-9]+(-[a-z0-9]+)*$/.test(s.name)) r.error(where, 'name must be kebab-case');
    if (names.has(s.name)) r.error(where, 'duplicate name');
    names.add(s.name);
    if (typeof s.alt !== 'string' || s.alt.trim().length < 15) r.error(where, 'alt must be real descriptive text (15+ chars)');
    for (const f of ['screen', 'state']) if (typeof s[f] !== 'string' || !s[f]) r.error(where, `${f} is required`);
    for (const f of ['cols', 'rows']) if (!Number.isInteger(s[f]) || s[f] < 20 || s[f] > 300) r.error(where, `${f} must be an integer 20..300`);
  }
}

const used = new Set();
for (const p of loadPages()) {
  for (const m of p.body.matchAll(/<Shot\b[^>]*\bname=["']([^"']+)["']/g)) {
    used.add(m[1]);
    if (!names.has(m[1])) r.error(p.id, `<Shot name="${m[1]}"> is not in a shots manifest`);
  }
}
for (const n of names) if (!used.has(n)) r.warn('shots', `"${n}" is not used by any page`);

const dir = path.join(SITE_ROOT, 'src', 'assets', 'screens');
const have = new Set(fs.existsSync(dir) ? fs.readdirSync(dir).filter((f) => /\.(webp|png|jpe?g|avif)$/.test(f)).map((f) => f.replace(/\.[^.]+$/, '')) : []);
const missing = [...names].filter((n) => !have.has(n));
for (const n of missing) (strict ? r.error : r.warn).call(r, 'screens', `no image yet for "${n}" (src/assets/screens/${n}.webp)`);
for (const n of have) if (!names.has(n)) r.warn('screens', `${n} has no entry in a shots manifest`);

process.exit(r.finish(`${names.size} shots, ${names.size - missing.length} with images`));
