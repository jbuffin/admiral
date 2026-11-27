import { signal } from '@preact/signals';

// Red-phase stub so the tests compile; the implementation replaces it.
export function parseRepoOrder(_raw: string | null): string[] {
  return [];
}

export const repoOrder = signal<string[]>([]);
