import { useState } from 'preact/hooks';
import { call, type Ship } from './api';
import { reviews, rowButtons } from './moored';

/** A moored ship's buttons. On the Ships row that is Wake, Dismiss and Don't follow reviews; in the PR tab (`tab`) only Don't follow reviews, since the view's head holds Wake. */
export function MooredActions({ ship, onError, tab = false, locked = false, onBusy }: { ship: Ship; onError: (e: string) => void; tab?: boolean; locked?: boolean; onBusy?: (busy: boolean) => void }) {
  const [busy, setBusy] = useState('');
  const act = async (route: string) => {
    setBusy(route);
    onBusy?.(true);
    onError('');
    const res = await call('POST', `/ships/${encodeURIComponent(ship.session)}/${route}`);
    // On success the buttons stay off until the ships push removes the row, so a second tap cannot hit a 404.
    if (!res.ok) {
      onError(res.error);
      setBusy('');
      onBusy?.(false);
    }
  };
  const off = rowButtons(ship, busy !== '' || locked);
  return (
    <div class="ship-actions">
      {tab ? null : (
        <button type="button" class="btn" disabled={off.wake} onClick={() => act('wake')}>
          Wake
        </button>
      )}
      {tab ? null : (
        <button type="button" class="btn" disabled={off.others} onClick={() => act('dismiss')}>
          Dismiss
        </button>
      )}
      {reviews(ship) ? null : (
        <button type="button" class="btn" disabled={off.others} onClick={() => act('unfollow')}>
          Don't follow reviews
        </button>
      )}
    </div>
  );
}
