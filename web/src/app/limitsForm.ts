export type Level = 'A0' | 'A1' | 'A3' | 'A4' | 'A5';
export type Restricted = 'investigate' | 'draft_fix';

export interface Threshold {
  events: number;
  days: number;
  level: Level;
  restricted?: Restricted;
}

export interface Ceiling {
  level: Level;
  restricted?: Restricted;
  asks_first?: boolean;
  threshold?: Threshold | null;
}

export interface Scoped {
  value: string;
  routine?: string;
}

export interface Window {
  start: string;
  end: string;
  weekdays: string[];
}

export interface Limits {
  ceilings: { t1: Ceiling; t2: Ceiling; t3: Ceiling; t4: Ceiling; fleet: Ceiling };
  sender_allowlist: string[];
  rates: { launches_per_hour: number; launches_per_sender_per_day: number; concurrent: number };
  quiet_hours: Window[];
  repo_allowlist: string[];
  wall_clock_minutes: number;
  spend: { lookout_call_usd: number; daily_worker_usd: number; reviewer_call_usd: number };
  quota_hold_percent: number;
  write_deny: string[];
  skip: { labels: Scoped[]; senders: Scoped[]; dedupe_keys: Scoped[] };
}

/** The `limits` object of a fleet save. */
export type LimitsPatch = Record<string, unknown>;

/** The sentence field each Limit's control carries as its id, in catalog order. */
export const LIMIT_FIELDS = [
  'ceilings.t1',
  'ceilings.t2',
  'ceilings.t3',
  'ceilings.t4',
  'ceilings.fleet',
  'sender_allowlist',
  'rates.launches_per_hour',
  'rates.launches_per_sender_per_day',
  'rates.concurrent',
  'quiet_hours',
  'repo_allowlist',
  'wall_clock_minutes',
  'spend.lookout_call_usd',
  'spend.daily_worker_usd',
  'spend.reviewer_call_usd',
  'quota_hold_percent',
  'write_deny',
  'skip.labels',
  'skip.senders',
  'skip.dedupe_keys',
] as const;

export const LEVELS: Level[] = ['A0', 'A1', 'A3', 'A4', 'A5'];

// An A3 draft fix sits between A3 investigate and A4, as the server ranks it.
const rank = (level: Level, restricted?: Restricted) => LEVELS.indexOf(level) * 2 + (level === 'A3' && restricted === 'draft_fix' ? 1 : 0);

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

function setPath(patch: LimitsPatch, path: string, value: unknown) {
  const keys = path.split('.');
  let at = patch;
  for (const k of keys.slice(0, -1)) {
    const next = at[k];
    at = at[k] = next && typeof next === 'object' ? (next as LimitsPatch) : {};
  }
  at[keys[keys.length - 1]] = value;
}

function ceilingPatch(path: string, shown: Ceiling, edited: Ceiling): Record<string, unknown> | null {
  const patch: Record<string, unknown> = {};
  if (shown.level !== edited.level) patch.level = edited.level;
  if (edited.restricted !== undefined && shown.restricted !== edited.restricted) patch.restricted = edited.restricted;
  if (edited.level === 'A3' && shown.level !== 'A3' && edited.restricted !== undefined) patch.restricted = edited.restricted;
  if (shown.asks_first !== undefined && shown.asks_first !== edited.asks_first && edited.asks_first !== undefined) patch.asks_first = edited.asks_first;
  const threshold = edited.threshold;
  if (path === 'ceilings.t4' && !same(shown.threshold ?? null, threshold ?? null)) patch.threshold = threshold ?? null;
  // The server refuses a ceiling at or above its threshold, so a raise that reaches the threshold clears it unless the person set a new one.
  else if (path === 'ceilings.t4' && threshold && ('level' in patch || 'restricted' in patch) && rank(edited.level, edited.restricted) >= rank(threshold.level, threshold.restricted)) patch.threshold = null;
  return Object.keys(patch).length ? patch : null;
}

/** What a Limits save sends: only the leaves that changed, `null` for each leaf in `resets`, and the T4 threshold the new ceiling would otherwise be refused over. */
export function limitsBody(shown: Limits, edited: Limits, resets: string[] = []): LimitsPatch {
  const patch: LimitsPatch = {};
  for (const field of LIMIT_FIELDS) {
    const get = (l: Limits) => field.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown>)[k], l);
    const a = get(shown);
    const b = get(edited);
    if (field.startsWith('ceilings.')) {
      const c = ceilingPatch(field, a as Ceiling, b as Ceiling);
      if (c) setPath(patch, field, c);
    } else if (!same(a, b)) {
      setPath(patch, field, b);
    }
  }
  for (const r of resets) setPath(patch, r, null);
  return patch;
}
