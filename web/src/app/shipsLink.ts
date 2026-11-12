import { signal } from '@preact/signals';
import { probeShips, shipsSocketUrl, type Resource, type Ship, type Source } from './api';
import { signedOut } from './session';
import type { ChatSnapshot } from './chat';
import type { InboxItem } from './inbox';
import { markPaired } from './pairing';
import { moveTarget } from './remoteTailscale';

export const ships = signal<Ship[]>([]);
// False until the first ships message, so the empty state doesn't show before the list has arrived.
export const shipsLoaded = signal(false);
// The items of the latest inbox message; each message replaces the last.
export const inboxItems = signal<InboxItem[]>([]);
// False until the first inbox message, so a Peek on a file item does not close before its item has arrived.
export const inboxLoaded = signal(false);
// Unlike the two flags above these reset with each connection: a resumed page holds the lists of before it was suspended until the new snapshots arrive.
export const shipsFresh = signal(false);
export const inboxFresh = signal(false);
export const sources = signal<Source[]>([]);
export const resources = signal<Resource[]>([]);
// Each fleet's latest chat message, keyed by fleet; a message replaces only its own fleet's.
export const chats = signal<Record<string, ChatSnapshot>>({});
export const shipsLink = signal<'connecting' | 'open' | 'reconnecting' | 'signed-out' | 'unreachable'>('connecting');

type Listener = (msg: { type: string; ship?: string; crew?: string; items?: unknown[]; reset?: boolean }) => void;
const listeners = new Set<Listener>();
const openers = new Set<() => void>();
let current: WebSocket | undefined;
const OPEN = 1; // WebSocket.OPEN

// A throwing listener must not stop the ships list from updating.
const guard = (fn: () => void) => {
  try {
    fn();
  } catch (e) {
    console.error(e);
  }
};

/** Hears every message on the ships socket that the list itself does not own. */
export function onShipsMessage(fn: Listener): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** Runs each time the ships socket opens, a reconnect included. */
export function onShipsOpen(fn: () => void): () => void {
  openers.add(fn);
  return () => openers.delete(fn);
}

/** Sends a message when the socket is up; nothing is queued. Whether it went is returned. */
export function sendShips(msg: object): boolean {
  if (current?.readyState !== OPEN) return false;
  current.send(JSON.stringify(msg));
  return true;
}

let retryNow: (() => void) | undefined;

/** Probes at once instead of waiting out the backoff; the socket opens when the server answers. */
export function retryShips() {
  retryNow?.();
}

const RETRY_FIRST_MS = 1000;
const RETRY_MAX_MS = 10000;

/** Follows the ships over /ws/v1 until the returned function is called. */
export function watchShips(): () => void {
  let socket: WebSocket | undefined;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let delay = RETRY_FIRST_MS;
  let stopped = false;
  let probing = false;
  let connecting = false;

  const open = () => {
    // A second socket would deliver every message twice and leave its own retry chain behind.
    if (socket) {
      socket.onclose = null;
      socket.close();
    }
    const s = new WebSocket(shipsSocketUrl());
    socket = s;
    connecting = true;
    current = s;
    s.onopen = () => {
      connecting = false;
      shipsFresh.value = false;
      inboxFresh.value = false;
      shipsLink.value = 'open';
      markPaired();
      delay = RETRY_FIRST_MS;
      openers.forEach(guard);
    };
    s.onmessage = (e) => {
      const msg = JSON.parse(e.data);
      listeners.forEach((fn) => guard(() => fn(msg)));
      if (msg.type === 'ships') {
        ships.value = msg.ships ?? [];
        shipsLoaded.value = true;
        shipsFresh.value = true;
      } else if (msg.type === 'inbox') {
        inboxItems.value = msg.items ?? [];
        inboxLoaded.value = true;
        inboxFresh.value = true;
      }
      else if (msg.type === 'move') {
        const to = moveTarget(msg, location.href);
        if (to) location.assign(to);
      } else if (msg.type === 'chat' && typeof msg.fleet === 'string') {
        const { type: _type, ...snap } = msg;
        chats.value = { ...chats.value, [msg.fleet]: snap };
      } else if (msg.type === 'sources') sources.value = msg.sources ?? [];
      else if (msg.type === 'resources') resources.value = msg.resources ?? [];
    };
    s.onclose = () => {
      connecting = false;
      shipsFresh.value = false;
      inboxFresh.value = false;
      shipsLink.value = 'reconnecting';
      schedule();
    };
  };

  const schedule = () => {
    const wait = delay;
    delay = Math.min(delay * 2, RETRY_MAX_MS);
    retry = setTimeout(probe, wait);
  };

  const probe = async () => {
    probing = true;
    const p = await probeShips();
    probing = false;
    if (stopped) return;
    if (p === 'signed-out') {
      shipsLink.value = 'signed-out';
      signedOut.value = true;
    } else if (p === 'unreachable') {
      shipsLink.value = 'unreachable';
      schedule();
    } else if (p === 'down') schedule();
    else open();
  };

  retryNow = () => {
    if (probing || connecting) return;
    clearTimeout(retry);
    delay = RETRY_FIRST_MS;
    void probe();
  };

  shipsLink.value = 'connecting';
  open();
  return () => {
    stopped = true;
    retryNow = undefined;
    clearTimeout(retry);
    if (socket) {
      if (current === socket) current = undefined;
      socket.onclose = null;
      socket.close();
    }
  };
}
