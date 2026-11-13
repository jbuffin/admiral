import type { Limits } from './limitsForm';
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
  unreadable?: boolean;
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

const REASON_WORDS: Record<string, string> = {
  skip_filter: 'a skip filter matched',
  repo_not_allowed: 'the repo is not on the allowlist',
  top_action: "the sender's tier allows nothing more",
  daily_spend_reached: 'the daily worker spend is reached',
  daily_spend_unknown: "today's worker spend could not be read",
  quiet_hours: 'quiet hours',
  hold_fire: 'Hold fire is on',
  dedupe_key_held: 'the same event was already held',
  no_restricted_harness: 'no harness in this fleet has passed the restricted-ship probe',
};

/** A reason code, or a comma-joined list of them, in words; a code it doesn't know stays as it is. */
function reasonWords(reason: string): string {
  return reason
    .split(', ')
    .map((c) => REASON_WORDS[c] ?? (c.startsWith('lookout_') ? `the Lookout failed (${c.slice('lookout_'.length).replace(/_/g, ' ')})` : c))
    .join(', ');
}

/** A Recent runs row's outcome in words; `ship` is set only when the row links to a ship. */
export function runLine(run: RoutineRun): { dot: RunDot; text: string; ship?: string } {
  const why = run.reason ? `: ${reasonWords(run.reason)}` : '';
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
  const costs = run.costs ?? [];
  if (!costs.length) return '';
  const total = costs.reduce((n, c) => n + c.usd, 0);
  const estimated = costs.some((c) => c.call === 'lookout_estimated');
  return `Lookout cost ${estimated ? 'estimated ' : ''}$${total.toFixed(total < 0.01 ? 4 : 2)}`;
}

/** The event's link when it is https; null for anything else. */
export function eventHref(event: string | undefined): string | null {
  return event?.startsWith('https://') ? event : null;
}

/** The sentence for a fleet whose daily worker spend is reached; null before that. */
export function spendNotice(spend: RoutineSpend | undefined): string | null {
  if (!spend?.reached) return null;
  if (spend.unreadable) return "Today's worker spend could not be read, so events stop at a proposal until it can.";
  return `Worker spend today is $${spend.today_usd.toFixed(2)} of the $${spend.cap_usd.toFixed(2)} cap, so events stop at a proposal until the day turns.`;
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
  if (/^when\b|^on\b|^an event routine has no when/.test(m)) return 'when';
  if (/^an event routine launches a ship/.test(m)) return 'do';
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
  return { name: form.name, enabled: form.enabled, ...(form.on ? { on: form.on } : { when: form.when }), do: form.mode === 'prompt' ? { prompt: form.prompt } : { launch }, before_acting: form.before, notify: form.notify };
}

/** The form a stored routine opens in. */
export function formOfRoutine(r: Routine): RoutineForm {
  const l = r.do.launch;
  const form = { ...blankForm(), name: r.name, enabled: r.enabled, when: r.when, ...(r.on ? { on: r.on } : {}), before: r.before_acting, notify: r.notify };
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

export interface EventChoice {
  value: string;
  title: string;
  /** The optgroup label: the source, with view entries apart from the rest. */
  group: string;
  on: NonNullable<Routine['on']>;
}

export const eventValue = (on: NonNullable<Routine['on']>) => `event:${on.source}:${on.kind}${on.panel ? `:${on.panel}` : ''}`;

/** Every (kind, panel) entry the fleet's sources list, in the server's words. */
export function eventChoices(sources: Source[], fleetKey: string): EventChoice[] {
  return sourcesForFleet(sources, fleetKey).flatMap((s) =>
    (s.events ?? []).map((e) => {
      const on = { source: s.key, kind: e.kind, ...(e.panel ? { panel: e.panel } : {}) };
      return { value: eventValue(on), title: e.title, group: e.panel ? `${s.name}: new in a view` : s.name, on };
    }),
  );
}

/** Why the event picker is empty: the fleet has no source, or its sources haven't listed their events. */
export function noEventsNote(sources: Source[], fleetKey: string): string {
  if (sourcesForFleet(sources, fleetKey).length === 0) return 'This fleet has no source that raises events. Add one under Sources and its events show here.';
  return "This source hasn't listed its events yet. They show here once it has.";
}

/** A routine's event in the catalog's words; the stored kind when the source no longer lists it. */
export function eventWords(sources: Source[], fleetKey: string, on: NonNullable<Routine['on']>): string {
  const hit = eventChoices(sources, fleetKey).find((c) => c.value === eventValue(on));
  return hit?.title ?? on.kind.replace(/_/g, ' ');
}

/** The fleet-level note on the Routines screen when A3 routines run as propose only. */
export function proposeOnlyNote(_caps: FleetCapabilities | undefined, _limits: Limits | undefined): string {
  return '';
}
