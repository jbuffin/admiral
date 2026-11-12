import { useRef, useState } from 'preact/hooks';
import { call, mergeShip, type Ship } from './api';
import { MooredPRs } from './MooredPRs';
import { mergeHint } from './moored';
import { Failure } from './PeekItem';

const STALE_TOKEN = 'Admiral restarted, reload to merge';

/** The PRs of a ready ship and its Merge button; a stack is merged by hand, since Admiral merges one PR. */
export function MergeBody({ ship, fold }: { ship: Ship; fold: (text: string) => void }) {
  const [state, setState] = useState({ busy: false, error: '', stale: false });
  const busy = useRef(false);
  const stack = (ship.prs ?? []).length > 1;
  const merge = async () => {
    if (busy.current) return;
    busy.current = true;
    setState({ busy: true, error: '', stale: false });
    const r = await mergeShip(ship.session);
    busy.current = false;
    if (r.ok) {
      void call('POST', '/moored/refresh');
      return fold('Merged');
    }
    const stale = r.status === 403;
    setState({ busy: false, error: stale ? STALE_TOKEN : r.error, stale });
  };
  return (
    <div class="peek-form">
      <div class="moored-info">
        <MooredPRs ship={ship} now={Date.now()} />
      </div>
      {stack ? (
        <p class="line">This is a stack: merge it with gh stack in a terminal.</p>
      ) : (
        <div class="peek-merge">
          <button type="button" class="btn primary" disabled={state.busy} onClick={() => void merge()}>
            Merge
          </button>
          {mergeHint(ship.merge_method) ? <span class="merge-hint">{mergeHint(ship.merge_method)}</span> : null}
          {state.stale ? (
            <button type="button" class="btn" onClick={() => location.reload()}>
              Reload
            </button>
          ) : null}
        </div>
      )}
      <Failure message={state.error} />
    </div>
  );
}
