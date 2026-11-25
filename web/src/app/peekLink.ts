import { crewKey, itemKey, mergeKey, rereviewKey, stopKey } from './inbox';
import type { PeekTarget } from './router';

/** The queue key of the entry a Peek path opens. */
export const peekKey = (t: PeekTarget): string => (t.merge ? mergeKey(t.session) : t.rereview ? rereviewKey(t.session) : t.item !== undefined ? itemKey(t.session, t.item) : t.crew !== undefined ? crewKey(t.session, t.crew) : stopKey(t.session));

/** The query for a link into the ship's own fleet, and its Terminal tab when asked. */
export function bridgeSearch(fleetKey: string | undefined, tab?: 'terminal', crew?: string): string {
  const parts = [...(fleetKey ? [`fleet=${encodeURIComponent(fleetKey)}`] : []), ...(tab ? [`tab=${tab}`] : []), ...(crew ? [`crew=${encodeURIComponent(crew)}`] : [])];
  return parts.length > 0 ? `?${parts.join('&')}` : '';
}

/** The crew boat a link names, read once when the ship opens. */
export function crewFromSearch(search: string): string | null {
  return new URLSearchParams(search).get('crew') || null;
}
