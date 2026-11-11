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
