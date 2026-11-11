import { computed, effect, signal } from '@preact/signals';
import { onMessage } from './connection';
import { type Decisions, type FindingCard, createDecisions, readCards } from './findings';
import { type Entry, type InboxItem, createQueue } from './inbox';
import { type Draft, answerBody, blankDraft } from './questions';
import { ships } from './ships';
import { wide } from './sizeClass';
import { api } from './sources';

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

// The per-item stores below are declared further down; the first refresh runs while the module loads.
let storesReady = false;

function refresh(): void {
  const served = items.peek();
  const open = new Set(served.map(itemKey));
  queue.sync({ items: [...served, ...[...sending].filter(([k]) => !open.has(k)).map(([, it]) => it)], ships: ships.peek() }, Date.now());
  // What was entered on an item that has left goes with it: a reused ship name and item id starts fresh.
  const live = new Set(queue.entries().map((e) => e.key));
  if (storesReady) for (const key of itemStateKeys()) if (!live.has(key)) forgetItemState(key);
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
  setPending(key: string, on: boolean) {
    queue.setPending(key, on);
    bump();
  },
};

const drafts = signal<Record<string, Draft>>({});
export const draftOf = (key: string): Draft => drafts.value[key] ?? blankDraft();
export function editDraft(key: string, fn: (d: Draft) => Draft): void {
  drafts.value = { ...drafts.value, [key]: fn(draftOf(key)) };
}

export async function sendAnswers(entry: Entry): Promise<void> {
  const it = entry.item;
  if (!it?.question) return;
  const body = answerBody(it.question.questions, draftOf(entry.key));
  editDraft(entry.key, (x) => ({ ...x, sending: true, error: '' }));
  sending.set(entry.key, it);
  const r = await api('POST', `/api/inbox/${encodeURIComponent(it.session)}/${encodeURIComponent(it.id)}/answer`, body);
  if (r.ok) inbox.fold(entry.key, 'Answered');
  sending.delete(entry.key);
  editDraft(entry.key, (x) => ({ ...x, sending: false, error: r.ok ? '' : r.error }));
  refresh();
}

export type ReviewStep = 'idle' | 'choose' | 'confirm' | 'discard';
export interface ReviewUi {
  step: ReviewStep;
  event: string;
  busy: boolean;
  error: string;
}
const reviews = signal<Record<string, ReviewUi>>({});
const noReview: ReviewUi = { step: 'idle', event: '', busy: false, error: '' };
export const reviewOf = (key: string): ReviewUi => reviews.value[key] ?? noReview;
export function editReview(key: string, patch: Partial<ReviewUi>): void {
  reviews.value = { ...reviews.value, [key]: { ...reviewOf(key), ...patch } };
}
// The engineer pressed Edit on this item in this page's life: Submit says so.
const opened = new Set<string>();
export const markEdited = (key: string) => void opened.add(key);

const itemPath = (it: InboxItem, verb: string) => `/api/inbox/${encodeURIComponent(it.session)}/${encodeURIComponent(it.id)}/${verb}`;

// Every key that holds per-item state, and the one place that clears it.
function itemStateKeys(): Set<string> {
  return new Set([
    ...Object.keys(drafts.peek()),
    ...Object.keys(reviews.peek()),
    ...Object.keys(editing.peek()),
    ...Object.keys(findingsUi.peek()),
    ...Object.keys(removals.peek()),
    ...decisions.keys(),
    ...opened,
  ]);
}
function forgetItemState(key: string): void {
  const without = <T,>(m: Record<string, T>) => Object.fromEntries(Object.entries(m).filter(([k]) => k !== key));
  drafts.value = without(drafts.peek());
  reviews.value = without(reviews.peek());
  editing.value = without(editing.peek());
  findingsUi.value = without(findingsUi.peek());
  removals.value = without(removals.peek());
  decisions.delete(key);
  cardsByItem.delete(key);
  opened.delete(key);
  fbump();
}

const inflight = new Set<string>();

// The badge drops at the tap; a refusal brings it back and keeps what was entered.
async function act(
  entry: Entry,
  verb: string,
  body: unknown,
  foldText: (data: { action?: string; invalid_target?: boolean }) => string,
  setBusy: (busy: boolean, error: string) => void,
): Promise<void> {
  const it = entry.item;
  if (!it || inflight.has(entry.key)) return;
  inflight.add(entry.key);
  setBusy(true, '');
  sending.set(entry.key, it);
  inbox.setPending(entry.key, true);
  const r = await api<{ action?: string; invalid_target?: boolean }>('POST', itemPath(it, verb), body);
  inflight.delete(entry.key);
  sending.delete(entry.key);
  if (r.ok) {
    inbox.fold(entry.key, foldText(r.data ?? {}));
    forgetItemState(entry.key); // the fold needs none of it, and a reused name and id must start fresh
  } else {
    inbox.setPending(entry.key, false);
    setBusy(false, r.error);
  }
  refresh();
}

export function submitReview(entry: Entry, event: string): Promise<void> {
  return act(
    entry,
    'submit',
    { event, edited: opened.has(entry.key) },
    (d) => (d.action === 'gone' ? 'Review no longer pending on GitHub' : `Review submitted: ${event}`),
    (busy, error) => editReview(entry.key, { busy, error }),
  );
}

export function discardReview(entry: Entry): Promise<void> {
  return act(
    entry,
    'discard',
    undefined,
    (d) => (d.invalid_target ? 'Review removed from the inbox; nothing changed on GitHub' : d.action === 'gone' ? 'Review no longer pending on GitHub' : 'Review discarded'),
    (busy, error) => editReview(entry.key, { busy, error }),
  );
}

// The ship never gets an answer, and it is not resumed.
export interface RemoveUi {
  confirm: boolean;
  busy: boolean;
  error: string;
}
const removals = signal<Record<string, RemoveUi>>({});
const noRemoval: RemoveUi = { confirm: false, busy: false, error: '' };
export const removalOf = (key: string): RemoveUi => removals.value[key] ?? noRemoval;
export function editRemoval(key: string, patch: Partial<RemoveUi>): void {
  removals.value = { ...removals.value, [key]: { ...removalOf(key), ...patch } };
}

export function removeItem(entry: Entry): Promise<void> {
  return act(
    entry,
    'remove',
    undefined,
    () => 'Removed from the inbox',
    (busy, error) => editRemoval(entry.key, { busy, error }),
  );
}

const decisions = new Map<string, Decisions>();
const cardsByItem = new Map<string, FindingCard[]>();
export function cardsOf(entry: Entry): FindingCard[] {
  decisionsOf(entry);
  return cardsByItem.get(entry.key)!;
}
const fver = signal(0);
const fbump = () => (fver.value = fver.peek() + 1);
export function decisionsOf(entry: Entry): Decisions {
  let d = decisions.get(entry.key);
  if (!d) {
    const cards = readCards(entry.item?.findings?.cards);
    cardsByItem.set(entry.key, cards);
    decisions.set(entry.key, (d = createDecisions(cards)));
  }
  return d;
}
/** Read before a render that shows the decisions, so it re-renders when they change. */
export const watchDecisions = () => void fver.value;
export function decide(entry: Entry, fn: (d: Decisions) => unknown): void {
  fn(decisionsOf(entry));
  fbump();
}

const editing = signal<Record<string, Record<string, string>>>({});
export const editingOf = (key: string, id: string): string | undefined => editing.value[key]?.[id];
export const isEditing = (key: string): boolean => Object.keys(editing.value[key] ?? {}).length > 0;
export function setEditing(key: string, id: string, text: string | undefined): void {
  const mine = { ...editing.value[key] };
  if (text === undefined) delete mine[id];
  else mine[id] = text;
  editing.value = { ...editing.value, [key]: mine };
}

const findingsUi = signal<Record<string, { busy: boolean; error: string }>>({});
export const findingsUiOf = (key: string) => findingsUi.value[key] ?? { busy: false, error: '' };

export function sendFindings(entry: Entry): Promise<void> {
  const d = decisionsOf(entry);
  if (!d.canSend() || isEditing(entry.key)) return Promise.resolve();
  const summary = d.summary();
  return act(
    entry,
    'findings',
    d.payload(),
    () => summary,
    (busy, error) => (findingsUi.value = { ...findingsUi.value, [entry.key]: { busy, error } }),
  );
}

storesReady = true;
