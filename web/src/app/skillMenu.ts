export interface Skill {
  name: string;
  description: string;
}
/** A ship's or boat's skills and the mark its harness types before one: / for Claude Code and Copilot, $ for Codex. */
export interface SkillList {
  mark: string;
  skills: Skill[];
}

const tokenEnd = (text: string) => {
  const m = /\s/.exec(text);
  return m ? m.index : text.length;
};

/** The skill name being typed: the box starts with the mark and the caret is still in its first word. Null when the menu has nothing to do. */
export function skillQuery(text: string, caret: number, mark: string): string | null {
  if (!mark || !text.startsWith(mark) || caret < mark.length) return null;
  const end = tokenEnd(text);
  return caret > end ? null : text.slice(mark.length, end);
}

/** The skills that match what is typed, case aside: a name that starts with it, then a plugin skill whose own part does, then a name that holds it. */
export function matchSkills(skills: Skill[], query: string): Skill[] {
  const q = query.toLowerCase();
  const rank = (name: string) => {
    const n = name.toLowerCase();
    if (n.startsWith(q)) return 0;
    if (n.slice(n.indexOf(':') + 1).startsWith(q)) return 1;
    return n.includes(q) ? 2 : -1;
  };
  return skills
    .map((s) => ({ s, r: rank(s.name) }))
    .filter((x) => x.r >= 0)
    .sort((a, b) => a.r - b.r || a.s.name.localeCompare(b.s.name))
    .map((x) => x.s);
}

/** The box with its first word replaced by the picked skill and a space, and where the caret goes: after that space, before whatever followed. */
export function acceptSkill(text: string, mark: string, name: string): { text: string; caret: number } {
  const head = `${mark}${name} `;
  return { text: head + text.slice(tokenEnd(text)).trimStart(), caret: head.length };
}
