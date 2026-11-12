import { useEffect, useRef, useState } from 'preact/hooks';
import { fetchDevices, type Device } from './api';
import { readPushDevice, turnOffPush, turnOnPush, type PushDeviceState } from './pushDevice';
import { pushSwitchState, type PushSwitchKind } from './pushSwitch';
import { remotePath, go } from './router';
import { fleetSearch } from './fleets';
import { thisDeviceMark } from './remoteAccess';

const stamp = (iso: string) => new Date(iso).toLocaleDateString([], { month: 'short', day: 'numeric' });

const HINTS: Partial<Record<PushSwitchKind, string>> = {
  off: 'Pushes reach this device when something needs you and no Admiral window is open.',
  on: 'Pushes reach this device when something needs you and no Admiral window is open.',
  'home-screen': 'Add Admiral to the Home Screen first. iPhone and iPad only allow pushes from the installed app.',
  blocked: 'Notifications are blocked for Admiral in this browser. Allow them in the browser’s settings, then come back.',
  'mac-off': 'Push is off on the Mac, so there is nothing to subscribe to.',
};

function DeviceLine({ device }: { device: Device }) {
  const mark = thisDeviceMark(device);
  return (
    <li class="set device" aria-label={device.name}>
      <div class="t">
        <span class="label">{device.name}</span>
        <span>
          {mark ? `${mark} · ` : ''}
          {device.pairedAt ? `Paired ${stamp(device.pairedAt)} · ` : ''}push {device.notifications ? 'on' : 'off'}
        </span>
      </div>
    </li>
  );
}

export function NotificationsPage() {
  const [device, setDevice] = useState<PushDeviceState | null>(null);
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const latest = useRef(0);

  const refresh = async () => {
    const seq = ++latest.current;
    // The list does not wait on the browser's answer.
    void fetchDevices().then((list) => {
      if (seq === latest.current && list.ok) setDevices(list.data);
    });
    const d = await readPushDevice();
    if (seq === latest.current) setDevice(d);
  };
  useEffect(() => void refresh(), []);

  const kind = device ? pushSwitchState(device.inputs).kind : null;
  const on = kind === 'on';
  const usable = (kind === 'on' || kind === 'off') && !device?.problem;

  // The permission prompt must open from this tap, so the handler starts it before anything else.
  const toggle = () => {
    if (!device || busy) return;
    setBusy(true);
    setError(null);
    const done = (err: string | null) => {
      if (err) setError(err);
      return refresh().finally(() => setBusy(false));
    };
    const run = on ? turnOffPush() : device.key ? turnOnPush(device.key) : Promise.resolve(null);
    run.then(done, (e: unknown) => done(e instanceof Error ? e.message : 'Something went wrong.'));
  };

  return (
    <>
      <p class="line">Where Admiral gets your attention. Each fleet's Notify switch decides which fleets push.</p>
      <div class="set box" role="group" aria-label="This device">
        <div class="t">
          <span id="push-label" class="label">
            Notifications on this device
          </span>
          <span id="push-hint">{kind === null ? 'Checking this device…' : (device?.problem ?? HINTS[kind] ?? '')}</span>
          {kind === 'needs-https' ? (
            <span class="note">
              Pushes need Tailscale mode, which gives Admiral a secure address.{' '}
              <a
                href={remotePath + fleetSearch.value}
                onClick={(e) => {
                  e.preventDefault();
                  go(remotePath, fleetSearch.value);
                }}
              >
                Set it up under Remote access
              </a>
            </span>
          ) : null}
          {error ? (
            <span class="note error-text" role="alert">
              {error}
            </span>
          ) : null}
        </div>
        {kind === 'home-screen' ? null : (
          <button type="button" class="switch" role="switch" aria-checked={on} aria-labelledby="push-label" aria-describedby="push-hint" disabled={busy || !usable} onClick={toggle}>
            <span class="track" aria-hidden="true" />
          </button>
        )}
      </div>
      <h2 class="h2" id="push-devices-title">
        Devices
      </h2>
      {devices && devices.length === 0 ? <p class="line">No devices yet.</p> : null}
      {devices && devices.length > 0 ? (
        <ul class="box devices" aria-label="Devices">
          {devices.map((d) => (
            <DeviceLine key={d.id} device={d} />
          ))}
        </ul>
      ) : null}
    </>
  );
}
