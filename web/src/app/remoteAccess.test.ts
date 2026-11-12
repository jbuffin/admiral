import { describe, expect, it } from 'vitest';
import { expiryState, formatCode, routeOf, signedOutLine, thisDeviceMark } from './remoteAccess';

describe('formatCode', () => {
  it('shows a code as XXXX-XXXX', () => {
    expect(formatCode('abcd2345')).toBe('ABCD-2345');
    expect(formatCode('ABCD-2345')).toBe('ABCD-2345');
  });
});

describe('expiryState', () => {
  const now = Date.parse('2026-11-11T10:00:00Z');
  const at = (ms: number) => new Date(now + ms).toISOString();

  it('counts down the five minutes a code lives', () => {
    expect(expiryState(at(5 * 60_000), now)).toEqual({ expired: false, remaining: '5:00' });
    expect(expiryState(at(9_000), now)).toEqual({ expired: false, remaining: '0:09' });
  });

  it('is expired at the expiry time and after', () => {
    expect(expiryState(at(0), now).expired).toBe(true);
    expect(expiryState(at(-60_000), now).expired).toBe(true);
  });
});

describe('thisDeviceMark', () => {
  it('marks only the device the request came from', () => {
    expect(thisDeviceMark({ current: true })).toBe('This device');
    expect(thisDeviceMark({ current: false })).toBeNull();
  });
});

describe('signedOutLine', () => {
  it('replaces the page with a pair-again line when a call answers 401', () => {
    expect(signedOutLine({ ok: false, status: 401 })).toBe('This device was signed out. Pair it again with a new code.');
  });

  it('says nothing for other failures or successes', () => {
    expect(signedOutLine({ ok: false, status: 500 })).toBeNull();
    expect(signedOutLine({ ok: false })).toBeNull();
    expect(signedOutLine({ ok: true })).toBeNull();
  });
});

describe('routeOf', () => {
  it('is local for a browser with no paired row', () => {
    expect(routeOf([{ current: false }], false)).toEqual({ local: true, remote: false });
  });

  it('is remote for a paired device', () => {
    expect(routeOf([{ current: true }, { thisMac: true }], false)).toEqual({ local: false, remote: true });
  });

  it('stays local on the Mac once This Mac is its current row', () => {
    expect(routeOf([{ current: true, thisMac: true }, { current: false }], false)).toEqual({ local: true, remote: false });
  });

  it('waits for the list, and reads a failed one as local', () => {
    expect(routeOf(null, false)).toEqual({ local: false, remote: false });
    expect(routeOf(null, true)).toEqual({ local: true, remote: false });
  });
});
