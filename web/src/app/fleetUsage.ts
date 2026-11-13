import { harnessName } from './harnessName';
import { moneyText, ageText, shortCount, type ShipUsage } from './shipUsage';

type Tokens = NonNullable<ShipUsage['tokens']>;
type Money = NonNullable<ShipUsage['money']>;

export interface Spender {
  tokens: Tokens | null;
  money: Money | null;
}
export interface HarnessUsage {
  harness: string;
  tokens: Tokens | null;
  money: Money | null;
  spenders: { ships?: Spender; chat?: Spender; workers?: Spender };
}
export interface Window {
  id: string;
  used_percent: number;
  window_minutes?: number;
  resets_at?: string;
  used_usd?: number;
  limit_usd?: number;
  period?: string;
}
export interface Quota {
  harness: string;
  label: string;
  has_data: boolean;
  age_seconds?: number;
  windows: Window[];
}
export type PeriodId = 'today' | '7d' | '30d' | 'month';
export interface FleetUsage {
  key: string;
  name: string;
  periods: Record<PeriodId, { harnesses: HarnessUsage[] }>;
  quota: Quota[];
  /** Present only while unattended launches are held for a plan window. */
  quota_hold?: { held: boolean; harness: string; window: string; window_minutes?: number; used_percent: number; line_percent: number; resets_at?: string };
}

/** A reading younger than this is live; the API sends only its age. */
export const QUOTA_LIVE_SECONDS = 120;

const NEAR_LIMIT_PERCENT = 90;

const tokensText = (t: Tokens | null) => (t ? `${shortCount(t.input + t.output + t.reasoning)} tokens` : null);

// Money keeps its own basis; nothing here adds one figure to another.
const moneyLine = (m: Money | null) => {
  if (!m) return null;
  const pr = m.premium_requests;
  return moneyText(m) + (m.basis === 'credits' && pr ? `, ${pr} premium request${pr === 1 ? '' : 's'}` : '');
};

const SPENDERS = [
  ['ships', 'Ships'],
  ['chat', 'AdmiralBot'],
  ['workers', 'Workers'],
] as const;

export interface SpenderLine {
  label: string;
  tokens: string | null;
  money: string | null;
  incomplete?: boolean;
}
export interface HarnessLine {
  harness: string;
  tokens: string | null;
  money: string | null;
  incomplete: boolean;
  spenders: SpenderLine[];
}

function harnessLine(h: HarnessUsage): HarnessLine {
  const spenders: SpenderLine[] = [];
  for (const [key, label] of SPENDERS) {
    const s = h.spenders[key];
    if (!s) continue;
    spenders.push({ label, tokens: tokensText(s.tokens), money: moneyLine(s.money), ...(s.money?.incomplete ? { incomplete: true } : {}) });
  }
  return { harness: harnessName(h.harness), tokens: tokensText(h.tokens), money: moneyLine(h.money), incomplete: h.money?.incomplete ?? false, spenders };
}

export interface PeriodLines {
  label: string;
  harnesses: HarnessLine[];
}

/** Today, 7 days and 30 days, and the month to date when a harness reports credits for it. */
export function usagePeriods(f: FleetUsage): PeriodLines[] {
  const out: PeriodLines[] = (
    [
      ['Today', 'today'],
      ['7 days', '7d'],
      ['30 days', '30d'],
    ] as const
  ).map(([label, id]) => ({ label, harnesses: f.periods[id].harnesses.map(harnessLine) }));
  const month = f.periods.month.harnesses;
  const credits = month.filter((h) => h.money?.basis === 'credits');
  if (credits.length) out.push({ label: 'Month to date', harnesses: credits.map(harnessLine) });
  return out;
}

export interface FleetRow {
  key: string;
  name: string;
  harnesses: HarnessLine[];
}

/** One row per fleet, in the order given, for one period. There is no row that adds fleets together. */
export const fleetRows = (fleets: FleetUsage[], period: PeriodId): FleetRow[] =>
  fleets.map((f) => ({ key: f.key, name: f.name, harnesses: f.periods[period].harnesses.map(harnessLine) }));

export interface QuotaBar {
  label: string;
  percent: number;
  resets: string;
  note: string;
  aria: string;
  detail: string;
}
export interface QuotaCard {
  label: string;
  fresh: string;
  bars: QuotaBar[];
}

function windowLabel(w: Window): string {
  if (w.id === 'spend') return w.period === 'monthly' ? 'Monthly spend' : 'Spend';
  const m = w.window_minutes;
  if (!m || m <= 0) return w.id === 'primary' ? 'Primary' : w.id === 'secondary' ? 'Secondary' : 'Window';
  if (m < 60) return `${m}-minute`;
  if (m % 1440 === 0) return `${m / 1440}-day`;
  if (m % 60 === 0) return `${m / 60}-hour`;
  return `${m}-minute`;
}

function resetsText(at: string | undefined, now: Date): string {
  if (!at) return '';
  const ms = new Date(at).getTime() - now.getTime();
  if (Number.isNaN(ms)) return '';
  if (ms <= 0) return 'resets now';
  const min = Math.floor(ms / 60_000);
  const h = Math.floor(min / 60);
  if (min < 60) return `resets in ${min}m`;
  if (h < 24) return `resets in ${h}h${min % 60 ? ` ${min % 60}m` : ''}`;
  return `resets in ${Math.floor(h / 24)}d${h % 24 ? ` ${h % 24}h` : ''}`;
}

function bar(w: Window, now: Date): QuotaBar {
  const label = windowLabel(w);
  const percent = Math.round(w.used_percent);
  const resets = resetsText(w.resets_at, now);
  const detail = w.id === 'spend' && w.used_usd !== undefined && w.limit_usd !== undefined ? `est. $${w.used_usd.toFixed(2)} of $${w.limit_usd.toFixed(2)}${w.period === 'monthly' ? ' this month' : ''}` : '';
  const head = w.id === 'spend' || !/\d/.test(label) ? label : `${label} window`;
  return {
    label,
    percent,
    resets,
    note: w.used_percent > NEAR_LIMIT_PERCENT ? 'near the limit' : '',
    aria: [`${head}, ${percent}% used`, detail, resets].filter(Boolean).join(', '),
    detail,
  };
}

/** One card per login the API lists: its bars, and whether they are live, last seen or not read yet. */
export function quotaCards(quota: Quota[], now: Date): QuotaCard[] {
  return quota.map((q) => {
    const age = q.age_seconds ?? 0;
    return {
      label: harnessName(q.label),
      fresh: !q.has_data ? 'no live data yet' : age < QUOTA_LIVE_SECONDS ? 'live' : `last seen ${ageText(age)} ago`,
      bars: q.has_data ? q.windows.map((w) => bar(w, now)) : [],
    };
  });
}

/** The line that says unattended launches are held, why and until when; null when they are not. */
export function heldLine(f: FleetUsage, now: Date): string | null {
  const h = f.quota_hold;
  if (!h?.held) return null;
  const label = h.window_minutes === 10080 ? 'weekly' : windowLabel({ id: h.window, used_percent: h.used_percent, window_minutes: h.window_minutes });
  const resets = resetsText(h.resets_at, now);
  return `Unattended launches are held: ${harnessName(h.harness)}'s ${/\d/.test(label) ? `${label} window` : `${label.toLowerCase()} window`} is at ${Math.round(h.used_percent)}% (line ${h.line_percent}%)${resets ? `, until it ${resets}` : ''}.`;
}
