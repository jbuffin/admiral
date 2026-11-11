import { describe, expect, it } from 'vitest';
import { toolRowLabel, workingLabel } from './turns';

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
