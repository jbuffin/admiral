import { useEffect, useRef, useState } from 'preact/hooks';
import { addSource, fetchPlugins, fetchSources, iconSrc, removeSource, restartSource, saveSource, type PluginInfo, type Source } from './api';
import { fleetPaint, fleets } from './fleets';
import type { Fleet } from './api';
import { SettingsForm } from './SettingsForm';

const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

function Badge({ source }: { source: Source }) {
  const [failed, setFailed] = useState(false);
  return (
    <span class="badge" aria-hidden="true">
      {source.icon_url && !failed ? <img src={iconSrc(source.icon_url)} alt="" onError={() => setFailed(true)} /> : (source.name || source.key)[0].toUpperCase()}
    </span>
  );
}

function SourceCard({ source, version, reload, bump, restart }: { source: Source; version: number; reload: () => Promise<void>; bump: (key: string) => void; restart: (key: string) => Promise<{ ok: boolean; error?: string }> }) {
  const github = source.plugin === 'github';
  const fleet = fleets.value.find((f) => f.key === source.fleet);
  const others = fleets.value.filter((f) => f.key !== source.fleet);
  const [target, setTarget] = useState(others[0]?.key ?? '');
  const [busy, setBusy] = useState('');
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const titleId = `source-${source.key}`;

  const run = async (what: string, act: () => Promise<{ ok: boolean; error?: string }>) => {
    setBusy(what);
    setError(null);
    const r = await act();
    if (!r.ok) setError(r.error ?? 'The request failed.');
    setBusy('');
    return r.ok;
  };

  return (
    <section class="source" aria-labelledby={titleId}>
      <header class="source-head">
        <Badge source={source} />
        <div class="source-id">
          <h2 id={titleId}>{source.name || source.key}</h2>
          <span class="source-meta">
            {github ? 'built in' : 'plugin'} · {source.state}
          </span>
        </div>
        {fleet ? (
          <span class="fleet-tag" role="img" aria-label={`${fleet.name} fleet`}>
            <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />
            {fleet.name}
          </span>
        ) : null}
      </header>
      {source.state === 'error' && source.error ? <p class="note error-text">{source.error}</p> : null}
      {source.warnings.map((w) => (
        <p class="note warning" role="status" key={w}>
          {w}
        </p>
      ))}
      <SettingsForm source={source} version={version} onSaved={() => (bump(source.key), void reload())} />
      <div class="source-actions">
        {!github && others.length > 0 ? (
          <div class="move">
            <label for={`${titleId}-move`}>Move to fleet</label>
            <select id={`${titleId}-move`} value={target} onChange={(e) => setTarget((e.target as HTMLSelectElement).value)}>
              {others.map((f) => (
                <option key={f.key} value={f.key}>
                  {f.name}
                </option>
              ))}
            </select>
            <button type="button" class="btn" disabled={!target || busy !== ''} onClick={() => run('move', async () => {
              const r = await saveSource(source.key, { fleet: target });
              if (r.ok) await reload();
              return r;
            })}>
              Move
            </button>
          </div>
        ) : null}
        <button type="button" class="btn" disabled={busy !== ''} onClick={() => run('restart', () => restart(source.key))}>
          {busy === 'restart' ? 'Restarting…' : 'Restart'}
        </button>
        {confirming ? (
          <div class="confirm" role="group" aria-label={`Remove ${source.name || source.key}`}>
            <span>{github ? 'Remove this GitHub source? This shell can’t add it back yet.' : 'Remove this source?'}</span>
            <button type="button" class="btn danger" disabled={busy !== ''} onClick={async () => (await run('remove', async () => {
              const r = await removeSource(source.key);
              if (r.ok) await reload();
              return r;
            })) || setConfirming(false)}>
              Remove source
            </button>
            <button type="button" class="btn" onClick={() => setConfirming(false)}>
              Cancel
            </button>
          </div>
        ) : (
          <button type="button" class="btn" onClick={() => setConfirming(true)}>
            Remove
          </button>
        )}
      </div>
      {error ? (
        <p class="note error" role="alert">
          {error}
        </p>
      ) : null}
    </section>
  );
}

function AddSource({ fleet, onAdded }: { fleet: Fleet; onAdded: () => Promise<void> }) {
  const [open, setOpen] = useState(false);
  const [plugins, setPlugins] = useState<PluginInfo[] | null>(null);
  const [pick, setPick] = useState('');
  const [error, setError] = useState<string | null>(null);

  const show = async () => {
    setOpen(true);
    setError(null);
    const r = await fetchPlugins();
    if (!r.ok) return setError(r.error);
    // Giving a fleet a GitHub source is not offered here yet.
    const list = r.data.filter((p) => p.valid && p.name !== 'github');
    setPlugins(list);
    setPick(list[0]?.name ?? '');
  };

  if (!open) {
    return (
      <button type="button" class="btn" onClick={show}>
        Add source
      </button>
    );
  }
  return (
    <div class="add box" role="group" aria-label="Add source">
      {plugins === null && !error ? <p class="note">Loading…</p> : null}
      {plugins && plugins.length === 0 ? <p class="note">No plugins are installed.</p> : null}
      {plugins && plugins.length > 0 ? (
        <div class="move">
          <label for="add-plugin">Plugin</label>
          <select id="add-plugin" value={pick} onChange={(e) => setPick((e.target as HTMLSelectElement).value)}>
            {plugins.map((p) => (
              <option key={p.name} value={p.name}>
                {p.name}
              </option>
            ))}
          </select>
          <button type="button" class="btn primary" disabled={!pick} onClick={async () => {
            const r = await addSource(pick, fleet.key);
            if (!r.ok) return setError(r.error);
            setOpen(false);
            await onAdded();
          }}>
            Add to {fleet.name}
          </button>
        </div>
      ) : null}
      {error ? (
        <p class="note error" role="alert">
          {error}
        </p>
      ) : null}
      <button type="button" class="btn" onClick={() => setOpen(false)}>
        Cancel
      </button>
    </div>
  );
}

export function OrderSources({ fleet }: { fleet: Fleet }) {
  const [sources, setSources] = useState<Source[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [versions, setVersions] = useState<Record<string, number>>({});
  const bump = (key: string) => setVersions((v) => ({ ...v, [key]: (v[key] ?? 0) + 1 }));
  const alive = useRef(true);
  useEffect(() => () => void (alive.current = false), []);

  const reload = async () => {
    const r = await fetchSources();
    if (!alive.current) return;
    if (r.ok) {
      setSources(r.data);
      setError(null);
    } else setError(r.error);
  };
  useEffect(() => void reload(), []);

  // A source that is still starting settles on its own; ask again until it has.
  useEffect(() => {
    if (!sources?.some((s) => s.state === 'starting')) return;
    const t = setTimeout(reload, 1000);
    return () => clearTimeout(t);
  }, [sources]);

  // Restart answers at once; the state it moves through is only seen by asking again, until two reads in a row are settled.
  const restart = async (key: string) => {
    const r = await restartSource(key);
    if (!r.ok) return r;
    // A read taken before the runner has stopped still says "ok": count settled reads only once the state has moved, or after a few reads without a move.
    let settled = 0;
    let moved = false;
    for (let i = 0; i < 40 && settled < 2 && alive.current; i++) {
      await wait(500);
      const s = await fetchSources();
      if (!s.ok || !alive.current) continue;
      setSources(s.data);
      const state = s.data.find((x) => x.key === key)?.state;
      moved ||= state !== 'ok';
      settled = state === 'starting' ? 0 : moved || i >= 5 ? settled + 1 : 0;
    }
    bump(key);
    return { ok: true };
  };

  const mine = sources?.filter((s) => s.fleet === fleet.key);
  return (
    <>
      {error ? (
        <p class="note error" role="alert">
          {error}
        </p>
      ) : null}
      {mine?.length === 0 ? <p class="line">{fleet.name} has no order sources.</p> : null}
      {mine?.map((s) => (
        <SourceCard key={s.key} source={s} version={versions[s.key] ?? 0} reload={reload} bump={bump} restart={restart} />
      ))}
      <AddSource fleet={fleet} onAdded={reload} />
    </>
  );
}
