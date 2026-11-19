import { describe, expect, it, vi } from 'vitest';
// router.ts reads the page's location when it loads.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/routines', search: '' });
  vi.stubGlobal('addEventListener', () => {});
});

import type { Dock, FleetCapabilities } from './api';
import {
  autopilotDocks,
  breakerTripped,
  checkoutScopeLine,
  errorField,
  formOfRoutine,
  holdFireBanner,
  holdFireNote,
  stopAllConfirm,
  stopAllLabel,
  blankForm,
  errorText,
  lastRunText,
  launchOnDock,
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
    expect(errorField('Dock is required')).toBe('dock');
    expect(errorField('Checkout is required')).toBe('checkout');
    expect(errorField('Text is required for a routine that runs on a schedule')).toBe('text');
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
    expect(errorField('invalid routine: Dock is required')).toBe('dock');
    expect(errorField('invalid routine: Checkout is required')).toBe('checkout');
    expect(errorField('invalid routine: Text is required for a routine that runs on a schedule')).toBe('text');
    expect(errorField('something else broke')).toBe('form');
  });

  it('puts the no-remote refusal on the Checkout field', () => {
    expect(errorField('invalid routine: Checkout has no remote, so a GitHub event routine on it never fires')).toBe('checkout');
  });
});

describe('the line that says which repos fire an event routine', () => {
  const github = { event: true, plugin: 'github' };
  it('names the one repo, without its host', () => {
    expect(checkoutScopeLine({ ...github, remotes: ['github.com/acme/api'] })).toBe('Fires only for events on acme/api.');
  });
  it('lists two repos', () => {
    expect(checkoutScopeLine({ ...github, remotes: ['github.com/me/api', 'github.com/acme/api'] })).toBe('Fires only for events on me/api and acme/api.');
  });
  it('collapses one owner/name on two hosts to a single repo', () => {
    expect(checkoutScopeLine({ ...github, remotes: ['github.com/acme/api', 'github-work/acme/api'] })).toBe('Fires only for events on acme/api.');
  });
  it('says the routine never fires when the checkout has no remote', () => {
    expect(checkoutScopeLine({ ...github, remotes: [] })).toMatch(/never fires.*no remote/i);
  });
  it('says nothing when the remotes are not known', () => {
    expect(checkoutScopeLine({ ...github, remotes: undefined })).toBeNull();
  });
  it('says nothing for a schedule routine or a plugin source', () => {
    expect(checkoutScopeLine({ event: false, plugin: 'github', remotes: ['github.com/acme/api'] })).toBeNull();
    expect(checkoutScopeLine({ event: true, plugin: 'tracker', remotes: ['github.com/acme/api'] })).toBeNull();
    expect(checkoutScopeLine({ event: true, plugin: undefined, remotes: [] })).toBeNull();
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

  it('sends a digest routine with its extra words and neither a launch nor a prompt', () => {
    const f = formOfRoutine({ ...stored, do: { digest: { extra: 'lead with PRs' } } });
    expect(f.mode).toBe('digest');
    expect(routineBody(f).do).toEqual({ digest: { extra: 'lead with PRs' } });
  });

  it('sends a digest with no extra words as an empty digest, which is still a digest', () => {
    const f = formOfRoutine({ ...stored, do: { digest: {} } });
    expect(f.mode).toBe('digest');
    expect(routineBody(f).do).toEqual({ digest: {} });
  });
});

describe('a digest in the run and error words', () => {
  it('says a queued digest run is a digest on its way, not a prompt sent', () => {
    const l = runLine(run({ outcome: 'queued' } as Partial<RoutineRun>));
    expect(l.text).toMatch(/digest/i);
    expect(l.text).not.toMatch(/prompt/i);
    expect(l.dot).not.toBe('bad');
    expect(l.text).toMatch(/queued/i);
  });

  it('says a digest queued from an accepted proposal came from it', () => {
    expect(runLine(run({ outcome: 'queued', via: 'proposal' } as Partial<RoutineRun>)).text).toMatch(/\(from an accepted proposal\)/);
  });

  it('says a fleet with nothing new was skipped for that reason', () => {
    expect(runLine(run({ outcome: 'skipped', reason: 'nothing new' }))).toMatchObject({ dot: 'idle', text: expect.stringContaining('nothing new') });
  });

  it('puts the digest refusals on the Do field, the field the server names', () => {
    expect(errorField('invalid routine: a digest runs on a schedule, never on a source event')).toBe('do');
    expect(errorField("invalid routine: the digest's extra words are longer than 1000 characters")).toBe('do');
    expect(errorField('invalid routine: do takes one of a launch, a prompt or a digest')).toBe('do');
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

describe('the Stop all worker-launched confirm', () => {
  it('names this fleet, and says Undo and Promote still work', () => {
    expect(stopAllConfirm('fleet')).toBe('Stops every ship an outside event started in this fleet. Their clones stay, so Undo and Promote still work.');
  });

  it('names every fleet for the global button', () => {
    expect(stopAllConfirm('all')).toBe('Stops every ship an outside event started in every fleet. Their clones stay, so Undo and Promote still work.');
  });
});
