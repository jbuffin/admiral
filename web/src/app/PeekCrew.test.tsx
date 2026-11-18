import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { useState } from 'preact/hooks';
import { afterEach, expect, test, vi } from 'vitest';
import { missionFold, readCrewMember } from './crewItems';
import type { Entry } from './inbox';
import { lockedBy } from './itemActions';
import { CrewMemberBody, CrewNotesBody, type MemberStep, MissionBody, useCrewNotes } from './PeekCrew';
import { Failure, useActing } from './PeekItem';

afterEach(() => vi.unstubAllGlobals());

const entry = (id: string, item: Record<string, unknown>) => ({ key: `item:_admiral/${id}`, type: 'item', session: '_admiral', id, folded: null, since: 0, item: { id, ...item } }) as unknown as Entry;

interface Sent {
  path: string;
  body: unknown;
}

function serve(reply: (path: string) => Response) {
  const sent: Sent[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      sent.push({ path, body: init?.body ? JSON.parse(init.body as string) : undefined });
      return reply(path);
    }),
  );
  return sent;
}

const press = (name: string, scope: HTMLElement | Document = document) => fireEvent.click(within(scope as HTMLElement).getByRole('button', { name }));
const note = (id: string, member: string, over: Record<string, unknown> = {}) => ({ id, member, role: '', scope: 'fleet', note: `${member} keeps notes`, replaces: null, ...over });

function Notes({ e, fold }: { e: Entry; fold: (t: string) => void }) {
  const acting = useActing(e, fold, lockedBy);
  const state = useCrewNotes(e);
  return <CrewNotesBody entry={e} acting={acting} state={state} decided={() => undefined} />;
}

const notes = (id: string, list: unknown[]) => entry(id, { kind: 'crew_notes', crew_notes: { fleet: 'zulu', ship: 'Dory', run_date: '2026-11-12', notes: list } });

test('crew notes: keep one, edit another to repo scope, and Send posts only those decisions once', async () => {
  const sent = serve(() => new Response('{}'));
  const fold = vi.fn();
  render(<Notes e={notes('cn-send', [note('n1', 'scout', { role: 'scribe', note: 'Scout dates every entry', replaces: 'scout keeps the log short' }), note('n2', 'ada', { note: 'Ada runs make check first' })])} fold={fold} />);
  const card = (n: number) => screen.getByRole('region', { name: `Note card ${n}` });
  const send = screen.getByRole('button', { name: 'Send' }) as HTMLButtonElement;
  expect(screen.getByText('Scout dates every entry')).toBeTruthy();
  expect(card(1).textContent).toContain('Replaces:');
  expect(card(1).textContent).toContain('scout keeps the log short');
  expect(screen.getByText('0 of 2 decided')).toBeTruthy();
  expect(send.disabled).toBe(true);

  press('Keep', card(1));
  press('Keep', card(2));
  expect(send.disabled).toBe(false);
  press('Edit', card(2));
  expect(send.disabled).toBe(true);
  expect(screen.getByText('Save or Cancel the open edit first.')).toBeTruthy();
  const text = within(card(2)).getByRole('textbox', { name: 'Edit note 2' });
  const save = () => within(card(2)).getByRole('button', { name: 'Save' }) as HTMLButtonElement;
  fireEvent.input(text, { target: { value: '' } });
  expect(save().disabled).toBe(true);
  fireEvent.input(text, { target: { value: 'Ada runs make check before anything' } });
  press('Repo', card(2));
  expect(within(card(2)).getByRole('button', { name: 'Repo' }).getAttribute('aria-pressed')).toBe('true');
  expect(within(card(2)).getByRole('button', { name: 'Fleet' }).getAttribute('aria-pressed')).toBe('false');
  const repo = within(card(2)).getByRole('textbox', { name: 'Repo (owner/repo)' });
  fireEvent.input(repo, { target: { value: 'acme' } });
  expect(save().disabled).toBe(true);
  fireEvent.input(repo, { target: { value: 'acme/api' } });
  press('Save', card(2));
  expect(screen.getByText('2 of 2 decided')).toBeTruthy();
  expect(within(card(2)).getByRole('button', { name: 'Edit' }).getAttribute('aria-pressed')).toBe('true');
  expect(send.disabled).toBe(false);

  fireEvent.click(send);
  await waitFor(() => expect(fold).toHaveBeenCalledWith('Crew notes sent: 1 kept, 1 edited, 0 dropped'));
  expect(sent).toEqual([
    {
      path: '/api/v1/inbox/_admiral/cn-send/crew-notes',
      body: { notes: { n1: { decision: 'keep' }, n2: { decision: 'edit', note: 'Ada runs make check before anything', scope: 'repo', repo: 'acme/api' } } },
    },
  ]);
});

test('a crew note the server cannot place shows its error on that card and keeps every decision', async () => {
  serve(() => new Response(JSON.stringify({ error: 'nothing was changed: no such line', note: 'n1' }), { status: 422 }));
  render(<Notes e={notes('cn-refused', [note('n1', 'scout'), note('n2', 'ada')])} fold={vi.fn()} />);
  const card = (n: number) => screen.getByRole('region', { name: `Note card ${n}` });
  press('Keep', card(1));
  press('Keep', card(2));
  press('Send');
  expect((await within(card(1)).findByRole('alert')).textContent).toContain('nothing was changed');
  expect(within(card(2)).queryByRole('alert')).toBeNull();
  expect(within(card(1)).getByRole('button', { name: 'Keep' }).getAttribute('aria-pressed')).toBe('true');
  expect(screen.getByText('2 of 2 decided')).toBeTruthy();

  press('Drop', card(1));
  expect(within(card(1)).queryByRole('alert')).toBeNull();
});

function Member({ e, fold, start = 'idle' }: { e: Entry; fold: (t: string) => void; start?: MemberStep }) {
  const acting = useActing(e, fold, lockedBy);
  const [step, setStep] = useState<MemberStep>(start);
  return (
    <>
      <CrewMemberBody entry={e} item={readCrewMember(e.item?.crew_member)} step={step} setStep={setStep} acting={acting} decided={() => undefined} />
      <Failure message={acting.error} />
    </>
  );
}

const member = (id: string, over: Record<string, unknown> = {}) => entry(id, { kind: 'crew_member', crew_member: { fleet: 'zulu', ship: 'Dory', run_date: '2026-11-12', member: 'deckhand', role: 'hand', ad_hoc_run: 'run-c', definition: 'You swab the deck.\n', notes: [note('n1', 'deckhand', { note: 'Deckhand swabs before it paints' })], ...over } });

test('an ad hoc member shows whole, Keep asks first, and the confirm sends one keep', async () => {
  const sent = serve(() => new Response('{}'));
  const fold = vi.fn();
  render(<Member e={member('cm-keep')} fold={fold} />);
  expect(screen.getByText('deckhand', { selector: 'b' })).toBeTruthy();
  expect(screen.getByText('Ad hoc run run-c')).toBeTruthy();
  expect(screen.getByText(/You swab the deck\./)).toBeTruthy();
  expect(screen.getByText('Deckhand swabs before it paints')).toBeTruthy();

  press('Keep');
  expect(screen.getByText("This adds the member to the fleet's crew for every later run and keeps its notes.")).toBeTruthy();
  expect(sent).toEqual([]);
  press('Keep deckhand');
  await waitFor(() => expect(fold).toHaveBeenCalledWith("Kept deckhand in the fleet's crew"));
  expect(sent).toEqual([{ path: '/api/v1/inbox/_admiral/cm-keep/crew-member', body: { decision: 'keep' } }]);
});

test('Drop on an ad hoc member says its file is deleted, Cancel backs out, and the confirm sends one drop', async () => {
  const sent = serve(() => new Response(JSON.stringify({ file_removed: true })));
  const fold = vi.fn();
  render(<Member e={member('cm-drop')} fold={fold} />);
  press('Drop');
  expect(screen.getByText('The ad hoc file is deleted and its notes are discarded.')).toBeTruthy();
  press('Cancel');
  expect(screen.getByRole('button', { name: 'Keep' })).toBeTruthy();
  expect(sent).toEqual([]);

  press('Drop');
  press('Drop deckhand');
  await waitFor(() => expect(fold).toHaveBeenCalled());
  expect(sent).toEqual([{ path: '/api/v1/inbox/_admiral/cm-drop/crew-member', body: { decision: 'drop' } }]);
});

test('a definition with hidden characters shows them marked, with how many', () => {
  render(<Member e={member('cm-hidden', { definition: 'You swab​ the deck.' })} fold={vi.fn()} />);
  expect(screen.getByText(/You swab⟨U\+200B⟩ the deck\./)).toBeTruthy();
  expect(screen.getByText('This definition has 1 hidden characters, shown as ⟨U+…⟩.')).toBeTruthy();
});

function Mission({ e, fold }: { e: Entry; fold: (t: string) => void }) {
  const acting = useActing(e, fold, lockedBy);
  const [step, setStep] = useState<MemberStep>('idle');
  const m = e.item?.mission as { name: string; text: string; scope: 'fleet' | 'repo'; repo?: string };
  return (
    <>
      <MissionBody entry={e} item={{ fleet: 'zulu', ship: 'Dory', run_date: '2026-11-12', repo: '', ...m }} step={step} setStep={setStep} acting={acting} decided={() => undefined} />
      <Failure message={acting.error} />
    </>
  );
}

const mission = (id: string, over: Record<string, unknown> = {}) => entry(id, { kind: 'mission', mission: { name: 'docs-sweep', scope: 'fleet', text: 'Sweep the docs.\nFix the links.\n', ...over } });

test('a mission shows its name and text, Keep says it is saved for the fleet, and the confirm sends one keep', async () => {
  const sent = serve(() => new Response(JSON.stringify({ file_written: true })));
  const fold = vi.fn();
  render(<Mission e={mission('ms-keep')} fold={fold} />);
  expect(screen.getByText('docs-sweep', { selector: 'b' })).toBeTruthy();
  expect(screen.getByText('Fleet')).toBeTruthy();
  expect(screen.getByText(/Sweep the docs\./)).toBeTruthy();

  press('Keep');
  expect(screen.getByText('The mission is saved for every later ship in the fleet.')).toBeTruthy();
  press('Keep docs-sweep');
  await waitFor(() => expect(fold).toHaveBeenCalledWith(missionFold('keep', 'docs-sweep', { file_written: true })));
  expect(sent).toEqual([{ path: '/api/v1/inbox/_admiral/ms-keep/mission', body: { decision: 'keep' } }]);
});

test('Drop on a mission says nothing is saved and sends one drop', async () => {
  const sent = serve(() => new Response('{}'));
  const fold = vi.fn();
  render(<Mission e={mission('ms-drop')} fold={fold} />);
  press('Drop');
  expect(screen.getByText('The mission is discarded and nothing is saved.')).toBeTruthy();
  expect(sent).toEqual([]);
  press('Drop docs-sweep');
  await waitFor(() => expect(fold).toHaveBeenCalledWith('Dropped mission docs-sweep'));
  expect(sent).toEqual([{ path: '/api/v1/inbox/_admiral/ms-drop/mission', body: { decision: 'drop' } }]);
});

test('a repo mission names its repo and marks hidden characters', () => {
  render(<Mission e={mission('ms-repo', { name: 'api-sweep', scope: 'repo', repo: 'acme/api', text: 'Sweep​ the api.' })} fold={vi.fn()} />);
  expect(screen.getByText('Repo acme/api')).toBeTruthy();
  expect(screen.getByText(/Sweep⟨U\+200B⟩ the api\./)).toBeTruthy();
  expect(screen.getByText('This mission has 1 hidden characters, shown as ⟨U+…⟩.')).toBeTruthy();
});

test('a mission Keep the server refuses shows its error and leaves Keep available', async () => {
  serve(() => new Response(JSON.stringify({ error: 'the fleet already has a mission docs-sweep with other text' }), { status: 409 }));
  render(<Mission e={mission('ms-refused')} fold={vi.fn()} />);
  press('Keep');
  press('Keep docs-sweep');
  expect((await screen.findByRole('alert')).textContent).toContain('already has a mission docs-sweep with other text');
  await waitFor(() => expect((screen.getByRole('button', { name: 'Keep docs-sweep' }) as HTMLButtonElement).disabled).toBe(false));
});

const card = (n: number) => screen.getByRole('region', { name: `Note card ${n}` });
const STAMPED = '2026-11-01 (some-run): uses **pnpm**';

test('a crew note renders its markdown: bold, inline code and a safe link, with no literal markers', () => {
  render(<Notes e={notes('cn-md', [note('n1', 'scout', { note: 'Use **pnpm**, run `make check` and read [the docs](https://example.com/docs)' })])} fold={vi.fn()} />);
  const c = card(1);
  expect(within(c).getByText('pnpm').tagName).toBe('STRONG');
  expect(within(c).getByText('make check').tagName).toBe('CODE');
  const link = within(c).getByRole('link', { name: 'the docs' });
  expect(link.getAttribute('href')).toBe('https://example.com/docs');
  expect(c.textContent).not.toContain('**');
  expect(c.textContent).not.toContain('`');
});

test('a crew note with a fenced block renders it as a code block', () => {
  render(<Notes e={notes('cn-fence', [note('n1', 'scout', { note: 'Run this:\n\n```sh\nmake check\n```\n' })])} fold={vi.fn()} />);
  const code = card(1).querySelector('pre > code');
  expect(code).not.toBeNull();
  expect(code!.textContent).toContain('make check');
  expect(card(1).textContent).not.toContain('```');
});

test('the Replaces line keeps its label, shows the stamp as text and renders the fact as markdown', () => {
  render(<Notes e={notes('cn-replaces', [note('n1', 'scout', { replaces: STAMPED })])} fold={vi.fn()} />);
  const c = card(1);
  expect(within(c).getByText('Replaces:', { selector: 'b' })).toBeTruthy();
  expect(c.textContent).toContain('2026-11-01 (some-run):');
  expect(within(c).getByText('pnpm').tagName).toBe('STRONG');
  expect(c.textContent).not.toContain('**');
  expect(c.querySelector('p p')).toBeNull();
});

test('a Replaces line with no stamp renders as markdown too', () => {
  render(<Notes e={notes('cn-unstamped', [note('n1', 'scout', { replaces: 'uses **pnpm**' })])} fold={vi.fn()} />);
  expect(within(card(1)).getByText('pnpm').tagName).toBe('STRONG');
});

test('raw HTML in a crew note or its Replaces line shows as text and creates no element', () => {
  render(<Notes e={notes('cn-html', [note('n1', 'scout', { note: '<img src=x onerror=alert(1)> and <script>alert(2)</script>', replaces: '2026-11-01 (some-run): <img src=y onerror=alert(3)>' })])} fold={vi.fn()} />);
  const c = card(1);
  expect(c.querySelector('img')).toBeNull();
  expect(c.querySelector('script')).toBeNull();
  expect(c.textContent).toContain('<img src=x onerror=alert(1)>');
  expect(c.textContent).toContain('<script>');
  expect(c.textContent).toContain('<img src=y onerror=alert(3)>');
});

test('a hidden character in a crew note or its Replaces line still shows marked', () => {
  render(<Notes e={notes('cn-hidden', [note('n1', 'scout', { note: 'Scout trusts‮ the log', replaces: '2026-11-01 (some-run): old‮ line' })])} fold={vi.fn()} />);
  expect(card(1).textContent).toContain('Scout trusts⟨U+202E⟩ the log');
  expect(card(1).textContent).toContain('old⟨U+202E⟩ line');
});

test('a hidden character written as an entity shows marked, in a crew note and in its Replaces line', () => {
  render(<Notes e={notes('cn-entity', [note('n1', 'scout', { note: 'Scout trusts &#x202E;the log', replaces: '2026-11-01 (some-run): old &#8203;line' })])} fold={vi.fn()} />);
  expect(card(1).textContent).toContain('Scout trusts ⟨U+202E⟩the log');
  expect(card(1).textContent).toContain('old ⟨U+200B⟩line');
  expect(card(1).textContent).not.toContain('\u202E');
});

test("a link's target shows next to its text when they differ", () => {
  render(<Notes e={notes('cn-link', [note('n1', 'scout', { note: 'Read [the docs](https://example.com/x) and https://example.com/y', replaces: '2026-11-01 (some-run): see [setup](https://example.com/s)' })])} fold={vi.fn()} />);
  const t = card(1).textContent!;
  expect(t).toContain('the docs (https://example.com/x)');
  expect(t).toContain('setup (https://example.com/s)');
  expect(t).not.toContain('https://example.com/y (');
});

test('a crew note that renders to nothing shows its source as text', () => {
  render(<Notes e={notes('cn-empty', [note('n1', 'scout', { note: '[a]: https://example.com "t"' })])} fold={vi.fn()} />);
  expect(card(1).textContent).toContain('[a]: https://example.com "t"');
});

test('the crew notes under a crew member render markdown the same way', () => {
  render(<Member e={member('cm-md', { notes: [note('n1', 'deckhand', { note: 'Swab **first**, then `paint`', replaces: STAMPED })] })} fold={vi.fn()} />);
  const c = card(1);
  expect(within(c).getByText('first').tagName).toBe('STRONG');
  expect(within(c).getByText('paint').tagName).toBe('CODE');
  expect(within(c).getByText('pnpm').tagName).toBe('STRONG');
  expect(c.textContent).not.toContain('**');
  expect(c.textContent).toContain('2026-11-01 (some-run):');
});

test('an edited crew note shows its edited text rendered', () => {
  render(<Notes e={notes('cn-edit-md', [note('n1', 'scout')])} fold={vi.fn()} />);
  press('Edit', card(1));
  fireEvent.input(within(card(1)).getByRole('textbox', { name: 'Edit note 1' }), { target: { value: 'Scout runs **make check** first' } });
  press('Save', card(1));
  expect(within(card(1)).getByText('make check').tagName).toBe('STRONG');
  expect(card(1).textContent).not.toContain('**');
});
