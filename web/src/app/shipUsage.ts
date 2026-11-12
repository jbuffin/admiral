import { call, type Result } from './api';
import { when } from './when';

export interface ShipUsage {
  session: string;
  harness: string;
  source: 'live' | 'record' | 'none';
  tokens: { input: number; output: number; reasoning: number; cache_read: number; cache_write: number } | null;
  money: { amount: number; basis: 'estimate' | 'credits'; incomplete: boolean; premium_requests?: number } | null;
  age_seconds?: number;
  at?: string;
}

export const USAGE_POLL_MS = 30_000;

/** Reads `path` now, every USAGE_POLL_MS and when the page turns visible; returns the stop. `apply` says whether a reply counted, and a reply older than the last that counted is dropped. */
export function watchPath<T>(path: string, apply: (r: Result<T>) => boolean): () => void {
  let live = true;
  let sent = 0;
  let applied = 0;
  const read = () => {
    const mine = ++sent;
    void call<T>('GET', path).then((r) => {
      if (!live || mine < applied) return;
      if (apply(r)) applied = mine;
    });
  };
  const visible = () => {
    if (document.visibilityState === 'visible') read();
  };
  read();
  const timer = setInterval(read, USAGE_POLL_MS);
  document.addEventListener('visibilitychange', visible);
  return () => {
    live = false;
    clearInterval(timer);
    document.removeEventListener('visibilitychange', visible);
  };
}

/** Reads a ship's usage with watchPath. A 404 clears the figures, any other error keeps them. */
export function watchUsage(session: string, onUsage: (u: ShipUsage | null) => void): () => void {
  return watchPath<ShipUsage>(`/ships/${encodeURIComponent(session)}/usage`, (r) => {
    if (r.ok) onUsage(r.data);
    else if (r.status === 404) onUsage(null);
    else return false;
    return true;
  });
}

/** A count as `999`, `1.2k`, `15k` or `1.5M`. */
export function shortCount(n: number): string {
  if (n < 1000) return String(n);
  const k = n / 1e3;
  const kk = k < 10 ? Number(k.toFixed(1)) : Math.round(k);
  if (kk < 1000) return `${kk}k`;
  const m = n / 1e6;
  return `${m < 10 ? Number(m.toFixed(1)) : Math.round(m)}M`;
}

export interface UsageRow {
  tokens: { headline: string; classes: { label: string; value: string }[] } | null;
  money: { text: string; premium: string; incomplete: boolean } | null;
  fresh: string;
}

/** Money in its own basis: Claude's estimate in dollars, Copilot's credits. */
export const moneyText = (m: { amount: number; basis: 'estimate' | 'credits' }) => (m.basis === 'estimate' ? `est. $${m.amount.toFixed(2)}` : `${Number(m.amount.toFixed(2))} credits`);

export const ageText = (s: number) => (s < 60 ? `${Math.floor(s)}s` : s < 3600 ? `${Math.floor(s / 60)}m` : `${Math.floor(s / 3600)}h`);

/** The Bridge's usage row, or null when there is nothing to show. Money keeps its own basis; nothing here adds bases together. */
export function usageRow(u: ShipUsage | null): UsageRow | null {
  if (!u || u.source === 'none' || (!u.tokens && !u.money)) return null;
  const t = u.tokens;
  const m = u.money;
  const pr = m?.premium_requests;
  let fresh = 'from records';
  if (u.source === 'live') fresh = `live, ${ageText(u.age_seconds ?? 0)} ago`;
  else if (when(u.at)) fresh = `from records, ${when(u.at)}`;
  return {
    tokens: t
      ? {
          headline: `${shortCount(t.input + t.output + t.reasoning)} tokens`,
          classes: [
            { label: 'Input', value: shortCount(t.input) },
            { label: 'Output', value: shortCount(t.output) },
            { label: 'Reasoning', value: shortCount(t.reasoning) },
            { label: 'Cache read', value: shortCount(t.cache_read) },
            { label: 'Cache write', value: shortCount(t.cache_write) },
          ],
        }
      : null,
    money: m
      ? {
          text: moneyText(m),
          premium: m.basis === 'credits' && pr ? `${pr} premium request${pr === 1 ? '' : 's'}` : '',
          incomplete: m.incomplete,
        }
      : null,
    fresh,
  };
}
