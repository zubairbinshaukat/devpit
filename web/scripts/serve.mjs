// A tiny static server that behaves like Vercel for this site, so the built
// output can be checked locally (and by Lighthouse) without the Vercel CLI:
// cleanUrls, trailingSlash false redirects, the /install rewrite and the
// headers from web/vercel.json.
//
//   node scripts/serve.mjs [port]      serves web/dist (default port 4400)
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import zlib from 'node:zlib';

const WEB = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const DIST = path.join(WEB, 'dist');
const config = JSON.parse(fs.readFileSync(path.join(WEB, 'vercel.json'), 'utf8'));
const port = Number(process.argv[2] ?? 4400);

const TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.xml': 'application/xml; charset=utf-8',
  '.txt': 'text/plain; charset=utf-8',
  '.md': 'text/markdown; charset=utf-8',
  '.png': 'image/png',
  '.webp': 'image/webp',
  '.ico': 'image/x-icon',
  '.svg': 'image/svg+xml',
  '.woff2': 'font/woff2',
  '.wasm': 'application/wasm',
  '.webmanifest': 'application/manifest+json',
};

/**
 * vercel.json header sources use path-to-regexp; only `(.*)`, literals and
 * backslash-escaped characters (`\\.`) appear here.
 */
function headerRules(pathname) {
  const out = {};
  for (const rule of config.headers ?? []) {
    const literal = (s) => s.replace(/\\(.)/g, '$1').replace(/[.+?^${}()|[\]\\]/g, '\\$&');
    const re = new RegExp(`^${rule.source.split('(.*)').map(literal).join('.*')}$`);
    if (re.test(pathname)) for (const h of rule.headers) out[h.key] = h.value;
  }
  return out;
}

function resolve(pathname) {
  const rewrite = (config.rewrites ?? []).find((r) => r.source === pathname);
  if (rewrite) pathname = rewrite.destination;
  const candidates = [pathname];
  if (config.cleanUrls) candidates.push(`${pathname}.html`, path.posix.join(pathname, 'index.html'));
  for (const c of candidates) {
    const file = path.join(DIST, decodeURIComponent(c));
    if (file.startsWith(DIST) && fs.existsSync(file) && fs.statSync(file).isFile()) return file;
  }
  return null;
}

http
  .createServer((req, res) => {
    const url = new URL(req.url, 'http://x');
    let pathname = url.pathname;
    // trailingSlash false: /docs/ -> /docs
    if (config.trailingSlash === false && pathname.length > 1 && pathname.endsWith('/')) {
      res.writeHead(308, { Location: pathname.slice(0, -1) + url.search });
      return res.end();
    }
    // cleanUrls: /x.html -> /x
    if (config.cleanUrls && /\.html$/.test(pathname)) {
      res.writeHead(308, { Location: pathname.replace(/(\/index)?\.html$/, '') || '/' });
      return res.end();
    }
    const file = resolve(pathname);
    const headers = headerRules(pathname);
    if (!file) {
      const nf = path.join(DIST, '404.html');
      res.writeHead(404, { 'Content-Type': TYPES['.html'], ...headers });
      return res.end(fs.existsSync(nf) ? fs.readFileSync(nf) : 'Not found');
    }
    const type = TYPES[path.extname(file)] ?? 'application/octet-stream';
    // Vercel compresses text responses (Brotli, else gzip); do the same so
    // size and timing measurements here match production.
    const text = /^(text\/|application\/(json|xml|javascript|manifest)|image\/svg)/.test(type);
    const accept = String(req.headers['accept-encoding'] ?? '');
    const stream = fs.createReadStream(file);
    if (text && /\bbr\b/.test(accept)) {
      res.writeHead(200, { 'Content-Type': type, 'Content-Encoding': 'br', Vary: 'Accept-Encoding', ...headers });
      stream.pipe(zlib.createBrotliCompress()).pipe(res);
    } else if (text && /\bgzip\b/.test(accept)) {
      res.writeHead(200, { 'Content-Type': type, 'Content-Encoding': 'gzip', Vary: 'Accept-Encoding', ...headers });
      stream.pipe(zlib.createGzip()).pipe(res);
    } else {
      res.writeHead(200, { 'Content-Type': type, ...headers });
      stream.pipe(res);
    }
  })
  .listen(port, () => console.log(`serving web/dist at http://localhost:${port}`));
