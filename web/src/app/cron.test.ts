import { describe, expect, it } from 'vitest';
import { cronSentence } from './cron';

describe('a cron expression as a sentence', () => {
  it('says weekdays at a time of day', () => {
    expect(cronSentence('30 8 * * 1-5')).toBe('Weekdays at 08:30');
    expect(cronSentence('45 8 * * mon-fri')).toBe('Weekdays at 08:45');
  });

  it('says daily for every day', () => {
    expect(cronSentence('0 2 * * *')).toMatch(/^Daily (at )?02:00$/);
  });

  it('shows the expression itself when no sentence fits', () => {
    for (const expr of ['*/15 * * * *', '0 9 1 * *', '30 8,17 * * 1-5']) expect(cronSentence(expr)).toBe(expr);
  });
});
