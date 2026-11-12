import { signal } from '@preact/signals';
import { useEffect } from 'preact/hooks';
import { chime, muted } from '../chime';
import { alertDestination, createAlertHandler, type Alert } from './alerts';
import { fleetSearch } from './fleets';
import { go } from './router';
import { inboxItems, onShipsMessage, ships } from './shipsLink';
import { createToasts, type Toast } from './toasts';

const queue = createToasts<Alert>({ now: Date.now });
const shown = signal<Toast<Alert>[]>([]);
const sync = () => (shown.value = queue.list());
const handle = createAlertHandler({ toasts: queue, chime, muted: () => muted.value });

function open(t: Toast<Alert>) {
  queue.dismiss(t.id);
  sync();
  go(alertDestination(t.alert, { ships: ships.value, items: inboxItems.value }), fleetSearch.value);
}

/** Shows the server's alerts as toasts over the shell and chimes for each, and only for an alert. */
export function ToastRegion() {
  useEffect(() => {
    const off = onShipsMessage((msg) => {
      handle(msg);
      sync();
    });
    const tick = setInterval(() => {
      const before = queue.list().length;
      queue.expire();
      if (queue.list().length !== before) sync();
    }, 500);
    return () => {
      off();
      clearInterval(tick);
    };
  }, []);
  return (
    <div class="toasts" role="status" aria-live="polite">
      {shown.value.map((t) => (
        <div class="toast" key={t.id}>
          <button type="button" class="toast-body" onClick={() => open(t)}>
            <span class="toast-name">{t.name}</span>
            <span class="toast-line">{t.line}</span>
          </button>
          <button
            type="button"
            class="toast-dismiss"
            aria-label="Dismiss"
            onClick={() => {
              queue.dismiss(t.id);
              sync();
            }}
          >
            ×
          </button>
        </div>
      ))}
    </div>
  );
}
