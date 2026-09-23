'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const { validateReport, isoWeekKey, sha256Hex } = require('./kv');

const CANONICAL = {
  v: 1,
  installId: 'a1b2c3d4-e5f6-4789-8abc-def012345678',
  version: '0.1.0',
  os: 'windows',
  osVersion: '10.0.26200',
  freedBytes: 123456,
  items: 5,
  types: { node_modules: 3, 'npm-cache': 2 },
};

function withOverride(overrides) {
  return { ...CANONICAL, ...overrides };
}

test('validateReport accepts the canonical body', () => {
  assert.equal(validateReport(CANONICAL), null);
});

test('validateReport accepts an empty types object', () => {
  assert.equal(validateReport(withOverride({ items: 0, freedBytes: 0, types: {} })), null);
});

test('validateReport rejects a non-object body', () => {
  assert.ok(validateReport(null));
  assert.ok(validateReport('nope'));
  assert.ok(validateReport([1, 2, 3]));
});

test('validateReport rejects an unexpected top-level field', () => {
  assert.ok(validateReport({ ...CANONICAL, extra: 'field' }));
});

test('validateReport rejects a missing top-level field', () => {
  const { types, ...rest } = CANONICAL;
  assert.ok(validateReport(rest));
});

test('validateReport rejects v != 1', () => {
  assert.ok(validateReport(withOverride({ v: 2 })));
  assert.ok(validateReport(withOverride({ v: '1' })));
});

test('validateReport rejects a malformed installId', () => {
  assert.ok(validateReport(withOverride({ installId: 'not-a-uuid' })));
  // Wrong UUID version nibble (must be "4").
  assert.ok(
    validateReport(withOverride({ installId: 'a1b2c3d4-e5f6-1789-8abc-def012345678' }))
  );
});

test('validateReport rejects a malformed version string', () => {
  assert.ok(validateReport(withOverride({ version: 'v/1.0' })));
  assert.ok(validateReport(withOverride({ version: 'x'.repeat(33) })));
});

test('validateReport rejects an unknown os', () => {
  assert.ok(validateReport(withOverride({ os: 'plan9' })));
});

test('validateReport rejects a malformed osVersion', () => {
  assert.ok(validateReport(withOverride({ osVersion: 'bad/version!' })));
  assert.ok(validateReport(withOverride({ osVersion: 'x'.repeat(33) })));
});

test('validateReport rejects freedBytes out of range or non-integer', () => {
  assert.ok(validateReport(withOverride({ freedBytes: -1 })));
  assert.ok(validateReport(withOverride({ freedBytes: 2199023255553 })));
  assert.ok(validateReport(withOverride({ freedBytes: 1.5 })));
});

test('validateReport rejects items out of range or non-integer', () => {
  assert.ok(validateReport(withOverride({ items: -1 })));
  assert.ok(validateReport(withOverride({ items: 100001 })));
  assert.ok(validateReport(withOverride({ items: 2.5 })));
});

test('validateReport rejects too many type keys', () => {
  const types = {};
  for (let i = 0; i < 33; i++) types[`t${i}`] = 0;
  assert.ok(validateReport(withOverride({ items: 0, freedBytes: 0, types })));
});

test('validateReport rejects a malformed type key', () => {
  assert.ok(validateReport(withOverride({ types: { 'Bad Key!': 1 } })));
});

test('validateReport rejects a non-integer or out-of-range type value', () => {
  assert.ok(validateReport(withOverride({ types: { 'node_modules': 1.5 } })));
  assert.ok(validateReport(withOverride({ types: { 'node_modules': 100001 } })));
});

test('validateReport rejects a types sum greater than items', () => {
  assert.ok(validateReport(withOverride({ items: 4, types: { node_modules: 3, npm_cache: 2 } })));
});

test('isoWeekKey formats a plain mid-year date', () => {
  assert.equal(isoWeekKey(new Date(Date.UTC(2026, 5, 15))), '2026-W25');
});

test('isoWeekKey handles the Dec 31 -> Jan 1 boundary (year rolls forward)', () => {
  // 2025-12-31 is a Wednesday in ISO week 1 of 2026.
  assert.equal(isoWeekKey(new Date(Date.UTC(2025, 11, 31))), '2026-W01');
});

test('isoWeekKey handles a Jan 1 that still belongs to the previous ISO year', () => {
  // 2027-01-01 is a Friday, which falls in week 53 of ISO year 2026.
  assert.equal(isoWeekKey(new Date(Date.UTC(2027, 0, 1))), '2026-W53');
});

// Sets env vars for the duration of a test, returning the previous values so
// the caller can restore them in t.after. `undefined` deletes a key.
function setEnv(vars) {
  const prev = {};
  for (const [k, v] of Object.entries(vars)) {
    prev[k] = Object.prototype.hasOwnProperty.call(process.env, k) ? process.env[k] : undefined;
    if (v === undefined) delete process.env[k];
    else process.env[k] = v;
  }
  return prev;
}

function restoreEnv(prev) {
  for (const [k, v] of Object.entries(prev)) {
    if (v === undefined) delete process.env[k];
    else process.env[k] = v;
  }
}

// Both handlers read process.env at call time, not at require time, but the
// require cache still needs clearing so each test gets a clean module.
function freshHandler(path) {
  delete require.cache[require.resolve(path)];
  return require(path);
}

test('the report handler never calls Redis for an invalid body', async (t) => {
  let fetchCalled = false;
  const originalFetch = global.fetch;
  global.fetch = () => {
    fetchCalled = true;
    return Promise.reject(new Error('fetch should not be called'));
  };
  t.after(() => {
    global.fetch = originalFetch;
  });

  const prevEnv = setEnv({ KV_REST_API_URL: 'https://example.invalid', KV_REST_API_TOKEN: 'test-token' });
  t.after(() => restoreEnv(prevEnv));

  const handler = freshHandler('../report.js');

  const req = {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: { ...CANONICAL, v: 2 }, // invalid: v must be 1
  };
  const res = makeRes();

  await handler(req, res);

  assert.equal(fetchCalled, false);
  assert.equal(res.statusCode, 400);
});

test('the report handler accepts the canonical body in exactly two Redis round trips', async (t) => {
  const calls = [];
  const originalFetch = global.fetch;
  global.fetch = (url, init) => {
    const commands = JSON.parse(init.body);
    calls.push(commands);
    // "stats:biggest" reads back larger than this report's freedBytes so
    // the write pipeline doesn't need to include a SET.
    const results = commands.map((cmd) =>
      cmd[0] === 'GET' && cmd[1] === 'stats:biggest'
        ? { result: '999999999999' }
        : { result: 1 }
    );
    return Promise.resolve({
      ok: true,
      json: () => Promise.resolve(results),
    });
  };
  t.after(() => {
    global.fetch = originalFetch;
  });

  const prevEnv = setEnv({ KV_REST_API_URL: 'https://example.invalid', KV_REST_API_TOKEN: 'test-token' });
  t.after(() => restoreEnv(prevEnv));

  const handler = freshHandler('../report.js');

  const req = {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-forwarded-for': '203.0.113.5' },
    body: CANONICAL,
  };
  const res = makeRes();

  await handler(req, res);

  assert.equal(res.statusCode, 204);
  // Round trip 1: rate-limit INCR/EXPIRE + GET stats:biggest.
  // Round trip 2: the write pipeline. Never a third call.
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[0][4], ['GET', 'stats:biggest']);
});

test('the report handler returns 429 and stops after one call when the IP limit is exceeded', async (t) => {
  const calls = [];
  const originalFetch = global.fetch;
  global.fetch = (url, init) => {
    calls.push(JSON.parse(init.body));
    return Promise.resolve({
      ok: true,
      json: () =>
        Promise.resolve([
          { result: 61 }, // INCR rl:ip:... -> over the 60/hour limit
          { result: 0 }, // EXPIRE ... NX
          { result: 1 }, // INCR rl:id:...
          { result: 0 }, // EXPIRE ... NX
          { result: '0' }, // GET stats:biggest
        ]),
    });
  };
  t.after(() => {
    global.fetch = originalFetch;
  });

  const prevEnv = setEnv({ KV_REST_API_URL: 'https://example.invalid', KV_REST_API_TOKEN: 'test-token' });
  t.after(() => restoreEnv(prevEnv));

  const handler = freshHandler('../report.js');

  const req = {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-forwarded-for': '203.0.113.9' },
    body: CANONICAL,
  };
  const res = makeRes();

  await handler(req, res);

  assert.equal(res.statusCode, 429);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0][0], ['INCR', `rl:ip:${sha256Hex('203.0.113.9')}`]);
});

test('the report handler returns 429 and stops after one call when the install-id limit is exceeded', async (t) => {
  const calls = [];
  const originalFetch = global.fetch;
  global.fetch = (url, init) => {
    calls.push(JSON.parse(init.body));
    return Promise.resolve({
      ok: true,
      json: () =>
        Promise.resolve([
          { result: 1 }, // INCR rl:ip:...
          { result: 0 }, // EXPIRE ... NX
          { result: 51 }, // INCR rl:id:... -> over the 50/day limit
          { result: 0 }, // EXPIRE ... NX
          { result: '0' }, // GET stats:biggest
        ]),
    });
  };
  t.after(() => {
    global.fetch = originalFetch;
  });

  const prevEnv = setEnv({ KV_REST_API_URL: 'https://example.invalid', KV_REST_API_TOKEN: 'test-token' });
  t.after(() => restoreEnv(prevEnv));

  const handler = freshHandler('../report.js');

  const req = {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-forwarded-for': '203.0.113.12' },
    body: CANONICAL,
  };
  const res = makeRes();

  await handler(req, res);

  assert.equal(res.statusCode, 429);
  assert.equal(calls.length, 1);
});

test('the report handler returns 503 when Upstash is unreachable', async (t) => {
  const originalFetch = global.fetch;
  global.fetch = () => Promise.reject(new Error('network down'));
  t.after(() => {
    global.fetch = originalFetch;
  });

  const prevEnv = setEnv({ KV_REST_API_URL: 'https://example.invalid', KV_REST_API_TOKEN: 'test-token' });
  t.after(() => restoreEnv(prevEnv));

  const handler = freshHandler('../report.js');

  const req = {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-forwarded-for': '203.0.113.13' },
    body: CANONICAL,
  };
  const res = makeRes();

  await handler(req, res);

  assert.equal(res.statusCode, 503);
});

test('a report bigger than the current stats:biggest SETs it in the write pipeline', async (t) => {
  const calls = [];
  const originalFetch = global.fetch;
  global.fetch = (url, init) => {
    const commands = JSON.parse(init.body);
    calls.push(commands);
    const results = commands.map((cmd) =>
      cmd[0] === 'GET' && cmd[1] === 'stats:biggest' ? { result: '1000' } : { result: 1 }
    );
    return Promise.resolve({ ok: true, json: () => Promise.resolve(results) });
  };
  t.after(() => {
    global.fetch = originalFetch;
  });

  const prevEnv = setEnv({ KV_REST_API_URL: 'https://example.invalid', KV_REST_API_TOKEN: 'test-token' });
  t.after(() => restoreEnv(prevEnv));

  const handler = freshHandler('../report.js');

  const req = {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-forwarded-for': '203.0.113.10' },
    body: CANONICAL, // freedBytes: 123456, well above the stubbed 1000
  };
  const res = makeRes();

  await handler(req, res);

  assert.equal(res.statusCode, 204);
  assert.equal(calls.length, 2);
  const setCmd = calls[1].find((c) => c[0] === 'SET' && c[1] === 'stats:biggest');
  assert.ok(setCmd, 'expected a SET stats:biggest command in the write pipeline');
  assert.equal(setCmd[2], '123456');
});

test('a report not bigger than the current stats:biggest leaves it alone', async (t) => {
  const calls = [];
  const originalFetch = global.fetch;
  global.fetch = (url, init) => {
    const commands = JSON.parse(init.body);
    calls.push(commands);
    const results = commands.map((cmd) =>
      cmd[0] === 'GET' && cmd[1] === 'stats:biggest'
        ? { result: '999999999999' }
        : { result: 1 }
    );
    return Promise.resolve({ ok: true, json: () => Promise.resolve(results) });
  };
  t.after(() => {
    global.fetch = originalFetch;
  });

  const prevEnv = setEnv({ KV_REST_API_URL: 'https://example.invalid', KV_REST_API_TOKEN: 'test-token' });
  t.after(() => restoreEnv(prevEnv));

  const handler = freshHandler('../report.js');

  const req = {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'x-forwarded-for': '203.0.113.11' },
    body: CANONICAL,
  };
  const res = makeRes();

  await handler(req, res);

  assert.equal(res.statusCode, 204);
  assert.equal(calls.length, 2);
  assert.equal(
    calls[1].some((c) => c[0] === 'SET' && c[1] === 'stats:biggest'),
    false
  );
});

test('the stats handler returns the expected JSON shape and cache header', async (t) => {
  const originalFetch = global.fetch;
  global.fetch = (url, init) => {
    const commands = JSON.parse(init.body);
    const results = commands.map((cmd) => {
      if (cmd[1] === 'stats:bytes') return { result: '5000000000' }; // 5 GB
      if (cmd[0] === 'PFCOUNT') return { result: 42 };
      if (cmd[1] === 'stats:biggest') return { result: '2500000000' }; // 2.5 GB
      return { result: '1000000000' }; // stats:week:<...> -> 1 GB
    });
    return Promise.resolve({ ok: true, json: () => Promise.resolve(results) });
  };
  t.after(() => {
    global.fetch = originalFetch;
  });

  const prevEnv = setEnv({
    KV_REST_API_URL: 'https://example.invalid',
    KV_REST_API_TOKEN: 'test-token',
    KV_REST_API_READ_ONLY_TOKEN: undefined,
  });
  t.after(() => restoreEnv(prevEnv));

  const handler = freshHandler('../stats.js');
  const req = { method: 'GET', headers: {} };
  const res = makeRes();

  await handler(req, res);

  assert.equal(res.statusCode, 200);
  assert.deepEqual(res.body, { totalGB: 5, weekGB: 1, users: 42, biggest: 2.5 });
  assert.equal(res.headers['Cache-Control'], 'public, s-maxage=300, stale-while-revalidate=600');
});

test('the stats handler returns 503 when the KV env is missing', async (t) => {
  const prevEnv = setEnv({
    KV_REST_API_URL: undefined,
    KV_REST_API_TOKEN: undefined,
    KV_REST_API_READ_ONLY_TOKEN: undefined,
  });
  t.after(() => restoreEnv(prevEnv));

  const handler = freshHandler('../stats.js');
  const req = { method: 'GET', headers: {} };
  const res = makeRes();

  await handler(req, res);

  assert.equal(res.statusCode, 503);
});

function makeRes() {
  return {
    statusCode: undefined,
    body: undefined,
    headers: {},
    setHeader(key, value) {
      this.headers[key] = value;
    },
    status(code) {
      this.statusCode = code;
      return this;
    },
    json(payload) {
      this.body = payload;
      return this;
    },
    end(payload) {
      this.body = payload;
      return this;
    },
  };
}
