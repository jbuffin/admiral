import { call, type Ship } from './api';
import type { LaunchCard } from './chat';
import { shipPath } from './router';

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

const PROFILE = { investigate: 'Investigate', draft_fix: 'Draft fix' } as const;

export function launchCardView(item: { text: string; card: LaunchCard }, o: { ships: Ship[]; undone: boolean }): LaunchCardView {
  const c = item.card;
  const live = o.ships.find((s) => s.session === c.ship);
  const profile = c.restricted ? ` · Restricted · ${PROFILE[c.restricted]}` : '';
  const stays = c.level === 'A3' ? '' : ' Anything the ship already pushed, and any PR it opened, stays.';
  return {
    eyebrow: `${c.routine_name ?? 'Routine'} · ${c.level}${profile} · Untrusted`,
    text: item.text,
    ship: c.ship,
    shipHref: shipPath(c.ship),
    status: live?.status ?? '',
    actions: { stop: !!live && !o.undone, undo: !o.undone, mute: (c.mute_by?.length ?? 0) > 0 },
    undone: o.undone,
    notice: o.undone ? 'Launch undone: the ship was stopped and its local work deleted.' : '',
    undoConfirm: `Stop ${c.ship} and delete its local clone and branch?${stays}`,
  };
}

export const stopShip = (session: string) => call<unknown>('POST', `/ships/${encodeURIComponent(session)}/stop`);

const runPath = (fleet: string, routine: string, trigger: string) => `/fleets/${encodeURIComponent(fleet)}/routines/${encodeURIComponent(routine)}/runs/${encodeURIComponent(trigger)}`;
export const undoLaunch = (fleet: string, routine: string, trigger: string) => call<unknown>('POST', `${runPath(fleet, routine, trigger)}/undo`);

/** Whether the launch record is marked undone, or undefined when it can't be read. */
export async function fetchLaunchUndone(fleet: string, routine: string, trigger: string): Promise<boolean | undefined> {
  const r = await call<{ undone?: boolean }>('GET', runPath(fleet, routine, trigger));
  return r.ok ? !!r.data?.undone : undefined;
}
