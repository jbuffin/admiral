import { useRef, useState } from 'preact/hooks';
import { FullViewToggle, useCloseOutside } from './Bridge';
import { signedOut } from './connection';
import { KEYS, touchKeyboard } from './keys';
import { closeSetup, probeSetup, SETUP_SESSION, setupOpen } from './setup';
import { useBridgePlacement } from './useBridgePlacement';
import { useBridgeTerminal } from './useBridgeTerminal';

// The setup terminal: `admiral setup` in a bridge-style terminal over open water. It is not a ship, so
// there is no flotilla, no flag and no status pill; the keys and full view are the bridge's. The bar
// keeps its note on every device, and its keys on touch screens only.
export function SetupTerminal() {
  return setupOpen.value ? <SetupView /> : null;
}

function SetupView() {
  const [full, setFull] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const closeOutside = useCloseOutside(() => (setupOpen.value = false));
  useBridgePlacement(root, true, full);
  const { host, link, linkState, activity } = useBridgeTerminal({ session: SETUP_SESSION, open: true, gone: closeSetup, probe: probeSetup });
  const reconnecting = linkState === 'reconnecting';
  const canType = linkState === 'open';
  // Anything typed or pressed makes this browser the one that sizes the pane.
  const input = (data: string) => {
    activity.current();
    link.current?.send({ type: 'input', data });
  };

  return (
    <div class={full ? 'bridge full' : 'bridge'} ref={root} {...closeOutside}>
      <section class="terminal" aria-label="Setup terminal">
        <div class="term-head">
          <div class="term-title">
            <span class="term-name">Fleet setup</span>
            <span class="term-sub">admiral setup · answers go straight to the terminal</span>
          </div>
          {signedOut.value ? (
            <span class="term-notice">Open the URL Admiral printed</span>
          ) : (
            reconnecting && <span class="term-notice">Reconnecting…</span>
          )}
          <FullViewToggle full={full} set={setFull} />
          <button type="button" class="press term-close" aria-label="Close setup terminal" onClick={() => (setupOpen.value = false)}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="3" stroke-linecap="round" aria-hidden="true">
              <path d="M6 6l12 12M18 6L6 18" />
            </svg>
          </button>
        </div>

        <div class={reconnecting ? 'term-body dim' : 'term-body'}>
          <div class="term-host" ref={host} />
        </div>

        <div class="term-keys">
          {touchKeyboard() &&
            KEYS.map(([name, label, bytes]) => (
              <button type="button" class="term-key" aria-label={`Send ${name}`} disabled={!canType} onClick={() => input(bytes)}>
                {label}
              </button>
            ))}
          <span class="term-grow" />
          <span class="term-note">Closing this leaves setup running; tap the fleet count to come back</span>
        </div>
      </section>
    </div>
  );
}
