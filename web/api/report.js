'use strict';

// POST /api/report — opt-in usage report from the Devpit CLI. See
// PRIVACY.md for what this accepts and why. Never logs the body or the
// caller's IP; never echoes input back in a response.
const {
  hasKvEnv,
  redisPipeline,
  isoWeekKey,
  sha256Hex,
  clientIp,
  validateReport,
} = require('./_lib/kv');

const MAX_BODY_BYTES = 4096;

// TTL-only rate limits. Both keys carry an EXPIRE and nothing survives past
// it — there is no persisted request log.
const IP_LIMIT = 60;
const IP_TTL_SECONDS = 3600; // 1 hour
const ID_LIMIT = 50;
const ID_TTL_SECONDS = 86400; // 1 day
const WEEK_TTL_SECONDS = 1209600; // 14 days, comfortably longer than one ISO week

module.exports = async (req, res) => {
  if (req.method !== 'POST') {
    res.setHeader('Allow', 'POST');
    res.status(405).json({ error: 'method not allowed' });
    return;
  }

  const contentType = req.headers['content-type'] || '';
  if (!contentType.includes('application/json')) {
    res.status(400).json({ error: 'expected application/json' });
    return;
  }

  const contentLength = Number(req.headers['content-length'] || 0);
  if (contentLength > MAX_BODY_BYTES) {
    res.status(400).json({ error: 'body too large' });
    return;
  }

  // Vercel parses application/json lazily via a getter; malformed JSON
  // throws on access rather than on the request itself.
  let body;
  try {
    body = req.body;
  } catch (_e) {
    res.status(400).json({ error: 'malformed json' });
    return;
  }
  if (body === undefined || body === null) {
    res.status(400).json({ error: 'empty body' });
    return;
  }

  // Belt-and-suspenders size check for cases without a trustworthy
  // Content-Length (e.g. chunked transfer-encoding).
  let serialized;
  try {
    serialized = JSON.stringify(body);
  } catch (_e) {
    res.status(400).json({ error: 'malformed json' });
    return;
  }
  if (!serialized || serialized.length > MAX_BODY_BYTES) {
    res.status(400).json({ error: 'body too large' });
    return;
  }

  const validationError = validateReport(body);
  if (validationError) {
    res.status(400).json({ error: validationError });
    return;
  }

  // Nothing to count (empty cleanup): accept without touching Redis at all.
  if (body.freedBytes === 0 && body.items === 0) {
    res.status(204).end();
    return;
  }

  if (!hasKvEnv()) {
    res.status(503).json({ error: 'storage unavailable' });
    return;
  }

  // The hashed-IP key below is the ONLY use this server makes of the
  // caller's IP address: it bounds how many reports one connection can
  // send per hour. It carries a 1-hour EXPIRE, is never logged, and is
  // never joined with anything else.
  const ipHash = sha256Hex(clientIp(req));
  const ipKey = `rl:ip:${ipHash}`;
  const idKey = `rl:id:${body.installId}`;

  // Round trip 1 of (at most) 2: the rate-limit INCR/EXPIRE pair for both
  // keys, plus a GET of stats:biggest riding along for free. Reading
  // stats:biggest here — rather than in the write pipeline — means a
  // rate-limited request costs exactly one round trip, and an accepted one
  // costs exactly two, never three.
  let rl;
  try {
    rl = await redisPipeline([
      ['INCR', ipKey],
      ['EXPIRE', ipKey, String(IP_TTL_SECONDS), 'NX'],
      ['INCR', idKey],
      ['EXPIRE', idKey, String(ID_TTL_SECONDS), 'NX'],
      ['GET', 'stats:biggest'],
    ]);
  } catch (_e) {
    res.status(503).json({ error: 'storage unavailable' });
    return;
  }

  const ipCount = Number(rl && rl[0] && rl[0].result);
  const idCount = Number(rl && rl[2] && rl[2].result);
  if (ipCount > IP_LIMIT || idCount > ID_LIMIT) {
    res.status(429).json({ error: 'rate limited' });
    return;
  }

  const currentBiggest = Number((rl && rl[4] && rl[4].result) || 0);

  const week = isoWeekKey(new Date());
  const weekKey = `stats:week:${week}`;

  // Round trip 2: every write for this report in one pipeline, including a
  // conditional SET of stats:biggest decided from the value already read
  // above. Two concurrent large reports can both see the same
  // currentBiggest and both include a SET, so the last one to land wins —
  // a benign race on a display-only figure, not worth a transaction.
  const commands = [
    ['INCRBY', 'stats:bytes', String(body.freedBytes)],
    ['INCRBY', 'stats:items', String(body.items)],
    ['PFADD', 'stats:users', body.installId],
    ['INCRBY', weekKey, String(body.freedBytes)],
    ['EXPIRE', weekKey, String(WEEK_TTL_SECONDS), 'NX'],
    ['INCR', 'stats:reports'],
  ];
  for (const [type, count] of Object.entries(body.types)) {
    if (count > 0) commands.push(['HINCRBY', 'stats:types', type, String(count)]);
  }
  if (body.freedBytes > currentBiggest) {
    commands.push(['SET', 'stats:biggest', String(body.freedBytes)]);
  }

  try {
    await redisPipeline(commands);
  } catch (_e) {
    res.status(503).json({ error: 'storage unavailable' });
    return;
  }

  res.status(204).end();
};
