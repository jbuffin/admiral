import { useEffect, useState } from 'preact/hooks';
import { Modal } from './Modal';
import { openSettings } from './SettingsDialog';
import { type PluginInfo, type Source, api } from './sources';

export function AddSource() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" class="add-source press" onClick={() => setOpen(true)}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" aria-hidden="true">
          <path d="M12 5v14M5 12h14" />
        </svg>
        Add source plugin
      </button>
      {open && <PluginList onClose={() => setOpen(false)} />}
    </>
  );
}

// Choosing a plugin adds a source with empty settings and opens its settings,
// because the form only exists once the source runs.
function PluginList({ onClose }: { onClose: () => void }) {
  const [plugins, setPlugins] = useState<PluginInfo[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    api<PluginInfo[]>('GET', '/api/plugins').then((r) => (r.ok ? setPlugins(r.data) : setError(r.error)));
  }, []);
  const add = async (name: string) => {
    const r = await api<Source>('POST', '/api/sources', { plugin: name });
    if (!r.ok) return setError(r.error);
    onClose();
    openSettings(r.data.key);
  };
  return (
    <Modal label="Add source plugin" onClose={onClose}>
      <h3>Add source plugin</h3>
      {error && <p role="alert">{error}</p>}
      {plugins && plugins.length === 0 && <p>No plugins are installed. Copy one into the plugins folder.</p>}
      <ul class="plugin-list">
        {(plugins ?? []).map((p) => (
          <li key={p.name}>
            <button type="button" class="plugin press" disabled={!p.valid} onClick={() => add(p.name)}>
              {p.icon_url ? <img src={p.icon_url} alt="" width="32" height="32" /> : <span class="source-dot" aria-hidden="true">{p.name.charAt(0).toUpperCase()}</span>}
              <span class="plugin-text">
                <strong>{p.name}</strong>
                <span>{p.valid ? p.description : p.error}</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
      <button type="button" class="small-button press" onClick={onClose}>
        Close
      </button>
    </Modal>
  );
}
