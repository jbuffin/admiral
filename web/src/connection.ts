import { signal } from '@preact/signals';

export type ConnectionState = 'connecting' | 'open' | 'closed';

export const connectionState = signal<ConnectionState>('connecting');

type Handler = (msg: any) => void;
const handlers = new Map<string, Set<Handler>>();

// Calls fn for every message of this type; returns the unsubscribe.
export function onMessage(type: string, fn: Handler): () => void {
  let set = handlers.get(type);
  if (!set) handlers.set(type, (set = new Set()));
  set.add(fn);
  return () => set.delete(fn);
}

let ws: WebSocket | undefined;
let retry: ReturnType<typeof setTimeout> | undefined;

function open(): void {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const socket = new WebSocket(`${proto}://${location.host}/ws`);
  ws = socket;
  connectionState.value = 'connecting';
  socket.onopen = () => (connectionState.value = 'open');
  socket.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    handlers.get(msg.type)?.forEach((fn) => fn(msg));
  };
  // The socket is reopened when it drops.
  socket.onclose = () => {
    connectionState.value = 'closed';
    retry = setTimeout(open, 1000);
  };
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
