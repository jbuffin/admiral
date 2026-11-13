import { describe, expect, it } from 'vitest';
import type { CrewBoat } from './api';
import { boatBoxState, boatRoutes, chipLabel, chipState, flotilla } from './flotilla';

const boat = (b: Partial<CrewBoat> & { name: string }): CrewBoat => ({ conversation: false, harness: 'claude', status: 'working', exit_code: null, exit_signal: null, ...b });

describe('chipLabel', () => {
  it('shows the role, and Teammate for a boat with none', () => {
    expect(chipLabel(boat({ name: 'rev', role: 'reviewer' }))).toBe('reviewer');
    expect(chipLabel(boat({ name: 'mate' }))).toBe('Teammate');
  });
});

describe('chipState', () => {
  it('reads a boat at work, idle or asking as running', () => {
    for (const status of ['working', 'idle', 'needs_you']) expect(chipState(boat({ name: 'b', status }))).toBe('running');
  });

  it('reads an anchored boat that exited 0 as finished, not as an error', () => {
    expect(chipState(boat({ name: 'b', status: 'anchored', exit_code: 0 }))).toBe('finished');
  });

  it('reads an anchored boat with another exit code, or killed by a signal, as an error', () => {
    expect(chipState(boat({ name: 'b', status: 'anchored', exit_code: 3 }))).toBe('error');
    expect(chipState(boat({ name: 'b', status: 'anchored', exit_signal: 'KILL' }))).toBe('error');
  });
});

describe('flotilla', () => {
  it('puts the ship first and one chip per boat after it, in the boats\' order', () => {
    const chips = flotilla({ name: 'Saltpaw', crew: [boat({ name: 'rev', role: 'reviewer' }), boat({ name: 'mate', status: 'anchored', exit_code: 0 })] });
    expect(chips.map((c) => [c.crew, c.label, c.state])).toEqual([
      [null, 'Saltpaw', 'ship'],
      ['rev', 'reviewer', 'running'],
      ['mate', 'Teammate', 'finished'],
    ]);
  });

  it('is the ship alone when it has no crew', () => {
    expect(flotilla({ name: 'Saltpaw', crew: [] }).map((c) => c.crew)).toEqual([null]);
  });
});

describe('boatBoxState', () => {
  it('takes a message while the boat runs', () => {
    expect(boatBoxState(boat({ name: 'b' })).enabled).toBe(true);
    expect(boatBoxState(boat({ name: 'b', status: 'idle' })).enabled).toBe(true);
  });

  it('is read only once the boat has exited, and says finished for exit 0 and error otherwise', () => {
    const done = boatBoxState(boat({ name: 'b', status: 'anchored', exit_code: 0 }));
    expect(done.enabled).toBe(false);
    expect(done.note).toMatch(/finished/i);
    expect(done.note).not.toMatch(/error/i);
    const failed = boatBoxState(boat({ name: 'b', status: 'anchored', exit_code: 2 }));
    expect(failed.enabled).toBe(false);
    expect(failed.note).toMatch(/error/i);
  });
});

describe('boatRoutes', () => {
  it('points a boat at its own crew routes and the ship at the ship\'s', () => {
    expect(boatRoutes('Salt paw', 'rev')).toEqual({
      conversation: '/api/v1/ships/Salt%20paw/crew/rev/conversation',
      bridge: '/ws/v1/bridge/Salt%20paw/crew/rev',
      messages: '/api/v1/ships/Salt%20paw/crew/rev/messages',
      interrupt: '/api/v1/ships/Salt%20paw/crew/rev/interrupt',
      skills: '/api/v1/ships/Salt%20paw/crew/rev/skills',
    });
    expect(boatRoutes('Salt paw')).toEqual({
      conversation: '/api/v1/ships/Salt%20paw/conversation',
      bridge: '/ws/v1/bridge/Salt%20paw',
      messages: '/api/v1/ships/Salt%20paw/messages',
      interrupt: '/api/v1/ships/Salt%20paw/interrupt',
      skills: '/api/v1/ships/Salt%20paw/skills',
    });
  });
});
