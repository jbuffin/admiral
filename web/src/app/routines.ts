import type { Dock, FleetCapabilities } from './api';
import type { SkillChoice } from './launchForm';
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
  do: { launch?: RoutineLaunch; prompt?: string };
  before_acting: Before;
  notify: boolean;
  off_by?: string;
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

/** True when the runs (newest first) hold a breaker record newer than `lastSeen`, the newest run id the screen showed before; false on a first load. */
export function breakerTripped(runs: RoutineRun[], lastSeen: string | null): boolean {
  if (lastSeen === null) return false;
  const seen = runs.findIndex((r) => r.id === lastSeen);
  return runs.slice(0, seen < 0 ? runs.length : seen).some((r) => r.outcome === 'breaker');
}

/** The part of the form a server 400 message belongs to. */
export function errorField(message: string): 'name' | 'when' | 'do' | 'form' {
  if (/^name\b/.test(message)) return 'name';
  if (/^when\b/.test(message)) return 'when';
  if (/^(do\b|the prompt|autopilot|args|skill|no resource|fleet \S+ has no)/.test(message)) return 'do';
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
export function noticeEntryLinks(notice: { body?: string; entries?: { key: string; name: string; panel: string }[] } | undefined): { label: string; path: string }[] {
  return (notice?.entries ?? []).map((e) => ({ label: `Automate… ${e.name}, view ${e.panel}`, path: routineNewPath + automateSearch(e.key, e.panel) }));
}
