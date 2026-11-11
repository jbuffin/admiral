import { signal } from '@preact/signals';
import { probeShips, shipsSocketUrl, type Resource, type Ship, type Source } from './api';

export const ships = signal<Ship[]>([]);
// False until the first ships message, so the empty state doesn't show before the list has arrived.
export const shipsLoaded = signal(false);
export const sources = signal<Source[]>([]);
export const resources = signal<Resource[]>([]);
export const shipsLink = signal<'connecting' | 'open' | 'reconnecting' | 'signed-out'>('connecting');

type Listener = (msg: { type: string; ship?: string; items?: unknown[] }) => void;
const listeners = new Set<Listener>();
const openers = new Set<() => void>();
let current: WebSocket | undefined;
const OPEN = 1; // WebSocket.OPEN

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

const RETRY_FIRST_MS = 1000;
const RETRY_MAX_MS = 10000;

/** Follows the ships over /ws/v1 until the returned function is called. */
export function watchShips(): () => void {
  let socket: WebSocket | undefined;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let delay = RETRY_FIRST_MS;
  let stopped = false;

  const open = () => {
    const s = new WebSocket(shipsSocketUrl());
    socket = s;
    current = s;
    s.onopen = () => {
      shipsLink.value = 'open';
      delay = RETRY_FIRST_MS;
      openers.forEach((fn) => fn());
    };
    s.onmessage = (e) => {
      const msg = JSON.parse(e.data);
      listeners.forEach((fn) => fn(msg));
      if (msg.type === 'ships') {
        ships.value = msg.ships ?? [];
        shipsLoaded.value = true;
      } else if (msg.type === 'sources') sources.value = msg.sources ?? [];
      else if (msg.type === 'resources') resources.value = msg.resources ?? [];
    };
    s.onclose = () => {
      shipsLink.value = 'reconnecting';
      schedule();
    };
  };

  const schedule = () => {
    const wait = delay;
    delay = Math.min(delay * 2, RETRY_MAX_MS);
    retry = setTimeout(async () => {
      const p = await probeShips();
      if (stopped) return;
      if (p === 'signed-out') shipsLink.value = 'signed-out';
      else if (p === 'down') schedule();
      else open();
    }, wait);
  };

  shipsLink.value = 'connecting';
  open();
  return () => {
    stopped = true;
    clearTimeout(retry);
    if (socket) {
      if (current === socket) current = undefined;
      socket.onclose = null;
      socket.close();
    }
  };
}
