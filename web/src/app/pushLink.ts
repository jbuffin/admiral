import { effect } from '@preact/signals';
import { alertDestination, alertSearch } from './alerts';
import { pushTargetOf, pushTargetOfData, type PushTarget } from './pushTarget';
import { go } from './router';
import { inboxItems, inboxLoaded, ships, shipsLoaded } from './shipsLink';
import { staleNotices } from './staleNotices';

const open = (t: PushTarget, replace: boolean) => go(alertDestination(t, { ships: ships.value, items: inboxItems.value }), alertSearch(t), replace);

interface Registration {
  getNotifications(): Promise<{ data: unknown; close(): void }[]>;
}

/** Closes the shown notifications whose entry is gone, matching on each one's payload; answering a stop sends no push, so this is the only way they leave. */
async function closeStale(): Promise<void> {
  if (!shipsLoaded.value || !inboxLoaded.value || !('serviceWorker' in navigator)) return;
  const reg = (await navigator.serviceWorker.getRegistration('/app/')) as unknown as Registration | undefined;
  if (!reg) return;
  for (const n of staleNotices(await reg.getNotifications(), { ships: ships.value, items: inboxItems.value })) n.close();
}

/** Opens a tapped push: a window that was open gets a message from the worker, and a window the tap opened carries the payload in its address until the lists have loaded. Also closes stale notifications whenever the Inbox or ships change. */
export function watchPush(): () => void {
  let pending = pushTargetOf(location.search);
  const onMessage = (e: MessageEvent) => {
    const msg = e.data as { type?: unknown; data?: unknown } | null;
    const target = msg?.type === 'push-click' ? pushTargetOfData(msg.data) : null;
    if (target) open(target, false);
  };
  const sw = 'serviceWorker' in navigator ? navigator.serviceWorker : null;
  sw?.addEventListener('message', onMessage);
  const stop = effect(() => {
    void ships.value;
    void inboxItems.value;
    if (!shipsLoaded.value || !inboxLoaded.value) return;
    if (pending) {
      const target = pending;
      pending = null;
      open(target, true);
    }
    void closeStale().catch(() => {});
  });
  return () => {
    sw?.removeEventListener('message', onMessage);
    stop();
  };
}
