import { describe, expect, it } from 'vitest';
import { pushSwitchState, type PushSwitchInputs } from './pushSwitch';

// A desktop browser on Tailscale HTTPS with push available, nothing subscribed yet.
const ready: PushSwitchInputs = { secure: true, hasPushManager: true, hasServiceWorker: true, ios: false, standalone: false, permission: 'default', key: 'ready', subscribed: false, held: false };
const state = (over: Partial<PushSwitchInputs>) => pushSwitchState({ ...ready, ...over }).kind;

describe('the notifications switch on this device', () => {
  it('is off and usable before anything is subscribed', () => {
    expect(state({})).toBe('off');
  });

  it('is on when the browser holds a subscription and the server still holds that endpoint', () => {
    expect(state({ subscribed: true, held: true, permission: 'granted' })).toBe('on');
  });

  it('reads off when the server dropped an endpoint the browser still holds, so the next turn-on subscribes again', () => {
    expect(state({ subscribed: true, held: false, permission: 'granted' })).toBe('off');
  });

  it('cannot be on over plain HTTP, and points to Tailscale mode', () => {
    expect(state({ secure: false, hasPushManager: false, hasServiceWorker: false })).toBe('needs-https');
  });

  it('asks an iPad or iPhone that is not on the Home Screen to add it first', () => {
    expect(state({ ios: true, standalone: false, hasPushManager: false })).toBe('home-screen');
  });

  it('lets a Home Screen app on iOS turn it on', () => {
    expect(state({ ios: true, standalone: true })).toBe('off');
  });

  it('says it is blocked in the browser when permission was denied', () => {
    expect(state({ permission: 'denied' })).toBe('blocked');
  });

  it('says push is off on the Mac when the key route answers 503', () => {
    expect(state({ key: 'off' })).toBe('mac-off');
  });

  it('names the thing to fix first when states overlap: HTTPS, then the Home Screen, then the browser block, then the Mac', () => {
    expect(state({ secure: false, hasPushManager: false, hasServiceWorker: false, ios: true, standalone: false })).toBe('needs-https');
    expect(state({ permission: 'denied', key: 'off' })).toBe('blocked');
    expect(state({ subscribed: true, held: true, permission: 'denied' })).toBe('blocked');
    expect(state({ ios: true, standalone: false, hasPushManager: false, permission: 'denied' })).toBe('home-screen');
  });
});
