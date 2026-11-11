export interface SourceView {
  key: string;
  fleet: string;
}
export type SkillChoice = { kind: 'commodore' | 'none' } | { kind: 'skill'; name: string };
export interface SkillsAnswer {
  commodore: { found: boolean; name: string };
  skills: { name: string; favorite: boolean }[];
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

export function defaultSkill(answer: SkillsAnswer): SkillChoice {
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
}

/** The order the dirs call names for a campaign child: source `campaign`, id `<campaign id>/<child URL>`. */
export function campaignOrder(campaignId: string, childUrl: string): { source: string; id: string } {
  return { source: 'campaign', id: `${campaignId}/${childUrl}` };
}

/** The order the dirs call names for a slice: source `campaign`, the compact id the server answers. */
export function sliceOrder(campaignId: string, urls: string[]): { source: string; id: string } {
  return campaignOrder(campaignId, urls[0] ?? '');
}

/** The route and body of a campaign child's launch; the server picks skill, text and autopilot. */
export function campaignLaunch(form: CampaignLaunchState): { path: string; body: Record<string, unknown> } {
  return {
    path: `/campaigns/${form.campaignId}/launch`,
    body: {
      child_url: form.childUrl,
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
