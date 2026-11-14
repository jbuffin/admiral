import { useState } from 'preact/hooks';
import { call, type Ship } from './api';
import { Actions, Button } from './Button';
import { reviews, rowButtons } from './moored';

/** A moored ship's buttons. On the Ships row that is Wake, Dismiss and Don't follow reviews; in the PR tab (`tab`) only Don't follow reviews, since the view's head holds Wake. */
export function MooredActions({ ship, onError, tab = false, locked = false, onBusy }: { ship: Ship; onError: (e: string) => void; tab?: boolean; locked?: boolean; onBusy?: (busy: boolean) => void }) {
  // Set once a request succeeds: the buttons stay off until the ships push removes the row, so a second tap cannot hit a 404.
  const [sent, setSent] = useState('');
  const act = async (route: string) => {
    onBusy?.(true);
    onError('');
    const res = await call('POST', `/ships/${encodeURIComponent(ship.session)}/${route}`);
    if (res.ok) return setSent(route);
    onError(res.error);
    onBusy?.(false);
  };
  const off = rowButtons(ship, sent !== '' || locked);
  const key = (verb: string) => ['ship', ship.session, verb] as const;
  return (
    <Actions class="ship-actions">
      {tab ? null : <Button label="Wake" busyLabel="Waking" busy={sent === 'wake'} pending={key('wake')} disabled={off.wake} onClick={() => act('wake')} />}
      {tab ? null : <Button label="Dismiss" pending={key('dismiss')} disabled={off.others} onClick={() => act('dismiss')} />}
      {reviews(ship) ? null : <Button label="Don't follow reviews" pending={key('unfollow')} disabled={off.others} onClick={() => act('unfollow')} />}
    </Actions>
  );
}
