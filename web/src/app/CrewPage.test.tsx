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

const fleet = { key: 'zulu', name: 'Zulu', docks: [{ harness: 'claude' }] } as unknown as Fleet;

const member = (name: string, pick: string, over: Record<string, unknown> = {}) => ({ name, role: 'scribe', 'based-on': null, skills: [], model: null, harness: null, 'pick-me-when': pick, instructions: '', layer: 'fleet', customized: false, notes: { fleet: [], repos: {} }, ...over });

interface Call {
  method: string;
  path: string;
  body?: Record<string, unknown>;
}

// A fake crew API: GET serves `state`, every other request is recorded and answered by `reply`.
function server(state: { members: unknown[]; unclaimed?: unknown[] }, reply: (c: Call) => Response | undefined = () => undefined) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      const c: Call = { method, path, body: init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined };
      if (method === 'GET' && path.startsWith('/api/v1/fleets/zulu/crew')) return new Response(JSON.stringify({ harness: 'claude', members: state.members, unclaimed: state.unclaimed ?? [] }));
      if (method === 'GET') return new Response(JSON.stringify({ skills: [] }));
      calls.push(c);
      return reply(c) ?? new Response('{}');
    }),
  );
  return calls;
}

// A button keeps its spinner for a moment after its request settles, and ids repeat between tests, so a click right after another test's Draft would be ignored.
const whenIdle = (b: HTMLElement) => waitFor(() => expect(b.getAttribute('aria-busy')).not.toBe('true'));

const page = (name: string | null = null, phone = false) => render(<CrewPage fleet={fleet} name={name} title phone={phone} />);

test('the crew is listed by layer, Fleet first, a customized built-in once and marked, a harness lock with its flag', async () => {
  server({
    members: [
      member('ada', 'Pick for parser work.', { harness: 'codex' }),
      member('mate', 'Pick for a sharper review.', { customized: true }),
      member('scout', 'Pick for the log.', { layer: 'built-in' }),
    ],
  });
  page();
  const fleetGroup = await screen.findByRole('region', { name: /^Fleet\b/ });
  const builtIn = screen.getByRole('region', { name: /^Built-in\b/ });
  expect(fleetGroup.compareDocumentPosition(builtIn) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  const rowOf = (g: HTMLElement, pick: string) => within(g).getByText(pick).closest('li')!;
  expect(within(rowOf(fleetGroup, 'Pick for a sharper review.')).getByText('customized')).toBeTruthy();
  expect(within(rowOf(fleetGroup, 'Pick for parser work.')).queryByText('customized')).toBeNull();
  expect(within(builtIn).queryByText('Pick for a sharper review.')).toBeNull();
  expect(within(rowOf(fleetGroup, 'Pick for parser work.')).getByRole('img', { name: 'runs on codex' })).toBeTruthy();
  expect(within(rowOf(builtIn, 'Pick for the log.')).queryByRole('img')).toBeNull();
  expect(screen.queryByRole('region', { name: /^Unclaimed notes/ })).toBeNull();
  expect(within(fleetGroup).getByRole('link', { name: /ada/ }).getAttribute('href')).toBe('/app/crew/ada');
});

test('on the phone the detail replaces the list, and an unknown name says so with a way back', async () => {
  server({ members: [member('ada', 'Pick for parser work.', { instructions: 'Work as ada.' }), member('bosun', 'Pick for rigging.')] });
  const { unmount } = page('ada', true);
  expect(await screen.findByText('Work as ada.')).toBeTruthy();
  expect(screen.queryByText('Pick for rigging.')).toBeNull();
  expect(screen.getByRole('link', { name: /back to crew/i }).getAttribute('href')).toBe('/app/crew');
  unmount();

  page('nobody', true);
  expect(await screen.findByText('Member not found.')).toBeTruthy();
  expect(screen.getByRole('link', { name: /back to crew/i })).toBeTruthy();
});

test('Customize for this fleet posts once and the built-in moves under Fleet with its notes', async () => {
  const state = { members: [member('scout', 'Pick for the log.', { layer: 'built-in', notes: { fleet: ['scout keeps the log short'], repos: {} } })] };
  const calls = server(state, (c) => {
    if (c.path.endsWith('/members/scout/customize')) state.members = [member('scout', 'Pick for the log.', { customized: true, notes: { fleet: ['scout keeps the log short'], repos: {} } })];
    return undefined;
  });
  page('scout');
  expect(await screen.findByText('scout keeps the log short')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Customize for this fleet' }));
  await waitFor(() => expect(within(screen.getByRole('region', { name: /^Fleet\b/ })).getByText('customized')).toBeTruthy());
  expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(['POST /api/v1/fleets/zulu/crew/members/scout/customize']);
  expect(screen.getByText('scout keeps the log short')).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Customize for this fleet' })).toBeNull();
});

test('an edit saves the changed pick-me-when, and a refused save shows the server message beside the field and keeps what was typed', async () => {
  const state = { members: [member('ada', 'Pick for parser work.', { skills: ['tdd'] })] };
  const calls = server(state, (c) => {
    if (c.method === 'PUT' && c.body?.['pick-me-when'] === '') return new Response(JSON.stringify({ error: 'pick-me-when is required', field: 'pick-me-when' }), { status: 400 });
    if (c.method === 'PUT') state.members = [member('ada', String(c.body?.['pick-me-when']), { skills: c.body?.skills })];
    return undefined;
  });
  page('ada');
  fireEvent.click(await screen.findByRole('button', { name: 'Edit', exact: true } as never));
  const pick = (await screen.findByLabelText(/pick me when/i)) as HTMLInputElement;

  fireEvent.input(pick, { target: { value: '' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(pick.getAttribute('aria-invalid')).toBe('true'));
  expect(pick.getAttribute('aria-describedby')).toBeTruthy();
  expect(document.getElementById(pick.getAttribute('aria-describedby')!)!.textContent).toContain('pick-me-when is required');
  expect(pick.value).toBe('');
  expect(screen.getByRole('form', { name: /edit ada/i })).toBeTruthy();

  fireEvent.input(pick, { target: { value: 'Pick for grammar work.' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(screen.queryByRole('form', { name: /edit ada/i })).toBeNull());
  expect(screen.getAllByText('Pick for grammar work.').length).toBeGreaterThan(0);
  const put = calls.filter((c) => c.method === 'PUT').pop()!;
  expect(put.path).toBe('/api/v1/fleets/zulu/crew/members/ada');
  expect(put.body).toMatchObject({ role: 'scribe', 'pick-me-when': 'Pick for grammar work.', skills: ['tdd'] });
});

test('Rename shows the server refusal in the sheet and keeps it open; Carry notes is off until checked and is what is sent', async () => {
  const state = { members: [member('dory', 'Pick for small jobs.'), member('ada', 'Pick for parser work.')] };
  const calls = server(state, (c) => (c.body?.to === 'ada' ? new Response(JSON.stringify({ error: 'ada already exists' }), { status: 409 }) : undefined));
  page('dory');
  const open = await screen.findByRole('button', { name: 'Rename', exact: true } as never);
  await whenIdle(open);
  fireEvent.click(open);
  const sheet = await screen.findByRole('dialog', { name: /rename dory/i });
  const carry = within(sheet).getByRole('checkbox', { name: 'Carry notes' }) as HTMLInputElement;
  expect(carry.checked).toBe(false);
  expect(sheet.textContent).toMatch(/unchecked leaves the notes under Unclaimed/i);

  fireEvent.input(within(sheet).getByLabelText(/new name/i), { target: { value: 'ada' } });
  fireEvent.click(within(sheet).getByRole('button', { name: 'Rename', exact: true } as never));
  expect((await within(sheet).findByRole('alert')).textContent).toBe('ada already exists');
  expect(screen.getByRole('dialog', { name: /rename dory/i })).toBeTruthy();

  fireEvent.input(within(sheet).getByLabelText(/new name/i), { target: { value: 'steward' } });
  fireEvent.click(carry);
  fireEvent.click(within(sheet).getByRole('button', { name: 'Rename', exact: true } as never));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: /rename dory/i })).toBeNull());
  expect(calls.map((c) => c.body)).toEqual([
    { to: 'ada', carry_notes: false },
    { to: 'steward', carry_notes: true },
  ]);
});

test('Delete asks in the page first and removes the member only on Confirm delete', async () => {
  const calls = server({ members: [member('dory', 'Pick for small jobs.')] });
  const confirm = vi.fn();
  vi.stubGlobal('confirm', confirm);
  page('dory');
  fireEvent.click(await screen.findByRole('button', { name: 'Delete', exact: true } as never));
  expect(screen.getByText(/Delete dory\? Its notes stay under Unclaimed\./)).toBeTruthy();
  expect(calls).toEqual([]);
  fireEvent.click(screen.getByRole('button', { name: 'Confirm delete' }));
  await waitFor(() => expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(['DELETE /api/v1/fleets/zulu/crew/members/dory']));
  expect(confirm).not.toHaveBeenCalled();
});

test('a note edits inline keeping its stamp out of the text, moves to another scope, and deletes', async () => {
  const stamped = '2026-11-10 (run-b): ada keeps handlers thin';
  const state = { members: [member('ada', 'Pick for parser work.', { notes: { fleet: ['ada reads the grammar first'], repos: { 'acme/api': [stamped, 'ada runs make check'] } } })] };
  const calls = server(state);
  page('ada');
  const scope = async (name: RegExp) => await screen.findByRole('region', { name });
  const note = async (text: string) => (await screen.findByText(text, { exact: false })).closest('li.crew-note')! as HTMLElement;

  fireEvent.click(within(await note('ada keeps handlers thin')).getByRole('button', { name: 'Edit note' }));
  const text = screen.getByRole('textbox', { name: 'Note text' }) as HTMLTextAreaElement;
  expect(text.value).toBe('ada keeps handlers thin');
  fireEvent.input(text, { target: { value: 'ada keeps handlers thin and typed' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save', exact: true } as never));
  await waitFor(() => expect(calls).toHaveLength(1));
  expect(calls[0]).toMatchObject({ method: 'POST', path: '/api/v1/fleets/zulu/crew/notes', body: { member: 'ada', op: 'edit', scope: 'repo', repo: 'acme/api', note: stamped, text: 'ada keeps handlers thin and typed' } });

  fireEvent.click(within(await note('ada reads the grammar first')).getByRole('button', { name: 'Move note' }));
  fireEvent.change(screen.getByRole('combobox', { name: 'Move to' }), { target: { value: 'repo:acme/api' } });
  fireEvent.click(screen.getByRole('button', { name: 'Move', exact: true } as never));
  await waitFor(() => expect(calls).toHaveLength(2));
  expect(calls[1].body).toMatchObject({ op: 'move', scope: 'fleet', note: 'ada reads the grammar first', to: { scope: 'repo', repo: 'acme/api' } });

  fireEvent.click(within(await note('ada runs make check')).getByRole('button', { name: 'Delete note' }));
  await waitFor(() => expect(calls).toHaveLength(3));
  expect(calls[2].body).toMatchObject({ op: 'delete', scope: 'repo', repo: 'acme/api', note: 'ada runs make check' });
  expect(await scope(/^This fleet\b/)).toBeTruthy();
});

test('notes left by a renamed member wait under Unclaimed, and deleting the last one removes the section', async () => {
  const state = { members: [member('boatswain', 'Pick for rigging.')], unclaimed: [{ member: 'bosun', notes: { fleet: ['bosun ties a bowline'], repos: {} } }] as unknown[] };
  server(state, (c) => {
    if (c.body?.op === 'delete') state.unclaimed = [];
    return undefined;
  });
  page();
  const unclaimed = await screen.findByRole('region', { name: /^Unclaimed notes\b/ });
  expect(unclaimed.textContent).toContain('bosun');
  fireEvent.click(within(unclaimed).getByRole('button', { name: 'Delete note' }));
  await waitFor(() => expect(screen.queryByRole('region', { name: /^Unclaimed notes\b/ })).toBeNull());
});

test('Add crew drafts from a description into a form and writes nothing until Save; Discard returns to the description, still filled', async () => {
  const draft = { name: 'quartermaster', role: 'scribe', skills: ['tdd'], 'pick-me-when': 'Pick for stocking the galley.', instructions: 'Keep the ship stocked.' };
  const calls = server({ members: [member('ada', 'Pick for parser work.')] }, (c) => (c.path.endsWith('/draft') ? new Response(JSON.stringify({ draft, problems: [] })) : undefined));
  history.pushState({}, '', '/app/crew?add=1');
  loc.value = { ...loc.value, path: '/app/crew', search: '?add=1' };
  page();
  const panel = await screen.findByRole('region', { name: 'Add crew' });
  const describe = within(panel).getByRole('textbox', { name: 'Describe the member' }) as HTMLTextAreaElement;
  fireEvent.input(describe, { target: { value: 'A quartermaster who keeps the ship stocked.' } });
  await whenIdle(within(panel).getByRole('button', { name: 'Draft' }));
  fireEvent.click(within(panel).getByRole('button', { name: 'Draft' }));
  const form = await screen.findByRole('form', { name: 'New crew member' });
  expect((within(form).getByLabelText('Name') as HTMLInputElement).value).toBe('quartermaster');
  expect(panel.textContent).toContain('Drafted from your description. Check every field before you save; nothing is written until you do.');
  expect(calls.map((c) => c.path)).toEqual(['/api/v1/fleets/zulu/crew/draft']);

  fireEvent.click(within(form).getByRole('button', { name: 'Discard' }));
  await waitFor(() => expect(screen.queryByRole('form', { name: 'New crew member' })).toBeNull());
  expect((within(panel).getByRole('textbox', { name: 'Describe the member' }) as HTMLTextAreaElement).value).toBe('A quartermaster who keeps the ship stocked.');
  expect(calls.some((c) => c.path.endsWith('/members'))).toBe(false);
});

test('a draft the resolver refuses shows its problem beside the field, and fixing the field there saves it', async () => {
  const draft = { name: 'purser', role: 'accountant', 'based-on': 'nobody', skills: ['tdd'], 'pick-me-when': 'Pick for the accounts.', instructions: 'Keep the books.' };
  const problems = [{ field: 'based-on', message: 'based-on names no built-in role' }];
  const calls = server({ members: [member('ada', 'Pick for parser work.')] }, (c) => (c.path.endsWith('/draft') ? new Response(JSON.stringify({ draft, problems })) : c.path.endsWith('/members') ? new Response(JSON.stringify({ name: 'purser' })) : undefined));
  loc.value = { ...loc.value, path: '/app/crew', search: '?add=1' };
  page();
  const panel = await screen.findByRole('region', { name: 'Add crew' });
  fireEvent.input(within(panel).getByRole('textbox', { name: 'Describe the member' }), { target: { value: 'A purser who keeps the accounts.' } });
  await whenIdle(within(panel).getByRole('button', { name: 'Draft' }));
  fireEvent.click(within(panel).getByRole('button', { name: 'Draft' }));
  const form = await screen.findByRole('form', { name: 'New crew member' });
  const basedOn = within(form).getByLabelText('Based on') as HTMLSelectElement;
  expect(document.getElementById(basedOn.getAttribute('aria-describedby')!)!.textContent).toContain('based-on names no built-in role');

  fireEvent.change(basedOn, { target: { value: 'scribe' } });
  fireEvent.click(within(form).getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(calls.some((c) => c.path.endsWith('/members'))).toBe(true));
  expect(calls.find((c) => c.path.endsWith('/members'))!.body).toMatchObject({ name: 'purser', 'based-on': 'scribe', 'pick-me-when': 'Pick for the accounts.' });
});

const withNotes = (fleetNotes: string[]) => ({ members: [member('ada', 'Pick for parser work.', { notes: { fleet: fleetNotes, repos: {} } })] });
const rowOf = async (text: string) => (await screen.findByText(text, { exact: false })).closest('li.crew-note') as HTMLElement;

test('a stamped note shows its stamp as muted text outside the markdown, and an unstamped note renders whole with no stamp', async () => {
  server(withNotes(['2026-11-10 (run-b): ```go\nfunc answer() int { return 42 }\n```', 'plain **fact** with no stamp']));
  page('ada');
  const stamped = (await screen.findByText('2026-11-10 (run-b):')).closest('li.crew-note') as HTMLElement;
  const stamp = stamped.querySelector('.crew-note-stamp')!;
  expect(stamp.textContent).toBe('2026-11-10 (run-b):');
  expect(stamp.closest('.md')).toBeNull();
  expect(stamped.querySelector('.md pre > code')!.textContent).toContain('func answer() int');

  const bare = await rowOf('plain');
  expect(bare.querySelector('.crew-note-stamp')).toBeNull();
  expect(bare.querySelector('.md strong')!.textContent).toBe('fact');
});

test('a note with markdown shows bold, a safe external link and a list as chat renders them', async () => {
  server(withNotes(['read **the log** at [the docs](https://example.com/docs) first\n\n- tidy\n- push']));
  page('ada');
  const row = await rowOf('the log');
  const md = row.querySelector('.md')!;
  expect(md.querySelector('strong')!.textContent).toBe('the log');
  const a = md.querySelector('a')!;
  expect(a.getAttribute('href')).toBe('https://example.com/docs');
  expect(a.getAttribute('target')).toBe('_blank');
  expect(a.getAttribute('rel')).toBe('noopener noreferrer');
  expect([...md.querySelectorAll('ul > li')].map((li) => li.textContent)).toEqual(['tidy', 'push']);
  expect(md.textContent).not.toContain('**');
});

test('a note shows inline code and a highlighted fenced block, and raw HTML stays text', async () => {
  server(withNotes(['run `make check` then\n\n```go\npackage main\n```\n\n<img src=x onerror="alert(1)">']));
  page('ada');
  const row = await rowOf('make check');
  const md = row.querySelector('.md')!;
  expect(md.querySelector('p code')!.textContent).toBe('make check');
  expect(md.querySelector('pre > code.hljs')!.textContent).toContain('package main');
  expect(md.querySelector('img')).toBeNull();
  expect(md.textContent).toContain('<img src=x onerror=');
});

test('the Move view renders the note as markdown too', async () => {
  server(withNotes(['2026-11-10 (run-b): keep **handlers** thin']));
  page('ada');
  fireEvent.click(within(await rowOf('handlers')).getByRole('button', { name: 'Move note' }));
  const row = (await screen.findByRole('combobox', { name: 'Move to' })).closest('li.crew-note') as HTMLElement;
  expect(row.querySelector('.md strong')!.textContent).toBe('handlers');
});

test('editing a markdown note shows the raw fact without its stamp and sends the raw strings; delete sends the raw note', async () => {
  const raw = '2026-11-10 (run-b): keep **handlers** thin, see `x.go`';
  const other = 'run `make check` before **pushing**';
  const calls = server(withNotes([raw, other]));
  page('ada');
  expect((await rowOf('handlers')).querySelector('.md strong')!.textContent).toBe('handlers');
  fireEvent.click(within(await rowOf('handlers')).getByRole('button', { name: 'Edit note' }));
  const text = screen.getByRole('textbox', { name: 'Note text' }) as HTMLTextAreaElement;
  expect(text.value).toBe('keep **handlers** thin, see `x.go`');
  fireEvent.input(text, { target: { value: 'keep **handlers** thin, see `y.go`' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save', exact: true } as never));
  await waitFor(() => expect(calls).toHaveLength(1));
  expect(calls[0].body).toMatchObject({ op: 'edit', scope: 'fleet', note: raw, text: 'keep **handlers** thin, see `y.go`' });

  fireEvent.click(within(await rowOf('pushing')).getByRole('button', { name: 'Delete note' }));
  await waitFor(() => expect(calls).toHaveLength(2));
  expect(calls[1].body).toMatchObject({ op: 'delete', scope: 'fleet', note: other });
});

test('an Unclaimed note renders its markdown the same way', async () => {
  server({ members: [member('boatswain', 'Pick for rigging.')], unclaimed: [{ member: 'bosun', notes: { fleet: ['2026-11-09 (run-a): tie a **bowline**'], repos: {} } }] });
  page();
  const unclaimed = await screen.findByRole('region', { name: /^Unclaimed notes\b/ });
  const row = unclaimed.querySelector('li.crew-note')!;
  expect(row.querySelector('.crew-note-stamp')!.textContent).toBe('2026-11-09 (run-a):');
  expect(row.querySelector('.md strong')!.textContent).toBe('bowline');
});
