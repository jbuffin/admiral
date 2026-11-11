import { signal } from '@preact/signals';
import { probeShips, shipsSocketUrl, type Ship } from './api';

export const ships = signal<Ship[]>([]);
export const shipsLink = signal<'connecting' | 'open' | 'reconnecting' | 'signed-out'>('connecting');

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
    s.onopen = () => {
      shipsLink.value = 'open';
      delay = RETRY_FIRST_MS;
    };
    s.onmessage = (e) => {
      const msg = JSON.parse(e.data);
      if (msg.type === 'ships') ships.value = msg.ships ?? [];
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
      socket.onclose = null;
      socket.close();
    }
  };
}
