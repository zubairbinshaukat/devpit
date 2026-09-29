// `npm run shots`: turns the ANSI frames written by the Go screenshot scenes
// into the WebP images the docs pages show.
//
//   DEVPIT_SHOTS=1 DEVPIT_SHOTS_OUT=<dir> go test -tags shots ./internal/shots -run TestRender
//   node scripts/shots-render.mjs --in <dir>          (or: task docs:shots)
//
// Each <name>.ans frame listed in <dir>/index.json becomes
// src/assets/screens/<name>.webp. A frame is laid out as a grid of terminal
// cells inside a Windows Terminal style window, drawn by a headless Chromium
// (Edge or Chrome) at twice the pixel density, and saved as lossless WebP. The
// page's own <Picture> pipeline makes the smaller AVIF and WebP sizes from it.
//
// Nothing here is captured by hand: the pixels are the app's own output, the
// colours are the truecolor values the app wrote, and every cell sits on the
// grid the app measured, so a glyph from a fallback font can never shift a
// border out of line.
//
// Options:
//   --in <dir>       folder with index.json and the .ans frames
//                    (default: $DEVPIT_SHOTS_OUT or <tmp>/devpit-shots)
//   --out <dir>      where the images go (default: src/assets/screens)
//   --only a,b       render only these names
//   --keep           keep the intermediate HTML and PNG next to the frames
//
// Environment:
//   SHOTS_BROWSER    path to msedge.exe / chrome.exe / chromium
//   SHOTS_FONT       path to a monospace TTF (default: Cascadia Mono)
//   SHOTS_SYMBOLS    path to Symbols Nerd Font Mono (for the icon glyphs)
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import sharp from 'sharp';

export { sharp };

const here = path.dirname(fileURLToPath(import.meta.url));
const siteRoot = path.resolve(here, '..');

// --- Options ----------------------------------------------------------------

function parseArgs(argv) {
  const o = { in: '', out: path.join(siteRoot, 'src', 'assets', 'screens'), only: new Set(), keep: false };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const next = () => {
      if (i + 1 >= argv.length) throw new Error(`${a} needs a value`);
      return argv[++i];
    };
    if (a === '--in') o.in = next();
    else if (a === '--out') o.out = next();
    else if (a === '--only') for (const n of next().split(',')) n.trim() && o.only.add(n.trim());
    else if (a === '--keep') o.keep = true;
    else throw new Error(`unknown option ${a}`);
  }
  o.in ||= process.env.DEVPIT_SHOTS_OUT || path.join(os.tmpdir(), 'devpit-shots');
  return o;
}

// --- Look ---------------------------------------------------------------------

// Cell size in CSS pixels. The font size is chosen so a Cascadia Mono cell
// (advance 1200/2048 em) is exactly 9 px wide, which keeps every column on a
// whole pixel. The row height is Windows Terminal's default line height for
// this font, rounded to a whole pixel.
const CELL_W = 9;
const FONT_PX = (CELL_W * 2048) / 1200; // 15.36
const CELL_H = 19;
const PAD_X = 14;
const PAD_Y = 12;
const TITLE_H = 40;
const SCALE = 2;

// The terminal colours behind the app: Catppuccin Mocha / Latte, the palettes
// the app's own theme is drawn from (internal/ui/theme).
const THEMES = {
  dark: { bg: '#1e1e2e', fg: '#cdd6f4', bar: '#11111b', tab: '#1e1e2e', tabFg: '#cdd6f4', dim: '#7f849c' },
  light: { bg: '#eff1f5', fg: '#4c4f69', bar: '#dce0e8', tab: '#eff1f5', tabFg: '#4c4f69', dim: '#8c8fa1' },
};

// The xterm 256-colour table, for frames that were downsampled.
const XTERM = (() => {
  const base = [
    '#000000', '#cd0000', '#00cd00', '#cdcd00', '#0000ee', '#cd00cd', '#00cdcd', '#e5e5e5',
    '#7f7f7f', '#ff0000', '#00ff00', '#ffff00', '#5c5cff', '#ff00ff', '#00ffff', '#ffffff',
  ];
  const hex = (n) => n.toString(16).padStart(2, '0');
  const steps = [0, 95, 135, 175, 215, 255];
  for (let r = 0; r < 6; r++)
    for (let g = 0; g < 6; g++) for (let b = 0; b < 6; b++) base.push(`#${hex(steps[r])}${hex(steps[g])}${hex(steps[b])}`);
  for (let i = 0; i < 24; i++) {
    const v = 8 + i * 10;
    base.push(`#${hex(v)}${hex(v)}${hex(v)}`);
  }
  return base;
})();

// --- ANSI to cells -------------------------------------------------------------

// Display width of one code point, the way the Go side measures it
// (charmbracelet/x/ansi): 0 for combining marks and joiners, 2 for East Asian
// wide and emoji presentation, 1 for everything else, Nerd Font icons included.
export function cellWidth(cp) {
  if (cp === 0x200d || (cp >= 0xfe00 && cp <= 0xfe0f) || (cp >= 0x0300 && cp <= 0x036f) || (cp >= 0x20d0 && cp <= 0x20ff))
    return 0;
  if (
    (cp >= 0x1100 && cp <= 0x115f) ||
    (cp >= 0x2e80 && cp <= 0x303e) ||
    (cp >= 0x3041 && cp <= 0x33ff) ||
    (cp >= 0x3400 && cp <= 0x4dbf) ||
    (cp >= 0x4e00 && cp <= 0x9fff) ||
    (cp >= 0xa000 && cp <= 0xa4cf) ||
    (cp >= 0xac00 && cp <= 0xd7a3) ||
    (cp >= 0xf900 && cp <= 0xfaff) ||
    (cp >= 0xfe30 && cp <= 0xfe4f) ||
    (cp >= 0xff00 && cp <= 0xff60) ||
    (cp >= 0xffe0 && cp <= 0xffe6) ||
    (cp >= 0x1f300 && cp <= 0x1f64f) ||
    (cp >= 0x1f900 && cp <= 0x1f9ff) ||
    (cp >= 0x20000 && cp <= 0x3fffd)
  )
    return 2;
  return 1;
}

const defaultStyle = () => ({ fg: null, bg: null, bold: false, italic: false, underline: false, reverse: false, faint: false, strike: false });

// applySGR folds one "ESC [ ... m" parameter list into the style.
function applySGR(style, params) {
  const p = params === '' ? [0] : params.split(/[;]/).map((x) => (x === '' ? 0 : Number.parseInt(x.split(':')[0], 10)));
  for (let i = 0; i < p.length; i++) {
    const n = p[i];
    if (n === 0) Object.assign(style, defaultStyle());
    else if (n === 1) style.bold = true;
    else if (n === 2) style.faint = true;
    else if (n === 3) style.italic = true;
    else if (n === 4) style.underline = true;
    else if (n === 7) style.reverse = true;
    else if (n === 9) style.strike = true;
    else if (n === 22) style.bold = style.faint = false;
    else if (n === 23) style.italic = false;
    else if (n === 24) style.underline = false;
    else if (n === 27) style.reverse = false;
    else if (n === 29) style.strike = false;
    else if (n >= 30 && n <= 37) style.fg = XTERM[n - 30];
    else if (n >= 90 && n <= 97) style.fg = XTERM[n - 90 + 8];
    else if (n >= 40 && n <= 47) style.bg = XTERM[n - 40];
    else if (n >= 100 && n <= 107) style.bg = XTERM[n - 100 + 8];
    else if (n === 39) style.fg = null;
    else if (n === 49) style.bg = null;
    else if (n === 38 || n === 48 || n === 58) {
      let color = null;
      if (p[i + 1] === 2) {
        const [r, g, b] = [p[i + 2], p[i + 3], p[i + 4]].map((v) => Math.max(0, Math.min(255, v | 0)));
        color = `#${[r, g, b].map((v) => v.toString(16).padStart(2, '0')).join('')}`;
        i += 4;
      } else if (p[i + 1] === 5) {
        color = XTERM[p[i + 2] & 255];
        i += 2;
      }
      if (n === 38) style.fg = color;
      else if (n === 48) style.bg = color;
    }
  }
}

// parseFrame turns an ANSI frame into rows of cells. A wide glyph takes its
// cell plus a null continuation cell, so every row is exactly cols long.
export function parseFrame(text, cols, rows) {
  const grid = [];
  const lines = text.replace(/\r\n/g, '\n').split('\n');
  const style = defaultStyle();
  for (let y = 0; y < rows; y++) {
    const row = [];
    const line = lines[y] ?? '';
    let i = 0;
    while (i < line.length) {
      const ch = line[i];
      if (ch === '\x1b') {
        const next = line[i + 1];
        if (next === '[') {
          let j = i + 2;
          while (j < line.length && !/[@-~]/.test(line[j])) j++;
          if (line[j] === 'm') applySGR(style, line.slice(i + 2, j));
          i = j + 1;
          continue;
        }
        if (next === ']') {
          // OSC (hyperlinks, titles): skip to BEL or ST.
          let j = i + 2;
          while (j < line.length && line[j] !== '\x07' && !(line[j] === '\x1b' && line[j + 1] === '\\')) j++;
          i = line[j] === '\x07' ? j + 1 : j + 2;
          continue;
        }
        i += 2;
        continue;
      }
      const cp = line.codePointAt(i);
      const s = String.fromCodePoint(cp);
      i += s.length;
      if (cp < 0x20) continue;
      const w = cellWidth(cp);
      if (w === 0) {
        if (row.length) row[row.length - 1 - (row.at(-1) === null ? 1 : 0)].ch += s;
        continue;
      }
      if (row.length + w > cols) break;
      row.push({ ch: s, w, ...style });
      if (w === 2) row.push(null);
    }
    while (row.length < cols) row.push({ ch: ' ', w: 1, ...defaultStyle() });
    grid.push(row);
  }
  return grid;
}

// --- Cells to HTML ----------------------------------------------------------------

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

function cellColors(c, theme) {
  let fg = c.fg ?? theme.fg;
  let bg = c.bg ?? null;
  if (c.reverse) [fg, bg] = [bg ?? theme.bg, fg];
  return { fg, bg };
}

// A glyph is safe to leave in a text run when Cascadia Mono draws it at
// exactly one cell: printable ASCII and Latin-1. Anything else gets a box of
// its own that is exactly its cells wide, so a fallback font cannot move the
// columns after it.
const inRun = (ch) => {
  const cp = ch.codePointAt(0);
  return ch.length === 1 && cp >= 0x20 && cp <= 0xff;
};

function rowHTML(row, theme) {
  let html = '';
  let run = null;
  const flush = () => {
    if (!run) return;
    const st = [];
    if (run.fg !== theme.fg) st.push(`color:${run.fg}`);
    if (run.bg) st.push(`background:${run.bg}`);
    const cls = [run.bold && 'b', run.italic && 'i', run.underline && 'u', run.strike && 's', run.faint && 'f'].filter(Boolean);
    const attrs = `${cls.length ? ` class="${cls.join(' ')}"` : ''}${st.length ? ` style="${st.join(';')}"` : ''}`;
    html += attrs ? `<span${attrs}>${run.text}</span>` : run.text;
    run = null;
  };
  for (const c of row) {
    if (c === null) continue;
    const { fg, bg } = cellColors(c, theme);
    const key = `${fg}|${bg}|${c.bold}|${c.italic}|${c.underline}|${c.strike}|${c.faint}`;
    const piece = inRun(c.ch) ? esc(c.ch) : `<i class="g w${c.w}${glyphClass(c.ch)}">${esc(c.ch)}</i>`;
    if (run && run.key === key) run.text += piece;
    else {
      flush();
      run = { key, fg, bg, bold: c.bold, italic: c.italic, underline: c.underline, strike: c.strike, faint: c.faint, text: piece };
    }
  }
  flush();
  return `<div class="r">${html}</div>`;
}

// fontMetrics reads what the layout needs from a TrueType file: units per
// em, the hhea ascender and descender, and the widest advance.
export function fontMetrics(file) {
  const buf = fs.readFileSync(file);
  const tables = {};
  const n = buf.readUInt16BE(4);
  for (let i = 0; i < n; i++) {
    const rec = 12 + i * 16;
    tables[buf.toString('latin1', rec, rec + 4)] = buf.readUInt32BE(rec + 8);
  }
  if (tables.head === undefined || tables.hhea === undefined || tables.hmtx === undefined) {
    throw new Error(`${file} is not a TrueType font with head, hhea and hmtx tables`);
  }
  const upem = buf.readUInt16BE(tables.head + 18);
  const ascender = buf.readInt16BE(tables.hhea + 4);
  const descender = buf.readInt16BE(tables.hhea + 6);
  const metrics = buf.readUInt16BE(tables.hhea + 34);
  // The most common non-zero advance is the cell width of a monospace font;
  // a few odd glyphs must not decide it.
  const counts = new Map();
  for (let i = 0; i < metrics; i++) {
    const a = buf.readUInt16BE(tables.hmtx + i * 4);
    if (a > 0) counts.set(a, (counts.get(a) ?? 0) + 1);
  }
  const advance = [...counts].sort((a, b) => b[1] - a[1])[0]?.[0] ?? upem;
  return { upem, ascender, descender, advance };
}

// BLOCKS maps the solid block characters to the part of the cell they fill.
const BLOCKS = { 0x2588: 'kf', 0x258c: 'kl', 0x2590: 'kr', 0x2580: 'kt', 0x2584: 'kb' };

// glyphClass is the extra class a boxed glyph gets: Nerd Font icons (Private
// Use Area) are shrunk to fit their cell the way Windows Terminal draws them,
// and box-drawing and block characters are stretched to the full row height
// so borders and the logo join up with no seam between rows.
function glyphClass(ch) {
  const cp = ch.codePointAt(0);
  if ((cp >= 0xe000 && cp <= 0xf8ff) || cp >= 0xf0000) return ' p';
  // Solid blocks are painted, not drawn: two anti-aliased glyph edges side by
  // side leave a faint seam, a painted cell does not.
  if (BLOCKS[cp]) return ` k ${BLOCKS[cp]}`;
  if (cp >= 0x2500 && cp <= 0x259f) return ' x';
  return '';
}

function fontFace(family, file, weight = '100 900') {
  const data = fs.readFileSync(file).toString('base64');
  return `@font-face{font-family:"${family}";src:url(data:font/ttf;base64,${data}) format("truetype");font-weight:${weight};font-display:block}`;
}

export function frameHTML(grid, cols, rows, themeName, fonts, title = 'Devpit') {
  const theme = THEMES[themeName] ?? THEMES.dark;
  const w = PAD_X * 2 + cols * CELL_W;
  const h = TITLE_H + PAD_Y * 2 + rows * CELL_H;
  const faces = [fontFace('DevpitMono', fonts.mono)];
  if (fonts.symbols) faces.push(fontFace('DevpitSymbols', fonts.symbols, 'normal'));
  const mono = fontMetrics(fonts.mono);
  // How much a box-drawing glyph must grow to fill the row, and how small an
  // icon must be drawn to fit one cell.
  const stretch = CELL_H / (((mono.ascender - mono.descender) / mono.upem) * FONT_PX);
  let iconPx = FONT_PX;
  if (fonts.symbols) {
    const sym = fontMetrics(fonts.symbols);
    // Windows Terminal lets an icon hang over into the blank cell after it;
    // 1.4 cells keeps that look without touching the next letter.
    iconPx = Math.min(FONT_PX, (1.4 * CELL_W) / (sym.advance / sym.upem));
  }
  const family = `"DevpitMono",${fonts.symbols ? '"DevpitSymbols",' : ''}"Segoe UI Symbol","Noto Sans Symbols 2",monospace`;
  const body = grid.map((r) => rowHTML(r, theme)).join('');
  // The title bar follows Windows Terminal on Windows 11: one tab with the
  // profile icon and title, the new-tab and profile buttons, then the three
  // caption buttons at the right edge.
  const icon = `<svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><rect x="0.5" y="1.5" width="15" height="13" rx="3" fill="#7fdbca"/><path d="M4 5.5l2.5 2.5L4 10.5" stroke="${theme.bg}" stroke-width="1.6" fill="none" stroke-linecap="round" stroke-linejoin="round"/><path d="M8 10.5h4" stroke="${theme.bg}" stroke-width="1.6" stroke-linecap="round"/></svg>`;
  const x = `<svg width="10" height="10" viewBox="0 0 10 10"><path d="M1 1l8 8M9 1l-8 8" stroke="currentColor" stroke-width="1"/></svg>`;
  const caption = `
    <div class="cap"><svg width="10" height="10" viewBox="0 0 10 10"><path d="M0 5.5h10" stroke="currentColor"/></svg></div>
    <div class="cap"><svg width="10" height="10" viewBox="0 0 10 10"><rect x="0.5" y="0.5" width="9" height="9" rx="1.5" fill="none" stroke="currentColor"/></svg></div>
    <div class="cap">${x}</div>`;
  return `<!doctype html><html><head><meta charset="utf-8"><style>
${faces.join('\n')}
*{margin:0;padding:0;box-sizing:border-box}
html,body{width:${w}px;height:${h}px;overflow:hidden;background:${theme.bg}}
.bar{height:${TITLE_H}px;background:${theme.bar};display:flex;align-items:flex-end;padding-left:8px;font:12px "Segoe UI Variable Text","Segoe UI",system-ui,sans-serif;color:${theme.tabFg}}
.tab{height:32px;width:220px;background:${theme.tab};border-radius:8px 8px 0 0;display:flex;align-items:center;gap:10px;padding:0 10px 0 12px}
.tab .t{flex:1;white-space:nowrap;overflow:hidden}
.tab .x{color:${theme.dim};display:flex}
.btn{height:32px;width:34px;display:flex;align-items:center;justify-content:center;color:${theme.dim};font-size:15px}
.sp{flex:1}
.cap{align-self:stretch;width:46px;display:flex;align-items:center;justify-content:center;color:${theme.tabFg}}
.term{padding:${PAD_Y}px ${PAD_X}px;font-family:${family};font-size:${FONT_PX}px;line-height:${CELL_H}px;color:${theme.fg};font-variant-ligatures:none;font-feature-settings:"liga" 0,"calt" 0;-webkit-font-smoothing:antialiased;text-rendering:geometricPrecision}
.r{height:${CELL_H}px;width:${cols * CELL_W}px;white-space:pre;overflow:hidden}
.r span{display:inline-block;height:${CELL_H}px;vertical-align:top}
.g{font-style:normal;display:inline-block;height:${CELL_H}px;vertical-align:top;text-align:center;overflow:visible}
.w1{width:${CELL_W}px}.w2{width:${CELL_W * 2}px}
.k{-webkit-text-fill-color:transparent;background-repeat:no-repeat}
.kf{background:currentColor}
.kl{background-image:linear-gradient(currentColor,currentColor);background-size:50% 100%;background-position:left}
.kr{background-image:linear-gradient(currentColor,currentColor);background-size:50% 100%;background-position:right}
.kt{background-image:linear-gradient(currentColor,currentColor);background-size:100% 50%;background-position:top}
.kb{background-image:linear-gradient(currentColor,currentColor);background-size:100% 50%;background-position:bottom}
.x{transform:scaleY(${stretch.toFixed(4)});transform-origin:50% 50%}
.p{display:inline-flex;align-items:center;justify-content:flex-start;position:relative;z-index:1;white-space:nowrap;font-family:"DevpitSymbols",${family};font-size:${iconPx.toFixed(2)}px}
.b{font-weight:700}.i{font-style:italic}.u{text-decoration:underline}.s{text-decoration:line-through}.f{opacity:.6}
</style></head><body>
<div class="bar"><div class="tab">${icon}<span class="t">${esc(title)}</span><span class="x">${x}</span></div><div class="btn">+</div><div class="btn"><svg width="10" height="10" viewBox="0 0 10 10"><path d="M1.5 3.5L5 7l3.5-3.5" stroke="currentColor" stroke-width="1.1" fill="none"/></svg></div><div class="sp"></div>${caption}</div>
<div class="term">${body}</div>
</body></html>`;
}

// --- Browser and fonts ---------------------------------------------------------------

export function findBrowser() {
  const candidates = [
    process.env.SHOTS_BROWSER,
    'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
    'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
    'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
    process.env.LOCALAPPDATA && path.join(process.env.LOCALAPPDATA, 'Google', 'Chrome', 'Application', 'chrome.exe'),
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    '/usr/bin/google-chrome',
    '/usr/bin/chromium',
    '/usr/bin/chromium-browser',
    '/usr/bin/microsoft-edge',
  ].filter(Boolean);
  const found = candidates.find((p) => fs.existsSync(p));
  if (!found) throw new Error('no Chromium browser found; set SHOTS_BROWSER to msedge.exe, chrome.exe or chromium');
  return found;
}

// Windows Terminal ships Cascadia Mono inside its package, which is where a
// machine without the font installed system-wide still has it.
function windowsTerminalFonts() {
  if (process.platform !== 'win32') return [];
  const r = spawnSync(
    'powershell.exe',
    ['-NoProfile', '-NonInteractive', '-Command', '(Get-AppxPackage Microsoft.WindowsTerminal*).InstallLocation'],
    { encoding: 'utf8' },
  );
  return (r.stdout || '')
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter(Boolean)
    .map((dir) => path.join(dir, 'CascadiaMono.ttf'));
}

function findFonts() {
  const home = os.homedir();
  const local = process.env.LOCALAPPDATA ?? path.join(home, 'AppData', 'Local');
  const mono = [
    process.env.SHOTS_FONT,
    'C:\\Windows\\Fonts\\CascadiaMono.ttf',
    path.join(local, 'Microsoft', 'Windows', 'Fonts', 'CascadiaMono.ttf'),
    ...windowsTerminalFonts(),
    path.join(home, '.local', 'share', 'fonts', 'CascadiaMono.ttf'),
    '/usr/share/fonts/truetype/cascadia-code/CascadiaMono.ttf',
  ]
    .filter(Boolean)
    .find((p) => fs.existsSync(p));
  if (!mono) throw new Error('Cascadia Mono not found; install it or set SHOTS_FONT to a monospace .ttf');
  const symbols = [
    process.env.SHOTS_SYMBOLS,
    path.join(local, 'Microsoft', 'Windows', 'Fonts', 'SymbolsNerdFontMono-Regular.ttf'),
    'C:\\Windows\\Fonts\\SymbolsNerdFontMono-Regular.ttf',
    path.join(home, '.local', 'share', 'fonts', 'SymbolsNerdFontMono-Regular.ttf'),
  ]
    .filter(Boolean)
    .find((p) => fs.existsSync(p));
  return { mono, symbols };
}

export function screenshot(browser, htmlFile, pngFile, w, h, profileDir) {
  const r = spawnSync(
    browser,
    [
      '--headless=new',
      '--disable-gpu',
      '--hide-scrollbars',
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-extensions',
      `--user-data-dir=${profileDir}`,
      `--force-device-scale-factor=${SCALE}`,
      `--window-size=${w},${h}`,
      '--virtual-time-budget=3000',
      `--screenshot=${pngFile}`,
      pathToFileURL(htmlFile).href,
    ],
    { encoding: 'utf8', timeout: 60_000 },
  );
  if (!fs.existsSync(pngFile)) throw new Error(`the browser wrote no screenshot (exit ${r.status}): ${r.stderr || r.error || ''}`);
}

// --- Main -------------------------------------------------------------------------------

async function main() {
  const o = parseArgs(process.argv.slice(2));
  const indexFile = path.join(o.in, 'index.json');
  if (!fs.existsSync(indexFile)) {
    throw new Error(`${indexFile} not found. Render the frames first:\n  DEVPIT_SHOTS=1 DEVPIT_SHOTS_OUT=${o.in} go test -tags shots ./internal/shots -run TestRender`);
  }
  const index = JSON.parse(fs.readFileSync(indexFile, 'utf8'));
  const frames = (index.rendered ?? []).filter((f) => o.only.size === 0 || o.only.has(f.name));
  if (frames.length === 0) throw new Error('no frames to render');

  const browser = findBrowser();
  const fonts = findFonts();
  console.log(`browser: ${browser}\nfont:    ${fonts.mono}\nsymbols: ${fonts.symbols ?? '(none: Nerd Font icons will draw as boxes)'}`);

  fs.mkdirSync(o.out, { recursive: true });
  const work = fs.mkdtempSync(path.join(os.tmpdir(), 'devpit-shots-html-'));
  const profile = path.join(work, 'profile');
  let failed = 0;
  try {
    for (const f of frames) {
      const ans = fs.readFileSync(path.join(o.in, `${f.name}.ans`), 'utf8');
      const grid = parseFrame(ans, f.cols, f.rows);
      const html = frameHTML(grid, f.cols, f.rows, f.theme, fonts);
      const w = PAD_X * 2 + f.cols * CELL_W;
      const h = TITLE_H + PAD_Y * 2 + f.rows * CELL_H;
      const htmlFile = path.join(work, `${f.name}.html`);
      const pngFile = path.join(work, `${f.name}.png`);
      fs.writeFileSync(htmlFile, html);
      try {
        screenshot(browser, htmlFile, pngFile, w, h, profile);
        const meta = await sharp(pngFile).metadata();
        if (meta.width !== w * SCALE || meta.height !== h * SCALE) {
          throw new Error(`screenshot is ${meta.width}x${meta.height}, expected ${w * SCALE}x${h * SCALE}`);
        }
        const dest = path.join(o.out, `${f.name}.webp`);
        await sharp(pngFile).webp({ lossless: true, effort: 6 }).toFile(dest);
        const kb = Math.round(fs.statSync(dest).size / 1024);
        console.log(`  ${f.name}.webp  ${meta.width}x${meta.height}  ${kb} KB`);
        if (o.keep) {
          fs.copyFileSync(htmlFile, path.join(o.in, `${f.name}.html`));
          fs.copyFileSync(pngFile, path.join(o.in, `${f.name}.png`));
        }
      } catch (e) {
        failed++;
        console.error(`  ${f.name}: ${e.message}`);
      }
    }
  } finally {
    fs.rmSync(work, { recursive: true, force: true, maxRetries: 3 });
  }
  for (const s of index.skipped ?? []) console.warn(`  skipped ${s.name}: ${s.reason}`);
  console.log(`${frames.length - failed} of ${frames.length} images written to ${path.relative(process.cwd(), o.out) || '.'}`);
  if (failed) process.exit(1);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((e) => {
    console.error(e.message);
    process.exit(1);
  });
}
