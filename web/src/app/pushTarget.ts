import type { Alert } from './alerts';

export type PushTarget = Pick<Alert, 'target' | 'fleet' | 'item' | 'crew' | 'merge' | 'routine' | 'message'> & { session?: string };

/** The target fields of a tapped push's payload, text only; null when it names no ship, routine or chat message. */
export function pushTargetOfData(raw: unknown): PushTarget | null {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null;
  const d = raw as Record<string, unknown>;
  const routine = d.target === 'routine' && typeof d.routine === 'string' && d.routine !== '';
  const chat = d.target === 'chat' && typeof d.fleet === 'string' && d.fleet !== '' && typeof d.message === 'string' && d.message !== '';
  if (d.target === 'chat' && !chat) return null;
  if (!routine && !chat && (typeof d.session !== 'string' || d.session === '')) return null;
  const out: PushTarget = routine || chat ? {} : { session: d.session as string };
  for (const k of ['target', 'fleet', 'item', 'crew', 'routine', 'message'] as const) if (typeof d[k] === 'string') out[k] = d[k] as string;
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
