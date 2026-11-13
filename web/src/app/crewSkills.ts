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

// The rule a saved member applies to each skill name (skillPattern in internal/crew/member.go).
const SKILL_NAME = /^[A-Za-z0-9][A-Za-z0-9._:-]*$/;

// Words that say nothing about what a skill is for: pick-me-when lines all begin "Pick for…".
const IGNORED_WORDS = new Set(['pick', 'for', 'when']);

const wordsOf = (text: string) => text.toLowerCase().match(/[a-z0-9]+/g) ?? [];

const byName = (a: SkillEntry, b: SkillEntry) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0);

/** The picker's order: the member's own skills as chosen, then skills of the same role in other definitions, then description matches with the role or pick-me-when, then the rest A to Z. */
export function rankSkills(skills: SkillEntry[], ctx: RankContext): SkillEntry[] {
  const listed = new Map(skills.map((s) => [s.name, s]));
  const out: SkillEntry[] = [];
  const used = new Set<string>();
  const take = (list: SkillEntry[]) => {
    for (const s of [...list].sort(byName)) {
      if (!used.has(s.name)) {
        used.add(s.name);
        out.push(s);
      }
    }
  };
  for (const name of ctx.chosen) {
    const s = listed.get(name);
    if (s && !used.has(name)) {
      used.add(name);
      out.push(s);
    }
  }
  const peers = new Set(ctx.members.filter((m) => m.name !== ctx.self && m.role === ctx.role).flatMap((m) => m.skills));
  take(skills.filter((s) => peers.has(s.name)));
  const wanted = new Set([...wordsOf(ctx.role), ...wordsOf(ctx.pickMeWhen)].filter((w) => w.length >= 4 && !IGNORED_WORDS.has(w)));
  take(skills.filter((s) => wordsOf(s.description).some((w) => wanted.has(w))));
  take(skills);
  return out;
}

/** What matches the typed text in its name or description, still in ranked order. */
export function filterSkills(ranked: SkillEntry[], query: string): SkillEntry[] {
  const q = query.trim().toLowerCase();
  if (!q) return ranked;
  return ranked.filter((s) => s.name.toLowerCase().includes(q) || s.description.toLowerCase().includes(q));
}

export const pickSkill = (chosen: string[], name: string): string[] => (chosen.includes(name) ? chosen : [...chosen, name]);

export type Typed = { ok: true; skills: string[] } | { ok: false; reason: string };

/** A typed name joins the list only as a saved member would take it, and only once. */
export function typeSkill(chosen: string[], typed: string): Typed {
  const name = typed.trim();
  if (!name) return { ok: false, reason: 'Type a skill name.' };
  if (!SKILL_NAME.test(name)) return { ok: false, reason: 'A skill name starts with a letter or digit and holds only letters, digits and . _ : -' };
  if (chosen.includes(name)) return { ok: false, reason: `${name} is already added.` };
  return { ok: true, skills: [...chosen, name] };
}

/** What a pill says when the list it is checked against lacks the skill. */
export function skillWarning(name: string, listed: SkillEntry[], harness: string): string | null {
  if (listed.some((s) => s.name === name)) return null;
  return harness ? `not installed on ${harness}` : 'not found';
}
