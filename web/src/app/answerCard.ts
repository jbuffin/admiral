import type { CrewBoat, Item, Ship } from './api';
import type { Conversation } from './conversation';

export type AnswerCard =
  | { kind: 'none' }
  | { kind: 'terminal' }
  | { kind: 'permission'; tool: string; reason: string; call?: Item }
  | { kind: 'question'; reason: string; item: string; text: string; options: string[]; descriptions: string[]; questions: CardQuestion[] };

/** One question of the call a card answers: the listed options, and whether Other's free text is offered. */
export interface CardQuestion {
  id: string;
  header: string;
  text: string;
  multiSelect: boolean;
  options: string[];
  descriptions: string[];
  other: boolean;
}

const SUBAGENT_TOOLS = new Set(['task', 'Task', 'Agent']);
const HAS_KEY_MAP = new Set(['claude', 'copilot', 'codex']);

// Codex's other approval menus are unrecorded, so they go to the terminal.
// The rollout's decoder names Codex's calls after the tool inside the exec script, so a Bash card looks for exec_command; the live observer names it Bash.
const CODEX_CARD_CALLS: Record<string, string[]> = { Bash: ['exec_command', 'Bash'], apply_patch: ['apply_patch'] };

const NO_CARD_TOOLS = new Set(['AskUserQuestion', 'ExitPlanMode']);

// One AskUserQuestion call with several questions becomes items whose ids share the part before '#'.
const callOf = (id: string) => id.split('#')[0];

function currentTurnStart(items: Item[]): number {
  let at = items.length;
  while (at > 0 && items[at - 1].kind !== 'user' && items[at - 1].kind !== 'turn_end') at--;
  return at;
}

// Claude's menu takes any shape; Copilot's and Codex's recorded keys take one single-choice question, so the rest goes to the terminal.
function questionCard(reason: string, conv: Conversation, harness: string): AnswerCard {
  // A question an earlier turn left pending is not on the pane any more.
  const turn = conv.items.slice(currentTurnStart(conv.items));
  const newest = turn.filter((i) => i.kind === 'question' && i.question?.pending).pop();
  if (!newest) return { kind: 'terminal' };
  const call = turn.filter((i) => i.kind === 'question' && callOf(i.id) === callOf(newest.id));
  if (call.some((i) => !i.question?.pending)) return { kind: 'terminal' };
  const claude = harness === 'claude';
  if (!claude && (call.length > 1 || call[0].question?.multiSelect)) return { kind: 'terminal' };
  const questions: CardQuestion[] = [];
  for (const i of call) {
    const q = i.question!;
    const options = (q.options ?? []).map((o) => o.label);
    const wide = claude && (q.multiSelect || call.length > 1) ? 8 : 9;
    if (options.length < 1 || options.length > wide) return { kind: 'terminal' };
    questions.push({ id: i.id, header: q.header ?? '', text: q.text, multiSelect: q.multiSelect === true, options, descriptions: (q.options ?? []).map((o) => o.description ?? ''), other: claude && options.length <= 8 });
  }
  const first = questions[0];
  return { kind: 'question', reason, item: first.id, text: first.text, options: first.options, descriptions: first.descriptions, questions };
}

// Copilot's reasons carry no tool name, so the card previews the one call still open in the current turn.
function copilotCard(r: string, conv: Conversation): AnswerCard {
  if (r === 'question') return questionCard(r, conv, 'copilot');
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
  if (harness === 'codex' && r === 'bell') return questionCard(r, conv, harness);
  if (r === 'question' || r === 'permission:AskUserQuestion') return questionCard(r, conv, harness);
  if (r.startsWith('permission:')) {
    const tool = r.slice('permission:'.length);
    if (tool === '' || NO_CARD_TOOLS.has(tool) || (harness === 'codex' && !(tool in CODEX_CARD_CALLS))) return { kind: 'terminal' };
    // An orphan call from an earlier turn is not what the pane asks about.
    const turnStart = currentTurnStart(conv.items);
    const callNames = harness === 'codex' ? CODEX_CARD_CALLS[tool] : [tool];
    const turn = conv.items.slice(turnStart);
    if (harness === 'codex' && conv.items.some((i) => i.tool?.live)) {
      // The server names the call it waits on; none named (several waiting, a stdin write, a grant) is the terminal's.
      const named = turn.find((i) => i.kind === 'tool_call' && i.tool?.approving && !i.tool.done);
      if (!named?.tool || !callNames.includes(named.tool.name)) return { kind: 'terminal' };
      return { kind: 'permission', tool, reason: r, call: named };
    }
    const call = turn.find((i) => i.kind === 'tool_call' && i.tool !== undefined && callNames.includes(i.tool.name) && !i.tool.done);
    return { kind: 'permission', tool, reason: r, call };
  }
  return { kind: 'terminal' };
}

/** The card for what the view targets: the picked boat, else the ship, each by its own harness, status and reason. */
export function cardFor(ship: Ship, boat: CrewBoat | undefined, conv: Conversation): AnswerCard {
  const t = boat ?? ship;
  return answerCard({ harness: t.harness, status: t.status, reason: t.reason, conv });
}
