import { useEffect, useState } from 'preact/hooks';
import { type Draft, type EntryKey, buildEntry, draftOf } from './autopilotEntry';
import { config } from './config';
import { LogoFlag } from './Logos';
import { Modal } from './Modal';
import { resources } from './resources';
import { api, sourcePath } from './sources';

// The per-panel autopilot form (#181). It reads and writes one panel's entry in config.json through the
// autopilot settings routes; `version` is the entry version those routes use to refuse a stale save.

interface Loaded {
  entry: Record<string, any> | null;
  version: string;
  max_ships: number; // the global cap
  resume_source: boolean;
  args_error: string;
  config_error: string;
}

const lowStakes: [string, string][] = [
  ['advisor', 'advisor (default)'],
  ['never', 'never'],
];
const merges: [string, string][] = [
  ['', 'Off'],
  ['squash', 'squash'],
  ['merge', 'merge'],
  ['rebase', 'rebase'],
];

export function AutopilotSettings({ sourceKey, panelId, panelTitle, onClose }: { sourceKey: string; panelId: string; panelTitle: string; onClose: () => void }) {
  const path = sourcePath(sourceKey, `/panels/${encodeURIComponent(panelId)}/autopilot/settings`);
  const fleets = (config.value?.fleets ?? []).filter((f) => f.docks.some((d) => d.harness === 'claude'));
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [touched, setTouched] = useState<ReadonlySet<EntryKey>>(new Set());
  const [maxText, setMaxText] = useState('');
  const [retroText, setRetroText] = useState('');
  const [refusal, setRefusal] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    api<Loaded>('GET', path).then((r) => {
      if (!live) return;
      if (!r.ok) return setLoadError(r.error);
      const d = draftOf(r.data.entry, fleets[0]?.key ?? '');
      setLoaded(r.data);
      setDraft(d);
      setMaxText(String(d.max_ships));
      setRetroText(String(d.retro_every));
    });
    return () => {
      live = false;
    };
  }, [path]);

  const title = (
    <header class="app-settings-head">
      <h2>
        Autopilot settings
        <small class="ap-panel">{panelTitle}</small>
      </h2>
      <button type="button" class="press app-settings-x" aria-label="Close autopilot settings" onClick={onClose}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" aria-hidden="true">
          <path d="M6 6l12 12M18 6L6 18" />
        </svg>
      </button>
    </header>
  );

  if (!loaded || !draft) {
    return (
      <Modal label="Autopilot settings" variant="app-settings" onClose={onClose}>
        {title}
        <div class="app-settings-body">{loadError ? <div role="alert">{loadError}</div> : <p>Loading…</p>}</div>
      </Modal>
    );
  }

  const stored = loaded.entry;
  const configError = loaded.config_error || config.value?.error?.message || '';
  const ro = !!configError || busy;
  const locked = loaded.args_error !== '';
  const noFleet = fleets.length === 0;
  const maxOk = /^\d+$/.test(maxText) && Number(maxText) >= 1;
  const retroOk = /^\d+$/.test(retroText);
  const entry = buildEntry(stored, draft, touched);
  const line = `sources.${sourceKey}.autopilot.${panelId} = ${JSON.stringify(entry)}`;

  const edit = (key: EntryKey, patch: Partial<Draft>) => {
    setDraft({ ...draft, ...patch });
    setTouched(new Set(touched).add(key));
    setRefusal(null);
  };

  // The fleet and skill pickers: values the stored entry holds that the lists lack stay choosable.
  const fleetKeys = [...new Set([...fleets.map((f) => f.key), draft.fleet].filter(Boolean))];
  const favorites = config.value?.favorite_skills ?? [];
  const isFavorite = draft.skill.kind === 'skill' && favorites.includes(draft.skill.name ?? '');
  const skillValue = draft.skill.kind === 'skill' ? (isFavorite ? `skill:${draft.skill.name}` : 'custom') : draft.skill.kind;
  const pickSkill = (v: string) => {
    if (v === 'commodore' || v === 'none') edit('skill', { skill: { kind: v } });
    else if (v === 'custom') edit('skill', { skill: { kind: 'skill', name: '' } });
    else edit('skill', { skill: { kind: 'skill', name: v.slice('skill:'.length) } });
  };

  const names = [...new Set([...resources.value.map((r) => r.name), ...draft.resources])];
  const refusedArg = refusal?.match(/^--arg (\S+)/)?.[1];

  async function save() {
    setBusy(true);
    const r = await api('PUT', path, { entry, version: loaded!.version });
    setBusy(false);
    if (r.ok) onClose();
    else setRefusal(r.error);
  }

  async function remove() {
    setBusy(true);
    const r = await api('DELETE', `${path}?version=${encodeURIComponent(loaded!.version)}`);
    setBusy(false);
    if (r.ok) onClose();
    else {
      setConfirming(false);
      setRefusal(r.error);
    }
  }

  return (
    <Modal
      label="Autopilot settings"
      variant="app-settings"
      onClose={onClose}
      onCancel={(e) => {
        if (confirming) {
          e.preventDefault(); // Escape closes the question first
          setConfirming(false);
        }
      }}
    >
      {title}
      <div class="app-settings-body">
        {configError && (
          <div role="alert" class="ap-alert">
            <strong>Fix config.json first</strong>
            <span>config.json doesn't load, so Admiral is running on the last good config. Saving is off until the file loads again.</span>
            <code>{configError}</code>
          </div>
        )}
        <div class="app-set-row">
          <span class="app-set-text">
            <strong>Launch this panel's orders</strong>
            <small>{draft.enabled ? 'In list order, while the caps have room. Pause in the panel header stops it for a while.' : 'Off. The settings stay in config.json and nothing launches.'}</small>
          </span>
          <button type="button" role="switch" aria-checked={draft.enabled} aria-label="Autopilot" class={draft.enabled ? 'press ap-switch on' : 'press ap-switch'} disabled={ro} onClick={() => edit('enabled', { enabled: !draft.enabled })}>
            <span aria-hidden="true" />
          </button>
        </div>
        <div class="app-set-field">
          <label for="ap-fleet">Fleet</label>
          <select id="ap-fleet" value={draft.fleet} disabled={ro} onChange={(e) => edit('fleet', { fleet: e.currentTarget.value })}>
            {fleetKeys.map((k) => (
              <option key={k} value={k}>
                {config.value?.fleets.find((f) => f.key === k)?.name ?? k}
              </option>
            ))}
          </select>
          <small>{noFleet ? 'No fleet has a Claude dock, so autopilot has nowhere to run.' : 'Only fleets with a Claude dock.'}</small>
        </div>
        <div class="app-set-field">
          <span class="ap-label">Harness</span>
          <span class="ap-harness">
            <svg width="42" height="30" viewBox="0 0 40 28" aria-hidden="true">
              <LogoFlag harness="claude" kind="pier" />
            </svg>
            <strong>Claude Code</strong>
            <small>Autopilot runs on Claude Code only, for now</small>
          </span>
        </div>
        <div class="app-set-field">
          <label for="ap-skill">Skill</label>
          <select id="ap-skill" value={skillValue} disabled={ro} onChange={(e) => pickSkill(e.currentTarget.value)}>
            <option value="commodore">Commodore</option>
            {favorites.map((f) => (
              <option key={f} value={`skill:${f}`}>
                {f} ★
              </option>
            ))}
            <option value="none">None (just launch)</option>
            <option value="custom">Another skill…</option>
          </select>
          {skillValue === 'custom' && (
            <input
              type="text"
              aria-label="Skill name"
              class="ap-mono"
              autocomplete="off"
              spellcheck={false}
              placeholder="Skill name, such as triage"
              value={draft.skill.name ?? ''}
              disabled={ro}
              onInput={(e) => edit('skill', { skill: { kind: 'skill', name: e.currentTarget.value.trim() } })}
            />
          )}
          <small>Commodore, then your favorite skills. The ship starts on the skill by name.</small>
        </div>
        <div class="app-set-field">
          <span class="ap-label">Extra arguments</span>
          {locked ? (
            <div class="ap-locked">
              <code>{(stored?.args ?? []).join(' ')}</code>
              <small>
                Set by hand, and a save from here would be refused: {loaded.args_error.replace(/^--arg \S+ is refused: /, '')}. Edit them in config.json; a save here keeps them as they are.
              </small>
            </div>
          ) : (
            <>
              {draft.args.map((a, i) => (
                <span class="ap-arg" key={i}>
                  <input
                    type="text"
                    class={refusedArg !== undefined && a === refusedArg ? 'ap-mono refused' : 'ap-mono'}
                    aria-label={`Argument ${i + 1}`}
                    autocomplete="off"
                    spellcheck={false}
                    value={a}
                    disabled={ro}
                    onInput={(e) => edit('args', { args: draft.args.map((x, j) => (j === i ? e.currentTarget.value : x)) })}
                  />
                  <button type="button" class="press ap-icon" aria-label={`Remove argument ${a || i + 1}`} disabled={ro} onClick={() => edit('args', { args: draft.args.filter((_, j) => j !== i) })}>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" aria-hidden="true">
                      <path d="M6 6l12 12M18 6L6 18" />
                    </svg>
                  </button>
                </span>
              ))}
              <button type="button" class="press ap-add" disabled={ro} onClick={() => edit('args', { args: [...draft.args, ''] })}>
                + Add argument
              </button>
              <small>
                One argument per row, after the fleet's own. Allowed: <code>--model</code>, <code>--advisor</code>, <code>--effort</code>, <code>--disallowedTools</code>, and <code>--permission-mode</code> no looser than the dock's.
              </small>
            </>
          )}
        </div>
        <div class="ap-pair">
          <div class="app-set-field">
            <label for="ap-max">Ships at once</label>
            <input
              id="ap-max"
              type="number"
              inputmode="numeric"
              min={1}
              value={maxText}
              disabled={ro}
              onInput={(e) => {
                const t = e.currentTarget.value;
                setMaxText(t);
                if (/^\d+$/.test(t) && Number(t) >= 1) edit('max_ships', { max_ships: Number(t) });
              }}
            />
            <small>This panel's cap. The global cap is {loaded.max_ships}, set in config.json.</small>
          </div>
          <div class="app-set-field">
            <label for="ap-retro">Retro every</label>
            <input
              id="ap-retro"
              type="number"
              inputmode="numeric"
              min={0}
              value={retroText}
              disabled={ro}
              onInput={(e) => {
                const t = e.currentTarget.value;
                setRetroText(t);
                if (/^\d+$/.test(t)) edit('retro_every', { retro_every: Number(t) });
              }}
            />
            <small>{draft.retro_every === 0 ? 'Off: no retro orders.' : 'Runs between retro orders. 0 turns them off.'}</small>
          </div>
        </div>
        {!loaded.resume_source && (
          <div role="note" class="ap-note">
            <span>
              <b>autopilot.resume_source</b> isn't set in config.json, so this panel queues no resume or retro orders. Set it by hand to turn them on.
            </span>
          </div>
        )}
        <div role="radiogroup" aria-labelledby="ap-low" class="app-set-field">
          <span id="ap-low" class="ap-label">
            Low-stakes calls
          </span>
          <span class="ap-pills">
            {lowStakes.map(([v, label]) => (
              <button key={v} type="button" role="radio" aria-checked={draft.low_stakes === v} class="press ap-pill" disabled={ro} onClick={() => edit('low_stakes', { low_stakes: v })}>
                {label}
              </button>
            ))}
          </span>
          <small>Passed to the ship as ADMIRAL_LOW_STAKES.</small>
        </div>
        <div role="radiogroup" aria-labelledby="ap-merge" class="app-set-field">
          <span id="ap-merge" class="ap-label">
            Auto-merge
          </span>
          <span class="ap-pills">
            {merges.map(([v, label]) => (
              <button key={v} type="button" role="radio" aria-checked={draft.auto_merge === v} class="press ap-pill" disabled={ro} onClick={() => edit('auto_merge', { auto_merge: v })}>
                {label}
              </button>
            ))}
          </span>
        </div>
        <div class="app-set-field">
          <span id="ap-res" class="ap-label">
            Resources its orders need
          </span>
          <span role="group" aria-labelledby="ap-res" class="ap-pills">
            {names.map((n) => {
              const on = draft.resources.includes(n);
              return (
                <button
                  key={n}
                  type="button"
                  role="checkbox"
                  aria-checked={on}
                  class="press ap-pill"
                  disabled={ro}
                  onClick={() => edit('resources', { resources: on ? draft.resources.filter((x) => x !== n) : [...draft.resources, n] })}
                >
                  {on ? '✓ ' : ''}
                  {n}
                </button>
              );
            })}
          </span>
          <small>From the resources in config.json. An order waits until each has a free unit.</small>
        </div>
        <div class="app-set-field">
          <span class="app-set-caption">Writes to config.json</span>
          <code class="app-set-line">{line}</code>
        </div>
      </div>
      {refusal && (
        <div role="alert" class="ap-strip refusal">
          <b>Not saved.</b> <span>{refusal}</span>
        </div>
      )}
      {confirming && (
        <div role="alert" class="ap-strip confirm">
          <span>
            Remove autopilot from <b>{panelTitle}</b>? Its entry leaves config.json and the pause clears. Ships already out keep sailing.
          </span>
          <button type="button" class="press app-set-cancel" onClick={() => setConfirming(false)}>
            Keep it
          </button>
          <button type="button" class="press app-set-cancel ap-danger" disabled={ro} onClick={() => void remove()}>
            Remove
          </button>
        </div>
      )}
      <footer class="app-settings-foot ap-foot">
        {(stored || configError) && ( // an unreadable file answers no entry, so Remove stays, disabled
          <button type="button" class="press app-set-cancel ap-remove" disabled={ro} onClick={() => setConfirming(true)}>
            Remove autopilot
          </button>
        )}
        <span class="ap-spacer" />
        <button type="button" class="press app-set-cancel" onClick={onClose}>
          Cancel
        </button>
        <button type="button" class="press app-set-save" disabled={ro || noFleet || !maxOk || !retroOk} onClick={() => void save()}>
          Save
        </button>
      </footer>
    </Modal>
  );
}
