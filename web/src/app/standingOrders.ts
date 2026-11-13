export interface Sentence {
  field: string;
  sentence: string;
}

export interface StandingOrdersFields {
  checks: string[];
  guidance: string;
}

export type OrdersErrorTarget = { kind: 'check'; index: number } | { kind: 'checks' } | { kind: 'guidance' };

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

/** What an `invalid standing orders: …` 400 points at; null sends it to the top of the tab. */
export function ordersErrorTarget(message: string): OrdersErrorTarget | null {
  const m = /^(?:checks\[(\d+)\]|(checks)\b|(guidance)\b)/.exec(message.replace(PREFIX, ''));
  if (!m) return null;
  if (m[1] !== undefined) return { kind: 'check', index: Number(m[1]) };
  return m[2] ? { kind: 'checks' } : { kind: 'guidance' };
}

/** The server's message without its `invalid standing orders: <path>: ` prefix. */
export function ordersMessage(message: string): string {
  return message.replace(PREFIX, '').replace(/^[^:]*:\s*/, '');
}

/** Where a sentence's link goes; null for a fixed sentence, which has no field. */
export function sentenceLink(fleetKey: string, field: string): string | null {
  return field ? `/app/preferences/${encodeURIComponent(fleetKey)}/limits?field=${encodeURIComponent(field)}` : null;
}

/** The target's own sentence for each loosened field, in the order loosens names them. */
export function loosenedSentences(loosens: string[], sentences: Sentence[]): string[] {
  return loosens.flatMap((f) => sentences.find((s) => s.field === f)?.sentence ?? []);
}
