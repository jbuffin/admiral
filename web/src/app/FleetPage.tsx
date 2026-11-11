import { useState } from 'preact/hooks';
import { saveFleet, type Fleet } from './api';
import { Flag } from './Flag';
import { fleetPaint, fleets, loadFleets } from './fleets';
import { errorTarget, fieldMessage, saveBody, type FleetFields } from './fleetSettings';

const fieldsOf = (f: Fleet): FleetFields => ({ search_roots: f.search_roots, owners: f.owners, clone_root: f.clone_root, color: f.color });

function ListField({ id, legend, item, add, values, errors, disabled, onChange }: {
  id: string;
  legend: string;
  item: string;
  add: string;
  values: string[];
  errors: Map<number, string>;
  disabled: boolean;
  onChange: (next: string[]) => void;
}) {
  return (
    <div class="rows list-field" role="group" aria-labelledby={`${id}-legend`}>
      <span class="legend" id={`${id}-legend`}>
        {legend}
      </span>
      {values.map((v, i) => {
        const err = errors.get(i);
        return (
          <div class="row-field" key={i}>
            <input
              type="text"
              aria-label={`${item} ${i + 1}`}
              value={v}
              disabled={disabled}
              aria-invalid={err ? true : undefined}
              aria-describedby={err ? `${id}-${i}-err` : undefined}
              onInput={(e) => onChange(values.map((x, j) => (j === i ? (e.target as HTMLInputElement).value : x)))}
            />
            <button type="button" class="btn" disabled={disabled} aria-label={`Remove ${item.toLowerCase()} ${i + 1}`} onClick={() => onChange(values.filter((_, j) => j !== i))}>
              Remove
            </button>
            {err ? (
              <p class="note error" role="alert" id={`${id}-${i}-err`}>
                {err}
              </p>
            ) : null}
          </div>
        );
      })}
      <button type="button" class="btn" disabled={disabled} onClick={() => onChange([...values, ''])}>
        {add}
      </button>
    </div>
  );
}

export function FleetPage({ fleet }: { fleet: Fleet }) {
  const [shown, setShown] = useState(() => fieldsOf(fleet));
  const [edited, setEdited] = useState(shown);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ message: string; status?: number } | null>(null);

  const body = saveBody(shown, edited);
  const target = error ? errorTarget(error.message, fleet.key) : null;
  const listErrors = (field: 'search_roots' | 'owners') =>
    new Map(target?.field === field && target.index !== undefined && error ? [[target.index, fieldMessage(error.message, fleet.key)]] : []);
  const fieldError = (field: 'clone_root' | 'color') => (target?.field === field && error ? fieldMessage(error.message, fleet.key) : null);
  const topError = error && !target ? error : null;
  const set = (patch: Partial<FleetFields>) => {
    // An error names a field (and a list slot); once that field changes it no longer points at what it said.
    if (target && target.field in patch) setError(null);
    setEdited({ ...edited, ...patch });
  };

  const reset = (f: Fleet) => {
    const next = fieldsOf(f);
    setShown(next);
    setEdited(next);
  };

  const save = async () => {
    setBusy(true);
    setError(null);
    const r = await saveFleet(fleet.key, body);
    if (r.ok) {
      fleets.value = r.data;
      const f = r.data.find((x) => x.key === fleet.key);
      if (f) reset(f);
    } else {
      setError({ message: r.error, status: r.status });
    }
    setBusy(false);
  };

  const reload = async () => {
    await loadFleets();
    const f = fleets.value.find((x) => x.key === fleet.key);
    if (f) reset(f);
    setError(null);
  };

  const colorError = fieldError('color');
  const cloneError = fieldError('clone_root');

  return (
    <>
      {topError ? (
        <p class="note error" role="alert">
          {topError.status === 409 ? 'The config file changed or is invalid, so nothing was saved. ' : ''}
          {topError.message}{' '}
          {topError.status === 409 ? (
            <button type="button" class="btn" onClick={() => void reload()}>
              Reload
            </button>
          ) : null}
        </p>
      ) : null}
      <div class="box">
        <ListField id="roots" legend="Search roots" item="Search root" add="Add search root" values={edited.search_roots} errors={listErrors('search_roots')} disabled={busy} onChange={(v) => set({ search_roots: v })} />
        {!edited.search_roots.some((r) => r.trim()) ? (
          <div class="set">
            <p class="note warning" role="status">
              With no search root this fleet can’t launch ships.
            </p>
          </div>
        ) : null}
        <ListField id="owners" legend="Owners" item="Owner" add="Add owner" values={edited.owners} errors={listErrors('owners')} disabled={busy} onChange={(v) => set({ owners: v })} />
        <div class="set">
          <div class="t">
            <label for="clone-root">Clone root</label>
            <span>Where this fleet clones repositories. Absolute, or starting with ~/. Empty for none.</span>
          </div>
          <div class="control">
            <input
              id="clone-root"
              type="text"
              value={edited.clone_root}
              disabled={busy}
              aria-invalid={cloneError ? true : undefined}
              aria-describedby={cloneError ? 'clone-root-err' : undefined}
              onInput={(e) => set({ clone_root: (e.target as HTMLInputElement).value })}
            />
            {cloneError ? (
              <p class="note error" role="alert" id="clone-root-err">
                {cloneError}
              </p>
            ) : null}
          </div>
        </div>
        <div class="set">
          <div class="t">
            <label for="fleet-color">Color</label>
            <span>{edited.color === '' ? 'Saving gives this fleet its default colour.' : 'Pick a colour for this fleet.'}</span>
          </div>
          <div class="control color-control">
            <span
              class={edited.color === '' ? 'swatch swatch-default' : 'swatch'}
              role="img"
              aria-label={edited.color === '' ? 'Default colour' : 'Fleet colour'}
              style={edited.color === '' ? undefined : { background: edited.color === shown.color ? fleetPaint(fleet).fill : edited.color }}
            />
            <input id="fleet-color" type="color" value={edited.color || shown.color} disabled={busy} aria-describedby={colorError ? 'fleet-color-err' : undefined} onInput={(e) => set({ color: (e.target as HTMLInputElement).value })} />
            <button type="button" class="btn" disabled={busy} onClick={() => set({ color: '' })}>
              Use default
            </button>
            {colorError ? (
              <p class="note error" role="alert" id="fleet-color-err">
                {colorError}
              </p>
            ) : null}
          </div>
        </div>
        <div class="set actions">
          <button type="button" class="btn primary" disabled={busy || Object.keys(body).length === 0} onClick={() => void save()}>
            Save
          </button>
        </div>
      </div>

      <h2 class="section-title" id="docks-title">
        Docks
      </h2>
      <p class="line">Docks and env are changed in <code>admiral setup</code>.</p>
      <ul class="docks box" aria-label="Docks">
        {fleet.docks.map((d) => (
          <li key={d.harness} class="dock">
            <Flag harness={d.harness} size="row" />
            <div class="dock-id">
              <span class="dock-name">{d.harness}</span>
              <span class="source-meta">
                {d.executable || 'default executable'} · {d.executable_found ? 'found' : 'not found'}
              </span>
              {d.warnings.map((w) => (
                <p class="note warning" role="status" key={w}>
                  {w}
                </p>
              ))}
            </div>
          </li>
        ))}
      </ul>
    </>
  );
}
