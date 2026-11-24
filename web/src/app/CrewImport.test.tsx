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

const sharing = (sources: string[]) => ({ enabled: true, share_repo: 'octocat/admiral-crew', sources });
const fleetOf = (sources: string[] = []) => ({ key: 'zulu', name: 'Zulu', docks: [{ harness: 'claude', models: ['sonnet'], models_source: 'listed' }], crew_sharing: sharing(sources) }) as unknown as Fleet;
const view = (sources: string[] = [], over: Record<string, unknown> = {}) => ({ crew_sharing: sharing(sources), connected: true, login: 'octocat', login_error: '', published: [], published_error: '', ...over });
const member = (name: string, over: Record<string, unknown> = {}) => ({ name, role: 'scribe', 'based-on': null, skills: [], model: null, harness: null, 'pick-me-when': 'Pick ' + name, instructions: 'Do it.', layer: 'fleet', customized: false, notes: { fleet: [], repos: {} }, shared: null, published: false, ...over });

interface Call {
  method: string;
  path: string;
  body?: Record<string, unknown>;
}

// A fake crew API: GET crew and GET sharing are served; every other request is recorded and answered by `reply`, or with {}.
function server(members: unknown[], reply: (c: Call) => Response | undefined, v: Record<string, unknown> = view()) {
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
      if (method === 'GET' && /\/fleets\/zulu\/crew\/sharing(\?|$)/.test(path)) return new Response(JSON.stringify(v));
      if (method === 'GET' && /\/fleets\/zulu\/crew(\?|$)/.test(path)) return new Response(JSON.stringify({ harness: 'claude', members, unclaimed: [] }));
      return new Response('{}');
    }),
  );
  return calls;
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const page = (name: string | null = null, of: Fleet = fleetOf()) => render(<CrewPage fleet={of} name={name} title phone={false} />);
const to = (c: Call, suffix: string) => c.path.replace(/\?.*$/, '').endsWith(suffix);
const named = (calls: Call[], method: string, suffix: string) => calls.filter((c) => c.method === method && to(c, suffix));

const commit = '0123456789abcdef0123456789abcdef01234567';
const raw = '---\nname: scout\nrole: triager\npick-me-when: Pick **scout** now.\nskills:\n  - nope\n---\n\nWork as <b>scout</b>.\n';
const pv = { bytes: raw, card: { name: 'scout', role: 'triager', 'based-on': null, skills: ['nope'], model: null, harness: null, 'pick-me-when': 'Pick **scout** now.' }, owner: 'hubot', repo: 'crew', path: 'crew/scout.md', commit, tag: 'v1', blob: 'b'.repeat(40), sha256: 'abc', clash: null, missing_skills: ['nope'], scan: { state: 'clean', findings: [] } };

async function openImport() {
  fireEvent.click(await screen.findByRole('button', { name: 'Import' }));
  return (await screen.findByLabelText(/ref or url/i)) as HTMLInputElement;
}

async function previewOf(ref = 'hubot/crew/scout') {
  const box = await openImport();
  fireEvent.input(box, { target: { value: ref } });
  fireEvent.click(screen.getByRole('button', { name: 'Preview' }));
}

test('with GitHub not connected the Import panel says to connect it and shows no box', async () => {
  server([member('bosun')], () => undefined, view([], { connected: false }));
  page();
  fireEvent.click(await screen.findByRole('button', { name: 'Import' }));
  expect(await screen.findByText(/Connect GitHub \(github.com\) for this fleet first/)).toBeTruthy();
  const link = within(screen.getByRole('region', { name: 'Import' })).getByRole('link', { name: 'Connect GitHub' });
  expect(link.getAttribute('href')).toBe('/app/preferences/zulu/github');
  fireEvent.click(link);
  expect(loc.value.path).toBe('/app/preferences/zulu/github');
  expect(screen.queryByLabelText(/ref or url/i)).toBeNull();
});

test('Preview posts the ref and shows the card through the markdown renderer, who shared it, the short commit, the tag, and the raw file as text', async () => {
  const calls = server([member('bosun')], (c) => (to(c, '/imports/preview') ? json(pv) : undefined));
  page();
  await previewOf();
  const panel = await screen.findByRole('region', { name: 'Import' });
  const shared = await within(panel).findByText(/shared by/i);
  expect(named(calls, 'POST', '/imports/preview')[0]!.body).toEqual({ ref: 'hubot/crew/scout' });
  expect(shared.textContent).toContain('hubot');
  expect(panel.textContent).toContain('0123456');
  expect(panel.textContent).not.toContain(commit);
  expect(panel.textContent).toContain('v1');
  const card = within(panel).getByLabelText('Member card');
  expect(card.querySelector('strong')!.textContent).toBe('scout');
  const file = within(panel).getByLabelText('File to import');
  expect(file.textContent).toBe(raw);
  expect(file.querySelector('b')).toBeNull();
  expect(within(panel).getByText('Not installed: nope')).toBeTruthy();
  expect(within(panel).getByRole('button', { name: 'Import' })).toBeTruthy();
});

test('a hit in the file shows a warning with the line and rule, never the secret, and Import stays', async () => {
  server([member('bosun')], (c) => (to(c, '/imports/preview') ? json({ ...pv, scan: { state: 'hit', findings: [{ line: 8, rule: 'decoy-secret' }] } }) : undefined));
  page();
  await previewOf();
  const panel = await screen.findByRole('region', { name: 'Import' });
  await within(panel).findByText(/decoy-secret/);
  expect(within(panel).getByText(/line 8/)).toBeTruthy();
  expect(within(panel).getByRole('button', { name: 'Import' })).toBeTruthy();
});

test('a refused file shows the refusal in place of Import', async () => {
  server([member('bosun')], (c) => (to(c, '/imports/preview') ? json({ error: 'U+202E on line 8: a published file can\'t hold a character a reader can\'t see' }, 422) : undefined));
  page();
  await previewOf();
  expect((await screen.findByRole('alert')).textContent).toContain('U+202E on line 8');
  expect(within(screen.getByRole('region', { name: 'Import' })).queryByRole('button', { name: 'Import' })).toBeNull();
});

test('Import sends the preview back and the new member shows afterwards', async () => {
  const members = [member('bosun')];
  const calls = server(members, (c) => {
    if (to(c, '/imports/preview')) return json(pv);
    if (to(c, '/imports') && c.method === 'POST') {
      members.push(member('scout'));
      return json({ name: 'scout', missing_skills: ['nope'] });
    }
    return undefined;
  });
  page();
  await previewOf();
  await within(await screen.findByRole('region', { name: 'Import' })).findByText(/shared by/i);
  const crewReads = calls.filter((c) => c.method === 'GET' && /\/crew(\?|$)/.test(c.path)).length;
  fireEvent.click(within(screen.getByRole('region', { name: 'Import' })).getByRole('button', { name: 'Import' }));
  await waitFor(() => expect(named(calls, 'POST', '/imports')).toHaveLength(1));
  expect(named(calls, 'POST', '/imports')[0]!.body).toEqual({ repo: 'hubot/crew', path: 'crew/scout.md', commit, sha256: 'abc' });
  await waitFor(() => expect(calls.filter((c) => c.method === 'GET' && /\/crew(\?|$)/.test(c.path)).length).toBeGreaterThan(crewReads));
  expect(await screen.findByText('Imported as scout. Not installed here: nope.')).toBeTruthy();
  expect(await screen.findByRole('link', { name: /scout/ })).toBeTruthy();
});

test('a name that is taken says so, offers the suggested name in an editable box, and Import sends the name typed', async () => {
  const calls = server([member('scout')], (c) => (to(c, '/imports/preview') ? json({ ...pv, clash: { name: 'scout', suggested: 'scout-hubot' } }) : to(c, '/imports') && c.method === 'POST' ? json({ name: 'scout-hubot', missing_skills: [] }) : undefined));
  page();
  await previewOf();
  const name = (await screen.findByLabelText(/name in this fleet/i)) as HTMLInputElement;
  expect(screen.getByText(/already (taken|in use)|is taken/i)).toBeTruthy();
  expect(name.value).toBe('scout-hubot');
  fireEvent.input(name, { target: { value: 'scout-two' } });
  fireEvent.click(within(screen.getByRole('region', { name: 'Import' })).getByRole('button', { name: 'Import' }));
  await waitFor(() => expect(named(calls, 'POST', '/imports')).toHaveLength(1));
  expect(named(calls, 'POST', '/imports')[0]!.body).toMatchObject({ name: 'scout-two' });
});

test('a clash with no suggestion leaves the name box empty for the engineer to type', async () => {
  server([member('scout')], (c) => (to(c, '/imports/preview') ? json({ ...pv, clash: { name: 'scout', suggested: null } }) : undefined));
  page();
  await previewOf();
  expect(((await screen.findByLabelText(/name in this fleet/i)) as HTMLInputElement).value).toBe('');
});

test('a name taken since the preview puts the suggested name in the box and keeps the preview without reading it again', async () => {
  const calls = server([member('bosun')], (c) => (to(c, '/imports/preview') ? json(pv) : to(c, '/imports') && c.method === 'POST' ? json({ error: 'A crew member named scout is already in this fleet. Choose another name.', suggested: 'scout-hubot' }, 409) : undefined));
  page();
  await previewOf();
  fireEvent.click(await within(await screen.findByRole('region', { name: 'Import' })).findByRole('button', { name: 'Import' }));
  expect((await screen.findByRole('alert')).textContent).toContain('already in this fleet');
  expect(named(calls, 'POST', '/imports/preview')).toHaveLength(1);
  const panel = screen.getByRole('region', { name: 'Import' });
  expect(within(panel).getByLabelText('Member card')).toBeTruthy();
  expect((within(panel).getByLabelText(/name in this fleet/i) as HTMLInputElement).value).toBe('scout-hubot');
});

test('the card shows the name, role, based-on, skills, model, harness and pick-me-when', async () => {
  const card = { name: 'scout', role: 'triager', 'based-on': 'reviewer', skills: ['tdd', 'nope'], model: 'sonnet', harness: 'codex', 'pick-me-when': 'Pick **scout** now.' };
  server([member('bosun')], (c) => (to(c, '/imports/preview') ? json({ ...pv, card }) : undefined));
  page();
  await previewOf();
  const shown = within(await screen.findByLabelText('Member card'));
  for (const text of ['scout', 'triager', 'reviewer', 'tdd, nope', 'sonnet', 'codex', 'now.']) expect(shown.getAllByText(new RegExp(text)).length).toBeGreaterThan(0);
});

test('a scan that did not run because gitleaks is missing or too old shows nothing on import', async () => {
  for (const state of ['missing', 'too-old']) {
    server([member('bosun')], (c) => (to(c, '/imports/preview') ? json({ ...pv, scan: { state, findings: [] } }) : undefined));
    const { unmount } = page();
    await previewOf();
    const panel = await screen.findByRole('region', { name: 'Import' });
    await within(panel).findByText(/shared by/i);
    expect(panel.textContent).not.toMatch(/secret scan/i);
    unmount();
  }
});

test('adding a repo as a crew source opens its browse list', async () => {
  server(
    [member('bosun')],
    (c) => {
      if (to(c, '/imports/preview')) return json({ source: 'hubot/crew' });
      if (c.method === 'PUT' && to(c, '/sources')) return json({ sources: ['hubot/crew'] });
      if (c.method === 'GET' && to(c, '/sources/hubot/crew')) return json({ members: [{ name: 'mate', path: 'crew/mate.md', blob: 'b2', imported: null, published: false }] });
      return undefined;
    },
    view([]),
  );
  page();
  await previewOf('https://github.com/hubot/crew');
  fireEvent.click(await screen.findByRole('button', { name: 'Add as a crew source' }));
  const list = await screen.findByRole('list', { name: 'hubot/crew' });
  expect(within(list).getByText('mate')).toBeTruthy();
});

test('a browse row shows the role once its file has been read', async () => {
  server(
    [member('bosun')],
    (c) => {
      if (c.method === 'GET' && to(c, '/sources/hubot/crew')) return json({ members: [{ name: 'scout', path: 'crew/scout.md', blob: 'b1', imported: null, published: false }, { name: 'mate', path: 'crew/mate.md', blob: 'b2', imported: null, published: false }] });
      if (to(c, '/imports/preview')) return json(pv);
      return undefined;
    },
    view(['hubot/crew']),
  );
  page(null, fleetOf(['hubot/crew']));
  await openImport();
  fireEvent.click(screen.getAllByRole('button', { name: 'Browse' })[1]!);
  const list = await screen.findByRole('list', { name: 'hubot/crew' });
  const [scoutRow, mateRow] = within(list).getAllByRole('listitem');
  expect(within(scoutRow!).queryByText('triager')).toBeNull();
  fireEvent.click(within(scoutRow!).getByRole('button', { name: 'Import' }));
  await within(scoutRow!).findByText('triager');
  expect(within(mateRow!).queryByText('triager')).toBeNull();
});

test('an Import refused as changed since the preview shows the message and loads the preview again', async () => {
  const calls = server([member('bosun')], (c) => (to(c, '/imports/preview') ? json(pv) : to(c, '/imports') && c.method === 'POST' ? json({ error: 'The file changed since the preview' }, 409) : undefined));
  page();
  await previewOf();
  fireEvent.click(await within(await screen.findByRole('region', { name: 'Import' })).findByRole('button', { name: 'Import' }));
  await waitFor(() => expect(named(calls, 'POST', '/imports/preview')).toHaveLength(2));
  expect((await screen.findByRole('alert')).textContent).toContain('The file changed since the preview');
});

test('an Import from a Browse row refused as changed loads that row\'s preview again', async () => {
  const calls = server(
    [member('bosun')],
    (c) => {
      if (c.method === 'GET' && to(c, '/sources/hubot/crew')) return json({ members: [{ name: 'mate', path: 'crew/mate.md', blob: 'b2', imported: null, published: false }] });
      if (to(c, '/imports/preview')) return json({ ...pv, path: 'crew/mate.md' });
      if (to(c, '/imports') && c.method === 'POST') return json({ error: 'The file changed since the preview' }, 409);
      return undefined;
    },
    view(['hubot/crew']),
  );
  page(null, fleetOf(['hubot/crew']));
  await openImport();
  fireEvent.click(screen.getAllByRole('button', { name: 'Browse' })[1]!);
  const list = await screen.findByRole('list', { name: 'hubot/crew' });
  fireEvent.click(within(list).getByRole('button', { name: 'Import' }));
  const panel = screen.getByRole('region', { name: 'Import' });
  await within(panel).findByLabelText('Member card');
  // The preview sits above the browse list, so its Import is the first.
  fireEvent.click(within(panel).getAllByRole('button', { name: 'Import' })[0]!);
  await waitFor(() => expect(named(calls, 'POST', '/imports/preview')).toHaveLength(2));
  expect(named(calls, 'POST', '/imports/preview')[1]!.body).toEqual({ ref: 'hubot/crew/mate' });
});

test('a repo URL offers Add as a crew source, which puts the list with the repo added', async () => {
  const calls = server([member('bosun')], (c) => (to(c, '/imports/preview') ? json({ source: 'hubot/crew' }) : c.method === 'PUT' && to(c, '/sources') ? json({ sources: ['a/b', 'hubot/crew'] }) : c.method === 'GET' && to(c, '/sources/hubot/crew') ? json({ members: [] }) : undefined), view(['a/b']));
  page(null, fleetOf(['a/b']));
  await previewOf('https://github.com/hubot/crew');
  fireEvent.click(await screen.findByRole('button', { name: 'Add as a crew source' }));
  await waitFor(() => expect(named(calls, 'PUT', '/sources')).toHaveLength(1));
  expect(named(calls, 'PUT', '/sources')[0]!.body).toEqual({ sources: ['a/b', 'hubot/crew'] });
});

test('the panel lists the share repo first and each source with Browse and Remove; Remove puts the list without it', async () => {
  const calls = server([member('bosun')], (c) => (c.method === 'PUT' && to(c, '/sources') ? json({ sources: ['c/d'] }) : undefined), view(['a/b', 'c/d']));
  page(null, fleetOf(['a/b', 'c/d']));
  await openImport();
  const panel = screen.getByRole('region', { name: 'Import' });
  expect(within(panel).getByText('Your share repo')).toBeTruthy();
  expect(within(panel).getAllByText('a/b').length).toBeGreaterThan(0);
  expect(within(panel).getAllByRole('button', { name: 'Browse' })).toHaveLength(3);
  expect(within(panel).getAllByRole('button', { name: 'Remove' })).toHaveLength(2);
  fireEvent.click(within(panel).getAllByRole('button', { name: 'Remove' })[0]!);
  await waitFor(() => expect(named(calls, 'PUT', '/sources')).toHaveLength(1));
  expect(named(calls, 'PUT', '/sources')[0]!.body).toEqual({ sources: ['c/d'] });
});

test('Add source takes a repo and puts the list with it appended', async () => {
  const calls = server([member('bosun')], (c) => (c.method === 'PUT' && to(c, '/sources') ? json({ sources: ['a/b', 'hubot/crew'] }) : undefined), view(['a/b']));
  page(null, fleetOf(['a/b']));
  await openImport();
  fireEvent.input(screen.getByLabelText(/source repo/i), { target: { value: 'hubot/crew' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add source' }));
  await waitFor(() => expect(named(calls, 'PUT', '/sources')).toHaveLength(1));
  expect(named(calls, 'PUT', '/sources')[0]!.body).toEqual({ sources: ['a/b', 'hubot/crew'] });
});

test('a source that cannot be added shows the server message and keeps the list', async () => {
  server([member('bosun')], (c) => (c.method === 'PUT' && to(c, '/sources') ? json({ error: 'hubot/crew has no crew/ folder' }, 404) : undefined), view(['a/b']));
  page(null, fleetOf(['a/b']));
  await openImport();
  fireEvent.input(screen.getByLabelText(/source repo/i), { target: { value: 'hubot/crew' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add source' }));
  expect((await screen.findByRole('alert')).textContent).toContain('no crew/ folder');
  expect(screen.queryByText('hubot/crew')).toBeNull();
});

test('Browse lists a source\'s members, shows Imported instead of Import on one already in the fleet, and previews a row on Import', async () => {
  const calls = server(
    [member('bosun')],
    (c) => {
      if (c.method === 'GET' && to(c, '/sources/hubot/crew')) return json({ members: [{ name: 'scout', path: 'crew/scout.md', blob: 'b1', imported: 'scout-hubot', published: false }, { name: 'mate', path: 'crew/mate.md', blob: 'b2', imported: null, published: false }] });
      if (to(c, '/imports/preview')) return json({ ...pv, path: 'crew/mate.md' });
      return undefined;
    },
    view(['hubot/crew']),
  );
  page(null, fleetOf(['hubot/crew']));
  await openImport();
  fireEvent.click(screen.getAllByRole('button', { name: 'Browse' })[1]!);
  const list = await screen.findByRole('list', { name: 'hubot/crew' });
  const rows = within(list).getAllByRole('listitem');
  expect(rows).toHaveLength(2);
  expect(within(rows[0]!).getByText('Imported')).toBeTruthy();
  expect(within(rows[0]!).queryByRole('button', { name: 'Import' })).toBeNull();
  fireEvent.click(within(rows[1]!).getByRole('button', { name: 'Import' }));
  await waitFor(() => expect(named(calls, 'POST', '/imports/preview')).toHaveLength(1));
  expect(named(calls, 'POST', '/imports/preview')[0]!.body).toEqual({ ref: 'hubot/crew/mate' });
});

test('every row of the share repo has Unpublish, which opens the same confirm as a member\'s', async () => {
  const calls = server(
    [member('bosun')],
    (c) => {
      if (c.method === 'GET' && to(c, '/sources/octocat/admiral-crew')) return json({ members: [{ name: 'gone', path: 'crew/gone.md', blob: 'b1', imported: null, published: true }, { name: 'bosun', path: 'crew/bosun.md', blob: 'b2', imported: null, published: true }] });
      if (c.method === 'GET' && to(c, '/published/gone')) return json({ name: 'gone', repo: 'octocat/admiral-crew', bytes: '---\nname: gone\n---\n', blob: 'b1' });
      return undefined;
    },
    view([]),
  );
  page();
  await openImport();
  fireEvent.click(screen.getByRole('button', { name: 'Browse' }));
  const list = await screen.findByRole('list', { name: 'octocat/admiral-crew' });
  expect(within(list).getAllByRole('button', { name: 'Unpublish' })).toHaveLength(2);
  fireEvent.click(within(list).getAllByRole('button', { name: 'Unpublish' })[0]!);
  expect(await screen.findByText(/It stays in the repo's history/)).toBeTruthy();
  expect(named(calls, 'GET', '/published/gone')).toHaveLength(1);
});

test('a shared member shows "shared by" linked to the shared_url the server built, with the short commit and tag', async () => {
  const url = 'https://github.com/hub%20bot/crew/blob/' + commit + '/crew/scout.md';
  server([member('scout', { shared: { owner: 'hub bot', repo: 'crew', commit, tag: 'v1', detached: false }, shared_url: url })], () => undefined);
  page('scout');
  const link = await screen.findByRole('link', { name: /shared by hub bot/i });
  expect(link.getAttribute('href')).toBe(url);
  expect(link.textContent).toContain('hub bot');
  expect(screen.getAllByText(/0123456\b/).length).toBeGreaterThan(0);
  expect(screen.getAllByText(/v1/).length).toBeGreaterThan(0);
});
