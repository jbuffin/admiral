import { conversationUrl, type ConversationPage, type Item } from './api';
import { onShipsMessage, onShipsOpen, sendShips } from './shipsLink';

export interface Conversation {
  state: 'loading' | 'ok' | 'none' | 'unreadable' | 'gone';
  items: Item[];
  more: boolean;
}

// The server never answers an open for a ship with nothing recorded, so the page is read anyway after this wait.
const ACK_WAIT_MS = 1500;
// A ship whose transcript has not appeared is asked about again until it does; a harness with no adapter is not.
const RETRY_MS = 3000;

function merge(have: Item[], incoming: Item[]): Item[] {
  const at = new Map(have.map((it, i) => [it.id, i]));
  const next = [...have];
  for (const it of incoming) {
    const i = at.get(it.id);
    if (i === undefined) {
      at.set(it.id, next.length);
      next.push(it);
    } else next[i] = it;
  }
  return next;
}

/** Follows one ship's conversation over the ships socket: subscribe, read the page, then take the pushes. */
export function followConversation(session: string, onChange: (c: Conversation) => void, { retryNone = true }: { retryNone?: boolean } = {}): { loadOlder(): Promise<void>; stop(): void } {
  let c: Conversation = { state: 'loading', items: [], more: false };
  let stopped = false;
  let acked = false;
  let wait: ReturnType<typeof setTimeout> | undefined;
  // A reply to an older read must not overwrite a newer one.
  let reads = 0;
  const set = (next: Conversation) => {
    c = next;
    onChange(c);
  };

  const read = async () => {
    const n = ++reads;
    let page: ConversationPage;
    try {
      const res = await fetch(conversationUrl(session));
      if (res.status === 404) return stopped || n !== reads ? undefined : set({ state: 'gone', items: [], more: false });
      if (!res.ok) return;
      page = await res.json();
    } catch {
      return;
    }
    if (stopped || n !== reads) return;
    const items = page.items ?? [];
    if (page.state !== 'ok') set({ state: page.state, items: [], more: false });
    else set({ state: 'ok', items: merge(c.items, items), more: c.items.length > 0 ? c.more : page.more });
    if ((page.state === 'none' && retryNone) || (page.state === 'ok' && !acked)) retry();
  };

  const retry = () => {
    clearTimeout(wait);
    wait = setTimeout(open, RETRY_MS);
  };

  const open = () => {
    if (stopped) return;
    acked = false;
    clearTimeout(wait);
    if (sendShips({ type: 'conversation_open', ship: session })) wait = setTimeout(() => void read(), ACK_WAIT_MS);
    else retry();
  };

  const offMessage = onShipsMessage((m) => {
    if (m.ship !== session) return;
    if (m.type === 'conversation_opened') {
      acked = true;
      clearTimeout(wait);
      void read();
    } else if (m.type === 'conversation' && c.state !== 'gone') {
      set({ ...c, state: 'ok', items: merge(c.items, (m.items ?? []) as Item[]) });
    }
  });
  const offOpen = onShipsOpen(open);
  open();

  return {
    async loadOlder() {
      const first = c.items[0];
      if (!first || !c.more) return;
      const res = await fetch(conversationUrl(session, first.id));
      if (!res.ok || stopped) return;
      const page = (await res.json()) as ConversationPage;
      const older = page.items ?? [];
      const have = new Set(c.items.map((i) => i.id));
      set({ ...c, items: [...older.filter((i) => !have.has(i.id)), ...c.items], more: page.more });
    },
    stop() {
      if (stopped) return;
      stopped = true;
      clearTimeout(wait);
      offMessage();
      offOpen();
      sendShips({ type: 'conversation_close', ship: session });
    },
  };
}
