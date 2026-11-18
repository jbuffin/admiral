import { beforeAll, expect, test, vi } from 'vitest';
import { loaded, watchFleets } from './fleets';
import { go } from './router';

beforeAll(async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ fleets: [{ key: 'zulu', name: 'Zulu', docks: [] }] }))));
  watchFleets();
  await vi.waitFor(() => expect(loaded.value).toBe(true));
});

test('a link to an unknown fleet falls back to the first fleet and keeps its draft', () => {
  go('/app/routines/new', '?fleet=nope&draft=d%2F1&other=1');
  expect(location.pathname + location.search).toBe('/app/routines/new?fleet=zulu&draft=d%2F1');
});

test('a link to an unknown fleet without a draft drops its other parameters', () => {
  go('/app/routines/new', '?fleet=nope&other=1');
  expect(location.pathname + location.search).toBe('/app/routines/new?fleet=zulu');
});
