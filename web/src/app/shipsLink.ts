import { signal } from '@preact/signals';
import type { Ship } from './api';

export const ships = signal<Ship[]>([]);
export const shipsLink = signal<'connecting' | 'open' | 'reconnecting' | 'signed-out'>('connecting');

export function watchShips(): () => void {
  return () => {};
}
