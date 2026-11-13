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
export const runes = (_s: string): number => 0;

export const canAddCheck = (_count: number): boolean => false;

export const checkTooLong = (_text: string): boolean => false;

export const guidanceTooLong = (_text: string): boolean => false;

/** A save's body: only the key the person changed. */
export function ordersBody(_shown: StandingOrdersFields, _edited: StandingOrdersFields): Partial<StandingOrdersFields> {
  return { checks: [], guidance: '' };
}

/** What an `invalid standing orders: …` 400 points at; null sends it to the top of the tab. */
export function ordersErrorTarget(_message: string): OrdersErrorTarget | null {
  return { kind: 'check', index: -1 };
}

/** The server's message without its `invalid standing orders: <path>: ` prefix. */
export function ordersMessage(message: string): string {
  return message;
}

/** Where a sentence's link goes; null for a fixed sentence, which has no field. */
export function sentenceLink(_fleetKey: string, _field: string): string | null {
  return '';
}

/** The target's own sentence for each loosened field, in the order loosens names them. */
export function loosenedSentences(_loosens: string[], _sentences: Sentence[]): string[] {
  return [];
}
