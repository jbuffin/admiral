import { useEffect, useRef, useState } from 'preact/hooks';
import { call, type DirsAnswer, type Fleet, type FleetCapabilities } from './api';
import { capabilities, capabilitiesFailed, capabilitiesLoaded, loadCapabilities } from './capabilities';
import { cronSentence } from './cron';
import { DescribeRoutine } from './FilingForms';
import { fleetSearch } from './fleets';
import { holdFire, setHoldFire } from './holdFireLink';
import { JudgeLine } from './HarnessCapabilities';
import { MissingCapability } from './MissingCapability';
import { CheckoutSelect, DockPicker, ModelEffortPair, ResourcesPicker, SkillSelect, skillOfValue, skillValue } from './LaunchPickers';
import type { SkillsAnswer } from './launchForm';
import { RoutineTabs } from './RoutineTabs';
import { automateOf, go, loc, routineNewPath, routinePath, shipPath } from './router';
import {
  autopilotDocks,
  automateDraft,
  automateTarget,
  blankForm,
  breakerTripped,
  errorField,
  errorText,
  formOfRoutine,
  holdFireNote,
  lastRunText,
  launchOnDock,
  routineBody,
  runLine,
  runTime,
  type Before,
  type Routine,
  type RoutineForm,
  type RoutineRun,
} from './routines';
import { ships, shipsLoaded, sources } from './shipsLink';

const api = (fleet: string, rest = '') => `/fleets/${encodeURIComponent(fleet)}/routines${rest}`;
const BEFORE: { value: Before; label: string }[] = [
  { value: 'report_only', label: 'Report only' },
  { value: 'ask_first', label: 'Ask first' },
  { value: 'act', label: 'Act' },
];
const RUNS_EVERY = 10_000;

const target = (e: Event) => e.target as HTMLInputElement & HTMLTextAreaElement & HTMLSelectElement;

export function Switch({ label, on, busy, onToggle, hidden }: { label: string; on: boolean; busy?: boolean; onToggle: () => void; hidden?: boolean }) {
  return (
    <button type="button" class="switch" role="switch" aria-checked={on} aria-label={label} disabled={busy} onClick={onToggle}>
      <span class="track" aria-hidden={hidden ?? true} />
    </button>
  );
}

function HoldFireToggles({ fleet }: { fleet: Fleet }) {
  const state = holdFire.value?.fleet === fleet.key ? holdFire.value.state : null;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const flip = async (scope: 'fleet' | 'all') => {
    if (!state) return;
    setBusy(true);
    setError(await setHoldFire(fleet.key, scope, !(scope === 'all' ? state.global.on : state.fleet.on)));
    setBusy(false);
  };
  return (
    <div class="holdfire-toggles">
      <span class="holdfire-toggle">
        Hold fire <Switch label="Hold fire" on={!!state?.fleet.on} busy={busy || !state} onToggle={() => void flip('fleet')} />
      </span>
      <span class="holdfire-toggle">
        All fleets <Switch label="Hold fire, all fleets" on={!!state?.global.on} busy={busy || !state} onToggle={() => void flip('all')} />
      </span>
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}

// The run records the session slug; the ship's name is what the engineer knows it by, and a ship gone from the list keeps its slug's words.
const slugName = (slug: string) => slug.split('-').map((w) => w.charAt(0).toUpperCase() + w.slice(1)).join(' ');
const shipTitle = (session: string, text: string) => text.replace(session, ships.value.find((s) => s.session === session)?.name ?? slugName(session));

function RecentRuns({ runs }: { runs: RoutineRun[] | null }) {
  return (
    <section class="routine-runs" aria-labelledby="runs-head">
      <h2 id="runs-head" class="crew-add-head">
        Recent runs
      </h2>
      {runs === null ? <p class="line">Loading…</p> : null}
      {runs?.length === 0 ? <p class="line">It has not run yet.</p> : null}
      <ul class="run-list">
        {(runs ?? []).map((r) => {
          const l = runLine(r);
          return (
            <li key={r.id} class="run-row">
              <span class={`run-dot ${l.dot}`} aria-hidden="true" />
              <time class="run-time" dateTime={r.at}>
                {runTime(r.at, new Date())}
              </time>
              {l.ship ? <a href={shipPath(l.ship) + fleetSearch.value}>{shipTitle(l.ship, l.text)}</a> : <span>{l.text}</span>}
              {r.via === 'proposal' && !l.text.includes('proposal') ? <span class="muted"> via proposal</span> : null}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function RoutineEditor({ fleet, routine, initial, caps, problem: drafted, onSaved, onDeleted, onToggled }: { fleet: Fleet; routine: Routine | null; initial: RoutineForm; caps: FleetCapabilities | undefined; problem?: { field: 'name' | 'when' | 'before_acting' | 'do'; message: string }; onSaved: (r: Routine) => void; onDeleted: () => void; onToggled: (r: Routine) => void }) {
  const [form, setForm] = useState(initial);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [dirs, setDirs] = useState<DirsAnswer | null>(null);
  const [skills, setSkills] = useState<SkillsAnswer | null>(null);
  const [custom, setCustom] = useState(initial.launch.skill.kind === 'skill' ? initial.launch.skill.name : '');
  const hold = holdFire.value?.fleet === fleet.key ? holdFire.value.state.on : false;
  const docks = autopilotDocks(caps, fleet.docks);
  const l = form.launch;
  const [problem, setProblem] = useState(drafted);
  // A drafted problem goes with the first edit of the field it names.
  const fixes = (field: 'name' | 'when' | 'before_acting' | 'do') => setProblem((p) => (p?.field === field ? undefined : p));
  const set = (patch: Partial<RoutineForm>) => {
    for (const [k, f] of [['name', 'name'], ['when', 'when'], ['before', 'before_acting'], ['mode', 'do'], ['prompt', 'do']] as const) if (k in patch) fixes(f);
    setForm((f) => ({ ...f, ...patch }));
  };
  const setLaunch = (patch: Partial<RoutineForm['launch']>) => {
    fixes('do');
    setForm((f) => ({ ...f, launch: { ...f.launch, ...patch } }));
  };
  const offered = docks.harnesses.includes(l.harness) || !l.harness ? docks.harnesses : [...docks.harnesses, l.harness];

  // The first qualifying dock is the default for a new launch, so Save needs no dock pick.
  useEffect(() => {
    if (form.mode === 'launch' && !l.harness && docks.harnesses[0]) setLaunch({ harness: docks.harnesses[0] });
  }, [docks.harnesses.join(',')]);

  useEffect(() => {
    if (form.mode !== 'launch' || !l.harness) return;
    let live = true;
    void call<DirsAnswer>('POST', '/launch-orders/dirs', { fleet: fleet.key, harness: l.harness, text: '' }).then((r) => live && r.ok && setDirs(r.data));
    return () => {
      live = false;
    };
  }, [fleet.key, l.harness, form.mode]);

  useEffect(() => {
    if (form.mode !== 'launch' || !l.harness || !l.dir) return;
    let live = true;
    const q = new URLSearchParams({ fleet: fleet.key, harness: l.harness, dir: l.dir });
    void call<SkillsAnswer>('GET', `/launch-orders/skills?${q}`).then((r) => {
      if (!live || !r.ok) return;
      setSkills(r.data);
      // A new routine that asked for Commodore on a dock without it falls back to just launching.
      if (!routine) setForm((f) => (f.launch.skill.kind === 'commodore' && !r.data.commodore.found ? { ...f, launch: { ...f.launch, skill: { kind: 'none' } } } : f));
    });
    return () => {
      live = false;
    };
  }, [fleet.key, l.harness, l.dir, form.mode]);

  const dirsShown: DirsAnswer | null = dirs && l.dir && !dirs.entries.some((d) => d.path === l.dir) ? { ...dirs, entries: [...dirs.entries, { path: l.dir, name: l.dir, kind: 'checkout', ships_here: 0 }] } : dirs;
  const favorites = new Set((skills?.skills ?? []).filter((s) => s.favorite).map((s) => s.name));
  const skillSel = l.skill.kind === 'skill' && !favorites.has(l.skill.name) ? 'custom' : skillValue(l.skill);

  const err = error ? { field: errorField(error), text: errorText(error) } : problem ? { field: problem.field, text: problem.message } : null;
  const save = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setError('');
    const r = routine ? await call<Routine>('PUT', api(fleet.key, `/${encodeURIComponent(routine.id)}`), routineBody({ ...form, enabled: routine.enabled })) : await call<Routine>('POST', api(fleet.key), routineBody(form));
    setBusy(false);
    if (!r.ok) return setError(r.error);
    onSaved(r.data);
  };
  const remove = async () => {
    if (!routine) return;
    setConfirming(false);
    setBusy(true);
    const r = await call('DELETE', api(fleet.key, `/${encodeURIComponent(routine.id)}`));
    setBusy(false);
    if (!r.ok) return setError(r.error);
    onDeleted();
  };
  const toggle = async () => {
    if (!routine) return set({ enabled: !form.enabled });
    const r = await call<Routine>('POST', api(fleet.key, `/${encodeURIComponent(routine.id)}/${routine.enabled ? 'off' : 'on'}`));
    if (!r.ok) return setError(r.error);
    onToggled(r.data);
  };
  const enabled = routine ? routine.enabled : form.enabled;
  const note = holdFireNote(form.before, hold);

  return (
    <form class="routine-form" aria-label="Routine" onSubmit={save}>
      {routine?.off_by ? <p class="note">{`Turned off by ${routine.off_by === 'admiralbot' ? 'AdmiralBot' : routine.off_by}.`}</p> : null}
      <label class="field">
        <span>Name</span>
        <input type="text" value={form.name} autocomplete="off" onInput={(e) => set({ name: target(e).value })} />
      </label>
      {err?.field === 'name' ? <p class="note error" role="alert">{err.text}</p> : null}

      <label class="field">
        <span>When</span>
        <input type="text" value={form.when} autocomplete="off" autocapitalize="off" spellcheck={false} placeholder="30 8 * * 1-5" onInput={(e) => set({ when: target(e).value })} />
      </label>
      {form.when.trim() ? <p class="note">{cronSentence(form.when)}</p> : <p class="note">Five fields: minute hour day month weekday, in this machine's time zone.</p>}
      {err?.field === 'when' ? <p class="note error" role="alert">{err.text}</p> : null}

      <fieldset class="seg" role="radiogroup">
        <legend>Do</legend>
        {(['launch', 'prompt'] as const).map((m) => (
          <label key={m} class={form.mode === m ? 'seg-opt on' : 'seg-opt'}>
            <input type="radio" name="do-mode" checked={form.mode === m} onChange={() => set({ mode: m })} />
            {m === 'launch' ? 'Launch a ship' : 'AdmiralBot prompt'}
          </label>
        ))}
      </fieldset>

      {form.mode === 'prompt' ? (
        <label class="field">
          <span>Prompt</span>
          <textarea rows={4} value={form.prompt} onInput={(e) => set({ prompt: target(e).value })} />
        </label>
      ) : (
        <div class="routine-launch">
          {offered.length === 0 ? <p class="note warn">{docks.why || 'Loading docks…'}</p> : <DockPicker docks={offered.map((harness) => ({ harness }))} harness={l.harness} onChange={(harness) => setForm((f) => ({ ...f, launch: launchOnDock(f.launch, harness) }))} />}
          <CheckoutSelect dir={l.dir} dirs={dirsShown} allowClone={false} onChange={(dir) => setLaunch({ dir })} />
          <label class="field">
            <span>Text</span>
            <textarea rows={4} value={l.text} onInput={(e) => setLaunch({ text: target(e).value })} />
          </label>
          <SkillSelect
            value={skillSel}
            skills={skills}
            onChange={(v) => setLaunch({ skill: v === 'custom' ? { kind: 'skill', name: custom } : (skillOfValue(v) ?? { kind: 'none' }) })}
          />
          {skillSel === 'custom' ? (
            <label class="field">
              <span>Skill name</span>
              <input
                type="text"
                autocomplete="off"
                autocapitalize="off"
                spellcheck={false}
                value={custom}
                onInput={(e) => {
                  setCustom(target(e).value);
                  setLaunch({ skill: { kind: 'skill', name: target(e).value } });
                }}
              />
            </label>
          ) : null}
          <ModelEffortPair harness={l.harness} model={l.model} effort={l.effort} onModel={(model) => setLaunch({ model })} onEffort={(effort) => setLaunch({ effort })} />
          <label class="field">
            <span>Args</span>
            <input type="text" autocomplete="off" autocapitalize="off" spellcheck={false} value={l.args} placeholder="Extra harness arguments, separated by spaces" onInput={(e) => setLaunch({ args: target(e).value })} />
          </label>
          <ResourcesPicker held={l.resources} onChange={(resources) => setLaunch({ resources })} />
        </div>
      )}
      {err?.field === 'do' ? <p class="note error" role="alert">{err.text}</p> : null}

      <fieldset class="seg" role="radiogroup">
        <legend>Before acting</legend>
        {BEFORE.map((b) => (
          <label key={b.value} class={form.before === b.value ? 'seg-opt on' : 'seg-opt'}>
            <input type="radio" name="before" checked={form.before === b.value} onChange={() => set({ before: b.value })} />
            {b.label}
          </label>
        ))}
      </fieldset>
      {note ? <p class="note">{note}</p> : null}
      {err?.field === 'before_acting' ? <p class="note error" role="alert">{err.text}</p> : null}

      <div class="routine-switches">
        <span class="routine-switch">
          Notify me <Switch label="Notify me" on={form.notify} onToggle={() => set({ notify: !form.notify })} />
        </span>
        <span class="routine-switch">
          On <Switch label="On" on={enabled} onToggle={() => void toggle()} />
        </span>
      </div>

      {err?.field === 'form' ? <p class="alert" role="alert">{err.text}</p> : null}
      <div class="routine-actions">
        <button type="submit" class="btn primary" disabled={busy}>
          Save
        </button>
        {routine ? (
          <button
            type="button"
            class="btn danger"
            disabled={busy}
            onClick={() => {
              if (confirming) void remove();
              else setConfirming(true);
            }}
            onBlur={() => setConfirming(false)}
          >
            {confirming ? 'Delete routine?' : 'Delete'}
          </button>
        ) : null}
      </div>
    </form>
  );
}

export function RoutinesPage({ fleet, id, creating, title, phone }: { fleet: Fleet; id: string | null; creating: boolean; title: boolean; phone: boolean }) {
  const [list, setList] = useState<Routine[] | null>(null);
  const [failed, setFailed] = useState('');
  const [drafted, setDrafted] = useState<{ form: RoutineForm; problem?: { field: 'name' | 'when' | 'before_acting' | 'do'; message: string }; n: number } | null>(null);
  const caps: FleetCapabilities | undefined = capabilities.value.find((c) => c.key === fleet.key);
  const capsError = capabilitiesFailed.value && !caps ? "Could not load the fleet's capabilities." : '';
  const [runs, setRuns] = useState<RoutineRun[] | null>(null);
  const [version, setVersion] = useState(0);
  const [tripped, setTripped] = useState(false);
  const lastSeen = useRef<string | null>(null);
  const search = fleetSearch.value;

  const load = async () => {
    const r = await call<{ routines: Routine[] | null }>('GET', api(fleet.key));
    if (r.ok) {
      setList(r.data.routines ?? []);
      setFailed('');
    } else setFailed(r.error);
  };
  // Both updates land in one render, so a remounted editor starts from the new list.
  const reopen = () => setVersion((v) => v + 1);
  // A saved or toggled routine comes back whole; its row keeps the last run the list carried.
  const stored = (r: Routine) => {
    setList((l) => (l ?? []).map((x) => (x.id === r.id ? { ...r, last_run: x.last_run } : x)));
    reopen();
  };
  const flip = async (r: Routine) => {
    const res = await call<Routine>('POST', api(fleet.key, `/${encodeURIComponent(r.id)}/${r.enabled ? 'off' : 'on'}`));
    if (res.ok) stored(res.data);
    else setFailed(res.error);
  };
  const loadCaps = () => void loadCapabilities();
  useEffect(() => {
    void load();
    loadCaps();
  }, [fleet.key]);
  useEffect(() => {
    if (!creating) setDrafted(null);
  }, [creating]);

  // Recent runs follow the open routine; a breaker record reloads it so an open form can't undo the trip.
  useEffect(() => {
    setRuns(null);
    setTripped(false);
    lastSeen.current = null;
    if (!id) return;
    let live = true;
    const poll = async () => {
      const r = await call<{ runs: RoutineRun[] | null }>('GET', api(fleet.key, `/${encodeURIComponent(id)}/runs`));
      if (!live || !r.ok) return;
      const got = r.data.runs ?? [];
      setRuns(got);
      if (breakerTripped(got, lastSeen.current)) {
        setTripped(true);
        await load();
        if (live) reopen();
      } else if (lastSeen.current !== null && got[0] && got[0].id !== lastSeen.current) void load();
      lastSeen.current = got[0]?.id ?? lastSeen.current ?? '';
    };
    void poll();
    const t = setInterval(poll, RUNS_EVERY);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [fleet.key, id]);

  const toList = (replace = false) => go('/app/routines', search, replace);
  const heading = title ? <h1>Routines</h1> : null;
  const header = (
    <>
      <div class="routines-head">
        {heading}
        <HoldFireToggles fleet={fleet} />
      </div>
      <RoutineTabs current="routines" />
    </>
  );
  if (!list) {
    return (
      <main class="panel crew routines">
        {header}
        {failed ? (
          <>
            <p class="alert" role="alert">
              {failed}
            </p>
            <button type="button" class="btn" onClick={() => void load()}>
              Try again
            </button>
          </>
        ) : (
          <p class="line">Loading…</p>
        )}
      </main>
    );
  }

  const selected = id ? (list.find((r) => r.id === id) ?? null) : null;
  const showDetail = id !== null || creating;
  const showList = !phone || !showDetail;
  const automate = creating ? automateOf(loc.value.search) : null;
  // Only a source and view that exist in this fleet draft a routine; any other query opens blank, so a crafted link pre-fills nothing.
  const found = automate ? automateTarget(sources.value, fleet.key, automate) : null;
  const gone = !!automate && shipsLoaded.value && !found;
  const judgeOff = !!caps?.judge && !caps.judge.harness;
  const draft = (() => {
    if (drafted) return drafted.form;
    if (!found) return blankForm();
    const dock = autopilotDocks(caps, fleet.docks).harnesses[0] ?? null;
    return automateDraft({ sourceKey: found.source.key, sourceName: found.source.name, viewTitle: found.view.title }, { harness: dock, dir: '', commodore: true });
  })();
  // A draft waits for the sources and the capabilities, so its dock is known when the form opens.
  const ready = !creating || !automate || (shipsLoaded.value && (caps !== undefined || capabilitiesLoaded.value));

  return (
    <main class={`panel crew routines${showList && showDetail ? ' split' : ''}`}>
      {header}
      {failed ? (
        <p class="alert" role="alert">
          {failed}
        </p>
      ) : null}
      <div class={`crew-grid${showList && showDetail ? ' split' : ''}`}>
        {showList ? (
          <div class="crew-list">
            {judgeOff ? (
              <>
                {caps?.judge ? <JudgeLine judge={caps.judge} /> : null}
                {fleet.docks.map((d) => (
                  <MissingCapability key={d.harness} fleet={fleet.key} harness={d.harness} reason="Describing a routine needs a harness proven safe for judge calls." />
                ))}
              </>
            ) : (
              <DescribeRoutine
                key={fleet.key}
                fleet={fleet}
                onDrafted={(form, problem) => {
                  setDrafted({ form, problem, n: (drafted?.n ?? 0) + 1 });
                  go(routineNewPath, search);
                }}
              />
            )}
            <div class="crew-toolbar">
              <a
                class="btn"
                href={routineNewPath + search}
                onClick={(e) => {
                  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                  e.preventDefault();
                  setDrafted(null);
                  go(routineNewPath, search);
                }}
              >
                New routine
              </a>
            </div>
            {list.length === 0 ? <p class="line">No routines yet. A routine launches a ship or sends AdmiralBot a prompt on a schedule.</p> : null}
            <ul class="routine-list">
              {list.map((r) => (
                <li key={r.id} class={`crew-item routine-item${r.enabled ? '' : ' off'}`}>
                  <a
                    class="crew-row"
                    href={routinePath(r.id) + search}
                    aria-current={r.id === id ? 'page' : undefined}
                    onClick={(e) => {
                      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                      e.preventDefault();
                      go(routinePath(r.id), search);
                    }}
                  >
                    <span class="crew-main">
                      <span class="crew-name">{r.name}</span>
                      <span class="crew-sub">{cronSentence(r.when)}</span>
                    </span>
                    <span class="crew-side">
                      <span class="crew-count">{lastRunText(r.last_run, new Date())}</span>
                    </span>
                  </a>
                  <Switch label={`${r.name} on`} on={r.enabled} onToggle={() => void flip(r)} />
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        {showDetail ? (
          <div class="crew-pane">
            {phone ? (
              <a
                class="back-link"
                href={'/app/routines' + search}
                onClick={(e) => {
                  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                  e.preventDefault();
                  toList();
                }}
              >
                Back to routines
              </a>
            ) : null}
            {tripped ? (
              <p class="note warning" role="status">
                The breaker moved this routine to Ask first after repeated failures. The form shows what is saved now.
              </p>
            ) : null}
            {capsError ? (
              <p class="alert" role="alert">
                {capsError}{' '}
                <button type="button" class="btn" onClick={loadCaps}>
                  Try again
                </button>
              </p>
            ) : null}
            {gone ? <p class="note">That view is no longer in this fleet, so this is a blank routine.</p> : null}
            {creating ? (
              ready ? (
                <RoutineEditor
                  key={`new:${loc.value.search}:${drafted ? `described:${drafted.n}` : found ? 'draft' : 'blank'}`}
                  fleet={fleet}
                  routine={null}
                  initial={draft}
                  caps={caps}
                  problem={drafted?.problem}
                  onSaved={(r) => {
                    void load();
                    go(routinePath(r.id), search);
                  }}
                  onDeleted={() => toList()}
                  onToggled={() => {}}
                />
              ) : (
                <p class="line">Loading…</p>
              )
            ) : selected ? (
              <>
                <RoutineEditor
                  key={`${selected.id}:${version}`}
                  fleet={fleet}
                  routine={selected}
                  initial={formOfRoutine(selected)}
                  caps={caps}
                  onSaved={stored}
                  onDeleted={() => {
                    void load();
                    toList();
                  }}
                  onToggled={stored}
                />
                <RecentRuns runs={runs} />
              </>
            ) : (
              <p class="line">Routine not found.</p>
            )}
          </div>
        ) : null}
      </div>
    </main>
  );
}
