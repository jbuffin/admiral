import type { Item, Ship } from './api';

export interface BoxState {
  enabled: boolean;
  note?: string;
}

/** Whether the message box takes a message, and what it says when it does not. */
export function boxState(ship: Pick<Ship, 'status' | 'exit_code' | 'exit_signal'>): BoxState {
  if (ship.status === 'underway' || ship.status === 'needs_you') return { enabled: true };
  if (ship.status === 'anchored') {
    // An anchored ship always exited with an error: a non-zero code, or no code and a signal.
    if (ship.exit_code === null && ship.exit_signal) return { enabled: false, note: `Killed (${ship.exit_signal}), read only` };
    return { enabled: false, note: 'Exited with error, read only' };
  }
  return { enabled: false };
}

export const userItemCount = (items: Item[]): number => items.filter((i) => i.kind === 'user').length;

/**
 * The server cleans the text and the harness may record it differently, so the first user item after the send
 * stands for it, whatever it says. A ship with no conversation has nothing to wait for but the server's answer.
 */
export function sentBubbleShows(sent: { text: string; userItemsAtSend: number }, items: Item[], ctx: { hasConversation: boolean; answered: boolean }): boolean {
  return ctx.hasConversation ? userItemCount(items) <= sent.userItemsAtSend : !ctx.answered;
}
