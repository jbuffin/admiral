import { describe, expect, it } from 'vitest';
import type { Item } from './api';
import { blocksOf, capOutput, toolRowLabel, workingLabel } from './turns';

const times = (name: string, n: number) => Array<string>(n).fill(name);

describe('toolRowLabel', () => {
  it('says how long a finished turn took and what its tools did', () => {
    expect(toolRowLabel(360_000, [...times('Read', 9), ...times('Bash', 2)])).toBe('Worked for 6m · read 9 files, ran 2 commands');
  });

  it('counts a tool with no verb as a tool used', () => {
    expect(toolRowLabel(120_000, ['Grep', 'Glob', 'WebFetch'])).toBe('Worked for 2m · used 3 tools');
  });

  it('counts Edit and Write together as edits', () => {
    expect(toolRowLabel(60_000, ['Edit', 'Write', 'Edit'])).toBe('Worked for 1m · edited 3 files');
  });
});

describe('workingLabel', () => {
  const at = (iso: string) => Date.parse(iso);
  const items = [
    { kind: 'user', at: '2026-05-01T10:00:00Z' },
    { kind: 'assistant', at: '2026-05-01T10:01:00Z' },
    { kind: 'user', at: '2026-05-01T10:05:00Z' },
    { kind: 'tool_call', at: '2026-05-01T10:05:30Z' },
  ];

  it('counts from the last user message', () => {
    expect(workingLabel(items, at('2026-05-01T10:07:10Z'))).toBe('Working · 2m');
  });

  it('counts on as the clock moves', () => {
    expect(workingLabel(items, at('2026-05-01T10:12:10Z'))).toBe('Working · 7m');
  });
});

describe('blocksOf', () => {
  const mk = (id: string, kind: Item['kind'], extra: Partial<Item> = {}): Item => ({ id, kind, ...extra });
  const call = (id: string) => mk(id, 'tool_call', { tool: { name: 'Read', done: true } });
  const shape = (items: Item[]) => blocksOf(items).map((b) => (b.kind === 'item' ? b.item.id : `${b.calls.map((c) => c.id).join('+')}${b.durationMs === undefined ? '' : `@${b.durationMs}`}`));

  it('folds a finished turn into one row where its first call was', () => {
    expect(shape([mk('u', 'user'), mk('a1', 'assistant'), call('t1'), call('t2'), mk('a2', 'assistant'), mk('e', 'turn_end', { durationMs: 5 })])).toEqual(['u', 'a1', 't1+t2@5', 'a2']);
  });

  it('leaves the calls of a turn that is still running unfolded', () => {
    expect(shape([mk('u', 'user'), call('t1')])).toEqual(['u', 't1']);
  });

  it('does not fold calls a new message cut off before a turn end', () => {
    expect(shape([call('t1'), mk('u', 'user'), call('t2'), mk('e', 'turn_end', { durationMs: 7 })])).toEqual(['t1', 'u', 't2@7']);
  });

  it('starts a new group after a compaction', () => {
    expect(shape([call('t1'), mk('c', 'compaction'), call('t2'), mk('e', 'turn_end', { durationMs: 1 })])).toEqual(['t1', 'c', 't2@1']);
  });
});

describe('capOutput', () => {
  const lines = (n: number) => Array.from({ length: n }, (_, i) => `l${i}`).join('\n');

  it('leaves output of 200 lines whole', () => {
    expect(capOutput(lines(200), false)).toEqual({ text: lines(200) });
  });

  it('cuts longer output to its first 200 lines and says how many there are', () => {
    const r = capOutput(lines(500), false);
    expect(r.total).toBe(500);
    expect(r.text.split('\n')).toHaveLength(200);
  });

  it('shows everything once asked', () => {
    expect(capOutput(lines(500), true)).toEqual({ text: lines(500) });
  });
});
