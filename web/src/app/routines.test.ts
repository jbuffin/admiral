import { describe, expect, it, vi } from 'vitest';
// router.ts reads the page's location when it loads.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/routines', search: '' });
  vi.stubGlobal('addEventListener', () => {});
});

import type { Dock, FleetCapabilities } from './api';
import { automateSearch } from './router';
import {
  autopilotDocks,
  automateDraft,
  breakerTripped,
  errorField,
  formOfRoutine,
  holdFireBanner,
  holdFireNote,
  stopAllLabel,
  blankForm,
  errorText,
  lastRunText,
  launchOnDock,
  noticeEntryLinks,
  runTime,
  routineBody,
  runLine,
  type HoldFireState,
  type Routine,
  type RoutineRun,
} from './routines';

const run = (o: Partial<RoutineRun>): RoutineRun => ({ id: 't1', at: '2026-11-12T08:30:00Z', routine: 'r1', outcome: 'launched', ...o });

describe('Recent runs say each outcome in words', () => {
  it('names the ship a launch started and links to it', () => {
    const l = runLine(run({ outcome: 'launched', ship: 'dory' }));
    expect(l.text).toContain('dory');
    expect(l.ship).toBe('dory');
    expect(l.dot).toBe('ok');
  });

  it('says a prompt run sent the prompt, with no ship to link', () => {
    const l = runLine(run({ outcome: 'launched' }));
    expect(l.text).toMatch(/AdmiralBot prompt sent/i);
    expect(l.ship).toBeUndefined();
  });

  it('says a launch came from an accepted proposal', () => {
    const l = runLine(run({ outcome: 'launched', ship: 'dory', via: 'proposal' }));
    expect(l.text).toMatch(/proposal/i);
    expect(l.ship).toBe('dory');
  });

  it('gives the reason of a skipped, missed or failed run', () => {
    expect(runLine(run({ outcome: 'skipped', reason: 'a ship already holds this routine' }))).toMatchObject({ dot: 'idle', text: expect.stringContaining('a ship already holds this routine') });
    expect(runLine(run({ outcome: 'missed', reason: 'Admiral was off' }))).toMatchObject({ dot: 'idle', text: expect.stringContaining('Admiral was off') });
    expect(runLine(run({ outcome: 'failed', reason: 'no room' }))).toMatchObject({ dot: 'bad', text: expect.stringContaining('no room') });
  });

  it('says a Report only run is what it would have launched', () => {
    const l = runLine(run({ outcome: 'reported', action: 'launch claude in /repo' }));
    expect(l.text).toContain('launch claude in /repo');
  });

  it('says a proposal is waiting in the Inbox', () => {
    expect(runLine(run({ outcome: 'proposed' })).text).toMatch(/Inbox/i);
  });

  it('says the breaker moved the routine to Ask first', () => {
    const l = runLine(run({ outcome: 'breaker' }));
    expect(l.text).toMatch(/Ask first/i);
    expect(l.dot).toBe('bad');
  });

  it('says a launch that was undone', () => {
    expect(runLine(run({ outcome: 'launched', ship: 'dory', undone: true })).text).toMatch(/undone/i);
  });
});

describe('the breaker reload', () => {
  const runs = (...ids: [string, RoutineRun['outcome']][]) => ids.map(([id, outcome]) => run({ id, outcome }));

  it('reloads the routine when a breaker record is newer than the last run the screen showed', () => {
    const fresh = runs(['t5', 'breaker'], ['t4', 'failed'], ['t3', 'failed']);
    expect(breakerTripped(fresh, 't4')).toBe(true);
    expect(breakerTripped(fresh, 't5')).toBe(false);
    expect(breakerTripped(runs(['t5', 'launched'], ['t4', 'failed']), 't4')).toBe(false);
    expect(breakerTripped(fresh, null)).toBe(false);
  });
});

describe('a server 400 shows by the field it is about', () => {
  it('maps the server messages to name, when, do or the whole form', () => {
    expect(errorField('name is required')).toBe('name');
    expect(errorField('name is longer than 80 characters')).toBe('name');
    expect(errorField('when: the cron expression "0 0 30 2 *" never fires')).toBe('when');
    expect(errorField('do.launch needs a harness, a dir and a prompt text')).toBe('do');
    expect(errorField('the prompt is empty')).toBe('do');
    expect(errorField('autopilot is not proven on claude in fleet work: its guard has not passed')).toBe('do');
    expect(errorField('args: --dangerous is not allowed')).toBe('do');
    expect(errorField('something else broke')).toBe('form');
    expect(errorField('before_acting must be report_only, ask_first or act')).toBe('before_acting');
  });

  it('reads the field through the prefix the server puts on every validation error', () => {
    const real = 'invalid routine: when: the cron expression "0 0 30 2 *" never fires';
    expect(errorField(real)).toBe('when');
    expect(errorText(real)).toBe('when: the cron expression "0 0 30 2 *" never fires');
    expect(errorField('invalid routine: name is required')).toBe('name');
    expect(errorField('invalid routine: the prompt is empty')).toBe('do');
    expect(errorField('something else broke')).toBe('form');
  });
});

describe('the docks a routine may launch on', () => {
  const docks: Dock[] = [
    { harness: 'claude', executable: 'claude', executable_found: true, warnings: [] },
    { harness: 'codex', executable: 'codex', executable_found: true, warnings: [] },
  ];
  const harness = (name: string, status: 'passed' | 'failed' | 'not_checked') => ({
    harness: name,
    version: '1',
    detected_at: null,
    features: [{ id: 'autopilot', label: 'Autopilot', capabilities: [{ id: 'autopilot.guard_hook', label: 'Guard hook', status, checked_at: null, detail: '' }] }],
  });
  const caps = (judge: string | null, ...hs: ReturnType<typeof harness>[]): FleetCapabilities => ({ key: 'work', name: 'Work', harnesses: hs, judge: judge ? { harness: judge } : { harness: null, reason: 'no harness has a strong model' } });

  it('offers a dock whose guard passed when the fleet has a judge', () => {
    expect(autopilotDocks(caps('claude', harness('claude', 'passed'), harness('codex', 'not_checked')), docks).harnesses).toEqual(['claude']);
    expect(autopilotDocks(undefined, docks).harnesses).toEqual([]); // nothing is known before the capabilities load
  });

  it('offers none without a judge, and says why', () => {
    const a = autopilotDocks(caps(null, harness('claude', 'passed')), docks);
    expect(a.harnesses).toEqual([]);
    expect(a.why).toContain('no harness has a strong model');
  });

  it('offers none when no guard passed, and says so', () => {
    const a = autopilotDocks(caps('claude', harness('claude', 'failed')), docks);
    expect(a.harnesses).toEqual([]);
    expect(a.why).not.toBe('');
  });
});

describe('the Hold fire banner', () => {
  const now = new Date('2026-11-12T08:05:00Z');
  const off: HoldFireState = { on: false, fleet: { on: false }, global: { on: false } };

  it('shows nothing while neither switch is on', () => {
    expect(holdFireBanner(off, 'Work', now)).toBeNull();
    expect(holdFireBanner(null, 'Work', now)).toBeNull();
  });

  it('names AdmiralBot as who turned this fleet on, and when', () => {
    const b = holdFireBanner({ on: true, fleet: { on: true, by: 'admiralbot', at: '2026-11-12T08:00:00Z' }, global: { on: false } }, 'Work', now);
    expect(b).not.toBeNull();
    expect(b!.scope).toBe('fleet');
    expect(b!.by).toBe('admiralbot');
    expect(b!.at).toBe('2026-11-12T08:00:00Z');
    expect(b!.text).toContain('AdmiralBot');
    expect(b!.text).toContain('Work');
  });

  it('says you for the engineer, and all fleets for the global switch', () => {
    const b = holdFireBanner({ on: true, fleet: { on: false }, global: { on: true, by: 'engineer', at: '2026-11-12T08:00:00Z' } }, 'Work', now);
    expect(b).not.toBeNull();
    expect(b!.scope).toBe('all');
    expect(b!.text).toMatch(/\byou\b/i);
    expect(b!.text).toMatch(/all fleets/i);
  });

  it('tells the editor that Hold fire makes an Act routine ask first, and nothing else', () => {
    expect(holdFireNote('act', true)).toMatch(/ask first/i);
    expect(holdFireNote('act', false)).toBeNull();
    expect(holdFireNote('report_only', true)).toBeNull();
    expect(holdFireNote('ask_first', true)).toBeNull();
  });
});

describe('Automate… drafts a scheduled routine for a view', () => {
  const view = { sourceKey: 'gh', sourceName: 'GitHub', viewTitle: 'Review requested' };

  it('is a weekday-morning launch of Commodore that asks first, and is not saved', () => {
    const f = automateDraft(view, { harness: 'claude', dir: '/repo', commodore: true });
    expect(f.name).toBe('Automate Review requested');
    expect(f.when).toBe('30 8 * * 1-5');
    expect(f.before).toBe('ask_first');
    expect(f.mode).toBe('launch');
    expect(f.launch.harness).toBe('claude');
    expect(f.launch.skill).toEqual({ kind: 'commodore' });
    expect(f.launch.text).toContain('GitHub');
    expect(f.launch.text).toContain('Review requested');
    expect(automateDraft(view, { harness: 'claude', dir: '/repo', commodore: false }).launch.skill).toEqual({ kind: 'none' });
  });

  it('is an AdmiralBot prompt when no dock qualifies', () => {
    const f = automateDraft(view, { harness: null, dir: '', commodore: false });
    expect(f.mode).toBe('prompt');
    expect(f.prompt).toContain('Review requested');
  });
});

describe('the form and the request body', () => {
  const stored: Routine = {
    id: 'r1',
    name: 'Nightly sweep',
    enabled: true,
    when: '30 8 * * 1-5',
    do: { launch: { harness: 'claude', dir: '/repo', text: 'sweep the board', model: 'sonnet', effort: 'high', args: ['--effort', 'high'], skill: { kind: 'commodore' }, resources: ['db'] } },
    before_acting: 'act',
    notify: true,
  };

  it('sends a launch routine the way the server stores it, args split into a list', () => {
    expect(routineBody(formOfRoutine(stored))).toEqual({
      name: 'Nightly sweep',
      enabled: true,
      when: '30 8 * * 1-5',
      do: stored.do,
      before_acting: 'act',
      notify: true,
    });
  });

  it('leaves out what the launch left empty', () => {
    const body = routineBody({ ...formOfRoutine(stored), launch: { harness: 'claude', dir: '/repo', text: 'go', model: '', effort: '', args: '', skill: { kind: 'none' }, resources: [] } }) as { do: { launch: Record<string, unknown> } };
    expect(Object.keys(body.do.launch).sort()).toEqual(['dir', 'harness', 'text']);
  });

  it('sends a prompt routine with a prompt and no launch', () => {
    const f = formOfRoutine({ ...stored, do: { prompt: 'summarise the night' } });
    expect(f.mode).toBe('prompt');
    expect(routineBody(f)).toMatchObject({ do: { prompt: 'summarise the night' } });
    expect((routineBody(f).do as Record<string, unknown>).launch).toBeUndefined();
  });
});

describe('the migration notice links to Automate…', () => {
  it('has one link for each migrated entry, naming the source and opening the editor for its view', () => {
    const links = noticeEntryLinks({ body: 'x', entries: [{ key: 'gh', name: 'GitHub', panel: 'mine' }, { key: 'notes', name: 'Notes', panel: 'orders' }] });
    expect(links).toHaveLength(2);
    expect(links[0].label).toContain('GitHub');
    expect(links[0].path.startsWith('/app/routines/new')).toBe(true);
    expect(links[0].path.endsWith(automateSearch('gh', 'mine'))).toBe(true);
    expect(links[1].label).toContain('Notes');
  });

  it('has none on a plain note', () => {
    expect(noticeEntryLinks({ body: 'plain' })).toEqual([]);
    expect(noticeEntryLinks(undefined)).toEqual([]);
  });
});

describe('the list row and the run time', () => {
  const now = new Date(2026, 10, 12, 10, 0);
  it('says how long ago a routine ran, or Never', () => {
    expect(lastRunText(undefined, now)).toBe('Never');
    expect(lastRunText({ at: new Date(2026, 10, 12, 8, 0).toISOString(), outcome: 'launched' }, now)).toBe('2 h ago');
  });
  it('writes a run time as the canvas does', () => {
    expect(runTime(new Date(2026, 10, 12, 9, 38).toISOString(), now)).toBe('Today 09:38');
    expect(runTime(new Date(2026, 10, 11, 17, 2).toISOString(), now)).toBe('Yesterday 17:02');
    expect(runTime(new Date(2026, 10, 9, 8, 30).toISOString(), now)).toBe('Mon 08:30');
    expect(runTime(new Date(2026, 9, 3, 8, 30).toISOString(), now)).toBe('Oct 3');
  });
  it('drops a model and an effort the new dock does not offer', () => {
    const l = { ...blankForm().launch, harness: 'claude', dir: '/p', model: 'opus', effort: 'max' };
    const moved = launchOnDock(l, 'codex');
    expect(moved).toMatchObject({ harness: 'codex', dir: '', model: '' });
    expect(moved.effort).toBe('');
    expect(launchOnDock({ ...l, effort: 'high' }, 'claude').effort).toBe('high');
  });
  it('carries the entry\'s fleet in a notice link', () => {
    const [l] = noticeEntryLinks({ entries: [{ key: 'gh', name: 'GitHub', panel: 'mine', fleet: 'work' }] });
    expect(l.path).toContain('fleet=work');
  });
});

describe('the Stop all worker-launched button', () => {
  it('shows the fleet count and is live while ships run', () => {
    expect(stopAllLabel(2, 'fleet')).toEqual({ label: 'Stop all worker-launched (2)', disabled: false });
  });

  it('says all fleets for the global button', () => {
    expect(stopAllLabel(3, 'all')).toEqual({ label: 'Stop all worker-launched, all fleets (3)', disabled: false });
  });

  it('is disabled with nothing to stop, and while the count is not known yet', () => {
    expect(stopAllLabel(0, 'fleet')).toEqual({ label: 'Stop all worker-launched (0)', disabled: true });
    expect(stopAllLabel(undefined, 'fleet')).toEqual({ label: 'Stop all worker-launched', disabled: true });
  });
});
