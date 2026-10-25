import type { InboxQuestion } from './inbox';

// What the engineer has entered on a question card, and the answer it sends. The inbox and the chat each
// keep their own drafts; these helpers are pure, so both build the same body.

export interface Pick {
  sel: string[];
  other: boolean;
  text: string;
}
export interface Draft {
  picks: Record<string, Pick>;
  note: string;
  error: string;
  sending: boolean;
}
export const blankDraft = (): Draft => ({ picks: {}, note: '', error: '', sending: false });

const emptyPick: Pick = { sel: [], other: false, text: '' };
export const pickOf = (d: Draft, q: InboxQuestion): Pick => d.picks[q.id] ?? emptyPick;
export const answeredQuestion = (p: Pick) => p.sel.length > 0 || (p.other && p.text.trim() !== '');

export function choose(d: Draft, q: InboxQuestion, label: string): Draft {
  const p = pickOf(d, q);
  const sel = q.multi ? (p.sel.includes(label) ? p.sel.filter((l) => l !== label) : [...p.sel, label]) : [label];
  return { ...d, picks: { ...d.picks, [q.id]: { ...p, sel, other: q.multi ? p.other : false } } };
}
export function chooseOther(d: Draft, q: InboxQuestion): Draft {
  const p = pickOf(d, q);
  return { ...d, picks: { ...d.picks, [q.id]: q.multi ? { ...p, other: !p.other } : { ...p, sel: [], other: true } } };
}
export function setOtherText(d: Draft, q: InboxQuestion, text: string): Draft {
  return { ...d, picks: { ...d.picks, [q.id]: { ...pickOf(d, q), text } } };
}

export interface AnswerBody {
  answers: Record<string, { choice?: string | string[]; other?: string }>;
  note?: string;
}

/** The body of an answer POST, the same for the inbox and the chat. */
export function answerBody(questions: InboxQuestion[], d: Draft): AnswerBody {
  const answers: AnswerBody['answers'] = {};
  for (const q of questions) {
    const p = pickOf(d, q);
    const a: AnswerBody['answers'][string] = {};
    if (p.sel.length > 0) a.choice = q.multi ? p.sel : p.sel[0];
    if (p.other && p.text.trim()) a.other = p.text.trim();
    answers[q.id] = a;
  }
  const note = d.note.trim();
  return { answers, ...(note && { note }) };
}

/** "Answered: Freeform" for a served answer: the chosen labels and the Other text, then a note flag. */
export function foldText(answer: { answers?: AnswerBody['answers']; note?: string }): string {
  const labels = Object.values(answer.answers ?? {}).flatMap((a) => [...(Array.isArray(a.choice) ? a.choice : a.choice ? [a.choice] : []), ...(a.other ? [a.other] : [])]);
  return `Answered: ${labels.join(', ')}${answer.note ? ' (with a note)' : ''}`;
}
