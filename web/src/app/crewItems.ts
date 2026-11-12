// The crew Inbox items are written by ships and are untrusted: every reader degrades a wrong-typed field to an
// empty value, and every string here is rendered as text.

export interface CrewNote {
  id: string;
  member: string;
  role: string;
  scope: 'fleet' | 'repo';
  repo?: string;
  note: string;
  replaces: string | null;
}

export interface CrewNotesItem {
  fleet: string;
  ship: string;
  run_date: string;
  notes: CrewNote[];
}

export interface CrewMemberItem extends CrewNotesItem {
  member: string;
  role: string;
  ad_hoc_run: string;
  definition: string;
}

const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const obj = (v: unknown): Record<string, unknown> => (typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : {});

function readNotes(raw: unknown): CrewNote[] {
  if (!Array.isArray(raw)) return [];
  const seen = new Set<string>();
  const out: CrewNote[] = [];
  for (const r of raw) {
    const o = obj(r);
    const id = o.id;
    if (typeof id !== 'string' || id === '' || seen.has(id)) continue;
    seen.add(id);
    const scope = o.scope === 'repo' ? 'repo' : 'fleet';
    out.push({
      id,
      member: str(o.member),
      role: str(o.role),
      scope,
      ...(scope === 'repo' && { repo: str(o.repo) }),
      note: str(o.note),
      replaces: typeof o.replaces === 'string' ? o.replaces : null,
    });
  }
  return out;
}

export function readCrewNotes(raw: unknown): CrewNotesItem {
  const o = obj(raw);
  return { fleet: str(o.fleet), ship: str(o.ship), run_date: str(o.run_date), notes: readNotes(o.notes) };
}

export function readCrewMember(raw: unknown): CrewMemberItem {
  const o = obj(raw);
  return { ...readCrewNotes(raw), member: str(o.member), role: str(o.role), ad_hoc_run: str(o.ad_hoc_run), definition: str(o.definition) };
}

export type NoteDecision = 'keep' | 'drop' | 'edit';
export interface NoteEdit {
  note: string;
  scope: 'fleet' | 'repo';
  repo?: string;
}

type Sent = { decision: 'keep' | 'drop' } | ({ decision: 'edit' } & NoteEdit);

export function createNoteDecisions(notes: CrewNote[]) {
  const ids = new Set(notes.map((n) => n.id));
  const made = new Map<string, Sent>();
  const put = (id: string, v: Sent) => void (ids.has(id) && made.set(id, v));
  return {
    keep: (id: string) => put(id, { decision: 'keep' }),
    drop: (id: string) => put(id, { decision: 'drop' }),
    save(id: string, e: NoteEdit): boolean {
      if (!ids.has(id)) return false;
      put(id, { decision: 'edit', note: e.note, scope: e.scope, ...(e.scope === 'repo' && { repo: e.repo ?? '' }) });
      return true;
    },
    decision: (id: string): NoteDecision | undefined => made.get(id)?.decision,
    edit: (id: string): NoteEdit | undefined => {
      const v = made.get(id);
      return v?.decision === 'edit' ? { note: v.note, scope: v.scope, repo: v.repo } : undefined;
    },
    progress: () => ({ decided: made.size, total: notes.length }),
    canSend: () => notes.length > 0 && made.size === notes.length,
    payload: () => ({ notes: Object.fromEntries(notes.filter((n) => made.has(n.id)).map((n) => [n.id, made.get(n.id)!])) }),
  };
}
export type NoteDecisions = ReturnType<typeof createNoteDecisions>;

export const NOTE_MAX = 300;
const REPO = /^[A-Za-z0-9._-]+\/[A-Za-z0-9._-]+$/;

/** What the server's note and repo checks would refuse, so Save is off until none is left. */
export function noteEditProblems(e: NoteEdit): { note?: string; repo?: string } {
  const out: { note?: string; repo?: string } = {};
  if (e.note.trim() === '') out.note = 'Write the note.';
  else if (/[\r\n]/.test(e.note)) out.note = 'A note is one line.';
  else if ([...e.note].length > NOTE_MAX) out.note = `A note is at most ${NOTE_MAX} characters.`;
  if (e.scope === 'repo' && (!REPO.test(e.repo ?? '') || (e.repo ?? '').split('/').some((p) => p === '.' || p === '..'))) out.repo = 'Write the repo as owner/repo.';
  return out;
}

export const crewMemberFold = (decision: 'keep' | 'drop', member: string, data: { file_removed?: boolean }): string =>
  decision === 'keep' ? `Kept ${member} in the fleet's crew` : droppedFold(member, data);

export const crewRemoveFold = (data: { file_removed?: boolean }, member: string): string => droppedFold(member, data);

const droppedFold = (member: string, data: { file_removed?: boolean }) =>
  data.file_removed === true ? `Dropped ${member}; the ad hoc file was deleted` : `Dropped ${member}; the ad hoc file was left in place`;
