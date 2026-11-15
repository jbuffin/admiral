import { useState } from 'preact/hooks';
import { call, type Ship } from './api';
import { Actions, Button } from './Button';
import { Sheet, useCancelFocus } from './Sheet';
import { fixItems, fixLabel } from './prTab';

/** The confirm behind Fix PR and Fix stack, from the PR tab and the order row: it lists each PR's problems, shows the server's command word for word and starts the round on confirm. `onBusy` follows the call; after a success it stays true until the ships push removes the action. */
export function FixSheet({ ship, onClose, onBusy }: { ship: Ship; onClose: () => void; onBusy: (busy: boolean) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useCancelFocus(true);
  const label = fixLabel(ship);
  const run = async () => {
    setBusy(true);
    onBusy(true);
    setError('');
    const r = await call('POST', `/ships/${encodeURIComponent(ship.session)}/fix`);
    if (!r.ok) {
      setBusy(false);
      onBusy(false);
      return setError(r.error);
    }
    onClose();
  };
  return (
    <Sheet label={label} kind="launch-backdrop stop-backdrop" onClose={() => !busy && onClose()}>
      <p class="line">Wakes {ship.name} under its name to clear what keeps {label === 'Fix PR' ? 'the PR' : 'the PRs'} from merging. It answers any feedback waiting on its PRs in the same round.</p>
      <ul class="line">
        {fixItems(ship.fix).map((t) => (
          <li key={t}>{t}</li>
        ))}
      </ul>
      <p class="cmd-block mono">{ship.fix?.command}</p>
      {error ? (
        <p class="launch-error" role="alert">
          {error}
        </p>
      ) : null}
      <Actions class="launch-foot">
        <Button label="Cancel" disabled={busy} onClick={onClose} />
        <Button label={label} variant="primary" pending={['ship', ship.session, 'fix']} disabled={busy} onClick={run} />
      </Actions>
    </Sheet>
  );
}

/** The Fix PR / Fix stack button of a ship with a fix on offer, and the sheet it opens. */
export function FixButton({ ship, locked, onBusy }: { ship: Ship; locked: boolean; onBusy: (busy: boolean) => void }) {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  return (
    <>
      <Button label={fixLabel(ship)} variant="primary" pending={['ship', ship.session, 'fix']} disabled={locked || busy} onClick={() => setAsking(true)} />
      {asking ? (
        <FixSheet
          ship={ship}
          onClose={() => setAsking(false)}
          onBusy={(b) => {
            setBusy(b);
            onBusy(b);
          }}
        />
      ) : null}
    </>
  );
}
