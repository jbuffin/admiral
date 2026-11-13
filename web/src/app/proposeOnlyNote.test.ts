import { describe, expect, it, vi } from 'vitest';
// router.ts reads the page's location when it loads.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/routines', search: '' });
  vi.stubGlobal('addEventListener', () => {});
});

import type { FleetCapabilities } from './api';
import type { Limits } from './limitsForm';
import { proposeOnlyNote } from './routines';

const limits = (t1: string, t4 = 'A1'): Limits =>
  ({
    sender_allowlist: [],
    quiet_hours: [],
    repo_allowlist: [],
    write_deny: [],
    wall_clock_minutes: 60,
    quota_hold_percent: 90,
    skip: { labels: [], senders: [], dedupe_keys: [] },
    rates: { concurrent: 2, launches_per_hour: 4 },
    ceilings: { t1: { level: t1 }, t2: { level: 'A1' }, t3: { level: 'A0' }, t4: { level: t4 }, fleet: { level: 'A1' } },
  }) as unknown as Limits;

const caps = (restricted: boolean): FleetCapabilities => ({ key: 'work', name: 'Work', harnesses: [], restricted });

describe('The Routines screen says when A3 routines run as propose only', () => {
  it('shows a note when a tier ceiling is A3 and no harness is restricted-capable', () => {
    expect(proposeOnlyNote(caps(false), limits('A3'))).toMatch(/propose only/i);
  });

  it('shows it for an A3 ceiling on any tier', () => {
    expect(proposeOnlyNote(caps(false), limits('A1', 'A3'))).toMatch(/propose only/i);
  });

  it('is hidden once a harness is restricted-capable', () => {
    expect(proposeOnlyNote(caps(true), limits('A3'))).toBe('');
  });

  it('is hidden when no tier ceiling is A3', () => {
    expect(proposeOnlyNote(caps(false), limits('A1'))).toBe('');
  });

  it('waits for the capabilities and the limits before saying anything', () => {
    expect(proposeOnlyNote(undefined, limits('A3'))).toBe('');
    expect(proposeOnlyNote(caps(false), undefined)).toBe('');
  });
});
