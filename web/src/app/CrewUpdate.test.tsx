import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Fleet } from './api';
import { CrewPage } from './CrewPage';
import { loc } from './router';

const start = { value: loc.value, url: location.href };

afterEach(() => {
  vi.unstubAllGlobals();
  loc.value = start.value;
  history.replaceState({}, '', start.url);
});

const fleet = { key: 'zulu', name: 'Zulu', docks: [{ harness: 'claude', models: ['sonnet'], models_source: 'listed' }] } as unknown as Fleet;

const sharedFrom = (detached = false) => ({ owner: 'hubot', repo: 'crew', commit: 'a'.repeat(40), tag: 'v1', detached });
const member = (name: string, over: Record<string, unknown> = {}) => ({
  name, role: 'triager', 'based-on': null, skills: [], model: null, harness: null, 'pick-me-when': `Pick ${name}.`, instructions: `Work as ${name}.`,
  layer: 'fleet', customized: false, notes: { fleet: [], repos: {} }, shared: sharedFrom(), shared_url: null, published: false, sidecar_unreadable: false, update: 'none', ...over,
});

const preview = (over: Record<string, unknown> = {}) => ({
  name: 'scout', repo: 'hubot/crew', path: 'crew/scout.md', current_bytes: 'Work as scout.\n', current_sha256: 'c'.repeat(64), bytes: 'Work as scout, better.\n', sha256: 'd'.repeat(64),
  commit: 'b'.repeat(40), tag: 'v2', blob: 'e'.repeat(40), card: { name: 'scout', role: 'triager', 'pick-me-when': 'Pick scout v2.' },
  missing_skills: [], scan: { status: 'clean' }, up_to_date: false, ...over,
});

interface Call {
  method: string;
  path: string;
  body?: Record<string, unknown>;
}

interface Stub {
  members: unknown[];
  updates?: () => Promise<Response>;
  reply?: (c: Call) => Response | undefined;
}

// A fake server for the crew list, the update states and the update routes; every non-GET and every update call is recorded.
function server(state: Stub) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      const c: Call = { method, path, body: init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined };
      if (path.includes('/crew/updates')) {
        calls.push(c);
        return state.updates ? state.updates() : new Response(JSON.stringify({ updates: {} }));
      }
      if (method === 'GET' && path.includes('/crew/sharing')) return new Response(JSON.stringify({ crew_sharing: { enabled: true, sources: [] }, connected: true, login: 'octocat' }));
      if (method === 'GET' && path.startsWith('/api/v1/fleets/zulu/crew')) return new Response(JSON.stringify({ harness: 'claude', members: state.members, unclaimed: [] }));
      if (method === 'GET') return new Response(JSON.stringify({ skills: [] }));
      calls.push(c);
      return state.reply?.(c) ?? new Response('{}');
    }),
  );
  return calls;
}

const page = (name: string | null = null, phone = false) => render(<CrewPage fleet={fleet} name={name} title phone={phone} />);
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const updateButton = () => screen.findByRole('button', { name: 'Update', exact: true } as never);

test('the page says it is checking while the update states load', async () => {
  server({ members: [member('scout')], updates: () => new Promise<Response>(() => {}) });
  page();
  expect(await screen.findByText('Checking for updates')).toBeTruthy();
});

test('an update state shows as words on the list row and on the detail header', async () => {
  const states = { scout: 'available', mate: 'removed', ada: 'none' };
  server({
    members: [member('scout', { update: 'available' }), member('mate', { update: 'removed' }), member('ada', { shared: sharedFrom(true), update: 'none' })],
    updates: async () => json({ updates: states }),
  });
  const { unmount } = page();
  const row = (n: string) => screen.findByRole('link', { name: new RegExp(n) }).then((a) => a.closest('li')!);
  expect(within(await row('scout')).getByText('Update available')).toBeTruthy();
  expect(within(await row('mate')).getByText('Removed upstream')).toBeTruthy();
  expect(within(await row('ada')).getByText('Edited here')).toBeTruthy();
  unmount();

  server({ members: [member('scout', { update: 'available' })], updates: async () => json({ updates: { scout: 'available' } }) });
  page('scout', true);
  const header = (await screen.findByRole('heading', { name: 'scout', level: 2 })).closest('header')!;
  expect(within(header).getByText('Update available')).toBeTruthy();
});

test('a detached member says why it gets no updates and keeps who shared it', async () => {
  server({ members: [member('ada', { shared: sharedFrom(true), shared_url: 'https://github.com/hubot/crew/blob/x/crew/ada.md' })] });
  page('ada', true);
  expect(await screen.findByText('Edited here, so it no longer gets updates from hubot/crew.')).toBeTruthy();
  expect(screen.getByText('Shared by hubot')).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Update', exact: true } as never)).toBeNull();
});

test('Update opens the preview with the repo, short commit, tag and the diff, and previews without writing', async () => {
  const calls = server({
    members: [member('scout', { update: 'available' })],
    updates: async () => json({ updates: { scout: 'available' } }),
    reply: (c) => (c.path.endsWith('/members/scout/update/preview') ? json(preview()) : undefined),
  });
  page('scout', true);
  fireEvent.click(await updateButton());
  expect(await screen.findByText(/hubot\/crew/)).toBeTruthy();
  expect(await screen.findByText(/bbbbbbb/)).toBeTruthy();
  expect(screen.getByText(/v2/)).toBeTruthy();
  expect(screen.getByText(/Work as scout, better\./)).toBeTruthy();
  expect(calls.filter((c) => c.method === 'POST').map((c) => c.path)).toEqual(['/api/v1/fleets/zulu/crew/members/scout/update/preview']);
});

test('the preview lists missing skills and a scan warning, and still offers Apply', async () => {
  server({
    members: [member('scout', { update: 'available' })],
    updates: async () => json({ updates: { scout: 'available' } }),
    reply: () => json(preview({ missing_skills: ['tdd'], scan: { status: 'findings', findings: [{ rule: 'generic-api-key', line: 3 }] } })),
  });
  page('scout', true);
  fireEvent.click(await updateButton());
  expect(await screen.findByText(/tdd/)).toBeTruthy();
  expect(screen.getByText(/generic-api-key|secret/i)).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Apply' })).toBeTruthy();
});

test('a refused preview shows the server message and no Apply button', async () => {
  server({
    members: [member('scout', { update: 'available' })],
    updates: async () => json({ updates: { scout: 'available' } }),
    reply: () => json({ error: 'scout.md has a hidden direction mark.' }, 422),
  });
  page('scout', true);
  fireEvent.click(await updateButton());
  expect(await screen.findByText('scout.md has a hidden direction mark.')).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Apply' })).toBeNull();
});

test('a preview that is up to date says so and offers no Apply', async () => {
  server({
    members: [member('scout', { update: 'available' })],
    updates: async () => json({ updates: { scout: 'available' } }),
    reply: () => json(preview({ up_to_date: true })),
  });
  page('scout', true);
  fireEvent.click(await updateButton());
  expect(await screen.findByText(/up to date/i)).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Apply' })).toBeNull();
});

test('Apply posts the commit and both digests from the preview once, then the badge is gone', async () => {
  const state: Stub = {
    members: [member('scout', { update: 'available' })],
    updates: async () => json({ updates: { scout: 'available' } }),
    reply: (c) => {
      if (c.path.endsWith('/update/preview')) return json(preview());
      if (c.path.endsWith('/members/scout/update')) {
        state.members = [member('scout', { update: 'none', shared: { ...sharedFrom(), commit: 'b'.repeat(40), tag: 'v2' } })];
        state.updates = async () => json({ updates: { scout: 'none' } });
        return json({});
      }
      return undefined;
    },
  };
  const calls = server(state);
  page('scout', true);
  fireEvent.click(await updateButton());
  fireEvent.click(await screen.findByRole('button', { name: 'Apply' }));
  await waitFor(() => expect(screen.queryByText('Update available')).toBeNull());
  const applies = calls.filter((c) => c.method === 'POST' && c.path.endsWith('/members/scout/update'));
  expect(applies).toHaveLength(1);
  expect(applies[0].body).toEqual({ commit: 'b'.repeat(40), sha256: 'd'.repeat(64), current_sha256: 'c'.repeat(64) });
});

test('a changed conflict shows the server message and reloads the member', async () => {
  server({
    members: [member('scout', { update: 'available' })],
    updates: async () => json({ updates: { scout: 'available' } }),
    reply: (c) => (c.path.endsWith('/update/preview') ? json(preview()) : json({ error: 'The member changed since the preview. Preview it again.', kind: 'changed' }, 409)),
  });
  const listLoads = () => (fetch as unknown as { mock: { calls: [string, RequestInit?][] } }).mock.calls.filter(([p, i]) => (i?.method ?? 'GET') === 'GET' && /\/crew(\?|$)/.test(p)).length;
  page('scout', true);
  fireEvent.click(await updateButton());
  const apply = await screen.findByRole('button', { name: 'Apply' });
  const before = listLoads();
  fireEvent.click(apply);
  expect(await screen.findByText('The member changed since the preview. Preview it again.')).toBeTruthy();
  await waitFor(() => expect(listLoads()).toBeGreaterThan(before));
});

test('editing an attached member asks first and names the repo; a detached member edits straight away', async () => {
  server({ members: [member('scout')] });
  const first = page('scout', true);
  fireEvent.click(await screen.findByRole('button', { name: 'Edit', exact: true } as never));
  expect(await screen.findByText("Editing detaches this member from hubot/crew. It won't get updates.")).toBeTruthy();
  first.unmount();

  server({ members: [member('ada', { shared: sharedFrom(true) })] });
  page('ada', true);
  fireEvent.click(await screen.findByRole('button', { name: 'Edit', exact: true } as never));
  expect(screen.queryByText(/Editing detaches this member/)).toBeNull();
});

test('editing a member that was never imported asks nothing', async () => {
  server({ members: [member('plain', { shared: null, update: 'none' })] });
  page('plain', true);
  fireEvent.click(await screen.findByRole('button', { name: 'Edit', exact: true } as never));
  expect(screen.queryByText(/Editing detaches this member/)).toBeNull();
});
