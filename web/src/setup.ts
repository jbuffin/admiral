import { effect, signal } from '@preact/signals';
import { applyConfig } from './config';
import type { Probe } from './connection';
import { boardedSession } from './ships';

// The setup terminal (stories 19-21): `admiral setup` in the tmux session `_setup`, shown with the
// bridge's terminal. Open from the "Set up a fleet" marina and from the header's fleet count.
export const SETUP_SESSION = '_setup';

export const setupOpen = signal(false);

// Asks the server to start setup, or to leave the one that is running alone, so every browser
// that taps joins the same session. Only then does the terminal open and join it.
export async function openSetup(): Promise<void> {
  try {
    const res = await fetch('/api/setup', { method: 'POST' });
    if (!res.ok) return;
  } catch {
    return;
  }
  boardedSession.value = null; // one terminal over open water at a time
  setupOpen.value = true;
}

// The bridge of a ship and the setup terminal are never both up.
effect(() => {
  if (boardedSession.value) setupOpen.value = false;
});

// The terminal closes when the `_setup` session ends, whether setup wrote the file or not.
// The page then re-reads the config: the push may have come while the page was away.
export function closeSetup(): void {
  setupOpen.value = false;
  void refreshConfig();
}

async function refreshConfig(): Promise<void> {
  try {
    const res = await fetch('/api/config', { cache: 'no-store' });
    if (res.ok) applyConfig(await res.json());
  } catch {
    // The socket's own push brings the config when the server is back.
  }
}

/** The reconnect check of the link: is `_setup` still running? Same shape as the ships probe. */
export async function probeSetup(): Promise<Probe> {
  try {
    const res = await fetch('/api/setup', { cache: 'no-store' });
    if (res.status === 401) return { kind: 'signed-out' };
    if (!res.ok) return { kind: 'down' };
    const { running } = await res.json();
    return { kind: 'ok', ships: running ? [{ session: SETUP_SESSION }] : [] };
  } catch {
    return { kind: 'down' };
  }
}
