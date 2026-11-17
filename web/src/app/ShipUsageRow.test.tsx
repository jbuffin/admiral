import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { ShipUsage } from './shipUsage';
import { UsageRow } from './ShipView';

const reply = (u: Partial<ShipUsage>) =>
  vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ session: 's', harness: 'claude', source: 'record', tokens: null, money: null, ...u })));
afterEach(() => vi.unstubAllGlobals());

const shown = async () => {
  render(<UsageRow session="s" />);
  return screen.findByRole('group', { name: 'Usage' });
};

test('a Claude ship shows its tokens and its dollar estimate from records, with the cache classes inside the fold and no credit or premium', async () => {
  reply({ tokens: { input: 100, output: 50, reasoning: 0, cache_read: 300, cache_write: 0 }, money: { amount: 0.25, basis: 'estimate', incomplete: false } });
  const usage = await shown();
  expect(usage.textContent).toContain('150 tokens');
  expect(usage.textContent).toContain('est. $0.25');
  expect(usage.textContent).toContain('from records');
  expect(usage.textContent).not.toMatch(/credit|premium/i);
  const fold = usage.querySelector('details') as HTMLDetailsElement;
  expect(fold.open).toBe(false);
  fireEvent.click(usage.querySelector('summary') as HTMLElement);
  await waitFor(() => expect(fold.open).toBe(true));
  expect(fold.textContent).toContain('Cache read');
  expect(fold.textContent).toContain('300');
});

test('a Copilot ship shows credits and its premium requests and no dollar sign', async () => {
  reply({ harness: 'copilot', money: { amount: 2.5, basis: 'credits', incomplete: false, premium_requests: 3 } });
  const usage = await shown();
  expect(usage.textContent).toContain('2.5 credits');
  expect(usage.textContent).toContain('3 premium requests');
  expect(usage.textContent).not.toContain('$');
});

test('a Codex ship shows its tokens and no money', async () => {
  reply({ harness: 'codex', tokens: { input: 300, output: 60, reasoning: 0, cache_read: 0, cache_write: 0 } });
  const usage = await shown();
  expect(usage.textContent).toContain('360 tokens');
  expect(usage.textContent).not.toMatch(/\$|credit/);
});
