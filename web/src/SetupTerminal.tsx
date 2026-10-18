import { useRef, useState } from 'preact/hooks';
import { KEYS, ENTER_GAP_MS } from './Bridge';
import { signedOut } from './connection';
import { closeSetup, probeSetup, SETUP_SESSION, setupOpen } from './setup';
import { useBridgePlacement } from './useBridgePlacement';
import { useBridgeTerminal } from './useBridgeTerminal';

// The setup terminal: `admiral setup` in a bridge-style terminal over open water. It is not a ship, so
// there is no flotilla, no flag and no status pill; the keys are the bridge's.
export function SetupTerminal() {
  return setupOpen.value ? <SetupView /> : null;
}

function SetupView() {
  const [draft, setDraft] = useState('');
  const root = useRef<HTMLDivElement>(null);
  useBridgePlacement(root, true);
  const { host, link, linkState, activity } = useBridgeTerminal({ session: SETUP_SESSION, open: true, gone: closeSetup, probe: probeSetup });
  const reconnecting = linkState === 'reconnecting';
  const canType = linkState === 'open';
  // Anything typed or pressed makes this browser the one that sizes the pane.
  const input = (data: string) => {
    activity.current();
    link.current?.send({ type: 'input', data });
  };
  const submit = (e: Event) => {
    e.preventDefault();
    const text = draft;
    setDraft('');
    if (text) {
      input(text);
      setTimeout(() => input('\r'), ENTER_GAP_MS);
    } else input('\r');
  };

  return (
    <div class="bridge" ref={root}>
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
          <button type="button" class="press term-close" aria-label="Close setup terminal" onClick={() => (setupOpen.value = false)}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="3" stroke-linecap="round" aria-hidden="true">
              <path d="M6 6l12 12M18 6L6 18" />
            </svg>
          </button>
        </div>

        <div class={reconnecting ? 'term-body dim' : 'term-body'}>
          <div class="term-host" ref={host} />
          <form class="term-prompt" onSubmit={submit}>
            <label for="adm-setup-cmd" class="sr-only">
              Input to setup
            </label>
            <span aria-hidden="true" class="term-caret">
              ❯
            </span>
            <input
              id="adm-setup-cmd"
              class="term-in"
              value={draft}
              disabled={!canType}
              onInput={(e) => setDraft((e.target as HTMLInputElement).value)}
              autocomplete="off"
              autocapitalize="off"
              spellcheck={false}
              placeholder="type here and press Enter"
            />
          </form>
        </div>

        <div class="term-keys">
          {KEYS.map(([name, label, bytes]) => (
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
