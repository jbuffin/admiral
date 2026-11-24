import { useEffect, useRef, useState } from 'preact/hooks';
import { call, type DirsAnswer, type Fleet, type FleetCapabilities } from './api';
import { Actions, Button } from './Button';
import { capabilities, capabilitiesFailed, loadCapabilities } from './capabilities';
import { describeCron } from './cron';
import { DescribeRoutine, openedDraft, routineDraftPath } from './FilingForms';
import { fleetSearch } from './fleets';
import { holdFire, setHoldFire, stopResult, stopWorkerShips } from './holdFireLink';
import { JudgeLine } from './HarnessCapabilities';
import { MissingCapability } from './MissingCapability';
import { CheckoutSelect, DockPicker, ModelEffortPair, ResourcesPicker, SkillSelect, skillOfValue, skillValue } from './LaunchPickers';
import type { SkillsAnswer } from './launchForm';
import { normalizeLimits } from './limitsForm';
import { RoutineTabs } from './RoutineTabs';
import { go, loc, peekPath, routineNewPath, routinePath, shipPath } from './router';
import {
  autopilotDocks,
  blankForm,
  breakerTripped,
  eventChoices,
  eventHref,
  eventValue,
  eventWords,
  errorField,
  errorText,
  formOfRoutine,
  holdFireNote,
  checkoutScopeLine,
  lastRunText,
  noEventsNote,
  triggerRemoved,
  launchOnDock,
  routineBody,
  runCost,
  runLine,
  spendNotice,
  proposeOnlyNote,
  stopAllConfirm,
  stopAllLabel,
  runTime,
  type Before,
  type Routine,
  type RoutineField,
  type RoutineForm,
  type RoutineRun,
  type RoutineSpend,
} from './routines';
import { inboxItems, ships, sources } from './shipsLink';

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
  const [confirming, setConfirming] = useState<'' | 'fleet' | 'all'>('');
  const stop = async (scope: 'fleet' | 'all') => {
    setBusy(true);
    stopResult.value = '';
    setError(await stopWorkerShips(fleet.key, scope));
    setBusy(false);
    setConfirming('');
  };
  const flip = async (scope: 'fleet' | 'all') => {
    if (!state) return;
    setBusy(true);
    setError(await setHoldFire(fleet.key, scope, !(scope === 'all' ? state.global.on : state.fleet.on)));
    setBusy(false);
  };
  return (
    <div class="holdfire-toggles">
      {(['fleet', 'all'] as const).map((scope) => {
        const { label, disabled } = stopAllLabel(scope === 'all' ? state?.all_worker_ships : state?.worker_ships, scope);
        return (
          <span class="holdfire-toggle" key={scope}>
            {scope === 'all' ? 'All fleets' : 'Hold fire'}{' '}
            <Switch
              label={scope === 'all' ? 'Hold fire, all fleets' : 'Hold fire'}
              on={!!(scope === 'all' ? state?.global.on : state?.fleet.on)}
              busy={busy || !state}
              onToggle={() => void flip(scope)}
            />
            <Button label={label} disabled={busy || disabled} onClick={() => setConfirming(scope)} />
          </span>
        );
      })}
      {confirming ? (
        <div class="confirm" role="group" aria-label="Stop all worker-launched">
          <p class="line">{stopAllConfirm(confirming)}</p>
          <Actions class="confirm-actions">
            <Button label="Confirm stop" variant="danger" pending={['fleet', fleet.key, 'stop-workers', confirming]} onClick={() => stop(confirming)} />
            <Button label="Cancel" onClick={() => setConfirming('')} />
          </Actions>
        </div>
      ) : null}
      {stopResult.value ? <p class="line" role="status">{stopResult.value}</p> : null}
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

// The proposal's Inbox item is named after the record; it links only while the Inbox still holds it.
function ProposalLink({ id }: { id: string }) {
  const item = `event-${id}`;
  if (!inboxItems.value.some((i) => i.id === item)) return null;
  return <a href={peekPath({ session: '_admiral', item }) + fleetSearch.value}>Open the proposal</a>;
}

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
              {r.tier ? <span class="run-tier">{r.tier}</span> : null}
              {r.sender ? <span class="run-sender">{r.sender}</span> : null}
              {l.ship ? <a href={shipPath(l.ship) + fleetSearch.value}>{shipTitle(l.ship, l.text)}</a> : <span>{l.text}</span>}
              {r.outcome === 'proposed' && r.tier ? <ProposalLink id={r.id} /> : null}
              {r.via === 'proposal' && !l.text.includes('proposal') ? <span class="muted"> via proposal</span> : null}
              {eventHref(r.event) ? (
                <a href={eventHref(r.event)!} target="_blank" rel="noopener noreferrer">
                  Event
                </a>
              ) : null}
              {runCost(r) ? <span class="muted">{runCost(r)}</span> : null}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

/** What a routine that still names the removed view trigger says; the server refuses a save that keeps it. */
const REMOVED_NOTE = 'The "New in <view>" trigger was removed, so this routine never runs.';

function RoutineEditor({ fleet, zone, routine, initial, caps, problem: drafted, onSaved, onDeleted, onToggled }: { fleet: Fleet; zone?: string; routine: Routine | null; initial: RoutineForm; caps: FleetCapabilities | undefined; problem?: { field: RoutineField; message: string }; onSaved: (r: Routine) => void; onDeleted: () => void; onToggled: (r: Routine) => void }) {
  const [form, setForm] = useState(initial);
  // the event chosen before a switch to a schedule, which a switch back restores
  const lastEvent = useRef(initial.on);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [saving, setSaving] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [dirs, setDirs] = useState<DirsAnswer | null>(null);
  const [skills, setSkills] = useState<SkillsAnswer | null>(null);
  const [custom, setCustom] = useState(initial.launch.skill.kind === 'skill' ? initial.launch.skill.name : '');
  const hold = holdFire.value?.fleet === fleet.key ? holdFire.value.state.on : false;
  const docks = autopilotDocks(caps, fleet.docks);
  const l = form.launch;
  const choices = eventChoices(sources.value, fleet.key);
  const groups = [...new Set(choices.map((c) => c.group))].map((g) => [g, choices.filter((c) => c.group === g)] as const);
  const [problem, setProblem] = useState(drafted);
  // A drafted problem goes with the first edit of the field it names.
  const fixes = (field: RoutineField) => {
    setProblem((p) => (p?.field === field ? undefined : p));
    setError((e) => (e && errorField(e) === field ? '' : e));
  };
  const set = (patch: Partial<RoutineForm>) => {
    for (const [k, f] of [['name', 'name'], ['when', 'when'], ['before', 'before_acting'], ['mode', 'do'], ['prompt', 'do'], ['extra', 'do']] as const) if (k in patch) fixes(f);
    setForm((f) => ({ ...f, ...patch }));
  };
  const setLaunch = (patch: Partial<RoutineForm['launch']>) => {
    fixes('do');
    if ('dir' in patch) fixes('checkout');
    if ('text' in patch) fixes('text');
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

  const known = dirs?.entries.find((d) => d.path === l.dir);
  const scopeLine = l.dir && form.on ? checkoutScopeLine({ event: true, plugin: sources.value.find((s) => s.key === form.on!.source && s.fleet === fleet.key)?.plugin, remotes: known?.remotes }) : null;
  const dirsShown: DirsAnswer | null = dirs && l.dir && !dirs.entries.some((d) => d.path === l.dir) ? { ...dirs, entries: [...dirs.entries, { path: l.dir, name: l.dir, kind: 'checkout', ships_here: 0, remotes: [] }] } : dirs;
  const favorites = new Set((skills?.skills ?? []).filter((s) => s.favorite).map((s) => s.name));
  const skillSel = l.skill.kind === 'skill' && !favorites.has(l.skill.name) ? 'custom' : skillValue(l.skill);

  const err = error ? { field: errorField(error), text: errorText(error) } : problem ? { field: problem.field, text: problem.message } : null;
  const save = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setSaving(true);
    setError('');
    const r = routine ? await call<Routine>('PUT', api(fleet.key, `/${encodeURIComponent(routine.id)}`), routineBody({ ...form, enabled: routine.enabled })) : await call<Routine>('POST', api(fleet.key), routineBody(form));
    setBusy(false);
    setSaving(false);
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

      <fieldset class="seg" role="radiogroup">
        <legend>Runs on</legend>
        {(form.mode === 'digest' ? (['schedule'] as const) : (['schedule', 'event'] as const)).map((k) => (
          <label key={k} class={(k === 'event') === !!form.on ? 'seg-opt on' : 'seg-opt'}>
            <input
              type="radio"
              name="when-kind"
              checked={(k === 'event') === !!form.on}
              onChange={() => {
                fixes('when');
                // An event routine launches a ship; a prompt has no event to answer.
                setForm((f) => {
                  if (k === 'event') return { ...f, on: f.on ?? lastEvent.current ?? { source: '', kind: '' }, mode: 'launch' };
                  lastEvent.current = f.on ?? lastEvent.current;
                  return { ...f, on: undefined };
                });
              }}
            />
            {k === 'schedule' ? 'A schedule' : 'An event'}
          </label>
        ))}
      </fieldset>
      {form.on ? (
        <>
          <label class="field">
            <span>When</span>
            <select value={eventValue(form.on)} onChange={(e) => {
                const on = choices.find((c) => c.value === target(e).value)?.on ?? { source: '', kind: '' };
                lastEvent.current = on;
                set({ on });
              }}>
              {!choices.some((c) => c.value === eventValue(form.on!)) ? <option value={eventValue(form.on)}>{triggerRemoved(form.on) ? 'New in view (removed)' : form.on.kind ? eventWords(sources.value, fleet.key, form.on) : 'Choose an event'}</option> : null}
              {groups.map(([group, list]) => (
                <optgroup key={group} label={group}>
                  {list.map((c) => (
                    <option key={c.value} value={c.value}>
                      {c.title}
                    </option>
                  ))}
                </optgroup>
              ))}
            </select>
          </label>
          {triggerRemoved(form.on) ? <p class="note">{REMOVED_NOTE} Pick another event or a schedule to fix it.</p> : null}
          {choices.length === 0 ? <p class="note">{noEventsNote(sources.value, fleet.key)}</p> : triggerRemoved(form.on) ? null : <p class="note">Runs when this happens, with the Lookout reading the event first.</p>}
        </>
      ) : (
        <>
          <label class="field">
            <span>When</span>
            <input type="text" value={form.when} autocomplete="off" autocapitalize="off" spellcheck={false} placeholder="30 8 * * 1-5" onInput={(e) => set({ when: target(e).value })} />
          </label>
          <WhenNote when={form.when} zone={zone} />
        </>
      )}
      {err?.field === 'when' ? <p class="note error" role="alert">{err.text}</p> : null}

      <fieldset class="seg" role="radiogroup">
        <legend>Do</legend>
        {(form.on ? (['launch'] as const) : (['launch', 'prompt', 'digest'] as const)).map((m) => (
          <label key={m} class={form.mode === m ? 'seg-opt on' : 'seg-opt'}>
            <input type="radio" name="do-mode" checked={form.mode === m} onChange={() => set(m === 'digest' && !routine && form.mode !== 'digest' ? { mode: m, before: 'act' } : { mode: m })} />
            {m === 'launch' ? 'Launch a ship' : m === 'prompt' ? 'AdmiralBot prompt' : 'Post a fleet digest'}
          </label>
        ))}
      </fieldset>

      {form.mode === 'digest' ? (
        <label class="field">
          <span>Extra words (optional)</span>
          <textarea rows={3} value={form.extra} onInput={(e) => set({ extra: target(e).value })} />
          <span class="note">AdmiralBot posts what changed in this fleet since the last digest, and reads only: it cannot act.</span>
        </label>
      ) : form.mode === 'prompt' ? (
        <label class="field">
          <span>Prompt</span>
          <textarea rows={4} value={form.prompt} onInput={(e) => set({ prompt: target(e).value })} />
        </label>
      ) : (
        <div class="routine-launch">
          {offered.length === 0 ? <p class="note warn">{docks.why || 'Loading docks…'}</p> : <DockPicker docks={offered.map((harness) => ({ harness }))} harness={l.harness} onChange={(harness) => { fixes('dock'); setForm((f) => ({ ...f, launch: launchOnDock(f.launch, harness) })); }} />}
          {err?.field === 'dock' ? <p class="note error" role="alert">{err.text}</p> : null}
          <CheckoutSelect dir={l.dir} dirs={dirsShown} allowClone={false} onChange={(dir) => setLaunch({ dir })} />
          {scopeLine && err?.field !== 'checkout' ? <p class="note">{scopeLine}</p> : null}
          {err?.field === 'checkout' ? <p class="note error" role="alert">{err.text}</p> : null}
          <label class="field">
            <span>Text</span>
            <textarea rows={4} value={l.text} onInput={(e) => setLaunch({ text: target(e).value })} />
          </label>
          {err?.field === 'text' ? <p class="note error" role="alert">{err.text}</p> : null}
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
          <ModelEffortPair harness={l.harness} dock={fleet.docks.find((d) => d.harness === l.harness)} model={l.model} effort={l.effort} onModel={(model) => setLaunch({ model })} onEffort={(effort) => setLaunch({ effort })} />
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
      <Actions class="routine-actions">
        <Button label="Save" variant="primary" type="submit" busy={saving} disabled={busy && !saving} />
        {routine ? (
          <Button
            label={confirming ? 'Delete routine?' : 'Delete'}
            variant="danger"
            pending={['routine', routine.id, 'delete']}
            onClick={() => {
              if (confirming) return remove();
              setConfirming(true);
            }}
            onBlur={() => setConfirming(false)}
          />
        ) : null}
      </Actions>
    </form>
  );
}

type LinkVisit = { status: 'loading' } | { status: 'ready'; form: RoutineForm; problem?: { field: RoutineField; message: string } } | { status: 'gone' } | { status: 'error'; error: string };

// One status per link visit: the page stays mounted as `draft=` comes and goes, so it resets when the link leaves.
function useLinkedDraft(fleetKey: string, linkId: string | null): LinkVisit | null {
  const [answer, setAnswer] = useState<{ id: string; visit: LinkVisit } | null>(null);
  useEffect(() => {
    if (!linkId) return;
    if (linkId === '.' || linkId === '..') {
      setAnswer({ id: linkId, visit: { status: 'gone' } });
      return () => setAnswer(null);
    }
    let live = true;
    void (async () => {
      const r = await call<{ draft: Omit<Routine, 'id'>; problems: { field: string; message: string }[] | null }>('GET', routineDraftPath(fleetKey, linkId));
      if (!live) return;
      if (r.ok) {
        const [form, problem] = openedDraft(r.data);
        setAnswer({ id: linkId, visit: { status: 'ready', form, problem } });
      } else setAnswer({ id: linkId, visit: r.status === 404 ? { status: 'gone' } : { status: 'error', error: r.error } });
    })();
    return () => {
      live = false;
      setAnswer(null);
    };
  }, [fleetKey, linkId]);
  if (!linkId) return null;
  return answer?.id === linkId ? answer.visit : { status: 'loading' };
}

function WhenNote({ when, zone }: { when: string; zone?: string }) {
  if (!when.trim()) return <p class="note">Five fields: minute hour day month weekday{`, in ${zone ?? "the server's time zone"}.`}</p>;
  const d = describeCron(when, zone);
  return <p class="note">{d.ok ? d.text : d.reason}</p>;
}

function whenText(when: string, zone?: string): string {
  const d = describeCron(when, zone);
  return d.ok ? d.text : d.reason;
}

export function RoutinesPage({ fleet, id, creating, title, phone }: { fleet: Fleet; id: string | null; creating: boolean; title: boolean; phone: boolean }) {
  const [list, setList] = useState<Routine[] | null>(null);
  const [failed, setFailed] = useState('');
  const [spend, setSpend] = useState<RoutineSpend | undefined>();
  const [zone, setZone] = useState<string | undefined>();
  const [drafted, setDrafted] = useState<{ form: RoutineForm; problem?: { field: RoutineField; message: string }; n: number } | null>(null);
  const caps: FleetCapabilities | undefined = capabilities.value.find((c) => c.key === fleet.key);
  const proposeNote = proposeOnlyNote(caps, normalizeLimits(fleet.limits));
  const capsError = capabilitiesFailed.value && !caps ? "Could not load the fleet's capabilities." : '';
  const [runs, setRuns] = useState<RoutineRun[] | null>(null);
  const [version, setVersion] = useState(0);
  const [tripped, setTripped] = useState(false);
  const lastSeen = useRef<string | null>(null);
  const search = fleetSearch.value;
  const linkId = creating ? new URLSearchParams(loc.value.search).get('draft') : null;
  const visit = useLinkedDraft(fleet.key, linkId);

  const load = async () => {
    const r = await call<{ routines: Routine[] | null; spend?: RoutineSpend; zone?: string }>('GET', api(fleet.key));
    if (r.ok) {
      setList(r.data.routines ?? []);
      setSpend(r.data.spend);
      setZone(r.data.zone || undefined);
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
      {spendNotice(spend) ? (
        <p class="note warning" role="status">
          {spendNotice(spend)}
        </p>
      ) : null}
      {proposeNote ? (
        <p class="note" role="status">
          {proposeNote}
        </p>
      ) : null}
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
            <Button label="Try again" onClick={() => void load()} />
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
  const judgeOff = !!caps?.judge && !caps.judge.harness;
  const opened = visit?.status === 'ready' ? visit : visit ? null : drafted;
  const draft = opened && 'form' in opened ? opened.form : blankForm();

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
              <Button
                label="New routine"
                href={routineNewPath + search}
                onClick={(e) => {
                  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                  e.preventDefault();
                  setDrafted(null);
                  go(routineNewPath, search);
                }}
              />
            </div>
            {list.length === 0 ? <p class="line">No routines yet. A routine launches a ship, sends AdmiralBot a prompt or posts a fleet digest on a schedule.</p> : null}
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
                      <span class="crew-sub">
                        {triggerRemoved(r.on) ? (
                          <>
                            <span class="pill">Broken</span> {REMOVED_NOTE}
                          </>
                        ) : r.on ? (
                          `On: ${eventWords(sources.value, fleet.key, r.on)}`
                        ) : r.do.digest ? (
                          `Fleet digest, ${whenText(r.when, zone)}`
                        ) : (
                          whenText(r.when, zone)
                        )}
                      </span>
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
                <Button label="Try again" onClick={() => void loadCaps()} />
              </p>
            ) : null}
            {creating && visit?.status === 'gone' ? (
              <p class="note" role="status">
                That draft has expired or can't be found. Describe the routine again or fill in the form.
              </p>
            ) : null}
            {creating && visit?.status === 'error' ? (
              <p class="alert" role="alert">
                {visit.error}
              </p>
            ) : null}
            {creating && visit?.status === 'loading' ? (
              <p class="line">Loading…</p>
            ) : creating ? (
              <RoutineEditor
                key={`new:${visit?.status === 'ready' ? `link:${linkId}` : visit ? 'blank' : drafted ? `described:${drafted.n}` : 'blank'}`}
                fleet={fleet}
                zone={zone}
                routine={null}
                initial={draft}
                caps={caps}
                problem={opened?.problem}
                onSaved={(r) => {
                  void load();
                  go(routinePath(r.id), search);
                }}
                onDeleted={() => toList()}
                onToggled={() => {}}
              />
            ) : selected ? (
              <>
                <RoutineEditor
                  key={`${selected.id}:${version}`}
                  fleet={fleet}
                  zone={zone}
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
