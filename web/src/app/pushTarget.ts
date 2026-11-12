import type { Alert } from './alerts';

export type PushTarget = Pick<Alert, 'target' | 'fleet' | 'session' | 'item' | 'crew' | 'merge' | 'routine'>;

/** The target fields of a tapped push's payload, text only; null when it names no ship. */
export function pushTargetOfData(raw: unknown): PushTarget | null {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null;
  const d = raw as Record<string, unknown>;
  if (typeof d.session !== 'string' || d.session === '') return null;
  const out: PushTarget = { session: d.session };
  for (const k of ['target', 'fleet', 'item', 'crew'] as const) if (typeof d[k] === 'string') out[k] = d[k] as string;
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
