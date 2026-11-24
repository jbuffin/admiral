import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Capability, Fleet, FleetCapabilities } from './api';
import { capabilities, capabilitiesFailed, capabilitiesLoaded } from './capabilities';
import { RoutinesPage } from './RoutinesPage';
import type { Routine, RoutineSpend } from './routines';
import { loc } from './router';
import { sources } from './shipsLink';

const fleet = { key: 'zulu', name: 'Zulu', docks: [{ harness: 'claude', models: ['opus', 'sonnet', 'fable', 'haiku', 'claude-opus-4-8'], models_source: 'listed' }], limits: {}, sentences: [] } as unknown as Fleet;
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
let zone: string | undefined;
let calls: Call[];
let draftReply: () => Response | Promise<Response>;
const start = { value: loc.value };
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
  zone = 'America/Denver';
  calls = [];
  draftReply = () => json({ error: 'not found' }, 404);
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
      if (method === 'GET' && path.endsWith('/routines')) return json({ routines, spend, zone });
      calls.push({ method, path, body });
      if (method === 'GET' && path.includes('/routine-drafts/')) return draftReply();
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
afterEach(() => {
  vi.unstubAllGlobals();
  loc.value = start.value;
});

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
  expect(await form.findByText('At 03:00 (America/Denver)')).toBeTruthy();
  await waitFor(() => expect(form.getByRole('option', { name: /proj/ })).toBeTruthy());
  fireEvent.change(form.getByLabelText('Checkout'), { target: { value: '/h/proj' } });
  fireEvent.input(form.getByLabelText('Text'), { target: { value: 'go' } });
  fireEvent.click(form.getByRole('radio', { name: 'Act' }));
  fireEvent.click(save());

  await waitFor(() => expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(['POST /api/v1/fleets/zulu/routines']));
  expect(calls[0]!.body).toEqual({ name: 'Form sweep', enabled: true, when: '0 3 * * *', do: { launch: { harness: 'claude', dir: '/h/proj', text: 'go' } }, before_acting: 'act', notify: false });
  await waitFor(() => expect(location.pathname).toBe('/app/routines/r-new'));
});

test("the Routine editor's Model select offers the dock's list, and Other… saves the typed model", async () => {
  page(null, true);
  await screen.findByRole('form', { name: 'Routine' });
  const form = within(editor());
  await waitFor(() => expect((form.getByRole('radio', { name: 'Claude' }) as HTMLInputElement).checked).toBe(true));
  expect(within(form.getByLabelText('Model')).getAllByRole('option').map((o) => o.textContent)).toEqual(['Default', 'opus', 'sonnet', 'fable', 'haiku', 'claude-opus-4-8', 'Other…']);

  fireEvent.input(form.getByLabelText('Name'), { target: { value: 'Form sweep' } });
  fireEvent.input(form.getByLabelText('When'), { target: { value: '0 3 * * *' } });
  await waitFor(() => expect(form.getByRole('option', { name: /proj/ })).toBeTruthy());
  fireEvent.change(form.getByLabelText('Checkout'), { target: { value: '/h/proj' } });
  fireEvent.input(form.getByLabelText('Text'), { target: { value: 'go' } });
  fireEvent.change(form.getByLabelText('Model'), { target: { value: form.getByRole('option', { name: 'Other…' }).getAttribute('value')! } });
  fireEvent.input(form.getByLabelText('Other model'), { target: { value: 'my-model' } });
  fireEvent.click(save());

  await waitFor(() => expect(calls.map((c) => c.method)).toEqual(['POST']));
  expect(calls[0]!.body).toMatchObject({ do: { launch: { harness: 'claude', dir: '/h/proj', text: 'go', model: 'my-model' } } });
});

test('a routine saved with a model the list no longer has opens on Other… with that model and saves it unchanged', async () => {
  routines = [stored({ do: { launch: { harness: 'claude', dir: '/h/proj', text: 'go', model: 'old-model' } } })];
  page('r1');
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  const select = form.getByLabelText('Model') as HTMLSelectElement;
  expect(select.selectedOptions[0]!.textContent).toBe('Other…');
  expect((form.getByLabelText('Other model') as HTMLInputElement).value).toBe('old-model');

  fireEvent.input(form.getByLabelText('When'), { target: { value: '15 9 * * 1-5' } });
  fireEvent.click(save());
  await waitFor(() => expect(calls.map((c) => c.method)).toEqual(['PUT']));
  expect(calls[0]!.body).toMatchObject({ do: { launch: { model: 'old-model' } } });
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

test('picking Post a fleet digest swaps the launch fields for optional extra words, and Save creates a digest routine', async () => {
  page(null, true);
  await screen.findByRole('form', { name: 'Routine' });
  const form = within(editor());
  await waitFor(() => expect((form.getByRole('radio', { name: 'Claude' }) as HTMLInputElement).checked).toBe(true));
  fireEvent.click(form.getByRole('radio', { name: 'Post a fleet digest' }));
  expect(form.queryByLabelText('Text')).toBeNull();
  expect(form.queryByLabelText('Checkout')).toBeNull();
  expect(form.queryByRole('radio', { name: 'Claude' })).toBeNull();
  fireEvent.input(form.getByLabelText('Name'), { target: { value: 'Morning digest' } });
  fireEvent.input(form.getByLabelText('When'), { target: { value: '0 8 * * *' } });
  fireEvent.input(form.getByLabelText(/extra words/i), { target: { value: 'lead with PRs' } });
  fireEvent.click(form.getByRole('radio', { name: 'Act' }));
  fireEvent.click(save());

  await waitFor(() => expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(['POST /api/v1/fleets/zulu/routines']));
  expect(calls[0]!.body).toEqual({ name: 'Morning digest', enabled: true, when: '0 8 * * *', do: { digest: { extra: 'lead with PRs' } }, before_acting: 'act', notify: false });
});

test('a new digest routine defaults Before acting to Act, since a digest cannot act and never asks first', async () => {
  page(null, true);
  await screen.findByRole('form', { name: 'Routine' });
  const form = within(editor());
  fireEvent.click(form.getByRole('radio', { name: 'Post a fleet digest' }));
  fireEvent.input(form.getByLabelText('Name'), { target: { value: 'Morning digest' } });
  fireEvent.input(form.getByLabelText('When'), { target: { value: '0 8 * * *' } });
  fireEvent.click(save());
  await waitFor(() => expect(calls.map((c) => c.method)).toEqual(['POST']));
  expect((calls[0]!.body as { before_acting: string }).before_acting).toBe('act');
});

test('a digest with the extra words left blank is saved as an empty digest', async () => {
  page(null, true);
  await screen.findByRole('form', { name: 'Routine' });
  const form = within(editor());
  fireEvent.click(form.getByRole('radio', { name: 'Post a fleet digest' }));
  fireEvent.input(form.getByLabelText('Name'), { target: { value: 'Morning digest' } });
  fireEvent.input(form.getByLabelText('When'), { target: { value: '0 8 * * *' } });
  fireEvent.click(save());
  await waitFor(() => expect(calls.map((c) => c.method)).toEqual(['POST']));
  expect((calls[0]!.body as { do: unknown }).do).toEqual({ digest: {} });
});

test('a digest runs on a schedule only: the event choice is not offered while it is picked, and an event routine does not offer it', async () => {
  page(null, true);
  await screen.findByRole('form', { name: 'Routine' });
  const form = within(editor());
  fireEvent.click(form.getByRole('radio', { name: 'Post a fleet digest' }));
  expect(form.queryByRole('radio', { name: 'An event' })).toBeNull();
  expect(form.getByRole('radio', { name: 'A schedule' })).toBeTruthy();
  fireEvent.click(form.getByRole('radio', { name: 'Launch a ship' }));
  fireEvent.click(form.getByRole('radio', { name: 'An event' }));
  expect(form.queryByRole('radio', { name: 'Post a fleet digest' })).toBeNull();
});

test('a stored digest routine opens with the digest picked and its extra words, and its list row says what it does', async () => {
  routines = [stored({ name: 'Morning brief', do: { digest: { extra: 'lead with PRs' } } })];
  page('r1');
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  expect((form.getByRole('radio', { name: 'Post a fleet digest' }) as HTMLInputElement).checked).toBe(true);
  expect((form.getByLabelText(/extra words/i) as HTMLInputElement | HTMLTextAreaElement).value).toBe('lead with PRs');
  expect(screen.getByRole('listitem').textContent).toMatch(/digest/i);
});

test('a kept digest draft opens in the editor with the digest picked, and Save stores it', async () => {
  draftReply = linked({ do: { digest: { extra: 'lead with PRs' } } });
  link('?fleet=zulu&draft=d%2F1');
  page(null, true);
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  expect((form.getByRole('radio', { name: 'Post a fleet digest' }) as HTMLInputElement).checked).toBe(true);
  expect((form.getByLabelText(/extra words/i) as HTMLInputElement | HTMLTextAreaElement).value).toBe('lead with PRs');
  fireEvent.click(save());
  await waitFor(() => expect(routines.map((r) => r.do)).toEqual([{ digest: { extra: 'lead with PRs' } }]));
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

const GONE = /expired or can.t be found/;
const link = (search: string) => {
  history.replaceState(null, '', '/app/routines/new' + search);
  loc.value = { path: '/app/routines/new', search };
};
const linked = (over: Partial<Routine> = {}, problems: { field: string; message: string }[] | null = null) => () =>
  json({ id: 'd/1', draft: stored({ name: 'Linked sweep', when: '0 4 * * 1', before_acting: 'ask_first', ...over }), problems, expires_at: '2026-11-17T12:00:00Z' });

test('a draft link fills the editor from the kept draft with its first problem on its field, and Save stores it', async () => {
  draftReply = linked({}, [{ field: 'when', message: 'needs five parts' }]);
  link('?fleet=zulu&draft=d%2F1');
  page(null, true);
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  expect((form.getByLabelText('Name') as HTMLInputElement).value).toBe('Linked sweep');
  expect((form.getByLabelText('When') as HTMLInputElement).value).toBe('0 4 * * 1');
  expect((form.getByRole('radio', { name: 'Ask first' }) as HTMLInputElement).checked).toBe(true);
  expect(form.getByRole('alert').textContent).toContain('When needs five parts');
  expect(screen.queryByText(GONE)).toBeNull();
  expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(['GET /api/v1/fleets/zulu/routine-drafts/d%2F1']);
  expect(routines).toEqual([]);
  fireEvent.click(save());
  await waitFor(() => expect(routines.map((r) => r.name)).toEqual(['Linked sweep']));
  expect(calls.filter((c) => c.method === 'GET')).toHaveLength(1);
});

test('the editor slot shows the loading line, not an empty form, until the linked draft answers', async () => {
  let answer: (r: Response) => void = () => {};
  draftReply = () => new Promise<Response>((r) => (answer = r));
  link('?fleet=zulu&draft=d1');
  page(null, true);
  await screen.findByText(/No routines yet/);
  expect(screen.getByText('Loading…')).toBeTruthy();
  expect(screen.queryByRole('form', { name: 'Routine' })).toBeNull();
  answer(linked()());
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  expect((form.getByLabelText('Name') as HTMLInputElement).value).toBe('Linked sweep');
  expect(screen.queryByText('Loading…')).toBeNull();
});

test('an expired or unknown draft opens the empty form with a note saying so', async () => {
  link('?fleet=zulu&draft=gone');
  page(null, true);
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  expect((form.getByLabelText('Name') as HTMLInputElement).value).toBe('');
  const note = await screen.findByText(GONE);
  expect(note.getAttribute('role')).toBe('status');
  expect(note.textContent).toMatch(/describe the routine again or fill in the form/i);
  expect(routines).toEqual([]);
});

test('another failure shows its error over the empty form', async () => {
  draftReply = () => json({ error: 'the store is down' }, 500);
  link('?fleet=zulu&draft=d1');
  page(null, true);
  const form = await screen.findByRole('form', { name: 'Routine' });
  expect((within(form).getByLabelText('Name') as HTMLInputElement).value).toBe('');
  expect((await screen.findByText(/the store is down/)).getAttribute('role')).toBe('alert');
  expect(screen.queryByText(GONE)).toBeNull();
});

test('a draft with markup in it shows as text and adds no elements', async () => {
  draftReply = linked({ name: '<img src=x onerror=alert(1)>' });
  link('?fleet=zulu&draft=d1');
  const view = page(null, true);
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  expect((form.getByLabelText('Name') as HTMLInputElement).value).toBe('<img src=x onerror=alert(1)>');
  expect(view.container.querySelector('img[src="x"]')).toBeNull();
});

test('describing a routine while on a draft link shows the described draft, and a late linked answer is dropped', async () => {
  let answer: (r: Response) => void = () => {};
  draftReply = () => new Promise<Response>((r) => (answer = r));
  link('?fleet=zulu&draft=d1');
  const view = page(null, true);
  const describe = await screen.findByRole('textbox', { name: 'Describe a routine' });
  await waitFor(() => expect(calls.map((c) => c.path)).toEqual(['/api/v1/fleets/zulu/routine-drafts/d1']));
  fireEvent.input(describe, { target: { value: 'every night at 3, check for outdated dependencies' } });
  fireEvent.click(screen.getByRole('button', { name: 'Draft' }));
  await waitFor(() => expect(location.search).not.toContain('draft='));
  view.rerender(<RoutinesPage fleet={fleet} id={null} creating title phone={false} />);
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  expect((form.getByLabelText('Name') as HTMLInputElement).value).toBe('Nightly dependency check');
  await act(async () => {
    answer(linked()());
  });
  expect((within(editor()).getByLabelText('Name') as HTMLInputElement).value).toBe('Nightly dependency check');
  expect(screen.queryByText(GONE)).toBeNull();
});

const visit = async (search: string, creating = true, path = '/app/routines/new') => {
  history.replaceState(null, '', path + search);
  await act(async () => {
    loc.value = { path, search };
  });
};

test('going back to a draft link in the same mount shows the loading line until its answer', async () => {
  draftReply = linked();
  link('?fleet=zulu&draft=d1');
  const view = page(null, true);
  await screen.findByRole('form', { name: 'Routine' });
  await visit('?fleet=zulu', false, '/app/routines/r1');
  view.rerender(<RoutinesPage fleet={fleet} id="r1" creating={false} title phone={false} />);
  let answer: (r: Response) => void = () => {};
  draftReply = () => new Promise<Response>((r) => (answer = r));
  await visit('?fleet=zulu&draft=d1');
  view.rerender(<RoutinesPage fleet={fleet} id={null} creating title phone={false} />);
  expect(screen.getByText('Loading…')).toBeTruthy();
  expect(screen.queryByRole('form', { name: 'Routine' })).toBeNull();
  await waitFor(() => expect(calls).toHaveLength(2));
  await act(async () => {
    answer(linked({ name: 'Second visit' })());
  });
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  expect((form.getByLabelText('Name') as HTMLInputElement).value).toBe('Second visit');
});

test('a link that is gone shows the note over the empty form after an earlier linked draft', async () => {
  draftReply = linked();
  link('?fleet=zulu&draft=d1');
  page(null, true);
  await screen.findByRole('form', { name: 'Routine' });
  draftReply = () => json({ error: 'not found' }, 404);
  await visit('?fleet=zulu&draft=gone');
  expect(await screen.findByText(GONE)).toBeTruthy();
  expect((within(editor()).getByLabelText('Name') as HTMLInputElement).value).toBe('');
});

test('a link that fails shows the error over the empty form after a described draft', async () => {
  link('?fleet=zulu');
  const view = page(null, true);
  fireEvent.input(await screen.findByRole('textbox', { name: 'Describe a routine' }), { target: { value: 'every night at 3, check for outdated dependencies' } });
  fireEvent.click(screen.getByRole('button', { name: 'Draft' }));
  await waitFor(() => expect((within(editor()).getByLabelText('Name') as HTMLInputElement).value).toBe('Nightly dependency check'));
  draftReply = () => json({ error: 'the store is down' }, 500);
  await visit('?fleet=zulu&draft=d1');
  view.rerender(<RoutinesPage fleet={fleet} id={null} creating title phone={false} />);
  expect((await screen.findByText(/the store is down/)).getAttribute('role')).toBe('alert');
  expect((within(editor()).getByLabelText('Name') as HTMLInputElement).value).toBe('');
});

test('New routine drops a late linked answer and keeps the blank form', async () => {
  let answer: (r: Response) => void = () => {};
  draftReply = () => new Promise<Response>((r) => (answer = r));
  link('?fleet=zulu&draft=d1');
  page(null, true);
  await screen.findByText(/No routines yet/);
  fireEvent.click(screen.getByRole('link', { name: 'New routine' }));
  await waitFor(() => expect(location.search).not.toContain('draft='));
  await act(async () => {
    answer(linked()());
  });
  expect((within(editor()).getByLabelText('Name') as HTMLInputElement).value).toBe('');
  expect(screen.queryByText(GONE)).toBeNull();
});

test('a dot segment as the draft id is gone without a request', async () => {
  link('?fleet=zulu&draft=..');
  page(null, true);
  expect(await screen.findByText(GONE)).toBeTruthy();
  expect(calls).toEqual([]);
});

test('the When field describes the schedule under it as the engineer types, in the server zone', async () => {
  page(null, true);
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  const when = form.getByLabelText('When');
  fireEvent.input(when, { target: { value: '0 9 * * 1-5' } });
  expect(await form.findByText('At 09:00, Monday through Friday (America/Denver)')).toBeTruthy();
  fireEvent.input(when, { target: { value: '*/15 * * * *' } });
  expect(await form.findByText('Every 15 minutes (America/Denver)')).toBeTruthy();
  expect(form.queryByText(/Monday through Friday/)).toBeNull();
});

test('an invalid When says so in plain words instead of a description, without raising an alert', async () => {
  page(null, true);
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  fireEvent.input(form.getByLabelText('When'), { target: { value: '0 25 * * *' } });
  expect(await form.findByText('hour: 25 is outside 0-23')).toBeTruthy();
  expect(form.queryByText(/^At /)).toBeNull();
  expect(form.queryByRole('alert')).toBeNull();
  fireEvent.input(form.getByLabelText('When'), { target: { value: '0 9 * * *' } });
  expect(await form.findByText('At 09:00 (America/Denver)')).toBeTruthy();
  expect(form.queryByText('hour: 25 is outside 0-23')).toBeNull();
});

test('the empty When keeps a hint that names the server zone, not this machine', async () => {
  page(null, true);
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  expect(await form.findByText(/Five fields: minute hour day month weekday.*America\/Denver/)).toBeTruthy();
  expect(form.queryByText(/this machine/)).toBeNull();
});

test('with no zone from the server the description has no zone suffix', async () => {
  zone = undefined;
  page(null, true);
  const form = within(await screen.findByRole('form', { name: 'Routine' }));
  fireEvent.input(form.getByLabelText('When'), { target: { value: '0 9 * * *' } });
  expect(await form.findByText('At 09:00')).toBeTruthy();
});

test('each listed schedule reads in plain English with the server zone, and a hand-written bad one does not crash the row', async () => {
  routines = [
    stored({ id: 'r1', name: 'Weekday sweep', when: '0 9 * * 1-5' }),
    stored({ id: 'r2', name: 'Morning digest', when: '0 8 * * *', do: { digest: {} } }),
    stored({ id: 'r3', name: 'Hand edited', when: '60 * * * *' }),
  ];
  page();
  expect(await screen.findByText('At 09:00, Monday through Friday (America/Denver)')).toBeTruthy();
  expect(screen.getByText('Fleet digest, At 08:00 (America/Denver)')).toBeTruthy();
  expect(screen.getByText('minute: 60 is outside 0-59')).toBeTruthy();
});
