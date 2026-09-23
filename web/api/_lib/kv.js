'use strict';

// Shared helpers for the two usage-stats functions (report.js, stats.js).
//
// This file lives under api/_lib/ on purpose: Vercel's zero-config API
// detector excludes any path segment starting with "_" from becoming a
// function (see vercel/vercel, packages/fs-detectors/src/detect-builders.ts:
// `if (fileName.includes('/_')) { return null; }`). So this module is never
// deployed as its own endpoint, only required by the real handlers.

const { createHash } = require('node:crypto');

const KV_TIMEOUT_MS = 2500;

function hasKvEnv() {
  return Boolean(process.env.KV_REST_API_URL && process.env.KV_REST_API_TOKEN);
}

function hasKvReadEnv() {
  return Boolean(
    process.env.KV_REST_API_URL &&
      (process.env.KV_REST_API_READ_ONLY_TOKEN || process.env.KV_REST_API_TOKEN)
  );
}

// POSTs a batch of Redis commands to Upstash's REST pipeline endpoint.
// Body is a JSON array of ["CMD", "arg", ...] arrays; the response is a
// JSON array of { result } (or { error }) objects in the same order.
// https://upstash.com/docs/redis/features/restapi
async function redisPipeline(commands, opts = {}) {
  const url = opts.url || process.env.KV_REST_API_URL;
  const token = opts.token || process.env.KV_REST_API_TOKEN;
  const timeoutMs = opts.timeoutMs || KV_TIMEOUT_MS;
  if (!url || !token) {
    throw new Error('kv_env_missing');
  }

  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    const res = await fetch(`${url}/pipeline`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(commands),
      signal: ctrl.signal,
    });
    if (!res.ok) {
      throw new Error(`kv_http_${res.status}`);
    }
    return res.json();
  } finally {
    clearTimeout(timer);
  }
}

// ISO 8601 week key, e.g. "2026-W01". Uses the standard "nearest Thursday"
// trick so the week number and its year both flip correctly across a
// Dec/Jan boundary (the ISO year of a week is the year of its Thursday).
function isoWeekKey(date) {
  const d = new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate()));
  const dayNum = (d.getUTCDay() + 6) % 7; // Monday = 0 .. Sunday = 6
  d.setUTCDate(d.getUTCDate() - dayNum + 3); // move to this week's Thursday
  const firstThursday = new Date(Date.UTC(d.getUTCFullYear(), 0, 4));
  const firstDayNum = (firstThursday.getUTCDay() + 6) % 7;
  firstThursday.setUTCDate(firstThursday.getUTCDate() - firstDayNum + 3);
  const weekNum = 1 + Math.round((d - firstThursday) / (7 * 24 * 3600 * 1000));
  return `${d.getUTCFullYear()}-W${String(weekNum).padStart(2, '0')}`;
}

function sha256Hex(input) {
  return createHash('sha256').update(String(input)).digest('hex');
}

// First hop of X-Forwarded-For: the client's own address, not any proxy
// further down the chain.
function clientIp(req) {
  const xff = req.headers['x-forwarded-for'];
  if (typeof xff === 'string' && xff.length) {
    return xff.split(',')[0].trim();
  }
  return (req.socket && req.socket.remoteAddress) || '';
}

function isPlainObject(v) {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

const ALLOWED_OS = new Set(['windows', 'linux', 'darwin']);
const RE_INSTALL_ID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const RE_VERSION = /^[0-9A-Za-z.+-]{1,32}$/;
const RE_OS_VERSION = /^[0-9A-Za-z. _-]{0,32}$/;
const RE_TYPE_KEY = /^[a-z0-9._-]{1,40}$/;
const ALLOWED_TOP_KEYS = [
  'v',
  'installId',
  'version',
  'os',
  'osVersion',
  'freedBytes',
  'items',
  'types',
];
const MAX_FREED_BYTES = 2199023255552; // 2 TB
const MAX_ITEMS = 100000;
const MAX_TYPE_KEYS = 32;

// Returns null when `body` is a valid report, otherwise a short reason
// string suitable for a 400 error body. Never touches Redis or the network,
// so it's safe to call before any rate limiting or accounting.
function validateReport(body) {
  if (!isPlainObject(body)) return 'body must be an object';

  const keys = Object.keys(body);
  if (
    keys.length !== ALLOWED_TOP_KEYS.length ||
    !ALLOWED_TOP_KEYS.every((k) => Object.prototype.hasOwnProperty.call(body, k))
  ) {
    return 'unexpected or missing fields';
  }

  if (body.v !== 1) return 'v must be 1';
  if (typeof body.installId !== 'string' || !RE_INSTALL_ID.test(body.installId)) {
    return 'invalid installId';
  }
  if (typeof body.version !== 'string' || !RE_VERSION.test(body.version)) {
    return 'invalid version';
  }
  if (typeof body.os !== 'string' || !ALLOWED_OS.has(body.os)) {
    return 'invalid os';
  }
  if (typeof body.osVersion !== 'string' || !RE_OS_VERSION.test(body.osVersion)) {
    return 'invalid osVersion';
  }
  if (
    !Number.isInteger(body.freedBytes) ||
    body.freedBytes < 0 ||
    body.freedBytes > MAX_FREED_BYTES
  ) {
    return 'invalid freedBytes';
  }
  if (!Number.isInteger(body.items) || body.items < 0 || body.items > MAX_ITEMS) {
    return 'invalid items';
  }
  if (!isPlainObject(body.types)) return 'invalid types';

  const typeKeys = Object.keys(body.types);
  if (typeKeys.length > MAX_TYPE_KEYS) return 'too many types';

  let sum = 0;
  for (const k of typeKeys) {
    if (!RE_TYPE_KEY.test(k)) return 'invalid type key';
    const val = body.types[k];
    if (!Number.isInteger(val) || val < 0 || val > MAX_ITEMS) return 'invalid type value';
    sum += val;
  }
  if (sum > body.items) return 'types sum exceeds items';

  return null;
}

module.exports = {
  hasKvEnv,
  hasKvReadEnv,
  redisPipeline,
  isoWeekKey,
  sha256Hex,
  clientIp,
  validateReport,
  MAX_FREED_BYTES,
  MAX_ITEMS,
};
