import { useEffect, useRef, useState } from 'preact/hooks';
import { editLogins, fetchDevices, fetchRemote, runTailscaleSetup, mintPairingCode, renameDevice, revokeAllDevices, revokeDevice, type Device, type PairingCode, type RemoteCheck, type RemoteView, type Result } from './api';
import { qrMatrix } from './qr';
import { checkLogin, copyOutcome, removingOwnLogin } from './remoteTailscale';
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

function CheckRows({ rows }: { rows: RemoteCheck[] }) {
  return (
    <ul class="box list" aria-label="Checks">
      {rows.map((c) => (
        <li key={c.id} class="set">
          <div class="t">
            <strong class={c.ok ? 'status-passed' : 'status-failed'}>{c.ok ? 'Passed' : 'Failed'}</strong>
            <span>{c.detail}</span>
          </div>
        </li>
      ))}
    </ul>
  );
}

function selectText(el: HTMLElement) {
  const range = document.createRange();
  range.selectNodeContents(el);
  const sel = getSelection();
  sel?.removeAllRanges();
  sel?.addRange(range);
}

function Tailscale({ view, local, remote, reload, onResult }: { view: RemoteView | null; local: boolean; remote: boolean; reload: () => Promise<void>; onResult: (r: Result<unknown>) => void }) {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [rows, setRows] = useState<RemoteCheck[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [turnedOn, setTurnedOn] = useState(false);
  const [copy, setCopy] = useState<'copied' | 'select' | null>(null);
  const urlEl = useRef<HTMLElement>(null);

  const setUp = async () => {
    setAsking(false);
    setBusy(true);
    setError(null);
    setTurnedOn(false);
    const r = await runTailscaleSetup();
    setBusy(false);
    onResult(r);
    if (!r.ok) return setError(r.error);
    setRows(r.data.checks);
    setTurnedOn(r.data.ok);
    await reload();
  };
  const copyUrl = async (url: string) => {
    const outcome = await copyOutcome(url, navigator.clipboard);
    if (outcome === 'select' && urlEl.current) selectText(urlEl.current);
    setCopy(outcome);
  };

  const on = view?.tailscale ?? false;
  return (
    <>
      <section role="group" aria-labelledby="tailscale-title" class="tailscale">
        <h2 id="tailscale-title" class="h2">
          Tailscale
        </h2>
        {!view ? (
          <p class="line">Reading Tailscale…</p>
        ) : on ? (
          <>
            {rows ? <CheckRows rows={rows} /> : null}
            <CheckRows rows={view.checks} />
            {view.url ? (
              <div class="box tailscale-url">
                <code ref={urlEl} class="pair-url">
                  {view.url}
                </code>
                <button type="button" class="btn" onClick={() => void copyUrl(view.url)}>
                  Copy
                </button>
                <span class="note" role="status">
                  {copy === 'copied' ? 'Copied.' : copy === 'select' ? 'Select the address and copy it.' : ''}
                </span>
              </div>
            ) : null}
          </>
        ) : (
          <>
            <p class="line">Tailscale mode is off. Admiral stays on this network only.</p>
            {rows ? <CheckRows rows={rows} /> : null}
            {asking ? null : (
              <button type="button" class="btn" disabled={busy || !local} onClick={() => (setAsking(true), setRows(null), setError(null))}>
                Set up
              </button>
            )}
            {remote ? <p class="note">Run Set up on the Mac that runs Admiral, or run admiral setup there.</p> : null}
          </>
        )}
        {turnedOn ? <p class="note">Tailscale mode is on. If Admiral listens on every address, the change takes effect the next time it starts.</p> : null}
        {error ? (
          <p class="note error-text" role="alert">
            {error}
          </p>
        ) : null}
      </section>
      {asking ? (
        <div class="confirm" role="group" aria-label="Set up Tailscale mode">
          <span>
            Set up checks Tailscale, runs one tailscale serve command and turns Tailscale mode on. The HTTPS certificate publishes this Mac's ts.net name in
            public Certificate Transparency logs.
          </span>
          <button type="button" class="btn" disabled={busy} onClick={() => void setUp()}>
            Turn on Tailscale mode
          </button>
          <button type="button" class="btn" onClick={() => setAsking(false)}>
            Cancel
          </button>
        </div>
      ) : null}
    </>
  );
}

function AllowedLogins({ view, devices, onView, onResult }: { view: RemoteView | null; devices: Device[]; onView: (v: RemoteView) => void; onResult: (r: Result<unknown>) => void }) {
  const [text, setText] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [removing, setRemoving] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  if (!view) return null;

  const act = async (change: { add: string } | { remove: string }) => {
    setError(null);
    setBusy(true);
    const r = await editLogins(change);
    setBusy(false);
    onResult(r);
    if (!r.ok) return setError(r.error), false;
    setRemoving(null);
    onView(r.data);
    return true;
  };
  const add = async (e: Event) => {
    e.preventDefault();
    const c = checkLogin(text, view.allowedLogins);
    if (!c.ok) return setError(c.error);
    if (await act({ add: c.login })) setText('');
  };

  return (
    <>
      <h2 class="h2" id="logins-title">
        Allowed logins
      </h2>
      {view.allowedLogins.length === 0 ? <p class="line">No login is allowed yet, so no tailnet device can reach Admiral.</p> : null}
      <ul class="box list" aria-label="Allowed logins">
        {view.allowedLogins.map((l) => (
          <li key={l} class="set" aria-label={l}>
            <div class="t">
              <span>{l}</span>
              {removing === l ? (
                <div class="confirm" role="group" aria-label={`Remove ${l}?`}>
                  <span>This device came in on {l}, so removing it signs this device out.</span>
                  <button type="button" class="btn danger" disabled={busy} onClick={() => void act({ remove: l })}>
                    Remove and sign out
                  </button>
                  <button type="button" class="btn" onClick={() => setRemoving(null)}>
                    Cancel
                  </button>
                </div>
              ) : null}
            </div>
            {removing === l ? null : (
              <button type="button" class="btn" disabled={busy} aria-label={`Remove ${l}`} onClick={() => (removingOwnLogin(l, devices) ? setRemoving(l) : void act({ remove: l }))}>
                Remove
              </button>
            )}
          </li>
        ))}
      </ul>
      <form class="set-add" onSubmit={(e) => void add(e)}>
        <input type="text" aria-label="Allowed login" placeholder="name@example.com" value={text} onInput={(e) => setText(e.currentTarget.value)} />
        <button type="submit" class="btn" disabled={busy}>
          Add login
        </button>
      </form>
      {error ? (
        <p class="note error-text" role="alert">
          {error}
        </p>
      ) : null}
    </>
  );
}

export function RemoteAccess() {
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  // An action's error stays until the person acts again, so a poll can't clear it.
  const [error, setError] = useState<string | null>(null);
  const [signedOut, setSignedOut] = useState<string | null>(null);
  const [confirmAll, setConfirmAll] = useState(false);
  const [note, setNote] = useState('');
  const [remote, setRemote] = useState<RemoteView | null>(null);

  const onResult = (r: Result<unknown>) => {
    const line = signedOutLine(r);
    if (line) setSignedOut(line);
  };
  const latest = useRef(0);
  const reload = async () => {
    const seq = ++latest.current;
    const r = await fetchDevices();
    if (seq !== latest.current) return;
    onResult(r);
    if (r.ok) {
      setDevices(r.data);
      setListError(null);
    } else setListError(r.error);
  };
  const loadRemote = async () => {
    const r = await fetchRemote();
    onResult(r);
    if (r.ok) setRemote(r.data);
  };
  useEffect(() => void loadRemote(), []);
  // A device that redeems the code on screen shows up without a reload.
  useEffect(() => {
    void reload();
    if (signedOut) return;
    const t = setInterval(() => void reload(), 2000);
    return () => clearInterval(t);
  }, [signedOut]);

  if (signedOut)
    return (
      <p class="line" role="status">
        {signedOut}
      </p>
    );

  const signOutAll = async () => {
    setError(null);
    const r = await revokeAllDevices();
    onResult(r);
    setConfirmAll(false);
    if (!r.ok) return setError(r.error);
    setNote('Every device was signed out. Pair each one again with a new code.');
    await reload();
  };

  return (
    <>
      <Tailscale view={remote} local={devices === null ? listError !== null : !devices.some((d) => d.current)} remote={devices !== null && devices.some((d) => d.current)} reload={loadRemote} onResult={onResult} />
      <AllowedLogins view={remote} devices={devices ?? []} onView={setRemote} onResult={onResult} />
      <Pairing onResult={onResult} />
      <h2 class="h2" id="devices-title">
        Devices
      </h2>
      {listError || error ? (
        <p class="note error-text" role="alert">
          {error ?? listError}
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
        <button type="button" class="btn danger" onClick={() => (setNote(''), setError(null), setConfirmAll(true))}>
          Sign out all devices
        </button>
      )}
    </>
  );
}
