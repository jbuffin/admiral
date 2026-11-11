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
  skill: SkillChoice;
  text: string;
  model: string;
  effort: string;
  autopilot: boolean;
  resources: string[];
  source?: string;
  orderId?: string;
}

export function sourcesForFleet<T extends SourceView>(_sources: T[], _fleet: string): T[] {
  throw new Error('not implemented');
}
export function defaultSkill(_answer: SkillsAnswer): SkillChoice {
  throw new Error('not implemented');
}
export function launchBody(_form: LaunchFormState): Record<string, unknown> {
  throw new Error('not implemented');
}
export function effortsFor(_harness: string): string[] {
  throw new Error('not implemented');
}
