import type { Dock } from './api';

export const CUSTOM_ROLE = '__custom__';
export const ROLES = ['implementer', 'test-writer', 'reviewer', 'security-reviewer', 'brief-checker', 'triager', 'target-steward', 'scribe', 'fix-checker'];
export const BASED_ON_ROLES = ROLES.filter((r) => r !== 'fix-checker');
export const HARNESSES = ['claude', 'codex', 'copilot'];
const FORM_FIELDS = ['role', 'based-on', 'skills', 'model', 'harness', 'pick-me-when', 'instructions'];

export interface Notes {
  fleet: string[];
  repos: Record<string, string[]>;
}

export interface CrewMember {
  name: string;
  role: string;
  'based-on': string;
  skills: string[];
  model: string;
  harness: string;
  'pick-me-when': string;
  instructions: string;
  layer: 'built-in' | 'fleet';
  customized: boolean;
  notes: Notes;
}

export interface Diagnostic {
  layer: string;
  file: string;
  code: string;
  field: string;
  message: string;
}

export interface Unclaimed {
  member: string;
  notes: Notes;
}

export interface CrewData {
  harness: string;
  members: CrewMember[];
  unclaimed: Unclaimed[];
  warnings: Diagnostic[];
  errors: Diagnostic[];
}

export interface MemberForm {
  role: string;
  customRole: string;
  basedOn: string;
  skills: string;
  model: string;
  harness: string;
  pickMeWhen: string;
  instructions: string;
}

export interface MemberBody {
  role: string;
  'based-on'?: string;
  skills?: string[];
  model?: string;
  harness?: string;
  'pick-me-when': string;
  instructions?: string;
}

const notesOf = (n: Partial<Notes> | null | undefined): Notes => ({ fleet: n?.fleet ?? [], repos: n?.repos ?? {} });

// Go sends null for empty slices, maps and strings.
export function normalizeCrew(raw: Record<string, unknown>): CrewData {
  const list = <T>(v: unknown): T[] => (Array.isArray(v) ? (v as T[]) : []);
  const diag = (d: Partial<Record<keyof Diagnostic, string | null>>): Diagnostic => ({ layer: d.layer ?? '', file: d.file ?? '', code: d.code ?? '', field: d.field ?? '', message: d.message ?? '' });
  return {
    harness: (raw.harness as string) ?? '',
    members: list<Record<string, unknown>>(raw.members).map((m) => ({
      name: m.name as string,
      role: m.role as string,
      'based-on': (m['based-on'] as string | null) ?? '',
      skills: list<string>(m.skills),
      model: (m.model as string | null) ?? '',
      harness: (m.harness as string | null) ?? '',
      'pick-me-when': (m['pick-me-when'] as string) ?? '',
      instructions: (m.instructions as string) ?? '',
      layer: m.layer as CrewMember['layer'],
      customized: !!m.customized,
      notes: notesOf(m.notes as Partial<Notes>),
    })),
    unclaimed: list<{ member: string; notes: Partial<Notes> }>(raw.unclaimed).map((u) => ({ member: u.member, notes: notesOf(u.notes) })),
    warnings: list<Record<string, string | null>>(raw.warnings).map(diag),
    errors: list<Record<string, string | null>>(raw.errors).map(diag),
  };
}

/** Fleet members first, built-in after, each group in the order given. Both groups always exist. */
export function groupMembers(members: CrewMember[]) {
  return [
    { title: 'Fleet', members: members.filter((m) => m.layer === 'fleet') },
    { title: 'Built-in', members: members.filter((m) => m.layer === 'built-in') },
  ];
}

export function formOf(m: CrewMember): MemberForm {
  const custom = !ROLES.includes(m.role);
  return {
    role: custom ? CUSTOM_ROLE : m.role,
    customRole: custom ? m.role : '',
    basedOn: custom ? m['based-on'] : '',
    skills: m.skills.join('\n'),
    model: m.model,
    harness: m.harness,
    pickMeWhen: m['pick-me-when'],
    instructions: m.instructions,
  };
}

/** The PUT body. A key with nothing to say is left out, and the fix checker takes no skills or instructions. */
export function memberBody(f: MemberForm): MemberBody {
  const custom = f.role === CUSTOM_ROLE;
  const role = custom ? f.customRole.trim() : f.role;
  const body: MemberBody = { role, 'pick-me-when': f.pickMeWhen.trim() };
  if (custom) body['based-on'] = f.basedOn;
  if (role !== 'fix-checker') {
    body.skills = f.skills.split('\n').map((s) => s.trim()).filter(Boolean);
    // The reader drops blank lines around a body anyway; a first line's indent is the author's.
    body.instructions = f.instructions.replace(/^(\s*\n)+/, '').trimEnd();
  }
  if (f.model.trim()) body.model = f.model.trim();
  if (f.harness) body.harness = f.harness;
  return body;
}

/** Which input a 400 belongs beside; an error naming no input of the form goes to the top. */
export function fieldError(e: { error: string; field?: string }): { field: string | null; message: string } {
  return { field: e.field && FORM_FIELDS.includes(e.field) ? e.field : null, message: e.error };
}

export interface Scope {
  key: string;
  title: string;
  scope: 'fleet' | 'repo';
  repo: string;
  notes: string[];
}

export const THIS_FLEET = 'This fleet';

/** The scopes holding a note: this fleet first, then repos in name order. */
export function scopesOf(n: Notes): Scope[] {
  const out: Scope[] = [];
  if (n.fleet.length > 0) out.push({ key: 'fleet', title: THIS_FLEET, scope: 'fleet', repo: '', notes: n.fleet });
  for (const repo of Object.keys(n.repos).sort()) {
    if (n.repos[repo].length > 0) out.push({ key: `repo:${repo}`, title: repo, scope: 'repo', repo, notes: n.repos[repo] });
  }
  return out;
}

export const noteCount = (n: Notes) => n.fleet.length + Object.values(n.repos).reduce((sum, l) => sum + l.length, 0);

export const REPO_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]*\/[A-Za-z0-9._-]+$/;

/** The scopes a note can move to: every other scope of the member, and a typed repo. */
export function moveChoices(n: Notes, from: Scope): { value: string; label: string }[] {
  const repos = Object.keys(n.repos).filter((r) => n.repos[r].length > 0);
  const out: { value: string; label: string }[] = [];
  if (from.scope !== 'fleet') out.push({ value: 'fleet', label: THIS_FLEET });
  for (const r of repos.sort()) if (!(from.scope === 'repo' && from.repo === r)) out.push({ value: `repo:${r}`, label: r });
  out.push({ value: 'typed', label: 'Another repo…' });
  return out;
}

export interface NoteOp {
  op: 'edit' | 'move' | 'delete';
  member: string;
  scope: 'fleet' | 'repo';
  repo: string;
  note: string;
  text?: string;
  to?: { scope: 'fleet' | 'repo'; repo: string };
}

/** Where a move choice sends a note, or null for a typed repo that is not owner/repo. */
export function moveTarget(choice: string, typed: string): { scope: 'fleet' | 'repo'; repo: string } | null {
  if (choice === 'fleet') return { scope: 'fleet', repo: '' };
  if (choice.startsWith('repo:')) return { scope: 'repo', repo: choice.slice(5) };
  const repo = typed.trim();
  return REPO_PATTERN.test(repo) ? { scope: 'repo', repo } : null;
}

export const crewApi = (fleet: string, harness: string, tail = '') => `/fleets/${encodeURIComponent(fleet)}/crew${tail}${harness ? `?harness=${encodeURIComponent(harness)}` : ''}`;

export const firstDock = (docks: Dock[]) => docks[0]?.harness ?? '';
