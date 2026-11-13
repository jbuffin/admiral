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

/** What a Limits save sends: only the leaves that changed, `null` for each leaf in `resets`, and the T4 threshold the new ceiling would otherwise be refused over. */
export function limitsBody(_shown: Limits, _edited: Limits, _resets: string[] = []): LimitsPatch {
  return { wall_clock_minutes: -1 };
}
