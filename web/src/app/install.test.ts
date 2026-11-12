import { describe, expect, it } from 'vitest';
import { installState } from './install';

const IPAD = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15';
const CHROME = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36';
const desktop = { offered: false, installed: false, standalone: false, userAgent: CHROME, maxTouchPoints: 0 };

describe('installState', () => {
  it('offers the install where the browser offered one', () => {
    expect(installState({ ...desktop, offered: true })).toBe('offer');
  });

  it('says installed in the installed app or once the install was accepted, even on an iPad', () => {
    expect(installState({ ...desktop, standalone: true })).toBe('installed');
    expect(installState({ ...desktop, installed: true })).toBe('installed');
    expect(installState({ ...desktop, userAgent: IPAD, maxTouchPoints: 5, standalone: true })).toBe('installed');
  });

  it('gives iPad Safari the Add to Home Screen steps, and any other browser without an offer the fallback', () => {
    expect(installState({ ...desktop, userAgent: IPAD, maxTouchPoints: 5 })).toBe('ios');
    expect(installState(desktop)).toBe('unavailable');
  });
});
