import { signal } from '@preact/signals';
import { useState } from 'preact/hooks';
import { muted, setMuted } from './chime';
import { config } from './config';
import { Modal } from './Modal';
import { api } from './sources';

// The app Settings modal (#119): General and Chat. It edits a draft; Save applies it and Cancel drops it.
// The source settings of SettingsDialog.tsx are a different dialog.

const open = signal(false);
export const openAppSettings = () => (open.value = true);

const MODELS = ['sonnet', 'opus', 'haiku'];
const EFFORTS = ['low', 'medium', 'high'];

export function AppSettings() {
  if (!open.value) return null;
  return <Form onClose={() => (open.value = false)} />;
}

function Form({ onClose }: { onClose: () => void }) {
  const stored = config.value?.chat;
  const fleets = config.value?.fleets ?? [];
  const [fleet, setFleet] = useState(stored?.fleet ?? '');
  const [model, setModel] = useState(stored?.model ?? 'sonnet');
  const [effort, setEffort] = useState(stored?.effort ?? '');
  const [sound, setSound] = useState(!muted.value);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const defaultFleet = fleets.find((f) => f.docks.some((d) => d.harness === 'claude')) ?? fleets[0];
  const line = `"chat": {"fleet": "${fleet}", "model": "${model}"${effort ? `, "effort": "${effort}"` : ''}}`;

  async function save() {
    setSaving(true);
    // With no fleet there is nothing to write for the chat.
    if (fleet) {
      const r = await api('PUT', '/api/chat', { fleet, model, effort });
      if (!r.ok) {
        setError(r.error);
        setSaving(false);
        return;
      }
    }
    if (sound === muted.value) setMuted(!sound); // the draft disagrees with the stored state
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
        <section aria-labelledby="app-set-chat">
          <h3 id="app-set-chat">Chat</h3>
          <div class="app-set-field">
            <label for="app-set-fleet">Fleet</label>
            <select id="app-set-fleet" value={fleet} onChange={(e) => setFleet(e.currentTarget.value)}>
              {fleets.map((f) => (
                <option key={f.key} value={f.key}>
                  {f.name}
                  {f.key === defaultFleet?.key ? ' (default)' : ''}
                </option>
              ))}
            </select>
            {defaultFleet && <small>Default: {defaultFleet.name}, the first fleet with a Claude dock.</small>}
          </div>
          <div class="app-set-field">
            <label for="app-set-model">Model</label>
            <select id="app-set-model" value={model} onChange={(e) => setModel(e.currentTarget.value)}>
              {MODELS.map((m) => (
                <option key={m} value={m}>
                  {m}
                  {m === 'sonnet' ? ' (default)' : ''}
                </option>
              ))}
            </select>
          </div>
          <div class="app-set-field">
            <label for="app-set-effort">Effort</label>
            <select id="app-set-effort" value={effort} onChange={(e) => setEffort(e.currentTarget.value)}>
              <option value="">Harness default</option>
              {EFFORTS.map((x) => (
                <option key={x} value={x}>
                  {x}
                </option>
              ))}
            </select>
            <small>Harness default passes no effort flag.</small>
          </div>
          <div class="app-set-field">
            <span class="app-set-caption">Writes to config.json</span>
            <code class="app-set-line">{line}</code>
          </div>
        </section>
        {error && <div role="alert">{error}</div>}
      </div>
      <footer class="app-settings-foot">
        <button type="button" class="press app-set-cancel" onClick={onClose}>
          Cancel
        </button>
        <button type="button" class="press app-set-save" disabled={saving} onClick={() => void save()}>
          Save
        </button>
      </footer>
    </Modal>
  );
}
