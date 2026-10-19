import { signal } from '@preact/signals';
import { useEffect, useState } from 'preact/hooks';
import { Modal } from './Modal';
import { api, sources, sourcePath } from './sources';

// What the server sends in place of a stored secret, and takes back to keep it.
const UNCHANGED = '__admiral_unchanged__';

const settingsFor = signal<string | null>(null);
export const openSettings = (key: string) => (settingsFor.value = key);

interface Prop {
  type?: string;
  title?: string;
  description?: string;
  format?: string;
  writeOnly?: boolean;
  enum?: unknown[];
  items?: Prop;
  properties?: Record<string, Prop>;
}
interface Schema {
  properties?: Record<string, Prop>;
  required?: string[];
}

const MarkerHint = () => <small>{UNCHANGED} keeps a stored value.</small>;

const scalar = (p: Prop) => ['string', 'number', 'integer', 'boolean'].includes(p.type ?? '');
const simple = (p: Prop) => !!p.enum || scalar(p);
const flaggedSecret = (p: Prop) => p.format?.toLowerCase() === 'password' || p.writeOnly === true;
// Lists of secrets stay JSON: a row field cannot tell a kept secret from a cleared one.
const isStrings = (p: Prop) => p.type === 'array' && p.items?.type === 'string' && !p.items.enum && !flaggedSecret(p) && !flaggedSecret(p.items);
const isObjects = (p: Prop) =>
  p.type === 'array' && p.items?.type === 'object' && !!p.items.properties && !flaggedSecret(p) && !flaggedSecret(p.items) && Object.values(p.items.properties).every((f) => scalar(f) && !f.enum && !flaggedSecret(f));
// Whether a list property is drawn as rows. Decided once at load, from the schema and the stored value:
// a value that is not an array, or that holds a masked value, stays a JSON field so it round-trips untouched.
const asRows = (p: Prop, v: unknown) => (isStrings(p) || isObjects(p)) && (v === undefined || (Array.isArray(v) && !JSON.stringify(v).includes(UNCHANGED)));

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
  const [stored, setStored] = useState<Record<string, boolean>>({}); // properties that hold a secret the server kept
  const [schema, setSchema] = useState<Schema | null>(null);
  const [whole, setWhole] = useState('{}'); // one JSON field when there is no schema
  const [error, setError] = useState<string | null>(null);

  // The form is read again when the source's schema appears while it is open.
  const hasSchema = !!sources.value?.find((s) => s.key === sourceKey)?.has_schema;

  useEffect(() => {
    let live = true;
    (async () => {
      // A source that is still starting has no schema yet: wait for it to settle.
      for (let i = 0; i < 20 && live; i++) {
        const state = sources.value?.find((s) => s.key === sourceKey)?.state;
        if (state && state !== 'starting') break;
        await new Promise((r) => setTimeout(r, 250));
      }
      if (!live) return;
      // The schema first, so the settings are never masked more than the form expects.
      // Without one there is nothing to ask for: the JSON field is the form.
      const known = !!sources.value?.find((s) => s.key === sourceKey)?.has_schema;
      const sch = known ? await api<Schema>('GET', sourcePath(sourceKey, '/schema')) : { ok: false as const, error: '' };
      const cur = await api<{
        name: string;
        settings: Record<string, unknown>;
      }>('GET', sourcePath(sourceKey, '/settings'));
      if (!live) return;
      if (!cur.ok) setError(cur.error);
      const settings = cur.ok ? cur.data.settings : {};
      setName(cur.ok ? cur.data.name : '');
      setValues(settings);
      setStored(Object.fromEntries(Object.entries(settings).map(([k, v]) => [k, v === UNCHANGED])));
      setWhole(JSON.stringify(settings, null, 2));
      if (sch.ok && sch.data.properties) {
        setSchema(sch.data);
        const text: Record<string, string> = {};
        for (const [k, p] of Object.entries(sch.data.properties)) if (!simple(p) && !asRows(p, settings[k])) text[k] = settings[k] === undefined ? '' : JSON.stringify(settings[k]);
        setJson(text);
      }
      setLoaded(true);
    })();
    return () => {
      live = false;
    };
  }, [sourceKey, hasSchema]);

  const save = async (e: Event) => {
    e.preventDefault();
    let settings: Record<string, unknown>;
    try {
      if (schema) {
        settings = { ...values };
        for (const [k, p] of Object.entries(schema.properties ?? {})) {
          if (!(isStrings(p) || isObjects(p)) || k in json) continue;
          // Rows nothing was typed into are dropped; no rows left deletes the key.
          const rows = (Array.isArray(settings[k]) ? (settings[k] as unknown[]) : []).filter((r) => (typeof r === 'string' ? r !== '' : typeof r === 'object' && r !== null && Object.keys(r).length > 0));
          if (rows.length === 0) delete settings[k];
          else settings[k] = rows;
        }
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
                <Field
                  key={k}
                  id={k}
                  prop={p}
                  required={!!schema.required?.includes(k)}
                  stored={stored[k]}
                  value={values[k]}
                  text={json[k]}
                  onValue={(v) => set(k, v)}
                  onText={(t) => setJson((c) => ({ ...c, [k]: t }))}
                />
              ))
            ) : (
              <label>
                <span>Settings JSON</span>
                <textarea rows={8} value={whole} onInput={(e) => setWhole((e.target as HTMLTextAreaElement).value)} />
                {whole.includes(UNCHANGED) && <MarkerHint />}
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

function Field(props: { id: string; prop: Prop; required: boolean; stored?: boolean; value: unknown; text?: string; onValue: (v: unknown) => void; onText: (t: string) => void }) {
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
          <option value="" />
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
    // A kept secret is never drawn: the field starts empty, and stays "unchanged" until typed into.
    const secret = prop.format === 'password';
    const kept = secret && value === UNCHANGED;
    const cleared = props.stored && secret && value === undefined;
    return (
      <>
        <label>
          <span>{label}</span>
          <input
            type={(secret ? 'password' : 'text') as 'password'}
            autocomplete="off"
            required={required && !props.stored}
            placeholder={cleared ? 'Cleared' : props.stored ? 'Unchanged' : undefined}
            value={typeof value === 'string' && !kept ? value : ''}
            onInput={(e) => {
              const t = (e.target as HTMLInputElement).value;
              props.onValue(t === '' && props.stored ? UNCHANGED : t === '' && secret ? undefined : t);
            }}
          />
          {props.stored && secret && <small>{cleared ? 'Saved without a value.' : 'Leave empty to keep the stored value.'}</small>}
          {hint}
        </label>
        {props.stored && secret && !required && !cleared && (
          <button type="button" class="small-button press" aria-label={`Clear ${label}`} onClick={() => props.onValue(undefined)}>
            Clear
          </button>
        )}
      </>
    );
  }
  if ((isStrings(prop) || isObjects(prop)) && props.text === undefined) {
    const rows = Array.isArray(value) ? (value as unknown[]) : [];
    const strings = isStrings(prop);
    const fields = Object.entries(prop.items?.properties ?? {});
    const setRow = (i: number, v: unknown) => props.onValue(rows.map((r, j) => (j === i ? v : r)));
    return (
      <fieldset class="rows">
        <legend>{label}</legend>
        {hint}
        {rows.map((row, i) => (
          <div class="item" key={i}>
            {strings ? (
              <input type="text" autocomplete="off" aria-label={`${label} ${i + 1}`} value={String(row ?? '')} onInput={(e) => setRow(i, (e.target as HTMLInputElement).value)} />
            ) : (
              fields.map(([fk, fp]) => {
                const cur = (row as Record<string, unknown>)?.[fk];
                const put = (v: unknown) => {
                  const next = { ...(row as Record<string, unknown>) };
                  if (v === undefined) delete next[fk];
                  else next[fk] = v;
                  setRow(i, next);
                };
                const fl = fp.title ?? fk;
                const fh = fp.description && <small>{fp.description}</small>;
                if (fp.type === 'boolean') {
                  return (
                    <label class="check" key={fk}>
                      <input type="checkbox" checked={cur === true} onChange={(e) => put((e.target as HTMLInputElement).checked)} />
                      <span>{fl}</span>
                      {fh}
                    </label>
                  );
                }
                const num = fp.type === 'number' || fp.type === 'integer';
                return (
                  <label key={fk}>
                    <span>{fl}</span>
                    <input
                      type={(num ? 'number' : 'text') as 'password'}
                      step={fp.type === 'integer' ? 1 : num ? 'any' : undefined}
                      value={cur === undefined ? '' : String(cur)}
                      onInput={(e) => {
                        const t = (e.target as HTMLInputElement).value;
                        put(t === '' ? undefined : num ? Number(t) : t);
                      }}
                    />
                    {fh}
                  </label>
                );
              })
            )}
            <button type="button" class="small-button press" aria-label={`Remove ${label} ${i + 1}`} onClick={() => props.onValue(rows.filter((_, j) => j !== i))}>
              Remove
            </button>
          </div>
        ))}
        <button type="button" class="small-button press" aria-label={`Add ${label}`} onClick={() => props.onValue([...rows, strings ? '' : {}])}>
          Add
        </button>
      </fieldset>
    );
  }
  // Anything else is edited as JSON.
  return (
    <label>
      <span>{label}</span>
      <textarea rows={3} value={props.text ?? ''} onInput={(e) => props.onText((e.target as HTMLTextAreaElement).value)} />
      {props.text?.includes(UNCHANGED) && <MarkerHint />}
      {hint}
    </label>
  );
}
