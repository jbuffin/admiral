import type { ChatCommitment, ChatHarnessOption, Ship } from './api';
import { fieldMessage } from './fleetSettings';

export interface ChatSettings {
  harness: string;
  model: string;
  effort: string;
  context_threshold: number | 'never';
}

export const CHAT_THRESHOLDS: ChatSettings['context_threshold'][] = [100000, 150000, 300000, 'never'];

/** Choosing a harness keeps the model and effort it takes and resets the rest to its defaults (its lists lead with them). */
export function switchHarness(settings: ChatSettings, option: Pick<ChatHarnessOption, 'harness' | 'models' | 'efforts'>): ChatSettings {
  return {
    ...settings,
    harness: option.harness,
    model: option.models.includes(settings.model) ? settings.model : (option.models[0] ?? settings.model),
    effort: option.efforts.includes(settings.effort) ? settings.effort : (option.efforts[0] ?? ''),
  };
}

/** Only a Claude Code chat is given the instructions, the notes and the scheduled refresh; the other harnesses' chats take none of them yet. */
export const readsMemory = (harness: string): boolean => harness === 'claude';

/** The line under the instructions and notes of a chat that doesn't read them. */
export const memoryNotRead = (harness: string): string =>
  readsMemory(harness) ? '' : `AdmiralBot on ${harness.charAt(0).toUpperCase() + harness.slice(1)} doesn't read the instructions or the notes.`;

const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

export const harnessLabel = capital;
// An alias (sonnet) is capitalised; an id a harness names itself (gpt-6.1-sol) is shown as given.
export const modelLabel = (m: string) => (/\d/.test(m) ? m : capital(m));
export const effortLabel = (e: string) => (e === '' ? 'Default' : capital(e));
export const thresholdLabel = (t: ChatSettings['context_threshold']) => (t === 'never' ? 'Never' : `${t / 1000}K`);

/** What a save sends: only the keys that changed, or null when none did. */
export function chatSaveBody(shown: ChatSettings, edited: ChatSettings): { chat: Partial<ChatSettings> } | null {
  const chat: Partial<ChatSettings> = {};
  if (shown.harness !== edited.harness) chat.harness = edited.harness;
  if (shown.model !== edited.model) chat.model = edited.model;
  if (shown.effort !== edited.effort) chat.effort = edited.effort;
  if (shown.context_threshold !== edited.context_threshold) chat.context_threshold = edited.context_threshold;
  return Object.keys(chat).length > 0 ? { chat } : null;
}

/** The server's refusal without its `fleets.<key>.chat.<field>: ` path. */
export const chatErrorMessage = fieldMessage;

export const INSTRUCTIONS_MAX = 2000;

/** The length the server counts: Unicode code points of the trimmed text. */
export const instructionsCount = (text: string): number => [...text.trim()].length;

/** Whether "Save instructions" is enabled: the text differs from what is saved and fits the cap. */
export const canSaveInstructions = (saved: string, edited: string): boolean => saved.trim() !== edited.trim() && instructionsCount(edited) <= INSTRUCTIONS_MAX;

/** A commitment's trigger in plain words; `href` is set for a PR or issue. */
export function triggerWords(trigger: ChatCommitment['trigger'], ships: Pick<Ship, 'session' | 'name' | 'launched'>[], campaign?: { title?: string; url: string }): { text: string; href?: string } {
  const { kind, subject = '' } = trigger;
  if (kind === 'campaign') {
    const m = campaign && /^https?:\/\/[^/]+\/([^/]+\/[^/]+)\/issues\/(\d+)/.exec(campaign.url);
    const name = campaign?.title || (m ? `${m[1]}#${m[2]}` : subject);
    return { text: `Campaign ${name}`, ...(campaign?.url && { href: campaign.url }) };
  }
  if (kind === 'ship_sailed' || kind === 'ship_anchored') {
    // A session is reused, so a stored launch stamp must match too, or this is another ship.
    const name = ships.find((s) => s.session === subject && (!trigger.launched || s.launched === trigger.launched))?.name || subject;
    return { text: `When ${name} ${kind === 'ship_sailed' ? 'sails off' : 'anchors'}` };
  }
  if (kind === 'time') {
    const d = new Date(subject);
    return { text: `At ${Number.isNaN(d.getTime()) ? subject : d.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })}` };
  }
  if (kind === 'none') return { text: 'No trigger' };
  // The stored subject is canonical: https://<host>/<owner>/<repo>/(pull|issues)/<n>.
  const m = /^https?:\/\/[^/]+\/([^/]+\/[^/]+)\/(?:pull|issues)\/(\d+)/.exec(subject);
  const what = kind === 'pr_merged' ? 'is merged' : 'is closed';
  const ref = m ? `${m[1]}#${m[2]}` : subject;
  return { text: `When ${kind === 'issue_closed' ? 'issue' : 'PR'} ${ref} ${what}`, ...(m && { href: subject }) };
}

/** What a commitment does when it fires. */
export const actionWords = (action: ChatCommitment['action']): string => (action === 'launch' ? 'Launch a ship' : action === 'work' ? 'Launch Without-you children' : 'Remind you');

/** A Cancel failure in words; a 404 means the commitment closed since the page loaded. */
export const cancelErrorMessage = (status: number | undefined, error: string): string => (status === 404 ? 'That commitment already closed.' : error);

/** The marks a commitment row carries: a campaign's counts, waiting, with the reason when the server gave one, and already asked about. */
export function commitmentMarks(c: Pick<ChatCommitment, 'waiting' | 'wait_reason' | 'shown' | 'launched' | 'waiting_children'>): string[] {
  return [
    ...(c.launched ? [`${c.launched} launched`] : []),
    ...(c.waiting_children ? [`${c.waiting_children} waiting`] : []),
    ...(c.waiting ? [c.wait_reason || 'Waiting for room'] : []),
    ...(c.shown ? ['Still want this?'] : []),
  ];
}

/** How long ago a note was saved; a note lasts days, so the unit grows past minutes. */
export function noteAge(ts: string, now: number): string {
  const t = Date.parse(ts);
  if (Number.isNaN(t)) return '';
  const m = Math.max(0, Math.floor((now - t) / 60000));
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  if (m < 1440) return `${Math.floor(m / 60)}h ago`;
  return `${Math.floor(m / 1440)}d ago`;
}
