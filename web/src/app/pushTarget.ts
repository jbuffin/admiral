import type { Alert } from './alerts';

export type PushTarget = Pick<Alert, 'target' | 'fleet' | 'item' | 'crew' | 'merge' | 'routine'> & { session?: string };

/** The target fields of a tapped push's payload, text only; null when it names no ship or routine. */
export function pushTargetOfData(raw: unknown): PushTarget | null {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null;
  const d = raw as Record<string, unknown>;
  const routine = d.target === 'routine' && typeof d.routine === 'string' && d.routine !== '';
  if (!routine && (typeof d.session !== 'string' || d.session === '')) return null;
  const out: PushTarget = routine ? {} : { session: d.session as string };
  for (const k of ['target', 'fleet', 'item', 'crew', 'routine'] as const) if (typeof d[k] === 'string') out[k] = d[k] as string;
  if (d.merge === true) out.merge = true;
  return out;
}

/** What the worker's click URL carried in the address: the same payload fields the tapped notification held. */
export function pushTargetOf(search: string): PushTarget | null {
  const raw = new URLSearchParams(search).get('push');
  if (raw === null) return null;
  try {
    return pushTargetOfData(JSON.parse(raw));
  } catch {
    return null;
  }
}
