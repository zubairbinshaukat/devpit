// Renders web/og-image.png, the 1200x630 social card of the landing page.
//
//   node web/scripts/og-image.mjs
//
// The card is plain HTML drawn by a headless Edge or Chrome (the same browser
// helpers the docs screenshots use), with the landing page's own fonts, logo
// and colours, rendered at 2x and scaled down so the text edges stay clean.
// Edit the HTML below and run it again; never edit the PNG by hand.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// sharp is a dependency of the docs site; the renderer re-exports it.
import { findBrowser, screenshot, sharp } from '../docs-site/scripts/shots-render.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const web = path.resolve(here, '..');
const W = 1200;
const H = 630;

const mark = fs.readFileSync(path.join(web, 'logo-mark.png')).toString('base64');

const html = `<!doctype html><html><head><meta charset="utf-8">
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Geist:wght@400;600;700&family=Instrument+Serif:ital@1&family=JetBrains+Mono:wght@400;500&display=block">
<style>
*{margin:0;padding:0;box-sizing:border-box}
html,body{width:${W}px;height:${H}px;overflow:hidden;background:#030305;color:#EDEDF2;font-family:"Geist",system-ui,sans-serif;-webkit-font-smoothing:antialiased}
.rays{position:absolute;inset:-40% -20% 0;background:
  repeating-conic-gradient(from 180deg at 50% 0%,rgba(140,225,240,.20) 0deg 1.2deg,transparent 1.2deg 7deg);
  -webkit-mask-image:radial-gradient(ellipse 70% 80% at 50% 0%,#000 10%,transparent 72%);filter:blur(3px)}
.glow{position:absolute;left:50%;top:-220px;width:900px;height:520px;transform:translateX(-50%);
  background:radial-gradient(ellipse at center,rgba(140,225,240,.16),transparent 65%)}
.stars{position:absolute;inset:0;background-image:
  radial-gradient(1px 1px at 12% 30%,rgba(255,255,255,.35),transparent 60%),
  radial-gradient(1px 1px at 85% 22%,rgba(255,255,255,.3),transparent 60%),
  radial-gradient(1px 1px at 8% 78%,rgba(255,255,255,.25),transparent 60%),
  radial-gradient(1px 1px at 92% 70%,rgba(255,255,255,.3),transparent 60%),
  radial-gradient(1px 1px at 70% 88%,rgba(255,255,255,.2),transparent 60%)}
main{position:relative;height:100%;display:flex;flex-direction:column;align-items:center;justify-content:center;text-align:center;padding-top:6px}
.brand{display:flex;align-items:center;gap:14px;font-weight:600;font-size:34px;letter-spacing:-.01em;margin-bottom:26px}
.brand img{width:62px;height:auto}
h1{font-weight:700;font-size:92px;line-height:.98;letter-spacing:-.035em}
h1 em{display:block;font-family:"Instrument Serif",Georgia,serif;font-style:italic;font-weight:400;font-size:108px;letter-spacing:-.01em;line-height:1.02;
  background:linear-gradient(180deg,#F2FDFF 10%,#B9F1FA 45%,#6FD3E8 100%);-webkit-background-clip:text;background-clip:text;color:transparent;padding:0 12px}
p{margin-top:26px;font-size:23px;color:#A2A2B0}
.cmd{margin-top:30px;display:inline-flex;align-items:center;gap:12px;padding:14px 26px;border-radius:16px;
  background:rgba(10,10,14,.72);border:1px solid rgba(255,255,255,.14);box-shadow:0 0 60px rgba(140,225,240,.10);
  font-family:"JetBrains Mono",Consolas,monospace;font-size:21px;color:#EDEDF2}
.cmd b{color:#93CDF7;font-weight:500}
</style></head><body>
<div class="rays"></div><div class="glow"></div><div class="stars"></div>
<main>
  <div class="brand"><img src="data:image/png;base64,${mark}" alt="">Devpit</div>
  <h1>A pit stop for<em>your dev machine.</em></h1>
  <p>Free developer toolkit for Windows · accounts, disk space, ports, updates, file sharing</p>
  <div class="cmd"><b>PS&gt;</b> irm devpit.zubyr.dev/install | iex</div>
</main>
</body></html>`;

const work = fs.mkdtempSync(path.join(os.tmpdir(), 'devpit-og-'));
try {
  const htmlFile = path.join(work, 'og.html');
  const pngFile = path.join(work, 'og.png');
  fs.writeFileSync(htmlFile, html);
  screenshot(findBrowser(), htmlFile, pngFile, W, H, path.join(work, 'profile'));
  const out = path.join(web, 'og-image.png');
  await sharp(pngFile).resize(W, H, { kernel: 'lanczos3' }).png({ compressionLevel: 9, palette: false }).toFile(out);
  console.log(`wrote ${path.relative(process.cwd(), out)} (${Math.round(fs.statSync(out).size / 1024)} KB)`);
} finally {
  fs.rmSync(work, { recursive: true, force: true, maxRetries: 3 });
}
