import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Ship } from './api';
import { inboxItems, retryShips, ships, shipsLink, watchShips, chats } from './shipsLink';

class FakeSocket extends EventTarget {
  static all: FakeSocket[] = [];
  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onclose: ((e: Event) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  closed = false;
  constructor(public url: string) {
    super();
    FakeSocket.all.push(this);
  }
  close() {
    this.closed = true;
  }
  private fire(type: string, e: Event, on: ((e: Event) => void) | null) {
    on?.call(this, e);
    this.dispatchEvent(e);
    return type;
  }
  open() {
    this.fire('open', new Event('open'), this.onopen);
  }
  send(msg: unknown) {
    const e = new MessageEvent('message', { data: JSON.stringify(msg) });
    this.fire('message', e, this.onmessage as ((e: Event) => void) | null);
  }
  drop() {
    this.fire('close', new Event('close'), this.onclose);
  }
}

const ship = (name: string, status: Ship['status'], tail = ''): Ship => ({
  session: `s-${name}`,
  name,
  fleet: 'zulu',
  harness: 'claude',
  status,
  order_id: '',
  title: '',
  tail,
  fleet_color: '',
  launched: '',
  crew_live: 0,
  dir: '',
  repo: '',
  exit_code: null,
  exit_signal: null,
  crew: [],
});

let fetches: string[];
let probeAnswer: () => Promise<Response>;
let stop: () => void;

beforeEach(() => {
  vi.useFakeTimers();
  FakeSocket.all = [];
  fetches = [];
  probeAnswer = async () => new Response(JSON.stringify({ ships: [] }), { status: 200 });
  vi.stubGlobal('WebSocket', FakeSocket);
  vi.stubGlobal('location', { protocol: 'http:', host: 'admiral.test' });
  vi.stubGlobal('fetch', (input: string | URL | Request) => {
    fetches.push(String(input));
    return probeAnswer();
  });
  ships.value = [];
});

afterEach(() => {
  stop?.();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const sockets = () => FakeSocket.all;
const last = () => {
  expect(FakeSocket.all, 'sockets opened').not.toHaveLength(0);
  return FakeSocket.all[FakeSocket.all.length - 1];
};
const tick = (ms: number) => vi.advanceTimersByTimeAsync(ms);

describe('watchShips', () => {
  it('opens the versioned socket and takes the ships of each message', () => {
    stop = watchShips();
    expect(sockets().map((s) => s.url)).toEqual(['ws://admiral.test/ws/v1']);
    last().open();
    expect(shipsLink.value).toBe('open');

    last().send({ type: 'ships', ships: [ship('a', 'needs_you', 'Which branch?')] });
    expect(ships.value.map((s) => [s.name, s.status, s.tail])).toEqual([['a', 'needs_you', 'Which branch?']]);

    last().send({ type: 'ships', ships: [ship('a', 'underway', 'Reading main.go')] });
    expect(ships.value.map((s) => [s.name, s.status, s.tail])).toEqual([['a', 'underway', 'Reading main.go']]);
  });

  it('ignores a message that is not the ships', () => {
    stop = watchShips();
    last().open();
    last().send({ type: 'ships', ships: [ship('a', 'underway')] });
    last().send({ type: 'inbox', items: [] });
    expect(ships.value.map((s) => s.name)).toEqual(['a']);
  });

  it('keeps the items of each inbox message, replacing the last, and leaves the ships alone', () => {
    stop = watchShips();
    last().open();
    expect(inboxItems.value).toEqual([]);
    last().send({ type: 'ships', ships: [ship('a', 'underway')] });
    last().send({ type: 'inbox', items: [{ id: 'q-1', session: 's-a' }, { id: 'r-1', session: 's-a' }] });
    expect(inboxItems.value.map((i) => i.id)).toEqual(['q-1', 'r-1']);
    last().send({ type: 'inbox', items: [{ id: 'r-1', session: 's-a' }] });
    expect(inboxItems.value.map((i) => i.id)).toEqual(['r-1']);
    expect(ships.value.map((s) => s.name)).toEqual(['a']);
  });

  it("keeps each fleet's chat apart: a chat message replaces only its own fleet's, and leaves the ships alone", () => {
    chats.value = {};
    stop = watchShips();
    last().open();
    last().send({ type: 'ships', ships: [ship('a', 'underway')] });
    last().send({ type: 'chat', fleet: 'alpha', running: true, state: 'idle', messages: [{ id: 'u-1', kind: 'message', role: 'user', text: 'alpha one', at: '' }], questions: [] });
    last().send({ type: 'chat', fleet: 'beta', running: true, state: 'idle', messages: [{ id: 'u-9', kind: 'message', role: 'user', text: 'beta one', at: '' }], questions: [] });
    expect(Object.keys(chats.value).sort()).toEqual(['alpha', 'beta']);
    expect(chats.value.alpha.messages.map((m) => m.text)).toEqual(['alpha one']);
    last().send({ type: 'chat', fleet: 'alpha', running: true, state: 'working', messages: [], questions: [] });
    expect(chats.value.alpha.state).toBe('working');
    expect(chats.value.beta.messages.map((m) => m.text)).toEqual(['beta one']);
    expect(ships.value.map((s) => s.name)).toEqual(['a']);
  });

  it('uses wss on an https page', () => {
    vi.stubGlobal('location', { protocol: 'https:', host: 'admiral.test' });
    stop = watchShips();
    expect(last().url).toBe('wss://admiral.test/ws/v1');
  });

  it('probes /api/v1/ships after 1 s, then reopens the socket', async () => {
    stop = watchShips();
    last().open();
    last().drop();
    expect(shipsLink.value).toBe('reconnecting');

    await tick(999);
    expect(fetches).toEqual([]);
    await tick(1);
    expect(fetches).toEqual(['/api/v1/ships']);
    expect(sockets()).toHaveLength(2);
    expect(last().url).toBe('ws://admiral.test/ws/v1');
  });

  it('waits 1, 2, 4, 8 and then 10 s while the probe finds the server down, and starts over once it is open', async () => {
    probeAnswer = () => Promise.reject(new TypeError('connection refused'));
    stop = watchShips();
    last().open();
    last().drop();

    let probes = 0;
    for (const wait of [1000, 2000, 4000, 8000, 10000, 10000]) {
      await tick(wait - 1);
      expect(fetches, `before ${wait} ms`).toHaveLength(probes);
      await tick(1);
      probes++;
      expect(fetches, `at ${wait} ms`).toHaveLength(probes);
    }
    expect(sockets()).toHaveLength(1);

    probeAnswer = async () => new Response(JSON.stringify({ ships: [] }), { status: 200 });
    await tick(10000);
    expect(sockets()).toHaveLength(2);
    last().open();
    expect(shipsLink.value).toBe('open');
    last().drop();

    const before = fetches.length;
    await tick(999);
    expect(fetches).toHaveLength(before);
    await tick(1);
    expect(fetches).toHaveLength(before + 1);
  });

  it('stops as signed out when the probe answers 401', async () => {
    probeAnswer = async () => new Response('', { status: 401 });
    stop = watchShips();
    last().open();
    last().drop();
    await tick(1000);

    expect(shipsLink.value).toBe('signed-out');
    await tick(60_000);
    expect(fetches).toHaveLength(1);
    expect(sockets()).toHaveLength(1);
  });

  it('says the Mac cannot be reached when the probe fails to connect, and keeps probing', async () => {
    probeAnswer = () => Promise.reject(new TypeError('Load failed'));
    stop = watchShips();
    last().drop();
    await tick(1000);
    expect(shipsLink.value).toBe('unreachable');
    await tick(2000);
    expect(fetches).toHaveLength(2);
    expect(shipsLink.value).toBe('unreachable');
  });

  it('does not say unreachable when the server answers with an error', async () => {
    probeAnswer = async () => new Response('', { status: 500 });
    stop = watchShips();
    last().drop();
    await tick(1000);
    expect(shipsLink.value).toBe('reconnecting');
  });

  it('probes at once on retry and connects when the server answers', async () => {
    probeAnswer = () => Promise.reject(new TypeError('Load failed'));
    stop = watchShips();
    last().drop();
    await tick(1000);
    expect(shipsLink.value).toBe('unreachable');

    probeAnswer = async () => new Response(JSON.stringify({ ships: [] }), { status: 200 });
    const before = fetches.length;
    retryShips();
    await tick(0);
    expect(fetches).toHaveLength(before + 1);
    expect(sockets()).toHaveLength(2);
    last().open();
    expect(shipsLink.value).toBe('open');
  });

  it('opens one socket however often Retry is tapped while it connects', async () => {
    probeAnswer = () => Promise.reject(new TypeError('Load failed'));
    stop = watchShips();
    last().drop();
    await tick(1000);
    probeAnswer = async () => new Response(JSON.stringify({ ships: [] }), { status: 200 });
    retryShips();
    retryShips();
    await tick(0);
    retryShips();
    await tick(0);
    expect(sockets()).toHaveLength(2);
    last().open();
    expect(shipsLink.value).toBe('open');
  });

  it('stays unreachable when a retry still cannot connect', async () => {
    probeAnswer = () => Promise.reject(new TypeError('Load failed'));
    stop = watchShips();
    last().drop();
    await tick(1000);
    retryShips();
    await tick(0);
    expect(shipsLink.value).toBe('unreachable');
    expect(sockets()).toHaveLength(1);
  });

  it('closes its socket and stops retrying when stopped', async () => {
    stop = watchShips();
    const first = last();
    first.open();
    stop();
    expect(first.closed).toBe(true);

    first.drop();
    await tick(60_000);
    expect(fetches).toEqual([]);
    expect(sockets()).toHaveLength(1);
  });
});
