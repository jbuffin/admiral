import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/preact';
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

const viewOf = (over: Record<string, unknown> = {}, repo = 'octocat/admiral-crew') => ({ crew_sharing: { enabled: true, share_repo: repo, sources: [] }, connected: true, login: 'octocat', login_error: '', published: [], published_error: '', ...over });

// A fake crew API: GET crew serves `members` and GET sharing serves `view`; every other request is recorded and answered by `reply`, or with {}.
function server(members: unknown[], reply: (c: Call) => Response | undefined = () => undefined, view: Record<string, unknown> = viewOf()) {
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
      if (method === 'GET' && /\/fleets\/zulu\/crew\/sharing(\?|$)/.test(path)) return new Response(JSON.stringify(view));
      if (method === 'GET' && /\/fleets\/zulu\/crew(\?|$)/.test(path)) return new Response(JSON.stringify({ harness: 'claude', members, unclaimed: [] }));
      return new Response('{}');
    }),
  );
  return calls;
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const page = (name: string, of: Fleet = on) => render(<CrewPage fleet={of} name={name} title phone={false} />);
const posts = (calls: Call[]) => calls.filter((c) => c.method !== 'GET').map((c) => `${c.method} ${c.path.replace('/api/v1/fleets/zulu/crew', '')}`);

const clean = { path: 'crew/bosun.md', bytes: '---\nname: bosun\n---\n\nDo it.\n', sha256: 'abc', repo: 'octocat/admiral-crew', private: false, replaces_blob: null, published_bytes: null, scan: { state: 'clean', findings: [] } };

test('Share shows for a fleet member of a sharing fleet, and not for a built-in, an imported member or a fleet with sharing off', async () => {
  server([member('bosun'), member('mate', { layer: 'built-in' }), member('imp', { shared: { owner: 'a', repo: 'b', commit: '0123456789abcdef0123456789abcdef01234567', tag: null, detached: false } })]);
  const { unmount } = page('bosun');
  expect(await screen.findByRole('button', { name: 'Share' })).toBeTruthy();
  unmount();
  page('mate');
  await screen.findByRole('button', { name: 'Customize for this fleet' });
  expect(screen.queryByRole('button', { name: 'Share' })).toBeNull();
});

test('an imported member and a fleet with sharing off show no Share button', async () => {
  server([member('imp', { shared: { owner: 'a', repo: 'b', commit: '0123456789abcdef0123456789abcdef01234567', tag: null, detached: false } }), member('bosun')]);
  const { unmount } = page('imp');
  await screen.findByRole('button', { name: 'Make mine' });
  expect(screen.queryByRole('button', { name: 'Share' })).toBeNull();
  unmount();
  page('bosun', off);
  await screen.findByRole('button', { name: 'Edit' });
  expect(screen.queryByRole('button', { name: 'Share' })).toBeNull();
});

const readmeOrRepo = (c: Call) => {
  if (!c.path.endsWith('/sharing/repo')) return undefined;
  return c.body?.preview ? json({ readme: `# ${c.body.owner}/${c.body.name}\n\nCrew members published from Admiral.\n` }) : json({ share_repo: 'octocat/admiral-crew' });
};

test('with no share repo yet Share opens the create-or-adopt dialog with the owner, the description and the README, and creating posts them', async () => {
  const calls = server([member('bosun')], readmeOrRepo, viewOf({}, ''));
  page('bosun', noRepo);
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  const name = (await screen.findByLabelText(/repo name/i)) as HTMLInputElement;
  expect(name.value).toBe('admiral-crew');
  expect(screen.getByText('octocat')).toBeTruthy();
  expect(await screen.findByRole('heading', { name: 'octocat/admiral-crew' })).toBeTruthy();
  fireEvent.input(screen.getByLabelText(/description/i), { target: { value: 'My crew' } });
  fireEvent.click(screen.getByRole('button', { name: 'Create public repo' }));
  await waitFor(() => expect(posts(calls)).toContain('POST /sharing/repo'));
  expect(calls.find((c) => c.path.endsWith('/sharing/repo') && !c.body?.preview)!.body).toEqual({ name: 'admiral-crew', description: 'My crew', private: false });
});

test('Use an existing repo posts that repo', async () => {
  const calls = server([member('bosun')], readmeOrRepo, viewOf({}, ''));
  page('bosun', noRepo);
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  fireEvent.input(await screen.findByLabelText(/use an existing repo/i), { target: { value: 'me/shared-crew' } });
  fireEvent.click(screen.getByRole('button', { name: 'Use this repo' }));
  await waitFor(() => expect(calls.some((c) => c.body?.repo === 'me/shared-crew')).toBe(true));
});

test('Share is hidden while GitHub is not connected, with a link to connect it', async () => {
  server([member('bosun')], undefined, viewOf({ connected: false }));
  page('bosun');
  const link = await screen.findByRole('link', { name: 'Connect GitHub' });
  expect(link.getAttribute('href')).toBe('/app/preferences/zulu/github');
  expect(screen.queryByRole('button', { name: 'Share' })).toBeNull();
});

test('a failed published listing shows its error on the page', async () => {
  server([member('bosun')], undefined, viewOf({ published_error: 'GitHub rate limit' }));
  page('bosun');
  expect((await screen.findByText(/GitHub rate limit/)).textContent).toContain('Published marks are missing');
});

test('a taken repo name offers to use the existing repo, and using it posts that repo', async () => {
  let attempts = 0;
  const calls = server(
    [member('bosun')],
    (c) => {
      if (!c.path.endsWith('/sharing/repo') || c.body?.preview) return c.body?.preview ? json({ readme: '# r\n' }) : undefined;
      attempts++;
      return attempts === 1 ? json({ error: 'octocat/admiral-crew already exists', use_existing: 'octocat/admiral-crew' }, 409) : json({ share_repo: 'octocat/admiral-crew' });
    },
    viewOf({}, ''),
  );
  page('bosun', noRepo);
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Create public repo' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Use octocat/admiral-crew' }));
  await waitFor(() => expect(calls.some((c) => c.body?.repo === 'octocat/admiral-crew')).toBe(true));
  expect(calls.filter((c) => c.path.endsWith('/sharing/repo') && !c.body?.preview)).toHaveLength(2);
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
  expect(screen.getByText('crew/bosun.md in octocat/admiral-crew')).toBeTruthy();
  expect(screen.getByText(/Do it\./)).toBeTruthy();
  expect(screen.getByText('Secret scan: nothing found.')).toBeTruthy();
  expect(posts(calls)).toEqual(['POST /members/bosun/share/preview']);
  fireEvent.click(screen.getByRole('button', { name: 'Publish' }));
  await waitFor(() => expect(posts(calls)).toEqual(['POST /members/bosun/share/preview', 'POST /members/bosun/share']));
  expect(calls[calls.length - 1]!.body).toMatchObject({ sha256: 'abc', repo: 'octocat/admiral-crew', private: false, replaces_blob: null });
  expect(await screen.findByRole('link', { name: /commit/i })).toBeTruthy();
});

test('a published member shows a Published badge and an Unpublish button, and Share shows what changes since the published file', async () => {
  server([member('bosun', { published: true })], (c) => (c.path.endsWith('/share/preview') ? json({ ...clean, replaces_blob: 'blob1', published_bytes: '---\nname: bosun\n---\n\nOld.\n' }) : undefined));
  page('bosun');
  const pill = await screen.findByRole('link', { name: 'Published' });
  expect(pill.getAttribute('href')).toBe('https://github.com/octocat/admiral-crew/blob/HEAD/crew/bosun.md');
  expect(screen.getByRole('button', { name: 'Unpublish' })).toBeTruthy();
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  await screen.findByText(/crew\/bosun\.md/);
  expect(screen.getByText('Replaces the published version')).toBeTruthy();
  expect(screen.getByLabelText('File to publish').textContent).toContain('Do it.');
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

test.each([
  ['missing', 'brew install gitleaks'],
  ['too-old', 'brew upgrade gitleaks'],
])('with gitleaks %s Publish needs the opt-out box, which carries the warning, and sends skip_secret_scan', async (state, install) => {
  const calls = server([member('bosun')], (c) => {
    if (c.path.endsWith('/share/preview')) return json({ ...clean, scan: { state, findings: [], install } });
    if (c.path.endsWith('/members/bosun/share')) return json({ commit_url: 'https://github.com/octocat/admiral-crew/commit/abc123' });
    return undefined;
  });
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  expect(await screen.findByText(new RegExp(install))).toBeTruthy();
  expect(screen.getByRole('link', { name: /gitleaks install guide/ })).toBeTruthy();
  expect(screen.getByText('This file goes to octocat/admiral-crew without a secret scan. Anyone who can read that repo can read it. Read it for keys, tokens and passwords before you publish.')).toBeTruthy();
  const publish = screen.getByRole('button', { name: 'Publish' }) as HTMLButtonElement;
  expect(publish.disabled).toBe(true);
  fireEvent.click(screen.getByRole('checkbox'));
  expect(publish.disabled).toBe(false);
  fireEvent.click(publish);
  await waitFor(() => expect(posts(calls)).toContain('POST /members/bosun/share'));
  expect(calls[calls.length - 1]!.body).toMatchObject({ skip_secret_scan: true });
});

test('a clean scan shows no opt-out box and sends no skip_secret_scan', async () => {
  const calls = server([member('bosun')], (c) => (c.path.endsWith('/share/preview') ? json(clean) : c.path.endsWith('/members/bosun/share') ? json({ commit_url: '' }) : undefined));
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  await screen.findByText(/crew\/bosun\.md/);
  expect(screen.queryByRole('checkbox')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Publish' }));
  await waitFor(() => expect(posts(calls)).toContain('POST /members/bosun/share'));
  expect(calls[calls.length - 1]!.body).not.toHaveProperty('skip_secret_scan');
});

test('a Share refused as changed since the preview shows the message and loads the preview again', async () => {
  const calls = server([member('bosun')], (c) => {
    if (c.path.endsWith('/share/preview')) return json(clean);
    if (c.path.endsWith('/members/bosun/share')) return json({ error: 'The published file changed since the preview' }, 409);
    return undefined;
  });
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Publish' }));
  await waitFor(() => expect(calls.filter((c) => c.path.endsWith('/share/preview'))).toHaveLength(2));
  expect((await screen.findByRole('alert')).textContent).toContain('The published file changed since the preview');
  expect(await screen.findByRole('button', { name: 'Publish' })).toBeTruthy();
});

test('a share repo that cannot be written offers Choose another repo, which opens the repo step', async () => {
  server([member('bosun')], (c) => {
    if (c.path.endsWith('/share/preview')) return json({ error: 'The share repo octocat/admiral-crew is archived. Choose another repo.' }, 409);
    return readmeOrRepo(c);
  });
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  expect((await screen.findByRole('alert')).textContent).toContain('archived');
  fireEvent.click(screen.getByRole('button', { name: 'Choose another repo' }));
  expect(await screen.findByRole('button', { name: 'Create public repo' })).toBeTruthy();
});

test('a refused preview shows the server message', async () => {
  server([member('bosun')], (c) => (c.path.endsWith('/share/preview') ? json({ error: 'The member has a private path' }, 422) : undefined));
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  expect((await screen.findByRole('alert')).textContent).toContain('The member has a private path');
  expect(screen.queryByRole('button', { name: 'Publish' })).toBeNull();
});

const got = { bytes: '---\nname: bosun\n---\n\nOld.\n', blob: 'blob1', repo: 'octocat/admiral-crew' };

test('Unpublish asks with the exact text and the published bytes, sends the blob and repo it read, and Cancel sends nothing', async () => {
  const calls = server([member('bosun', { published: true })], (c) => (c.method === 'GET' && c.path.endsWith('/published/bosun') ? json(got) : undefined));
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Unpublish' }));
  expect(await screen.findByText("Delete crew/bosun.md from octocat/admiral-crew? People who imported it keep their copy; they'll see it as removed upstream. It stays in the repo's history.")).toBeTruthy();
  expect(screen.getByLabelText('Published file').textContent).toBe(got.bytes);
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(calls.filter((c) => c.method === 'DELETE')).toEqual([]);
  fireEvent.click(screen.getByRole('button', { name: 'Unpublish' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Confirm unpublish' }));
  await waitFor(() => expect(calls.filter((c) => c.method === 'DELETE').length).toBe(1));
  expect(calls.find((c) => c.method === 'DELETE')!.body).toEqual({ blob: 'blob1', repo: 'octocat/admiral-crew' });
});

test('an Unpublish with a stale blob shows the 409 message and reads the file again', async () => {
  const calls = server([member('bosun', { published: true })], (c) => {
    if (c.method === 'GET' && c.path.endsWith('/published/bosun')) return json(got);
    if (c.method === 'DELETE') return json({ error: 'The published file changed since you opened this' }, 409);
    return undefined;
  });
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Unpublish' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Confirm unpublish' }));
  await waitFor(() => expect(calls.filter((c) => c.method === 'GET' && c.path.endsWith('/published/bosun'))).toHaveLength(2));
  expect((await screen.findByRole('alert')).textContent).toContain('changed since you opened this');
});

test('an Unpublish that finds the file already gone reloads the crew view and drops the Published pill', async () => {
  let gone = false;
  const calls = server([member('bosun', { published: true })], (c) => {
    if (c.method === 'GET' && c.path.endsWith('/published/bosun')) return json(got);
    if (c.method === 'DELETE') {
      gone = true;
      return json({ error: 'bosun is not published' }, 404);
    }
    if (gone && c.method === 'GET' && /\/fleets\/zulu\/crew(\?|$)/.test(c.path)) return json({ harness: 'claude', members: [member('bosun')], unclaimed: [] });
    return undefined;
  });
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Unpublish' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Confirm unpublish' }));
  await waitFor(() => expect(screen.queryByRole('link', { name: 'Published' })).toBeNull());
  expect(calls.filter((c) => c.method === 'GET' && /\/fleets\/zulu\/crew(\?|$)/.test(c.path)).length).toBeGreaterThan(1);
});

const NO_ACCESS = 'You need access to octocat/admiral-crew. Ask its owner to add you.';
const noAccess = (text = NO_ACCESS) => json({ error: text, code: 'no_access' }, 403);
const createBody = (calls: Call[]) => calls.find((c) => c.path.endsWith('/sharing/repo') && !c.body?.preview && !c.body?.repo)?.body;

test('the repo step asks for Public or Private under the name, Public preselected, each with its sentence', async () => {
  server([member('bosun')], readmeOrRepo, viewOf({}, ''));
  page('bosun', noRepo);
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  await screen.findByLabelText(/repo name/i);
  const pub = screen.getByRole('radio', { name: /^Public/ }) as HTMLInputElement;
  const priv = screen.getByRole('radio', { name: /^Private/ }) as HTMLInputElement;
  expect(pub.checked).toBe(true);
  expect(priv.checked).toBe(false);
  const follows = (a: Element, b: Element) => !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
  const nameInput = screen.getByLabelText(/repo name/i);
  const descriptionInput = screen.getByLabelText(/description/i);
  expect(follows(nameInput, pub) && follows(pub, priv) && follows(priv, descriptionInput)).toBe(true);
  expect(screen.getByRole('radiogroup', { name: 'Visibility' })).toBeTruthy();
  expect(pub.getAttribute('aria-describedby')).toBeTruthy();
  expect(document.getElementById(pub.getAttribute('aria-describedby')!)?.textContent).toMatch(/Anyone can read/);
  expect(screen.getByText(/Anyone can read and import your crew members\./)).toBeTruthy();
  expect(screen.getByText(/Only you and people you add to the repo can read and import them\./)).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Create public repo' })).toBeTruthy();
});

test('the repo step note says a GitHub repo you own, not a public one', async () => {
  server([member('bosun')], readmeOrRepo, viewOf({}, ''));
  page('bosun', noRepo);
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  expect(await screen.findByText('Crew members are published to a GitHub repo you own. Admiral creates it under your account, or you can use one you have.')).toBeTruthy();
});

test('choosing Private renames the button and creates a private repo', async () => {
  const calls = server([member('bosun')], readmeOrRepo, viewOf({}, ''));
  page('bosun', noRepo);
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  await screen.findByLabelText(/repo name/i);
  fireEvent.click(screen.getByRole('radio', { name: /^Private/ }));
  expect(screen.queryByRole('button', { name: 'Create public repo' })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Create private repo' }));
  await waitFor(() => expect(createBody(calls)).toBeTruthy());
  expect(createBody(calls)).toMatchObject({ name: 'admiral-crew', private: true });
});

test('Use this repo and the Use offer send no private, and the README preview is unchanged', async () => {
  let attempts = 0;
  const calls = server(
    [member('bosun')],
    (c) => {
      if (!c.path.endsWith('/sharing/repo')) return undefined;
      if (c.body?.preview) return json({ readme: '# r\n' });
      attempts++;
      return c.body?.repo ? json({ share_repo: 'octocat/admiral-crew' }) : json({ error: 'octocat/admiral-crew already exists', use_existing: 'octocat/admiral-crew' }, 409);
    },
    viewOf({}, ''),
  );
  page('bosun', noRepo);
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  await screen.findByLabelText(/repo name/i);
  fireEvent.click(screen.getByRole('radio', { name: /^Private/ }));
  fireEvent.click(screen.getByRole('button', { name: 'Create private repo' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Use octocat/admiral-crew' }));
  await waitFor(() => expect(attempts).toBe(2));
  expect(calls.filter((c) => c.path.endsWith('/sharing/repo') && c.body?.repo).map((c) => c.body)).toEqual([{ repo: 'octocat/admiral-crew' }]);
  cleanup();

  const second = server([member('bosun')], (c) => (c.path.endsWith('/sharing/repo') ? (c.body?.preview ? json({ readme: '# r\n' }) : json({ share_repo: 'me/shared-crew' })) : undefined), viewOf({}, ''));
  page('bosun', noRepo);
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  fireEvent.input(await screen.findByLabelText(/use an existing repo/i), { target: { value: 'me/shared-crew' } });
  fireEvent.click(screen.getByRole('button', { name: 'Use this repo' }));
  await waitFor(() => expect(second.some((c) => c.body?.repo)).toBe(true));
  expect(second.filter((c) => c.path.endsWith('/sharing/repo') && c.body?.repo).map((c) => c.body)).toEqual([{ repo: 'me/shared-crew' }]);
  const previews = calls.filter((c) => c.body?.preview);
  expect(previews.length).toBeGreaterThan(0);
  for (const c of previews) expect(c.body).toEqual({ preview: true, owner: 'octocat', name: 'admiral-crew' });
});

test('the preview says private or public from its own answer, not from the setup view', async () => {
  for (const [previewPrivate, setupPrivate, shown, hidden] of [
    [true, false, 'octocat/admiral-crew is private. Anyone with access to it can read this file.', /is public/],
    [false, true, 'octocat/admiral-crew is public. Anyone can read this file.', /is private/],
  ] as const) {
    server([member('bosun')], (c) => (c.path.endsWith('/share/preview') ? json({ ...clean, private: previewPrivate }) : undefined), viewOf({ repo: { exists: true, private: setupPrivate, archived: false, pushable: true, error: '' } }));
    const { unmount } = page('bosun');
    fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
    expect(await screen.findByText(shown)).toBeTruthy();
    expect(screen.queryByText(hidden)).toBeNull();
    unmount();
  }
});

test('the preview names the share repo above the exact file, with the aria label File to publish', async () => {
  server([member('bosun')], (c) => (c.path.endsWith('/share/preview') ? json(clean) : undefined));
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  expect(await screen.findByText('The file that goes to octocat/admiral-crew, exactly:')).toBeTruthy();
  expect(screen.getByLabelText('File to publish').textContent).toContain('Do it.');
  expect(screen.queryByText(/goes public/)).toBeNull();
});

test('Publish sends the private the preview answered', async () => {
  const calls = server([member('bosun')], (c) => (c.path.endsWith('/share/preview') ? json({ ...clean, private: true }) : c.path.endsWith('/members/bosun/share') ? json({ commit_url: '' }) : undefined));
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Publish' }));
  await waitFor(() => expect(posts(calls)).toContain('POST /members/bosun/share'));
  expect(calls[calls.length - 1]!.body).toMatchObject({ private: true });
});

test('no access on the share preview shows its text with Choose another repo, which opens the repo step', async () => {
  server([member('bosun')], (c) => (c.path.endsWith('/share/preview') ? noAccess() : readmeOrRepo(c)));
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  expect((await screen.findByRole('alert')).textContent).toBe(NO_ACCESS);
  expect(screen.queryByRole('button', { name: 'Publish' })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Choose another repo' }));
  expect(await screen.findByRole('button', { name: 'Create public repo' })).toBeTruthy();
});

test('a 403 with no code (sharing off) offers no Choose another repo', async () => {
  server([member('bosun')], (c) => (c.path.endsWith('/share/preview') ? json({ error: 'Crew sharing is off for this fleet. Turn it on in Preferences.' }, 403) : undefined));
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  expect((await screen.findByRole('alert')).textContent).toContain('Crew sharing is off');
  expect(screen.queryByRole('button', { name: 'Choose another repo' })).toBeNull();
});

test('no access on Publish shows its text with Choose another repo and does not read the preview again', async () => {
  const calls = server([member('bosun')], (c) => {
    if (c.path.endsWith('/share/preview')) return json(clean);
    if (c.path.endsWith('/members/bosun/share')) return noAccess();
    return readmeOrRepo(c);
  });
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Publish' }));
  expect((await screen.findByRole('alert')).textContent).toBe(NO_ACCESS);
  fireEvent.click(await screen.findByRole('button', { name: 'Choose another repo' }));
  expect(await screen.findByRole('button', { name: 'Create public repo' })).toBeTruthy();
  expect(calls.filter((c) => c.path.endsWith('/share/preview'))).toHaveLength(1);
});

test('a Share refused because the visibility changed shows the message, reads the preview again and offers no Choose another repo (guard)', async () => {
  const calls = server([member('bosun')], (c) => {
    if (c.path.endsWith('/share/preview')) return json(clean);
    if (c.path.endsWith('/members/bosun/share')) return json({ error: "The share repo's visibility changed since the preview. Preview it again." }, 409);
    return undefined;
  });
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Publish' }));
  await waitFor(() => expect(calls.filter((c) => c.path.endsWith('/share/preview'))).toHaveLength(2));
  expect((await screen.findByRole('alert')).textContent).toContain('Preview it again.');
  expect(screen.queryByRole('button', { name: 'Choose another repo' })).toBeNull();
});

test('a saved share repo whose setup view says it does not exist shows the share preview, not the repo step (guard)', async () => {
  server([member('bosun')], (c) => (c.path.endsWith('/share/preview') ? noAccess() : undefined), viewOf({ repo: { exists: false } }));
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  expect((await screen.findByRole('alert')).textContent).toBe(NO_ACCESS);
  expect(screen.queryByLabelText(/repo name/i)).toBeNull();
});

test('adopting a repo the engineer cannot read shows the no access text (guard)', async () => {
  server([member('bosun')], (c) => (c.path.endsWith('/sharing/repo') ? (c.body?.preview ? json({ readme: '# r\n' }) : noAccess('You need access to me/shared-crew. Ask its owner to add you.')) : undefined), viewOf({}, ''));
  page('bosun', noRepo);
  fireEvent.click(await screen.findByRole('button', { name: 'Share' }));
  fireEvent.input(await screen.findByLabelText(/use an existing repo/i), { target: { value: 'me/shared-crew' } });
  fireEvent.click(screen.getByRole('button', { name: 'Use this repo' }));
  expect((await screen.findByRole('alert')).textContent).toBe('You need access to me/shared-crew. Ask its owner to add you.');
});

test('no access on the published listing shows its text where the Published marks are missing (guard)', async () => {
  server([member('bosun')], undefined, viewOf({ published_error: NO_ACCESS }));
  page('bosun');
  expect((await screen.findByText(/Published marks are missing/)).textContent).toContain(NO_ACCESS);
});

test('Unpublish with no access shows the text when reading the file and when deleting it (guard)', async () => {
  server([member('bosun', { published: true })], (c) => (c.method === 'GET' && c.path.endsWith('/published/bosun') ? noAccess() : undefined));
  const first = page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Unpublish' }));
  expect((await screen.findByRole('alert')).textContent).toBe(NO_ACCESS);
  first.unmount();

  server([member('bosun', { published: true })], (c) => {
    if (c.method === 'GET' && c.path.endsWith('/published/bosun')) return json(got);
    if (c.method === 'DELETE') return noAccess();
    return undefined;
  });
  page('bosun');
  fireEvent.click(await screen.findByRole('button', { name: 'Unpublish' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Confirm unpublish' }));
  expect((await screen.findByRole('alert')).textContent).toBe(NO_ACCESS);
});
