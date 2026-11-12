import { describe, expect, it } from 'vitest';
import { checkLogin, copyOutcome, removingOwnLogin } from './remoteTailscale';

describe('checkLogin', () => {
  it('trims the login it adds', () => {
    expect(checkLogin('  bob@example.com ', ['alice@example.com'])).toEqual({ ok: true, login: 'bob@example.com' });
  });

  it('refuses a blank login and one already listed', () => {
    expect(checkLogin('   ', []).ok).toBe(false);
    expect(checkLogin(' alice@example.com', ['alice@example.com']).ok).toBe(false);
  });
});

describe('removingOwnLogin', () => {
  const devices = [
    { id: 'a', login: 'alice@example.com', current: true },
    { id: 'b', login: 'bob@example.com' },
  ];

  it('is true only for the login this device came in on', () => {
    expect(removingOwnLogin('alice@example.com', devices)).toBe(true);
    expect(removingOwnLogin('bob@example.com', devices)).toBe(false);
  });

  it('is false for a device on the Mac, which has no login', () => {
    expect(removingOwnLogin('alice@example.com', [{ id: 'm', login: '', current: true }])).toBe(false);
  });
});

describe('copyOutcome', () => {
  it('says it copied when the clipboard takes the text', async () => {
    let got = '';
    expect(await copyOutcome('https://x.ts.net/', { writeText: async (t: string) => void (got = t) })).toBe('copied');
    expect(got).toBe('https://x.ts.net/');
  });

  it('asks to select the text when the clipboard refuses or is missing', async () => {
    expect(await copyOutcome('u', { writeText: async () => Promise.reject(new Error('NotAllowedError')) })).toBe('select');
    expect(await copyOutcome('u', undefined)).toBe('select');
  });
});
