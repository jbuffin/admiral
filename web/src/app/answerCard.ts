import type { CrewBoat, Item, Ship } from './api';
import type { Conversation } from './conversation';

export type AnswerCard =
  | { kind: 'none' }
  | { kind: 'terminal' }
  | { kind: 'permission'; tool: string; reason: string; call?: Item }
  | { kind: 'question'; reason: string; item: string; text: string; options: string[]; descriptions: string[] };

const SUBAGENT_TOOLS = new Set(['task', 'Task', 'Agent']);
const HAS_KEY_MAP = new Set(['claude', 'copilot', 'codex']);

const NO_CARD_TOOLS = new Set(['AskUserQuestion', 'ExitPlanMode']);

// One AskUserQuestion call with several questions becomes items whose ids share the part before '#'.
const callOf = (id: string) => id.split('#')[0];

function currentTurnStart(items: Item[]): number {
  let at = items.length;
  while (at > 0 && items[at - 1].kind !== 'user' && items[at - 1].kind !== 'turn_end') at--;
  return at;
}

function questionCard(reason: string, conv: Conversation): AnswerCard {
  // A question an earlier turn left pending is not on the pane any more.
  const pending = conv.items.slice(currentTurnStart(conv.items)).filter((i) => i.kind === 'question' && i.question?.pending);
  const newest = pending[pending.length - 1];
  const q = newest?.question;
  if (!newest || !q) return { kind: 'terminal' };
  const options = (q.options ?? []).map((o) => o.label);
  if (q.multiSelect || options.length < 1 || options.length > 9) return { kind: 'terminal' };
  if (conv.items.some((i) => i !== newest && i.kind === 'question' && callOf(i.id) === callOf(newest.id))) return { kind: 'terminal' };
  return { kind: 'question', reason, item: newest.id, text: q.text, options, descriptions: (q.options ?? []).map((o) => o.description ?? '') };
}

// Copilot's reasons carry no tool name, so the card previews the one call still open in the current turn.
function copilotCard(r: string, conv: Conversation): AnswerCard {
  if (r === 'question') return questionCard(r, conv);
  if (r !== 'notification:permission_prompt') return { kind: 'terminal' };
  const open = conv.items.slice(currentTurnStart(conv.items)).filter((i) => i.kind === 'tool_call' && !i.tool?.done);
  // A subagent's own command asks while the launching call is the one open, so that call is not what Allow runs.
  const call = open.length === 1 && !SUBAGENT_TOOLS.has(open[0].tool?.name ?? '') ? open[0] : undefined;
  return { kind: 'permission', tool: call?.tool?.name ?? '', reason: r, call };
}

/** What the Conversation tab shows for a ship or boat that may be asking: a card to answer in place, a pointer to the terminal, or nothing. */
export function answerCard({ harness, status, reason, conv }: { harness: string; status: string; reason?: string; conv: Conversation }): AnswerCard {
  if (!HAS_KEY_MAP.has(harness) || status !== 'needs_you' || reason === 'crew') return { kind: 'none' };
  const r = reason ?? '';
  if (harness === 'copilot') return copilotCard(r, conv);
  if (harness === 'codex' && r === 'question') return { kind: 'none' };
  if (r === 'question' || r === 'permission:AskUserQuestion') return questionCard(r, conv);
  if (r.startsWith('permission:')) {
    const tool = r.slice('permission:'.length);
    if (tool === '' || NO_CARD_TOOLS.has(tool)) return { kind: 'terminal' };
    // An orphan call from an earlier turn is not what the pane asks about.
    const turnStart = currentTurnStart(conv.items);
    const call = conv.items.slice(turnStart).find((i) => i.kind === 'tool_call' && i.tool?.name === tool && !i.tool.done);
    return { kind: 'permission', tool, reason: r, call };
  }
  return { kind: 'terminal' };
}

/** The card for what the view targets: the picked boat, else the ship, each by its own harness, status and reason. */
export function cardFor(ship: Ship, boat: CrewBoat | undefined, conv: Conversation): AnswerCard {
  const t = boat ?? ship;
  return answerCard({ harness: t.harness, status: t.status, reason: t.reason, conv });
}
