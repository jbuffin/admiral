import type { AllowRuleCard, Ship } from './api';
import type { InboxItem } from './inbox';
import type { ReviewerFields } from './reviewerNote';
import type { AnswerBody } from '../questions';
import { chatSettingsPath, shipPath } from './router';

export interface ChatAnswer {
  answers?: AnswerBody['answers'];
  note?: string;
}

export type ChatQuestion = InboxItem & { answer: ChatAnswer | null };

/** The card Admiral posts when a commitment fires, as the chat log carries it. */
export interface CommitmentCard {
  commitment: string;
  outcome: 'launched' | 'reminded' | 'cant_run' | 'dropped' | 'aged' | 'ended' | 'too_large';
  ship?: string;
  reason?: string;
  child?: string;
}

/** The A0 card for an outside event: the Lookout's summary or the metadata line, never the event's own text. */
export interface EventCard extends ReviewerFields {
  routine: string;
  routine_name?: string;
  tier?: string;
  sender?: string;
  link?: string;
  dedupe_key?: string;
  metadata?: boolean;
  mute_by?: string[];
}

/** The card an event launch posts for its ship: the event card's fields without the reviewer's, the ship, its level and, at A3, its restricted profile. */
export interface LaunchCard extends Omit<EventCard, keyof ReviewerFields> {
  ship: string;
  level: 'A3' | 'A4' | 'A5';
  restricted?: 'investigate' | 'draft_fix';
}

/** The card Admiral posts in a ship's fleet's chat when its result comes in. */
export interface ShipCard {
  ship: string;
  name?: string;
  title?: string;
  first_launched?: string;
  fleet: string;
  skill: string;
  skill_kind: 'commodore' | 'skill' | 'none' | 'scout' | 'quick';
  result: 'sailed_off' | 'anchored' | 'moored' | 'ready_to_merge' | 'returned' | 'lost' | 'failed' | 'done';
  prs: { url: string; number: number }[];
  /** The branch a local-only ship committed its work on, on a done card. */
  branch?: string;
  findings?: boolean;
}

/** The line AdmiralBot's launch posts in the chat: what it sent and why, with its launch text as written. */
export interface LaunchLine {
  ship: string;
  name?: string;
  title?: string;
  first_launched?: string;
  fleet: string;
  kind: string;
  reason: string;
  text: string;
}

export interface ChatEntry {
  id: string;
  kind: 'message' | 'question' | 'reset' | 'interrupted' | 'commitment' | 'event_card' | 'answer' | 'launch_card' | 'ship_card' | 'launch_line' | 'campaign_draft_card' | 'following_card' | 'need_card';
  role?: 'user' | 'assistant';
  text: string;
  at: string;
  card?: InboxItem | CommitmentCard | EventCard | LaunchCard | ShipCard | LaunchLine | CampaignDraftCard | FollowingCard | NeedCard;
  /** The id of the ship card this entry belongs to: a report turn and its reply. */
  thread?: string;
}

/** A fleet's chat as the server serves it: GET /api/chat/<fleet>/session and the `chat` socket message. */
export interface ChatSnapshot {
  fleet: string;
  running: boolean;
  state: 'idle' | 'working' | 'waiting' | '';
  messages: ChatEntry[];
  questions: ChatQuestion[];
  error?: string;
  needsTerminal?: boolean;
}

/** A message of the chat; `report` marks the user entry Admiral typed as a ship's report turn, which is not the engineer's. */
export type ThreadMessage = { kind: 'message'; id: string; role: 'user' | 'assistant'; text: string; at: string; report?: true };

export type ThreadItem =
  | ThreadMessage
  | { kind: 'reset'; id: string; text: string; at: string }
  | { kind: 'interrupted'; id: string; text: string; at: string }
  | { kind: 'commitment'; id: string; text: string; at: string; card: CommitmentCard }
  | { kind: 'event'; id: string; text: string; at: string; card: EventCard }
  | { kind: 'launch'; id: string; text: string; at: string; card: LaunchCard }
  | { kind: 'launch_line'; id: string; text: string; at: string; card: LaunchLine }
  | { kind: 'ship'; id: string; text: string; at: string; card: ShipCard; thread?: ThreadMessage[] }
  | { kind: 'campaign_draft'; id: string; text: string; at: string; card: CampaignDraftCard }
  | { kind: 'following'; id: string; text: string; at: string; card: FollowingCard }
  | { kind: 'need'; id: string; text: string; at: string; card: NeedCard }
  | { kind: 'question'; id: string; at: string; card: InboxItem; state: 'open' | 'answered' | 'withdrawn'; answer?: ChatAnswer; kept?: string };

interface NeedCardBase {
  item: string;
  state: 'open' | 'answered' | 'removed' | 'moved_on';
  answered_in?: 'chat' | 'inbox';
}
interface ShipNeedBase extends NeedCardBase {
  ship: string;
}

/** The card Admiral posts in a fleet's chat for each thing in its Inbox that needs the engineer. */
export type NeedCard =
  | (ShipNeedBase & { item_kind: 'question'; questions: NonNullable<InboxItem['question']>['questions'] })
  | (ShipNeedBase & { item_kind: 'review'; events: string[]; discard: boolean; pr_url?: string })
  | (ShipNeedBase & { item_kind: 'findings'; cards: { id: string; proposal: { kind: string; content: string } | null }[] })
  | (NeedCardBase & { item_kind: 'routine_proposal'; action: string; reason: string; expires_at?: string; routine_name?: string })
  | (NeedCardBase & { item_kind: 'allow_rules'; cards: AllowRuleCard[] })
  | (NeedCardBase & { item_kind: 'campaign_draft'; repo: string; title: string; body: string });

export interface FollowingCard {
  ship: string;
  campaign_id: string;
  title: string;
}

export interface CampaignDraftCard {
  title: string;
  url?: string;
  created?: boolean;
  failed?: string;
}

/** The snapshot's entries oldest first; entries with one time, or none that parses, keep the served order. */
/** The server says the chat waits on a startup prompt only the terminal answers. */
export const needsTerminal = (s: ChatSnapshot) => s.needsTerminal === true;

export function threadOf(s: ChatSnapshot): ThreadItem[] {
  const answers = new Map(s.questions.map((q) => [q.id, q.answer]));
  // A question the server removed after its answer keeps that answer in the log.
  const kept = new Map(s.messages.filter((m) => m.kind === 'answer').map((m) => [m.id, m.text]));
  const rows = s.messages.flatMap((m, i): { t: number; i: number; item: ThreadItem }[] => {
    const t = Date.parse(m.at);
    if (m.kind === 'reset') return [{ t, i, item: { kind: 'reset', id: m.id, text: m.text, at: m.at } }];
    if (m.kind === 'interrupted') return [{ t, i, item: { kind: 'interrupted', id: m.id, text: m.text, at: m.at } }];
    if (m.kind === 'commitment' && m.card) return [{ t, i, item: { kind: 'commitment', id: m.id, text: m.text, at: m.at, card: m.card as CommitmentCard } }];
    if (m.kind === 'event_card' && m.card) return [{ t, i, item: { kind: 'event', id: m.id, text: m.text, at: m.at, card: m.card as EventCard } }];
    if (m.kind === 'launch_card' && m.card) return [{ t, i, item: { kind: 'launch', id: m.id, text: m.text, at: m.at, card: m.card as LaunchCard } }];
    if (m.kind === 'launch_line' && m.card) return [{ t, i, item: { kind: 'launch_line', id: m.id, text: m.text, at: m.at, card: m.card as LaunchLine } }];
    if (m.kind === 'ship_card' && m.card) return [{ t, i, item: { kind: 'ship', id: m.id, text: m.text, at: m.at, card: m.card as ShipCard } }];
    if (m.kind === 'campaign_draft_card' && m.card) return [{ t, i, item: { kind: 'campaign_draft', id: m.id, text: m.text, at: m.at, card: m.card as CampaignDraftCard } }];
    if (m.kind === 'following_card' && m.card) return [{ t, i, item: { kind: 'following', id: m.id, text: m.text, at: m.at, card: m.card as FollowingCard } }];
    if (m.kind === 'need_card' && m.card) return [{ t, i, item: { kind: 'need', id: m.id, text: m.text, at: m.at, card: m.card as NeedCard } }];
    if (m.kind === 'question' && m.card) {
      const card = m.card as InboxItem;
      const answer = answers.get(card.id);
      const keptText = answer === undefined ? kept.get('a-' + card.id) : undefined;
      const state = answer === undefined && keptText === undefined ? 'withdrawn' : answer === null ? 'open' : 'answered';
      return [{ t, i, item: { kind: 'question', id: m.id, at: m.at, card, state, ...(answer ? { answer } : {}), ...(keptText !== undefined ? { kept: keptText } : {}) } }];
    }
    if (m.kind === 'message' && m.role) return [{ t, i, item: { kind: 'message', id: m.id, role: m.role, text: m.text, at: m.at, ...(m.thread && m.role === 'user' ? { report: true as const } : {}), ...(m.thread ? { threadId: m.thread } : {}) } }];
    return [];
  });
  rows.sort((a, b) => (Number.isNaN(a.t) || Number.isNaN(b.t) ? 0 : a.t - b.t) || a.i - b.i);
  // A report turn and the replies marked with a ship card that is in the window go under that ship card; the rest stay where they fell.
  const out: ThreadItem[] = [];
  const cards = new Map<string, Extract<ThreadItem, { kind: 'ship' }>>();
  for (const r of rows) if (r.item.kind === 'ship') cards.set(r.item.id, r.item);
  for (const r of rows) {
    const { threadId, ...rest } = r.item as ThreadItem & { threadId?: string };
    const card = threadId ? cards.get(threadId) : undefined;
    if (card && rest.kind === 'message') (card.thread ??= []).push(rest);
    else out.push(r.item.kind === 'ship' ? r.item : (rest as ThreadItem));
  }
  return out;
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

/** owner/repo#n for a GitHub issue URL, else the URL as given. */
const childRef = (url: string): string => {
  const m = /^https?:\/\/[^/]+\/([^/]+\/[^/]+)\/(?:pull|issues)\/(\d+)/.exec(url);
  return m ? `${m[1]}#${m[2]}` : url;
};

/** What a commitment card shows: a one-line outcome, the commitment's text, and the ship page it links to. */
export function commitmentCardView(item: Extract<ThreadItem, { kind: 'commitment' }>, fleet?: string): { eyebrow: string; text: string; reason: string; shipHref: string; settingsHref: string; childLabel: string } {
  const { outcome, ship, reason, child } = item.card;
  const eyebrow = { launched: 'AdmiralBot launched a ship', reminded: 'AdmiralBot reminds you', cant_run: "AdmiralBot can't run this", dropped: 'AdmiralBot dropped this', aged: 'Still want this?', ended: 'Campaign ended', too_large: 'Campaign too large to work on' }[outcome];
  return { eyebrow, text: item.text, reason: reason ?? '', shipHref: outcome === 'launched' && ship ? shipPath(ship) : '', settingsHref: outcome === 'aged' && fleet ? chatSettingsPath(fleet) : '', childLabel: child ? childRef(child) : '' };
}

/** The eyebrow of an open question card: Admiral's own proposals read differently from AdmiralBot's questions. */
export const questionEyebrow = (id: string): string => (id.startsWith('campaign-') ? 'Admiral asks' : 'AdmiralBot asks');

const SHIP_RESULT: Record<ShipCard['result'], string> = { moored: 'Moored', anchored: 'Anchored', ready_to_merge: 'Ready to merge', sailed_off: 'Sailed off', returned: 'Returned to port', lost: 'Lost', failed: 'Failed', done: 'Done' };

const httpsHref = (url: string): string => {
  try {
    return new URL(url).protocol === 'https:' ? url : '';
  } catch {
    return '';
  }
};

const LAUNCH_KIND: Record<string, string> = { quick_ship: 'a quick ship', scout: 'a scout', commodore: 'a Commodore ship' };

/** What a launch line shows: its sentence, the ship and, while the ship is afloat, its page. */
export function launchLineView(item: Extract<ThreadItem, { kind: 'launch_line' }>, ships: Ship[]): { sentence: string; name: string; title: string; shipHref: string; text: string } {
  const c = item.card;
  const kind = Object.hasOwn(LAUNCH_KIND, c.kind) ? LAUNCH_KIND[c.kind] : `${/^[aeiou]/i.test(c.kind) ? 'an' : 'a'} ${c.kind} ship`;
  return { sentence: c.reason ? `Sent ${kind} ${c.reason}` : `Sent ${kind}`, name: c.name || c.ship, title: c.title ?? '', shipHref: ships.some((s) => s.session === c.ship && s.first_launched === c.first_launched) ? shipPath(c.ship) : '', text: c.text };
}

/** What a ship card shows: the ship and its kind, the result in words, each PR and, while its voyage is still in the ships list, the ship's page. */
export function shipCardView(item: Extract<ThreadItem, { kind: 'ship' }>, ships: Ship[]): { eyebrow: string; name: string; title: string; kind: string; result: string; prs: { label: string; url: string; href: string }[]; shipHref: string; findings: boolean; branch: string } {
  const c = item.card;
  const name = c.name || c.ship;
  const kind = c.skill_kind === 'commodore' ? 'Commodore' : c.skill_kind === 'scout' ? 'Scout' : c.skill_kind === 'quick' ? 'Quick ship' : c.skill_kind === 'none' ? 'No skill' : c.skill || 'No skill';
  const afloat = ships.some((s) => s.session === c.ship && s.first_launched === c.first_launched);
  return {
    eyebrow: `${name} · ${kind}`,
    name,
    title: c.title ?? '',
    kind,
    result: Object.hasOwn(SHIP_RESULT, c.result) ? SHIP_RESULT[c.result] : 'Result unknown',
    prs: c.prs.map((p) => ({ label: `PR #${p.number}`, url: p.url, href: httpsHref(p.url) })),
    shipHref: afloat ? shipPath(c.ship) : '',
    findings: c.findings === true,
    branch: c.branch ?? '',
  };
}
