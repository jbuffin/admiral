import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Capability, Fleet, FleetCapabilities } from './api';
import { capabilities, capabilitiesFailed, capabilitiesLoaded } from './capabilities';
import { RoutinesPage } from './RoutinesPage';
import type { Routine, RoutineSpend } from './routines';
import { sources } from './shipsLink';

const fleet = { key: 'zulu', name: 'Zulu', docks: [{ harness: 'claude' }], limits: {}, sentences: [] } as unknown as Fleet;
const cap = (id: string, status: Capability['status']): Capability => ({ id, label: id, status, checked_at: null, detail: '' });
const proven: FleetCapabilities[] = [
  {
    key: 'zulu',
    name: 'Zulu',
    judge: { harness: 'claude' },
    harnesses: [{ harness: 'claude', version: '', detected_at: null, features: [{ id: 'f', label: 'F', capabilities: [cap('autopilot.guard_hook', 'passed')] }] }],
  } as unknown as FleetCapabilities,
];

interface Call {
  method: string;
  path: string;
  body?: Record<string, unknown>;
}

let routines: Routine[];
let spend: RoutineSpend | undefined;
let calls: Call[];
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const stored = (over: Partial<Routine> = {}): Routine => ({
  id: 'r1',
  name: 'Form sweep',
  enabled: true,
  when: '30 3 * * *',
  do: { launch: { harness: 'claude', dir: '/h/proj', text: 'go' } },
  before_acting: 'act',
  notify: false,
  ...over,
});

beforeEach(() => {
  routines = [];
  spend = undefined;
  calls = [];
  capabilities.value = [];
  capabilitiesLoaded.value = false;
  capabilitiesFailed.value = false;
  sources.value = [];
  history.replaceState(null, '', '/app/routines?fleet=zulu');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined;
      if (path.endsWith('/harness-capabilities')) return json({ fleets: proven });
      if (path.endsWith('/launch-orders/dirs')) return json({ entries: [{ path: '/h/proj', name: 'proj', kind: 'checkout', ships_here: 0 }], selected: null, clone: null });
      if (path.includes('/launch-orders/skills')) return json({ mark: '/', commodore: { found: false, name: '' }, skills: [] });
      if (path.endsWith('/runs')) return json({ runs: [] });
      if (method === 'GET' && path.endsWith('/routines')) return json({ routines, spend });
      calls.push({ method, path, body });
      if (path.endsWith('/routines/draft')) return json({ draft: stored({ name: 'Nightly dependency check', when: '0 3 * * *' }), problems: null });
      const id = /routines\/([^/]+?)(?:\/(on|off))?$/.exec(path);
      if (method === 'POST' && !id) {
        const made = stored({ id: 'r-new', ...(body as Partial<Routine>) });
        routines = [made];
        return json(made);
      }
      const at = routines.findIndex((r) => r.id === id?.[1]);
      if (at < 0) return json({ error: 'not found' }, 404);
      if (id?.[2]) routines[at] = { ...routines[at]!, enabled: id[2] === 'on' };
      else if (method === 'PUT') routines[at] = { ...routines[at]!, ...(body as Partial<Routine>) };
      return json(routines[at]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

const page = (id: string | null = null, creating = false) => render(<RoutinesPage fleet={fleet} id={id} creating={creating} title phone={false} />);
const editor = () => screen.getByRole('form', { name: 'Routine' });
const save = () => within(editor()).getByRole('button', { name: 'Save' });

test('describing a routine opens the editor filled from the draft, and nothing is stored until Save', async () => {
  const view = page();
  await screen.findByText(/No routines yet/);
  const describe = await screen.findByRole('textbox', { name: 'Describe a routine' });
  fireEvent.input(describe, { target: { value: 'every night at 3, check for outdated dependencies' } });
  fireEvent.click(screen.getByRole('button', { name: 'Draft' }));
  await waitFor(() => expect(location.pathname).toBe('/app/routines/new'));
  view.rerender(<RoutinesPage fleet={fleet} id={null} creating title phone={false} />);
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  expect((form.getByLabelText('Name') as HTMLInputElement).value).toBe('Nightly dependency check');
  expect((form.getByLabelText('When') as HTMLInputElement).value).toBe('0 3 * * *');
  expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(['POST /api/v1/fleets/zulu/routines/draft']);
  expect(routines).toEqual([]);
  fireEvent.click(save());
  await waitFor(() => expect(routines.map((r) => r.name)).toEqual(['Nightly dependency check']));
  await waitFor(() => expect(screen.getAllByRole('listitem').some((li) => li.textContent?.includes('Nightly dependency check'))).toBe(true));
});

test('with no proven judge the Routines tab hides Describe a routine, keeps New routine and says why', async () => {
  vi.stubGlobal('fetch', vi.fn(async (path: string) => (path.endsWith('/harness-capabilities') ? json({ fleets: [{ key: 'zulu', name: 'Zulu', judge: { harness: null, reason: 'No harness is proven for judge calls' }, harnesses: [] }] }) : path.endsWith('/routines') ? json({ routines: [] }) : json({ runs: [], entries: [], skills: [] }))));
  page();
  await screen.findByText(/Judge calls: off/);
  expect(screen.queryByRole('textbox', { name: 'Describe a routine' })).toBeNull();
  expect(screen.getByRole('link', { name: 'New routine' })).toBeTruthy();
});

test('a routine filled in through the form is created with what the form shows, and the cron field reads back as a sentence', async () => {
  page(null, true);
  await screen.findByRole('form', { name: 'Routine' });
  const form = within(editor());
  await waitFor(() => expect((form.getByRole('radio', { name: 'Claude' }) as HTMLInputElement).checked).toBe(true));
  fireEvent.input(form.getByLabelText('Name'), { target: { value: 'Form sweep' } });
  fireEvent.input(form.getByLabelText('When'), { target: { value: '0 3 * * *' } });
  expect(form.getByText(/Daily/)).toBeTruthy();
  await waitFor(() => expect(form.getByRole('option', { name: /proj/ })).toBeTruthy());
  fireEvent.change(form.getByLabelText('Checkout'), { target: { value: '/h/proj' } });
  fireEvent.input(form.getByLabelText('Text'), { target: { value: 'go' } });
  fireEvent.click(form.getByRole('radio', { name: 'Act' }));
  fireEvent.click(save());

  await waitFor(() => expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(['POST /api/v1/fleets/zulu/routines']));
  expect(calls[0]!.body).toEqual({ name: 'Form sweep', enabled: true, when: '0 3 * * *', do: { launch: { harness: 'claude', dir: '/h/proj', text: 'go' } }, before_acting: 'act', notify: false });
  await waitFor(() => expect(location.pathname).toBe('/app/routines/r-new'));
});

test('Save before a Checkout is chosen shows the error under Checkout, above Text, and choosing one clears it', async () => {
  const fetchStored = globalThis.fetch;
  vi.stubGlobal('fetch', vi.fn(async (path: string, init?: RequestInit) => {
    const body = init?.body ? (JSON.parse(String(init.body)) as Partial<Routine>) : undefined;
    if (init?.method === 'POST' && path.endsWith('/routines') && !body?.do?.launch?.dir) return json({ error: 'invalid routine: Checkout is required' }, 400);
    return fetchStored(path, init);
  }));
  page(null, true);
  await screen.findByRole('form', { name: 'Routine' });
  const form = within(editor());
  await waitFor(() => expect((form.getByRole('radio', { name: 'Claude' }) as HTMLInputElement).checked).toBe(true));
  fireEvent.input(form.getByLabelText('Name'), { target: { value: 'Form sweep' } });
  fireEvent.input(form.getByLabelText('When'), { target: { value: '0 3 * * *' } });
  fireEvent.click(save());

  const alert = await form.findByRole('alert');
  expect(alert.textContent).toContain('Checkout');
  const follows = (a: Node, b: Node) => (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0;
  expect(follows(form.getByLabelText('Checkout'), alert)).toBe(true);
  expect(follows(alert, form.getByLabelText('Text'))).toBe(true);

  await waitFor(() => expect(form.getByRole('option', { name: /proj/ })).toBeTruthy());
  fireEvent.change(form.getByLabelText('Checkout'), { target: { value: '/h/proj' } });
  await waitFor(() => expect(form.queryByRole('alert')).toBeNull());
});

test('Save shows what the server stored, a second Save does not revert it, and the row switch turns the routine off with the editor following', async () => {
  routines = [stored({ last_run: { at: new Date().toISOString(), outcome: 'launched' } })];
  page('r1');
  const when = (await screen.findByLabelText('When')) as HTMLInputElement;
  const row = screen.getByRole('listitem');
  expect(within(row).getByRole('link').textContent).not.toContain('Never');
  fireEvent.input(when, { target: { value: '15 9 * * 1-5' } });
  fireEvent.click(save());
  await waitFor(() => expect(row.textContent).toContain('09:15'));
  expect(row.textContent).not.toContain('Never');
  expect(calls.map((c) => c.method)).toEqual(['PUT']);
  expect(calls[0]!.body).toMatchObject({ when: '15 9 * * 1-5', before_acting: 'act' });

  expect((screen.getByLabelText('When') as HTMLInputElement).value).toBe('15 9 * * 1-5');
  await waitFor(() => expect(save().getAttribute('aria-busy')).not.toBe('true'));
  fireEvent.click(save());
  await waitFor(() => expect(calls.map((c) => c.method)).toEqual(['PUT', 'PUT']));
  await waitFor(() => expect(save().getAttribute('aria-busy')).not.toBe('true'));
  expect((screen.getByLabelText('When') as HTMLInputElement).value).toBe('15 9 * * 1-5');
  expect((within(editor()).getByRole('radio', { name: 'Act' }) as HTMLInputElement).checked).toBe(true);

  fireEvent.click(within(row).getByRole('switch'));
  await waitFor(() => expect(within(row).getByRole('switch').getAttribute('aria-checked')).toBe('false'));
  expect(calls.at(-1)).toMatchObject({ method: 'POST', path: '/api/v1/fleets/zulu/routines/r1/off' });
  await waitFor(() => expect(within(editor()).getByRole('switch', { name: 'On' }).getAttribute('aria-checked')).toBe('false'));
});

test('a stored New in view routine shows as broken with a note, and the editor offers events but no New in view', async () => {
  sources.value = [{ key: 'gh', name: 'GitHub', fleet: 'zulu', views: [], events: [{ kind: 'review_requested', title: 'Review requested of you' }, { kind: 'new_in_view', title: 'New in Bugs', panel: 'bugs' }] }] as never;
  routines = [stored({ id: 'r-view', name: 'Watch the Bugs view', when: '', on: { source: 'gh', kind: 'new_in_view', panel: 'bugs' } })];
  const { unmount } = page('r-view');
  const row = (await screen.findAllByRole('listitem')).find((li) => li.textContent?.includes('Watch the Bugs view'))!;
  expect(row.textContent).toMatch(/broken/i);
  expect(row.textContent).toMatch(/removed/i);
  expect(editor().textContent).toMatch(/trigger was removed/i);
  unmount();

  page(null, true);
  await screen.findByRole('form', { name: 'Routine' });
  fireEvent.click(within(editor()).getByRole('radio', { name: 'An event' }));
  const picker = within(editor()).getByLabelText('When');
  const options = within(picker).getAllByRole('option').map((o) => o.textContent);
  expect(options).toContain('Review requested of you');
  expect(options.filter((o) => /New in/.test(o ?? ''))).toEqual([]);
});

test('the daily worker cap notice names the spend once it is reached, and the page says nothing before', async () => {
  spend = { today_usd: 1.5, cap_usd: 2, reached: false };
  const { unmount } = page();
  await screen.findByRole('heading', { name: 'Routines' });
  await waitFor(() => expect(screen.queryByText('Loading…')).toBeNull());
  expect(screen.queryByRole('status')).toBeNull();
  expect(document.body.textContent).not.toMatch(/stop at A1|proposal/i);
  unmount();

  spend = { today_usd: 2, cap_usd: 2, reached: true };
  page();
  const note = await screen.findByText(/\$2\.00/);
  expect(note.textContent).toBe('Worker spend today is $2.00 of the $2.00 cap, so events stop at a proposal until the day turns.');
  expect(note.getAttribute('role')).toBe('status');
});
