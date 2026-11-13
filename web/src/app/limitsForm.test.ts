import { describe, expect, it } from 'vitest';
import { limitsBody, type Limits } from './limitsForm';

const base = {
  sender_allowlist: [],
  quiet_hours: [],
  repo_allowlist: [],
  write_deny: [],
  wall_clock_minutes: 60,
  quota_hold_percent: 90,
  skip: { labels: [], senders: [], dedupe_keys: [] },
};

const work: Limits = {
  ...base,
  ceilings: { t1: { level: 'A5', asks_first: true }, t2: { level: 'A1' }, t3: { level: 'A0' }, t4: { level: 'A3', restricted: 'investigate' }, fleet: { level: 'A5' } },
  rates: { launches_per_hour: 2, launches_per_sender_per_day: 1, concurrent: 2 },
  spend: { lookout_call_usd: 0.25, daily_worker_usd: 10, reviewer_call_usd: 0.1 },
};

const home: Limits = {
  ...base,
  ceilings: {
    t1: { level: 'A5' },
    t2: { level: 'A3', restricted: 'draft_fix' },
    t3: { level: 'A1' },
    t4: { level: 'A3', restricted: 'investigate', threshold: { events: 10, days: 14, level: 'A3', restricted: 'draft_fix' } },
    fleet: { level: 'A5' },
  },
  rates: { launches_per_hour: 4, launches_per_sender_per_day: 3, concurrent: 3 },
  spend: { lookout_call_usd: 0.25, daily_worker_usd: 5, reviewer_call_usd: 0.1 },
};

const withT4 = (l: Limits, t4: Limits['ceilings']['t4']): Limits => ({ ...l, ceilings: { ...l.ceilings, t4 } });

describe('limitsBody', () => {
  it('sends nothing when nothing changed', () => {
    expect(limitsBody(work, structuredClone(work))).toEqual({});
  });

  it('sends only the leaf that changed, so the other Limits keep their stored form', () => {
    const edited = { ...structuredClone(work), rates: { ...work.rates, concurrent: 3 } };
    expect(limitsBody(work, edited)).toEqual({ rates: { concurrent: 3 } });
    expect(limitsBody(work, { ...work, wall_clock_minutes: 90 })).toEqual({ wall_clock_minutes: 90 });
  });

  it('sends a changed list whole', () => {
    expect(limitsBody(work, { ...work, repo_allowlist: ['acme/api', 'acme/web'] })).toEqual({ repo_allowlist: ['acme/api', 'acme/web'] });
  });

  it('sends only the ceiling leaf that changed', () => {
    const edited = { ...structuredClone(work), ceilings: { ...work.ceilings, t1: { level: 'A5', asks_first: false } } } as Limits;
    expect(limitsBody(work, edited)).toEqual({ ceilings: { t1: { asks_first: false } } });
  });

  it('sends the restricted level with a ceiling that enters A3', () => {
    const edited = { ...structuredClone(work), ceilings: { ...work.ceilings, t2: { level: 'A3', restricted: 'draft_fix' } } } as Limits;
    expect(limitsBody(work, edited)).toEqual({ ceilings: { t2: { level: 'A3', restricted: 'draft_fix' } } });
  });

  it('sends null for each Limit reset to its preset, whole tiers and groups included', () => {
    expect(limitsBody(work, work, ['rates.concurrent', 'wall_clock_minutes', 'ceilings.t1', 'skip.labels'])).toEqual({
      rates: { concurrent: null },
      wall_clock_minutes: null,
      ceilings: { t1: null },
      skip: { labels: null },
    });
  });

  it('keeps the routine of a skip filter the person did not touch when another is added', () => {
    const shown = { ...work, skip: { ...work.skip, labels: [{ value: 'wip', routine: 'abcd1234abcd1234' }] } };
    const edited = { ...shown, skip: { ...shown.skip, labels: [...shown.skip.labels, { value: 'draft' }] } };
    expect(limitsBody(shown, edited)).toEqual({ skip: { labels: [{ value: 'wip', routine: 'abcd1234abcd1234' }, { value: 'draft' }] } });
  });

  it('sends nothing for skip filters the person left alone', () => {
    const shown = { ...work, skip: { ...work.skip, senders: [{ value: 'bot', routine: 'abcd1234abcd1234' }] } };
    expect(limitsBody(shown, { ...shown, wall_clock_minutes: 30 })).toEqual({ wall_clock_minutes: 30 });
  });

  describe('the T4 threshold rule', () => {
    it('sends a null threshold with a Home T4 ceiling raised to A4, which the preset threshold would refuse', () => {
      const edited = withT4(home, { level: 'A4', threshold: home.ceilings.t4.threshold });
      expect(limitsBody(home, edited)).toEqual({ ceilings: { t4: { level: 'A4', threshold: null } } });
    });

    it('sends a null threshold with a Home T4 ceiling raised to A3 draft_fix, the threshold level itself', () => {
      const edited = withT4(home, { level: 'A3', restricted: 'draft_fix', threshold: home.ceilings.t4.threshold });
      expect(limitsBody(home, edited)).toEqual({ ceilings: { t4: { restricted: 'draft_fix', threshold: null } } });
    });

    it('sends the threshold the person set above the new ceiling, not a null', () => {
      const edited = withT4(home, { level: 'A4', threshold: { events: 10, days: 14, level: 'A5' } });
      expect(limitsBody(home, edited)).toEqual({ ceilings: { t4: { level: 'A4', threshold: { events: 10, days: 14, level: 'A5' } } } });
    });

    it('sends no threshold key when the ceiling has none to clear', () => {
      expect(limitsBody(work, withT4(work, { level: 'A4' }))).toEqual({ ceilings: { t4: { level: 'A4' } } });
    });

    it('leaves the preset threshold in place while the ceiling stays below it', () => {
      const edited = withT4(home, { level: 'A1', threshold: home.ceilings.t4.threshold });
      expect(limitsBody(home, edited)).toEqual({ ceilings: { t4: { level: 'A1' } } });
    });

    it('sends null for the threshold when the person picks No threshold', () => {
      expect(limitsBody(home, withT4(home, { level: 'A3', restricted: 'investigate', threshold: null }))).toEqual({ ceilings: { t4: { threshold: null } } });
    });
  });
});
