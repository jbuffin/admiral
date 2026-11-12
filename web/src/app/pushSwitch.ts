export interface PushSwitchInputs {
  secure: boolean;
  hasPushManager: boolean;
  hasServiceWorker: boolean;
  ios: boolean;
  standalone: boolean;
  permission: 'default' | 'granted' | 'denied';
  /** The Mac's key route: 'off' is its 503. */
  key: 'ready' | 'off';
  /** The browser holds a push subscription. */
  subscribed: boolean;
  /** The server answered that it holds that subscription's endpoint for this device. */
  held: boolean;
}

export type PushSwitchKind = 'needs-https' | 'home-screen' | 'blocked' | 'mac-off' | 'on' | 'off';

/** What the switch shows. When states overlap the first of these wins, so a person is told the thing they can fix first. */
export function pushSwitchState(i: PushSwitchInputs): { kind: PushSwitchKind } {
  // Safari outside the Home Screen has no PushManager yet, and that is the home-screen state, not a missing transport.
  const iosBrowser = i.ios && !i.standalone;
  if (!i.secure || !i.hasServiceWorker || (!i.hasPushManager && !iosBrowser)) return { kind: 'needs-https' };
  if (iosBrowser) return { kind: 'home-screen' };
  if (i.permission === 'denied') return { kind: 'blocked' };
  if (i.key === 'off') return { kind: 'mac-off' };
  // A subscription the server dropped reads off, so the next turn-on subscribes again.
  return { kind: i.subscribed && i.held ? 'on' : 'off' };
}
