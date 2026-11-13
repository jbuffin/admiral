import { describe, expect, it } from 'vitest';
import { canAddCheck, checkTooLong, guidanceTooLong, loosenedSentences, ordersBody, ordersErrorTarget, ordersMessage, runes, sentenceLink } from './standingOrders';

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

describe('sentenceLink', () => {
  it('links a sentence to its field on the fleet\'s Limits page', () => {
    expect(sentenceLink('work', 'ceilings.t1')).toBe('/app/preferences/work/limits?field=ceilings.t1');
  });

  it('gives a fixed sentence no link', () => {
    expect(sentenceLink('work', '')).toBeNull();
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
