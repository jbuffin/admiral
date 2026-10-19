import { signal } from '@preact/signals';

export type HarnessName = 'claude' | 'copilot' | 'codex';

export interface Dock {
  harness: HarnessName;
  executable: string;
  executable_found: boolean;
  warnings: string[];
}

export interface Fleet {
  key: string;
  name: string;
  color: string;
  ink: string;
  search_roots: string[];
  can_launch: boolean;
  docks: Dock[];
}

export interface Config {
  fleets: Fleet[];
  error: { file: string; message: string } | null;
}

// null until the server's first `config` message arrives, so the page claims
// nothing about fleets before then.
export const config = signal<Config | null>(null);

// Follows the server's `config` messages. The socket is reopened when it drops.
export function connect(): void {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const ws = new WebSocket(`${proto}://${location.host}/ws`);
  ws.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    if (msg.type === 'config') config.value = { fleets: msg.fleets, error: msg.error };
  };
  ws.onclose = () => setTimeout(connect, 1000);
}
