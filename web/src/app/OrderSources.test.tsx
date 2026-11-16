import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet, Source } from './api';
import { fleets } from './fleets';
import { OrderSources } from './OrderSources';

const zulu = { key: 'zulu', name: 'Westwind', color: '#336699', ink: '#fff' } as unknown as Fleet;
const alpha = { key: 'alpha', name: 'Homeport', color: '#993366', ink: '#fff' } as unknown as Fleet;
const plugin = (key: string, over: Partial<Source> = {}): Source => ({ key, name: key, plugin: key, fleet: 'zulu', icon_url: '', has_schema: false, views: [], state: 'ok', error: null, warnings: [], ...over });

let sources: Source[];
let calls: string[];
let failMove: string | null;
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

beforeEach(() => {
  sources = [plugin('testplug'), plugin('github', { plugin: 'github' })];
  calls = [];
  failMove = null;
  fleets.value = [zulu, alpha];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined;
      if (path.endsWith('/plugins')) return json([{ name: 'github', description: '', icon_url: '', valid: true, error: '' }, { name: 'testplug', description: '', icon_url: '', valid: true, error: '' }, { name: 'broken', description: '', icon_url: '', valid: false, error: 'bad' }]);
      if (method === 'GET' && path.endsWith('/sources')) return json(sources);
      if (path.endsWith('/settings')) return json({ name: '', settings: {} });
      calls.push(`${method} ${path.replace('/api/v1', '')}${body ? ' ' + JSON.stringify(body) : ''}`);
      if (method === 'POST' && path.endsWith('/sources')) {
        sources = [...sources, plugin(String(body!.plugin) + '-2', { plugin: String(body!.plugin), fleet: String(body!.fleet) })];
        return json(sources.at(-1));
      }
      const key = /\/sources\/([^/]+)$/.exec(path)?.[1];
      if (method === 'PUT') {
        if (failMove) return json({ error: failMove }, 400);
        sources = sources.map((s) => (s.key === key ? { ...s, ...(body as Partial<Source>) } : s));
        return json(sources.find((s) => s.key === key));
      }
      if (method === 'DELETE') {
        sources = sources.filter((s) => s.key !== key);
        return new Response(null, { status: 204 });
      }
      return json({});
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

const card = (name: string) => screen.getByRole('region', { name });

test('lists only the fleet’s plugin sources, never its GitHub source', async () => {
  render(<OrderSources fleet={zulu} />);
  await screen.findByRole('region', { name: 'testplug' });
  expect(screen.queryByRole('region', { name: 'github' })).toBeNull();
});

test('Add source offers only valid plugins other than GitHub and adds the pick to this fleet', async () => {
  render(<OrderSources fleet={zulu} />);
  await screen.findByRole('region', { name: 'testplug' });
  fireEvent.click(screen.getByRole('button', { name: 'Add source' }));
  const group = await screen.findByRole('group', { name: 'Add source' });
  await waitFor(() => expect(within(group).getAllByRole('option').map((o) => o.textContent)).toEqual(['testplug']));
  fireEvent.click(within(group).getByRole('button', { name: 'Add to Westwind' }));
  await screen.findByRole('region', { name: 'testplug-2' });
  expect(calls).toEqual(['POST /sources {"plugin":"testplug","fleet":"zulu"}']);
  expect(screen.queryByRole('group', { name: 'Add source' })).toBeNull();
});

test('Move to fleet sends only the fleet, and the source leaves this fleet’s list', async () => {
  render(<OrderSources fleet={zulu} />);
  await screen.findByRole('region', { name: 'testplug' });
  expect((within(card('testplug')).getByLabelText('Move to fleet') as HTMLSelectElement).value).toBe('alpha');
  fireEvent.click(within(card('testplug')).getByRole('button', { name: 'Move' }));
  await waitFor(() => expect(calls).toEqual(['PUT /sources/testplug {"fleet":"alpha"}']));
  await waitFor(() => expect(screen.queryByRole('region', { name: 'testplug' })).toBeNull());
  expect(screen.getByText('Westwind has no plugin sources.')).toBeTruthy();
});

test('a refused Move shows the error on the card and keeps the source', async () => {
  failMove = 'fleet alpha is full';
  sources = [plugin('plugfail')];
  render(<OrderSources fleet={zulu} />);
  await screen.findByRole('region', { name: 'plugfail' });
  fireEvent.click(within(card('plugfail')).getByRole('button', { name: 'Move' }));
  expect((await within(card('plugfail')).findByRole('alert')).textContent).toBe('fleet alpha is full');
});

test('Remove asks inside the page, Cancel backs out, and confirming deletes the source', async () => {
  render(<OrderSources fleet={zulu} />);
  await screen.findByRole('region', { name: 'testplug' });
  fireEvent.click(within(card('testplug')).getByRole('button', { name: 'Remove' }));
  await waitFor(() => expect(within(card('testplug')).getByRole('button', { name: 'Cancel' }).hasAttribute('disabled')).toBe(false));
  fireEvent.click(within(card('testplug')).getByRole('button', { name: 'Cancel' }));
  expect(calls).toEqual([]);
  await waitFor(() => expect(within(card('testplug')).queryByRole('group', { name: 'Remove testplug' })).toBeNull());

  await waitFor(() => expect(within(card('testplug')).getByRole('button', { name: 'Remove' }).hasAttribute('disabled')).toBe(false));
  fireEvent.click(within(card('testplug')).getByRole('button', { name: 'Remove' }));
  const confirm = await screen.findByRole('group', { name: 'Remove testplug' });
  await waitFor(() => expect(within(confirm).getByRole('button', { name: 'Remove source' }).hasAttribute('disabled')).toBe(false));
  fireEvent.click(within(confirm).getByRole('button', { name: 'Remove source' }));
  await waitFor(() => expect(calls).toEqual(['DELETE /sources/testplug']));
  await waitFor(() => expect(screen.queryByRole('region', { name: 'testplug' })).toBeNull());
});
