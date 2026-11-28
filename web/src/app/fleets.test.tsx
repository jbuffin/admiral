import { beforeAll, expect, test, vi } from 'vitest';
import { defaultFleet, fleets, loaded, loadFleets, setFleetsView, watchFleets } from './fleets';
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
  try {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => answers.push(resolve))));
    const older = loadFleets();
    const newer = loadFleets();
    answers[1]!(answer(['new']));
    await newer;
    answers[0]!(answer(['old']));
    await older;
    expect(fleets.value[0]!.docks[0]!.models).toEqual(['new']);
  } finally {
    vi.stubGlobal('fetch', real);
  }
  await loadFleets();
});

test('loadFleets reads the default fleet and whether it fell back, and a view without them reads as fallen back', async () => {
  served = { ...served, default_fleet: 'zulu', default_fleet_fallback: false } as typeof served;
  await loadFleets();
  expect(defaultFleet.value).toEqual({ key: 'zulu', fallback: false });
  served = { fleets: served.fleets };
  await loadFleets();
  expect(defaultFleet.value).toEqual({ key: '', fallback: true });
});

test('a load that started before a save answered and lands after it does not put the old default back', async () => {
  const answers: ((r: Response) => void)[] = [];
  const real = fetch;
  try {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => answers.push(resolve))));
    const older = loadFleets();
    setFleetsView({ fleets: [fleets.value[0]!], default_fleet: 'bravo', default_fleet_fallback: false });
    answers[0]!(new Response(JSON.stringify({ fleets: [{ key: 'zulu', name: 'Zulu', docks: [dock([])] }], default_fleet: 'zulu', default_fleet_fallback: false })));
    await older;
    expect(defaultFleet.value).toEqual({ key: 'bravo', fallback: false });
  } finally {
    vi.stubGlobal('fetch', real);
  }
  await loadFleets();
});

test('the fleets are not loaded while a newer load is pending, so an older answer cannot leave an empty list marked loaded', async () => {
  const answers: ((r: Response) => void)[] = [];
  const answer = () => new Response(JSON.stringify({ fleets: [{ key: 'zulu', name: 'Zulu', docks: [dock(['sonnet'])] }] }));
  const real = fetch;
  fleets.value = [];
  loaded.value = false;
  try {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => answers.push(resolve))));
    const older = loadFleets();
    const newer = loadFleets();
    answers[0]!(answer());
    await older;
    expect(loaded.value).toBe(false);
    answers[1]!(answer());
    await newer;
    expect(loaded.value).toBe(true);
    expect(fleets.value.map((f) => f.key)).toEqual(['zulu']);
  } finally {
    vi.stubGlobal('fetch', real);
  }
  await loadFleets();
});
