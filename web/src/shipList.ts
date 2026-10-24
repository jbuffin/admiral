import type { WireShip } from './ships';

const at = (iso: string | undefined) => Date.parse(iso ?? '') || 0;

// The phone Ships list in order: ships that need you first, the longest waiting first, then every other
// ship, newest first. Ships sailing off are left out; anchored ships stay.
export function sortShips(list: WireShip[]): WireShip[] {
  const shown = list.filter((s) => s.status !== 'sailing_off');
  const asking = shown.filter((s) => s.status === 'needs_you').sort((a, b) => at(a.stop_since) - at(b.stop_since));
  const rest = shown.filter((s) => s.status !== 'needs_you').sort((a, b) => at(b.launched) - at(a.launched));
  return [...asking, ...rest];
}
