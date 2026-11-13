import type { InboxItem } from './inbox';
import type { AnswerBody } from '../questions';
import { shipPath } from './router';

export interface ChatAnswer {
  answers?: AnswerBody['answers'];
  note?: string;
}

export type ChatQuestion = InboxItem & { answer: ChatAnswer | null };

/** The card Admiral posts when a commitment fires, as the chat log carries it. */
export interface CommitmentCard {
  commitment: string;
  outcome: 'launched' | 'reminded' | 'cant_run' | 'dropped' | 'aged';
  ship?: string;
  reason?: string;
}

export interface ChatEntry {
  id: string;
  kind: 'message' | 'question' | 'reset' | 'commitment';
  role?: 'user' | 'assistant';
  text: string;
  at: string;
  card?: InboxItem | CommitmentCard;
}

/** A fleet's chat as the server serves it: GET /api/chat/<fleet>/session and the `chat` socket message. */
export interface ChatSnapshot {
  fleet: string;
  running: boolean;
  state: 'idle' | 'working' | 'waiting' | '';
  messages: ChatEntry[];
  questions: ChatQuestion[];
  error?: string;
}

export type ThreadItem =
  | { kind: 'message'; id: string; role: 'user' | 'assistant'; text: string; at: string }
  | { kind: 'reset'; id: string; text: string; at: string }
  | { kind: 'commitment'; id: string; text: string; at: string; card: CommitmentCard }
  | { kind: 'question'; id: string; at: string; card: InboxItem; state: 'open' | 'answered' | 'withdrawn'; answer?: ChatAnswer };

/** The snapshot's entries oldest first; entries with one time, or none that parses, keep the served order. */
/** Claude's first-start dialog holds the chat; the server's text for that is the only error the terminal fixes. */
export const needsTerminal = (s: ChatSnapshot) => s.error === 'Open the terminal to finish starting the chat';

export function threadOf(s: ChatSnapshot): ThreadItem[] {
  const answers = new Map(s.questions.map((q) => [q.id, q.answer]));
  const rows = s.messages.flatMap((m, i): { t: number; i: number; item: ThreadItem }[] => {
    const t = Date.parse(m.at);
    if (m.kind === 'reset') return [{ t, i, item: { kind: 'reset', id: m.id, text: m.text, at: m.at } }];
    if (m.kind === 'commitment' && m.card) return [{ t, i, item: { kind: 'commitment', id: m.id, text: m.text, at: m.at, card: m.card as CommitmentCard } }];
    if (m.kind === 'question' && m.card) {
      const card = m.card as InboxItem;
      const answer = answers.get(card.id);
      const state = answer === undefined ? 'withdrawn' : answer === null ? 'open' : 'answered';
      return [{ t, i, item: { kind: 'question', id: m.id, at: m.at, card, state, ...(answer ? { answer } : {}) } }];
    }
    if (m.kind === 'message' && m.role) return [{ t, i, item: { kind: 'message', id: m.id, role: m.role, text: m.text, at: m.at } }];
    return [];
  });
  rows.sort((a, b) => (Number.isNaN(a.t) || Number.isNaN(b.t) ? 0 : a.t - b.t) || a.i - b.i);
  return rows.map((r) => r.item);
}

/** What the engineer chose, as a line: every choice and typed answer, in question order. */
export function youAnswered(answer: ChatAnswer): string {
  const labels = Object.values(answer.answers ?? {}).flatMap((a) => [...(Array.isArray(a.choice) ? a.choice : a.choice ? [a.choice] : []), ...(a.other ? [a.other] : [])]);
  return `You answered: ${labels.join(', ')}`;
}

export function clockTime(at: string | undefined): string {
  const t = at ? Date.parse(at) : NaN;
  return Number.isNaN(t) ? '' : new Date(t).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
}

/** A reset marker's line: its text, then the time. */
export function resetLabel(text: string, at: string | undefined): string {
  const time = clockTime(at);
  return time ? `${text} · ${time}` : text;
}

/** What a commitment card shows: a one-line outcome, the commitment's text, and the ship page it links to. */
export function commitmentCardView(item: Extract<ThreadItem, { kind: 'commitment' }>): { eyebrow: string; text: string; reason: string; shipHref: string } {
  const { outcome, ship, reason } = item.card;
  const eyebrow = { launched: 'AdmiralBot launched a ship', reminded: 'AdmiralBot reminds you', cant_run: "AdmiralBot can't run this", dropped: 'AdmiralBot dropped this', aged: 'Still want this?' }[outcome];
  return { eyebrow, text: item.text, reason: reason ?? '', shipHref: outcome === 'launched' && ship ? shipPath(ship) : '' };
}
