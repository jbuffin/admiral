import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { followConversation } from './conversation';
import { watchShips } from './shipsLink';

class FakeSocket extends EventTarget {
  static all: FakeSocket[] = [];
  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onclose: ((e: Event) => void) | null = null;
  readyState = 1;
  sent: unknown[] = [];
  constructor(public url: string) {
    super();
    FakeSocket.all.push(this);
  }
  close() {}
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  open() {
    this.onopen?.(new Event('open'));
  }
  receive(msg: unknown) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(msg) }));
  }
  drop() {
    this.onclose?.(new Event('close'));
  }
}

const item = (id: string, text: string, kind = 'assistant') => ({ id, kind, text, at: '2026-05-01T10:00:00Z' });

let pages: Record<string, unknown>;
let fetches: string[];
let stop: (() => void)[];

beforeEach(() => {
  vi.useFakeTimers();
  FakeSocket.all = [];
  fetches = [];
  stop = [];
  pages = {};
  vi.stubGlobal('WebSocket', FakeSocket);
  vi.stubGlobal('location', { protocol: 'http:', host: 'admiral.test' });
  vi.stubGlobal('fetch', async (input: string | URL | Request) => {
    const url = String(input);
    fetches.push(url);
    return new Response(JSON.stringify(pages[url] ?? { ships: [] }), { status: 200 });
  });
});

afterEach(() => {
  stop.forEach((s) => s());
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const sock = () => FakeSocket.all[FakeSocket.all.length - 1];
const flush = () => vi.advanceTimersByTimeAsync(0);
const PAGE = '/api/v1/ships/s-a/conversation';

function follow() {
  stop.push(watchShips());
  sock().open();
  const seen: { state: string; items: { id: string; text?: string }[]; more: boolean }[] = [];
  const f = followConversation('s-a', (c) => seen.push(structuredClone(c)));
  stop.push(f.stop);
  return { f, seen, last: () => seen[seen.length - 1] };
}

describe('followConversation', () => {
  it('opens the subscription, and reads the page only once the server has confirmed it', async () => {
    pages[PAGE] = { state: 'ok', items: [item('u1', 'hi', 'user')], more: false };
    const { last } = follow();
    expect(sock().sent).toContainEqual({ type: 'conversation_open', ship: 's-a' });
    await flush();
    expect(fetches).not.toContain(PAGE);

    sock().receive({ type: 'conversation_opened', ship: 's-a' });
    await flush();
    expect(fetches).toContain(PAGE);
    expect(last().state).toBe('ok');
    expect(last().items.map((i) => i.id)).toEqual(['u1']);
  });

  it('shows what each state of the page reads', async () => {
    pages[PAGE] = { state: 'none', items: [], more: false };
    const { last } = follow();
    sock().receive({ type: 'conversation_opened', ship: 's-a' });
    await flush();
    expect(last().state).toBe('none');
  });

  it('adds pushed items, and replaces one that arrives again under its id', async () => {
    pages[PAGE] = { state: 'ok', items: [item('a1', 'one')], more: false };
    const { last } = follow();
    sock().receive({ type: 'conversation_opened', ship: 's-a' });
    await flush();

    sock().receive({ type: 'conversation', ship: 's-a', items: [item('a2', 'two')] });
    expect(last().items.map((i) => i.id)).toEqual(['a1', 'a2']);
    sock().receive({ type: 'conversation', ship: 's-a', items: [{ ...item('a1', 'one'), text: 'one, edited' }] });
    expect(last().items.map((i) => [i.id, i.text])).toEqual([['a1', 'one, edited'], ['a2', 'two']]);
  });

  it('drops what it held when the server says the transcript was replaced', async () => {
    pages[PAGE] = { state: 'ok', items: [item('a1', 'one'), item('a2', 'two')], more: false };
    const { last } = follow();
    sock().receive({ type: 'conversation_opened', ship: 's-a' });
    await flush();

    pages[PAGE] = { state: 'ok', items: [item('b1', 'fresh')], more: false };
    sock().receive({ type: 'conversation', ship: 's-a', reset: true, items: [item('b1', 'fresh')] });
    await flush();
    expect(last().items.map((i) => i.id)).toEqual(['b1']);
  });

  it('ignores a push for another ship', async () => {
    pages[PAGE] = { state: 'ok', items: [item('a1', 'one')], more: false };
    const { last } = follow();
    sock().receive({ type: 'conversation_opened', ship: 's-a' });
    await flush();
    sock().receive({ type: 'conversation', ship: 's-b', items: [item('b1', 'elsewhere')] });
    expect(last().items.map((i) => i.id)).toEqual(['a1']);
  });

  it('opens again after a reconnect and reads the page again', async () => {
    pages[PAGE] = { state: 'ok', items: [item('a1', 'one')], more: false };
    follow();
    sock().receive({ type: 'conversation_opened', ship: 's-a' });
    await flush();
    const before = fetches.filter((u) => u === PAGE).length;

    const first = sock();
    first.drop();
    await vi.advanceTimersByTimeAsync(1000);
    expect(sock()).not.toBe(first);
    sock().open();
    expect(sock().sent).toContainEqual({ type: 'conversation_open', ship: 's-a' });
    expect(fetches.filter((u) => u === PAGE).length).toBe(before);

    sock().receive({ type: 'conversation_opened', ship: 's-a' });
    await flush();
    expect(fetches.filter((u) => u === PAGE).length).toBe(before + 1);
  });

  it('reads a ship with nothing recorded once when it is told not to ask again', async () => {
    pages[PAGE] = { state: 'none', items: [], more: false };
    stop.push(watchShips());
    sock().open();
    stop.push(followConversation('s-a', () => {}, { retryNone: false }).stop);
    await vi.advanceTimersByTimeAsync(20_000);
    expect(fetches.filter((u) => u === PAGE)).toHaveLength(1);
    expect(sock().sent.filter((m) => (m as { type: string }).type === 'conversation_open')).toHaveLength(1);
  });

  it('keeps the page first, with its more flag, when a push lands before the page does', async () => {
    pages[PAGE] = { state: 'ok', items: [item('a1', 'one'), item('a2', 'two')], more: true };
    const { last } = follow();
    sock().receive({ type: 'conversation', ship: 's-a', items: [item('a3', 'three'), { ...item('a2', 'two'), text: 'two, newer' }] });
    sock().receive({ type: 'conversation_opened', ship: 's-a' });
    await flush();
    expect(last().items.map((i) => [i.id, i.text])).toEqual([['a1', 'one'], ['a2', 'two, newer'], ['a3', 'three']]);
    expect(last().more).toBe(true);
  });

  it("replaces a held round divider with the page's, so a state it carried does not stick", async () => {
    const divider = (state?: string) => ({ id: 'r1', kind: 'round', round: 1, at: '2026-05-01T10:00:00Z', ...(state ? { state } : {}) });
    pages[PAGE] = { state: 'ok', items: [item('a1', 'one'), divider(), item('r1:a2', 'two')], more: false };
    const { last } = follow();
    sock().receive({ type: 'conversation', ship: 's-a', items: [divider('none')] });
    sock().receive({ type: 'conversation_opened', ship: 's-a' });
    await flush();
    expect(last().items.map((i) => i.id)).toEqual(['a1', 'r1', 'r1:a2']);
    expect(last().items.find((i) => i.id === 'r1')).not.toHaveProperty('state');
  });

  it('ignores a push that carries a crew boat', async () => {
    pages[PAGE] = { state: 'ok', items: [item('a1', 'one')], more: false };
    const { last } = follow();
    sock().receive({ type: 'conversation_opened', ship: 's-a' });
    await flush();
    sock().receive({ type: 'conversation', ship: 's-a', crew: 'boat-1', items: [item('c1', 'boat')] });
    expect(last().items.map((i) => i.id)).toEqual(['a1']);
  });

  it('does not throw when older items fail to load', async () => {
    pages[PAGE] = { state: 'ok', items: [item('a1', 'one')], more: true };
    const { f, last } = follow();
    sock().receive({ type: 'conversation_opened', ship: 's-a' });
    await flush();
    vi.stubGlobal('fetch', async () => {
      throw new TypeError('offline');
    });
    await expect(f.loadOlder()).resolves.toBeUndefined();
    expect(last().more).toBe(true);
  });

  it('stops asking about a ship with nothing recorded once the ask says no', async () => {
    pages[PAGE] = { state: 'none', items: [], more: false };
    let ask = true;
    stop.push(watchShips());
    sock().open();
    stop.push(followConversation('s-a', () => {}, { retryNone: () => ask }).stop);
    await vi.advanceTimersByTimeAsync(5_000);
    const opens = () => sock().sent.filter((m) => (m as { type: string }).type === 'conversation_open').length;
    expect(opens()).toBeGreaterThan(1);
    ask = false;
    await vi.advanceTimersByTimeAsync(5_000);
    const settled = opens();
    await vi.advanceTimersByTimeAsync(20_000);
    expect(opens()).toBe(settled);
  });

  it('closes the subscription when stopped', async () => {
    const { f } = follow();
    f.stop();
    expect(sock().sent).toContainEqual({ type: 'conversation_close', ship: 's-a' });
  });

  it('loads older items before the first one, with its id escaped', async () => {
    pages[PAGE] = { state: 'ok', items: [item('uuid-1#1', 'newer')], more: true };
    pages[`${PAGE}?before=uuid-1%231`] = { state: 'ok', items: [item('uuid-0', 'older')], more: false };
    const { f, last } = follow();
    sock().receive({ type: 'conversation_opened', ship: 's-a' });
    await flush();
    expect(last().more).toBe(true);

    await f.loadOlder();
    expect(fetches).toContain(`${PAGE}?before=uuid-1%231`);
    expect(last().items.map((i) => i.id)).toEqual(['uuid-0', 'uuid-1#1']);
    expect(last().more).toBe(false);
  });
});
