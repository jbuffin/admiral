import { computed, effect, signal } from '@preact/signals';
import { onMessage } from './connection';
import { type Entry, type InboxItem, type InboxQuestion, createQueue } from './inbox';
import { ships } from './ships';
import { api } from './sources';

// The signals behind the Needs You inbox: the served items, the queue (inbox.ts) over them and the
// ships, the open state and what the engineer has entered on each item.

const WIDE = '(min-width: 1024px) and (orientation: landscape)';
const wideQuery = typeof matchMedia === 'function' ? matchMedia(WIDE) : null;
// Wide is the drawer beside the bridge; everything else is the full-screen sheet.
export const wide = signal(wideQuery?.matches ?? false);
wideQuery?.addEventListener('change', (e) => (wide.value = e.matches));

const items = signal<InboxItem[]>([]);
onMessage('inbox', (msg: { items: InboxItem[] }) => (items.value = msg.items));

const queue = createQueue();
// Bumped after every change to the queue, so what reads it re-renders.
const version = signal(0);
const bump = () => (version.value = version.peek() + 1);
// The clock for "Waiting 12 min", which changes without any message.
export const clock = signal(Date.now());
setInterval(() => (clock.value = Date.now()), 30_000);

// Items being sent: they stay in the queue even if a push drops them before the answer comes back.
const sending = new Map<string, InboxItem>();
const itemKey = (it: InboxItem) => `item:${it.session}/${it.id}`;

function refresh(): void {
  const served = items.peek();
  const open = new Set(served.map(itemKey));
  queue.sync({ items: [...served, ...[...sending].filter(([k]) => !open.has(k)).map(([, it]) => it)], ships: ships.peek() }, Date.now());
  bump();
}
effect(() => {
  void items.value;
  void ships.value;
  refresh();
});

setInterval(() => {
  if (queue.tick(Date.now())) bump();
}, 250);

export const inboxOpen = signal(false);
// The wide layout's drawer is open: the Orders panel gives way to it and the bridge narrows.
export const inboxDrawerOpen = computed(() => inboxOpen.value && wide.value);
export function closeInbox(restoreFocus = true): void {
  inboxOpen.value = false;
  if (restoreFocus) setTimeout(() => document.getElementById('inbox-button')?.focus(), 0);
}

effect(() => {
  document.documentElement.classList.toggle('inbox-drawer-open', inboxDrawerOpen.value);
});

export const inbox = {
  entries: (): Entry[] => (version.value, queue.entries()),
  count: () => (version.value, queue.count()),
  position: () => (version.value, queue.position()),
  currentKey: () => (version.value, queue.currentKey()),
  pulsing: () => (version.value, queue.pulsing()),
  move(by: 1 | -1) {
    queue.move(by);
    bump();
  },
  fold(key: string, text: string) {
    queue.fold(key, text, Date.now());
    bump();
  },
};

// ---- what the engineer has entered on an item ----

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
const blank = (): Draft => ({ picks: {}, note: '', error: '', sending: false });
const drafts = signal<Record<string, Draft>>({});
export const draftOf = (key: string): Draft => drafts.value[key] ?? blank();
export function editDraft(key: string, fn: (d: Draft) => Draft): void {
  drafts.value = { ...drafts.value, [key]: fn(draftOf(key)) };
}

const emptyPick: Pick = { sel: [], other: false, text: '' };
export const pickOf = (d: Draft, q: InboxQuestion): Pick => d.picks[q.id] ?? emptyPick;
export const answeredQuestion = (p: Pick) => p.sel.length > 0 || (p.other && p.text.trim() !== '');

export function choose(key: string, q: InboxQuestion, label: string): void {
  editDraft(key, (d) => {
    const p = pickOf(d, q);
    const sel = q.multi ? (p.sel.includes(label) ? p.sel.filter((l) => l !== label) : [...p.sel, label]) : [label];
    return { ...d, picks: { ...d.picks, [q.id]: { ...p, sel, other: q.multi ? p.other : false } } };
  });
}
export function chooseOther(key: string, q: InboxQuestion): void {
  editDraft(key, (d) => {
    const p = pickOf(d, q);
    return { ...d, picks: { ...d.picks, [q.id]: q.multi ? { ...p, other: !p.other } : { ...p, sel: [], other: true } } };
  });
}

// Sends a question's answers. On success it folds to "Answered"; a refusal keeps what was entered and says why.
export async function sendAnswers(entry: Entry): Promise<void> {
  const it = entry.item;
  if (!it?.question) return;
  const d = draftOf(entry.key);
  const answers: Record<string, { choice?: string | string[]; other?: string }> = {};
  for (const q of it.question.questions) {
    const p = pickOf(d, q);
    const a: { choice?: string | string[]; other?: string } = {};
    if (p.sel.length > 0) a.choice = q.multi ? p.sel : p.sel[0];
    if (p.other && p.text.trim()) a.other = p.text.trim();
    answers[q.id] = a;
  }
  editDraft(entry.key, (x) => ({ ...x, sending: true, error: '' }));
  sending.set(entry.key, it);
  const note = d.note.trim();
  const r = await api('POST', `/api/inbox/${encodeURIComponent(it.session)}/${encodeURIComponent(it.id)}/answer`, {
    answers,
    ...(note && { note }),
  });
  if (r.ok) inbox.fold(entry.key, 'Answered');
  sending.delete(entry.key);
  editDraft(entry.key, (x) => ({ ...x, sending: false, error: r.ok ? '' : r.error }));
  refresh();
}
