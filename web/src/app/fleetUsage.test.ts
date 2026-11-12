import { describe, expect, it } from 'vitest';
import { QUOTA_LIVE_SECONDS, fleetRows, quotaCards, usagePeriods, type FleetUsage, type HarnessUsage, type Quota } from './fleetUsage';

const tok = (input: number, output = 0, reasoning = 0) => ({ input, output, reasoning, cache_read: 9000, cache_write: 400 });
const est = (amount: number, incomplete = false) => ({ amount, basis: 'estimate' as const, incomplete });

const claude: HarnessUsage = {
  harness: 'claude',
  tokens: tok(1200, 300),
  money: est(1.32),
  spenders: {
    ships: { tokens: tok(1000, 250), money: est(1.1) },
    chat: { tokens: tok(200, 50), money: est(0.1) },
    workers: { tokens: null, money: est(0.12) },
  },
};
const copilot: HarnessUsage = {
  harness: 'copilot',
  tokens: null,
  money: { amount: 2.5, basis: 'credits', incomplete: false, premium_requests: 3 },
  spenders: { ships: { tokens: null, money: { amount: 2.5, basis: 'credits', incomplete: false, premium_requests: 3 } } },
};
const empty = { harnesses: [] as HarnessUsage[] };
const fleet = (key: string, today: HarnessUsage[], over: Partial<FleetUsage> = {}): FleetUsage => ({
  key,
  name: key.toUpperCase(),
  periods: { today: { harnesses: today }, '7d': { harnesses: today }, '30d': { harnesses: today }, month: empty },
  quota: [],
  ...over,
});

describe('usagePeriods', () => {
  it('lists today, 7 days and 30 days, each with one line per harness', () => {
    const p = usagePeriods(fleet('zulu', [claude, copilot]));
    expect(p.map((x) => x.label)).toEqual(['Today', '7 days', '30 days']);
    expect(p[0].harnesses.map((h) => h.harness)).toEqual(['claude', 'copilot']);
  });

  it('headlines input + output + reasoning and keeps each harness money in its own basis, never summed', () => {
    const [today] = usagePeriods(fleet('zulu', [claude, copilot]));
    const [c, p] = today.harnesses;
    expect(c.tokens).toBe('1.5k tokens');
    expect(c.money).toBe('est. $1.32');
    expect(p.tokens).toBeNull();
    expect(p.money).toBe('2.5 credits, 3 premium requests');
    expect(JSON.stringify(today)).not.toMatch(/3\.82|3\.8|total/i);
  });

  it('splits a harness by spender: ships, AdmiralBot and workers, workers with money and no tokens', () => {
    const [today] = usagePeriods(fleet('zulu', [claude]));
    expect(today.harnesses[0].spenders).toEqual([
      { label: 'Ships', tokens: '1.3k tokens', money: 'est. $1.10' },
      { label: 'AdmiralBot', tokens: '250 tokens', money: 'est. $0.10' },
      { label: 'Workers', tokens: null, money: 'est. $0.12' },
    ]);
  });

  it('leaves out a spender the response left out', () => {
    const [today] = usagePeriods(fleet('zulu', [copilot]));
    expect(today.harnesses[0].spenders.map((s) => s.label)).toEqual(['Ships']);
  });

  it('marks incomplete money quietly, on the harness and on a spender', () => {
    const inc: HarnessUsage = { ...claude, money: est(1.32, true), spenders: { ships: { tokens: tok(10), money: est(1, true) } } };
    const h = usagePeriods(fleet('zulu', [inc]))[0].harnesses[0];
    expect(h.incomplete).toBe(true);
    expect(h.spenders[0].incomplete).toBe(true);
    expect(usagePeriods(fleet('zulu', [claude]))[0].harnesses[0].incomplete).toBe(false);
  });

  it('shows a period with nothing as no harness lines', () => {
    expect(usagePeriods(fleet('zulu', []))[0].harnesses).toEqual([]);
  });

  it('adds month to date only when the month has credits', () => {
    const none = usagePeriods(fleet('zulu', [claude], { periods: { today: empty, '7d': empty, '30d': empty, month: { harnesses: [claude] } } }));
    expect(none.map((x) => x.label)).toEqual(['Today', '7 days', '30 days']);
    const credits = usagePeriods(fleet('alpha', [copilot], { periods: { today: empty, '7d': empty, '30d': empty, month: { harnesses: [copilot] } } }));
    expect(credits.map((x) => x.label)).toEqual(['Today', '7 days', '30 days', 'Month to date']);
    expect(credits[3].harnesses[0].money).toBe('2.5 credits, 3 premium requests');
  });

  it('has no worker spend cap in what it says, until the cap exists', () => {
    expect(JSON.stringify(usagePeriods(fleet('zulu', [claude])))).not.toMatch(/cap|budget|cost/i);
  });
});

const now = new Date('2026-11-12T12:00:00Z');
const at = (min: number) => new Date(now.getTime() + min * 60_000).toISOString();
const q = (over: Partial<Quota> = {}): Quota => ({
  harness: 'claude',
  label: 'claude',
  has_data: true,
  age_seconds: 30,
  windows: [
    { id: 'primary', used_percent: 40, window_minutes: 300, resets_at: at(135) },
    { id: 'secondary', used_percent: 10, window_minutes: 10080, resets_at: at(60 * 52) },
  ],
  ...over,
});

describe('quotaCards', () => {
  it('makes one bar per window, labelled from its minutes, with its share and reset time', () => {
    const [card] = quotaCards([q()], now);
    expect(card.label).toBe('claude');
    expect(card.bars.map((b) => [b.label, b.percent, b.resets])).toEqual([
      ['5-hour', 40, 'resets in 2h 15m'],
      ['7-day', 10, 'resets in 2d 4h'],
    ]);
    expect(card.bars[0].aria).toBe('5-hour window, 40% used, resets in 2h 15m');
  });

  it('names other windows by their hours or days', () => {
    const w = (m: number) => ({ id: 'primary', used_percent: 1, window_minutes: m, resets_at: at(30) });
    const labels = quotaCards([q({ windows: [w(60), w(120), w(2880)] })], now)[0].bars.map((b) => b.label);
    expect(labels).toEqual(['1-hour', '2-hour', '2-day']);
  });

  it('shows the Claude spend window as a bar with est. dollars used of the limit this month', () => {
    const spend = { id: 'spend', used_percent: 30, used_usd: 6, limit_usd: 20, period: 'monthly', resets_at: at(60 * 24 * 10) };
    const bar = quotaCards([q({ windows: [spend] })], now)[0].bars[0];
    expect(bar.percent).toBe(30);
    expect(bar.detail).toBe('est. $6.00 of $20.00 this month');
    expect(bar.label).toMatch(/spend/i);
  });

  it('marks a window past 90% with a quiet note, and no other window', () => {
    const w = (p: number) => ({ id: 'primary', used_percent: p, window_minutes: 300, resets_at: at(30) });
    const notes = quotaCards([q({ windows: [w(90), w(90.5), w(93), w(100)] })], now)[0].bars.map((b) => b.note);
    expect(notes).toEqual(['', 'near the limit', 'near the limit', 'near the limit']);
  });

  it('rounds the share to a whole percent', () => {
    const bar = quotaCards([q({ windows: [{ id: 'primary', used_percent: 23.5, window_minutes: 300, resets_at: at(30) }] })], now)[0].bars[0];
    expect(bar.percent).toBe(24);
  });

  it('says a window that has reset, and a reset under an hour in minutes', () => {
    const w = (min: number) => ({ id: 'primary', used_percent: 0, window_minutes: 300, resets_at: at(min) });
    expect(quotaCards([q({ windows: [w(-5), w(45)] })], now)[0].bars.map((b) => b.resets)).toEqual(['resets now', 'resets in 45m']);
  });

  it('says live under the threshold and last seen with its age at or past it', () => {
    expect(QUOTA_LIVE_SECONDS).toBe(120);
    const fresh = (age: number) => quotaCards([q({ age_seconds: age })], now)[0].fresh;
    expect(fresh(0)).toBe('live');
    expect(fresh(119)).toBe('live');
    expect(fresh(120)).toBe('last seen 2m ago');
    expect(fresh(7300)).toBe('last seen 2h ago');
  });

  it('says no live data yet, with no bars, when nothing has been read since the restart', () => {
    const [card] = quotaCards([q({ has_data: false, age_seconds: undefined, windows: [] })], now);
    expect(card.fresh).toBe('no live data yet');
    expect(card.bars).toEqual([]);
  });

  it('gives one card per login entry in the order given, and none when there are none', () => {
    expect(quotaCards([q(), q({ harness: 'codex', label: 'codex' })], now).map((c) => c.label)).toEqual(['claude', 'codex']);
    expect(quotaCards([], now)).toEqual([]);
  });

  it('keeps the quiet mark to a text note: no alarm fields', () => {
    const bar = quotaCards([q({ windows: [{ id: 'primary', used_percent: 95, window_minutes: 300, resets_at: at(30) }] })], now)[0].bars[0];
    expect(Object.keys(bar).sort()).toEqual(['aria', 'detail', 'label', 'note', 'percent', 'resets']);
  });
});

describe('fleetRows', () => {
  const fleets = [fleet('zulu', [claude]), fleet('alpha', [copilot]), fleet('idle', [])];

  it('gives one row per fleet in the order given, each with its own harness figures', () => {
    const rows = fleetRows(fleets, 'today');
    expect(rows.map((r) => r.key)).toEqual(['zulu', 'alpha', 'idle']);
    expect(rows[0].harnesses.map((h) => [h.harness, h.tokens, h.money])).toEqual([['claude', '1.5k tokens', 'est. $1.32']]);
    expect(rows[1].harnesses.map((h) => [h.harness, h.money])).toEqual([['copilot', '2.5 credits, 3 premium requests']]);
    expect(rows[2].harnesses).toEqual([]);
  });

  it('reads the period it is asked for', () => {
    const f = fleet('zulu', [claude], { periods: { today: empty, '7d': { harnesses: [claude] }, '30d': empty, month: empty } });
    expect(fleetRows([f], 'today')[0].harnesses).toEqual([]);
    expect(fleetRows([f], '7d')[0].harnesses).toHaveLength(1);
  });

  it('never adds money across fleets: two fleets on one basis stay two figures, with no total row', () => {
    const rows = fleetRows([fleet('a', [claude]), fleet('b', [{ ...claude, money: est(2) }])], 'today');
    expect(rows).toHaveLength(2);
    expect(rows.map((r) => r.harnesses[0].money)).toEqual(['est. $1.32', 'est. $2.00']);
    expect(JSON.stringify(rows)).not.toMatch(/3\.32|total/i);
  });
});
