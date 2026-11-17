import { render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { FleetUsage, HarnessUsage, Quota } from './fleetUsage';
import { AllUsagePage, FleetUsagePage } from './UsagePage';

const tok = (input: number, output = 0) => ({ input, output, reasoning: 0, cache_read: 300, cache_write: 0 });
const claude: HarnessUsage = { harness: 'claude', tokens: tok(100, 50), money: { amount: 0.25, basis: 'estimate', incomplete: false }, spenders: { ships: { tokens: tok(100, 50), money: { amount: 0.25, basis: 'estimate', incomplete: false } } } };
const copilot: HarnessUsage = { harness: 'copilot', tokens: null, money: { amount: 2.5, basis: 'credits', incomplete: false, premium_requests: 3 }, spenders: {} };
const codex: HarnessUsage = { harness: 'codex', tokens: tok(300, 60), money: null, spenders: {} };
const none = { harnesses: [] as HarnessUsage[] };

const usage = (key: string, name: string, harnesses: HarnessUsage[], quota: Quota[] = []): FleetUsage => ({
  key,
  name,
  periods: { today: { harnesses }, '7d': { harnesses }, '30d': { harnesses }, month: none },
  quota,
});
const dock = (harness: string): Quota => ({ harness, label: harness, has_data: false, windows: [] });

const serve = (routes: Record<string, unknown>) =>
  vi.stubGlobal('fetch', async (url: string) => new Response(JSON.stringify(routes[url] ?? {}), { status: routes[url] ? 200 : 404 }));
afterEach(() => vi.unstubAllGlobals());

const text = (el: Element) => el.textContent ?? '';
const region = (name: string | RegExp) => screen.getByRole('region', { name });

test('a Claude fleet shows each period with the harness\'s tokens, its own estimate in dollars and the Ships spender, and no credit, total or cap', async () => {
  serve({ '/api/v1/usage?fleet=zulu': usage('zulu', 'Westwind', [claude], [dock('claude'), dock('codex')]) });
  const { container } = render(<FleetUsagePage fleetKey="zulu" phone={false} />);
  await screen.findByRole('region', { name: 'Today' });
  for (const p of ['Today', '7 days', '30 days']) {
    expect(text(region(p))).toContain('150 tokens');
    expect(text(region(p))).toContain('est. $0.25');
  }
  expect(text(region('Today'))).toContain('Ships');
  expect(text(container)).not.toMatch(/credit|total|cap\b/i);
  // A login nothing has read yet says so and draws no window.
  expect(text(region(/claude/i))).toContain('no live data yet');
  expect(text(region(/codex/i))).toContain('no live data yet');
  expect(screen.queryByRole('img', { name: /window/ })).toBeNull();
});

test('a Copilot and Codex fleet keeps credits and tokens apart, with no dollar sign, no total and no Copilot quota card', async () => {
  serve({ '/api/v1/usage?fleet=alpha': usage('alpha', 'Homeport', [copilot, codex], [dock('codex')]) });
  const { container } = render(<FleetUsagePage fleetKey="alpha" phone={false} />);
  await screen.findByRole('region', { name: 'Today' });
  expect(text(region('Today'))).toContain('2.5 credits');
  expect(text(region('Today'))).toContain('3 premium requests');
  expect(text(region('Today'))).toContain('360 tokens');
  expect(text(container)).not.toMatch(/\$|total/i);
  expect(screen.queryByRole('region', { name: /copilot/i })).toBeNull();
});

test('a live reading draws one bar per window, marks only the one past 90% as near the limit, and leaves a dock with no reading alone', async () => {
  const resets = (s: number) => new Date(Date.now() + s * 1000).toISOString();
  serve({
    '/api/v1/usage?fleet=zulu': usage('zulu', 'Westwind', [claude], [
      { harness: 'claude', label: 'claude', has_data: true, age_seconds: 5, windows: [{ id: 'five_hour', used_percent: 93, window_minutes: 300, resets_at: resets(2 * 3600 + 30) }, { id: 'seven_day', used_percent: 41.2, window_minutes: 10080, resets_at: resets(3 * 86400 + 60) }] },
      dock('codex'),
    ]),
  });
  render(<FleetUsagePage fleetKey="zulu" phone={false} />);
  const card = await screen.findByRole('region', { name: /claude/i });
  expect(within(card).getByRole('img', { name: /^5-hour window, 93% used, resets in/ })).toBeTruthy();
  expect(within(card).getByRole('img', { name: /^7-day window, 41% used, resets in/ })).toBeTruthy();
  expect(text(card)).toContain('live');
  expect(within(card).getAllByText('near the limit')).toHaveLength(1);
  expect(text(region(/codex/i))).toContain('no live data yet');
});

test('the cross-fleet page has one row per fleet, each in its own money, and nothing that adds them', async () => {
  serve({ '/api/v1/usage': { fleets: [usage('zulu', 'Westwind', [claude]), usage('alpha', 'Homeport', [copilot, codex])] } });
  const { container } = render(<AllUsagePage phone={false} />);
  await waitFor(() => expect(screen.getAllByRole('region')).toHaveLength(2));
  expect(text(region('Westwind'))).toContain('est. $0.25');
  expect(text(region('Westwind'))).toContain('150 tokens');
  expect(text(region('Homeport'))).toContain('2.5 credits');
  expect(text(region('Homeport'))).toContain('360 tokens');
  expect(text(region('Homeport'))).not.toContain('$');
  expect(text(container)).not.toMatch(/total|all fleets combined/i);
});
