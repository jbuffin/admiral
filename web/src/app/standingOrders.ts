import type { FleetCapabilities, HarnessCapabilities } from './api';

export interface Sentence {
  field: string;
  sentence: string;
}

export interface StandingOrdersFields {
  checks: string[];
  guidance: string;
}

export type OrdersErrorTarget = { kind: 'check'; index: number } | { kind: 'checks' } | { kind: 'guidance' } | { kind: 'new' };

export const MAX_CHECKS = 20;
export const MAX_CHECK_RUNES = 300;
export const MAX_GUIDANCE_RUNES = 2000;

/** Length in characters as the server counts them. */
export const runes = (s: string): number => [...s].length;

export const canAddCheck = (count: number): boolean => count < MAX_CHECKS;

export const checkTooLong = (text: string): boolean => runes(text) > MAX_CHECK_RUNES;

export const guidanceTooLong = (text: string): boolean => runes(text) > MAX_GUIDANCE_RUNES;

/** A save's body: only the key the person changed. */
export function ordersBody(shown: StandingOrdersFields, edited: StandingOrdersFields): Partial<StandingOrdersFields> {
  const body: Partial<StandingOrdersFields> = {};
  if (shown.checks.length !== edited.checks.length || shown.checks.some((c, i) => c !== edited.checks[i])) body.checks = edited.checks;
  if (shown.guidance !== edited.guidance) body.guidance = edited.guidance;
  return body;
}

const PREFIX = /^invalid standing orders:\s*/;

/** A save's body when one action runs: only its own key, whatever the other draft holds. */
export function ordersPatch(shown: StandingOrdersFields, edited: StandingOrdersFields, only: 'checks' | 'guidance'): Partial<StandingOrdersFields> {
  return ordersBody(shown, only === 'checks' ? { checks: edited.checks, guidance: shown.guidance } : { checks: shown.checks, guidance: edited.guidance });
}

/** What an `invalid standing orders: …` 400 points at; null sends it to the top of the tab. A Check index past the `rows` shown is the one Add just appended. */
export function ordersErrorTarget(message: string, rows?: number): OrdersErrorTarget | null {
  const m = /^(?:checks\[(\d+)\]|(checks)\b|(guidance)\b)/.exec(message.replace(PREFIX, ''));
  if (!m) return null;
  if (m[1] !== undefined) return rows !== undefined && Number(m[1]) >= rows ? { kind: 'new' } : { kind: 'check', index: Number(m[1]) };
  return m[2] ? { kind: 'checks' } : { kind: 'guidance' };
}

/** The server's message without its `invalid standing orders: <path>: ` prefix. */
export function ordersMessage(message: string): string {
  return message.replace(PREFIX, '').replace(/^[^:]*:\s*/, '');
}

/** The target's own sentence for each loosened field, in the order loosens names them. */
export function loosenedSentences(loosens: string[], sentences: Sentence[]): string[] {
  return loosens.flatMap((f) => sentences.find((s) => s.field === f)?.sentence ?? []);
}

/** The features that stay off until a harness is proven safe for judge calls. */
export const NEEDS_JUDGE = ['The Lookout that reads outside events', 'The reviewer that judges Checks before a launch', 'The drafter that files a standing order and describes a routine', "Autopilot's advisor"];

/** True when the fleet reports no judge harness; false while it is unknown. */
export const judgeOff = (fleet: FleetCapabilities | undefined): boolean => !!fleet?.judge && !fleet.judge.harness;

/** The judge capability a harness has not passed, for the line that explains why it can't judge. */
export function judgeGap(h: HarnessCapabilities | undefined): { capability: string; status: 'failed' | 'not_checked' } | null {
  const gap = h?.features.find((f) => f.id === 'judge')?.capabilities.find((c) => c.status !== 'passed');
  return gap && gap.status !== 'passed' ? { capability: gap.label, status: gap.status } : null;
}
