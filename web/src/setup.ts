import { effect, signal } from '@preact/signals';
import { applyConfig } from './config';
import type { Probe } from './connection';
import { boarded, unboard } from './ships';

export const SETUP_SESSION = '_setup';

export const setupOpen = signal(false);

// The server leaves a running setup alone, so every browser that taps joins the same session.
export async function openSetup(): Promise<void> {
  try {
    const res = await fetch('/api/setup', { method: 'POST' });
    if (!res.ok) return;
  } catch {
    return;
  }
  unboard(); // one terminal over open water at a time
  setupOpen.value = true;
}

// The bridge of a ship and the setup terminal are never both up.
effect(() => {
  if (boarded.value) setupOpen.value = false;
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
