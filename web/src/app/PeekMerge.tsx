import { useRef, useState } from 'preact/hooks';
import { call, mergeShip, type Ship } from './api';
import { MooredPRs } from './MooredPRs';
import { mergeHint } from './moored';
import { Failure } from './PeekItem';

const STALE_TOKEN = 'Admiral restarted, reload to merge';

/** Merge for a ready ship, one PR or a whole stack, with the method it will use. The caller shows it only when the ship is ready, and shows the error it reports. */
export function MergeControl({ ship, onMerged, onError, locked = false, onBusy }: { ship: Ship; onMerged: () => void; onError: (e: string) => void; locked?: boolean; onBusy?: (busy: boolean) => void }) {
  const [state, setState] = useState({ busy: false, stale: false });
  const busy = useRef(false);
  const hint = mergeHint(ship.merge_method, (ship.prs ?? []).length > 1);
  const merge = async () => {
    if (busy.current) return;
    busy.current = true;
    setState({ busy: true, stale: false });
    onBusy?.(true);
    onError('');
    const r = await mergeShip(ship.session);
    busy.current = false;
    onBusy?.(false);
    if (r.ok) {
      void call('POST', '/moored/refresh');
      return onMerged();
    }
    const stale = r.status === 403;
    setState({ busy: false, stale });
    onError(stale ? STALE_TOKEN : r.error);
  };
  return (
    <div class="peek-merge">
      <button type="button" class="btn primary" disabled={state.busy || locked} onClick={() => void merge()}>
        Merge
      </button>
      {hint ? <span class="merge-hint">{hint}</span> : null}
      {state.stale ? (
        <button type="button" class="btn" onClick={() => location.reload()}>
          Reload
        </button>
      ) : null}
    </div>
  );
}

/** The PRs of a ready ship and its Merge button. */
export function MergeBody({ ship, fold }: { ship: Ship; fold: (text: string) => void }) {
  const [error, setError] = useState('');
  return (
    <div class="peek-form">
      <div class="moored-info">
        <MooredPRs ship={ship} now={Date.now()} />
      </div>
      <MergeControl ship={ship} onMerged={() => fold('Merged')} onError={setError} />
      <Failure message={error} />
    </div>
  );
}
