import type { Fleet } from './api';
import { FLEET_COUNT } from './shell-tokens';

/** Fill and the ink on it. An indexed fleet takes the redesign's pair; a custom hex keeps its own. */
export function fleetPaint(f: Fleet): { fill: string; ink: string } {
  if (f.color_index == null) return { fill: f.color, ink: f.ink };
  const n = ((f.color_index % FLEET_COUNT) + FLEET_COUNT) % FLEET_COUNT;
  return { fill: `var(--fleet-${n})`, ink: `var(--fleet-${n}-ink)` };
}
