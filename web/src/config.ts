import { computed, signal } from '@preact/signals';
import { onMessage } from './connection';

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
// nothing about fleets before then; configLoaded tells the two states apart.
export const config = signal<Config | null>(null);
export const configLoaded = computed(() => config.value !== null);

onMessage('config', (msg) => (config.value = { fleets: msg.fleets, error: msg.error }));
