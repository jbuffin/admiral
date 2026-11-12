import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installSheetDue, markInstallSheetShown } from './installSheet';

const IPAD = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15';
const ANDROID = 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36';
const safari = { userAgent: IPAD, maxTouchPoints: 5, standalone: false };

const memory = () => {
  const m = new Map<string, string>();
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), removeItem: (k: string) => void m.delete(k) };
};

beforeEach(() => vi.stubGlobal('localStorage', memory()));
afterEach(() => vi.unstubAllGlobals());

describe('installSheetDue', () => {
  it('is due once for iPad Safari, and not again after it was shown', () => {
    expect(installSheetDue(safari)).toBe(true);
    markInstallSheetShown();
    expect(installSheetDue(safari)).toBe(false);
  });

  it('is never due in the installed app or off iOS', () => {
    expect(installSheetDue({ ...safari, standalone: true })).toBe(false);
    expect(installSheetDue({ userAgent: ANDROID, maxTouchPoints: 5, standalone: false })).toBe(false);
  });

  it('is due when storage is blocked, rather than throwing', () => {
    const blocked = () => {
      throw new Error('blocked');
    };
    vi.stubGlobal('localStorage', { getItem: blocked, setItem: blocked });
    expect(installSheetDue(safari)).toBe(true);
    expect(() => markInstallSheetShown()).not.toThrow();
  });
});
