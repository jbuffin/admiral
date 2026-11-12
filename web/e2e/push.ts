import { createDecipheriv, createECDH, hkdfSync, randomBytes, type ECDH } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { createServer, type Server } from 'node:https';
import { join } from 'node:path';

const b64url = (b: Buffer) => b.toString('base64url');

export interface PushRequest {
  path: string;
  method: string;
  headers: Record<string, string | string[] | undefined>;
  body: Buffer;
}

/** A browser's push subscription as a test holds it: the key pair and auth secret the Mac encrypts to. */
export interface Subscriber {
  ecdh: ECDH;
  auth: Buffer;
  p256dh: string;
  authB64: string;
}

export function newSubscriber(): Subscriber {
  const ecdh = createECDH('prime256v1');
  ecdh.generateKeys();
  const auth = randomBytes(16);
  return { ecdh, auth, p256dh: b64url(ecdh.getPublicKey()), authB64: b64url(auth) };
}

/** RFC 8291 (aes128gcm): the plaintext of one push body, written from the RFC and not from the code under test. */
export function decryptPush(s: Pick<Subscriber, 'ecdh' | 'auth'>, body: Buffer): string {
  const salt = body.subarray(0, 16);
  const idlen = body[20];
  const asPub = body.subarray(21, 21 + idlen);
  const record = body.subarray(21 + idlen);
  const secret = s.ecdh.computeSecret(asPub);
  const info = Buffer.concat([Buffer.from('WebPush: info\0'), s.ecdh.getPublicKey(), asPub]);
  const ikm = Buffer.from(hkdfSync('sha256', secret, s.auth, info, 32));
  const cek = Buffer.from(hkdfSync('sha256', ikm, salt, 'Content-Encoding: aes128gcm\0', 16));
  const nonce = Buffer.from(hkdfSync('sha256', ikm, salt, 'Content-Encoding: nonce\0', 12));
  const d = createDecipheriv('aes-128-gcm', cek, nonce);
  d.setAuthTag(record.subarray(record.length - 16));
  const padded = Buffer.concat([d.update(record.subarray(0, record.length - 16)), d.final()]);
  let end = padded.length;
  while (end > 0 && padded[end - 1] === 0) end--;
  return padded.subarray(0, end - 1).toString('utf8');
}

export interface FakePush {
  /** Where the fake listens: https://127.0.0.1:<port>. */
  url: string;
  /** A PEM file of its certificate, for ADMIRAL_PUSH_TEST_ROOTS. */
  rootsFile: string;
  requests: PushRequest[];
  close(): Promise<void>;
}

/** An HTTPS push service on loopback with a self-made certificate; it answers every POST 201. */
export async function startFakePush(dir: string): Promise<FakePush> {
  const keyFile = join(dir, 'fake-push-key.pem');
  const rootsFile = join(dir, 'fake-push-cert.pem');
  const made = spawnSync(
    'openssl',
    ['req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:prime256v1', '-nodes', '-keyout', keyFile, '-out', rootsFile, '-days', '1', '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1'],
    { encoding: 'utf8' },
  );
  if (made.status !== 0) throw new Error(`openssl: ${made.stderr}`);
  const requests: PushRequest[] = [];
  const server: Server = createServer({ key: readFileSync(keyFile), cert: readFileSync(rootsFile) }, (req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (c: Buffer) => chunks.push(c));
    req.on('end', () => {
      requests.push({ path: req.url ?? '', method: req.method ?? '', headers: req.headers, body: Buffer.concat(chunks) });
      res.writeHead(201).end();
    });
  });
  await new Promise<void>((ok) => server.listen(0, '127.0.0.1', ok));
  const port = (server.address() as { port: number }).port;
  return { url: `https://127.0.0.1:${port}`, rootsFile, requests, close: () => new Promise((ok) => server.close(() => ok())) };
}

/** Runs before every page script: WebKit under Playwright has no PushManager, so this one hands out a subscription on the fake. The state lives in localStorage so a reload or a trip to about:blank keeps it. */
export function pushStub({ endpoint, p256dh, auth }: { endpoint: string; p256dh: string; auth: string }) {
  const SUB = 'e2e-push-subscription';
  const KEY = 'e2e-push-application-server-key';
  const ASKED = 'e2e-push-permission-asked';
  const PERM = 'e2e-push-permission';
  const text = (k: BufferSource | string) => {
    if (typeof k === 'string') return k.replace(/=+$/, '').replace(/\+/g, '-').replace(/\//g, '_');
    const bytes = new Uint8Array(k instanceof ArrayBuffer ? k : (k as ArrayBufferView).buffer, (k as ArrayBufferView).byteOffset ?? 0, (k as ArrayBufferView).byteLength ?? (k as ArrayBuffer).byteLength);
    return btoa(String.fromCharCode(...bytes)).replace(/=+$/, '').replace(/\+/g, '-').replace(/\//g, '_');
  };
  const subscription = () => ({
    endpoint,
    expirationTime: null,
    options: { userVisibleOnly: true },
    getKey: () => null,
    toJSON: () => ({ endpoint, expirationTime: null, keys: { p256dh, auth } }),
    unsubscribe: async () => {
      localStorage.removeItem(SUB);
      return true;
    },
  });
  const manager = {
    subscribe: async (opts: { applicationServerKey?: BufferSource | string }) => {
      if (opts?.applicationServerKey) localStorage.setItem(KEY, text(opts.applicationServerKey));
      localStorage.setItem(SUB, '1');
      return subscription();
    },
    getSubscription: async () => (localStorage.getItem(SUB) ? subscription() : null),
    permissionState: async () => localStorage.getItem(PERM) ?? 'prompt',
  };
  const w = window as unknown as Record<string, unknown>;
  if (!w.PushManager) w.PushManager = class PushManager {};
  Object.defineProperty(ServiceWorkerRegistration.prototype, 'pushManager', { configurable: true, get: () => manager });
  if (!w.Notification) w.Notification = class Notification {};
  const N = w.Notification as { permission?: string; requestPermission?: unknown };
  Object.defineProperty(N, 'permission', { configurable: true, get: () => localStorage.getItem(PERM) ?? 'default' });
  N.requestPermission = (cb?: (p: string) => void) => {
    localStorage.setItem(ASKED, '1');
    localStorage.setItem(PERM, 'granted');
    cb?.('granted');
    return Promise.resolve('granted');
  };
}

/** What the stub saw, read from the page: whether permission was asked from a tap, and the key subscribe was given. */
export const stubSeen = () => ({
  asked: localStorage.getItem('e2e-push-permission-asked') === '1',
  key: localStorage.getItem('e2e-push-application-server-key'),
  subscribed: localStorage.getItem('e2e-push-subscription') === '1',
});
