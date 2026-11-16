import type { Ship } from './api';

export type HandAction = { show: false } | { show: true; busy: boolean; refusal: string };

/** What a live ship's Hand to Commodore action looks like: absent, busy, or shown with the reason it is disabled. */
export function handAction(ship: Ship): HandAction {
  if (ship.status === 'moored' || ship.status === 'anchored' || ship.status === 'sailing_off' || ship.returning || ship.skill_kind === 'commodore') return { show: false };
  return { show: true, busy: !!ship.handing, refusal: ship.hand_refusal ?? '' };
}

/** The line under the actions while the last hand failed on a live ship. */
export function handFailure(ship: Ship): string {
  if (ship.handing || ship.status === 'anchored' || ship.status === 'sailing_off') return '';
  return ship.hand_failed ?? '';
}

/** The body the route takes: the GitHub issue, or the engineer's own words. */
export function handBody(kind: 'issue' | 'other', text: string) {
  return kind === 'issue' ? { kind } : { kind, text: text.trim() };
}
