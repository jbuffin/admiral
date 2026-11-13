import type { LimitsPatch } from './limitsForm';
import type { StandingOrdersFields } from './standingOrders';

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

/** The Limits patch without the changes the person removed; undefined when nothing is left to send. */
export function keptLimits(_limits: LimitsPatch | null, _removed: string[]): LimitsPatch | undefined {
  throw new Error('not implemented');
}

/** The stored Guidance, a blank line, then the drafted text; the drafted text alone when none is stored. */
export function joinGuidance(_stored: string, _drafted: string): string {
  throw new Error('not implemented');
}

/** The orders save's body: the stored values plus what the person kept, only the keys that changed. */
export function confirmOrdersBody(_shown: StandingOrdersFields, _kept: Kept): Partial<StandingOrdersFields> {
  throw new Error('not implemented');
}

/** The loosened Limits still in the patch once the removed changes are gone. */
export function remainingLoosens(_loosens: string[], _removed: string[]): string[] {
  throw new Error('not implemented');
}

/** The label of Confirm: plain, or naming how many loosened Limits the second tap accepts. */
export function confirmLabel(_loosening: number): string {
  throw new Error('not implemented');
}

/** The drafted Check an orders-save 400 names, or null when it names a stored Check or something else. */
export function draftedCheckIndex(_message: string, _stored: number): number | null {
  throw new Error('not implemented');
}

/** The refusal for prose the server would refuse, or null. */
export function proseError(_prose: string): string | null {
  throw new Error('not implemented');
}

/** The message for a failed draft call: the server's sentence, then that nothing was saved. */
export function draftFailure(_r: { error: string; status?: number }): string {
  throw new Error('not implemented');
}

/** The message for a Confirm whose save failed, saying which part was saved. */
export function confirmFailure(_step: 'limits' | 'orders', _limitsSaved: boolean, _error: string): string {
  throw new Error('not implemented');
}

/** A drafted routine's problem as the editor shows it: its field and a message that opens with the field's label. */
export function routineProblem(_p: { field: string; message: string }): { field: 'name' | 'when' | 'before_acting' | 'do'; message: string } {
  throw new Error('not implemented');
}
