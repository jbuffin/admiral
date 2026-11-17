import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet } from './api';
import type { CrewAnswer, SkillsAnswer } from './launchForm';
import { LaunchForm, type OrderRef } from './LaunchDialog';
import { resources } from './shipsLink';

const proj = '/home/jb/proj';
const fleet = { key: 'zulu', docks: [{ harness: 'claude' }, { harness: 'codex' }] } as Fleet;
const order: OrderRef = { source: 'orders', id: 'ord-api', title: 'Tighten the api limits', text: 'Tighten them.' };

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const entry = { path: proj, name: 'proj', kind: 'checkout', ships_here: 0 };
const dirsOf = (over: object = {}) => json({ entries: [entry], selected: proj, clone: null, ...over });
const installed = (name: string, description = '') => ({ name, description, favorite: false });
const answers: Record<string, SkillsAnswer> = {
  claude: { mark: '/', commodore: { found: false, name: 'commodore' }, skills: [installed('review', 'Review the diff before it merges.'), installed('tdd', 'Test first, then the code.'), installed('fable-claude'), installed('fable-both')] },
  codex: { mark: '$', commodore: { found: false, name: 'commodore' }, skills: [installed('review'), installed('tdd'), installed('fable-codex'), installed('fable-both')] },
};
const noCrew: CrewAnswer = { members: [], warnings: [], errors: [] };

interface Routes {
  dirs?: () => Response;
  skills?: (harness: string) => Response | Promise<Response>;
  crew?: () => Response;
  ships?: () => Response;
}

// The server's answers, stubbed; the returned array holds the body of every launch posted.
function serve(routes: Routes = {}) {
  const posts: Record<string, unknown>[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init: RequestInit = {}) => {
      const url = new URL(path, 'http://admiral.test');
      const route = url.pathname.replace('/api/v1', '');
      if (route === '/launch-orders/dirs') return (routes.dirs ?? dirsOf)();
      if (route === '/launch-orders/skills') return (routes.skills ?? ((h) => json(answers[h])))(url.searchParams.get('harness')!);
      if (route === '/launch-orders/crew') return (routes.crew ?? (() => json(noCrew)))();
      if (route === '/ships' && init.method === 'POST') {
        posts.push(JSON.parse(init.body as string));
        return (routes.ships ?? (() => json({ ship: { session: 'zulu-1' } }, 201)))();
      }
      throw new Error(`unstubbed ${init.method ?? 'GET'} ${path}`);
    }),
  );
  return posts;
}

function open(given?: OrderRef) {
  const props = { onClose: vi.fn(), onLaunched: vi.fn() };
  render(<LaunchForm fleet={fleet} order={given} {...props} />);
  return props;
}

const box = () => screen.getByRole('combobox', { name: 'Skill' }) as HTMLInputElement;
const checkout = () => screen.getByLabelText('Checkout') as HTMLSelectElement;
const launch = () => screen.getByRole('button', { name: 'Launch' }) as HTMLButtonElement;
const dock = (name: RegExp) => screen.getByRole('radio', { name }) as HTMLInputElement;
const type = (text: string) => fireEvent.input(box(), { target: { value: text } });
const option = (name: RegExp) => screen.getByRole('option', { name });
const listed = () => within(screen.getByRole('listbox')).getAllByRole('option').map((o) => o.textContent);
const choose = (name: RegExp) => fireEvent.click(dock(name));

// The skills of the dock answered and the checkout chosen, so a typed name can be checked.
async function ready(harness = 'claude') {
  await waitFor(() => expect(checkout().value).not.toBe(''));
  await waitFor(() => expect(vi.mocked(fetch).mock.calls.some(([p]) => String(p).includes(`harness=${harness}`) && String(p).includes('/skills'))).toBe(true));
  await waitFor(() => expect(box().placeholder).toBe(''));
}

beforeEach(() => {
  resources.value = [];
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test('an order opens with its text and checkout, offers the skills as the dock has them, and a picked skill is what it launches', async () => {
  const posts = serve();
  const { onClose, onLaunched } = open(order);
  await ready();

  expect((screen.getByLabelText('Text') as HTMLTextAreaElement).value).toBe('Tighten them.');
  expect(checkout().value).toBe(proj);
  expect(box().value).toBe('Just launch');
  expect(screen.getByRole('dialog').textContent).not.toContain('Clone ');

  fireEvent.click(box());
  expect(option(/^Commodore/).textContent).toContain('not found');
  expect(option(/^Commodore/).getAttribute('aria-disabled')).toBe('true');
  expect(option(/^Just launch/)).toBeTruthy();

  type('rev');
  expect(option(/^\/review/).textContent).toContain('Review the diff before it merges.');
  expect(screen.queryByRole('option', { name: /tdd/ })).toBeNull();
  fireEvent.click(option(/^\/review/));
  expect(screen.queryByRole('listbox')).toBeNull();
  expect(screen.getByRole('dialog')).toBeTruthy();
  expect(box().value).toBe('/review');

  fireEvent.click(launch());
  await waitFor(() => expect(onLaunched).toHaveBeenCalledWith('zulu-1'));
  expect(onClose).toHaveBeenCalled();
  expect(posts).toHaveLength(1);
  expect(posts[0]).toMatchObject({ fleet: 'zulu', harness: 'claude', source: 'orders', order_id: 'ord-api', dir: proj, skill: { kind: 'skill', name: 'review' } });
});

test('the keys pick too: Arrow moves, Enter takes a row without sending, a leading slash is no part of the name, and Escape closes the list only', async () => {
  const posts = serve();
  const { onClose } = open();
  await ready();

  type('/fable-cl');
  expect(option(/^\/fable-claude/)).toBeTruthy();
  fireEvent.keyDown(box(), { key: 'ArrowDown' });
  fireEvent.keyDown(box(), { key: 'Enter' });
  expect(screen.queryByRole('listbox')).toBeNull();
  expect(box().value).toBe('/fable-claude');

  type('re');
  expect(screen.getByRole('listbox')).toBeTruthy();
  fireEvent.keyDown(box(), { key: 'Escape' });
  expect(screen.queryByRole('listbox')).toBeNull();
  expect(onClose).not.toHaveBeenCalled();
  expect(posts).toEqual([]);
});

test('a name that is not whole warns that the dock lacks it, and the warning goes when the name completes', async () => {
  serve();
  open();
  await ready();

  type('fable-claud');
  expect(screen.getByRole('dialog').textContent).toContain('not installed on claude');
  type('fable-claude');
  expect(screen.getByRole('dialog').textContent).not.toContain('not installed on');
  expect(option(/^\/fable-claude/)).toBeTruthy();
});

test('a name no dock has is refused in the form: the warning shows, Launch is off, and Enter sends nothing until a listed skill is picked', async () => {
  const posts = serve();
  open();
  await ready();

  type('not-a-skill');
  expect(screen.queryByRole('listbox')).toBeNull();
  expect(screen.getByRole('dialog').textContent).toContain('not installed on claude');
  expect(launch().disabled).toBe(true);
  fireEvent.keyDown(box(), { key: 'Enter' });
  fireEvent.submit(screen.getByRole('dialog').querySelector('form')!);
  expect(posts).toEqual([]);

  choose(/codex/i);
  await waitFor(() => expect(screen.getByRole('dialog').textContent).toContain('not installed on codex'));
  expect(launch().disabled).toBe(true);
  choose(/claude/i);
  await waitFor(() => expect(screen.getByRole('dialog').textContent).toContain('not installed on claude'));

  type('review');
  fireEvent.click(option(/^\/review/));
  expect(screen.getByRole('dialog').textContent).not.toContain('not installed on');
  expect(launch().disabled).toBe(false);
});

test('changing the dock re-reads the skills: a skill only the old dock has falls back to the default, each dock lists its own, and a skill both have stays picked with that dock\'s mark', async () => {
  serve();
  open();
  await ready();

  type('fable-claude');
  fireEvent.click(option(/^\/fable-claude/));
  expect(box().value).toBe('/fable-claude');
  const efforts = () => within(screen.getByLabelText('Effort')).getAllByRole('option').map((o) => o.textContent);
  expect(efforts()).toEqual(['Default', 'Low', 'Medium', 'High', 'Max']);

  choose(/codex/i);
  await waitFor(() => expect(box().value).toMatch(/^(Commodore|Just launch)$/));
  type('fable-');
  expect(listed()).toEqual(expect.arrayContaining([expect.stringContaining('fable-codex'), expect.stringContaining('fable-both')]));
  expect(screen.queryByRole('option', { name: /fable-claude/ })).toBeNull();
  fireEvent.keyDown(box(), { key: 'Escape' });
  expect(efforts()).not.toContain('Max');

  choose(/claude/i);
  type('fable-');
  expect(await screen.findByRole('option', { name: /^\/fable-claude/ })).toBeTruthy();
  expect(screen.queryByRole('option', { name: /fable-codex/ })).toBeNull();
  fireEvent.keyDown(box(), { key: 'Escape' });

  type('fable-both');
  fireEvent.click(option(/^\/fable-both/));
  choose(/codex/i);
  await waitFor(() => expect(box().value).toBe('$fable-both'));
  choose(/claude/i);
  await waitFor(() => expect(box().value).toBe('/fable-both'));
});

test('a dock whose skills have not answered cannot launch the last dock\'s pick: Launch stays off and nothing is sent until they answer', async () => {
  let answer!: () => void;
  const held = new Promise<void>((r) => (answer = r));
  const posts = serve({ skills: (h) => (h === 'codex' ? held.then(() => json(answers.codex)) : json(answers[h])) });
  open();
  await ready();
  type('fable-claude');
  fireEvent.click(option(/^\/fable-claude/));

  choose(/codex/i);
  await waitFor(() => expect(launch().disabled).toBe(true));
  fireEvent.keyDown(box(), { key: 'Enter' });
  fireEvent.submit(screen.getByRole('dialog').querySelector('form')!);
  expect(posts).toEqual([]);

  answer();
  await waitFor(() => expect(launch().disabled).toBe(false));
  expect(box().value).toMatch(/^(Commodore|Just launch)$/);
});

test('a dock whose skills fail to load says why and cannot launch, and the next good read clears it', async () => {
  serve({ skills: (h) => (h === 'codex' ? json({ error: 'skills are down' }, 500) : json(answers[h])) });
  open();
  await ready();
  type('fable-claude');
  fireEvent.click(option(/^\/fable-claude/));

  choose(/codex/i);
  await waitFor(() => expect(screen.getByRole('dialog').textContent).toContain("Couldn't load skills: skills are down"));
  expect(launch().disabled).toBe(true);

  choose(/claude/i);
  await waitFor(() => expect(screen.getByRole('dialog').textContent).not.toContain("Couldn't load skills"));
  expect(launch().disabled).toBe(false);
});

test('a freeform launch sends the text, model, effort and resources the form holds, and no order', async () => {
  resources.value = [{ name: 'db', capacity: 1, in_use: 0 }];
  const posts = serve();
  open();
  await ready();

  fireEvent.input(screen.getByLabelText('Text'), { target: { value: '  look at the parser  ' } });
  fireEvent.change(screen.getByLabelText('Model'), { target: { value: 'opus' } });
  fireEvent.change(screen.getByLabelText('Effort'), { target: { value: 'high' } });
  fireEvent.click(screen.getByRole('checkbox', { name: /^db/ }));
  type('review');
  fireEvent.click(option(/^\/review/));
  fireEvent.click(launch());

  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({ fleet: 'zulu', harness: 'claude', dir: proj, skill: { kind: 'skill', name: 'review' }, model: 'opus', effort: 'high', resources: ['db'], autopilot: false });
  expect(posts[0]).not.toHaveProperty('order_id');
  expect(String(posts[0]!.text).trim()).toBe('look at the parser');
});

test('a launch the server refuses shows its message and keeps the form open, for a held resource, an autopilot guard and a clone folder', async () => {
  resources.value = [{ name: 'db', capacity: 1, in_use: 1 }];
  const refusals = [
    [409, 'resource db is held by ship zulu-1'],
    [400, 'autopilot on codex: no guard probe has passed for it'],
    [409, '/home/jb/gadget already exists, so github.com/acme/gadget was not cloned there'],
  ] as const;
  let n = 0;
  serve({ ships: () => json({ error: refusals[n++]![1] }, refusals[n - 1]![0]) });
  const { onClose, onLaunched } = open();
  await ready();

  for (const [, message] of refusals) {
    fireEvent.click(launch());
    expect((await screen.findByText(message)).getAttribute('role')).toBe('alert');
    expect(screen.getByRole('dialog')).toBeTruthy();
    expect(launch().disabled).toBe(false);
  }
  expect(onClose).not.toHaveBeenCalled();
  expect(onLaunched).not.toHaveBeenCalled();
});

test('the autopilot box is sent with the launch', async () => {
  const posts = serve();
  open();
  await ready();

  fireEvent.click(screen.getByRole('checkbox', { name: /autopilot/i }));
  fireEvent.click(launch());

  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({ harness: 'claude', autopilot: true });
});

const commodore: SkillsAnswer = { ...answers.claude!, commodore: { found: true, name: 'commodore' } };
const scout = { name: 'scout', role: 'scribe', layer: 'built-in', harness: null, 'pick-me-when': 'Pick scout.' };

test('Commodore is the default when found, listed first, then the favorites, then Just launch', async () => {
  serve({ skills: () => json({ ...commodore, skills: [{ ...installed('review'), favorite: true }, { ...installed('tdd'), favorite: true }, installed('other')] }) });
  open();
  await ready();

  expect(box().value).toMatch(/commodore/i);
  fireEvent.click(box());
  expect(listed()).toEqual([expect.stringMatching(/^Commodore/), expect.stringMatching(/^\/review/), expect.stringMatching(/^\/tdd/), expect.stringMatching(/^Just launch/)]);
  expect(option(/^Commodore/).getAttribute('aria-disabled')).toBeNull();
});

test('a pinned crew member goes with a Commodore launch, the control goes with Just launch, and the Just launch button sends no skill and no pins', async () => {
  const posts = serve({ skills: () => json(commodore), crew: () => json({ ...noCrew, members: [scout] }) });
  open();
  await ready();

  const group = await screen.findByRole('group', { name: /crew/i });
  const pin = (g: HTMLElement) => {
    const scoutBox = within(g).getByRole('checkbox', { name: /scout/ }) as HTMLInputElement;
    if (!scoutBox.checked) fireEvent.click(scoutBox);
    expect(scoutBox.checked).toBe(true);
  };
  pin(group);

  fireEvent.click(box());
  fireEvent.click(option(/^Just launch/));
  await waitFor(() => expect(screen.queryByRole('group', { name: /crew/i })).toBeNull());
  expect(screen.queryByRole('button', { name: 'Just launch' })).toBeNull();

  fireEvent.click(box());
  fireEvent.click(option(/^Commodore/));
  pin(await screen.findByRole('group', { name: /crew/i }));
  fireEvent.input(screen.getByLabelText('Text'), { target: { value: 'with a pinned scout' } });
  fireEvent.click(launch());
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({ skill: { kind: 'commodore' }, crew: ['scout'], text: 'with a pinned scout' });

  fireEvent.input(screen.getByLabelText('Text'), { target: { value: 'just launched' } });
  fireEvent.click(screen.getByRole('button', { name: 'Just launch' }));
  await waitFor(() => expect(posts).toHaveLength(2));
  expect(posts[1]).toMatchObject({ skill: { kind: 'none' }, text: 'just launched' });
  expect(posts[1]).not.toHaveProperty('crew');
});

test('Launch as offers the fleet\'s members on Just launch, says where a locked one runs, and sends it with that harness and no pins', async () => {
  const ada = { name: 'ada', role: 'implementer', layer: 'fleet', harness: 'codex', 'pick-me-when': 'Pick ada.' };
  const posts = serve({ crew: () => json({ ...noCrew, members: [ada] }) });
  open();
  await ready();

  const as = (await screen.findByLabelText('Launch as')) as HTMLSelectElement;
  expect(as.value).toBe('');
  fireEvent.change(as, { target: { value: 'ada' } });
  await waitFor(() => expect(screen.getByRole('dialog').textContent).toContain('runs on codex'));
  fireEvent.input(screen.getByLabelText('Text'), { target: { value: 'as ada' } });
  fireEvent.click(launch());

  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({ skill: { kind: 'none' }, member: 'ada', harness: 'codex' });
  expect(posts[0]).not.toHaveProperty('crew');
});

test('an uncloned repo says where it will clone, is preselected, and launches as a clone with the repo shown and no dir', async () => {
  const posts = serve({ dirs: () => dirsOf({ selected: null, clone: { repo: 'github.com/acme/widget', path: '/home/jb/widget' } }) });
  open(order);
  await ready();

  await waitFor(() => expect(checkout().value).toBe('clone'));
  expect(screen.getByRole('dialog').textContent).toMatch(/Clone github\.com\/acme\/widget into \/home\/jb\/widget before launch/);
  fireEvent.click(launch());

  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({ fleet: 'zulu', source: 'orders', order_id: 'ord-api', clone: true, clone_repo: 'github.com/acme/widget' });
  expect(posts[0]).not.toHaveProperty('dir');
});

test('a folder already where the clone would land is warned about and not preselected, and choosing the clone anyway shows the server\'s refusal', async () => {
  const gadget = '/home/jb/gadget';
  const posts = serve({
    dirs: () => dirsOf({ clone: { repo: 'github.com/acme/gadget', path: gadget, exists: true } }),
    ships: () => json({ error: `${gadget} already exists, so github.com/acme/gadget was not cloned there` }, 409),
  });
  open(order);
  await ready();

  await waitFor(() => expect(screen.getByRole('dialog').textContent).toMatch(/already exists.*refused/));
  expect(screen.getByRole('dialog').textContent).toContain(gadget);
  expect(checkout().value).not.toBe('clone');

  fireEvent.change(checkout(), { target: { value: 'clone' } });
  fireEvent.click(launch());
  const alert = await screen.findByRole('alert');
  expect(alert.textContent).toContain('gadget');
  expect(posts[0]).toMatchObject({ clone: true, clone_repo: 'github.com/acme/gadget' });
  expect(screen.getByRole('dialog')).toBeTruthy();
});
