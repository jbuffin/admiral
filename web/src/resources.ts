import { signal } from '@preact/signals';
import { onMessage } from './connection';
import type { Source } from './sources';

// What the `resources` message carries (internal/resources).
export type Holder = { kind: 'ship'; session: string } | { kind: 'lock'; label: string };

export interface Resource {
  name: string;
  capacity: number;
  in_use: number;
  holders: Holder[];
  check: 'ok' | 'missing' | 'pending' | 'none';
}

export const resources = signal<Resource[]>([]);

onMessage('resources', (msg: { resources: Resource[] }) => (resources.value = msg.resources));

// `in_use` can read capacity + 1 right after a launch, and stays above it after a config lowers the capacity.
export const chipText = (r: Resource) => `${r.name} ${Math.min(r.in_use, r.capacity)}/${r.capacity}`;

const units = (n: number) => `${n} ${n === 1 ? 'unit' : 'units'}`;

// The chip's accessible label and tooltip. `shipName` turns a holder's session into the ship's name.
// A launch in flight holds a unit with no holder, so the holders listed may be fewer than `in_use`.
export function chipLabel(r: Resource, shipName: (session: string) => string): string {
  if (r.check === 'missing') return `${r.name}: missing (its check failed)`;
  if (r.check === 'pending') return `${r.name}: checking`;
  if (r.in_use === 0) return `${r.name}: free`;
  const held = r.holders.map((h) => (h.kind === 'ship' ? shipName(h.session) : h.label));
  const of = `${r.name}: ${Math.min(r.in_use, r.capacity)} of ${units(r.capacity)} in use`;
  return held.length > 0 ? `${of}, held by ${held.join(', ')}` : of;
}

// The line Launch orders shows for an order whose panel (autopilot.resources) needs resources: the first one that is
// missing, or has no free unit. A resource still being checked, or not known, says nothing.
export function launchHint(sources: Source[] | null, sourceKey: string, panelId: string | undefined, all: Resource[]): string | null {
  const needs = sources?.find((s) => s.key === sourceKey)?.panels.find((p) => p.id === panelId)?.autopilot?.resources ?? [];
  for (const name of needs) {
    const r = all.find((x) => x.name === name);
    if (!r || r.check === 'pending') continue;
    if (r.check === 'missing') return `${name} is missing`;
    if (r.in_use >= r.capacity) return `${name} is in use`;
  }
  return null;
}

