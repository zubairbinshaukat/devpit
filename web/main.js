/* =========================================================
   Devpit — landing page script
   1. Space: WebGL light rays from above (default) or black hole
      — switch with <html data-effect="rays" | "blackhole">
   2. Terminal demo (auto-typing scenes, junk gets pulled into the hole)
   3. Stats (tries /api/stats, falls back to dummy)
   4. Copy buttons
   No dependencies.
   ========================================================= */
(() => {
  "use strict";

  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => [...r.querySelectorAll(s)];
  const REDUCED = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* =========================================================
     1. SPACE — light rays (default) or black hole
     ========================================================= */
  
  /* ---------- shaders ---------- */
  const VERT = `attribute vec2 a; void main(){ gl_Position = vec4(a, 0.0, 1.0); }`;
  const FRAG_HOLE = `
precision highp float;
uniform vec2  uRes;
uniform float uTime;
uniform vec3  uHole;    // x, y (GL px), shadow radius (px)
uniform float uPulse;
uniform float uScroll;

float hash(vec2 p){ p = fract(p * vec2(234.34, 435.345)); p += dot(p, p + 34.23); return fract(p.x * p.y); }
float noise(vec2 p){
  vec2 i = floor(p), f = fract(p); f = f * f * (3.0 - 2.0 * f);
  float a = hash(i), b = hash(i + vec2(1.0, 0.0)), c = hash(i + vec2(0.0, 1.0)), d = hash(i + vec2(1.0, 1.0));
  return mix(mix(a, b, f.x), mix(c, d, f.x), f.y);
}
float fbm(vec2 p){
  float v = 0.0, a = 0.5;
  mat2 m = mat2(1.6, 1.2, -1.2, 1.6);
  for (int i = 0; i < 4; i++){ v += a * noise(p); p = m * p; a *= 0.5; }
  return v;
}
mat2 rot(float a){ float c = cos(a), s = sin(a); return mat2(c, -s, s, c); }

// temperature ramp: white-hot inner edge -> brand orange -> ember
vec3 hot(float t){
  vec3 c = mix(vec3(1.0, 0.97, 0.92), vec3(1.0, 0.70, 0.42), smoothstep(0.0, 0.16, t));
  c = mix(c, vec3(1.0, 0.42, 0.16), smoothstep(0.16, 0.5, t));
  c = mix(c, vec3(0.32, 0.06, 0.02), smoothstep(0.5, 1.0, t));
  return c;
}

vec3 stars(vec2 q){
  vec3 col = vec3(0.0);
  for (int l = 0; l < 3; l++){
    float fl = float(l);
    float cell = 22.0 + fl * 34.0;
    float th = 0.915 - fl * 0.02;
    vec2 g = q / cell; vec2 id = floor(g); vec2 f = fract(g);
    float h = hash(id + fl * 17.3);
    if (h > th){
      vec2 o = vec2(hash(id + 3.1 + fl), hash(id + 7.7 + fl));
      vec2 d = (f - o) * cell;
      float rad = 0.55 + (0.5 + fl * 0.55) * hash(id + 11.0 + fl);
      float b = exp(-dot(d, d) / (rad * rad));
      float tw = 0.65 + 0.35 * sin(uTime * (0.6 + 2.2 * h) + h * 70.0);
      vec3 tint = mix(vec3(0.72, 0.80, 1.0), vec3(1.0, 0.86, 0.72), hash(id + 5.0 + fl));
      col += tint * b * tw * (0.55 + fl * 0.45);
    }
  }
  return col;
}

void main(){
  vec2 fc = gl_FragCoord.xy;
  float R = max(uHole.z, 1.0);
  vec2 p = (fc - uHole.xy) / R;           // in shadow radii
  float r = length(p);
  vec2 dir = p / max(r, 1e-4);
  float pulse = 1.0 + uPulse;

  // --- background: stars + faint nebula, bent by the hole (thin-lens) ---
  vec2 lp = p * (1.0 - 2.6 / max(r * r, 1.0));
  vec2 bg = uHole.xy + lp * R + vec2(0.0, uScroll);
  vec3 col = stars(bg);
  float n1 = fbm(bg * 0.0016 + vec2(uTime * 0.004, 0.0));
  float n2 = fbm(bg * 0.0011 + vec2(7.3, 1.1));
  col += vec3(0.03, 0.08, 0.10) * smoothstep(0.45, 0.95, n1);
  col += vec3(0.12, 0.045, 0.02) * smoothstep(0.55, 1.0, n2);

  vec3 light = vec3(0.0);
  float shadow = smoothstep(0.985, 1.03, r);

  if (r < 16.0){
    float dop = 1.0 - 0.5 * dir.x;          // approaching side (left) is brighter

    // god rays fanning out from the hole
    float rn  = fbm(dir * 2.4 + vec2(uTime * 0.035, -uTime * 0.025));
    float rn2 = noise(dir * 11.0 + uTime * 0.06);
    float rays = smoothstep(0.42, 0.88, rn) * (0.55 + 0.45 * rn2) * exp(-(r - 1.0) * 0.30) * smoothstep(1.2, 2.6, r);
    light += vec3(1.0, 0.60, 0.34) * rays * 0.26;

    // soft bloom around the horizon
    light += vec3(1.0, 0.52, 0.26) * exp(-(r - 1.0) * 1.05) * 0.32 * smoothstep(0.98, 1.1, r);

    // lensed image of the far side of the disk, bent over the top and under the bottom
    vec2 hd = rot(uTime * 0.28) * dir;
    float ht = fbm(hd * 3.2 + vec2(r * 7.0));
    float halo = exp(-pow((r - 1.30) / 0.21, 2.0)) * (0.62 + 0.38 * dir.y);
    light += hot(0.12) * halo * (0.35 + ht * 1.25) * dop * 0.95;

    // photon ring
    light += vec3(1.0, 0.93, 0.84) * exp(-pow((r - 1.045) / 0.024, 2.0)) * 1.2 * dop;
  }
  col = (col + light * pulse) * shadow;

  // --- accretion disk (tilted), near half drawn over the shadow ---
  vec2 d = vec2(p.x, p.y / 0.17);
  float dr = length(d);
  float inner = 1.55, outer = 6.2;
  if (dr > inner - 0.3 && dr < outer){
    vec2 dd = d / dr;
    float w = uTime * 1.25 / pow(dr, 1.5);
    vec2 rd = rot(w) * dd;
    float tex = fbm(rd * 2.3 + vec2(dr * 4.2, -dr * 1.4));
    float rings = noise(vec2(dr * 24.0, 0.0) + rd * 0.7);
    tex = tex * 0.85 + rings * 0.35;
    float t = clamp((dr - inner) / (outer - inner), 0.0, 1.0);
    float mask = smoothstep(inner - 0.2, inner + 0.35, dr) * (1.0 - smoothstep(outer * 0.42, outer, dr));
    float dop = pow(1.0 - 0.55 * dd.x, 2.0);
    float I = mask * (0.22 + tex * 1.35) * dop * (1.5 / (0.45 + t * 6.0));
    float front = smoothstep(0.03, -0.03, p.y);
    col += hot(t) * I * mix(shadow, 1.0, front) * pulse;
  }

  // anamorphic streak through the disk plane
  col += vec3(1.0, 0.70, 0.46) * exp(-abs(fc.y - uHole.y) / (1.6 + R * 0.012)) * exp(-abs(p.x) * 0.22) * 0.20 * pulse;

  // tone map + grain
  col = vec3(1.0) - exp(-col * 1.35);
  col += (hash(fc + fract(uTime) * 91.0) - 0.5) * 0.018;
  gl_FragColor = vec4(col, 1.0);
}`;
  // Light rays, in the spirit of React Bits' "Light Rays" background:
  // layers of angular beams from a source just above the screen.
  const FRAG_RAYS = `
precision highp float;
uniform vec2  uRes;
uniform float uTime;
uniform float uPulse;
uniform float uScroll;
uniform vec2  uSrc;     // ray origin (GL px), just above the top edge
uniform vec2  uDir;     // main ray direction
uniform float uGain;
uniform vec3  uColor;

float hash(vec2 p){ p = fract(p * vec2(234.34, 435.345)); p += dot(p, p + 34.23); return fract(p.x * p.y); }
float noise(vec2 p){
  vec2 i = floor(p), f = fract(p); f = f * f * (3.0 - 2.0 * f);
  float a = hash(i), b = hash(i + vec2(1.0, 0.0)), c = hash(i + vec2(0.0, 1.0)), d = hash(i + vec2(1.0, 1.0));
  return mix(mix(a, b, f.x), mix(c, d, f.x), f.y);
}
vec3 stars(vec2 q){
  vec3 col = vec3(0.0);
  for (int l = 0; l < 3; l++){
    float fl = float(l);
    float cell = 22.0 + fl * 34.0;
    float th = 0.925 - fl * 0.02;
    vec2 g = q / cell; vec2 id = floor(g); vec2 f = fract(g);
    float h = hash(id + fl * 17.3);
    if (h > th){
      vec2 o = vec2(hash(id + 3.1 + fl), hash(id + 7.7 + fl));
      vec2 d = (f - o) * cell;
      float rad = 0.55 + (0.5 + fl * 0.5) * hash(id + 11.0 + fl);
      float tw = 0.65 + 0.35 * sin(uTime * (0.6 + 2.2 * h) + h * 70.0);
      col += vec3(0.80, 0.90, 1.0) * exp(-dot(d, d) / (rad * rad)) * tw * (0.45 + fl * 0.4);
    }
  }
  return col;
}
float rayStrength(vec2 src, vec2 refDir, vec2 coord, float seedA, float seedB, float speed){
  vec2 v = coord - src;
  float dist = length(v);
  vec2 dn = v / max(dist, 1e-4);
  float a = dot(dn, refDir);
  a += 0.03 * sin(uTime * 1.3 + dist * 0.006);                 // gentle wobble
  float spread = pow(max(a, 0.0), 1.2);
  float maxD = uRes.y * 2.3;
  float lenFall = clamp((maxD - dist) / maxD, 0.0, 1.0);
  float fade = clamp((uRes.y * 1.7 - dist) / (uRes.y * 1.7), 0.3, 1.0);
  float base = clamp((0.45 + 0.15 * sin(a * seedA + uTime * speed)) +
                     (0.30 + 0.20 * cos(-a * seedB + uTime * speed)), 0.0, 1.0);
  base = pow(base, 3.2);                                          // crisp, separate shafts
  return base * lenFall * fade * spread;
}
void main(){
  vec2 fc = gl_FragCoord.xy;
  vec3 col = stars(fc + vec2(0.0, uScroll)) * 0.75;

  float r1 = rayStrength(uSrc, uDir, fc, 36.2214, 21.11349, 0.80);
  float r2 = rayStrength(uSrc, uDir, fc, 22.3991, 18.0234, 0.60);
  float r3 = rayStrength(uSrc, uDir, fc, 71.137, 53.719, 0.45);   // fine streaks
  float rays = r1 * 0.6 + r2 * 0.5 + r3 * 0.3;
  rays *= 0.86 + 0.14 * noise(fc * 0.006 + uTime * 0.08);      // soft shimmer
  float b = fc.y / uRes.y;                                       // brighter near the top
  rays *= 0.04 + pow(b, 1.5) * 1.05;

  float glow = exp(-length(fc - uSrc) / (uRes.y * 0.32));
  vec3 light = uColor * rays * 1.05 + mix(uColor, vec3(1.0), 0.6) * glow * 0.14;
  col += light * uGain * (1.0 + uPulse * 0.7);

  col = vec3(1.0) - exp(-col * 1.3);
  col += (hash(fc + fract(uTime) * 91.0) - 0.5) * 0.016;
  gl_FragColor = vec4(col, 1.0);
}`;

  /* ---------- renderer ----------
     Self-contained so it can run inside a Web Worker (OffscreenCanvas) and never
     block the page. Falls back to the main thread only where workers can't draw. */
  function Renderer(canvas, o, post) {
    const S = { vw: o.vw, vh: o.vh, scrollY: 0, docH: o.vh, mx: 0, my: 0, hx: o.vw / 2, hy: o.vh / 2, hr: 90, hidden: false };
    let pulse = 0, gain = 1, px = 0, py = 0;
    const raf = typeof requestAnimationFrame === "function" ? requestAnimationFrame : (f) => setTimeout(() => f(performance.now()), 16);
    const noop = { set() {} };

    // failIfMajorPerformanceCaveat + renderer check: no WebGL on software rendering (no GPU);
    // those devices keep the CSS rays, which cost almost nothing.
    const attrs = { antialias: false, alpha: false, depth: false, powerPreference: "high-performance", failIfMajorPerformanceCaveat: true };
    let gl = null;
    try { gl = canvas.getContext("webgl", attrs) || canvas.getContext("experimental-webgl", attrs); } catch (_) {}
    if (!gl) { post({ type: "fail" }); return noop; }
    const info = gl.getExtension("WEBGL_debug_renderer_info");
    const rname = String(gl.getParameter(info ? info.UNMASKED_RENDERER_WEBGL : gl.RENDERER) || "");
    if (/swiftshader|llvmpipe|softpipe|software|basic render/i.test(rname)) { post({ type: "fail", why: "software" }); return noop; }

    const prog = gl.createProgram();
    const shader = (type, src) => { const s = gl.createShader(type); gl.shaderSource(s, src); gl.compileShader(s); gl.attachShader(prog, s); return s; };
    shader(gl.VERTEX_SHADER, o.vert);
    const fs = shader(gl.FRAGMENT_SHADER, o.frag);
    gl.linkProgram(prog);

    let U = null, W = 0, H = 0, scale = o.dpr >= 2 ? 1.0 : 0.8, running = false;
    let t0 = 0, last = 0, slow = 0, frames = 0, lastW = 0, lastH = 0;

    function resize() {
      W = Math.max(1, Math.round(S.vw * scale));
      H = Math.max(1, Math.round(S.vh * scale));
      canvas.width = W; canvas.height = H;
      gl.viewport(0, 0, W, H);
      lastW = S.vw; lastH = S.vh;
    }
    function draw(now) {
      if (S.vw !== lastW || S.vh !== lastH) resize();
      const dt = now - last; last = now; frames++;
      if (frames > 30 && dt > 26) slow++; else slow = Math.max(0, slow - 1);
      if (slow > 40 && scale > 0.45) { scale = Math.max(0.45, scale - 0.15); resize(); slow = 0; }

      // rays are strongest at the top and bottom of the page, softer in between
      const top = Math.min(1, Math.max(0, 1 - S.scrollY / (S.vh * 1.1)));
      const bottom = Math.min(1, Math.max(0, 1 - (S.docH - S.scrollY - S.vh) / (S.vh * 1.1)));
      const want = 0.5 + 0.5 * Math.max(top, bottom);
      gain += (want - gain) * (o.reduced ? 1 : 0.08);
      px += (S.mx - px) * 0.04; py += (S.my - py) * 0.04;

      const t = o.reduced ? 18 : (now - t0) / 1000;
      gl.uniform2f(U.uRes, W, H);
      gl.uniform1f(U.uTime, t);
      gl.uniform3f(U.uHole, (S.hx + px * 18) * scale, (S.vh - (S.hy + py * 12)) * scale, S.hr * scale);
      gl.uniform1f(U.uPulse, pulse);
      gl.uniform1f(U.uScroll, S.scrollY * 0.12 * scale);
      let dx = px * 0.35, dy = -1;
      const dl = Math.hypot(dx, dy); dx /= dl; dy /= dl;
      gl.uniform2f(U.uSrc, W / 2 + px * 60 * scale, H * 1.2);
      gl.uniform2f(U.uDir, dx, dy);
      gl.uniform1f(U.uGain, gain);
      gl.uniform3f(U.uColor, 0.58, 0.90, 0.97);   // soft glacier aqua (#94E6F7)
      gl.drawArrays(gl.TRIANGLES, 0, 3);
      pulse *= 0.94;
    }
    function start() {
      gl.useProgram(prog);
      const buf = gl.createBuffer();
      gl.bindBuffer(gl.ARRAY_BUFFER, buf);
      gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
      const loc = gl.getAttribLocation(prog, "a");
      gl.enableVertexAttribArray(loc);
      gl.vertexAttribPointer(loc, 2, gl.FLOAT, false, 0, 0);
      U = {};
      ["uRes", "uTime", "uHole", "uPulse", "uScroll", "uSrc", "uDir", "uGain", "uColor"].forEach((n) => { U[n] = gl.getUniformLocation(prog, n); });
      resize();
      t0 = last = performance.now();
      running = true;
      draw(t0);
      post({ type: "ready" });
      if (!o.reduced) {
        const loop = (now) => { if (!S.hidden) draw(now); raf(loop); };
        raf(loop);
      }
    }
    const finish = () => {
      if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) { post({ type: "fail", why: gl.getShaderInfoLog(fs) || gl.getProgramInfoLog(prog) }); return; }
      start();
    };
    // KHR_parallel_shader_compile: poll instead of waiting on the compiler
    const par = gl.getExtension("KHR_parallel_shader_compile");
    if (par) { const poll = () => (gl.getProgramParameter(prog, par.COMPLETION_STATUS_KHR) ? finish() : setTimeout(poll, 60)); poll(); }
    else finish();

    return {
      set(m) {
        if (m.type === "bump") pulse = Math.min(1.4, pulse + m.v);
        else if (m.type === "state") Object.assign(S, m.state);
        if (o.reduced && running) draw(performance.now());
      },
    };
  }

  const WORKER_SRC = `const Renderer = ${Renderer.toString()};
let r = null;
self.onmessage = (e) => {
  const m = e.data;
  if (m.type === "init") r = Renderer(m.canvas, m.opts, (msg) => self.postMessage(msg));
  else if (r) r.set(m);
};`;

  /* ---------- page side ---------- */
  const EFFECT = document.documentElement.dataset.effect === "blackhole" ? "blackhole" : "rays";

  const Space = (() => {
    const canvas = $("#space");
    const anchors = [$("#hole-top"), $("#hole-end")].filter(Boolean);
    const hole = { x: innerWidth / 2, y: innerHeight * 0.55, r: 90 };
    let mx = 0, my = 0, send = () => {};
    let docH = document.documentElement.scrollHeight;

    // black-hole mode: the hole sits on whichever anchor is closest to the viewport center
    function locate() {
      if (EFFECT === "rays") return;
      let best = null, bestD = Infinity;
      for (const a of anchors) {
        const r = a.getBoundingClientRect();
        const cy = r.top + r.height / 2;
        const d = Math.abs(cy - innerHeight / 2);
        if (d < bestD) { bestD = d; best = { x: r.left + r.width / 2, y: cy, r: r.height * 0.25 }; }
      }
      if (best) Object.assign(hole, best);
    }
    const snapshot = () => {
      locate();
      return { type: "state", state: { vw: innerWidth, vh: innerHeight, scrollY, docH, mx, my, hx: hole.x, hy: hole.y, hr: hole.r, hidden: document.hidden } };
    };
    let queued = false;
    const schedule = () => { if (queued) return; queued = true; requestAnimationFrame(() => { queued = false; send(snapshot()); }); };
    addEventListener("scroll", schedule, { passive: true });
    addEventListener("resize", () => { docH = document.documentElement.scrollHeight; schedule(); });
    addEventListener("pointermove", (e) => { mx = e.clientX / innerWidth - 0.5; my = e.clientY / innerHeight - 0.5; schedule(); }, { passive: true });
    document.addEventListener("visibilitychange", schedule);
    if ("ResizeObserver" in window) new ResizeObserver(() => { docH = document.documentElement.scrollHeight; schedule(); }).observe(document.body);

    function fallback() {
      document.documentElement.classList.add("no-webgl");
      send = () => {};
    }
    function onMsg(m) {
      if (m.type === "ready") canvas.classList.add("is-on");   // live rays fade in over the CSS ones
      else if (m.type === "fail") fallback();
    }
    function boot() {
      if (!canvas) return;
      const opts = { vert: VERT, frag: EFFECT === "rays" ? FRAG_RAYS : FRAG_HOLE, reduced: REDUCED, dpr: devicePixelRatio || 1, vw: innerWidth, vh: innerHeight };
      if (canvas.transferControlToOffscreen && typeof Worker === "function") {
        try {
          const off = canvas.transferControlToOffscreen();
          const w = new Worker(URL.createObjectURL(new Blob([WORKER_SRC], { type: "text/javascript" })));
          w.onmessage = (e) => onMsg(e.data);
          w.onerror = fallback;
          w.postMessage({ type: "init", canvas: off, opts }, [off]);
          send = (m) => w.postMessage(m);
          send(snapshot());
          return;
        } catch (_) { /* fall through to main thread */ }
      }
      const r = Renderer(canvas, opts, onMsg);
      send = (m) => r.set(m);
      send(snapshot());
    }
    // start after the page has loaded, when the browser is idle
    const kick = () => ("requestIdleCallback" in window ? requestIdleCallback(boot, { timeout: 1500 }) : setTimeout(boot, 300));
    if (document.readyState === "complete") kick(); else addEventListener("load", kick, { once: true });
    locate();

    return {
      effect: EFFECT,
      get hole() { locate(); return hole; },
      // where pulled-away junk flies to, in viewport CSS px
      target() { return EFFECT === "rays" ? { x: innerWidth / 2 + mx * 60, y: -30 } : (locate(), { x: hole.x, y: hole.y }); },
      bump(v = 0.5) { send({ type: "bump", v }); },
    };
  })();

  /* Pull a piece of text away: up into the light (rays) or into the hole */
  const junkLayer = $("#junk-layer");
  function swallow(fromEl, text) {
    if (REDUCED || !fromEl || !junkLayer) return;
    const r = fromEl.getBoundingClientRect();
    if (r.bottom < 0 || r.top > innerHeight) return;
    const s = document.createElement("span");
    s.className = "junk";
    s.textContent = text;
    junkLayer.appendChild(s);
    const x0 = r.left + 40, y0 = r.top + r.height / 2;
    const side = Math.random() < 0.5 ? -1 : 1;
    const rays = Space.effect === "rays";
    const dur = rays ? 1800 + Math.random() * 400 : 1500 + Math.random() * 400;
    const start = performance.now();
    const step = (now) => {
      const k = Math.min(1, (now - start) / dur);
      if (rays) {
        const e = Math.pow(k, 1.7);               // drift, then rise faster into the light
        const tg = Space.target();
        const x = x0 + (tg.x - x0) * e * 0.3 + Math.sin(k * Math.PI * 2.2) * 24 * side * (1 - k);
        const y = y0 + (tg.y - y0) * e;
        s.style.transform = `translate(${x}px, ${y}px) translate(-50%, -50%) scale(${1 - 0.4 * e})`;
        s.style.opacity = k < 0.5 ? 1 : Math.max(0, 1 - (k - 0.5) / 0.5);
        s.style.filter = `blur(${e * 2.2}px)`;
        if (k < 1) requestAnimationFrame(step);
        else { s.remove(); Space.bump(0.45); }
        return;
      }
      const e = Math.pow(k, 1.9);                 // accelerate into the hole
      const h = Space.hole;
      const dx = x0 - h.x, dy = y0 - h.y;
      const a = Math.atan2(dy, dx) + side * e * 1.3;
      const dist = Math.hypot(dx, dy) * (1 - e);
      const flat = 1 - 0.6 * e;                    // settle into the disk plane
      const x = h.x + Math.cos(a) * dist;
      const y = h.y + Math.sin(a) * dist * flat;
      const sc = 1 - 0.9 * e;
      s.style.transform = `translate(${x}px, ${y}px) translate(-50%, -50%) scale(${sc}) rotate(${side * e * 160}deg)`;
      s.style.opacity = k < 0.8 ? 1 : 1 - (k - 0.8) / 0.2;
      s.style.filter = `blur(${e * 1.6}px)`;
      if (k < 1) requestAnimationFrame(step);
      else { s.remove(); Space.bump(0.55); }
    };
    requestAnimationFrame(step);
  }

  /* =========================================================
     2. TERMINAL DEMO
     ========================================================= */
  const term = $("#term");
  const keysEl = $("#term-keys");
  const pressEl = $("#term-press");
  const dotsEl = $("#subs-dots");
  const subsText = $("#subs-text");
  const metricEl = $("#subs-metric");
  const pauseBtn = $("#demo-pause");
  const tabs = $$(".tab");

  const CANCEL = Symbol("cancel");
  const PROMPT = '<span class="prompt">PS C:\\dev&gt;</span> ';
  let runId = 0, paused = false, offscreen = false, captions = [];

  function sleep(ms) {
    const id = runId;
    return new Promise((resolve, reject) => {
      let left = REDUCED ? 0 : ms;
      const step = () => {
        if (id !== runId) return reject(CANCEL);
        if (left <= 0) return resolve();
        if (!paused && !offscreen && !document.hidden) left -= 40;
        setTimeout(step, 40);
      };
      step();
    });
  }

  const el = (tag, cls, html = "") => { const n = document.createElement(tag); if (cls) n.className = cls; n.innerHTML = html; return n; };
  const scroll = () => { term.scrollTop = term.scrollHeight; };
  const add = (node, parent = term) => { parent.appendChild(node); scroll(); return node; };
  const line = (html = "", parent = term) => add(el("div", "tl", html), parent);
  const gap = (parent = term) => add(el("div", "tl--gap"), parent);

  async function typeInto(span, text, speed = 42) {
    for (const ch of text) { span.textContent += ch; scroll(); await sleep(speed + Math.random() * speed * 0.8); }
  }
  async function cmd(text, speed) {
    const l = line(PROMPT);
    const t = el("span", "t-w"), c = el("span", "cursor");
    l.append(t, c);
    await sleep(380);
    await typeInto(t, text, speed ?? (text.length > 30 ? 24 : 55));
    await sleep(320);
    c.remove();
    return l;
  }
  const idlePrompt = () => { line(PROMPT).appendChild(el("span", "cursor")); };

  async function press(label) {
    if (REDUCED) return;
    pressEl.textContent = label;
    pressEl.classList.add("is-on");
    await sleep(340);
    pressEl.classList.remove("is-on");
    await sleep(120);
  }

  const KEYS = {
    shell: "",
    menu: "<b>↑↓</b> move · <b>Enter</b> select · <b>Esc</b> back · <b>?</b> help",
    scan: "<b>Esc</b> stop safely",
    results: "<b>n</b> node_modules · <b>o</b> older than 30 days · <b>/</b> search · <b>Space</b> tick · <b>Enter</b> clean",
    del: "<b>Esc</b> stop · finished items stay finished",
    done: "<b>Enter</b> back to menu · <b>Esc</b> exit",
    input: "<b>Enter</b> confirm · <b>Esc</b> back",
    list: "<b>Space</b> tick · <b>a</b> all · <b>Enter</b> update",
  };
  const keys = (k) => { keysEl.innerHTML = KEYS[k] ?? k; };

  function setCaptions(list) {
    captions = list;
    dotsEl.innerHTML = list.map(() => "<i></i>").join("");
  }
  let capTimer;
  function cap(i) {
    $$("i", dotsEl).forEach((d, j) => { d.classList.toggle("is-past", j < i); d.classList.toggle("is-now", j === i); });
    clearTimeout(capTimer);
    if (REDUCED) { subsText.innerHTML = captions[i]; return; }
    subsText.classList.add("is-fading");
    capTimer = setTimeout(() => { subsText.innerHTML = captions[i]; subsText.classList.remove("is-fading"); }, 220);
  }

  let metricNow = 0, metricFmt = (v) => v;
  function metric(fmt) { metricFmt = fmt; metricNow = 0; metricEl.textContent = ""; }
  async function metricTo(to, ms = 600) {
    const from = metricNow, steps = REDUCED ? 1 : Math.max(1, Math.round(ms / 40));
    for (let s = 1; s <= steps; s++) {
      metricEl.textContent = metricFmt(from + (to - from) * (1 - Math.pow(1 - s / steps, 3)));
      if (s < steps) await sleep(40);
    }
    metricNow = to;
  }

  /* ----- Devpit TUI pieces ----- */
  const MENU = [
    ["◧", "Free Up Disk Space", "~12.4 GB can be freed"],
    ["⊘", "Fix Stuck Ports & Apps", "Port 3000 busy? Kill it"],
    ["⊕", "Install Developer Apps", "via Scoop · 23 apps installed"],
    ["↻", "Update Everything", "Scoop, winget, npm · 5 updates"],
    ["◎", "Network Tools", "IP, ping, DNS flush"],
    ["⎇", "Git & SSH Setup", "name, email, SSH keys"],
    ["⚙\uFE0E", "Devpit Settings", "folders, theme, rescan tools"],
  ];
  function openApp(free = "18.4 GB") {
    const app = el("div", "app",
      `<div class="app__status"><span class="app__name">◆ devpit 1.0.0</span>` +
      `<span class="t-m">node <span class="t-c">22.9.0</span></span>` +
      `<span class="t-m">git <span class="t-c">2.46.0</span></span>` +
      `<span class="t-m">C: <span class="t-w" data-free>${free}</span> free</span></div>` +
      `<div class="app__screen"></div>`);
    add(app);
    return { app, screen: $(".app__screen", app), free: $("[data-free]", app) };
  }
  const title = (t, sub = "") => `<div class="app__title">${t}</div>${sub ? `<div class="t-m">${sub}</div>` : ""}<div class="tl--gap"></div>`;
  function renderMenu(screen, sel) {
    screen.innerHTML =
      `<div class="t-m">Pit crew ready. Pick a section.</div><div class="tl--gap"></div>` +
      MENU.map((m, i) => `<div class="menu-row${i === sel ? " is-sel" : ""}"><span>${i === sel ? "▸" : " "}</span><span>${m[0]}</span><span class="menu-name">${m[1]}</span><span class="menu-desc">${m[2]}</span></div>`).join("") +
      `<div class="tl--gap"></div><div><span class="t-o">▲</span> <span class="t-m">12.4 GB can be freed since your last scan.</span></div>`;
    scroll();
  }
  function renderList(screen, head, items, sel) {
    screen.innerHTML = head + items.map((m, i) =>
      `<div class="menu-row${i === sel ? " is-sel" : ""}" style="grid-template-columns:2ch minmax(26ch,auto) 1fr"><span>${i === sel ? "▸" : " "}</span><span class="menu-name">${m[0]}</span><span class="menu-desc">${m[1] ?? ""}</span></div>`).join("");
    scroll();
  }
  async function moveMenu(screen, from, to) {
    let i = from;
    while (i !== to) { i += to > from ? 1 : -1; await press(to > from ? "↓" : "↑"); renderMenu(screen, i); await sleep(160); }
  }
  const SPIN = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏";
  const size = (gb) => (gb < 1 ? `${Math.round(gb * 1000)} MB` : `${gb.toFixed(2)} GB`);
  const badge = (r) => `<span class="badge--${r} t-b">${r[0].toUpperCase() + r.slice(1)}</span>`;
  const bar = (frac, w = 28) => { const f = Math.round(frac * w); return `<span class="t-o">${"━".repeat(f)}</span><span class="t-m">${"─".repeat(w - f)}</span>`; };

  /* ----- SCENE 1: Free up space ----- */
  async function sceneFree() {
    setCaptions([
      "You type <b>devpit</b>. It opens instantly and already knows about 12 GB can go.",
      "One scan checks every project, cache and Docker leftover on your machine.",
      "Everything is sized and flagged Safe, Review or Careful. Projects you're working on stay unticked.",
      "Nothing is deleted until you press <b>y</b>. The default is always No.",
      "Gone. 8.7 GB back, and the one locked folder is skipped and explained.",
    ]);
    metric((v) => `${v.toFixed(1)} GB freed`);
    keys("shell"); cap(0);
    await sleep(500);
    await cmd("devpit");
    const A = openApp();
    keys("menu"); renderMenu(A.screen, 0);
    await sleep(1500);
    await press("Enter");

    renderList(A.screen, title("Free Up Disk Space", "Only showing what's installed on this machine."), [
      ["Full Scan (recommended)", "everything below, one table"],
      ["Project Junk", "node_modules, dist, .next, venv…"],
      ["Package Caches", "npm, pnpm, pip"],
      ["Docker", "build cache, images, volumes"],
      ["Scoop Old Versions", "previous app versions"],
      ["Find Large Files", "anything over 1 GB"],
    ], 0);
    await sleep(1000);
    await press("Enter");

    cap(1); keys("scan");
    A.screen.innerHTML = title("Full Scan", "Projects in D:\\Projects, plus npm, pnpm, pip, Docker and Scoop");
    const sl = line("", A.screen);
    const paths = ["D:\\Projects\\shop-next", "D:\\Projects\\api", "D:\\Projects\\blog", "D:\\Projects\\landing-v2",
      "%LOCALAPPDATA%\\npm-cache", "%LOCALAPPDATA%\\pip\\Cache", "Docker build cache", "~\\scoop\\apps"];
    let folders = 0, found = 0, gb = 0;
    for (let f = 0; f < (REDUCED ? 1 : 36); f++) {
      folders += 30 + Math.floor(Math.random() * 60);
      if (f % 4 === 1) found++;
      gb = Math.min(14.0, gb + 0.39 + Math.random() * 0.05);
      const p = paths[Math.min(paths.length - 1, Math.floor(f / 4.5))];
      sl.innerHTML = `<span class="t-o">${SPIN[f % SPIN.length]}</span> Checking <span class="t-c">${p}</span>…  <span class="t-m">${folders.toLocaleString()} folders · ${found} found ·</span> <span class="t-w">${gb.toFixed(1)} GB</span>`;
      await sleep(85);
    }

    cap(2); keys("results");
    const ITEMS = [
      { g: "D:\\Projects\\shop-next", meta: "opened 41 days ago" },
      { n: "node_modules", r: "safe", s: 1.84, on: 1 },
      { n: ".next", r: "safe", s: 0.61, on: 1 },
      { g: "D:\\Projects\\blog", meta: "opened 63 days ago" },
      { n: "node_modules", r: "safe", s: 0.37, on: 1 },
      { g: "D:\\Projects\\api", meta: '<span class="t-g">Active</span> <span class="t-m">· opened today</span>' },
      { n: "node_modules", r: "safe", s: 0.94, on: 0 },
      { g: "Package caches" },
      { n: "npm cache", r: "safe", s: 3.1, on: 1 },
      { n: "pip cache", r: "review", s: 1.2, on: 0 },
      { g: "Docker" },
      { n: "build cache", r: "safe", s: 2.4, on: 1 },
      { n: "volumes (3)", r: "careful", s: 3.8, on: 0 },
      { g: "Scoop old versions" },
      { n: "nodejs 20.11, python 3.11", r: "safe", s: 0.75, on: 1 },
    ];
    A.screen.innerHTML = title("Found 9 items · 14.0 GB", "Active projects are never pre-ticked");
    let selGB = 0, selN = 0;
    for (const it of ITEMS) {
      if (it.g) add(el("div", "grp", `${it.g}${it.meta ? `  <span class="t-m" style="font-weight:400">${it.meta}</span>` : ""}`), A.screen);
      else {
        add(el("div", `row${it.on ? "" : " is-off"}`,
          `<span>${it.on ? '<span class="t-g">[x]</span>' : '<span class="t-m">[ ]</span>'}</span><span>${it.n}</span><span>${badge(it.r)}</span><span class="size">${size(it.s)}</span>`), A.screen);
        if (it.on) { selGB += it.s; selN++; }
      }
      await sleep(70);
    }
    add(el("div", "sel-total", `<span>Selected: <span class="t-g t-b">${selGB.toFixed(2)} GB</span> <span class="t-m">(${selN} of 9)</span></span><span class="t-m">Dry run: off</span>`), A.screen);
    await sleep(2600);
    await press("Enter");

    cap(3); keys("input");
    gap(A.screen);
    const conf = line(`<span class="t-a">?</span> Delete ${selN} items <span class="t-w">(${selGB.toFixed(2)} GB)</span>? You can reinstall with <span class="t-c">npm install</span>. <span class="t-w">[y/N]</span> `, A.screen);
    const cur = el("span", "cursor"); conf.appendChild(cur);
    await sleep(1300);
    cur.remove();
    conf.insertAdjacentHTML("beforeend", '<span class="t-w">y</span>');
    await press("y");

    cap(4); keys("del");
    A.screen.innerHTML = title("Cleaning");
    const pl = line("", A.screen);
    gap(A.screen);
    const DEL = [
      ["shop-next\\node_modules", 1.84, "node_modules"],
      ["shop-next\\.next", 0.61, ".next"],
      ["blog\\node_modules", 0.37, null, "skip"],
      ["npm cache", 3.1, "npm-cache"],
      ["Docker build cache", 2.4, "docker build cache"],
      ["Scoop: nodejs 20.11, python 3.11", 0.75, "nodejs 20.11"],
    ];
    let freed = 0;
    const drawBar = (d) => { pl.innerHTML = `${bar(d / DEL.length)}  <span class="t-w">${d}/${DEL.length}</span> <span class="t-m">·</span> <span class="t-g">${freed.toFixed(1)} GB freed</span>`; };
    drawBar(0);
    for (let i = 0; i < DEL.length; i++) {
      const [name, s, junk, skip] = DEL[i];
      await sleep(520 + Math.random() * 380);
      if (skip) {
        line(`<span class="t-a">⚠ Skipped ${name}</span> <span class="t-m">— it's open in VS Code. Close it and press R to retry.</span>`, A.screen);
      } else {
        freed += s;
        const l = line(`<span class="t-g">✔</span> ${name.padEnd(34, " ")}<span class="t-w">${size(s).padStart(9, " ")}</span>`, A.screen);
        swallow(l, junk);
        l.classList.add("is-gone");
        metricTo(freed, 380).catch(() => {});
      }
      drawBar(i + 1);
    }
    await sleep(900);

    keys("done");
    A.screen.innerHTML =
      title("Done") +
      `<div class="done-box"><span class="t-g t-b">✔ Freed 8.7 GB in 42s. Back on track</span></div><div class="tl--gap"></div>` +
      `<div class="tl"><span class="t-m">Space freed      </span><span class="t-g">8.7 GB</span></div>` +
      `<div class="tl"><span class="t-m">Folders deleted  </span>5</div>` +
      `<div class="tl"><span class="t-m">Time taken       </span>42s</div>` +
      `<div class="tl"><span class="t-m">Skipped          </span><span class="t-a">1</span> <span class="t-m">blog\\node_modules (open in VS Code)</span></div>`;
    A.free.textContent = "27.1 GB";
    scroll();
    await sleep(2600);
    await press("Esc");
    keys("shell");
    line(`<span class="t-m">You've freed</span> <span class="t-g">38.2 GB</span> <span class="t-m">with Devpit. See you next lap.</span>`);
    idlePrompt();
  }

  /* ----- SCENE 2: Port 3000 stuck ----- */
  async function scenePort() {
    setCaptions([
      "Your dev server won't start. Something is already sitting on port 3000.",
      "Open Devpit, pick <b>Fix Stuck Ports &amp; Apps</b>, type 3000.",
      "It shows exactly which app holds the port, and asks before stopping it.",
      "Port free, server up. No Task Manager, no netstat.",
    ]);
    metric((v) => `fixed in ${Math.round(v)}s`);
    keys("shell"); cap(0);
    await sleep(400);
    await cmd("npm run dev");
    line('<span class="t-m">&gt; shop-next@1.4.0 dev</span>');
    line('<span class="t-m">&gt; next dev</span>');
    await sleep(600);
    gap();
    line('<span class="t-r">Error: listen EADDRINUSE: address already in use :::3000</span>');
    line('<span class="t-m">    at Server.setupListenHandle [as _listen2] (node:net:1908:16)</span>');
    await sleep(1600);

    cap(1);
    await cmd("devpit");
    const A = openApp();
    keys("menu"); renderMenu(A.screen, 0);
    await sleep(800);
    await moveMenu(A.screen, 0, 1);
    await sleep(350);
    await press("Enter");
    renderList(A.screen, title("Fix Stuck Ports & Apps"), [
      ["Kill a specific port", "type a port number"],
      ["Busy dev ports", '<span class="t-a">2 in use</span> · 3000, 5173'],
      ["Stop stuck Node processes", "3 running"],
    ], 0);
    await sleep(900);
    await press("Enter");

    keys("input");
    A.screen.innerHTML = title("Kill a specific port");
    const inp = line("Port number: ", A.screen);
    const t = el("span", "t-w"), c = el("span", "cursor");
    inp.append(t, c);
    await sleep(400);
    await typeInto(t, "3000", 120);
    await sleep(300);
    c.remove();
    await press("Enter");

    cap(2);
    gap(A.screen);
    const held = line('<span class="t-w">3000</span> is held by <span class="t-o t-b">node.exe</span> <span class="t-m">(PID 18244)</span>', A.screen);
    line('<span class="t-m">started 3h ago from</span> <span class="t-c">D:\\Projects\\shop-next</span>', A.screen);
    gap(A.screen);
    const q = line('<span class="t-a">?</span> Stop node.exe on port 3000? <span class="t-w">[y/N]</span> ', A.screen);
    const qc = el("span", "cursor"); q.appendChild(qc);
    await sleep(1200);
    qc.remove();
    q.insertAdjacentHTML("beforeend", '<span class="t-w">y</span>');
    await press("y");
    swallow(held, "node.exe · PID 18244");
    held.classList.add("is-gone");
    await sleep(500);
    line('<span class="t-g t-b">✔ Stopped node.exe. Port 3000 is free.</span>', A.screen);
    metricTo(4, 500).catch(() => {});
    keys("done");
    await sleep(1500);

    cap(3);
    await press("Esc");
    keys("shell");
    await cmd("npm run dev");
    line('<span class="t-m">&gt; next dev</span>');
    await sleep(700);
    line('  <span class="t-w">▲ Next.js 15.0.3</span>');
    line('  <span class="t-m">- Local:</span>        <span class="t-c">http://localhost:3000</span>');
    await sleep(400);
    line(' <span class="t-g">✓</span> Ready in 1.8s');
    idlePrompt();
  }

  /* ----- SCENE 3: Update everything ----- */
  async function sceneUpdate() {
    setCaptions([
      "One action checks every package manager on your machine: Scoop, winget and npm.",
      "You see every update first, and untick anything you'd rather keep as it is.",
      "Updates run one by one. Old versions go straight into the pit.",
      "A clear summary: what updated, what you skipped, and why anything failed.",
    ]);
    metric((v) => `${Math.round(v)} apps updated`);
    keys("shell"); cap(0);
    await sleep(400);
    await cmd("devpit");
    const A = openApp();
    keys("menu"); renderMenu(A.screen, 0);
    await sleep(700);
    await moveMenu(A.screen, 0, 3);
    await sleep(350);
    await press("Enter");

    keys("scan");
    A.screen.innerHTML = title("Update Everything");
    for (const [m, n] of [["scoop", 2], ["winget", 2], ["npm -g", 1]]) {
      const l = line("", A.screen);
      for (let f = 0; f < (REDUCED ? 1 : 9); f++) { l.innerHTML = `<span class="t-o">${SPIN[f % SPIN.length]}</span> Checking <span class="t-c">${m}</span>…`; await sleep(70); }
      l.innerHTML = `<span class="t-g">✔</span> ${m.padEnd(8, " ")}<span class="t-m">${n} update${n > 1 ? "s" : ""}</span>`;
    }
    await sleep(500);

    cap(1); keys("list");
    const UP = [
      ["git", "2.46.0", "2.47.1", "scoop", 1],
      ["nodejs-lts", "22.9.0", "22.11.0", "scoop", 1],
      ["VS Code", "1.94.2", "1.95.3", "winget", 1],
      ["Docker Desktop", "4.34.2", "4.35.1", "winget", 1],
      ["pnpm", "9.11.0", "9.14.2", "npm -g", 1],
    ];
    const drawUp = (cursor) => {
      A.screen.innerHTML = title("5 updates available", "Untick anything you want to keep as it is.") +
        UP.map((u, i) =>
          `<div class="row${u[4] ? "" : " is-off"}${i === cursor ? " is-cur" : ""}" style="grid-template-columns:4ch minmax(0,1fr) 8ch">` +
          `<span>${u[4] ? '<span class="t-g">[x]</span>' : '<span class="t-m">[ ]</span>'}</span>` +
          `<span>${u[0].padEnd(16, " ")}<span class="t-m">${u[1]} →</span> <span class="${u[4] ? "t-w" : "t-m"}">${u[2]}</span></span>` +
          `<span class="t-m">${u[3]}</span></div>`).join("") +
        `<div class="sel-total"><span>Selected: <span class="t-g t-b">${UP.filter((u) => u[4]).length}</span> <span class="t-m">of 5</span></span><span class="t-m">Devpit itself is up to date</span></div>`;
      scroll();
    };
    drawUp(0);
    await sleep(900);
    for (let i = 1; i <= 3; i++) { await press("↓"); drawUp(i); await sleep(150); }
    await sleep(400);
    await press("Space");
    UP[3][4] = 0; drawUp(3);
    await sleep(1200);
    await press("Enter");

    cap(2); keys("del");
    A.screen.innerHTML = title("Updating 4 apps");
    let done = 0;
    for (const u of UP.filter((x) => x[4])) {
      const l = line("", A.screen);
      for (let f = 0; f < (REDUCED ? 1 : 14); f++) {
        l.innerHTML = `<span class="t-o">${SPIN[f % SPIN.length]}</span> ${u[0].padEnd(16, " ")}${bar(f / 13, 16)} <span class="t-m">via ${u[3]}</span>`;
        await sleep(70);
      }
      l.innerHTML = `<span class="t-g">✔</span> ${u[0].padEnd(16, " ")}<span class="t-m" data-old>${u[1]}</span> <span class="t-m">→</span> <span class="t-w">${u[2]}</span>`;
      swallow($("[data-old]", l), `${u[0]} ${u[1]}`);
      done++;
      metricTo(done, 300).catch(() => {});
    }
    await sleep(600);

    cap(3); keys("done");
    gap(A.screen);
    add(el("div", "done-box", `<span class="t-g t-b">✔ Updated 4</span> <span class="t-m">·</span> <span class="t-a">Skipped 1</span> <span class="t-m">·</span> Failed 0 <span class="t-m">· 1m 12s</span>`), A.screen);
    line('<span class="t-m">Skipped Docker Desktop (you unticked it).</span>', A.screen);
    await sleep(2400);
    await press("Esc");
    keys("shell");
    idlePrompt();
  }

  /* ----- SCENE 4: Install ----- */
  async function sceneInstall() {
    setCaptions([
      "Paste one line into PowerShell. No setup wizard to click through.",
      "It downloads the latest release from GitHub and checks its SHA256 checksum.",
      "Devpit goes on your PATH, so it works in PowerShell, CMD and Windows Terminal.",
      "Type <b>devpit</b>, anywhere. That's the whole setup.",
    ]);
    metric((v) => `ready in ${Math.round(v)}s`);
    keys("shell"); cap(0);
    await sleep(400);
    await cmd("irm https://devpit.zubyr.dev/install | iex");

    cap(1);
    line('<span class="t-o t-b">◆ Devpit installer</span>');
    line('<span class="t-m">  Latest release:</span> v1.0.0 <span class="t-m">(windows-x64)</span>');
    const dl = line("");
    for (let f = 0; f <= (REDUCED ? 0 : 24); f++) {
      const frac = REDUCED ? 1 : f / 24;
      dl.innerHTML = `  ${bar(frac, 24)}  <span class="t-w">${(9.8 * frac).toFixed(1)} / 9.8 MB</span>`;
      await sleep(55);
    }
    await sleep(300);
    line('  <span class="t-g">✔</span> SHA256 checksum verified');
    metricTo(5, 500).catch(() => {});
    await sleep(700);

    cap(2);
    line('  <span class="t-g">✔</span> Installed to <span class="t-c">%LOCALAPPDATA%\\Programs\\devpit</span>');
    await sleep(400);
    line('  <span class="t-g">✔</span> Added to PATH');
    gap();
    line('  Run <span class="t-w">devpit</span> in any terminal to start.');
    metricTo(9, 500).catch(() => {});
    await sleep(1200);

    cap(3);
    await cmd("devpit --version");
    line('devpit 1.0.0 <span class="t-m">(windows/amd64)</span>');
    await sleep(700);
    await cmd("devpit");
    const A = openApp();
    keys("menu");
    renderMenu(A.screen, 0);
  }

  const SCENES = [sceneFree, scenePort, sceneUpdate, sceneInstall];
  let current = 0;
  async function play(i) {
    runId++;
    const id = runId;
    current = i;
    tabs.forEach((t, j) => { t.classList.toggle("is-active", j === i); t.setAttribute("aria-selected", j === i ? "true" : "false"); });
    term.innerHTML = "";
    pressEl.classList.remove("is-on");
    try {
      await SCENES[i]();
      if (REDUCED) return;
      await sleep(4200);
      if (id === runId) play((i + 1) % SCENES.length);
    } catch (e) { if (e !== CANCEL) console.error(e); }
  }

  if (term) {
    tabs.forEach((t, i) => t.addEventListener("click", () => play(i)));
    $(".term__tabs").addEventListener("keydown", (e) => {
      if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
      const n = (current + (e.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length;
      tabs[n].focus(); play(n);
    });
    pauseBtn.addEventListener("click", () => {
      paused = !paused;
      pauseBtn.setAttribute("aria-pressed", String(paused));
      pauseBtn.setAttribute("aria-label", paused ? "Resume demo" : "Pause demo");
      $("#pause-path").setAttribute("d", paused ? "M8 5.5l11 6.5-11 6.5z" : "M8.5 5.5v13M15.5 5.5v13");
    });
    // pause only after the viewer has seen the demo and scrolled away
    if ("IntersectionObserver" in window) {
      let seen = false;
      new IntersectionObserver(([e]) => { if (e.isIntersecting) seen = true; offscreen = seen && !e.isIntersecting; }, { threshold: 0.05 }).observe(term);
    }
    play(0);
  }

  /* =========================================================
     3. GITHUB STARS — live count on the nav pill once there are 10 or more, "Star" otherwise
     ========================================================= */
  (async () => {
    const el = $('[data-stat="stars"]');
    if (!el) return;
    try {
      const ctrl = new AbortController();
      const to = setTimeout(() => ctrl.abort(), 2500);
      const r = await fetch("https://api.github.com/repos/zubairbinshaukat/devpit", { signal: ctrl.signal, headers: { accept: "application/vnd.github+json" } });
      clearTimeout(to);
      if (!r.ok) return;
      const n = Number((await r.json()).stargazers_count);
      if (Number.isFinite(n) && n >= 10) el.textContent = n >= 1000 ? `${(n / 1000).toFixed(1).replace(/\.0$/, "")}k` : String(n);
    } catch (_) { /* offline or rate-limited: keep "Star" */ }
  })();

  /* =========================================================
     3b. USAGE STATS — the counter stays hidden unless /api/stats
     answers with a real total of more than 100 GB. No invented numbers.
     ========================================================= */
  (async () => {
    const section = $("#stats");
    if (!section) return;
    try {
      const ctrl = new AbortController();
      const to = setTimeout(() => ctrl.abort(), 2500);
      const r = await fetch("/api/stats", { signal: ctrl.signal, headers: { accept: "application/json" } });
      clearTimeout(to);
      if (!r.ok || !(r.headers.get("content-type") || "").includes("json")) return;
      const st = await r.json();
      const total = Number(st.totalGB);
      if (!Number.isFinite(total) || total <= 100) return;
      const nf = new Intl.NumberFormat("en-US");
      const set = (k, v) => $$(`[data-stat="${k}"]`).forEach((n) => { n.textContent = v; });
      set("totalGB", nf.format(Math.round(total)));
      set("weekGB", `${nf.format(Math.round(Number(st.weekGB) || 0))} GB`);
      set("users", nf.format(Math.round(Number(st.users) || 0)));
      set("biggest", `${nf.format(Math.round(Number(st.biggest) || 0))} GB`);
      section.hidden = false;
    } catch (_) { /* no stats endpoint yet: the section stays hidden */ }
  })();

  /* =========================================================
     3b. INSTALL METHOD TABS (PowerShell / Scoop)
     Every group on the page follows the same choice, and the choice is
     remembered per browser as a convenience only.
     ========================================================= */
  (() => {
    const groups = $$("[data-install-group]");
    if (!groups.length) return;
    const KEY = "devpit.install";
    const select = (method, focus) => {
      groups.forEach((g) => {
        g.querySelectorAll("[data-method]").forEach((tab) => {
          const on = tab.dataset.method === method;
          tab.classList.toggle("is-active", on);
          tab.setAttribute("aria-selected", on ? "true" : "false");
          tab.tabIndex = on ? 0 : -1;
          if (on && focus && g.contains(document.activeElement)) tab.focus();
        });
        g.querySelectorAll("[data-panel]").forEach((p) => { p.hidden = p.dataset.panel !== method; });
      });
      try { localStorage.setItem(KEY, method); } catch (_) {}
    };
    groups.forEach((g) => {
      g.querySelectorAll("[data-method]").forEach((tab) => {
        tab.addEventListener("click", () => select(tab.dataset.method, false));
        tab.addEventListener("keydown", (e) => {
          if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
          const tabs = [...g.querySelectorAll("[data-method]")];
          const i = tabs.indexOf(tab);
          const next = tabs[(i + (e.key === "ArrowRight" ? 1 : tabs.length - 1)) % tabs.length];
          e.preventDefault();
          select(next.dataset.method, true);
        });
      });
    });
    let saved = null;
    try { saved = localStorage.getItem(KEY); } catch (_) {}
    if (saved === "scoop") select("scoop", false);
  })();

  /* =========================================================
     4. COPY BUTTONS
     ========================================================= */
  $$("[data-copy]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const ok = () => { btn.classList.add("is-done"); setTimeout(() => btn.classList.remove("is-done"), 1800); };
      const fallback = () => {
        const code = btn.parentElement.querySelector("code");
        if (!code) return;
        const range = document.createRange();
        range.selectNodeContents(code);
        const sel = getSelection(); sel.removeAllRanges(); sel.addRange(range);
      };
      try { navigator.clipboard.writeText(btn.dataset.copy).then(ok, fallback); } catch (_) { fallback(); }
      // a little tribute to the hole
      Space.bump(0.3);
    });
  });
})();
