import { useEffect, useState } from 'preact/hooks';
import { fetchDevices, mintPairingCode, renameDevice, revokeAllDevices, revokeDevice, type Device, type PairingCode, type Result } from './api';
import { qrMatrix } from './qr';
import { expiryState, formatCode, signedOutLine, thisDeviceMark } from './remoteAccess';

const stamp = (iso: string | null) => (iso ? new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }) : 'never');

function Qr({ url }: { url: string }) {
  const m = qrMatrix(url);
  const quiet = 4;
  const side = m.length + 2 * quiet;
  const d = m.flatMap((row, y) => row.map((on, x) => (on ? `M${x + quiet} ${y + quiet}h1v1h-1z` : ''))).join('');
  return (
    <svg class="qr" viewBox={`0 0 ${side} ${side}`} role="img" aria-label="Pairing QR code" shape-rendering="crispEdges">
      <rect width={side} height={side} fill="#fff" />
      <path d={d} fill="#000" />
    </svg>
  );
}

function Pairing({ onResult }: { onResult: (r: Result<unknown>) => void }) {
  const [code, setCode] = useState<PairingCode | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [now, setNow] = useState(Date.now());
  const [busy, setBusy] = useState(false);

  const mint = async () => {
    setBusy(true);
    const r = await mintPairingCode();
    setBusy(false);
    onResult(r);
    if (r.ok) {
      setCode(r.data);
      setError(null);
      setNow(Date.now());
    } else {
      setCode(null);
      setError(r.error);
    }
  };
  useEffect(() => void mint(), []);
  useEffect(() => {
    if (!code) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [code]);

  const state = code ? expiryState(code.expires, now) : null;
  return (
    <section role="group" aria-labelledby="pairing-title" class="pairing">
      <h2 id="pairing-title" class="h2">
        Pairing code
      </h2>
      <div class="box pairing-box">
        {code && state && !state.expired ? (
          <>
            <Qr url={code.url} />
            <div class="pairing-text">
              <code class="pair-url">{code.url}</code>{' '}
              <code class="pair-code">{formatCode(code.code)}</code>{' '}
              <span class="note">Expires in {state.remaining}. The code works one time.</span>
            </div>
          </>
        ) : code ? (
          <p class="line">This code expired.</p>
        ) : error ? (
          <p class="note error-text" role="alert">
            {error}
          </p>
        ) : (
          <p class="line">Making a code…</p>
        )}
        <button type="button" class="btn" disabled={busy} onClick={() => void mint()}>
          New code
        </button>
      </div>
    </section>
  );
}

function DeviceRow({ device, reload, onResult }: { device: Device; reload: () => Promise<void>; onResult: (r: Result<unknown>) => void }) {
  const [name, setName] = useState(device.name);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => setName(device.name), [device.name]);
  const mark = thisDeviceMark(device);

  const act = async (r: Result<unknown>) => {
    onResult(r);
    if (!r.ok) return setError(r.error);
    setError(null);
    setConfirming(false);
    await reload();
  };
  const commit = () => {
    const next = name.trim();
    if (next && next !== device.name) void renameDevice(device.id, next).then(act);
    else setName(device.name);
  };

  return (
    <li class="set device" aria-label={device.name}>
      <div class="t">
        <input
          type="text"
          aria-label="Device name"
          value={name}
          onInput={(e) => setName(e.currentTarget.value)}
          onBlur={commit}
          onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
        />
        <span>
          {mark ? <strong>{mark} · </strong> : null}
          {device.login ? `${device.login} · ` : ''}paired {stamp(device.pairedAt)} · last seen {stamp(device.lastSeen)}
        </span>
        {error ? <span class="note error-text">{error}</span> : null}
        {confirming ? (
          <div class="confirm" role="group" aria-label={`Revoke ${device.name}`}>
            <span>Revoke {device.name}? It will need a new code to pair again.</span>
            <button type="button" class="btn danger" onClick={() => void revokeDevice(device.id).then(act)}>
              Revoke device
            </button>
            <button type="button" class="btn" onClick={() => setConfirming(false)}>
              Cancel
            </button>
          </div>
        ) : null}
      </div>
      {confirming ? null : (
        <button type="button" class="btn" onClick={() => setConfirming(true)}>
          Revoke
        </button>
      )}
    </li>
  );
}

export function RemoteAccess() {
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [signedOut, setSignedOut] = useState<string | null>(null);
  const [confirmAll, setConfirmAll] = useState(false);
  const [note, setNote] = useState('');

  const onResult = (r: Result<unknown>) => {
    const line = signedOutLine(r);
    if (line) setSignedOut(line);
  };
  const reload = async () => {
    const r = await fetchDevices();
    onResult(r);
    if (r.ok) {
      setDevices(r.data);
      setError(null);
    } else setError(r.error);
  };
  // A device that redeems the code on screen shows up without a reload.
  useEffect(() => {
    void reload();
    const t = setInterval(() => void reload(), 2000);
    return () => clearInterval(t);
  }, []);

  if (signedOut)
    return (
      <p class="line" role="status">
        {signedOut}
      </p>
    );

  const signOutAll = async () => {
    const r = await revokeAllDevices();
    onResult(r);
    setConfirmAll(false);
    if (!r.ok) return setError(r.error);
    setNote('Every device was signed out. Pair each one again with a new code.');
    await reload();
  };

  return (
    <>
      <Pairing onResult={onResult} />
      <h2 class="h2" id="devices-title">
        Devices
      </h2>
      {error ? (
        <p class="note error-text" role="alert">
          {error}
        </p>
      ) : null}
      {devices && devices.length === 0 ? <p class="line">No paired devices yet.</p> : null}
      {devices && devices.length > 0 ? (
        <ul class="box devices" aria-label="Devices">
          {devices.map((d) => (
            <DeviceRow key={d.id} device={d} reload={reload} onResult={onResult} />
          ))}
        </ul>
      ) : null}
      {note ? (
        <p class="note" role="status">
          {note}
        </p>
      ) : null}
      {confirmAll ? (
        <div class="confirm" role="group" aria-label="Sign out all devices">
          <span>Sign out every paired device? Each needs a new code to pair again.</span>
          <button type="button" class="btn danger" onClick={() => void signOutAll()}>
            Sign out all
          </button>
          <button type="button" class="btn" onClick={() => setConfirmAll(false)}>
            Cancel
          </button>
        </div>
      ) : (
        <button type="button" class="btn danger" onClick={() => (setNote(''), setConfirmAll(true))}>
          Sign out all devices
        </button>
      )}
    </>
  );
}
