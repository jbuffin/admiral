import { effect, signal } from '@preact/signals';
import type { Ship } from './api';
import { attentionCount, sharedInboxQueue, type InboxItem } from './inbox';
import { inboxItems, ships } from './shipsLink';

export interface AttentionTarget {
  document: { title: string; querySelector(sel: string): { href: string } | null };
  navigator: { setAppBadge?(n?: number): Promise<void>; clearAppBadge?(): Promise<void> };
}

const PLAIN_ICON = '/favicon.svg';
// The favicon's own markup with a dot in the corner (the redesign's needs-you red), so the logo itself stays as drawn.
export const DOT_ICON = `data:image/svg+xml,${encodeURIComponent(
  '<svg xmlns="http://www.w3.org/2000/svg" width="46" height="46" viewBox="0 0 46 46" fill="none"><path d="M5 20Q23 0 41 20Z" fill="#0B1F33" stroke="#FFC23D" stroke-width="2.5" stroke-linejoin="round"/><circle cx="23" cy="12" r="3" fill="#FFC23D"/><rect x="9" y="20" width="28" height="22" rx="4" fill="#FFC23D" stroke="#0B1F33" stroke-width="2.5"/><path fill="#14304A" d="M15 27h5v5h-5zM26 27h5v5h-5zM18 36h10v3H18z"/><circle cx="37" cy="9" r="8" fill="#E5532F" stroke="#0B1F33" stroke-width="2"/></svg>',
)}`;

export function applyAttention(count: number, target: AttentionTarget): void {
  target.document.title = count > 0 ? `(${count}) Admiral` : 'Admiral';
  const icon = target.document.querySelector('link[rel="icon"]');
  if (icon) icon.href = count > 0 ? DOT_ICON : PLAIN_ICON;
  const nav = target.navigator;
  // The badge is best effort: a browser may refuse it outside an installed app.
  if (count > 0) nav.setAppBadge?.(count)?.catch(() => {});
  else nav.clearAppBadge?.()?.catch(() => {});
}

/** An applier that skips the write when the count is the one it last applied. */
export function attentionApplier(target: AttentionTarget): (count: number) => void {
  let last: number | null = null;
  return (count) => {
    if (count === last) return;
    last = count;
    applyAttention(count, target);
  };
}

// Bumped when the shared queue changes without a new push (a fold, a hold running out), so readers render again.
export const queueRev = signal(0);

let synced: [InboxItem[], Ship[]] | null = null;
/** Feeds the shared queue the shell's latest items and ships, once per push. */
export function syncSharedQueue(items: InboxItem[], fleetShips: Ship[]): void {
  if (synced && synced[0] === items && synced[1] === fleetShips) return;
  synced = [items, fleetShips];
  sharedInboxQueue().sync({ items, ships: fleetShips }, Date.now());
}

/** Keeps the shared queue and the title, favicon and badge current for the whole shell; returns a stop. */
export function watchAttention(target: AttentionTarget = { document, navigator }): () => void {
  const queue = sharedInboxQueue();
  const apply = attentionApplier(target);
  const stop = effect(() => {
    syncSharedQueue(inboxItems.value, ships.value);
    void queueRev.value;
    apply(attentionCount(queue));
  });
  const timer = setInterval(() => {
    if (queue.tick(Date.now())) queueRev.value++;
  }, 1000);
  return () => {
    stop();
    clearInterval(timer);
  };
}
