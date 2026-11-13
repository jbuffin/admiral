import type { BoxState } from './messageBox';
import type { CrewBoat, Ship } from './api';
import { shipUrl } from './api';
import { bridgePath } from '../crew';

export type ChipState = 'running' | 'finished' | 'error';

/** The role a boat was launched with, and Teammate when it has none, as the old UI reads it. */
export const chipLabel = (b: Pick<CrewBoat, 'role'>): string => b.role || 'Teammate';

// A boat anchors whatever its exit code, so only the code tells a finished boat from a failed one.
export function chipState(b: Pick<CrewBoat, 'status' | 'exit_code' | 'exit_signal'>): ChipState {
  if (b.status !== 'anchored') return 'running';
  return b.exit_code === 0 ? 'finished' : 'error';
}

export interface Chip {
  crew: string | null;
  label: string;
  state: 'ship' | ChipState;
}

/** The ship's chip first, then one per boat in the order the ship lists them. */
export function flotilla(ship: Pick<Ship, 'name' | 'crew'>): Chip[] {
  return [{ crew: null, label: ship.name, state: 'ship' }, ...ship.crew.map((b): Chip => ({ crew: b.name, label: chipLabel(b), state: chipState(b) }))];
}

export function boatBoxState(b: Pick<CrewBoat, 'status' | 'exit_code' | 'exit_signal'>): BoxState {
  const state = chipState(b);
  if (state === 'running') return { enabled: true };
  if (state === 'finished') return { enabled: false, note: 'Finished, read only' };
  if (b.exit_code === null && b.exit_signal) return { enabled: false, note: `Killed (${b.exit_signal}), read only` };
  return { enabled: false, note: 'Exited with error, read only' };
}

/** Where a boat's conversation, terminal, messages and Interrupt live; the ship's when no boat is named. */
export function boatRoutes(session: string, crew?: string) {
  const base = shipUrl(session, crew);
  return { conversation: `${base}/conversation`, bridge: bridgePath(session, crew, '/ws/v1'), messages: `${base}/messages`, interrupt: `${base}/interrupt`, skills: `${base}/skills` };
}
