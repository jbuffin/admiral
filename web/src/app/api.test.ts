import { afterEach, describe, expect, it, vi } from 'vitest';
import { call, probeShips, refreshSource } from './api';

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

  it('sends the AbortSignal it is given to fetch', async () => {
    let seen: AbortSignal | null | undefined;
    vi.stubGlobal('fetch', async (_url: string, init: RequestInit) => {
      seen = init.signal;
      return new Response('{}', { status: 200 });
    });
    const ctl = new AbortController();
    await call('POST', '/x', {}, undefined, ctl.signal);
    expect(seen).toBe(ctl.signal);
  });

  it('answers aborted, not an error to show, when the signal cancels the request', async () => {
    vi.stubGlobal('fetch', async () => {
      throw new DOMException('The operation was aborted.', 'AbortError');
    });
    const r = await call('POST', '/x', {}, undefined, new AbortController().signal);
    expect(r).toMatchObject({ ok: false, aborted: true });
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
