import type { Ship, ShipStatus } from './api';

export type VisibleStatus = Exclude<ShipStatus, 'sailing_off'>;

export interface ShipGroup {
  status: VisibleStatus;
  ships: Ship[];
}

export const GROUP_TITLES: Record<VisibleStatus, string> = {
  needs_you: 'Needs you',
  underway: 'Underway',
  anchored: 'Anchored',
};

const ORDER: VisibleStatus[] = ['needs_you', 'underway', 'anchored'];

export function groupShips(ships: Ship[], fleetKey: string): ShipGroup[] {
  const mine = ships.filter((s) => s.fleet === fleetKey);
  return ORDER.map((status) => ({ status, ships: mine.filter((s) => s.status === status) })).filter((g) => g.ships.length > 0);
}

/** "now", then 12m, 3h, 2d; blank when the launch time is missing or unreadable. */
export function shipAge(launched: string, now: number): string {
  const at = Date.parse(launched);
  if (Number.isNaN(at)) return '';
  const min = Math.max(0, Math.floor((now - at) / 60000));
  if (min < 1) return 'now';
  if (min < 60) return `${min}m`;
  if (min < 24 * 60) return `${Math.floor(min / 60)}h`;
  return `${Math.floor(min / 1440)}d`;
}
