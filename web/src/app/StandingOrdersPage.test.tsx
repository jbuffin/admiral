import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet, FleetCapabilities } from './api';
import { capabilities, capabilitiesFailed, capabilitiesLoaded } from './capabilities';
import { fleets } from './fleets';
import { StandingOrdersPage } from './StandingOrdersPage';

const zulu = { key: 'zulu', name: 'Zulu', docks: [{ harness: 'claude' }], limits: {}, sentences: [] } as unknown as Fleet;
const alpha = { key: 'alpha', name: 'Home', docks: [{ harness: 'claude' }], limits: {}, sentences: [] } as unknown as Fleet;
const judging = { key: 'zulu', name: 'Zulu', judge: { harness: 'claude', lookout_model: 'haiku', strong_model: 'opus' }, harnesses: [] } as unknown as FleetCapabilities;
const judgeless = { key: 'zulu', name: 'Zulu', judge: { harness: null, reason: 'No harness is proven for judge calls' }, harnesses: [{ harness: 'claude', version: '', detected_at: null, features: [] }] } as unknown as FleetCapabilities;

interface Orders {
  checks: string[];
  guidance: string;
  preset: string;
  sentences: { field: string; sentence: string }[];
  overridden: string[];
}
interface Call {
  method: string;
  path: string;
  body?: Record<string, any>;
}

const WALL = { field: 'ceilings.wall', sentence: 'A ship stops after 1,440 minutes.' };
const SENTENCES = [{ field: 'rates.concurrent', sentence: 'At most 3 ships run at once.' }, { field: '', sentence: 'Nothing merges without a review.' }];

let orders: Orders;
let cap: FleetCapabilities;
let calls: Call[];
let ordersFailures: number;
let draft: unknown;
let copied: unknown;
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

beforeEach(() => {
  orders = { checks: [], guidance: '', preset: 'work', sentences: SENTENCES, overridden: [] };
  cap = judging;
  calls = [];
  ordersFailures = 0;
  draft = undefined;
  copied = undefined;
  capabilities.value = [];
  capabilitiesLoaded.value = false;
  capabilitiesFailed.value = false;
  fleets.value = [zulu, alpha];
  history.replaceState(null, '', '/app/routines/standing-orders?fleet=zulu');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, any>) : undefined;
      if (path.endsWith('/harness-capabilities')) return json({ fleets: [cap] });
      if (method === 'GET' && path.endsWith('/fleets')) return json({ fleets: [zulu, alpha] });
      if (method === 'GET' && path.endsWith('/standing-orders')) return json(orders);
      calls.push({ method, path, body });
      if (path.endsWith('/standing-orders/draft')) return json(draft);
      if (path.endsWith('/standing-orders/copy')) return json(copied);
      if (method === 'PUT' && path.endsWith('/standing-orders')) {
        if (ordersFailures > 0) {
          ordersFailures--;
          return json({ error: 'disk full' }, 500);
        }
        orders = { ...orders, ...body };
        return json(orders);
      }
      if (method === 'PUT') return json({ fleets: [zulu, alpha] });
      return json({});
    }),
  );
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const render_ = () => render(<StandingOrdersPage fleet={zulu} title />);
const puts = (suffix: string) => calls.filter((c) => c.method === 'PUT' && c.path.endsWith(suffix));
const mixed = (loosens: string[]) => ({
  draft: { limits: { ceilings: { wall: 1440 } }, checks: ['Ask before changing CI.'], guidance: 'Keep commits small and focused.' },
  sentences: [WALL],
  changes: [WALL],
  loosens,
  problems: [],
});
const draftIt = async () => {
  fireEvent.input(await screen.findByRole('textbox', { name: 'New standing order' }), { target: { value: 'Stop ships after a day. Ask before changing CI. Keep commits small and focused.' } });
  fireEvent.click(screen.getByRole('button', { name: 'Draft' }));
  await screen.findByRole('textbox', { name: 'Drafted Check 1' });
};
const settled = (button: HTMLElement) => waitFor(() => expect(button.getAttribute('aria-busy')).not.toBe('true'));

test('the tab names the fleet and judge, shows each Limit as the sentence the server wrote, and links the ones that have a field to the Limits page', async () => {
  orders.overridden = ['rates.concurrent'];
  render_();
  const limits = await screen.findByRole('region', { name: 'Limits' });
  expect(screen.getByText('Zulu fleet only')).toBeTruthy();
  await screen.findByText(/Judge calls: Claude/);
  const link = within(limits).getByRole('link', { name: 'At most 3 ships run at once.' });
  expect(link.getAttribute('href')).toContain('/preferences/zulu/limits');
  expect(link.getAttribute('href')).toContain('field=rates.concurrent');
  expect(within(limits).getByText('Nothing merges without a review.')).toBeTruthy();
  expect(within(limits).queryByRole('link', { name: 'Nothing merges without a review.' })).toBeNull();
  expect(within(limits).getByText(/Changed from the preset/)).toBeTruthy();
  expect(limits.textContent).toContain('Work preset');
  fireEvent.click(link);
  expect(location.pathname).toBe('/app/preferences/zulu/limits');
  expect(location.search).toContain('field=rates.concurrent');
});

test('a Check is added and Guidance saved, each sending only its own key, and the fields show what the server stored', async () => {
  render_();
  const checks = await screen.findByRole('region', { name: 'Checks' });
  fireEvent.input(within(checks).getByRole('textbox', { name: 'New Check' }), { target: { value: 'Run make check before any PR.' } });
  fireEvent.click(within(checks).getByRole('button', { name: 'Add' }));
  await waitFor(() => expect(puts('/standing-orders').map((c) => c.body)).toEqual([{ checks: ['Run make check before any PR.'] }]));
  await waitFor(() => expect((within(checks).getByRole('textbox', { name: 'Check 1' }) as HTMLInputElement).value).toBe('Run make check before any PR.'));
  const guidance = screen.getByRole('region', { name: 'Guidance' });
  const save = within(guidance).getByRole('button', { name: 'Save' }) as HTMLButtonElement;
  expect(save.disabled).toBe(true);
  fireEvent.input(within(guidance).getByRole('textbox', { name: 'Guidance' }), { target: { value: 'Prefer small PRs.' } });
  await waitFor(() => expect(save.disabled).toBe(false));
  fireEvent.click(save);
  await waitFor(() => expect(puts('/standing-orders').map((c) => c.body)).toEqual([{ checks: ['Run make check before any PR.'] }, { guidance: 'Prefer small PRs.' }]));
  expect(orders).toMatchObject({ checks: ['Run make check before any PR.'], guidance: 'Prefer small PRs.' });
  expect((within(checks).getByRole('textbox', { name: 'Check 1' }) as HTMLInputElement).value).toBe('Run make check before any PR.');
});

test('a filed draft shows the loosened Limit, the Check and the Guidance; Confirm arms first and a tap inside the double-tap window only arms', async () => {
  orders = { ...orders, checks: ['Run make check before any PR.'], guidance: 'Prefer small PRs.' };
  draft = mixed(['ceilings.wall']);
  vi.useFakeTimers({ toFake: ['Date'] });
  render_();
  await draftIt();
  const review = screen.getByRole('region', { name: 'Draft review' });
  expect(within(review).getAllByText(/1,440 minutes/).length).toBeGreaterThan(0);
  expect(within(review).getByRole('group', { name: 'Loosening' }).textContent).toMatch(/loosen/i);
  expect(within(review).getByRole('link', { name: 'open the Limits page' }).getAttribute('href')).toContain('/preferences/zulu/limits');
  expect((within(review).getByRole('textbox', { name: 'Drafted Check 1' }) as HTMLInputElement).value).toBe('Ask before changing CI.');
  expect((within(review).getByRole('textbox', { name: 'Drafted Guidance' }) as HTMLTextAreaElement).value).toBe('Keep commits small and focused.');
  fireEvent.input(within(review).getByRole('textbox', { name: 'Drafted Check 1' }), { target: { value: 'Ask before changing CI or releases.' } });

  const first = within(review).getByRole('button', { name: 'Confirm' });
  await settled(first);
  fireEvent.click(first);
  const armed = await within(review).findByRole('button', { name: /^Confirm, loosening 1 Limit/ });
  expect(calls.filter((c) => c.method === 'PUT')).toEqual([]);
  vi.setSystemTime(Date.now() + 399);
  fireEvent.click(armed);
  await Promise.resolve();
  expect(calls.filter((c) => c.method === 'PUT')).toEqual([]);

  vi.setSystemTime(Date.now() + 2);
  fireEvent.click(armed);
  await waitFor(() => expect(puts('/standing-orders')).toHaveLength(1));
  expect(puts('/fleets/zulu').map((c) => c.body)).toEqual([{ limits: { ceilings: { wall: 1440 } } }]);
  expect(puts('/standing-orders')[0]!.body).toEqual({ checks: ['Run make check before any PR.', 'Ask before changing CI or releases.'], guidance: 'Prefer small PRs.\n\nKeep commits small and focused.' });
  await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Drafted Check 1' })).toBeNull());
  expect((screen.getByRole('textbox', { name: 'Check 2' }) as HTMLInputElement).value).toBe('Ask before changing CI or releases.');
  expect((screen.getByRole('textbox', { name: 'Guidance' }) as HTMLTextAreaElement).value).toBe('Prefer small PRs.\n\nKeep commits small and focused.');
});

test('an orders save that fails after the Limits were stored says so, and the retry sends only the orders', async () => {
  orders = { ...orders, checks: ['Run make check before any PR.'] };
  draft = mixed(['ceilings.wall']);
  ordersFailures = 1;
  vi.useFakeTimers({ toFake: ['Date'] });
  render_();
  await draftIt();
  const review = screen.getByRole('region', { name: 'Draft review' });
  const first = within(review).getByRole('button', { name: 'Confirm' });
  await settled(first);
  fireEvent.click(first);
  const armed = await within(review).findByRole('button', { name: /^Confirm, loosening 1 Limit/ });
  vi.setSystemTime(Date.now() + 500);
  fireEvent.click(armed);
  await within(review).findByText(/The Limits were saved, but the Checks and Guidance weren't: disk full/);
  expect(puts('/fleets/zulu')).toHaveLength(1);
  expect(orders.checks).toEqual(['Run make check before any PR.']);

  const retry = within(review).getByRole('button', { name: 'Confirm' });
  await settled(retry);
  fireEvent.click(retry);
  await waitFor(() => expect(orders.checks).toEqual(['Run make check before any PR.', 'Ask before changing CI.']));
  expect(puts('/fleets/zulu')).toHaveLength(1);
  expect(orders.guidance).toBe('Keep commits small and focused.');
  await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Drafted Check 1' })).toBeNull());
});

test('Copy to another fleet confirms in place, posts the target, and the notice names the fleet and each Limit it loosens with that fleet’s sentence', async () => {
  copied = { ...orders, preset: 'home', checks: ['Ask before touching CI.'], sentences: [{ field: 'ceilings.t3', sentence: 'Tier 3 ships may spend up to 40 minutes.' }], loosens: ['ceilings.t3'] };
  render_();
  fireEvent.click(await screen.findByRole('button', { name: 'Copy to another fleet' }));
  expect((screen.getByRole('combobox', { name: 'Copy to' }) as HTMLSelectElement).value).toBe('alpha');
  fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
  const confirm = screen.getByRole('group', { name: 'Copy to Home' });
  expect(confirm.textContent).toMatch(/replaces/);
  expect(confirm.textContent).toMatch(/Checks/);
  expect(calls.filter((c) => c.method === 'POST')).toEqual([]);
  fireEvent.click(within(confirm).getByRole('button', { name: /^Replace/ }));
  const notice = await screen.findByRole('status');
  expect(calls.filter((c) => c.method === 'POST').map((c) => [c.path, c.body])).toEqual([['/api/v1/fleets/zulu/standing-orders/copy', { to: 'alpha' }]]);
  expect(notice.textContent).toContain('Copied to Home');
  expect(notice.textContent).toContain('Tier 3 ships may spend up to 40 minutes.');
});

test('with no proven judge the tab says so, lists what stays off, re-runs the probe, and files text through a plain form that keeps unsaved edits', async () => {
  cap = judgeless;
  orders = { ...orders, checks: ['Never touch the payments-api migrations folder.'] };
  render_();
  await screen.findByText(/Judge calls: off/);
  const off = await screen.findByRole('region', { name: 'Needs the judge' });
  for (const feature of [/Lookout/, /reviewer/, /drafter/, /advisor/]) expect(within(off).getAllByText(feature).length).toBeGreaterThan(0);
  fireEvent.click(within(off).getAllByRole('button', { name: /Check again/ })[0]!);
  await waitFor(() => expect(calls.find((c) => c.path.endsWith('/harness-capabilities/check'))?.body).toMatchObject({ fleet: 'zulu' }));

  expect(screen.queryByRole('button', { name: 'Draft' })).toBeNull();
  const plain = await screen.findByRole('form', { name: 'New standing order' });
  expect(plain.textContent).toContain('Drafting is off');
  const first = screen.getByRole('textbox', { name: 'Check 1' }) as HTMLInputElement;
  fireEvent.input(first, { target: { value: 'Never touch the payments-api folder.' } });
  fireEvent.input(within(plain).getByRole('textbox', { name: 'New standing order' }), { target: { value: 'Ask before changing CI.' } });
  fireEvent.click(within(plain).getByRole('radio', { name: 'Check' }));
  fireEvent.click(within(plain).getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(puts('/standing-orders').map((c) => c.body)).toEqual([{ checks: ['Never touch the payments-api migrations folder.', 'Ask before changing CI.'] }]));
  await waitFor(() => expect((screen.getByRole('textbox', { name: 'Check 2' }) as HTMLInputElement).value).toBe('Ask before changing CI.'));
  expect((screen.getByRole('textbox', { name: 'Check 1' }) as HTMLInputElement).value).toBe('Never touch the payments-api folder.');
  fireEvent.click(screen.getByRole('button', { name: 'Save Checks' }));
  await waitFor(() => expect(puts('/standing-orders')[1]?.body).toEqual({ checks: ['Never touch the payments-api folder.', 'Ask before changing CI.'] }));
});
