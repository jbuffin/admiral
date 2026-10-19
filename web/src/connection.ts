import { signal } from '@preact/signals';

export type ConnectionState = 'connecting' | 'open' | 'closed';

export const connectionState = signal<ConnectionState>('connecting');

// True from the moment the page's socket drops until it is back: the header's "Reconnecting…".
export const offline = signal(false);

// A 401 on reconnect: the token changed. Everything stops retrying and says so.
export const signedOut = signal(false);

type Handler = (msg: any) => void;
const handlers = new Map<string, Set<Handler>>();

// Calls fn for every message of this type; returns the unsubscribe.
export function onMessage(type: string, fn: Handler): () => void {
  let set = handlers.get(type);
  if (!set) handlers.set(type, (set = new Set()));
  set.add(fn);
  return () => set.delete(fn);
}

// Retries wait 1 s, doubling to 10 s, until something connects.
export const RETRY_FIRST_MS = 1000;
export const RETRY_MAX_MS = 10000;
export const nextDelay = (ms: number) => Math.min(ms * 2, RETRY_MAX_MS);

export type Probe = { kind: 'ok'; ships: { session: string }[] } | { kind: 'signed-out' } | { kind: 'down' };

// Browsers don't tell a page why a WebSocket handshake failed, so a failed reconnect asks
// over HTTP: a 401 is a changed token, a refused connection is a server that is still away.
export async function probe(): Promise<Probe> {
  try {
    const res = await fetch('/api/ships', { cache: 'no-store' });
    if (res.status === 401) return { kind: 'signed-out' };
    if (!res.ok) return { kind: 'down' };
    const body = await res.json();
    return { kind: 'ok', ships: body.ships ?? [] };
  } catch {
    return { kind: 'down' };
  }
}

let ws: WebSocket | undefined;
let retry: ReturnType<typeof setTimeout> | undefined;
let delay = RETRY_FIRST_MS;

function open(): void {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const socket = new WebSocket(`${proto}://${location.host}/ws`);
  ws = socket;
  connectionState.value = 'connecting';
  socket.onopen = () => {
    connectionState.value = 'open';
    offline.value = false;
    delay = RETRY_FIRST_MS;
  };
  socket.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    handlers.get(msg.type)?.forEach((fn) => fn(msg));
  };
  // The socket is reopened when it drops.
  socket.onclose = () => {
    connectionState.value = 'closed';
    offline.value = true;
    schedule();
  };
}

function schedule(): void {
  const wait = delay;
  delay = nextDelay(delay);
  retry = setTimeout(async () => {
    const p = await probe();
    if (p.kind === 'signed-out') {
      signedOut.value = true;
    } else if (p.kind === 'down') {
      schedule();
    } else {
      open();
    }
  }, wait);
}

// Opens the page's one socket; the returned function closes it for good
// (unmount, HMR), so no socket outlives the page that asked for it.
export function connect(): () => void {
  open();
  return () => {
    clearTimeout(retry);
    if (ws) {
      ws.onclose = null;
      ws.close();
      ws = undefined;
    }
  };
}
