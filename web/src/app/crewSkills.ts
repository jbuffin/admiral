import type { CrewMember } from './crew';

export interface SkillEntry {
  name: string;
  description: string;
  harnesses: string[];
}

export interface RankContext {
  role: string;
  pickMeWhen: string;
  chosen: string[];
  members: CrewMember[];
  self?: string;
}

export const rankSkills = (_skills: SkillEntry[], _ctx: RankContext): SkillEntry[] => [];
export const filterSkills = (_ranked: SkillEntry[], _query: string): SkillEntry[] => [];
export const pickSkill = (_chosen: string[], _name: string): string[] => [];
export const typeSkill = (_chosen: string[], _typed: string): { ok: true; skills: string[] } | { ok: false; reason: string } => ({ ok: false, reason: '' });
export const skillWarning = (_name: string, _listed: SkillEntry[], _harness: string): string | null => null;
