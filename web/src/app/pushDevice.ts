import { checkPushSubscription, dropPushSubscription, fetchPushKey, savePushSubscription } from './api';
import { inStandalone, isIOS } from './ios';
import type { PushSwitchInputs } from './pushSwitch';

export interface PushDeviceState {
  inputs: PushSwitchInputs;
  /** The Mac's application server key, when its route answered. */
  key: string | null;
  /** Set when this browser's push state could not be read; the switch stays disabled and says so. */
  problem?: string;
}

const READ_MS = 5000;
const UNREADABLE = 'This browser did not answer. Reload the page to try again.';

const secureContext = () => typeof isSecureContext === 'boolean' && isSecureContext;
const permissionNow = (): PushSwitchInputs['permission'] => (typeof Notification === 'undefined' ? 'default' : Notification.permission);

const keyBytes = (key: string) => Uint8Array.from(atob(key.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0));

const registration = () => navigator.serviceWorker.ready;

// `ready` never resolves while no worker is registered, so the read gives up instead of hanging.
const withTimeout = <T,>(p: Promise<T>): Promise<T> =>
  Promise.race([p, new Promise<never>((_, no) => setTimeout(() => no(new Error('timeout')), READ_MS))]);

const sameKey = (a: ArrayBuffer, b: Uint8Array) => a.byteLength === b.length && new Uint8Array(a).every((x, i) => x === b[i]);

/** What this browser and the Mac say about push right now. */
export async function readPushDevice(): Promise<PushDeviceState> {
  try {
    return await readUnguarded();
  } catch {
    return { inputs: { ...(await baseInputs()), key: 'ready', subscribed: false, held: false }, key: null, problem: UNREADABLE };
  }
}

async function baseInputs() {
  return {
    secure: secureContext(),
    hasPushManager: 'PushManager' in window,
    hasServiceWorker: 'serviceWorker' in navigator,
    ios: isIOS({ userAgent: navigator.userAgent, maxTouchPoints: navigator.maxTouchPoints }),
    standalone: inStandalone(),
    permission: permissionNow(),
  };
}

async function readUnguarded(): Promise<PushDeviceState> {
  const base = await baseInputs();
  const { hasServiceWorker } = base;
  const inputs: PushSwitchInputs = { ...base, key: 'ready', subscribed: false, held: false };
  // Without a transport the rest cannot be asked, and the state says why.
  if (!base.secure || !hasServiceWorker || !base.hasPushManager) return { inputs, key: null };
  const k = await fetchPushKey();
  if (!k.ok) return { inputs: { ...inputs, key: 'off' }, key: null };
  const sub = await withTimeout(registration().then((r) => r.pushManager.getSubscription()));
  // A subscription made with another key (the Mac's key file was replaced) gets no pushes, so it reads off and the next turn-on makes a new one.
  const old = sub?.options?.applicationServerKey;
  const current = old == null || sameKey(old, keyBytes(k.data.key));
  const held = sub && current ? (await checkPushSubscription(sub.endpoint)).ok : false;
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
    const old = await reg.pushManager.getSubscription();
    if (old) {
      await dropPushSubscription(old.endpoint);
      await old.unsubscribe();
    }
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
