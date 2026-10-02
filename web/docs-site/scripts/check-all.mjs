// `npm run check`: runs every docs check and fails if any of them does.
//
// Needs a finished `npm run build` (docs-site/dist). check-site also needs
// the assembled web/dist from `node web/scripts/build.mjs` and is skipped
// with a notice when that has not been run; CI always runs the full build.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const webDist = path.resolve(here, '..', '..', 'dist', 'index.html');

const checks = ['check-meta', 'check-jsonld', 'check-assets', 'check-shots'];
if (fs.existsSync(webDist)) checks.push('check-site', 'check-markdown');
else console.log('check-site, check-markdown: skipped (run `node web/scripts/build.mjs` to assemble web/dist first)');

let failed = 0;
for (const c of checks) {
  const r = spawnSync(process.execPath, [path.join(here, `${c}.mjs`)], { stdio: 'inherit' });
  if (r.status !== 0) failed++;
}
if (failed) {
  console.error(`\n${failed} check(s) failed`);
  process.exit(1);
}
console.log('\nall checks passed');
