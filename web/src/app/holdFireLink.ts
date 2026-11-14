import { effect, signal } from '@preact/signals';
import { call } from './api';
import { activeFleet } from './fleets';
import type { HoldFireState } from './routines';

export const holdFire = signal<{ fleet: string; state: HoldFireState } | null>(null);

let seq = 0;
export async function refreshHoldFire(fleet: string | undefined): Promise<void> {
  if (!fleet) return void (holdFire.value = null);
  const mine = ++seq;
  const r = await call<HoldFireState>('GET', `/fleets/${encodeURIComponent(fleet)}/hold-fire`);
  if (mine === seq && r.ok) holdFire.value = { fleet, state: r.data };
}

/** Turns this fleet's switch, or the all-fleets one, on or off, then reads the state back; the error text when refused. */
export async function setHoldFire(fleet: string, scope: 'fleet' | 'all', on: boolean): Promise<string> {
  const base = scope === 'all' ? '/hold-fire' : `/fleets/${encodeURIComponent(fleet)}/hold-fire`;
  const r = await call('POST', `${base}/${on ? 'on' : 'off'}`);
  await refreshHoldFire(fleet);
  return r.ok ? '' : r.error;
}

/** Keeps the active fleet's Hold fire state fresh: on a fleet change, every 15 s and when the window regains focus. AdmiralBot can turn it on, and no socket message says so. */
export function watchHoldFire() {
  const stop = effect(() => void refreshHoldFire(activeFleet.value?.key));
  const again = () => void refreshHoldFire(activeFleet.peek()?.key);
  const timer = setInterval(again, 15_000);
  addEventListener('focus', again);
  return () => {
    stop();
    clearInterval(timer);
    removeEventListener('focus', again);
  };
}

/** What the last Stop all worker-launched stopped, as the page says it. */
export const stopResult = signal('');

/** Stops this fleet's live worker-launched ships, or every fleet's, then reads the state back; the error text when refused or a ship would not stop. */
export async function stopWorkerShips(fleet: string, scope: 'fleet' | 'all'): Promise<string> {
  const base = scope === 'all' ? '' : `/fleets/${encodeURIComponent(fleet)}`;
  const r = await call<{ stopped: string[]; failed: string[] }>('POST', `${base}/worker-ships/stop`);
  await refreshHoldFire(fleet);
  if (!r.ok) return r.error;
  const n = r.data.stopped.length;
  stopResult.value = `Stopped ${n} ${n === 1 ? 'ship' : 'ships'}`;
  return r.data.failed.length ? `Could not stop ${r.data.failed.join(', ')}.` : '';
}
