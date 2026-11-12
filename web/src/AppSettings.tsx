import { signal } from '@preact/signals';
import { useState } from 'preact/hooks';
import { muted, setMuted } from './chime';
import { Modal } from './Modal';

const open = signal(false);
export { open as appSettingsOpen };
let returnTo: HTMLElement | null = null;
export function openAppSettings(from?: HTMLElement | null): void {
  returnTo = from ?? null;
  open.value = true;
}

// A tap leaves WebKit's focus on <body>, so the dialog's own restore has nothing to return to.
const GEAR_ID = 'app-settings-gear';
export { GEAR_ID };
function close() {
  open.value = false;
  const from = returnTo;
  returnTo = null;
  setTimeout(() => (from?.isConnected ? from : document.getElementById(GEAR_ID))?.focus(), 0);
}

export function AppSettings() {
  if (!open.value) return null;
  return <Form onClose={close} />;
}

function Form({ onClose }: { onClose: () => void }) {
  const [sound, setSound] = useState(!muted.value);

  function save() {
    if (sound === muted.value) setMuted(!sound);
    onClose();
  }

  return (
    <Modal label="Settings" variant="app-settings" onClose={onClose}>
      <header class="app-settings-head">
        <h2>Settings</h2>
        <button type="button" class="press app-settings-x" aria-label="Close settings" onClick={onClose}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" aria-hidden="true">
            <path d="M6 6l12 12M18 6L6 18" />
          </svg>
        </button>
      </header>
      <div class="app-settings-body">
        <section aria-labelledby="app-set-general">
          <h3 id="app-set-general">General</h3>
          <div class="app-set-row">
            <span class="app-set-text">
              <strong>Attention sounds</strong>
              <small>A chime when a ship needs you.</small>
            </span>
            <button
              type="button"
              class="press app-set-sound"
              aria-label={sound ? 'Mute attention sounds' : 'Turn attention sounds on'}
              onClick={() => setSound(!sound)}
            >
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#F4EEDF" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M4 9h4l5-4v14l-5-4H4z" />
                <path d={sound ? 'M16.5 8.5a5 5 0 0 1 0 7M19 6a8.5 8.5 0 0 1 0 12' : 'M16 9l5 6M21 9l-5 6'} />
              </svg>
              {sound ? 'Sound on' : 'Muted'}
            </button>
          </div>
        </section>
      </div>
      <footer class="app-settings-foot">
        <button type="button" class="press app-set-cancel" onClick={onClose}>
          Cancel
        </button>
        <button type="button" class="press app-set-save" onClick={save}>
          Save
        </button>
      </footer>
    </Modal>
  );
}
