// Imported only by sw.ts: the worker is one classic file, so nothing here may import a module the app also uses.

const TEXT_FIELDS = ['name', 'line', 'tag', 'fleet', 'fleet_name', 'target', 'session', 'item', 'crew', 'routine'] as const;

type PushData = Partial<Record<(typeof TEXT_FIELDS)[number], string>> & { merge?: true };

export interface Notice {
  title: string;
  options: { body: string; tag?: string; icon: string; data: PushData };
}

const ICON = '/icons/icon-192.png';

/** Every field is text and nothing else: the payload is the Mac's, but session, item and crew come from files and sessions it does not clean. */
export function dataOf(raw: unknown): PushData {
  const out: PushData = {};
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return out;
  const fields = raw as Record<string, unknown>;
  for (const k of TEXT_FIELDS) if (typeof fields[k] === 'string') out[k] = fields[k] as string;
  if (fields.merge === true) out.merge = true;
  return out;
}

/** The notification for a push body. It always yields one: a browser that gets pushes it does not show drops the subscription. */
export function notice(raw: string | null): Notice {
  let parsed: unknown = null;
  try {
    parsed = raw ? JSON.parse(raw) : null;
  } catch {
    /* shown as the generic line */
  }
  const data = dataOf(parsed);
  const title = data.name ? (data.fleet_name ? `${data.name} · ${data.fleet_name}` : data.name) : 'Admiral';
  return { title, options: { body: data.line ?? 'Something needs you.', ...(data.tag ? { tag: data.tag } : {}), icon: ICON, data } };
}

/** Where a tap opens when no window is open: always the shell, carrying the payload for the page to resolve once it has loaded. */
export function clickUrl(data: unknown): string {
  const fields = dataOf(data);
  return Object.keys(fields).length === 0 ? '/app/' : `/app/?push=${encodeURIComponent(JSON.stringify(fields))}`;
}
