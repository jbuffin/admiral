import { checkPushSubscription, dropPushSubscription, fetchPushKey, savePushSubscription } from './api';
import { inStandalone, isIOS } from './ios';
import type { PushSwitchInputs } from './pushSwitch';

export interface PushDeviceState {
  inputs: PushSwitchInputs;
  /** The Mac's application server key, when its route answered. */
  key: string | null;
}

const secureContext = () => typeof isSecureContext === 'boolean' && isSecureContext;
const permissionNow = (): PushSwitchInputs['permission'] => (typeof Notification === 'undefined' ? 'default' : Notification.permission);

const keyBytes = (key: string) => Uint8Array.from(atob(key.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0));

const registration = () => navigator.serviceWorker.ready;

/** What this browser and the Mac say about push right now. */
export async function readPushDevice(): Promise<PushDeviceState> {
  const hasServiceWorker = 'serviceWorker' in navigator;
  const base = {
    secure: secureContext(),
    hasPushManager: 'PushManager' in window,
    hasServiceWorker,
    ios: isIOS({ userAgent: navigator.userAgent, maxTouchPoints: navigator.maxTouchPoints }),
    standalone: inStandalone(),
    permission: permissionNow(),
  };
  const inputs: PushSwitchInputs = { ...base, key: 'ready', subscribed: false, held: false };
  // Without a transport the rest cannot be asked, and the state says why.
  if (!base.secure || !hasServiceWorker || !base.hasPushManager) return { inputs, key: null };
  const k = await fetchPushKey();
  if (!k.ok) return { inputs: { ...inputs, key: 'off' }, key: null };
  const sub = await (await registration()).pushManager.getSubscription();
  const held = sub ? (await checkPushSubscription(sub.endpoint)).ok : false;
  return { inputs: { ...inputs, subscribed: sub !== null, held }, key: k.data.key };
}

/** Turns push on for this browser. Called from the tap itself: iOS only shows the permission prompt for a user gesture. */
export async function turnOnPush(key: string): Promise<string | null> {
  const permission = await Notification.requestPermission();
  if (permission !== 'granted') return null;
  const reg = await registration();
  const options = { userVisibleOnly: true, applicationServerKey: keyBytes(key) };
  let sub: PushSubscription;
  try {
    sub = await reg.pushManager.subscribe(options);
  } catch {
    // A subscription made with another key (the Mac's key changed) blocks a new one until it is dropped.
    await (await reg.pushManager.getSubscription())?.unsubscribe();
    sub = await reg.pushManager.subscribe(options);
  }
  const json = sub.toJSON();
  const saved = await savePushSubscription({ endpoint: sub.endpoint, keys: { p256dh: json.keys?.p256dh ?? '', auth: json.keys?.auth ?? '' } });
  if (saved.ok) return null;
  await sub.unsubscribe();
  return saved.error;
}

/** Turns push off: the Mac forgets the endpoint first, so a failure leaves the switch on with the subscription still held. */
export async function turnOffPush(): Promise<string | null> {
  const sub = await (await registration()).pushManager.getSubscription();
  if (!sub) return null;
  const dropped = await dropPushSubscription(sub.endpoint);
  if (!dropped.ok) return dropped.error;
  await sub.unsubscribe();
  return null;
}
