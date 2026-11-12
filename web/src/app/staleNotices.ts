import type { Ship } from './api';
import { alertDestination } from './alerts';
import type { InboxItem } from './inbox';
import { pushTargetOfData } from './pushTarget';

/** The shown notifications whose entry is gone, by the payload each carries: an item's tag and a crew boat's have the same shape. */
export function staleNotices<N extends { data: unknown }>(shown: N[], now: { ships: Ship[]; items: InboxItem[] }): N[] {
  return shown.filter((n) => {
    const target = pushTargetOfData(n.data);
    return target !== null && alertDestination(target, now) === '/app/inbox';
  });
}
