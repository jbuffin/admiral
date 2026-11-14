import { useEffect, useRef, useState } from 'preact/hooks';
import { Button } from './Button';
import { call, fetchStackShape, mergeShip, type Ship, type StackShape } from './api';
import { fleetSearch } from './fleets';
import { MooredPRs } from './MooredPRs';
import { mergeHint } from './moored';
import { Failure } from './PeekItem';
import { go, shipPath } from './router';

const STALE_TOKEN = 'Admiral restarted, reload to merge';

/** Merge for a ready ship with the method it will use: the ship's one PR, its whole stack (`stack`), or with `pr` that independent PR alone. The caller shows it only when the ship is ready, and shows the error it reports. */
export function MergeControl({ ship, pr, stack = false, onMerged, onError, locked = false, onBusy }: { ship: Ship; pr?: string; stack?: boolean; onMerged: () => void; onError: (e: string) => void; locked?: boolean; onBusy?: (busy: boolean) => void }) {
  const [stale, setStale] = useState(false);
  const busy = useRef(false);
  const hint = mergeHint(ship.merge_method, stack);
  const merge = async () => {
    if (busy.current) return;
    busy.current = true;
    setStale(false);
    onBusy?.(true);
    onError('');
    const r = await mergeShip(ship.session, pr);
    busy.current = false;
    onBusy?.(false);
    if (r.ok) {
      void call('POST', '/moored/refresh');
      return onMerged();
    }
    const refused = r.status === 403;
    setStale(refused);
    onError(refused ? STALE_TOKEN : r.error);
  };
  return (
    <div class="peek-merge">
      <Button label="Merge" variant="primary" pending={['ship', ship.session, 'merge', pr ?? '']} disabled={locked} onClick={merge} />
      {hint ? <span class="merge-hint">{hint}</span> : null}
      {stale ? <Button label="Reload" onClick={() => location.reload()} /> : null}
    </div>
  );
}

/** The ship's shape, read once when a ship of two or more PRs shows ready; null until read, or when the read fails. */
function useStackShape(ship: Ship): StackShape | null {
  const [shape, setShape] = useState<StackShape | null>(null);
  const many = (ship.prs ?? []).length > 1 && !!ship.stack_ready;
  useEffect(() => {
    if (!many) return;
    let live = true;
    void fetchStackShape(ship.session).then((r) => live && r.ok && setShape(r.data));
    return () => {
      live = false;
    };
  }, [ship.session, many]);
  return shape;
}

const PR_TAB_LINK: Record<string, string> = {
  independent: "Merge each PR from the ship's PR tab",
  chained: "Stack these PRs from the ship's PR tab",
};

/** Merge for a ready ship outside its PR tab: a lone PR or a GitHub stack merges here, any other ship of several PRs is merged from the PR tab, which this points to. */
export function ShipMerge({ ship, onMerged, onError }: { ship: Ship; onMerged: () => void; onError: (e: string) => void }) {
  const shape = useStackShape(ship);
  if ((ship.prs ?? []).length < 2) return <MergeControl ship={ship} onMerged={onMerged} onError={onError} />;
  if (!shape) return null;
  if (shape.shape === 'stacked') return <MergeControl ship={ship} stack onMerged={onMerged} onError={onError} />;
  const search = `${fleetSearch.value}${fleetSearch.value ? '&' : '?'}tab=pr`;
  return (
    <Button
      label={PR_TAB_LINK[shape.shape] ?? "Open the ship's PR tab"}
      href={shipPath(ship.session) + search}
      onClick={(e) => {
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        e.preventDefault();
        go(shipPath(ship.session), search);
      }}
    />
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
      <ShipMerge ship={ship} onMerged={() => fold('Merged')} onError={setError} />
      <Failure message={error} />
    </div>
  );
}
