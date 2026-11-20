import { beforeAll, expect, test, vi } from 'vitest';
import { fleets, loaded, loadFleets, watchFleets } from './fleets';
import { go } from './router';
import { watchShips } from './shipsLink';

const dock = (models: string[]) => ({ harness: 'claude', executable: 'claude', executable_found: true, warnings: [], models, models_source: 'listed' });
// What the server holds; GET /api/config and the socket's config message both carry it.
let served = { fleets: [{ key: 'zulu', name: 'Zulu', docks: [dock(['sonnet', 'opus'])] }] };
interface Socket {
  onopen?: () => void;
  onmessage?: (e: { data: string }) => void;
}
const sockets: Socket[] = [];

beforeAll(async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(served))));
  vi.stubGlobal('WebSocket', class implements Socket { onopen?: () => void; onmessage?: (e: { data: string }) => void; constructor() { sockets.push(this); } close() {} send() {} });
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

test("a config message on the shell's socket reloads the fleets, so a dock's new model list reaches every picker", async () => {
  const stop = watchShips();
  const socket = sockets.at(-1)!;
  socket.onopen?.();
  await vi.waitFor(() => expect(fleets.value[0]!.docks[0]!.models).toEqual(['sonnet', 'opus']));

  served = { fleets: [{ key: 'zulu', name: 'Zulu', docks: [dock(['sonnet', 'opus', 'fable'])] }] };
  socket.onmessage?.({ data: JSON.stringify({ type: 'config', ...served }) });

  await vi.waitFor(() => expect(fleets.value[0]!.docks[0]!.models).toEqual(['sonnet', 'opus', 'fable']));
  stop();
});

test('a slower older config response never replaces a newer one', async () => {
  const answers: ((r: Response) => void)[] = [];
  const answer = (models: string[]) => new Response(JSON.stringify({ fleets: [{ key: 'zulu', name: 'Zulu', docks: [dock(models)] }] }));
  const real = fetch;
  vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => answers.push(resolve))));
  const older = loadFleets();
  const newer = loadFleets();
  answers[1]!(answer(['new']));
  await newer;
  answers[0]!(answer(['old']));
  await older;
  const shown = fleets.value[0]!.docks[0]!.models;
  vi.stubGlobal('fetch', real);
  await loadFleets();
  expect(shown).toEqual(['new']);
});
