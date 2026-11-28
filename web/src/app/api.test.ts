import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { call, fetchFleets, fetchStackShape, linkStack, mergeShip, probeShips, refreshSource, saveDefaultFleet } from './api';

const answer = (status: number, body: unknown) => vi.stubGlobal('fetch', async () => new Response(typeof body === 'string' ? body : JSON.stringify(body), { status }));

afterEach(() => vi.unstubAllGlobals());

describe('call on a refusal', () => {
  it('carries the note id a 409 names, with its error', async () => {
    answer(409, { error: 'scope full', note: 'n2' });
    expect(await call('POST', '/inbox/_admiral/c-1/crew-notes', {})).toEqual({ ok: false, status: 409, error: 'scope full', note: 'n2' });
  });

  it('leaves note off a refusal that names none, and keeps field for the callers that read it', async () => {
    answer(409, { error: 'fleet is no longer configured' });
    expect(await call('POST', '/x')).toEqual({ ok: false, status: 409, error: 'fleet is no longer configured' });
    answer(400, { error: 'bad role', field: 'role' });
    expect(await call('PUT', '/x')).toEqual({ ok: false, status: 400, error: 'bad role', field: 'role' });
  });

  it('carries the code a 403 names, so a caller can tell no access from sharing off', async () => {
    answer(403, { error: 'You need access to a/b. Ask its owner to add you.', code: 'no_access' });
    expect(await call('POST', '/x')).toEqual({ ok: false, status: 403, error: 'You need access to a/b. Ask its owner to add you.', code: 'no_access' });
    answer(403, { error: 'Crew sharing is off for this fleet.' });
    expect(await call('POST', '/x')).toEqual({ ok: false, status: 403, error: 'Crew sharing is off for this fleet.' });
    answer(403, { error: 'nope', code: 4 });
    expect(await call('POST', '/x')).toEqual({ ok: false, status: 403, error: 'nope' });
  });

  it('ignores a note that is not a string', async () => {
    answer(409, { error: 'nope', note: 4 });
    expect(await call('POST', '/x')).toEqual({ ok: false, status: 409, error: 'nope' });
  });
});

describe('call on a draft refusal', () => {
  it('carries the failure kind a 502 names and the reason a 409 names', async () => {
    answer(502, { error: 'the judge timed out', kind: 'timeout' });
    expect(await call('POST', '/x')).toEqual({ ok: false, status: 502, error: 'the judge timed out', kind: 'timeout' });
    answer(409, { error: 'no judge harness', reason: 'claude has not passed the no-network check' });
    expect(await call('POST', '/x')).toEqual({ ok: false, status: 409, error: 'no judge harness', reason: 'claude has not passed the no-network check' });
  });
  it('carries the parent a 502 made and the issues it linked, and ignores them when they are the wrong type', async () => {
    answer(502, { error: 'link failed', parent: 'https://github.com/acme/widgets/issues/50', linked: ['https://github.com/acme/widgets/issues/41'] });
    expect(await call('POST', '/campaigns/create', {})).toEqual({
      ok: false,
      status: 502,
      error: 'link failed',
      parent: 'https://github.com/acme/widgets/issues/50',
      linked: ['https://github.com/acme/widgets/issues/41'],
    });
    answer(502, { error: 'no parent', parent: 4, linked: 'x' });
    expect(await call('POST', '/campaigns/create', {})).toEqual({ ok: false, status: 502, error: 'no parent' });
  });
  it('stops the request when the caller aborts it, and answers aborted', async () => {
    hangUntilAborted();
    const ctl = new AbortController();
    const pending = call('POST', '/x', {}, undefined, { signal: ctl.signal });
    ctl.abort();
    expect(await pending).toEqual({ ok: false, error: 'The request was stopped.', aborted: true });
  });
});

const hangUntilAborted = () =>
  vi.stubGlobal(
    'fetch',
    (_url: string, init: RequestInit) =>
      new Promise<Response>((_, reject) => {
        const stop = () => reject(new DOMException('The operation was aborted.', 'AbortError'));
        if (init.signal?.aborted) stop();
        init.signal?.addEventListener('abort', stop);
      }),
  );

const timedOut = (limit: string) => ({ ok: false, error: `Admiral didn't answer in ${limit}. It may still finish, so check before trying again.` });

describe('call timeout', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('fails with the timeout message once the default 30 s passes, and not before', async () => {
    hangUntilAborted();
    let settled = false;
    const pending = call('GET', '/x').then((r) => {
      settled = true;
      return r;
    });
    await vi.advanceTimersByTimeAsync(29_999);
    expect(settled).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(await pending).toEqual(timedOut('30 s'));
  });

  it('does not call a timeout an abort', async () => {
    hangUntilAborted();
    const pending = call('GET', '/x');
    await vi.advanceTimersByTimeAsync(30_000);
    expect(await pending).not.toHaveProperty('aborted');
  });

  it("lets a call's own limit replace the default and names that limit", async () => {
    hangUntilAborted();
    let settled = false;
    const pending = call('POST', '/x', {}, undefined, { timeoutMs: 180_000 }).then((r) => {
      settled = true;
      return r;
    });
    await vi.advanceTimersByTimeAsync(30_000);
    expect(settled).toBe(false);
    await vi.advanceTimersByTimeAsync(150_000);
    expect(await pending).toEqual(timedOut('3 min'));
  });

  it('writes whole minutes as N min and anything else as N s', async () => {
    for (const [ms, label] of [[600_000, '10 min'], [60_000, '1 min'], [45_000, '45 s'], [90_000, '90 s']] as const) {
      hangUntilAborted();
      const pending = call('POST', '/x', {}, undefined, { timeoutMs: ms });
      await vi.advanceTimersByTimeAsync(ms);
      expect(await pending, label).toEqual(timedOut(label));
    }
  });

  it("keeps a caller's abort an abort when it comes before the limit", async () => {
    hangUntilAborted();
    const ctl = new AbortController();
    const pending = call('POST', '/x', {}, undefined, { signal: ctl.signal });
    await vi.advanceTimersByTimeAsync(10_000);
    ctl.abort();
    expect(await pending).toEqual({ ok: false, error: 'The request was stopped.', aborted: true });
  });

  it('leaves no timer behind once the request answers', async () => {
    answer(200, {});
    expect(await call('GET', '/x')).toEqual({ ok: true, data: {} });
    expect(vi.getTimerCount()).toBe(0);
  });

  it('gives the long routes their own limit and leaves the quick ones at the default', async () => {
    vi.stubGlobal('document', { querySelector: () => null });
    const limits: [string, () => Promise<unknown>, string][] = [
      ['mergeShip', () => mergeShip('s1'), '3 min'],
      ['linkStack', () => linkStack('s1'), '3 min'],
      ['fetchStackShape', () => fetchStackShape('s1'), '30 s'],
    ];
    for (const [name, run, label] of limits) {
      hangUntilAborted();
      const pending = run();
      await vi.advanceTimersByTimeAsync(label === '30 s' ? 30_000 : 180_000);
      expect(await pending, name).toMatchObject(timedOut(label));
    }
  });
});

const answerWith = (impl: () => Promise<Response>) => vi.stubGlobal('fetch', impl);

describe('probeShips', () => {
  it('tells a server that answers from one that cannot be reached, and from a signed-out device', async () => {
    answerWith(async () => new Response('{}', { status: 200 }));
    expect(await probeShips()).toBe('ok');

    answerWith(async () => new Response('', { status: 401 }));
    expect(await probeShips()).toBe('signed-out');

    answerWith(() => Promise.reject(new TypeError('Load failed')));
    expect(await probeShips()).toBe('unreachable');

    for (const status of [502, 503, 504]) {
      answerWith(async () => new Response('', { status }));
      expect(await probeShips(), String(status)).toBe('unreachable');
    }

    answerWith(async () => new Response('', { status: 500 }));
    expect(await probeShips()).toBe('down');
  });
});

describe('refreshSource', () => {
  it('posts to the source key, URL-encoded, and reports success', async () => {
    const seen: { url: string; method?: string }[] = [];
    vi.stubGlobal('fetch', async (url: string, init: RequestInit) => {
      seen.push({ url, method: init.method });
      return new Response(JSON.stringify({ refreshing: 'a b' }), { status: 200 });
    });
    const r = await refreshSource('a b');
    expect(r.ok).toBe(true);
    expect(seen).toEqual([{ url: '/api/v1/sources/a%20b/refresh', method: 'POST' }]);
  });

  it('answers the refusal instead of throwing on a 500, and on an unreachable server', async () => {
    answer(500, { error: 'boom' });
    expect(await refreshSource('gh')).toMatchObject({ ok: false, status: 500, error: 'boom' });
    vi.stubGlobal('fetch', async () => {
      throw new TypeError('Failed to fetch');
    });
    expect(await refreshSource('gh')).toMatchObject({ ok: false });
  });
});

describe('fetchFleets on a dock', () => {
  it('gives a dock the server sent no list for an empty one, as it does warnings', async () => {
    answer(200, { fleets: [{ key: 'zulu', docks: [{ harness: 'claude' }] }] });
    const dock = (await fetchFleets())[0]!.docks[0]!;
    expect(dock.models).toEqual([]);
    expect(dock.warnings).toEqual([]);
  });
});

describe('saveDefaultFleet', () => {
  it('PUTs the key and answers the fleets with the new default, so the caller loses neither', async () => {
    const seen: { url: string; method?: string; body?: unknown }[] = [];
    vi.stubGlobal('fetch', async (url: string, init: RequestInit) => {
      seen.push({ url, method: init.method, body: JSON.parse(String(init.body)) });
      return new Response(JSON.stringify({ fleets: [{ key: 'bravo', docks: [{ harness: 'claude' }] }], default_fleet: 'bravo', default_fleet_fallback: false }), { status: 200 });
    });
    const r = await saveDefaultFleet('bravo');
    expect(seen).toEqual([{ url: '/api/v1/default-fleet', method: 'PUT', body: { fleet: 'bravo' } }]);
    if (!r.ok) throw new Error(r.error);
    expect(r.data).toMatchObject({ default_fleet: 'bravo', default_fleet_fallback: false });
    expect(r.data.fleets[0]!.docks[0]!.models).toEqual([]);
  });

  it('answers the server message and status on a 422', async () => {
    answer(422, { error: 'no such fleet' });
    expect(await saveDefaultFleet('gone')).toMatchObject({ ok: false, status: 422, error: 'no such fleet' });
  });
});
