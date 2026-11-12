import type { InboxItem } from './inbox';
import type { AnswerBody } from '../questions';

export interface ChatAnswer {
  answers?: AnswerBody['answers'];
  note?: string;
}

export type ChatQuestion = InboxItem & { answer: ChatAnswer | null };

export interface ChatEntry {
  id: string;
  kind: 'message' | 'question' | 'reset';
  role?: 'user' | 'assistant';
  text: string;
  at: string;
  card?: InboxItem;
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
  | { kind: 'reset'; id: string; at: string }
  | { kind: 'question'; id: string; at: string; card: InboxItem; state: 'open' | 'answered' | 'withdrawn'; answer?: ChatAnswer };

/** The snapshot's entries oldest first; entries with one time, or none that parses, keep the served order. */
export function threadOf(s: ChatSnapshot): ThreadItem[] {
  const answers = new Map(s.questions.map((q) => [q.id, q.answer]));
  const rows = s.messages.flatMap((m, i): { t: number; i: number; item: ThreadItem }[] => {
    const t = Date.parse(m.at);
    if (m.kind === 'reset') return [{ t, i, item: { kind: 'reset', id: m.id, at: m.at } }];
    if (m.kind === 'question' && m.card) {
      const answer = answers.get(m.card.id);
      const state = answer === undefined ? 'withdrawn' : answer === null ? 'open' : 'answered';
      return [{ t, i, item: { kind: 'question', id: m.id, at: m.at, card: m.card, state, ...(answer ? { answer } : {}) } }];
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

/** The New chat marker's line. */
export function resetLabel(at: string | undefined): string {
  const time = clockTime(at);
  return time ? `New chat · ${time}` : 'New chat';
}
