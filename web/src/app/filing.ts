import type { LimitsPatch } from './limitsForm';
import { runes, type StandingOrdersFields } from './standingOrders';

export interface OrdersDraft {
  limits: LimitsPatch | null;
  checks: string[];
  guidance: string | null;
}

export interface Kept {
  checks: string[];
  guidance: string;
}

export const MAX_PROSE_RUNES = 4000;

const GROUPS = ['ceilings', 'rates', 'spend', 'skip'];

/**
 * The Limits patch without the changes the person removed; undefined when nothing is left to send.
 * With `changed` (the fields of the change lines shown), a group-level null becomes member nulls for the changed members only, and any key without a change line is dropped.
 */
export function keptLimits(limits: LimitsPatch | null, removed: string[], changed?: string[]): LimitsPatch | undefined {
  if (!limits) return undefined;
  const wanted = (path: string) => !removed.includes(path) && (!changed || changed.includes(path));
  const out: LimitsPatch = {};
  for (const [key, value] of Object.entries(limits)) {
    if (value === null && GROUPS.includes(key)) {
      if (!changed) continue;
      const members = changed.filter((f) => f.startsWith(`${key}.`) && wanted(f));
      if (members.length > 0) out[key] = Object.fromEntries(members.map((f) => [f.slice(key.length + 1), null]));
    } else if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
      const group = Object.fromEntries(Object.entries(value).filter(([member]) => wanted(`${key}.${member}`)));
      if (Object.keys(group).length > 0) out[key] = group;
    } else if (wanted(key)) out[key] = value;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

/** True when drafting is off: the fleet's judge says so, or, before its capabilities are known, the drafter's 409 did. */
export function draftingOff(judge: { harness?: string | null } | undefined, refused: boolean): boolean {
  return judge ? !judge.harness : refused;
}

/** The stored Guidance, a blank line, then the drafted text; the drafted text alone when none is stored. */
export function joinGuidance(stored: string, drafted: string): string {
  return stored === '' ? drafted : `${stored}\n\n${drafted}`;
}

/** The orders save's body: the stored values plus what the person kept, only the keys that changed. */
export function confirmOrdersBody(shown: StandingOrdersFields, kept: Kept): Partial<StandingOrdersFields> {
  const body: Partial<StandingOrdersFields> = {};
  if (kept.checks.length > 0) body.checks = [...shown.checks, ...kept.checks];
  if (kept.guidance !== '') body.guidance = joinGuidance(shown.guidance, kept.guidance);
  return body;
}

/** The loosened Limits still in the patch once the removed changes are gone. */
export function remainingLoosens(loosens: string[], removed: string[]): string[] {
  return loosens.filter((f) => !removed.includes(f));
}

/** The label of Confirm: plain, or naming how many loosened Limits the second tap accepts. */
export function confirmLabel(loosening: number): string {
  return loosening === 0 ? 'Confirm' : `Confirm, loosening ${loosening} ${loosening === 1 ? 'Limit' : 'Limits'}`;
}

/** The drafted Check an orders-save 400 names, or null when it names a stored Check or something else. */
export function draftedCheckIndex(message: string, stored: number): number | null {
  const m = /^(?:invalid standing orders:\s*)?checks\[(\d+)\]/.exec(message);
  if (!m) return null;
  const i = Number(m[1]) - stored;
  return i >= 0 ? i : null;
}

/** The refusal for prose the server would refuse, or null. */
export function proseError(prose: string): string | null {
  if (prose.trim() === '') return 'prose: write what you want';
  return runes(prose) > MAX_PROSE_RUNES ? `prose: at most ${MAX_PROSE_RUNES} characters` : null;
}

/** The message for a failed draft call: the server's sentence, then that nothing was saved. */
export function draftFailure(r: { error: string; status?: number }): string {
  if (r.status === undefined) return "Couldn't reach Admiral. Nothing was saved.";
  return r.status === 400 ? r.error : `${r.error} Nothing was saved.`;
}

/** The message for a Confirm whose save failed, saying which part was saved. */
export function confirmFailure(step: 'limits' | 'orders', limitsSaved: boolean, error: string): string {
  return step === 'orders' && limitsSaved ? `The Limits were saved, but the Checks and Guidance weren't: ${error}` : `Nothing was saved: ${error}`;
}

const LABEL = { name: 'Name', when: 'When', before_acting: 'Before acting', do: 'Do' } as const;

/** A drafted routine's problem as the editor shows it: its field and a message that opens with the field's label. */
export function routineProblem(p: { field: string; message: string }): { field: 'name' | 'when' | 'before_acting' | 'do'; message: string } {
  const field = p.field in LABEL ? (p.field as keyof typeof LABEL) : 'do';
  return { field, message: `${LABEL[field]} ${p.message}` };
}

/** A setting's field in plain words, as the draft review names it. */
export const fieldWords = (field: string): string => field.replace(/[._]/g, ' ');

/** One line per loosened field: the target's sentence, or the field in plain words when it has none. */
export function loosenedLines(loosens: string[], sentences: { field: string; sentence: string }[]): string[] {
  return loosens.map((f) => sentences.find((s) => s.field === f)?.sentence ?? fieldWords(f));
}
