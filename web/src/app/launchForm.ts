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
}

export interface CrewMemberView {
  name: string;
  role: string;
  layer: string;
  harness: string | null;
}
export interface CrewOption extends CrewMemberView {
  disabled: boolean;
}

/** The members a pin control offers: those locked to another harness are shown but cannot be pinned. */
export function crewOptions(members: CrewMemberView[], harness: string): CrewOption[] {
  return [];
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
  return {
    fleet: form.fleet,
    harness: form.harness,
    skill: form.skill,
    text: form.text,
    autopilot: form.autopilot,
    resources: form.resources,
    ...(form.clone ? { clone: true, ...(form.cloneRepo && { clone_repo: form.cloneRepo }) } : { dir: form.dir }),
    ...(form.model && { model: form.model }),
    ...(form.effort && { effort: form.effort }),
    ...(form.source && { source: form.source }),
    ...(form.orderId && { order_id: form.orderId }),
  };
}

export function effortsFor(harness: string): string[] {
  return EFFORTS[harness] ?? [];
}

// Claude's models are a closed list the server checks; the other harnesses take any model name.
export const CLAUDE_MODELS = ['sonnet', 'opus', 'haiku'];

export const bareSkillName = (typed: string) => typed.trim().replace(/^[/$]/, '');
