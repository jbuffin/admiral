import { describe, expect, it } from 'vitest';
import { canAddCheck, checkTooLong, guidanceTooLong, loosenedSentences, ordersBody, ordersErrorTarget, ordersMessage, ordersPatch, judgeGap, judgeOff, NEEDS_JUDGE, runes, saveFailure } from './standingOrders';

describe('counters', () => {
  it('count characters the way the server does, not UTF-16 code units', () => {
    expect(runes('abc')).toBe(3);
    expect(runes('a😀b')).toBe(3);
  });

  it('turns Add off at 20 Checks', () => {
    expect(canAddCheck(0)).toBe(true);
    expect(canAddCheck(19)).toBe(true);
    expect(canAddCheck(20)).toBe(false);
  });

  it('allows a Check of 300 characters, emoji counted once each, and refuses 301', () => {
    expect(checkTooLong('😀'.repeat(300))).toBe(false);
    expect(checkTooLong('x'.repeat(301))).toBe(true);
  });

  it('allows Guidance of 2000 characters and refuses 2001', () => {
    expect(guidanceTooLong('😀'.repeat(2000))).toBe(false);
    expect(guidanceTooLong('x'.repeat(2001))).toBe(true);
  });
});

describe('ordersBody', () => {
  const shown = { checks: ['Run make check.'], guidance: 'Small PRs.' };

  it('sends nothing when nothing changed', () => {
    expect(ordersBody(shown, { ...shown })).toEqual({});
  });

  it('sends only the Checks when only they changed, so Guidance stays as stored', () => {
    expect(ordersBody(shown, { ...shown, checks: ['Run make check.', 'Ask first.'] })).toEqual({ checks: ['Run make check.', 'Ask first.'] });
  });

  it('sends only the Guidance when only it changed', () => {
    expect(ordersBody(shown, { ...shown, guidance: '' })).toEqual({ guidance: '' });
  });
});

describe('ordersErrorTarget', () => {
  it('names the Check an indexed error points at', () => {
    expect(ordersErrorTarget('invalid standing orders: checks[2]: longer than 300 characters')).toEqual({ kind: 'check', index: 2 });
    expect(ordersErrorTarget('invalid standing orders: checks[0]: listed twice')).toEqual({ kind: 'check', index: 0 });
  });

  it('names the whole Checks list when the error has no index', () => {
    expect(ordersErrorTarget('invalid standing orders: checks: at most 20 Checks, got 21')).toEqual({ kind: 'checks' });
  });

  it('names the Guidance', () => {
    expect(ordersErrorTarget('invalid standing orders: guidance: longer than 2000 characters')).toEqual({ kind: 'guidance' });
  });

  it('sends any other error to the top of the tab', () => {
    expect(ordersErrorTarget('malformed JSON body')).toBeNull();
  });

  it('drops the prefix the field already shows', () => {
    expect(ordersMessage('invalid standing orders: checks[2]: longer than 300 characters')).toBe('longer than 300 characters');
    expect(ordersMessage('invalid standing orders: checks: at most 20 Checks, got 21')).toBe('at most 20 Checks, got 21');
  });
});

describe('loosenedSentences', () => {
  const sentences = [
    { field: 'ceilings.t1', sentence: 'A routine event asks first.' },
    { field: 'wall_clock_minutes', sentence: 'A worker ship stops after 60 minutes.' },
    { field: '', sentence: 'A restricted ship never pushes.' },
  ];

  it("names the target's own sentence for each loosened field, in the order given", () => {
    expect(loosenedSentences(['wall_clock_minutes', 'ceilings.t1'], sentences)).toEqual(['A worker ship stops after 60 minutes.', 'A routine event asks first.']);
  });

  it('names nothing when nothing was loosened, and skips a field the target has no sentence for', () => {
    expect(loosenedSentences([], sentences)).toEqual([]);
    expect(loosenedSentences(['rates.concurrent', 'ceilings.t1'], sentences)).toEqual(['A routine event asks first.']);
  });
});

describe('an error past the shown rows', () => {
  it('lands on the New Check field when Add made a duplicate at the next index', () => {
    expect(ordersErrorTarget('invalid standing orders: checks[2]: listed twice', 2)).toEqual({ kind: 'new' });
    expect(ordersErrorTarget('invalid standing orders: checks[1]: listed twice', 2)).toEqual({ kind: 'check', index: 1 });
  });
});

describe('ordersPatch', () => {
  const shown = { checks: ['a'], guidance: 'old' };
  it('sends only the Checks when a Check action runs, whatever the Guidance draft holds', () => {
    expect(ordersPatch(shown, { checks: ['a', 'b'], guidance: 'half written' }, 'checks')).toEqual({ checks: ['a', 'b'] });
  });
  it('sends only the Guidance when its Save runs, whatever the Checks draft holds', () => {
    expect(ordersPatch(shown, { checks: ['edited'], guidance: 'new' }, 'guidance')).toEqual({ guidance: 'new' });
  });
});

describe('the judge', () => {
  const on = { key: 'w', name: 'W', harnesses: [], judge: { harness: 'claude' } };
  const off = { key: 'w', name: 'W', harnesses: [], judge: { harness: null, reason: 'none' } };
  it('is off only when the fleet reports no judge harness', () => {
    expect(judgeOff(on)).toBe(false);
    expect(judgeOff(off)).toBe(true);
    expect(judgeOff(undefined)).toBe(false);
  });
  it('lists the features that need it once', () => {
    expect(NEEDS_JUDGE.join(' ')).toMatch(/Lookout/);
    expect(NEEDS_JUDGE.join(' ')).toMatch(/reviewer/);
    expect(NEEDS_JUDGE.join(' ')).toMatch(/drafter/);
    expect(NEEDS_JUDGE.join(' ')).toMatch(/advisor/);
  });
  it('names the judge capability a harness failed or has not been checked on', () => {
    const h = (status: 'failed' | 'not_checked') => ({ harness: 'codex', version: '', detected_at: null, features: [{ id: 'judge', label: 'Judge calls', capabilities: [{ id: 'a', label: 'No tools', status: 'passed' as const, checked_at: null, detail: '' }, { id: 'b', label: 'No network', status, checked_at: null, detail: '' }] }] });
    expect(judgeGap(h('failed'))).toEqual({ capability: 'No network', status: 'failed' });
    expect(judgeGap(h('not_checked'))).toEqual({ capability: 'No network', status: 'not_checked' });
    expect(judgeGap(undefined)).toBeNull();
  });
});

describe('saveFailure', () => {
  it("says Admiral couldn't be reached when the request never got an answer", () => {
    expect(saveFailure({ error: 'Load failed' })).toBe("Couldn't reach Admiral. Your changes aren't saved yet.");
  });
  it("keeps the server's own words when it answered", () => {
    expect(saveFailure({ error: 'invalid standing orders: guidance: too long', status: 400 })).toBe('invalid standing orders: guidance: too long');
  });
});
