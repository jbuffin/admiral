import { signal } from '@preact/signals';
import { useEffect, useState } from 'preact/hooks';
import { Modal } from './Modal';
import { api, sources, sourcePath } from './sources';

const settingsFor = signal<string | null>(null);
export const openSettings = (key: string) => (settingsFor.value = key);

interface Prop {
  type?: string;
  title?: string;
  description?: string;
  format?: string;
  enum?: unknown[];
}
interface Schema {
  properties?: Record<string, Prop>;
  required?: string[];
}

const simple = (p: Prop) => (p.enum ? true : ['string', 'number', 'integer', 'boolean'].includes(p.type ?? ''));

export function SettingsDialog() {
  const key = settingsFor.value;
  if (!key) return null;
  return <SettingsForm key={key} sourceKey={key} onClose={() => (settingsFor.value = null)} />;
}

function SettingsForm({ sourceKey, onClose }: { sourceKey: string; onClose: () => void }) {
  const [loaded, setLoaded] = useState(false);
  const [name, setName] = useState('');
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [json, setJson] = useState<Record<string, string>>({}); // text of properties the form cannot draw
  const [schema, setSchema] = useState<Schema | null>(null);
  const [whole, setWhole] = useState('{}'); // one JSON field when there is no schema
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    (async () => {
      const cur = await api<{ name: string; settings: Record<string, unknown> }>('GET', sourcePath(sourceKey, '/settings'));
      // A source that is still starting has no schema yet: ask again while it does.
      let sch = await api<Schema>('GET', sourcePath(sourceKey, '/schema'));
      for (let i = 0; i < 20 && live && !sch.ok; i++) {
        const state = sources.value?.find((s) => s.key === sourceKey)?.state;
        if (state !== 'starting') break;
        await new Promise((r) => setTimeout(r, 250));
        sch = await api<Schema>('GET', sourcePath(sourceKey, '/schema'));
      }
      if (!live) return;
      if (!cur.ok) setError(cur.error);
      const settings = cur.ok ? cur.data.settings : {};
      setName(cur.ok ? cur.data.name : '');
      setValues(settings);
      setWhole(JSON.stringify(settings, null, 2));
      if (sch.ok && sch.data.properties) {
        setSchema(sch.data);
        const text: Record<string, string> = {};
        for (const [k, p] of Object.entries(sch.data.properties)) if (!simple(p)) text[k] = settings[k] === undefined ? '' : JSON.stringify(settings[k]);
        setJson(text);
      }
      setLoaded(true);
    })();
    return () => {
      live = false;
    };
  }, [sourceKey]);

  const save = async (e: Event) => {
    e.preventDefault();
    let settings: Record<string, unknown>;
    try {
      if (schema) {
        settings = { ...values };
        for (const [k, t] of Object.entries(json)) {
          if (t.trim() === '') delete settings[k];
          else settings[k] = JSON.parse(t);
        }
      } else {
        settings = JSON.parse(whole);
        if (settings === null || typeof settings !== 'object' || Array.isArray(settings)) throw new Error('not an object');
      }
    } catch {
      return setError('The settings must be valid JSON' + (schema ? '' : ' and an object') + '.');
    }
    const r = await api('PUT', sourcePath(sourceKey), { name, settings });
    if (r.ok) onClose();
    else setError(r.error);
  };

  const set = (k: string, v: unknown) => setValues((cur) => ({ ...cur, [k]: v }));
  return (
    <Modal label="Settings" onClose={onClose}>
      <form class="settings" onSubmit={save}>
        <h3>Settings</h3>
        {!loaded && <p>Loading…</p>}
        {loaded && (
          <>
            <label>
              <span>Name</span>
              <input type="text" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
            </label>
            {schema ? (
              Object.entries(schema.properties ?? {}).map(([k, p]) => (
                <Field key={k} id={k} prop={p} required={!!schema.required?.includes(k)} value={values[k]} text={json[k]} onValue={(v) => set(k, v)} onText={(t) => setJson((c) => ({ ...c, [k]: t }))} />
              ))
            ) : (
              <label>
                <span>Settings JSON</span>
                <textarea rows={8} value={whole} onInput={(e) => setWhole((e.target as HTMLTextAreaElement).value)} />
              </label>
            )}
          </>
        )}
        {error && <p role="alert">{error}</p>}
        <div class="row">
          <button type="submit" class="small-button press" disabled={!loaded}>
            Save
          </button>
          <button type="button" class="small-button press" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

function Field(props: { id: string; prop: Prop; required: boolean; value: unknown; text?: string; onValue: (v: unknown) => void; onText: (t: string) => void }) {
  const { id, prop, required, value } = props;
  const label = prop.title ?? id;
  const hint = prop.description && <small>{prop.description}</small>;
  if (prop.enum) {
    return (
      <label>
        <span>{label}</span>
        <select
          value={value === undefined ? '' : JSON.stringify(value)}
          required={required}
          onChange={(e) => {
            const t = (e.target as HTMLSelectElement).value;
            props.onValue(t === '' ? undefined : JSON.parse(t));
          }}
        >
          {!required && <option value="" />}
          {prop.enum.map((o) => (
            <option key={JSON.stringify(o)} value={JSON.stringify(o)}>
              {String(o)}
            </option>
          ))}
        </select>
        {hint}
      </label>
    );
  }
  if (prop.type === 'boolean') {
    return (
      <label class="check">
        <input type="checkbox" checked={value === true} onChange={(e) => props.onValue((e.target as HTMLInputElement).checked)} />
        <span>{label}</span>
        {hint}
      </label>
    );
  }
  if (prop.type === 'number' || prop.type === 'integer') {
    return (
      <label>
        <span>{label}</span>
        <input
          type="number"
          step={prop.type === 'integer' ? 1 : 'any'}
          required={required}
          value={value === undefined ? '' : String(value)}
          onInput={(e) => {
            const t = (e.target as HTMLInputElement).value;
            props.onValue(t === '' ? undefined : Number(t));
          }}
        />
        {hint}
      </label>
    );
  }
  if (prop.type === 'string') {
    return (
      <label>
        <span>{label}</span>
        <input type={(prop.format === 'password' ? 'password' : 'text') as 'password'} autocomplete="off" required={required} value={typeof value === 'string' ? value : ''} onInput={(e) => props.onValue((e.target as HTMLInputElement).value)} />
        {hint}
      </label>
    );
  }
  // Anything else is edited as JSON.
  return (
    <label>
      <span>{label}</span>
      <textarea rows={3} value={props.text ?? ''} onInput={(e) => props.onText((e.target as HTMLTextAreaElement).value)} />
      {hint}
    </label>
  );
}
