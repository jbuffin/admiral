import type { Item } from './api';
import type { Conversation } from './conversation';

export type AnswerCard =
  | { kind: 'none' }
  | { kind: 'terminal' }
  | { kind: 'permission'; tool: string; reason: string; call?: Item }
  | { kind: 'question'; reason: string; item: string; options: string[] };

const NO_CARD_TOOLS = new Set(['AskUserQuestion', 'ExitPlanMode']);

// One AskUserQuestion call with several questions becomes items whose ids share the part before '#'.
const callOf = (id: string) => id.split('#')[0];

function questionCard(reason: string, conv: Conversation): AnswerCard {
  const pending = conv.items.filter((i) => i.kind === 'question' && i.question?.pending);
  const newest = pending[pending.length - 1];
  const q = newest?.question;
  if (!newest || !q) return { kind: 'terminal' };
  const options = (q.options ?? []).map((o) => o.label);
  if (q.multiSelect || options.length < 1 || options.length > 9) return { kind: 'terminal' };
  if (conv.items.some((i) => i !== newest && i.kind === 'question' && callOf(i.id) === callOf(newest.id))) return { kind: 'terminal' };
  return { kind: 'question', reason, item: newest.id, options };
}

/** What the Conversation tab shows for a ship or boat that may be asking: a card to answer in place, a pointer to the terminal, or nothing. */
export function answerCard({ harness, status, reason, conv }: { harness: string; status: string; reason?: string; conv: Conversation }): AnswerCard {
  if (harness !== 'claude' || status !== 'needs_you' || reason === 'crew') return { kind: 'none' };
  const r = reason ?? '';
  if (r === 'question' || r === 'permission:AskUserQuestion') return questionCard(r, conv);
  if (r.startsWith('permission:')) {
    const tool = r.slice('permission:'.length);
    if (tool === '' || NO_CARD_TOOLS.has(tool)) return { kind: 'terminal' };
    const call = conv.items.find((i) => i.kind === 'tool_call' && i.tool?.name === tool && !i.tool.done);
    return { kind: 'permission', tool, reason: r, call };
  }
  return { kind: 'terminal' };
}
