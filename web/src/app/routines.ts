import type { Dock, FleetCapabilities } from './api';
import type { SkillChoice } from './launchForm';

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
  void run;
  return { dot: 'idle', text: '' };
}

/** True when the runs hold a breaker record newer than `lastSeen`, the newest run id the screen showed before; false on a first load. */
export function breakerTripped(runs: RoutineRun[], lastSeen: string | null): boolean {
  void runs;
  void lastSeen;
  return false;
}

/** The part of the form a server 400 message belongs to. */
export function errorField(message: string): 'name' | 'when' | 'do' | 'form' {
  void message;
  return 'form';
}

/** The harnesses a routine may launch on: autopilot guard passed and the fleet has a judge. `why` says what is missing when none qualify. */
export function autopilotDocks(caps: FleetCapabilities | undefined, docks: Dock[]): { harnesses: string[]; why: string } {
  void caps;
  void docks;
  return { harnesses: [], why: '' };
}

export interface HoldFireBanner {
  scope: 'fleet' | 'all';
  by: string;
  at: string;
  text: string;
}
/** The shell-wide banner for Hold fire; null while neither switch is on. */
export function holdFireBanner(state: HoldFireState | null, fleetName: string, now: Date): HoldFireBanner | null {
  void state;
  void fleetName;
  void now;
  return { scope: 'fleet', by: '', at: '', text: '' };
}

/** The note under Before acting while Hold fire is on and the routine is set to Act; null otherwise. */
export function holdFireNote(before: Before, holdFireOn: boolean): string | null {
  void before;
  void holdFireOn;
  return '';
}

export interface AutomateView {
  sourceKey: string;
  sourceName: string;
  viewTitle: string;
}
/** The unsaved scheduled routine Automate… drafts for a view; no launch form when no dock qualifies. */
export function automateDraft(view: AutomateView, opts: { harness: string | null; dir: string; commodore: boolean }): RoutineForm {
  void view;
  void opts;
  return blankForm();
}

export function blankForm(): RoutineForm {
  return { name: '', enabled: true, when: '', mode: 'launch', launch: { harness: '', dir: '', text: '', model: '', effort: '', args: '', skill: { kind: 'none' }, resources: [] }, prompt: '', before: 'ask_first', notify: false };
}

/** The request body for create and update. */
export function routineBody(form: RoutineForm): Record<string, unknown> {
  void form;
  return {};
}

/** The form a stored routine opens in. */
export function formOfRoutine(r: Routine): RoutineForm {
  void r;
  return blankForm();
}

/** One Automate… link for each entry of a migration notice; none for a plain note. */
export function noticeEntryLinks(notice: { body?: string; entries?: { key: string; name: string; panel: string }[] } | undefined): { label: string; path: string }[] {
  void notice;
  return [{ label: '', path: '' }];
}
