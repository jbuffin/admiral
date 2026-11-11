import { useEffect, useState } from 'preact/hooks';
import { fetchSchema, fetchSettings, saveSource, type Source } from './api';
import { DEFAULT_VIEWS, UNCHANGED, buildSettings, fieldKinds, jsonText, parseWholeJson, replacedDefault, type Prop, type Schema, type Settings } from './sourceSettings';

type Row = Record<string, unknown>;

function Field({ id, label, hint, children }: { id: string; label: string; hint?: string; children: preact.ComponentChildren }) {
  return (
    <div class="set">
      <div class="t">
        <label for={id}>{label}</label>
        {hint ? <span id={`${id}-hint`}>{hint}</span> : null}
      </div>
      {children}
    </div>
  );
}

function RowFields({ label, prop, rows, onRows }: { label: string; prop: Prop; rows: unknown[]; onRows: (r: unknown[]) => void }) {
  const strings = prop.items?.type === 'string';
  const fields = Object.entries(prop.items?.properties ?? {});
  const setRow = (i: number, v: unknown) => onRows(rows.map((r, j) => (j === i ? v : r)));
  return (
    <fieldset class="rows">
      <legend>{label}</legend>
      {rows.map((row, i) => (
        <div class="item" key={i}>
          {strings ? (
            <input type="text" autocomplete="off" aria-label={`${label} ${i + 1}`} value={String(row ?? '')} onInput={(e) => setRow(i, (e.target as HTMLInputElement).value)} />
          ) : (
            fields.map(([fk, fp]) => {
              const cur = (row as Row)?.[fk];
              const num = fp.type === 'number' || fp.type === 'integer';
              const put = (v: unknown) => {
                const next = { ...(row as Row) };
                if (v === undefined) delete next[fk];
                else next[fk] = v;
                setRow(i, next);
              };
              if (fp.type === 'boolean') {
                return (
                  <label class="check" key={fk}>
                    <input type="checkbox" checked={cur === true} onChange={(e) => put((e.target as HTMLInputElement).checked)} />
                    <span>{fp.title ?? fk}</span>
                  </label>
                );
              }
              return (
                <label class="row-field" key={fk}>
                  <span>{fp.title ?? fk}</span>
                  <input
                    type={(num ? 'number' : 'text') as 'password'}
                    step={fp.type === 'integer' ? 1 : num ? 'any' : undefined}
                    value={cur === undefined ? '' : String(cur)}
                    onInput={(e) => {
                      const t = (e.target as HTMLInputElement).value;
                      put(t === '' ? undefined : num ? Number(t) : t);
                    }}
                  />
                </label>
              );
            })
          )}
          <button type="button" class="btn" aria-label={`Remove ${label} ${i + 1}`} onClick={() => onRows(rows.filter((_, j) => j !== i))}>
            Remove
          </button>
        </div>
      ))}
      <button type="button" class="btn" aria-label={`Add ${label}`} onClick={() => onRows([...rows, strings ? '' : {}])}>
        Add
      </button>
    </fieldset>
  );
}

function ViewsEditor({ rows, onRows }: { rows: Row[]; onRows: (r: Row[]) => void }) {
  const setRow = (i: number, k: 'title' | 'query', v: string) =>
    onRows(
      rows.map((r, j) => {
        if (j !== i) return r;
        const next = { ...r, [k]: v };
        if (v === '') delete next[k];
        return next;
      }),
    );
  return (
    <div class="views">
      <h4>Default views</h4>
      <ul class="defaults" aria-label="Default views">
        {DEFAULT_VIEWS.map((d) => (
          <li key={d.id}>
            <b>{d.title}</b>
            <code>{d.query}</code>
          </li>
        ))}
      </ul>
      <h4>Custom views</h4>
      {rows.map((r, i) => {
        const replaced = replacedDefault(String(r.title ?? ''));
        return (
          <fieldset class="view" key={i}>
            <legend>{`Custom view ${i + 1}`}</legend>
            <label class="row-field">
              <span>Title</span>
              <input type="text" autocomplete="off" value={String(r.title ?? '')} onInput={(e) => setRow(i, 'title', (e.target as HTMLInputElement).value)} />
            </label>
            <label class="row-field">
              <span>Query</span>
              <input type="text" autocomplete="off" value={String(r.query ?? '')} onInput={(e) => setRow(i, 'query', (e.target as HTMLInputElement).value)} />
            </label>
            {replaced ? (
              <p class="note" role="status">
                Replaces the default view {replaced.title}.
              </p>
            ) : null}
            <button type="button" class="btn" onClick={() => onRows(rows.filter((_, j) => j !== i))}>
              Remove view
            </button>
          </fieldset>
        );
      })}
      <button type="button" class="btn" onClick={() => onRows([...rows, {}])}>
        Add view
      </button>
    </div>
  );
}

const GITHUB_DEFAULTS: Record<string, string> = { host: 'github.com' };

const isRow = (r: unknown): r is Row => typeof r === 'object' && r !== null && !Array.isArray(r);

export function SettingsForm({ source, version, onSaved }: { source: Source; version: number; onSaved: () => void }) {
  const starting = source.state === 'starting';
  const [schema, setSchema] = useState<Schema | undefined>();
  const [stored, setStored] = useState<Settings>({});
  const [name, setName] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [edits, setEdits] = useState<Settings>({});
  const [whole, setWhole] = useState('{}');
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // A source that is still starting has no schema yet; the form waits, then reads the schema before the settings so they are never masked more than it expects.
  useEffect(() => {
    if (starting) return;
    let live = true;
    (async () => {
      let sch = schema;
      if (!sch && source.has_schema) {
        const r = await fetchSchema(source.key);
        if (r.ok && (r.data as Schema)?.properties) sch = r.data as Schema;
      }
      const cur = await fetchSettings(source.key);
      if (!live) return;
      setSchema(sch);
      if (cur.ok) {
        setStored(cur.data.settings);
        setName(cur.data.name);
        setWhole(jsonText(cur.data.settings));
        setError(null);
      } else setError(cur.error);
      setEdits({});
      setLoaded(true);
    })();
    return () => {
      live = false;
    };
  }, [source.key, source.has_schema, starting, version]);

  const kinds = fieldKinds(schema, stored);
  const touch = (k: string, v: unknown) => setEdits((cur) => ({ ...cur, [k]: v }));
  const edited = (k: string) => (k in edits ? edits[k] : stored[k]);

  const save = async (e: Event) => {
    e.preventDefault();
    const built = schema ? buildSettings(schema, stored, edits) : parseWholeJson(whole);
    if (!built.ok) return setError(built.error);
    setSaving(true);
    const r = await saveSource(source.key, { name, settings: built.settings });
    setSaving(false);
    if (r.ok) {
      setError(null);
      onSaved();
    } else setError(r.error);
  };

  const github = source.plugin === 'github';
  const fid = (k: string) => `${source.key}-${k}`;

  return (
    <form class="settings box" aria-label="Settings" onSubmit={save}>
      {!loaded ? <p class="note">{starting ? 'Waiting for the source to start…' : 'Loading…'}</p> : null}
      {loaded ? (
        <>
          <Field id={fid('name')} label="Name">
            <input id={fid('name')} type="text" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
          </Field>
          {schema ? (
            Object.entries(schema.properties ?? {}).map(([k, p]) => {
              const label = p.title ?? k;
              const { kind, stored: kept } = kinds[k];
              const id = fid(k);
              const value = edited(k);
              if (github && k === 'views') {
                const rows = (Array.isArray(value) ? value : []).filter(isRow);
                return <ViewsEditor key={k} rows={rows} onRows={(r) => touch(k, r)} />;
              }
              if (kind === 'enum') {
                return (
                  <Field key={k} id={id} label={label} hint={p.description}>
                    <select id={id} value={value === undefined ? '' : JSON.stringify(value)} onChange={(e) => {
                      const t = (e.target as HTMLSelectElement).value;
                      touch(k, t === '' ? undefined : JSON.parse(t));
                    }}>
                      <option value="" />
                      {(p.enum ?? []).map((o) => (
                        <option key={JSON.stringify(o)} value={JSON.stringify(o)}>
                          {String(o)}
                        </option>
                      ))}
                    </select>
                  </Field>
                );
              }
              if (kind === 'boolean') {
                return (
                  <Field key={k} id={id} label={label} hint={p.description}>
                    <input id={id} type="checkbox" checked={value === true} onChange={(e) => touch(k, (e.target as HTMLInputElement).checked)} />
                  </Field>
                );
              }
              if (kind === 'number') {
                return (
                  <Field key={k} id={id} label={label} hint={p.description}>
                    <input
                      id={id}
                      type="number"
                      step={p.type === 'integer' ? 1 : 'any'}
                      value={value === undefined ? '' : String(value)}
                      onInput={(e) => {
                        const t = (e.target as HTMLInputElement).value;
                        touch(k, t === '' ? undefined : Number(t));
                      }}
                    />
                  </Field>
                );
              }
              if (kind === 'text') {
                const secret = p.format?.toLowerCase() === 'password' || p.writeOnly === true || kept;
                // The built-in source's own default, shown so the field is not a blank that means github.com.
                const shown = value === undefined && github ? GITHUB_DEFAULTS[k] : value;
                const cleared = kept && k in edits && edits[k] === undefined;
                return (
                  <Field key={k} id={id} label={label} hint={p.description}>
                    <div class="control">
                      <input
                        id={id}
                        type={(secret ? 'password' : 'text') as 'password'}
                        autocomplete="off"
                        aria-describedby={p.description ? `${id}-hint` : undefined}
                        placeholder={kept ? (cleared ? 'Cleared' : 'Stored') : undefined}
                        value={typeof shown === 'string' && shown !== UNCHANGED ? shown : ''}
                        onInput={(e) => touch(k, (e.target as HTMLInputElement).value)}
                      />
                      {kept ? <small>{cleared ? 'Saved without a value.' : 'A value is stored. Leave empty to keep it.'}</small> : null}
                      {kept && !cleared ? (
                        <button type="button" class="btn" aria-label={`Clear ${label}`} onClick={() => touch(k, undefined)}>
                          Clear
                        </button>
                      ) : null}
                    </div>
                  </Field>
                );
              }
              if (kind === 'strings' || kind === 'objects') {
                return <RowFields key={k} label={label} prop={p} rows={Array.isArray(value) ? value : []} onRows={(r) => touch(k, r)} />;
              }
              const text = typeof edits[k] === 'string' ? (edits[k] as string) : jsonText(stored[k]);
              return (
                <Field key={k} id={id} label={label} hint={p.description}>
                  <div class="control">
                    <textarea id={id} rows={3} spellcheck={false} value={text} onInput={(e) => touch(k, (e.target as HTMLTextAreaElement).value)} />
                    {text.includes(UNCHANGED) ? <small>{UNCHANGED} keeps a stored value.</small> : null}
                  </div>
                </Field>
              );
            })
          ) : (
            <Field id={fid('whole')} label="Settings JSON">
              <div class="control">
                <textarea id={fid('whole')} rows={8} spellcheck={false} value={whole} onInput={(e) => setWhole((e.target as HTMLTextAreaElement).value)} />
                {whole.includes(UNCHANGED) ? <small>{UNCHANGED} keeps a stored value.</small> : null}
              </div>
            </Field>
          )}
        </>
      ) : null}
      {error ? (
        <p class="note error" role="alert">
          {error}
        </p>
      ) : null}
      <div class="set actions">
        <button type="submit" class="btn primary" disabled={!loaded || saving}>
          Save
        </button>
      </div>
    </form>
  );
}
