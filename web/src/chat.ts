import { computed, effect, signal } from '@preact/signals';
import type { Probe } from './connection';
import { onMessage } from './connection';
import type { InboxQuestion } from './inbox';
import { inboxOpen } from './inboxState';
import { selected } from './launch';
import { phoneTab } from './phoneTab';
import { type AnswerBody, type Draft, blankDraft } from './questions';
import { phone, wide } from './sizeClass';
import { api } from './sources';

// The AdmiralBot chat (#119): the conversation the server tails from Claude's transcript, the questions
// AdmiralBot asked, and what the engineer has entered. The `chat` message and GET /api/chat/session share
// one shape; everything on screen is read from it.

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

// ---- what has been seen, for the dot ----

// Message and question ids this page has shown or been told about. The first snapshot of the page's life
// is seen; later ones set the dot for an assistant message or a question that is new while the chat is hidden.
const seen = new Set<string>();
let seeded = false;
export const chatUnread = signal(false);
export const chatOpen = signal(false);
export const chatView = signal<'chat' | 'terminal'>('chat');
// The chat shows: the phone's tab, or the panel or sheet.
const showing = () => (phone.peek() ? phoneTab.peek() === 'chat' : chatOpen.peek());

function applySnapshot(msg: Partial<ChatSession>): void {
  const next: ChatSession = {
    running: !!msg.running,
    state: msg.state ?? 'idle',
    messages: msg.messages ?? [],
    questions: msg.questions ?? [],
    error: msg.error,
  };
  let fresh = false;
  const ids = [...next.messages.filter((m) => m.role === 'assistant').map((m) => `m:${m.id}`), ...next.questions.map((q) => `q:${q.id}`)];
  for (const id of ids) if (!seen.has(id)) fresh = true;
  for (const id of ids) seen.add(id);
  for (const m of next.messages) seen.add(`m:${m.id}`);
  if (seeded && fresh && !showing()) chatUnread.value = true;
  seeded = true;
  session.value = next;
  // A question that left (New chat) takes what was entered on it.
  const live = new Set(next.questions.map((q) => q.id));
  const kept = Object.entries(qdrafts.peek()).filter(([id]) => live.has(id));
  if (kept.length !== Object.keys(qdrafts.peek()).length) qdrafts.value = Object.fromEntries(kept);
  // The terminal gives way when the session ends, without waiting for the socket to drop.
  if (!next.running) chatView.value = 'chat';
}
onMessage('chat', applySnapshot);

effect(() => {
  if (phone.value ? phoneTab.value === 'chat' : chatOpen.value) chatUnread.value = false;
});

// ---- where it opens ----

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

// Escape closes one layer per press: a dialog, a menu or an armed order first (each handles its own Escape, and the
// panel is non-modal, so they can be open over it), then the New chat bar, then the panel or sheet. A phone's
// Chat tab is a tab, not a layer, so Escape only dismisses its bar there.
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

// ---- the keyboard ----

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
// A phone's tab bar steps aside while the box has focus.
export const boxFocused = signal(false);

// ---- starting, sending, New chat ----

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
  const r = await api('POST', '/api/chat/start');
  starting.value = false;
  if (!r.ok) startError.value = r.error;
}

export async function sendMessage(): Promise<void> {
  const typed = draft.peek();
  const text = typed.trim();
  if (!text || sendingMessage.peek()) return;
  sendingMessage.value = true;
  chatError.value = '';
  const r = await api('POST', '/api/chat/messages', { text });
  sendingMessage.value = false;
  if (!r.ok) chatError.value = r.error;
  else {
    // Keep only what was typed while the POST was out, so a second Enter doesn't send the text again.
    const now = draft.peek();
    draft.value = now.startsWith(typed) ? now.slice(typed.length) : now;
  }
}

export async function newChat(): Promise<void> {
  chatError.value = '';
  const r = await api('POST', '/api/chat/new');
  confirmNew.value = false;
  if (!r.ok) {
    chatError.value = r.error;
    return;
  }
  draft.value = '';
  qdrafts.value = {};
}

// ---- questions ----

// Kept here, not in the inbox's store: that one drops every key that is not a live inbox item.
const qdrafts = signal<Record<string, Draft>>({});
export const qdraftOf = (id: string): Draft => qdrafts.value[id] ?? blankDraft();
export function editQDraft(id: string, fn: (d: Draft) => Draft): void {
  qdrafts.value = { ...qdrafts.value, [id]: fn(qdraftOf(id)) };
}

async function reread(): Promise<void> {
  try {
    const res = await fetch('/api/chat/session', { cache: 'no-store' });
    if (res.ok) applySnapshot(await res.json());
  } catch {
    // The socket's next push brings it.
  }
}

/** Sends an answer. The card folds when the served snapshot carries the answer, never before. */
export async function answerQuestion(id: string, body: AnswerBody): Promise<void> {
  if (qdraftOf(id).sending) return;
  editQDraft(id, (d) => ({ ...d, sending: true, error: '' }));
  const r = await api('POST', `/api/chat/questions/${encodeURIComponent(id)}/answer`, body);
  // A 409 means it was answered elsewhere; that answer is in the snapshot, which the server doesn't push for it.
  if (r.ok || r.status === 409) await reread();
  const folded = session.peek()?.questions.find((q) => q.id === id)?.answer != null;
  editQDraft(id, (d) => ({ ...d, sending: false, error: r.ok || folded ? '' : r.error }));
}

// ---- the terminal ----

/** The reconnect check of the chat's terminal: is `_chat` still running? Same shape as the ships probe. */
export async function probeChat(): Promise<Probe> {
  try {
    const res = await fetch('/api/chat/session', { cache: 'no-store' });
    if (res.status === 401) return { kind: 'signed-out' };
    if (!res.ok) return { kind: 'down' };
    const { running } = await res.json();
    return { kind: 'ok', ships: running ? [{ session: CHAT_SESSION }] : [] };
  } catch {
    return { kind: 'down' };
  }
}

// ---- the thread ----

export type ThreadItem =
  | { kind: 'me' | 'bot'; id: string; text: string; at: string }
  | { kind: 'card'; id: string; q: ChatQuestion }
  | { kind: 'fold'; id: string; q: ChatQuestion };

/** The messages and the questions in the order they happened. */
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
