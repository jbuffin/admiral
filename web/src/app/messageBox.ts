import type { Item, Ship } from './api';
import { NO_RESUME_NOTE } from './moored';

export interface BoxState {
  enabled: boolean;
  note?: string;
  placeholder?: string;
}

/** Whether the message box takes a message, and what it says when it does not. */
export function boxState(ship: Pick<Ship, 'status' | 'exit_code' | 'exit_signal' | 'no_resume'>): BoxState {
  if (ship.status === 'underway' || ship.status === 'needs_you') return { enabled: true };
  if (ship.status === 'anchored') {
    // An anchored ship always exited with an error: a non-zero code, or no code and a signal.
    if (ship.exit_code === null && ship.exit_signal) return { enabled: false, note: `Killed (${ship.exit_signal}), read only` };
    return { enabled: false, note: 'Exited with error, read only' };
  }
  // Typing at a moored ship's box wakes it, with the text after the resume text; waking needs no GitHub, so a stale poll does not matter.
  if (ship.status === 'moored') return ship.no_resume ? { enabled: false, note: NO_RESUME_NOTE } : { enabled: true, placeholder: 'Wake the ship with this message' };
  return { enabled: false };
}

/** A refused wake from the box: the typed text comes back to an empty box (whatever was typed meanwhile stays) and the route's message shows. */
export function wakeRefused(box: { text: string }, typed: string | undefined, error: string) {
  return { waking: false, error, ...(typed !== undefined && box.text === '' ? { text: typed } : {}) };
}

/** What the conversation held when a message was sent: its item ids, and the last one. */
export interface Snapshot {
  ids: string[];
  last?: string;
}

export const snapshotOf = (items: Item[]): Snapshot => ({ ids: items.map((i) => i.id), last: items[items.length - 1]?.id });

export interface Send {
  id: number;
  text: string;
  /** Null until the first page of the conversation has arrived, which the send waits for. */
  snapshot: Snapshot | null;
  answered: boolean;
}

/**
 * The sends whose dimmed bubble still shows. The server cleans the text and the harness may record it differently,
 * so a send is resolved by the first user item that is new since it was sent, whatever it says: not in its snapshot
 * and after the snapshot's last item (an older page loaded later comes before it). Sends claim items in send order.
 * A ship with no conversation has nothing to wait for but the server's answer.
 */
export function bubblesShowing(sends: Send[], items: Item[], hasConversation: boolean): Send[] {
  if (!hasConversation) return sends.filter((s) => !s.answered);
  const claimed = new Set<string>();
  return sends.filter((s) => {
    if (!s.snapshot) return true;
    const known = new Set(s.snapshot.ids);
    const from = s.snapshot.last === undefined ? -1 : items.findIndex((i) => i.id === s.snapshot!.last);
    const hit = items.find((it, n) => n > from && it.kind === 'user' && !known.has(it.id) && !claimed.has(it.id));
    if (hit) claimed.add(hit.id);
    return !hit;
  });
}
