import { call, type Dock, type Result } from './api';

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

/** The form inputs on screen for a role: Based on only for a custom role, no skills or instructions for the fix checker. */
export function shownFields(role: string): string[] {
  return FORM_FIELDS.filter((f) => (f !== 'based-on' || role === CUSTOM_ROLE) && (role !== 'fix-checker' || (f !== 'skills' && f !== 'instructions')));
}

/** Which input a 400 belongs beside; an error naming no input on screen goes to the top. */
export function fieldError(e: { error: string; field?: string }, shown: string[] = FORM_FIELDS): { field: string | null; message: string } {
  return { field: e.field && shown.includes(e.field) ? e.field : null, message: e.error };
}

/** A note is "<date> (<run>): <fact>" and the notes route edits only the fact, keeping the stamp; a line with no stamp is all fact. */
export function splitNote(note: string): { stamp: string; fact: string } {
  const at = note.indexOf('): ');
  return at < 0 ? { stamp: '', fact: note } : { stamp: note.slice(0, at + 2), fact: note.slice(at + 3) };
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

export const DESCRIPTION_MAX = 4000;

/** A description with something in it, of at most 4000 code points, which is how the server counts. */
export const canDraft = (description: string) => description.trim() !== '' && [...description].length <= DESCRIPTION_MAX;

export interface NewMemberForm extends MemberForm {
  name: string;
}

export interface DraftProblem {
  field: string;
  message: string;
}

export interface Draft {
  form: NewMemberForm;
  problems: DraftProblem[];
}

export const draftCrew = (fleet: string, description: string, signal?: AbortSignal) => call<unknown>('POST', crewApi(fleet, '', '/draft'), { description }, undefined, signal);

/** The editor's form from the draft route's answer; the draft is model output, so every field is read defensively. */
export function readDraft(raw: unknown): Draft {
  const top = (raw && typeof raw === 'object' ? raw : {}) as { draft?: Record<string, unknown> | null; problems?: unknown };
  const d = top.draft ?? {};
  const text = (v: unknown) => (typeof v === 'string' ? v : '');
  const role = text(d.role);
  const custom = role !== '' && !ROLES.includes(role);
  const skills = Array.isArray(d.skills) ? d.skills.filter((s): s is string => typeof s === 'string') : [];
  const problems = (Array.isArray(top.problems) ? top.problems : []).flatMap((p): DraftProblem[] => {
    const q = p as { field?: unknown; message?: unknown } | null;
    return q && typeof q.field === 'string' && typeof q.message === 'string' ? [{ field: q.field, message: q.message }] : [];
  });
  return {
    form: {
      name: text(d.name),
      role: custom ? CUSTOM_ROLE : role,
      customRole: custom ? role : '',
      basedOn: custom ? text(d['based-on']) : '',
      skills: skills.join('\n'),
      model: text(d.model),
      harness: text(d.harness),
      pickMeWhen: text(d['pick-me-when']),
      instructions: text(d.instructions),
    },
    problems,
  };
}

/** The problems left after a field changes; a role change also clears based-on, which a built-in role no longer shows. */
export function clearProblems(problems: DraftProblem[], key: keyof NewMemberForm): DraftProblem[] {
  const own = key === 'customRole' ? 'role' : key === 'basedOn' ? 'based-on' : key === 'pickMeWhen' ? 'pick-me-when' : key;
  const gone = key === 'role' ? ['role', 'based-on'] : [own];
  return problems.filter((p) => !gone.includes(p.field));
}

export type NewMemberBody = MemberBody & { name: string };

export const createBody = (f: NewMemberForm): NewMemberBody => ({ name: f.name.trim(), ...memberBody(f) });

export const createMember = (fleet: string, body: NewMemberBody) => call<{ name: string }>('POST', crewApi(fleet, '', '/members'), body);

/** The editor inputs for a new member: the Name first, then what an edit shows. */
export const addShownFields = (role: string): string[] => ['name', ...shownFields(role)];

export interface AddCrewState {
  phase: 'describe' | 'drafting' | 'draft' | 'failed' | 'nojudge';
  description: string;
  seconds: number;
  form?: NewMemberForm;
  problems?: DraftProblem[];
  error?: string;
  kind?: string;
  reason?: string;
}

export const ADD_CREW_START: AddCrewState = { phase: 'describe', description: '', seconds: 0 };

export type AddCrewEvent =
  | { type: 'edit'; description: string }
  | { type: 'draft' }
  | { type: 'tick' }
  | { type: 'stop' }
  | { type: 'result'; result: Result<unknown> }
  | { type: 'discard' };

/** The Add crew panel's steps. The description survives every one of them, and a result that lands outside drafting is stale. */
export function addCrewStep(s: AddCrewState, e: AddCrewEvent): AddCrewState {
  const back = { phase: 'describe' as const, description: s.description, seconds: 0 };
  switch (e.type) {
    case 'edit':
      return { ...s, description: e.description };
    case 'draft':
      return s.phase !== 'drafting' && s.phase !== 'draft' && canDraft(s.description) ? { phase: 'drafting', description: s.description, seconds: 0 } : s;
    case 'tick':
      return s.phase === 'drafting' ? { ...s, seconds: s.seconds + 1 } : s;
    case 'stop':
      return s.phase === 'drafting' ? back : s;
    case 'discard':
      return s.phase === 'draft' ? back : s;
    case 'result': {
      if (s.phase !== 'drafting') return s;
      const r = e.result;
      if (r.ok) {
        const { form, problems } = readDraft(r.data);
        return { phase: 'draft', description: s.description, seconds: 0, form, problems };
      }
      if (r.aborted) return back;
      if (r.status === 409 && r.reason) return { phase: 'nojudge', description: s.description, seconds: 0, reason: r.reason ?? '' };
      return { phase: 'failed', description: s.description, seconds: 0, kind: r.kind ?? 'error', error: r.error };
    }
  }
}
