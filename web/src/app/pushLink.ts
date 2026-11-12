import { effect, signal } from '@preact/signals';
import { alertDestination, alertSearch } from './alerts';
import { canResolve } from './pushResolve';
import { pushTargetOf, pushTargetOfData, type PushTarget } from './pushTarget';
import { go } from './router';
import { inboxFresh, inboxItems, ships, shipsFresh, shipsLink } from './shipsLink';
import { staleNotices } from './staleNotices';

interface Registration {
  getNotifications(): Promise<{ data: unknown; close(): void }[]>;
}

/** Closes the shown notifications whose entry is gone, matching on each one's payload; answering a stop sends no push, so this is the only way they leave. */
async function closeStale(): Promise<void> {
  if (!('serviceWorker' in navigator)) return;
  const reg = (await navigator.serviceWorker.getRegistration('/app/')) as unknown as Registration | undefined;
  if (!reg) return;
  for (const n of staleNotices(await reg.getNotifications(), { ships: ships.value, items: inboxItems.value })) n.close();
}

/** Opens a tapped push. The payload (from the address a tap opened, or the worker's message to an open window) waits until the lists are fresh: a page resumed from the background holds old ones. Also closes stale notifications whenever fresh lists change. */
export function watchPush(): () => void {
  const pending = signal<{ target: PushTarget; replace: boolean } | null>(null);
  const fromAddress = pushTargetOf(location.search);
  if (fromAddress) pending.value = { target: fromAddress, replace: true };
  const onMessage = (e: MessageEvent) => {
    if (!(e.source instanceof ServiceWorker)) return;
    const msg = e.data as { type?: unknown; data?: unknown } | null;
    const target = msg?.type === 'push-click' ? pushTargetOfData(msg.data) : null;
    if (target) pending.value = { target, replace: false };
  };
  const sw = 'serviceWorker' in navigator ? navigator.serviceWorker : null;
  sw?.addEventListener('message', onMessage);
  const stop = effect(() => {
    void ships.value;
    void inboxItems.value;
    if (!canResolve({ link: shipsLink.value, shipsFresh: shipsFresh.value, inboxFresh: inboxFresh.value })) return;
    const p = pending.value;
    if (p) {
      pending.value = null;
      go(alertDestination(p.target, { ships: ships.value, items: inboxItems.value }), alertSearch(p.target), p.replace);
    }
    void closeStale().catch(() => {});
  });
  return () => {
    sw?.removeEventListener('message', onMessage);
    stop();
  };
}
