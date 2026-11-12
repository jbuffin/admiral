import { describe, expect, it } from 'vitest';
import { isIOS, isStandalone } from './ios';

const IPHONE = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1';
const MAC = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15';
const ANDROID = 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36';

describe('isIOS', () => {
  it('knows an iPhone, an iPad that reports a Mac, and not a Mac or an Android phone', () => {
    expect(isIOS({ userAgent: IPHONE, maxTouchPoints: 5 })).toBe(true);
    expect(isIOS({ userAgent: MAC, maxTouchPoints: 5 })).toBe(true);
    expect(isIOS({ userAgent: MAC, maxTouchPoints: 0 })).toBe(false);
    expect(isIOS({ userAgent: ANDROID, maxTouchPoints: 5 })).toBe(false);
  });
});

describe('isStandalone', () => {
  it('is true for the Home Screen app by either signal', () => {
    expect(isStandalone({ navigatorStandalone: true, displayModeStandalone: false })).toBe(true);
    expect(isStandalone({ navigatorStandalone: undefined, displayModeStandalone: true })).toBe(true);
    expect(isStandalone({ navigatorStandalone: false, displayModeStandalone: false })).toBe(false);
    expect(isStandalone({ navigatorStandalone: undefined, displayModeStandalone: false })).toBe(false);
  });
});
