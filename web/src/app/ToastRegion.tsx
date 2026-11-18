import { signal } from '@preact/signals';
import { useEffect } from 'preact/hooks';
import { chime, muted } from '../chime';
import { alertDestination, alertSearch, createAlertHandler, type Alert } from './alerts';
import { go } from './router';
import { inboxItems, onShipsMessage, ships } from './shipsLink';
import { createToasts, type Toast } from './toasts';

const isWebUrl = (u?: string) => !!u && /^https?:\/\//i.test(u);

const queue = createToasts<Alert>({ now: Date.now });
const shown = signal<Toast<Alert>[]>([]);
const sync = () => (shown.value = queue.list());
const handle = createAlertHandler({ toasts: queue, chime, muted: () => muted.value });

function open(t: Toast<Alert>) {
  queue.dismiss(t.id);
  sync();
  go(alertDestination(t.alert, { ships: ships.value, items: inboxItems.value }), alertSearch(t.alert));
}

export interface ResultToast {
  tag: string;
  name: string;
  line: string;
  url?: string;
  error?: boolean;
}

/** Shows a module action's result as a toast that stays until dismissed. */
export function showResult(r: ResultToast) {
  queue.pushResult({ tag: r.tag, name: r.name, line: r.line, url: r.url, error: r.error } as unknown as Alert);
  sync();
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
      // The queue is module state, so a region that goes takes its toasts, which a result toast would otherwise outlive.
      for (const t of queue.list()) queue.dismiss(t.id);
      sync();
    };
  }, []);
  return (
    <div class="toasts" role="status" aria-live="polite">
      {shown.value.map((t) => (
        <div class={t.sticky ? 'toast result' : 'toast'} key={t.id} data-error={(t.alert as { error?: boolean }).error ? 'true' : undefined}>
          {t.sticky ? (
            <div class="toast-text">
              <span class="toast-name">{t.name}</span>
              <span class="toast-line">{t.line}</span>
              {isWebUrl(t.url) ? (
                <a class="toast-link" href={t.url} target="_blank" rel="noopener noreferrer">
                  {t.url}
                </a>
              ) : null}
            </div>
          ) : (
            <button type="button" class="toast-body" onClick={() => open(t)}>
              <span class="toast-name">{t.name}</span>
              <span class="toast-line">{t.line}</span>
            </button>
          )}
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
