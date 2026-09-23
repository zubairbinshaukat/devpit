'use strict';

// GET /api/stats — aggregate totals for the landing page counter. Reads
// only, so it prefers the read-only Upstash token when one is configured.
const { hasKvReadEnv, redisPipeline, isoWeekKey } = require('./_lib/kv');

function round1(n) {
  return Math.round(n * 10) / 10;
}

module.exports = async (req, res) => {
  if (req.method !== 'GET') {
    res.setHeader('Allow', 'GET');
    res.status(405).json({ error: 'method not allowed' });
    return;
  }

  if (!hasKvReadEnv()) {
    res.status(503).json({ error: 'storage unavailable' });
    return;
  }

  const token = process.env.KV_REST_API_READ_ONLY_TOKEN || process.env.KV_REST_API_TOKEN;
  const week = isoWeekKey(new Date());

  let result;
  try {
    result = await redisPipeline(
      [
        ['GET', 'stats:bytes'],
        ['GET', `stats:week:${week}`],
        ['PFCOUNT', 'stats:users'],
        ['GET', 'stats:biggest'],
      ],
      { token }
    );
  } catch (_e) {
    res.status(503).json({ error: 'storage unavailable' });
    return;
  }

  const bytes = Number(result && result[0] && result[0].result) || 0;
  const weekBytes = Number(result && result[1] && result[1].result) || 0;
  const users = Number(result && result[2] && result[2].result) || 0;
  const biggest = Number(result && result[3] && result[3].result) || 0;

  // The stats function's own Cache-Control (below) is scoped to this path,
  // and web/vercel.json only sets a blanket no-store on /api/report — so
  // there's no header collision to worry about here.
  res.setHeader('Cache-Control', 'public, s-maxage=300, stale-while-revalidate=600');
  res.status(200).json({
    totalGB: round1(bytes / 1e9),
    weekGB: round1(weekBytes / 1e9),
    users,
    biggest: round1(biggest / 1e9),
  });
};
