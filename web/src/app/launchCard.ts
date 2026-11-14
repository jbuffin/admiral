import { call, type Ship } from './api';
import type { LaunchCard } from './chat';
import { wallClockNote } from './messageBox';
import { shipPath } from './router';

export interface LaunchCardView {
  eyebrow: string;
  text: string;
  ship: string;
  shipHref: string;
  /** The live ship's status, empty when the host no longer lists it. */
  status: string;
  actions: { stop: boolean; undo: boolean; mute: boolean; promote: boolean };
  undone: boolean;
  notice: string;
  undoConfirm: string;
  promoteConfirm: string;
  /** The normal ship Promote started, empty until then. */
  promotedShip: string;
  promotedHref: string;
}

export interface LaunchState { undone: boolean; promotedShip: string }

const PROFILE = { investigate: 'Investigate', draft_fix: 'Draft fix' } as const;

export function launchCardView(item: { text: string; card: LaunchCard }, o: { ships: Ship[]; undone: boolean; promotedShip?: string }): LaunchCardView {
  const c = item.card;
  const live = o.ships.find((s) => s.session === c.ship);
  const promoted = o.promotedShip ?? '';
  const profile = c.restricted ? ` · Restricted · ${PROFILE[c.restricted]}` : '';
  const stays = c.level === 'A3' ? '' : ' Anything the ship already pushed, and any PR it opened, stays.';
  return {
    eyebrow: `${c.routine_name ?? 'Routine'} · ${c.level}${profile} · Untrusted`,
    text: item.text,
    ship: c.ship,
    shipHref: shipPath(c.ship),
    status: live ? wallClockNote(live) || live.status : '',
    actions: { stop: !!live && !o.undone, undo: !o.undone && !promoted, mute: (c.mute_by?.length ?? 0) > 0, promote: c.restricted === 'draft_fix' && !o.undone && !promoted },
    undone: o.undone,
    notice: o.undone ? 'Launch undone: the ship was stopped and its local work deleted.' : promoted ? `Promoted to ${promoted}` : '',
    undoConfirm: `Stop ${c.ship} and delete its local clone and branch?${stays}`,
    promoteConfirm: 'Stops the restricted ship if it’s running and starts a normal ship from its work. That ship can push and open PRs.',
    promotedShip: promoted,
    promotedHref: promoted ? shipPath(promoted) : '',
  };
}

export const stopShip = (session: string) => call<unknown>('POST', `/ships/${encodeURIComponent(session)}/stop`);

const runPath = (fleet: string, routine: string, trigger: string) => `/fleets/${encodeURIComponent(fleet)}/routines/${encodeURIComponent(routine)}/runs/${encodeURIComponent(trigger)}`;
export const undoLaunch = (fleet: string, routine: string, trigger: string) => call<unknown>('POST', `${runPath(fleet, routine, trigger)}/undo`);

/** The launch record's undone and promoted state, or undefined when it can't be read. */
export async function fetchLaunchState(fleet: string, routine: string, trigger: string): Promise<LaunchState | undefined> {
  const r = await call<{ undone?: boolean; promoted_ship?: string }>('GET', runPath(fleet, routine, trigger));
  return r.ok ? { undone: !!r.data?.undone, promotedShip: r.data?.promoted_ship ?? '' } : undefined;
}

export const promoteLaunch = (fleet: string, routine: string, trigger: string) => call<{ promoted_ship?: string }>('POST', `${runPath(fleet, routine, trigger)}/promote`);
