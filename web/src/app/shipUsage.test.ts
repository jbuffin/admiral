import { describe, expect, it } from 'vitest';
import { USAGE_POLL_MS, shortCount, usageRow, type ShipUsage } from './shipUsage';
import { when } from './when';

const usage = (over: Partial<ShipUsage> = {}): ShipUsage => ({
  session: 's-1',
  harness: 'claude',
  source: 'record',
  tokens: { input: 100, output: 40, reasoning: 10, cache_read: 300, cache_write: 40 },
  money: { amount: 0.25, basis: 'estimate', incomplete: false },
  at: '2026-11-12T08:00:00Z',
  ...over,
});

describe('shortCount', () => {
  it('writes small counts whole and larger ones as k and M', () => {
    expect([0, 999, 1000, 1234, 15_400, 999_999, 1_500_000].map(shortCount)).toEqual(['0', '999', '1k', '1.2k', '15k', '1M', '1.5M']);
  });
});

describe('usageRow tokens', () => {
  it('headlines input + output + reasoning, and lists all five classes with cache apart', () => {
    const row = usageRow(usage())!;
    expect(row.tokens?.headline).toBe('150 tokens');
    expect(row.tokens?.classes).toEqual([
      { label: 'Input', value: '100' },
      { label: 'Output', value: '40' },
      { label: 'Reasoning', value: '10' },
      { label: 'Cache read', value: '300' },
      { label: 'Cache write', value: '40' },
    ]);
  });

  it('uses the short form in the headline and the classes', () => {
    const row = usageRow(usage({ tokens: { input: 1_200_000, output: 30_000, reasoning: 0, cache_read: 5_000_000, cache_write: 0 } }))!;
    expect(row.tokens?.headline).toBe('1.2M tokens');
    expect(row.tokens?.classes[3]).toEqual({ label: 'Cache read', value: '5M' });
  });

  it('has no tokens when the ship reported none, and still shows its money', () => {
    const row = usageRow(usage({ tokens: null }))!;
    expect(row.tokens).toBeNull();
    expect(row.money?.text).toBe('est. $0.25');
  });
});

describe('usageRow money', () => {
  it('says Claude money as an estimate in dollars', () => {
    const row = usageRow(usage())!;
    expect(row.money).toEqual({ text: 'est. $0.25', premium: '', incomplete: false });
  });

  it('says Copilot money as credits with its premium requests', () => {
    const row = usageRow(usage({ harness: 'copilot', money: { amount: 2.5, basis: 'credits', incomplete: false, premium_requests: 3 } }))!;
    expect(row.money).toEqual({ text: '2.5 credits', premium: '3 premium requests', incomplete: false });
  });

  it('says one premium request in the singular and none when the harness gave none', () => {
    const one = usageRow(usage({ harness: 'copilot', money: { amount: 1, basis: 'credits', incomplete: false, premium_requests: 1 } }))!;
    expect(one.money?.premium).toBe('1 premium request');
    const none = usageRow(usage({ harness: 'copilot', money: { amount: 1, basis: 'credits', incomplete: false } }))!;
    expect(none.money?.premium).toBe('');
  });

  it('marks money incomplete only when the money says so', () => {
    expect(usageRow(usage({ money: { amount: 0.25, basis: 'estimate', incomplete: true } }))!.money?.incomplete).toBe(true);
    expect(usageRow(usage())!.money?.incomplete).toBe(false);
  });

  it('shows Codex tokens and no money', () => {
    const row = usageRow(usage({ harness: 'codex', money: null }))!;
    expect(row.money).toBeNull();
    expect(row.tokens?.headline).toBe('150 tokens');
  });

  it('never mixes bases: a basis in gives only its own wording out', () => {
    const claude = usageRow(usage())!.money!;
    expect(claude.text).not.toMatch(/credit/i);
    expect(claude.premium).toBe('');
    const copilot = usageRow(usage({ harness: 'copilot', money: { amount: 2.5, basis: 'credits', incomplete: false, premium_requests: 3 } }))!.money!;
    expect(`${copilot.text} ${copilot.premium}`).not.toMatch(/\$|est\./);
  });

  it('keeps the words cost and budget out of what it says', () => {
    const row = usageRow(usage({ harness: 'copilot', money: { amount: 2.5, basis: 'credits', incomplete: true, premium_requests: 3 } }))!;
    expect(JSON.stringify(row)).not.toMatch(/cost|budget/i);
  });
});

describe('usageRow freshness', () => {
  it('says a live reading is live with its age', () => {
    expect(usageRow(usage({ source: 'live', age_seconds: 7 }))!.fresh).toBe('live, 7s ago');
    expect(usageRow(usage({ source: 'live', age_seconds: 125 }))!.fresh).toBe('live, 2m ago');
    expect(usageRow(usage({ source: 'live', age_seconds: 7300 }))!.fresh).toBe('live, 2h ago');
  });

  it('says records with when the latest was recorded', () => {
    expect(usageRow(usage())!.fresh).toBe(`from records, ${when('2026-11-12T08:00:00Z')}`);
  });

  it('says records alone when no time came with them', () => {
    expect(usageRow(usage({ at: undefined }))!.fresh).toBe('from records');
  });
});

describe('usageRow hides', () => {
  it('shows nothing when the ship has no figures yet', () => {
    expect(usageRow(usage({ source: 'none', tokens: null, money: null, at: undefined }))).toBeNull();
  });

  it('shows nothing when tokens and money are both null, and when there is no reply', () => {
    expect(usageRow(usage({ tokens: null, money: null }))).toBeNull();
    expect(usageRow(null)).toBeNull();
  });
});

describe('refresh', () => {
  it('polls every 30 seconds', () => {
    expect(USAGE_POLL_MS).toBe(30_000);
  });
});
