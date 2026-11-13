import { go, standingOrdersPath } from './router';
import { fleetSearch } from './fleets';

/** The Routines screen's two tabs; each has its own URL. */
export function RoutineTabs({ current }: { current: 'routines' | 'standing-orders' }) {
  const tab = (id: 'routines' | 'standing-orders', label: string, path: string) => (
    <button type="button" role="tab" class="view-tab" aria-selected={current === id} onClick={() => current !== id && go(path, fleetSearch.value)}>
      {label}
    </button>
  );
  return (
    <div role="tablist" class="view-tabs" aria-label="Routines views">
      {tab('routines', 'Routines', '/app/routines')}
      {tab('standing-orders', 'Standing orders', standingOrdersPath)}
    </div>
  );
}
