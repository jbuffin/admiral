import { computed, effect, signal } from '@preact/signals';
import type { Probe } from './connection';
import type { InboxQuestion } from './inbox';
import { inboxOpen } from './inboxState';
import { selected } from './launch';
import { phoneTab } from './phoneTab';
import { type AnswerBody, type Draft, blankDraft } from './questions';
import { phone, wide } from './sizeClass';

// The chat runs per fleet and lives in the new shell. This pane is frozen with the rest of the old UI: it shows the
// chat as off and calls no chat route.
const MOVED = 'The chat is per fleet now. Open it in the new shell at /app/.';

export const CHAT_SESSION = '_chat';
export const CHAT_FAB_ID = 'chat-fab';
export const CHAT_BUTTON_ID = 'chat-button';

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  text: string;
  at: string;
}

export interface ChatAnswer {
  answered?: string;
  answers?: AnswerBody['answers'];
  note?: string;
}

export interface ChatQuestion {
  id: string;
  title?: string;
  created: string;
  question?: { questions: InboxQuestion[] };
  answer: ChatAnswer | null;
}

export interface ChatSession {
  running: boolean;
  state: 'idle' | 'working' | 'waiting';
  messages: ChatMessage[];
  questions: ChatQuestion[];
  error?: string;
}

export const session = signal<ChatSession | null>(null);

export const chatUnread = signal(false);
export const chatOpen = signal(false);
export const chatView = signal<'chat' | 'terminal'>('chat');
const showing = () => (phone.peek() ? phoneTab.peek() === 'chat' : chatOpen.peek());

effect(() => {
  if (phone.value ? phoneTab.value === 'chat' : chatOpen.value) chatUnread.value = false;
});

let opener: HTMLElement | null = null;
const entryId = () => (wide.peek() ? CHAT_FAB_ID : CHAT_BUTTON_ID);

export function openChat(from?: HTMLElement | null): void {
  opener = from ?? (document.activeElement as HTMLElement | null);
  // At the middle size the chat and the inbox are both full-screen sheets: one at a time.
  if (!wide.peek() && !phone.peek()) inboxOpen.value = false;
  chatOpen.value = true;
}

export function closeChat(): void {
  chatOpen.value = false;
  const from = opener;
  opener = null;
  setTimeout(() => {
    // Safari doesn't focus a tapped button, so the opener comes from the click; if it is gone, the entry.
    const target = from?.isConnected ? from : document.getElementById(entryId());
    target?.focus({ preventScroll: true });
  }, 0);
}

export function toggleChat(e: Event): void {
  if (chatOpen.peek()) closeChat();
  else openChat(e.currentTarget as HTMLElement);
}

// Resizing into the middle size with both open keeps the chat.
effect(() => {
  if (chatOpen.value && !wide.value && !phone.value && inboxOpen.value) inboxOpen.value = false;
});

// Escape closes one layer per press. Dialogs, menus and an armed order handle their own Escape first, since the panel is
// non-modal; a phone's Chat tab is a tab, not a layer, so Escape only dismisses its bar there.
export const confirmNew = signal(false);
window.addEventListener('keydown', (e) => {
  if (e.key !== 'Escape' || e.defaultPrevented) return;
  // The listener runs before a dialog's own cancel step, so an open dialog still matches here.
  if (document.querySelector('dialog[open], .menu-list') || selected.peek()) return;
  if (!showing()) return;
  // Esc is Claude Code's interrupt key: typed in the chat's terminal it goes to Claude only.
  if ((e.target as Element | null)?.closest?.('.chat-term')) return;
  if (confirmNew.peek()) confirmNew.value = false;
  else if (!phone.peek()) closeChat();
});

// The on-screen keyboard shrinks the visual viewport, not the layout one: the panel and the sheet size to what shows.
export const viewport = signal({ top: 0, height: 0, keyboard: false, client: 0 });
if (typeof window !== 'undefined' && window.visualViewport) {
  const vv = window.visualViewport;
  const update = () => {
    const client = document.documentElement.clientHeight || window.innerHeight || 0;
    viewport.value = { top: vv.offsetTop, height: vv.height, keyboard: vv.height < client - 120, client };
  };
  vv.addEventListener('resize', update);
  vv.addEventListener('scroll', update);
  update();
}
export const boxFocused = signal(false);

// startChat has been asked at least once: before that the page knows nothing a notice could say.
export const asked = signal(false);
export const starting = signal(false);
export const startError = signal('');
export const chatError = signal('');
export const draft = signal('');
const sendingMessage = signal(false);

/** Opening the chat starts the session; the server leaves one that is running alone. */
export async function startChat(): Promise<void> {
  if (starting.peek()) return;
  asked.value = true;
  starting.value = true;
  startError.value = '';
  starting.value = false;
  startError.value = MOVED;
}

export async function sendMessage(): Promise<void> {
  chatError.value = MOVED;
}

export async function newChat(): Promise<void> {
  chatError.value = MOVED;
  confirmNew.value = false;
}

// Kept here, not in the inbox's store: that one drops every key that is not a live inbox item.
const qdrafts = signal<Record<string, Draft>>({});
export const qdraftOf = (id: string): Draft => qdrafts.value[id] ?? blankDraft();
export function editQDraft(id: string, fn: (d: Draft) => Draft): void {
  qdrafts.value = { ...qdrafts.value, [id]: fn(qdraftOf(id)) };
}

export async function answerQuestion(id: string, _body: AnswerBody): Promise<void> {
  editQDraft(id, (d) => ({ ...d, sending: false, error: MOVED }));
}

export async function probeChat(): Promise<Probe> {
  return { kind: 'down' };
}

export type ThreadItem =
  | { kind: 'me' | 'bot'; id: string; text: string; at: string }
  | { kind: 'card'; id: string; q: ChatQuestion }
  | { kind: 'fold'; id: string; q: ChatQuestion };

export const thread = computed<ThreadItem[]>(() => {
  const s = session.value;
  if (!s) return [];
  const rows = [
    ...s.messages.map((m, i) => ({ t: Date.parse(m.at), i, item: { kind: m.role === 'user' ? 'me' : 'bot', id: `m:${m.id}`, text: m.text, at: m.at } as ThreadItem })),
    ...s.questions.map((q, i) => ({ t: Date.parse(q.created), i: s.messages.length + i, item: { kind: q.answer ? 'fold' : 'card', id: `q:${q.id}`, q } as ThreadItem })),
  ];
  rows.sort((a, b) => (Number.isNaN(a.t) || Number.isNaN(b.t) ? 0 : a.t - b.t) || a.i - b.i);
  return rows.map((r) => r.item);
});

export function clockTime(at?: string): string {
  const d = at ? new Date(at) : null;
  return d && !Number.isNaN(d.getTime()) ? d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }) : '';
}
