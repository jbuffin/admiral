import type { Ship, ShipStatus } from './api';

export interface ShipGroup {
  status: Exclude<ShipStatus, 'sailing_off'>;
  ships: Ship[];
}

export function groupShips(_ships: Ship[], _fleetKey: string): ShipGroup[] {
  return [];
}
