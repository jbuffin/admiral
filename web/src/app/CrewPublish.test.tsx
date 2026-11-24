import { fireEvent, render, screen, waitFor } from '@testing-library/preact';
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

const listed = (harness: string) => ({ harness, models: ['sonnet'], models_source: 'listed' });
const fleetWith = (crew_sharing: Record<string, unknown>) => ({ key: 'zulu', name: 'Zulu', docks: [listed('claude')], crew_sharing }) as unknown as Fleet;
const on = fleetWith({ enabled: true, share_repo: 'octocat/admiral-crew', sources: [] });
const off = fleetWith({ enabled: false, share_repo: '', sources: [] });
const noRepo = fleetWith({ enabled: true, share_repo: '', sources: [] });

const member = (name: string, over: Record<string, unknown> = {}) => ({ name, role: 'scribe', 'based-on': null, skills: [], model: null, harness: null, 'pick-me-when': 'Pick ' + name, instructions: 'Do it.', layer: 'fleet', customized: false, notes: { fleet: [], repos: {} }, shared: null, published: false, ...over });

interface Call {
  method: string;
  path: string;
  body?: Record<string, unknown>;
}

// A fake crew API: GET crew serves `members`; every other request is recorded and answered by `reply`, or with {}.
function server(members: unknown[], reply: (c: Call) => Response | undefined = () => undefined) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      const c: Call = { method, path, body: init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined };
      calls.push(c);
      const r = reply(c);
      if (r) return r;
      if (method === 'GET' && path.includes('/crew/skills')) return new Response(JSON.stringify({ skills: [] }));
      if (method === 'GET' && /\/fleets\/zulu\/crew(\?|$)/.test(path)) return new Response(JSON.stringify({ harness: 'claude', members, unclaimed: [] }));
      return new Response('{}');
    }),
  );
  return calls;
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const page = (name: string, of: Fleet = on) => render(<CrewPage fleet={of} name={name} title phone={false} />);
const posts = (calls: Call[]) => calls.filter((c) => c.method !== 'GET').map((c) => `${c.method} ${c.path.replace('/api/v1/fleets/zulu/crew', '')}`);

const clean = { path: 'crew/bosun.md', bytes: '---\nname: bosun\n---\n\nDo it.\n', sha256: 'abc', repo: 'octocat/admiral-crew', replaces_blob: null, published_bytes: null, scan: { state: 'clean', findings: [] } };

test('Share shows for a fleet member of a sharing fleet, and not for a built-in, an imported member or a fleet with sharing off', async () => {
  server([member('bosun'), member('mate', { layer: 'built-in' }), member('imp', { shared: { owner: 'a', repo: 'b' } })]);
  const { unmount } = page('bosun');
  expect(await screen.findByRole('button', { name: 'Share' })).toBeTruthy();
  unmount();
  page('mate');
  await screen.findByRole('button', { name: 'Customize for this fleet' });
  expect(screen.queryByRole('button', { name: 'Share' })).toBeNull();
});

test('an imported member and a fleet with sharing off show no Share button', async () => {
  server([member('imp', { shared: { owner: 'a', repo: 'b' } }), member('bosun')]);
  const { unmount } = page('imp');
  await screen.findByRole('button', { name: 'Make mine' });
  expect(screen.queryByRole('button', { name: 'Share' })).toBeNull();
  unmount();
  page('bosun', off);
  await screen.findByRole('button', { name: 'Edit' });
  expect(screen.queryByRole('button', { name: 'Share' })).toBeNull();
});

test('with no share repo yet Share opens the create-or-adopt dialog, and creating posts the name and then shares', async () => {
  const calls = server([member('bosun')], (c) => (c.path.endsWith('/sharing/repo') ? json({ share_repo: 'octocat/admiral-crew' }) : undefined));
  page('bosun', noRepo);
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  const name = (await screen.findByLabelText(/repo name/i)) as HTMLInputElement;
  expect(name.value).toBe('admiral-crew');
  fireEvent.click(screen.getByRole('button', { name: 'Create repo' }));
  await waitFor(() => expect(posts(calls)).toContain('POST /sharing/repo'));
  expect(calls.find((c) => c.path.endsWith('/sharing/repo'))!.body).toMatchObject({ name: 'admiral-crew' });
});

test('a taken repo name offers to use the existing repo, and using it posts that repo', async () => {
  let attempts = 0;
  const calls = server([member('bosun')], (c) => {
    if (!c.path.endsWith('/sharing/repo')) return undefined;
    attempts++;
    return attempts === 1 ? json({ error: 'octocat/admiral-crew already exists', use_existing: 'octocat/admiral-crew' }, 409) : json({ share_repo: 'octocat/admiral-crew' });
  });
  page('bosun', noRepo);
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Create repo' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Use octocat/admiral-crew' }));
  await waitFor(() => expect(calls.filter((c) => c.path.endsWith('/sharing/repo')).length).toBe(2));
  expect(calls.filter((c) => c.path.endsWith('/sharing/repo'))[1]!.body).toEqual({ repo: 'octocat/admiral-crew' });
});

test('Share previews the exact file, and Publish sends the preview back and reports the commit', async () => {
  const calls = server([member('bosun')], (c) => {
    if (c.path.endsWith('/share/preview')) return json(clean);
    if (c.path.endsWith('/members/bosun/share')) return json({ commit_url: 'https://github.com/octocat/admiral-crew/commit/abc123' });
    return undefined;
  });
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  expect(await screen.findByText(/crew\/bosun\.md/)).toBeTruthy();
  expect(screen.getByText(/octocat\/admiral-crew/)).toBeTruthy();
  expect(screen.getByText(/Do it\./)).toBeTruthy();
  expect(posts(calls)).toEqual(['POST /members/bosun/share/preview']);
  fireEvent.click(screen.getByRole('button', { name: 'Publish' }));
  await waitFor(() => expect(posts(calls)).toEqual(['POST /members/bosun/share/preview', 'POST /members/bosun/share']));
  expect(calls[calls.length - 1]!.body).toMatchObject({ sha256: 'abc', repo: 'octocat/admiral-crew', replaces_blob: null });
  expect(await screen.findByRole('link', { name: /commit/i })).toBeTruthy();
});

test('a published member shows a Published badge and an Unpublish button, and Share shows what changes since the published file', async () => {
  server([member('bosun', { published: true })], (c) => (c.path.endsWith('/share/preview') ? json({ ...clean, replaces_blob: 'blob1', published_bytes: '---\nname: bosun\n---\n\nOld.\n' }) : undefined));
  page('bosun');
  expect(await screen.findByText('Published')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Unpublish' })).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Share' }));
  await screen.findByText(/crew\/bosun\.md/);
  expect(document.querySelector('[data-kind="del"]')!.textContent).toContain('Old.');
  expect(document.querySelector('[data-kind="add"]')!.textContent).toContain('Do it.');
});

test('a member that is not published has no Published badge and no Unpublish button', async () => {
  server([member('bosun')]);
  page('bosun');
  await screen.findByRole('button', { name: 'Share' });
  expect(screen.queryByText('Published')).toBeNull();
  expect(screen.queryByRole('button', { name: 'Unpublish' })).toBeNull();
});

test('a hit blocks Publish and names the line and rule; the secret never shows', async () => {
  server([member('bosun')], (c) => (c.path.endsWith('/share/preview') ? json({ error: 'Secret scan found decoy-secret on line 6', scan: { state: 'hit', findings: [{ line: 6, rule: 'decoy-secret' }] } }, 422) : undefined));
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  expect(await screen.findByText(/decoy-secret/)).toBeTruthy();
  expect(screen.getByText(/line 6/)).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Publish' })).toBeNull();
});

test('with gitleaks missing Publish needs the opt-out box, which carries the warning, and sends skip_secret_scan', async () => {
  const calls = server([member('bosun')], (c) => {
    if (c.path.endsWith('/share/preview')) return json({ ...clean, scan: { state: 'missing', findings: [], install: 'brew install gitleaks' } });
    if (c.path.endsWith('/members/bosun/share')) return json({ commit_url: 'https://github.com/octocat/admiral-crew/commit/abc123' });
    return undefined;
  });
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  expect(await screen.findByText(/brew install gitleaks/)).toBeTruthy();
  expect(screen.getByText('This file goes public without a secret scan. Read it for keys, tokens and passwords before you publish.')).toBeTruthy();
  const publish = screen.getByRole('button', { name: 'Publish' }) as HTMLButtonElement;
  expect(publish.disabled).toBe(true);
  fireEvent.click(screen.getByRole('checkbox'));
  expect(publish.disabled).toBe(false);
  fireEvent.click(publish);
  await waitFor(() => expect(posts(calls)).toContain('POST /members/bosun/share'));
  expect(calls[calls.length - 1]!.body).toMatchObject({ skip_secret_scan: true });
});

test('a clean scan shows no opt-out box and sends no skip_secret_scan', async () => {
  const calls = server([member('bosun')], (c) => (c.path.endsWith('/share/preview') ? json(clean) : undefined));
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  await screen.findByText(/crew\/bosun\.md/);
  expect(screen.queryByRole('checkbox')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Publish' }));
  await waitFor(() => expect(posts(calls)).toContain('POST /members/bosun/share'));
  expect(calls[calls.length - 1]!.body).not.toHaveProperty('skip_secret_scan');
});

test('a refused preview shows the server message, and a refused Share keeps the dialog open with it', async () => {
  server([member('bosun')], (c) => {
    if (c.path.endsWith('/share/preview')) return json(clean);
    if (c.path.endsWith('/members/bosun/share')) return json({ error: 'The published file changed since the preview' }, 409);
    return undefined;
  });
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Publish' }));
  expect((await screen.findByRole('alert')).textContent).toContain('The published file changed since the preview');
  expect(screen.getByRole('button', { name: 'Publish' })).toBeTruthy();
});

test('Unpublish asks with the exact text, sends the blob it read, and Cancel sends nothing', async () => {
  const calls = server([member('bosun', { published: true })], (c) => (c.method === 'GET' && c.path.endsWith('/published/bosun') ? json({ bytes: 'x', blob: 'blob1' }) : undefined));
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Unpublish' }));
  expect(await screen.findByText('Delete `crew/bosun.md` from `octocat/admiral-crew`? People who imported it keep their copy; they\'ll see it as removed upstream. It stays in the repo\'s history.')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(calls.filter((c) => c.method === 'DELETE')).toEqual([]);
  fireEvent.click(screen.getByRole('button', { name: 'Unpublish' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Confirm unpublish' }));
  await waitFor(() => expect(calls.filter((c) => c.method === 'DELETE').length).toBe(1));
  expect(calls.find((c) => c.method === 'DELETE')!.body).toEqual({ blob: 'blob1' });
});

test('an Unpublish that finds the file already gone says so, and one with a stale blob shows the 409 message', async () => {
  let status = 404;
  server([member('bosun', { published: true })], (c) => {
    if (c.method === 'GET' && c.path.endsWith('/published/bosun')) return json({ bytes: 'x', blob: 'blob1' });
    if (c.method === 'DELETE') return json({ error: status === 404 ? 'bosun is not published' : 'The published file changed since you opened this' }, status);
    return undefined;
  });
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Unpublish' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Confirm unpublish' }));
  expect((await screen.findByRole('alert')).textContent).toContain('bosun is not published');
  status = 409;
  fireEvent.click(screen.getByRole('button', { name: 'Confirm unpublish' }));
  await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('changed since you opened this'));
});
