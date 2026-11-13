import { useEffect, useState } from 'preact/hooks';
import { saveFleet, type Fleet } from './api';
import { fleets, loadFleets } from './fleets';
import { errorTarget, fieldMessage } from './fleetSettings';
import { defaultThreshold, LEVELS, limitsBody, normalizeLimits, reconcileLimits, type Ceiling, type Level, type Limits, type Restricted, type Threshold, type Window } from './limitsForm';
import { saveFailure } from './standingOrders';
import { loc, limitsFieldOf } from './router';
import { Switch } from './RoutinesPage';

type Num = { id: string; label: string; hint: string; min: number; max: number; step?: number; get: (l: Limits) => number; set: (l: Limits, v: number) => Limits };

const LEVEL_NAME: Record<Level, string> = { A0: 'A0 Report only', A1: 'A1 Propose', A3: 'A3 Restricted ship', A4: 'A4 Ship', A5: 'A5 Commodore team' };
const WEEKDAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'];
const TIERS: { id: 't1' | 't2' | 't3' | 't4' | 'fleet'; label: string; hint: string }[] = [
  { id: 't1', label: 'You (T1)', hint: 'Your own pinned chat account.' },
  { id: 't2', label: 'Allowed people and trackers (T2)', hint: 'Senders on the allowlist, trackers, and your own email.' },
  { id: 't3', label: 'Strangers (T3)', hint: 'Never above A1.' },
  { id: 't4', label: 'Monitor alerts (T4)', hint: 'An alert can raise its ceiling when it repeats.' },
  { id: 'fleet', label: 'Whoever the sender', hint: 'The most this fleet allows from anyone.' },
];

const NUMS: Num[] = [
  { id: 'rates.launches_per_hour', label: 'Ships an hour', hint: '1 to 1000, scheduled runs included.', min: 1, max: 1000, get: (l) => l.rates.launches_per_hour, set: (l, v) => ({ ...l, rates: { ...l.rates, launches_per_hour: v } }) },
  { id: 'rates.launches_per_sender_per_day', label: 'Ships per sender a day', hint: '1 to 1000.', min: 1, max: 1000, get: (l) => l.rates.launches_per_sender_per_day, set: (l, v) => ({ ...l, rates: { ...l.rates, launches_per_sender_per_day: v } }) },
  { id: 'rates.concurrent', label: 'Ships at once', hint: '1 to 100, started by outside events.', min: 1, max: 100, get: (l) => l.rates.concurrent, set: (l, v) => ({ ...l, rates: { ...l.rates, concurrent: v } }) },
  { id: 'wall_clock_minutes', label: 'Minutes before a ship stops', hint: '1 to 1440.', min: 1, max: 1440, get: (l) => l.wall_clock_minutes, set: (l, v) => ({ ...l, wall_clock_minutes: v }) },
  { id: 'spend.lookout_call_usd', label: 'Lookout call, dollars', hint: 'Whole cents, 0 to 10000.', min: 0, max: 10000, step: 0.01, get: (l) => l.spend.lookout_call_usd, set: (l, v) => ({ ...l, spend: { ...l.spend, lookout_call_usd: v } }) },
  { id: 'spend.daily_worker_usd', label: 'Ships a day, dollars', hint: 'Whole cents, 0 to 10000.', min: 0, max: 10000, step: 0.01, get: (l) => l.spend.daily_worker_usd, set: (l, v) => ({ ...l, spend: { ...l.spend, daily_worker_usd: v } }) },
  { id: 'spend.reviewer_call_usd', label: 'Reviewer call, dollars', hint: 'Whole cents, 0 to 10000.', min: 0, max: 10000, step: 0.01, get: (l) => l.spend.reviewer_call_usd, set: (l, v) => ({ ...l, spend: { ...l.spend, reviewer_call_usd: v } }) },
  { id: 'quota_hold_percent', label: 'Quota hold, percent', hint: 'Launches from outside events are held at this share of the quota. 1 to 100.', min: 1, max: 100, get: (l) => l.quota_hold_percent, set: (l, v) => ({ ...l, quota_hold_percent: v }) },
];

/** A number field keeps what was typed ("12." on the way to "12.5") and tells the form only a number that parses. */
function NumberInput({ id, label, value, min, max, step, disabled, onChange }: { id?: string; label: string; value: number; min: number; max: number; step?: number; disabled: boolean; onChange: (n: number) => void }) {
  const [text, setText] = useState(String(value));
  useEffect(() => {
    if (Number(text) !== value) setText(String(value));
  }, [value]);
  return (
    <input
      id={id}
      type="text"
      inputMode={step ? 'decimal' : 'numeric'}
      aria-label={label}
      title={`${min} to ${max}`}
      value={text}
      disabled={disabled}
      onInput={(e) => {
        const v = (e.target as HTMLInputElement).value;
        setText(v);
        const n = Number(v);
        if (v.trim() !== '' && Number.isFinite(n)) onChange(step ? Math.round(n * 100) / 100 : n);
      }}
    />
  );
}

const target = (e: Event) => e.target as HTMLInputElement & HTMLSelectElement;

function Row({ id, label, hint, sentence, overridden, reset, onReset, error, children }: {
  id: string;
  label: string;
  hint?: string;
  sentence?: string;
  overridden: boolean;
  reset: boolean;
  onReset: (on: boolean) => void;
  error?: string;
  children: preact.ComponentChildren;
}) {
  return (
    <div class="set limit-row" role="group" aria-labelledby={`${id}-label`}>
      <div class="t">
        <span class="label" id={`${id}-label`}>
          {label}
        </span>
        {sentence ? <span>{sentence}</span> : null}
        {hint ? <span class="hint">{hint}</span> : null}
        {overridden && !reset ? <span class="changed">Changed from the preset</span> : null}
        {reset ? <span class="changed">Goes back to the preset on Save</span> : null}
        {error ? (
          <p class="note error" role="alert">
            {error}
          </p>
        ) : null}
      </div>
      <div class="control">
        {children}
        {overridden ? (
          <button type="button" class="btn" aria-label={reset ? `Keep my ${label}` : `Reset ${label} to the preset`} onClick={() => onReset(!reset)}>
            {reset ? 'Keep mine' : 'Reset to preset'}
          </button>
        ) : null}
      </div>
    </div>
  );
}

function LevelSelect({ id, label, value, levels, disabled, onChange }: { id?: string; label: string; value: Level; levels: Level[]; disabled: boolean; onChange: (l: Level) => void }) {
  return (
    <select id={id} aria-label={label} value={value} disabled={disabled} onChange={(e) => onChange(target(e).value as Level)}>
      {levels.map((l) => (
        <option key={l} value={l}>
          {LEVEL_NAME[l]}
        </option>
      ))}
    </select>
  );
}

function RestrictedSelect({ label, value, disabled, onChange }: { label: string; value: Restricted; disabled: boolean; onChange: (r: Restricted) => void }) {
  return (
    <select aria-label={label} value={value} disabled={disabled} onChange={(e) => onChange(target(e).value as Restricted)}>
      <option value="investigate">Investigate</option>
      <option value="draft_fix">Draft fix</option>
    </select>
  );
}

function CeilingControl({ id, tier, label, value, disabled, onChange }: { id: string; tier: string; label: string; value: Ceiling; disabled: boolean; onChange: (c: Ceiling) => void }) {
  const levels = tier === 't3' ? LEVELS.filter((l) => l === 'A0' || l === 'A1') : LEVELS;
  const withLevel = (level: Level): Ceiling => {
    const { restricted: _r, ...rest } = value;
    return level === 'A3' ? { ...rest, level, restricted: value.restricted ?? 'investigate' } : { ...rest, level };
  };
  const th = value.threshold;
  const setTh = (patch: Partial<Threshold>) => th && onChange({ ...value, threshold: { ...th, ...patch } });
  return (
    <>
      <LevelSelect id={id} label={`${label} ceiling`} value={value.level} levels={levels} disabled={disabled} onChange={(l) => onChange(withLevel(l))} />
      {value.level === 'A3' ? <RestrictedSelect label={`${label} restricted ship`} value={value.restricted ?? 'investigate'} disabled={disabled} onChange={(r) => onChange({ ...value, restricted: r })} /> : null}
      {tier === 't1' ? (
        <span class="inline-switch">
          Commodore asks first <Switch label="Commodore asks first" on={!!value.asks_first} busy={disabled} onToggle={() => onChange({ ...value, asks_first: !value.asks_first })} />
        </span>
      ) : null}
      {tier === 't4' ? (
        <div class="threshold" role="group" aria-label="T4 threshold">
          <span class="inline-switch">
            Raise the ceiling when an alert repeats{' '}
            <Switch label="Raise the ceiling when an alert repeats" on={!!th} busy={disabled} onToggle={() => onChange({ ...value, threshold: th ? null : defaultThreshold(value) })} />
          </span>
          {th ? (
            <>
              <label class="field">
                <span>Events</span>
                <NumberInput label="Threshold events" min={1} max={100000} value={th.events} disabled={disabled} onChange={(n) => setTh({ events: n })} />
              </label>
              <label class="field">
                <span>Days</span>
                <NumberInput label="Threshold days" min={1} max={365} value={th.days} disabled={disabled} onChange={(n) => setTh({ days: n })} />
              </label>
              <LevelSelect label="Threshold ceiling" value={th.level} levels={LEVELS} disabled={disabled} onChange={(l) => (l === 'A3' ? setTh({ level: l, restricted: th.restricted ?? 'investigate' }) : onChange({ ...value, threshold: { events: th.events, days: th.days, level: l } }))} />
              {th.level === 'A3' ? <RestrictedSelect label="Threshold restricted ship" value={th.restricted ?? 'investigate'} disabled={disabled} onChange={(r) => setTh({ restricted: r })} /> : null}
            </>
          ) : (
            <span class="hint">No threshold</span>
          )}
        </div>
      ) : null}
    </>
  );
}

function Rows({ id, item, add, values, notes, disabled, onEdit, onRemove, onAdd }: {
  id: string;
  item: string;
  add: string;
  values: string[];
  notes?: (string | undefined)[];
  disabled: boolean;
  onEdit: (i: number, v: string) => void;
  onRemove: (i: number) => void;
  onAdd: () => void;
}) {
  return (
    <div class="rows list-field">
      {values.map((v, i) => (
        <div class="row-field" key={i}>
          <input id={i === 0 ? id : undefined} type="text" aria-label={`${item} ${i + 1}`} value={v} disabled={disabled} onInput={(e) => onEdit(i, target(e).value)} />
          {notes?.[i] ? <span class="hint">{notes[i]}</span> : null}
          <button type="button" class="btn" disabled={disabled} aria-label={`Remove ${item.toLowerCase()} ${i + 1}`} onClick={() => onRemove(i)}>
            Remove
          </button>
        </div>
      ))}
      <button type="button" class="btn" id={values.length === 0 ? id : undefined} disabled={disabled} onClick={onAdd}>
        {add}
      </button>
    </div>
  );
}

function QuietHours({ id, windows, disabled, onChange }: { id: string; windows: Window[]; disabled: boolean; onChange: (w: Window[]) => void }) {
  const edit = (i: number, patch: Partial<Window>) => onChange(windows.map((w, j) => (j === i ? { ...w, ...patch } : w)));
  return (
    <div class="rows list-field">
      {windows.map((w, i) => (
        <div class="row-field window" role="group" aria-label={`Quiet hours window ${i + 1}`} key={i}>
          <input id={i === 0 ? id : undefined} type="time" aria-label={`Window ${i + 1} start`} value={w.start} disabled={disabled} onInput={(e) => edit(i, { start: target(e).value })} />
          <input type="time" aria-label={`Window ${i + 1} end`} value={w.end} disabled={disabled} onInput={(e) => edit(i, { end: target(e).value })} />
          <span class="weekdays">
            {WEEKDAYS.map((d) => (
              <label key={d} class="weekday">
                <input type="checkbox" checked={w.weekdays.includes(d)} disabled={disabled} onChange={() => edit(i, { weekdays: w.weekdays.includes(d) ? w.weekdays.filter((x) => x !== d) : WEEKDAYS.filter((x) => x === d || w.weekdays.includes(x)) })} />
                {d}
              </label>
            ))}
          </span>
          <button type="button" class="btn" disabled={disabled} aria-label={`Remove window ${i + 1}`} onClick={() => onChange(windows.filter((_, j) => j !== i))}>
            Remove
          </button>
        </div>
      ))}
      <button type="button" class="btn" id={windows.length === 0 ? id : undefined} disabled={disabled} onClick={() => onChange([...windows, { start: '22:00', end: '07:00', weekdays: [...WEEKDAYS] }])}>
        Add a window
      </button>
    </div>
  );
}

/** A fleet's preset and Limits: one control per Limit, each carrying its sentence field as its id. */
export function LimitsPage({ fleet }: { fleet: Fleet }) {
  const read = (f: Fleet) => normalizeLimits(f.limits);
  const [shown, setShown] = useState(() => read(fleet));
  const [edited, setEdited] = useState(shown);
  const [resets, setResets] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<'work' | 'home' | null>(null);
  const [error, setError] = useState<{ message: string; status?: number } | null>(null);
  const [stale, setStale] = useState(false);

  // Limits that change under an open form (a Copy into this fleet, another tab) are adopted unless the person has edits.
  useEffect(() => {
    const verdict = reconcileLimits(shown, edited, read(fleet));
    if (verdict === 'adopt' && resets.length === 0) {
      const next = read(fleet);
      setShown(next);
      setEdited(next);
    }
    setStale(verdict === 'stale' || (verdict === 'adopt' && resets.length > 0));
  }, [fleet.limits]);

  const field = limitsFieldOf(loc.value.search);
  useEffect(() => {
    const el = field ? document.getElementById(field) : null;
    el?.scrollIntoView({ block: 'center' });
    el?.focus();
  }, [field]);

  const body = limitsBody(shown, edited, resets);
  const dirty = Object.keys(body).length > 0;
  const errTarget = error ? errorTarget(error.message, fleet.key) : null;
  const topError = error && !pending && (!errTarget || errTarget.field !== 'limits') ? error : null;
  const rowError = (id: string) => (errTarget?.field === 'limits' && errTarget.limit === id && error ? fieldMessage(error.message, fleet.key).replace(/^limits\.[^:]*:\s*/, '') : undefined);
  const sentence = (id: string) => fleet.sentences.find((s) => s.field === id)?.sentence;
  const isOver = (id: string) => fleet.overridden.includes(id);
  const isReset = (id: string) => resets.includes(id);
  const toggleReset = (id: string) => setResets(isReset(id) ? resets.filter((r) => r !== id) : [...resets, id]);
  const change = (next: Limits) => {
    setError(null);
    setEdited(next);
  };
  const row = (id: string, label: string, hint: string | undefined, control: preact.ComponentChildren) => (
    <Row key={id} id={id} label={label} hint={hint} sentence={sentence(id)} overridden={isOver(id)} reset={isReset(id)} onReset={() => toggleReset(id)} error={rowError(id)}>
      {control}
    </Row>
  );

  const adopt = (list: Fleet[]) => {
    fleets.value = list;
    const f = list.find((x) => x.key === fleet.key);
    if (f) {
      const next = read(f);
      setShown(next);
      setEdited(next);
      setResets([]);
    }
  };
  const send = async (patch: Parameters<typeof saveFleet>[1]) => {
    setBusy(true);
    setError(null);
    const r = await saveFleet(fleet.key, patch);
    if (r.ok) adopt(r.data);
    else setError({ message: saveFailure(r), status: r.status });
    setBusy(false);
    return r.ok;
  };
  const reload = async () => {
    await loadFleets();
    adopt(fleets.value);
    setError(null);
    setStale(false);
  };

  const ceiling = (tier: (typeof TIERS)[number]) => {
    const id = `ceilings.${tier.id}`;
    return row(id, tier.label, tier.hint, <CeilingControl id={id} tier={tier.id} label={tier.label} value={edited.ceilings[tier.id]} disabled={busy || isReset(id)} onChange={(c) => change({ ...edited, ceilings: { ...edited.ceilings, [tier.id]: c } })} />);
  };
  const num = (n: Num) =>
    row(n.id, n.label, n.hint, (
      <NumberInput id={n.id} label={n.label} min={n.min} max={n.max} step={n.step} value={n.get(edited)} disabled={busy || isReset(n.id)} onChange={(v) => change(n.set(edited, v))} />
    ));
  const strings = (id: 'sender_allowlist' | 'repo_allowlist' | 'write_deny', label: string, hint: string, item: string, add: string) =>
    row(id, label, hint, (
      <Rows id={id} item={item} add={add} values={edited[id]} disabled={busy || isReset(id)} onEdit={(i, v) => change({ ...edited, [id]: edited[id].map((x, j) => (j === i ? v : x)) })} onRemove={(i) => change({ ...edited, [id]: edited[id].filter((_, j) => j !== i) })} onAdd={() => change({ ...edited, [id]: [...edited[id], ''] })} />
    ));
  const skip = (id: 'labels' | 'senders' | 'dedupe_keys', label: string, hint: string, item: string, add: string) => {
    const list = edited.skip[id];
    const set = (next: typeof list) => change({ ...edited, skip: { ...edited.skip, [id]: next } });
    return row(`skip.${id}`, label, hint, (
      <Rows id={`skip.${id}`} item={item} add={add} values={list.map((s) => s.value)} notes={list.map((s) => (s.routine ? `Only for routine ${s.routine}` : undefined))} disabled={busy || isReset(`skip.${id}`)} onEdit={(i, v) => set(list.map((s, j) => (j === i ? { ...s, value: v } : s)))} onRemove={(i) => set(list.filter((_, j) => j !== i))} onAdd={() => set([...list, { value: '' }])} />
    ));
  };

  const name = (p: string) => (p === 'home' ? 'Home' : 'Work');
  const topAlert = topError ? (
    <p class="note error" role="alert">
      {topError.status === 409 ? 'The config file changed or is invalid, so nothing was saved. ' : ''}
      {topError.message}{' '}
      {topError.status === 409 ? (
        <button type="button" class="btn" onClick={() => void reload()}>
          Reload
        </button>
      ) : null}
    </p>
  ) : null;

  return (
    <>
      <p class="line">Limits are what Admiral enforces on work started by outside events. Each Limit reads as the sentence beside its control.</p>
      {topAlert}
      {stale ? (
        <p class="note warning" role="status">
          This fleet’s Limits changed elsewhere. Reload to see them; your unsaved edits will be dropped.{' '}
          <button type="button" class="btn" onClick={() => void reload()}>
            Reload
          </button>
        </p>
      ) : null}
      <div class="box">
        <div class="set">
          <div class="t">
            <span class="label" id="preset-label">
              Preset
            </span>
            <span>Every Limit starts from the preset. Quiet hours belong to this fleet and stay when the preset changes.</span>
          </div>
          <fieldset class="seg" role="radiogroup" aria-labelledby="preset-label">
            {(['work', 'home'] as const).map((p) => (
              <label key={p} class={fleet.preset === p ? 'seg-opt on' : 'seg-opt'}>
                <input type="radio" name="preset" checked={fleet.preset === p} disabled={busy} onChange={() => p !== fleet.preset && setPending(p)} />
                {name(p)}
              </label>
            ))}
          </fieldset>
        </div>
        {pending ? (
          <div class="set confirm" role="group" aria-label={`Switch to ${name(pending)}`}>
            <p class="note">Switching to {name(pending)} replaces this fleet’s Limits with {name(pending)}’s, keeping quiet hours.{dirty ? ' Your unsaved Limit edits will be dropped.' : ''}</p>
            {error ? (
              <p class="note error" role="alert">
                {error.message}
              </p>
            ) : null}
            <div class="routine-actions">
              <button
                type="button"
                class="btn danger"
                disabled={busy}
                onClick={() =>
                  void send({ preset: pending }).then((ok) => {
                    if (ok) setPending(null);
                  })
                }
              >
                Switch
              </button>
              <button type="button" class="btn" onClick={() => setPending(null)}>
                Cancel
              </button>
            </div>
          </div>
        ) : null}
      </div>

      <h2 class="section-title">What an event can start</h2>
      <div class="box">{TIERS.map(ceiling)}</div>

      <h2 class="section-title">Who and where</h2>
      <div class="box">
        {strings('sender_allowlist', 'Allowed senders', 'One sender per row. They count as T2.', 'Sender', 'Add sender')}
        {strings('repo_allowlist', 'Repos', 'owner/name, globs allowed. Empty means any repo under the search roots. A new entry must be a repo under them.', 'Repo', 'Add repo')}
        {strings('write_deny', 'Paths ships may not write', 'Globs. Empty means anywhere in the repo.', 'Path', 'Add path')}
        {skip('labels', 'Skipped labels', 'An event with one of these labels is skipped.', 'Label', 'Add label')}
        {skip('senders', 'Skipped senders', 'An event from one of these senders is skipped.', 'Skipped sender', 'Add skipped sender')}
        {skip('dedupe_keys', 'Skipped keys', 'An event with one of these keys is skipped.', 'Key', 'Add key')}
      </div>

      <h2 class="section-title">How much and how often</h2>
      <div class="box">{NUMS.map(num)}</div>

      <h2 class="section-title">Quiet hours</h2>
      <div class="box">
        {row('quiet_hours', 'Quiet hours', 'Fleet-local times. A launch inside a window is held as a proposal.', <QuietHours id="quiet_hours" windows={edited.quiet_hours} disabled={busy || isReset('quiet_hours')} onChange={(w) => change({ ...edited, quiet_hours: w })} />)}
      </div>

      <div class="box">
        <div class="set actions">
          <button type="button" class="btn primary" disabled={busy || !dirty} onClick={() => void send({ limits: body })}>
            Save
          </button>
        </div>
      </div>
    </>
  );
}
