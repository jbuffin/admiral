import { useState } from 'preact/hooks';
import { call, type MooredPR, type Ship } from './api';
import { Actions, Button } from './Button';
import { Sheet, useCancelFocus } from './Sheet';
import { prNumber } from './prTab';

/** The confirm behind Answer review and the order row's Wake: it shows the exact command and starts the round on confirm. `onBusy` follows the call; after a success it stays true until the ships push removes the action. */
export function AnswerReviewSheet({ ship, pr, onClose, onBusy }: { ship: Ship; pr: MooredPR; onClose: () => void; onBusy: (busy: boolean) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useCancelFocus(true);
  const run = async () => {
    setBusy(true);
    onBusy(true);
    setError('');
    const r = await call('POST', `/ships/${encodeURIComponent(ship.session)}/answer-review`, { pr: pr.url });
    if (!r.ok) {
      setBusy(false);
      onBusy(false);
      return setError(r.error);
    }
    onClose();
  };
  return (
    <Sheet label="Answer review" kind="launch-backdrop stop-backdrop" onClose={() => !busy && onClose()}>
      <p class="line">Starts a feedback round on #{prNumber(pr.url)} in the same Commodore run.</p>
      <p class="cmd-block mono">{pr.answer}</p>
      {error ? (
        <p class="launch-error" role="alert">
          {error}
        </p>
      ) : null}
      <Actions class="launch-foot">
        <Button label="Cancel" disabled={busy} onClick={onClose} />
        <Button label="Answer review" variant="primary" pending={['ship', ship.session, 'answer-review', pr.url]} disabled={busy} onClick={run} />
      </Actions>
    </Sheet>
  );
}
