import { describe, expect, it } from 'vitest';
import { describeCron } from './cron';

const text = (expr: string, zone?: string, now?: Date) => {
  const d = describeCron(expr, zone, now);
  if (!d.ok) throw new Error(`${expr} read as invalid: ${d.reason}`);
  return d.text;
};
const reason = (expr: string, now?: Date) => {
  const d = describeCron(expr, undefined, now);
  if (d.ok) throw new Error(`${expr} read as valid: ${d.text}`);
  return d.reason;
};

describe('common schedules in plain English', () => {
  it.each([
    ['* * * * *', 'Every minute'],
    ['*/15 * * * *', 'Every 15 minutes'],
    ['0 * * * *', 'Every hour'],
    ['0 9 * * *', 'At 09:00'],
    ['0 9 * * 1-5', 'At 09:00, Monday through Friday'],
    ['0 9 * * 1', 'At 09:00, only on Monday'],
    ['0 9 1 * *', 'At 09:00, on day 1 of the month'],
    ['0 9 1 1 *', 'At 09:00, on day 1 of the month, only in January'],
    ['0 9-17 * * 1-5', 'Every hour, between 09:00 and 17:00, Monday through Friday'],
    ['0 9 * 1-3 *', 'At 09:00, January through March'],
  ])('%s reads %s', (expr, sentence) => {
    expect(text(expr)).toBe(sentence);
  });

  it('names the server zone when it is known, and nothing when it is not', () => {
    expect(text('0 9 * * 1-5', 'America/Denver')).toBe('At 09:00, Monday through Friday (America/Denver)');
    expect(text('0 9 * * 1-5', undefined)).toBe('At 09:00, Monday through Friday');
    expect(text('0 9 * * 1-5', '')).toBe('At 09:00, Monday through Friday');
  });
});

describe('the dialect Admiral runs', () => {
  it('reads month and weekday names in any case as their numbers', () => {
    expect(text('0 9 * * mon-fri')).toBe(text('0 9 * * 1-5'));
    expect(text('0 9 1 JAN *')).toBe(text('0 9 1 1 *'));
    expect(text('0 9 * * SuN')).toBe(text('0 9 * * 0'));
  });

  it('reads 7 and 0 both as Sunday', () => {
    expect(text('0 0 * * 7')).toBe(text('0 0 * * 0'));
    expect(text('0 0 * * 7')).toContain('Sunday');
  });

  it('keeps Sunday in a range that ends on 7, without turning it into a backwards range', () => {
    const t = text('0 9 * * 5-7');
    expect(t).toContain('Friday');
    expect(t).toContain('Sunday');
  });

  it('runs a day-of-week step from a number to 7, but a star step only to 6', () => {
    const from = text('0 9 * * 1/6');
    expect(from).toContain('Monday');
    expect(from).toContain('Sunday');
    const star = text('0 9 * * */6');
    expect(star).toContain('Saturday');
    expect(star).toContain('Sunday');
    expect(star).not.toContain('Monday');
  });

  it('says either day field matches when both are restricted', () => {
    const t = text('0 9 1 * 1');
    expect(t).toContain('day 1 of the month');
    expect(t).toContain('and on Monday');
  });

  it('says both must match when a day field starts with a star', () => {
    const t = text('0 9 */30 * 1');
    expect(t).toContain('every 30 days');
    expect(t).toContain('Monday');
    expect(t).not.toContain('and on Monday');
  });

  it('accepts a step with a leading plus and values with leading zeros, as Go does', () => {
    expect(text('*/+2 * * * *')).toBe('Every 2 minutes');
    expect(text('0 007 * * *')).toBe('At 07:00');
  });

  it('splits fields on any whitespace, tabs and runs of spaces included', () => {
    expect(text('  0\t9   * *\t1-5 ')).toBe('At 09:00, Monday through Friday');
  });
});

describe('a schedule that never fires', () => {
  const now = new Date('2026-11-24T12:00:00Z');

  it.each(['0 0 30 2 *', '0 0 31 4,6,9,11 *'])('%s never fires', (expr) => {
    expect(reason(expr, now)).toBe('This schedule never fires: no day in the next five years matches.');
  });

  it('fires on 29 February while a leap day is inside the window', () => {
    expect(text('0 0 29 2 *', undefined, now)).toContain('day 29 of the month');
  });

  it('judges by the window from the injected now: a leap day five years off is out of reach', () => {
    expect(reason('0 0 29 2 *', new Date('2097-01-01T00:00:00Z'))).toContain('never fires');
    expect(text('0 0 29 2 *', undefined, new Date('2099-03-01T00:00:00Z'))).toContain('day 29');
  });

  it('simulates an AND of a step day and a weekday: Monday 1 February 2027 is the only hit', () => {
    expect(reason('0 0 */30 2 1', new Date('2022-01-01T00:00:00Z'))).toContain('never fires');
    expect(text('0 0 */30 2 1', undefined, new Date('2022-06-01T00:00:00Z'))).toContain('Monday');
  });
});

describe('invalid input says what is wrong in plain English', () => {
  const wrong = [
    ['', 'When is empty: write five fields, minute hour day-of-month month day-of-week.'],
    ['0 9 * *', 'When needs 5 fields (minute hour day-of-month month day-of-week) but has 4.'],
    ['0 0 * * * *', 'When needs 5 fields (minute hour day-of-month month day-of-week) but has 6.'],
    ['@daily', 'When needs 5 fields (minute hour day-of-month month day-of-week) but has 1.'],
    ['0 25 * * *', 'hour: 25 is outside 0-23'],
    ['60 * * * *', 'minute: 60 is outside 0-59'],
    ['0 0 0 * *', 'day of month: 0 is outside 1-31'],
    ['0 0 * 13 *', 'month: 13 is outside 1-12'],
    ['0 0 * * 8', 'day of week: 8 is outside 0-7'],
    ['*/0 * * * *', 'minute: 0 is not a valid step, use a whole number of 1 or more'],
    ['5-1 * * * *', 'minute: 5-1 runs backwards'],
    ['x * * * *', 'minute: x is not a number'],
    ['-1 * * * *', 'minute: -1 is not a number'],
    ['0 0 L * *', 'day of month: L is not a number'],
    ['0 0 * * 1#2', 'day of week: 1#2 is not a number'],
    ['0 0 ? * *', 'day of month: ? is not a number'],
  ] as const;

  it.each(wrong)('%j is refused as %j', (expr, sentence) => {
    expect(describeCron(expr)).toEqual({ ok: false, reason: sentence });
  });

  it('never throws on odd input', () => {
    for (const expr of ['/', '*/', ',,,', '1-2-3 * * * *', '\u0000', '*/+', '+1 * * * *']) {
      expect(() => describeCron(expr)).not.toThrow();
      expect(reason(expr)).not.toBe('');
    }
  });
});
