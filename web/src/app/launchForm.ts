import { skillWarning } from './crewSkills';
import { matchSkills } from './skillMenu';
import type { SkillView } from './campaign';

export interface SourceView {
  key: string;
  fleet: string;
}
export type SkillChoice = { kind: 'commodore' | 'none' } | { kind: 'skill'; name: string };
export interface SkillsAnswer {
  /** What the dock's harness types before a skill name: / for Claude Code and Copilot, $ for Codex. */
  mark: string;
  commodore: { found: boolean; name: string };
  skills: { name: string; description: string; favorite: boolean }[];
}
export interface LaunchFormState {
  fleet: string;
  harness: string;
  dir: string;
  clone: boolean;
  // The repo the form showed for the clone, so the server can refuse a launch whose order changed under it.
  cloneRepo?: string;
  skill: SkillChoice;
  text: string;
  model: string;
  effort: string;
  autopilot: boolean;
  resources: string[];
  source?: string;
  orderId?: string;
  /** A second ship on an order that already has one. */
  anyway?: boolean;
  /** Launch on the GitHub stack `orderId`'s layer sits in. */
  stack?: boolean;
  crew?: string[];
  // The "Launch as" member and the harness it is locked to, if any.
  member?: string;
  memberHarness?: string | null;
}

export interface CrewMemberView {
  name: string;
  role: string;
  layer: string;
  harness: string | null;
  'pick-me-when'?: string;
}
export interface CrewOption extends CrewMemberView {
  hint: string | null;
}
export interface CrewAnswer {
  members: CrewMemberView[];
  warnings: { message: string }[];
  errors: { layer: string; file: string | null; message: string }[];
}

/** The members a pin control offers: one locked to another harness can be pinned and says where it runs. */
export function crewOptions(members: CrewMemberView[], harness: string): CrewOption[] {
  return members.map((m) => ({ ...m, hint: m.harness !== null && m.harness !== harness ? `runs on ${m.harness}` : null }));
}

/** Where a "Launch as" member runs: its lock, else the form's harness, and a hint when that is not the form's. */
export function memberRun(members: CrewMemberView[], harness: string, name: string): { harness: string; hint: string | null } | null {
  const m = members.find((x) => x.name === name);
  if (!m) return null;
  // A fix checker's harness is ignored, so it runs where the form says.
  const on = m.role === 'fix-checker' ? harness : (m.harness ?? harness);
  return { harness: on, hint: on !== harness ? `runs on ${on}` : null };
}

/** Pins or unpins a member; pinning replaces any other pinned member of the same role. */
export function togglePin(pinned: string[], members: CrewMemberView[], name: string): string[] {
  if (pinned.includes(name)) return pinned.filter((n) => n !== name);
  const role = members.find((m) => m.name === name)?.role;
  return [...pinned.filter((n) => members.find((m) => m.name === n)?.role !== role), name];
}

// Copilot offers none: it picks its own effort.
const EFFORTS: Record<string, string[]> = {
  claude: ['low', 'medium', 'high', 'max'],
  codex: ['low', 'medium', 'high'],
};

// The server doesn't filter sources by fleet.
export function sourcesForFleet<T extends SourceView>(sources: T[], fleet: string): T[] {
  return sources.filter((s) => s.fleet === fleet);
}

export function defaultSkill(answer: Pick<SkillsAnswer, 'commodore'> & { skills: { name: string; favorite: boolean }[] }): SkillChoice {
  if (answer.commodore.found) return { kind: 'commodore' };
  const favorite = answer.skills.find((s) => s.favorite);
  return favorite ? { kind: 'skill', name: favorite.name } : { kind: 'none' };
}

export function launchBody(form: LaunchFormState): Record<string, unknown> {
  const member = form.skill.kind === 'none' && form.member ? form.member : '';
  const harness = member && form.memberHarness ? form.memberHarness : form.harness;
  // The model and effort the form holds belong to the dock it chose, not to a member's lock.
  const moved = harness !== form.harness;
  const effort = moved && !effortsFor(harness).includes(form.effort) ? '' : form.effort;
  return {
    fleet: form.fleet,
    // The member's lock goes in the body only; the form keeps the dock it chose.
    harness,
    skill: form.skill,
    text: form.text,
    autopilot: form.autopilot,
    resources: form.resources,
    ...(form.clone ? { clone: true, ...(form.cloneRepo && { clone_repo: form.cloneRepo }) } : { dir: form.dir }),
    ...(form.model && !moved && { model: form.model }),
    ...(effort && { effort }),
    ...(form.source && { source: form.source }),
    ...(form.orderId && { order_id: form.orderId }),
    ...(form.anyway && { anyway: true }),
    ...(form.stack && { stack: true }),
    ...(form.skill.kind === 'commodore' && form.crew?.length && { crew: form.crew }),
    ...(member && { member }),
  };
}

export interface CampaignLaunchState {
  campaignId: string;
  childUrl?: string;
  /** The URLs of a slice, in row order; set instead of childUrl. */
  issues?: string[];
  harness: string;
  dir: string;
  clone: boolean;
  cloneRepo?: string;
  startAnyway: boolean;
  model: string;
  effort: string;
  /** The skill picked for this launch only; a child takes it, a slice never does. */
  skill?: SkillChoice;
}

/** The order the dirs call names for a campaign child: source `campaign`, id `<campaign id>/<child URL>`. */
export function campaignOrder(campaignId: string, childUrl: string): { source: string; id: string } {
  return { source: 'campaign', id: `${campaignId}/${childUrl}` };
}

/** The order the dirs call names for a slice: source `campaign`, the compact id the server answers. */
export function sliceOrder(campaignId: string, urls: string[]): { source: string; id: string } {
  const first = urls[0] ?? '';
  const prefix = first.slice(0, first.lastIndexOf('/') + 1);
  const rest = urls.slice(1).map((u) => (u.startsWith(prefix) && /^\d+$/.test(u.slice(prefix.length)) ? u.slice(prefix.length) : u));
  return campaignOrder(campaignId, [first, ...rest].join(','));
}

/** The route and body of a campaign child's launch; the server picks text and autopilot, and the skill unless one is sent. */
export function campaignLaunch(form: CampaignLaunchState): { path: string; body: Record<string, unknown> } {
  return {
    path: `/campaigns/${form.campaignId}/launch`,
    body: {
      ...(form.issues ? { issues: form.issues } : { child_url: form.childUrl, ...(form.skill && { skill: form.skill }) }),
      harness: form.harness,
      ...(form.clone ? { clone: true, ...(form.cloneRepo && { clone_repo: form.cloneRepo }) } : { dir: form.dir }),
      ...(form.startAnyway && { start_anyway: true }),
      ...(form.model && { model: form.model }),
      ...(form.effort && { effort: form.effort }),
    },
  };
}

export function effortsFor(harness: string): string[] {
  return EFFORTS[harness] ?? [];
}

// Claude's models are a closed list the server checks; the other harnesses take any model name.
export const CLAUDE_MODELS = ['sonnet', 'opus', 'haiku'];

export const bareSkillName = (typed: string) => typed.trim().replace(/^[/$]/, '');

export interface SkillOption {
  choice: SkillChoice;
  label: string;
  description: string;
  /** What the harness types before the name; empty for Commodore and Just launch. */
  mark: string;
  disabled?: boolean;
}

export const sameChoice = (a: SkillChoice, b: SkillChoice) => a.kind === b.kind && (a.kind !== 'skill' || (b.kind === 'skill' && a.name === b.name));

/** The rows of the picker's list: Commodore, the favorites and Just launch until something is typed, then the installed skills that match. */
export function skillOptions(answer: SkillsAnswer, typed: string): SkillOption[] {
  const query = bareSkillName(typed);
  const row = (s: SkillsAnswer['skills'][number]): SkillOption => ({ choice: { kind: 'skill', name: s.name }, label: s.name, description: s.description, mark: answer.mark });
  if (query) return matchSkills(answer.skills, query).map(row);
  const { found } = answer.commodore;
  return [
    { choice: { kind: 'commodore' }, label: 'Commodore', description: found ? '' : 'not found', mark: '', ...(!found && { disabled: true }) },
    ...answer.skills.filter((s) => s.favorite).map(row),
    { choice: { kind: 'none' }, label: 'Just launch', description: '', mark: '' },
  ];
}

/** What a typed name stands for: the installed skill it names exactly (one leading mark dropped), else the crew picker's warning. */
export function typedSkill(answer: SkillsAnswer, typed: string, harness: string): { choice: SkillChoice } | { warning: string } {
  const name = bareSkillName(typed);
  const warning = skillWarning(name, answer.skills, harness);
  return warning ? { warning } : { choice: { kind: 'skill', name } };
}

/** The pick after the dock or checkout changes: kept when this dock still offers it, else the default. */
export function keepSkill(answer: SkillsAnswer, current: SkillChoice): SkillChoice {
  const offered = current.kind === 'commodore' ? answer.commodore.found : current.kind === 'skill' ? answer.skills.some((s) => s.name === current.name) : true;
  return offered ? current : defaultSkill(answer);
}

/** Where a campaign child's picker starts: its resolved skill whether or not this dock offers it (the form then warns), else the default. */
export function startSkill(answer: SkillsAnswer, view: SkillView | undefined): SkillChoice {
  return viewChoice(view) ?? defaultSkill(answer);
}

/** The pick a campaign child's resolved skill stands for; null when it has none. */
export function viewChoice(view: SkillView | undefined): SkillChoice | null {
  if (!view || view.kind === '') return null;
  return view.kind === 'skill' ? { kind: 'skill', name: bareSkillName(view.name) } : { kind: view.kind };
}

/** The warning for a pick this dock doesn't offer, else null. */
export function pickWarning(answer: SkillsAnswer, choice: SkillChoice, harness: string): string | null {
  if (choice.kind === 'skill') return skillWarning(choice.name, answer.skills, harness);
  if (choice.kind === 'commodore' && !answer.commodore.found) return harness ? `not installed on ${harness}` : 'not found';
  return null;
}
