import { describe, expect, it } from 'vitest';
import type { Ship } from './api';
import { agentRows, runningAgents } from './agents';

const boat = (b: Record<string, unknown> & { name: string }) => ({ conversation: false, harness: 'claude', status: 'working', exit_code: null, exit_signal: null, launched: '2026-10-24T10:00:00Z', ...b });
const shipOf = (crew: unknown[] = [], agents: unknown[] = []) => ({ crew, agents }) as unknown as Ship;
const T = (hhmm: string) => `2026-10-24T${hhmm}:00Z`;

describe('agentRows', () => {
  it('labels each kind the way the tab names it', () => {
    const rows = agentRows(
      shipOf(
        [boat({ name: 'mate' })],
        [
          { id: 'a1', type: 'Explore', start: T('10:00'), kind: 'subagent' },
          { id: 'a2', type: 'Plan', start: T('10:00'), kind: 'teammate' },
          { id: 'sh1', type: '', start: T('10:00'), kind: 'shell' },
          { id: 'mon1', type: '', start: T('10:00'), kind: 'monitor' },
        ],
      ),
    );
    expect(rows.map((r) => [r.name, r.kindLabel])).toEqual([
      ['mate', 'Crew boat'],
      ['a1', 'Subagent'],
      ['a2', 'Agent-team teammate'],
      ['sh1', 'Background shell'],
      ['mon1', 'Background monitor'],
    ]);
  });

  it('reads an entry with no kind as a subagent', () => {
    const [row] = agentRows(shipOf([], [{ id: 'old1', type: 'Explore', start: T('10:00') }]));
    expect(row.kindLabel).toBe('Subagent');
  });

  it('names a boat by its name and an entry by its id, and keeps the subagent type only when there is one', () => {
    const rows = agentRows(shipOf([boat({ name: 'scout', role: 'reviewer' })], [{ id: 'a1', type: 'Explore', start: T('10:00'), kind: 'subagent' }, { id: 'sh1', type: '', start: T('10:00'), kind: 'shell' }]));
    expect(rows.map((r) => [r.name, r.type || null])).toEqual([['scout', null], ['a1', 'Explore'], ['sh1', null]]);
  });

  it('carries each row\'s start (a boat\'s launch) and, once finished, its end', () => {
    const rows = agentRows(
      shipOf([boat({ name: 'mate', launched: T('09:00') })], [{ id: 'a1', type: '', start: T('10:00'), kind: 'subagent', end: T('10:30') }, { id: 'a2', type: '', start: T('10:10'), kind: 'subagent' }]),
    );
    const by = Object.fromEntries(rows.map((r) => [r.name, r]));
    expect([by.mate.start, by.mate.state]).toEqual([T('09:00'), 'running']);
    expect([by.a1.start, by.a1.end, by.a1.state]).toEqual([T('10:00'), T('10:30'), 'finished']);
    expect([by.a2.end ?? null, by.a2.state]).toEqual([null, 'running']);
  });

  it('counts a boat as running whatever its status but anchored, so idle and needs_you run', () => {
    const rows = agentRows(shipOf(['working', 'idle', 'needs_you', 'underway', 'anchored'].map((status) => boat({ name: status, status }))));
    expect(rows.map((r) => [r.name, r.state])).toEqual([
      ['working', 'running'],
      ['idle', 'running'],
      ['needs_you', 'running'],
      ['underway', 'running'],
      ['anchored', 'finished'],
    ]);
  });

  it('puts running rows first, boats before entries, and finished rows after them newest first', () => {
    const rows = agentRows(
      shipOf(
        [
          boat({ name: 'old-boat', status: 'anchored', launched: T('08:00'), last_activity: undefined }), // finished, ordered by its launch
          boat({ name: 'busy', status: 'working', launched: T('08:30') }),
          boat({ name: 'late-boat', status: 'anchored', launched: T('08:00'), last_activity: T('12:30') }), // finished, ordered by its last activity
        ],
        [
          { id: 'fin-a', type: '', start: T('10:00'), kind: 'subagent', end: T('12:00') },
          { id: 'run-a', type: '', start: T('11:00'), kind: 'subagent' },
          { id: 'fin-b', type: '', start: T('10:00'), kind: 'shell', end: T('11:30') },
          { id: 'run-b', type: '', start: T('11:30'), kind: 'monitor' },
        ],
      ),
    );
    expect(rows.map((r) => r.name)).toEqual(['busy', 'run-a', 'run-b', 'late-boat', 'fin-a', 'fin-b', 'old-boat']);
  });

  it('is empty for a ship with no crew and no entries, and for one whose server sends no agents', () => {
    expect(agentRows(shipOf())).toEqual([]);
    expect(agentRows({ crew: [] } as unknown as Ship)).toEqual([]);
  });

  it('gives every row a distinct key, even when a boat and an entry share a name', () => {
    const rows = agentRows(shipOf([boat({ name: 'dup' })], [{ id: 'dup', type: '', start: T('10:00'), kind: 'subagent' }]));
    expect(new Set(rows.map((r) => r.key)).size).toBe(2);
  });
});

describe('runningAgents', () => {
  it('counts running boats and running entries, not finished ones', () => {
    const ship = shipOf(
      [boat({ name: 'a' }), boat({ name: 'b', status: 'idle' }), boat({ name: 'c', status: 'anchored' })],
      [{ id: 'x', type: '', start: T('10:00'), kind: 'subagent' }, { id: 'y', type: '', start: T('10:00'), kind: 'shell', end: T('10:05') }, { id: 'z', type: '', start: T('10:00'), kind: 'monitor' }],
    );
    expect(runningAgents(ship)).toBe(4);
  });

  it('is 0 when everything has finished or there is nothing', () => {
    expect(runningAgents(shipOf())).toBe(0);
    expect(runningAgents(shipOf([boat({ name: 'c', status: 'anchored' })], [{ id: 'y', type: '', start: T('10:00'), kind: 'shell', end: T('10:05') }]))).toBe(0);
  });
});
