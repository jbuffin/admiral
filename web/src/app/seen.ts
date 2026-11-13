import { useEffect, useRef } from 'preact/hooks';
import { call, type Ship } from './api';
import { ships } from './shipsLink';

/** The start of the stop the ship needs you for itself and that nobody has looked at yet. */
export const unseenStop = (s: Ship | undefined): string | undefined =>
  s?.status === 'needs_you' && s.reason !== 'crew' && !s.seen ? s.stop_since : undefined;

/** Marks the ship's latest stop seen when the view showing it closes. */
export function useSeenOnLeave(ship: Ship | undefined): void {
  const last = useRef<{ session: string; since: string } | null>(null);
  const since = unseenStop(ship);
  if (ship && since) last.current = { session: ship.session, since };
  const session = ship?.session;
  useEffect(
    () => () => {
      const l = last.current;
      // A stop answered, or a ship gone, while the view was open has nothing left to mark.
      if (l && l.session === session && unseenStop(ships.value.find((s) => s.session === l.session)) === l.since) void call('POST', `/ships/${encodeURIComponent(l.session)}/seen`, { stop_since: l.since });
    },
    [session],
  );
}
