import { describe, expect, it, vi } from 'vitest';
// router.ts reads the page's location when it loads.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/routines', search: '' });
  vi.stubGlobal('addEventListener', () => {});
});

import { blankForm, errorField, eventHref, formOfRoutine, routineBody, runCost, runLine, spendNotice, type Routine, type RoutineRun } from './routines';

const on = { source: 'gh', kind: 'review_requested' };
const eventRoutine: Routine = {
  id: 'r1',
  name: 'Review watch',
  enabled: true,
  when: '',
  on,
  do: { prompt: 'look at it' },
  before_acting: 'report_only',
  notify: false,
};
const run = (over: Partial<RoutineRun>): RoutineRun => ({ id: 't1', at: '2026-11-12T09:00:00Z', routine: 'r1', outcome: 'skipped', ...over });

describe('an event routine through the editor form', () => {
  it('keeps its event from the stored routine into the save body, with no when', () => {
    const body = routineBody(formOfRoutine(eventRoutine));
    expect(body.on).toEqual(on);
    expect(body).not.toHaveProperty('when');
  });

  it('keeps a panel the kind takes', () => {
    const withPanel = { ...eventRoutine, on: { source: 'gh', kind: 'new_in_view', panel: 'bugs' } };
    expect(routineBody(formOfRoutine(withPanel)).on).toEqual({ source: 'gh', kind: 'new_in_view', panel: 'bugs' });
  });

  it('saves a schedule with its when and no on', () => {
    const body = routineBody({ ...blankForm(), name: 'Nightly', when: '30 8 * * 1-5' });
    expect(body.when).toBe('30 8 * * 1-5');
    expect(body).not.toHaveProperty('on');
  });

  it('puts the when refusals on the When field and the ship refusal on Do', () => {
    for (const m of [
      'invalid routine: on needs a source and a kind',
      'invalid routine: on: the fleet has no source "gh" that raises events',
      'invalid routine: on: the source "gh" lists no event kind "x"',
      'invalid routine: an event routine has no when: it runs on the event',
    ]) {
      expect(errorField(m), m).toBe('when');
    }
    expect(errorField('invalid routine: an event routine launches a ship; a source event never prompts AdmiralBot')).toBe('do');
  });
});

describe('Recent runs for an event routine', () => {
  it('words each event reason, never the raw code', () => {
    for (const code of ['skip_filter', 'repo_not_allowed', 'top_action', 'daily_spend_reached', 'daily_spend_unknown', 'quiet_hours', 'hold_fire', 'dedupe_key_held', 'lookout_timeout']) {
      const text = runLine(run({ outcome: 'skipped', reason: code })).text;
      expect(text, code).not.toContain(code);
      expect(text, code).toMatch(/\w+ \w+/);
    }
  });

  it('words a list of codes one by one', () => {
    const text = runLine(run({ outcome: 'reported', reason: 'daily_spend_reached, quiet_hours', action: 'a card' })).text;
    expect(text).not.toContain('daily_spend_reached');
    expect(text).not.toContain('quiet_hours');
  });

  it('keeps a code it does not know as it is', () => {
    expect(runLine(run({ outcome: 'skipped', reason: 'weather_is_bad' })).text).toContain('weather_is_bad');
  });

  it('says the cost of a record that has one, and marks an estimate', () => {
    expect(runCost(run({ costs: [{ call: 'lookout', usd: 0.0123 }, { call: 'reviewer', usd: 0.02 }] }))).toContain('0.03');
    const estimated = runCost(run({ costs: [{ call: 'lookout_estimated', usd: 0.05 }] }));
    expect(estimated).toContain('0.05');
    expect(estimated).toMatch(/estimated/i);
    expect(runCost(run({ costs: [{ call: 'lookout', usd: 0.05 }] }))).not.toMatch(/estimated/i);
  });

  it('shows no cost without costs', () => {
    expect(runCost(run({}))).toBe('');
    expect(runCost(run({ costs: [] }))).toBe('');
  });

  it('links an event only when it is https', () => {
    expect(eventHref('https://github.com/acme/api/pull/7')).toBe('https://github.com/acme/api/pull/7');
    expect(eventHref('http://github.com/acme/api/pull/7')).toBeNull();
    expect(eventHref('javascript:alert(1)')).toBeNull();
    expect(eventHref(undefined)).toBeNull();
  });
});

describe('the spend-reached notice', () => {
  it('is silent until the cap is reached', () => {
    expect(spendNotice(undefined)).toBeNull();
    expect(spendNotice({ today_usd: 1.5, cap_usd: 2, reached: false })).toBeNull();
  });

  it('says events stop at a proposal, with today and the cap', () => {
    const text = spendNotice({ today_usd: 2, cap_usd: 2, reached: true }) ?? '';
    expect(text).toContain('$2.00');
    expect(text).toMatch(/proposal|A1/);
  });
});
