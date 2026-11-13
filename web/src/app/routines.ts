import type { Dock, FleetCapabilities, Source } from './api';
import { effortsFor, sourcesForFleet, type SkillChoice } from './launchForm';
import { automateSearch, routineNewPath } from './router';

export type Before = 'report_only' | 'ask_first' | 'act';
export type Outcome = 'launched' | 'proposed' | 'skipped' | 'missed' | 'failed' | 'reported' | 'breaker';

export interface RoutineLaunch {
  harness: string;
  dir: string;
  text: string;
  model?: string;
  effort?: string;
  args?: string[];
  skill?: SkillChoice;
  resources?: string[];
}
export interface Routine {
  id: string;
  name: string;
  enabled: boolean;
  when: string;
  /** A source event the routine runs on, instead of a schedule; `when` is empty then. */
  on?: { source: string; kind: string; panel?: string };
  do: { launch?: RoutineLaunch; prompt?: string };
  before_acting: Before;
  notify: boolean;
  off_by?: string;
  /** The newest trigger record; absent for a routine that never ran. */
  last_run?: { at: string; outcome: Outcome };
}
export interface RoutineRun {
  id: string;
  at: string;
  routine: string;
  outcome: Outcome;
  ship?: string;
  via?: string;
  reason?: string;
  action?: string;
  undone?: boolean;
  tier?: string;
  event?: string;
  sender?: string;
  costs?: { call: string; usd: number }[];
  dedupe_key?: string;
}
export interface RoutineSpend {
  today_usd: number;
  cap_usd: number;
  reached: boolean;
}
interface Switch {
  on: boolean;
  by?: string;
  at?: string;
}
export interface HoldFireState {
  on: boolean;
  fleet: Switch;
  global: Switch;
}

/** What the editor form holds; args is the one text field the server's list is typed into. */
export interface RoutineForm {
  name: string;
  enabled: boolean;
  when: string;
  on?: Routine['on'];
  mode: 'launch' | 'prompt';
  launch: { harness: string; dir: string; text: string; model: string; effort: string; args: string; skill: SkillChoice; resources: string[] };
  prompt: string;
  before: Before;
  notify: boolean;
}

export type RunDot = 'ok' | 'idle' | 'bad';

/** A Recent runs row's outcome in words; `ship` is set only when the row links to a ship. */
export function runLine(run: RoutineRun): { dot: RunDot; text: string; ship?: string } {
  const why = run.reason ? `: ${run.reason}` : '';
  switch (run.outcome) {
    case 'launched': {
      const from = run.via === 'proposal' ? ' (from an accepted proposal)' : '';
      const undone = run.undone ? ', undone' : '';
      if (run.ship) return { dot: 'ok', text: `Launched ${run.ship}${from}${undone}`, ship: run.ship };
      return { dot: 'ok', text: `AdmiralBot prompt sent${from}${undone}` };
    }
    case 'proposed':
      return { dot: 'ok', text: 'Proposed, waiting in the Inbox for your go-ahead' };
    case 'reported':
      return { dot: 'idle', text: `Report only, would have run: ${run.action ?? 'its action'}` };
    case 'skipped':
      return { dot: 'idle', text: `Skipped${why}` };
    case 'missed':
      return { dot: 'idle', text: `Missed${why}` };
    case 'failed':
      return { dot: 'bad', text: `Failed${why}` };
    case 'breaker':
      return { dot: 'bad', text: 'Failed too often in a row, so it moved to Ask first' };
  }
}

/** The Lookout cost of a Recent runs row in words; empty when the record has no costs. */
export function runCost(run: RoutineRun): string {
  void run;
  return '';
}

/** The event's link when it is https; null for anything else. */
export function eventHref(event: string | undefined): string | null {
  void event;
  return null;
}

/** The sentence for a fleet whose daily worker spend is reached; null before that. */
export function spendNotice(spend: RoutineSpend | undefined): string | null {
  void spend;
  return null;
}

/** True when the runs (newest first) hold a breaker record newer than `lastSeen`, the newest run id the screen showed before; false on a first load. */
export function breakerTripped(runs: RoutineRun[], lastSeen: string | null): boolean {
  if (lastSeen === null) return false;
  const seen = runs.findIndex((r) => r.id === lastSeen);
  return runs.slice(0, seen < 0 ? runs.length : seen).some((r) => r.outcome === 'breaker');
}

const INVALID = /^invalid routine: /;

/** A server 400 message without the "invalid routine: " the store puts before every validation error. */
export const errorText = (message: string) => message.replace(INVALID, '');

/** The part of the form a server 400 message belongs to. */
export function errorField(message: string): 'name' | 'when' | 'before_acting' | 'do' | 'form' {
  const m = errorText(message);
  if (/^name\b/.test(m)) return 'name';
  if (/^when\b/.test(m)) return 'when';
  if (/^before_acting\b|^before acting\b/i.test(m)) return 'before_acting';
  if (/^(do\b|the prompt|autopilot|args|skill|no resource|fleet \S+ has no)/.test(m)) return 'do';
  return 'form';
}

/** The harnesses a routine may launch on: autopilot guard passed and the fleet has a judge. `why` says what is missing when none qualify. */
export function autopilotDocks(caps: FleetCapabilities | undefined, docks: Dock[]): { harnesses: string[]; why: string } {
  if (!caps) return { harnesses: [], why: '' };
  if (!caps.judge?.harness) return { harnesses: [], why: `A routine needs the fleet's advisor: ${caps.judge?.reason ?? 'none is proven yet'}.` };
  const proven = new Set(caps.harnesses.filter((h) => h.features.some((f) => f.capabilities.some((c) => c.id === 'autopilot.guard_hook' && c.status === 'passed'))).map((h) => h.harness));
  const harnesses = docks.map((d) => d.harness).filter((h) => proven.has(h));
  return { harnesses, why: harnesses.length ? '' : 'No dock has its autopilot guard proven yet. Run its checks in Preferences, under Capabilities.' };
}

export interface HoldFireBanner {
  scope: 'fleet' | 'all';
  by: string;
  at: string;
  text: string;
}

const ago = (at: string, now: Date) => {
  const min = Math.floor((now.getTime() - new Date(at).getTime()) / 60_000);
  if (!(min >= 0)) return '';
  if (min < 1) return 'just now';
  if (min < 60) return `${min} min ago`;
  const h = Math.floor(min / 60);
  return h < 24 ? `${h} h ago` : `${Math.floor(h / 24)} d ago`;
};

/** A routine row's last run: how long ago, or Never. */
export function lastRunText(last: Routine['last_run'], now: Date): string {
  return last ? ago(last.at, now) || 'just now' : 'Never';
}

const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const pad = (n: number) => String(n).padStart(2, '0');

/** A run's time as the canvas writes it: Today 09:38, Yesterday 17:02, Mon 08:30 within the week, else Nov 3. */
export function runTime(at: string, now: Date): string {
  const d = new Date(at);
  if (Number.isNaN(d.getTime())) return '';
  const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const back = Math.round((day(now) - day(d)) / 86_400_000);
  const clock = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  if (back === 0) return `Today ${clock}`;
  if (back === 1) return `Yesterday ${clock}`;
  if (back > 1 && back < 7) return `${WEEKDAYS[d.getDay()]} ${clock}`;
  return `${MONTHS[d.getMonth()]} ${d.getDate()}`;
}

/** The launch with another dock: its checkout resets, and a model or effort that dock doesn't offer goes, as the launch dialog's body does. */
export function launchOnDock(l: RoutineForm['launch'], harness: string): RoutineForm['launch'] {
  return { ...l, harness, dir: '', model: '', effort: effortsFor(harness).includes(l.effort) ? l.effort : '' };
}

/** The source and view an Automate… query names, when the fleet has both. */
export function automateTarget(sources: Source[], fleetKey: string, q: { source: string; view: string }): { source: Source; view: Source['views'][number] } | null {
  const source = sourcesForFleet(sources, fleetKey).find((s) => s.key === q.source);
  const view = source?.views.find((v) => v.id === q.view);
  return source && view ? { source, view } : null;
}

/** The shell-wide banner for Hold fire; null while neither switch is on. */
export function holdFireBanner(state: HoldFireState | null, fleetName: string, now: Date): HoldFireBanner | null {
  if (!state) return null;
  const scope = state.global.on ? 'all' : state.fleet.on ? 'fleet' : null;
  if (!scope) return null;
  const sw = scope === 'all' ? state.global : state.fleet;
  const by = sw.by ?? '';
  const at = sw.at ?? '';
  const who = by === 'admiralbot' ? 'AdmiralBot' : 'you';
  const when = at ? ago(at, now) : '';
  const where = scope === 'all' ? 'all fleets' : fleetName;
  return { scope, by, at, text: `Hold fire is on for ${where}: routines ask first. Turned on by ${who}${when ? `, ${when}` : ''}.` };
}

/** The note under Before acting while Hold fire is on and the routine is set to Act; null otherwise. */
export function holdFireNote(before: Before, holdFireOn: boolean): string | null {
  return before === 'act' && holdFireOn ? 'Hold fire is on, so this routine will ask first until it is off. Its setting stays Act.' : null;
}

export interface AutomateView {
  sourceKey: string;
  sourceName: string;
  viewTitle: string;
}
/** The unsaved scheduled routine Automate… drafts for a view; a prompt for AdmiralBot when no dock qualifies. */
export function automateDraft(view: AutomateView, opts: { harness: string | null; dir: string; commodore: boolean }): RoutineForm {
  const text = `Look at the "${view.viewTitle}" view of ${view.sourceName} (source ${view.sourceKey}), pick up what is new in it and work on it.`;
  const form: RoutineForm = { ...blankForm(), name: `Automate ${view.viewTitle}`, when: '30 8 * * 1-5', before: 'ask_first' };
  if (opts.harness === null) return { ...form, mode: 'prompt', prompt: text };
  return { ...form, launch: { ...form.launch, harness: opts.harness, dir: opts.dir, text, skill: { kind: opts.commodore ? 'commodore' : 'none' } } };
}

export function blankForm(): RoutineForm {
  return { name: '', enabled: true, when: '', mode: 'launch', launch: { harness: '', dir: '', text: '', model: '', effort: '', args: '', skill: { kind: 'none' }, resources: [] }, prompt: '', before: 'ask_first', notify: false };
}

/** The request body for create and update. */
export function routineBody(form: RoutineForm): Record<string, unknown> {
  const l = form.launch;
  const args = l.args.split(/\s+/).filter(Boolean);
  const launch: RoutineLaunch = {
    harness: l.harness,
    dir: l.dir,
    text: l.text,
    ...(l.model ? { model: l.model } : {}),
    ...(l.effort ? { effort: l.effort } : {}),
    ...(args.length ? { args } : {}),
    ...(l.skill.kind !== 'none' ? { skill: l.skill } : {}),
    ...(l.resources.length ? { resources: l.resources } : {}),
  };
  return { name: form.name, enabled: form.enabled, when: form.when, do: form.mode === 'prompt' ? { prompt: form.prompt } : { launch }, before_acting: form.before, notify: form.notify };
}

/** The form a stored routine opens in. */
export function formOfRoutine(r: Routine): RoutineForm {
  const l = r.do.launch;
  const form = { ...blankForm(), name: r.name, enabled: r.enabled, when: r.when, before: r.before_acting, notify: r.notify };
  if (!l) return { ...form, mode: 'prompt', prompt: r.do.prompt ?? '' };
  return {
    ...form,
    launch: { harness: l.harness, dir: l.dir, text: l.text, model: l.model ?? '', effort: l.effort ?? '', args: (l.args ?? []).join(' '), skill: l.skill ?? { kind: 'none' }, resources: l.resources ?? [] },
  };
}

/** One Automate… link for each entry of a migration notice; none for a plain note. */
export function noticeEntryLinks(notice: { body?: string; entries?: { key: string; name: string; panel: string; fleet?: string }[] } | undefined): { label: string; path: string }[] {
  return (notice?.entries ?? []).map((e) => ({ label: `Automate… ${e.name}, view ${e.panel}`, path: routineNewPath + automateSearch(e.key, e.panel, e.fleet) }));
}
