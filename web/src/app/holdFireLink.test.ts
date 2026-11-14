import { afterEach, describe, expect, it, vi } from 'vitest';
// fleets.ts pulls in the generated shell tokens, which a unit run has not built.
vi.mock('./fleets', () => ({ activeFleet: { value: undefined, peek: () => undefined } }));

import { stopResult, stopWorkerShips } from './holdFireLink';

const answer = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

afterEach(() => vi.unstubAllGlobals());

describe('stopWorkerShips', () => {
  it('posts to the fleet route for this fleet, then reads Hold fire back', async () => {
    const fetchMock = vi.fn(async () => answer(200, { stopped: ['a'], failed: [] }));
    vi.stubGlobal('fetch', fetchMock);
    expect(await stopWorkerShips('work', 'fleet')).toBe('');
    const calls = fetchMock.mock.calls as unknown as [string, RequestInit][];
    expect(calls[0][0]).toMatch(/\/fleets\/work\/worker-ships\/stop$/);
    expect(calls[0][1].method).toBe('POST');
    expect(calls.some(([url, init]) => /\/fleets\/work\/hold-fire$/.test(url) && (init?.method ?? 'GET') === 'GET')).toBe(true);
  });

  it('posts to the all-fleets route for the global button', async () => {
    const fetchMock = vi.fn(async () => answer(200, { stopped: [], failed: [] }));
    vi.stubGlobal('fetch', fetchMock);
    await stopWorkerShips('work', 'all');
    const calls = fetchMock.mock.calls as unknown as [string, RequestInit][];
    expect(calls[0][0]).toMatch(/\/worker-ships\/stop$/);
    expect(calls[0][0]).not.toMatch(/\/fleets\//);
  });

  it('answers the refusal text, and names a ship that would not stop', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => answer(403, { error: 'an actor may not stop ships' })));
    expect(await stopWorkerShips('work', 'fleet')).toBe('an actor may not stop ships');
    vi.stubGlobal('fetch', vi.fn(async () => answer(200, { stopped: [], failed: ['calm-otter'] })));
    expect(await stopWorkerShips('work', 'fleet')).toContain('calm-otter');
  });
});

describe('the result of Stop all worker-launched', () => {
  it('says how many ships it stopped, in the singular for one', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => answer(200, { stopped: ['a'], failed: [] })));
    await stopWorkerShips('work', 'fleet');
    expect(stopResult.value).toBe('Stopped 1 ship');
    vi.stubGlobal('fetch', vi.fn(async () => answer(200, { stopped: ['a', 'b'], failed: [] })));
    await stopWorkerShips('work', 'fleet');
    expect(stopResult.value).toBe('Stopped 2 ships');
  });

  it('lists the ships it could not stop beside the count of those it did', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => answer(200, { stopped: ['a'], failed: ['b', 'c'] })));
    expect(await stopWorkerShips('work', 'fleet')).toBe('Could not stop b, c.');
    expect(stopResult.value).toBe('Stopped 1 ship');
  });
});
