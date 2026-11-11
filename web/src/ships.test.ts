import { beforeAll, describe, expect, it, vi } from 'vitest';

const handlers = new Map<string, (msg: unknown) => void>();
vi.mock('./connection', async () => {
  const { signal } = await import('@preact/signals');
  return { connectionState: signal('open'), onMessage: (t: string, fn: (m: unknown) => void) => handlers.set(t, fn) };
});

const wire = (name: string, status: string) => ({ session: `s-${name}`, name, fleet: 'zulu', status, crew: [], subagents: [], crew_live: 0, launched: '' });

describe('the old UI ship feed', () => {
  let visible: { value: { name: string }[] };
  beforeAll(async () => {
    vi.stubGlobal('window', { addEventListener() {} });
    ({ visible } = await import('./ships'));
  });

  it('never carries a moored ship', () => {
    handlers.get('ships')!({ ships: [wire('a', 'underway'), wire('m', 'moored'), wire('b', 'anchored')] });
    expect(visible.value.map((s) => s.name)).toEqual(['a', 'b']);
  });
});
