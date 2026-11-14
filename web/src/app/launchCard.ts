import { call, type Ship } from './api';
import type { LaunchCard } from './chat';

export interface LaunchCardView {
  eyebrow: string;
  text: string;
  ship: string;
  shipHref: string;
  /** The live ship's status, empty when the host no longer lists it. */
  status: string;
  actions: { stop: boolean; undo: boolean; mute: boolean };
  undone: boolean;
  notice: string;
  undoConfirm: string;
}

export function launchCardView(_item: { text: string; card: LaunchCard }, _o: { ships: Ship[]; undone: boolean }): LaunchCardView {
  return { eyebrow: '', text: '', ship: '', shipHref: '', status: '', actions: { stop: false, undo: false, mute: false }, undone: false, notice: '', undoConfirm: '' };
}

export const stopShip = (_session: string) => call<unknown>('GET', '/not-built');
export const undoLaunch = (_fleet: string, _routine: string, _trigger: string) => call<unknown>('GET', '/not-built');
export async function fetchLaunchUndone(_fleet: string, _routine: string, _trigger: string): Promise<boolean | undefined> {
  return undefined;
}
