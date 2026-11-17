import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet } from './api';
import type { CrewAnswer, SkillsAnswer } from './launchForm';
import { LaunchForm, type CampaignChildRef, type OrderRef } from './LaunchDialog';
import { resources } from './shipsLink';

const proj = '/home/jb/proj';
const fleet = { key: 'zulu', docks: [{ harness: 'claude' }, { harness: 'codex' }] } as Fleet;
const order: OrderRef = { source: 'orders', id: 'ord-api', title: 'Tighten the api limits', text: 'Tighten them.' };

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const entry = { path: proj, name: 'proj', kind: 'checkout', ships_here: 0 };
const dirsOf = (over: object = {}) => json({ entries: [entry], selected: proj, clone: null, ...over });
const installed = (name: string, description = '') => ({ name, description, favorite: false });
const answers: Record<string, SkillsAnswer> = {
  claude: { mark: '/', commodore: { found: false, name: 'commodore', state: 'missing' }, skills: [installed('review', 'Review the diff before it merges.'), installed('tdd', 'Test first, then the code.'), installed('fable-claude'), installed('fable-both')] },
  codex: { mark: '$', commodore: { found: false, name: 'commodore', state: 'missing' }, skills: [installed('review'), installed('tdd'), installed('fable-codex'), installed('fable-both')] },
};
const noCrew: CrewAnswer = { members: [], warnings: [], errors: [] };

// The body of every install or update posted since the last serve().
let commodorePosts: Record<string, unknown>[] = [];

interface Routes {
  dirs?: () => Response;
  skills?: (harness: string) => Response | Promise<Response>;
  crew?: () => Response;
  ships?: () => Response;
  commodore?: (body: Record<string, unknown>) => Response | Promise<Response>;
}

// The server's answers, stubbed; the returned array holds the body of every launch posted.
function serve(routes: Routes = {}) {
  const posts: Record<string, unknown>[] = [];
  commodorePosts = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init: RequestInit = {}) => {
      const url = new URL(path, 'http://admiral.test');
      const route = url.pathname.replace('/api/v1', '');
      if (route === '/launch-orders/dirs') return (routes.dirs ?? dirsOf)();
      if (route === '/launch-orders/skills') return (routes.skills ?? ((h) => json(answers[h])))(url.searchParams.get('harness')!);
      if (route === '/launch-orders/crew') return (routes.crew ?? (() => json(noCrew)))();
      if (route === '/launch-orders/commodore' && init.method === 'POST') {
        const body = JSON.parse(init.body as string);
        commodorePosts.push(body);
        return (routes.commodore ?? (() => json({ state: 'ok' })))(body);
      }
      if (/^\/campaigns\/[^/]+\/launch$/.test(route) && init.method === 'POST') {
        posts.push(JSON.parse(init.body as string));
        return (routes.ships ?? (() => json({ ship: { session: 'zulu-1' } }, 201)))();
      }
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

const child: CampaignChildRef = { campaignId: 'camp-1', url: 'https://github.com/acme/widgets/issues/24', title: 'Map the parser', mode: 'with-you', skill: { kind: 'skill', name: 'wayfinder', args: '' }, startAnyway: false };

function openChild() {
  render(<LaunchForm fleet={fleet} campaign={child} onClose={vi.fn()} onLaunched={vi.fn()} />);
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

const commodore: SkillsAnswer = { ...answers.claude!, commodore: { found: true, name: 'commodore', state: 'ok' } };
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

// Install and Update for Commodore: the answer's `state` decides what the line beside the skill box offers.
const withState = (harness: string, commodoreAnswer: SkillsAnswer['commodore']): SkillsAnswer => ({ ...answers[harness]!, commodore: commodoreAnswer });
const missing = withState('claude', { found: false, name: 'commodore', state: 'missing' });
const outdated = withState('claude', { found: true, name: 'commodore', state: 'outdated', installed: '3.1.0', minimum: '3.5.0' });
const installButton = () => screen.queryByRole('button', { name: /^Install Commodore/ });
const updateButton = () => screen.queryByRole('button', { name: /^Update/ });
const dialogText = () => screen.getByRole('dialog').textContent ?? '';
const reads = () => vi.mocked(fetch).mock.calls.filter(([p]) => String(p).includes('/launch-orders/skills')).length;
const failedInstall = { error: 'claude plugin install failed', output: 'line one\nboom: no network', commands: ['claude plugin marketplace add jbuffin/skills', 'claude plugin install jasonbuffin@jbuffinskills'], state: { state: 'missing' } };

test('a dock with no Commodore offers Install Commodore beside the box, with the list shut, and the form never says "not installed on" for it', async () => {
  serve({ skills: () => json(missing) });
  open();
  await ready();

  expect(await screen.findByRole('button', { name: 'Install Commodore' })).toBeTruthy();
  expect(screen.queryByRole('listbox')).toBeNull();
  expect(dialogText()).not.toContain('not installed on');
  expect(updateButton()).toBeNull();
  fireEvent.click(box());
  expect(option(/^Commodore/).getAttribute('aria-disabled')).toBe('true');
  expect(option(/^Commodore/).querySelector('button')).toBeNull();
});

test('a project copy of Commodore launches as before and still gets the Install offer, because only the dock\'s own identity decides it', async () => {
  const posts = serve({ skills: () => json(withState('claude', { found: true, name: 'commodore', state: 'missing' })) });
  open();
  await ready();

  expect(await screen.findByRole('button', { name: 'Install Commodore' })).toBeTruthy();
  expect(box().value).toBe('Commodore');
  fireEvent.click(launch());
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({ skill: { kind: 'commodore' } });
});

test('with Commodore ok nothing new shows: no Install, no Update, no version line', async () => {
  serve({ skills: () => json(commodore) });
  open();
  await ready();

  expect(box().value).toBe('Commodore');
  expect(installButton()).toBeNull();
  expect(updateButton()).toBeNull();
  expect(dialogText()).not.toMatch(/older than|version unknown|Installing|Updating/);
});

test('pressing Install posts the fleet, dock and action once, shows it running, ignores a second press, waits the launch limit, and sends no launch', async () => {
  let finish!: () => void;
  const held = new Promise<void>((r) => (finish = r));
  const timers = vi.spyOn(globalThis, 'setTimeout');
  const posts = serve({ skills: () => json(missing), commodore: () => held.then(() => json(failedInstall, 502)) });
  open();
  await ready();

  fireEvent.click(await screen.findByRole('button', { name: 'Install Commodore' }));
  const running = await screen.findByRole('button', { name: /Installing/ });
  expect(running.getAttribute('aria-busy')).toBe('true');
  fireEvent.click(running);
  fireEvent.click(running);
  expect(commodorePosts).toEqual([{ fleet: 'zulu', harness: 'claude', action: 'install' }]);
  expect(timers.mock.calls.some(([, ms]) => ms === 10 * 60_000)).toBe(true);
  expect(posts).toEqual([]);

  finish();
  await waitFor(() => expect(installButton()).toBeTruthy());
  timers.mockRestore();
});

test('after Install succeeds the skills are read again and Commodore is there with no reload: the button goes, the untouched pick becomes Commodore and its row is pickable', async () => {
  let now: SkillsAnswer = missing;
  serve({
    skills: () => json(now),
    commodore: () => {
      now = commodore;
      return json({ state: 'ok' });
    },
  });
  open();
  await ready();
  const before = reads();

  fireEvent.click(await screen.findByRole('button', { name: 'Install Commodore' }));
  await waitFor(() => expect(box().value).toBe('Commodore'));
  expect(reads()).toBeGreaterThan(before);
  expect(installButton()).toBeNull();
  fireEvent.click(box());
  expect(option(/^Commodore/).getAttribute('aria-disabled')).toBeNull();
});

test('a skill the engineer picked before Install stays picked after it', async () => {
  let now: SkillsAnswer = missing;
  serve({
    skills: () => json(now),
    commodore: () => {
      now = commodore;
      return json({ state: 'ok' });
    },
  });
  open();
  await ready();
  type('review');
  fireEvent.click(option(/^\/review/));

  fireEvent.click(await screen.findByRole('button', { name: 'Install Commodore' }));
  await waitFor(() => expect(installButton()).toBeNull());
  expect(box().value).toBe('/review');
});

test('an install that worked but could not turn on auto-update shows the warning as one note and still makes Commodore available', async () => {
  let now: SkillsAnswer = missing;
  serve({
    skills: () => json(now),
    commodore: () => {
      now = commodore;
      return json({ state: 'ok', warning: 'could not turn on auto-update: settings.json is not JSON' });
    },
  });
  open();
  await ready();

  fireEvent.click(await screen.findByRole('button', { name: 'Install Commodore' }));
  expect(await screen.findByText('could not turn on auto-update: settings.json is not JSON')).toBeTruthy();
  expect(box().value).toBe('Commodore');
  expect(screen.queryByRole('alert')).toBeNull();
});

test('a failed install shows the error, the last output as preformatted text and each manual command on its own selectable line, keeps Commodore not found, and can be pressed again', async () => {
  serve({ skills: () => json(missing), commodore: () => json(failedInstall, 502) });
  open();
  await ready();

  fireEvent.click(await screen.findByRole('button', { name: 'Install Commodore' }));
  expect(await screen.findByText('claude plugin install failed')).toBeTruthy();
  const out = screen.getByRole('dialog').querySelector('pre');
  expect(out?.textContent).toBe('line one\nboom: no network');
  for (const c of failedInstall.commands) {
    const line = screen.getByText(c);
    expect(line.children).toHaveLength(0);
    expect(line.closest('button')).toBeNull();
  }
  expect(box().value).not.toBe('Commodore');
  fireEvent.click(box());
  expect(option(/^Commodore/).getAttribute('aria-disabled')).toBe('true');
  fireEvent.keyDown(box(), { key: 'Escape' });

  const again = await screen.findByRole('button', { name: 'Install Commodore' });
  expect((again as HTMLButtonElement).disabled).toBe(false);
  expect(again.getAttribute('aria-busy')).toBeNull();
  fireEvent.click(again);
  await waitFor(() => expect(commodorePosts).toHaveLength(2));
});

test('a refused install (409) and a failure with no output show the server\'s message and leave Commodore as it was', async () => {
  const answered = [json({ error: 'another install for claude is still running' }, 409), json({ error: 'Admiral is shutting down' }, 500)];
  serve({ skills: () => json(missing), commodore: () => answered.shift()! });
  open();
  await ready();

  fireEvent.click(await screen.findByRole('button', { name: 'Install Commodore' }));
  expect(await screen.findByText('another install for claude is still running')).toBeTruthy();
  expect(screen.getByRole('dialog').querySelector('pre')).toBeNull();
  expect(box().value).not.toBe('Commodore');

  fireEvent.click(await screen.findByRole('button', { name: 'Install Commodore' }));
  expect(await screen.findByText('Admiral is shutting down')).toBeTruthy();
  expect(screen.queryByText('another install for claude is still running')).toBeNull();
});

test('an outdated Commodore says which versions, offers Update and no Install, stays pickable, and a Commodore launch still sends Commodore', async () => {
  const posts = serve({ skills: () => json(outdated) });
  open();
  await ready();

  expect(await screen.findByText('Commodore 3.1.0 is older than this Admiral needs (3.5.0)')).toBeTruthy();
  expect(updateButton()).toBeTruthy();
  expect(installButton()).toBeNull();
  expect(box().value).toBe('Commodore');
  fireEvent.click(box());
  expect(option(/^Commodore/).getAttribute('aria-disabled')).toBeNull();
  fireEvent.keyDown(box(), { key: 'Escape' });
  fireEvent.click(launch());
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({ skill: { kind: 'commodore' } });
  expect(commodorePosts).toEqual([]);
});

test('Update posts action update, runs, and on success the read shows Commodore ok with the line gone', async () => {
  let now: SkillsAnswer = outdated;
  let finish!: () => void;
  const held = new Promise<void>((r) => (finish = r));
  serve({
    skills: () => json(now),
    commodore: async () => {
      await held;
      now = commodore;
      return json({ state: 'ok' });
    },
  });
  open();
  await ready();

  fireEvent.click(await screen.findByRole('button', { name: /^Update/ }));
  const running = await screen.findByRole('button', { name: /Updating/ });
  expect(running.getAttribute('aria-busy')).toBe('true');
  fireEvent.click(running);
  finish();
  // Commodore reads ok only after the skills are read again, so wait on the line going and the button returning to idle.
  await waitFor(() => expect(dialogText()).not.toContain('older than this Admiral needs'));
  expect(screen.queryByRole('button', { name: /Updat/ })).toBeNull();
  expect(commodorePosts).toEqual([{ fleet: 'zulu', harness: 'claude', action: 'update' }]);
  expect(dialogText()).not.toContain('older than this Admiral needs');
});

test('a failed Update shows the output and the manual command, keeps the outdated line, and Commodore stays pickable', async () => {
  serve({ skills: () => json(outdated), commodore: () => json({ error: 'claude plugin update failed', output: 'update boom', commands: ['claude plugin update jasonbuffin@jbuffinskills'], state: { state: 'outdated', installed: '3.1.0', minimum: '3.5.0' } }, 502) });
  open();
  await ready();

  fireEvent.click(await screen.findByRole('button', { name: /^Update/ }));
  expect(await screen.findByText('claude plugin update failed')).toBeTruthy();
  expect(screen.getByRole('dialog').querySelector('pre')?.textContent).toBe('update boom');
  expect(screen.getByText('claude plugin update jasonbuffin@jbuffinskills')).toBeTruthy();
  expect(dialogText()).toContain('Commodore 3.1.0 is older than this Admiral needs (3.5.0)');
  expect(box().value).toBe('Commodore');
  expect(updateButton()).toBeTruthy();
});

test('a manifest\'s version strings show as text, never as HTML', async () => {
  serve({ skills: () => json(withState('claude', { found: true, name: 'commodore', state: 'outdated', installed: '<b id="v">3.1.0</b>', minimum: '<i>3.5.0</i>' })) });
  open();
  await ready();

  expect(await screen.findByText('Commodore <b id="v">3.1.0</b> is older than this Admiral needs (<i>3.5.0</i>)')).toBeTruthy();
  expect(screen.getByRole('dialog').querySelector('#v, b, i')).toBeNull();
});

test('loose Commodore skills say the version is unknown and how to switch, offer no Update or Install and no warning, and stay pickable', async () => {
  serve({ skills: () => json(withState('claude', { found: true, name: 'commodore', state: 'unknown-version' })) });
  open();
  await ready();

  expect(await screen.findByText('Commodore version unknown (installed as loose skills)')).toBeTruthy();
  expect(dialogText()).toMatch(/remove the loose skills.*Install/i);
  expect(updateButton()).toBeNull();
  expect(installButton()).toBeNull();
  expect(dialogText()).not.toMatch(/older than/);
  expect(box().value).toBe('Commodore');
  expect(launch().disabled).toBe(false);
});

test('an install still running when the engineer changes dock never paints its failure onto the other dock, and the other dock\'s own Install stays pressable', async () => {
  let finish!: () => void;
  const held = new Promise<void>((r) => (finish = r));
  serve({ skills: (h) => json(h === 'claude' ? missing : answers.codex), commodore: () => held.then(() => json(failedInstall, 502)) });
  open();
  await ready();
  fireEvent.click(await screen.findByRole('button', { name: 'Install Commodore' }));
  await screen.findByRole('button', { name: /Installing/ });

  choose(/codex/i);
  await waitFor(() => expect(box().value).toMatch(/^(Commodore|Just launch)$/));
  finish();
  // The failure shows once the dock it was for is back, which proves the held response has been handled.
  choose(/claude/i);
  expect(await screen.findByText('claude plugin install failed')).toBeTruthy();
  choose(/codex/i);
  await waitFor(() => expect(box().value).toMatch(/^Just launch$/));
  expect(screen.queryByText('claude plugin install failed')).toBeNull();
  expect(screen.getByRole('dialog').querySelector('pre')).toBeNull();
  const here = await screen.findByRole('button', { name: 'Install Commodore' });
  expect(here.getAttribute('aria-busy')).toBeNull();
});

test('an install that succeeds after the engineer changed dock leaves the other dock\'s Commodore as that dock\'s read says', async () => {
  let finish!: () => void;
  const held = new Promise<void>((r) => (finish = r));
  let claude: SkillsAnswer = missing;
  serve({
    skills: (h) => json(h === 'claude' ? claude : answers.codex),
    commodore: async () => {
      await held;
      claude = commodore;
      return json({ state: 'ok', warning: 'could not turn on auto-update' });
    },
  });
  open();
  await ready();
  fireEvent.click(await screen.findByRole('button', { name: 'Install Commodore' }));
  await screen.findByRole('button', { name: /Installing/ });

  choose(/codex/i);
  await waitFor(() => expect(box().value).toMatch(/^Just launch$/));
  finish();
  // The warning shows once the dock it was for is back, which proves the held response has been handled.
  choose(/claude/i);
  expect(await screen.findByText('could not turn on auto-update')).toBeTruthy();
  choose(/codex/i);
  await waitFor(() => expect(box().value).toBe('Just launch'));
  expect(dialogText()).not.toContain('could not turn on auto-update');
  expect(await screen.findByRole('button', { name: 'Install Commodore' })).toBeTruthy();
});

test('a run on one dock keeps its busy state and its failure when the engineer goes to another dock, runs there, and comes back', async () => {
  let finish!: () => void;
  const held = new Promise<void>((r) => (finish = r));
  serve({
    skills: (h) => json(h === 'claude' ? missing : answers.codex),
    commodore: (body) => (body.harness === 'claude' ? held.then(() => json(failedInstall, 502)) : json({ error: 'another install is running' }, 409)),
  });
  open();
  await ready();
  fireEvent.click(await screen.findByRole('button', { name: 'Install Commodore' }));
  await screen.findByRole('button', { name: /Installing/ });

  choose(/codex/i);
  fireEvent.click(await screen.findByRole('button', { name: 'Install Commodore' }));
  expect(await screen.findByText('another install is running')).toBeTruthy();
  choose(/claude/i);
  expect((await screen.findByRole('button', { name: /Installing/ })).getAttribute('aria-busy')).toBe('true');
  finish();
  expect(await screen.findByText('claude plugin install failed')).toBeTruthy();
  expect(screen.getByRole('dialog').querySelector('pre')).toBeTruthy();
  expect(screen.queryByText('another install is running')).toBeNull();
});

const withSkills = (...names: string[]): SkillsAnswer => ({ ...answers.claude!, skills: names.map((n) => installed(n)) });

test('a campaign child on the bare wayfinder skill fills the dock\'s plugin skill, and the launch carries that qualified name', async () => {
  const posts = serve({ skills: () => json(withSkills('review', 'mattpocock-skills:wayfinder')) });
  openChild();
  await ready();

  await waitFor(() => expect(box().value).toBe('/mattpocock-skills:wayfinder'));
  expect(dialogText()).not.toContain('not installed on');
  fireEvent.click(launch());
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({ child_url: child.url, skill: { kind: 'skill', name: 'mattpocock-skills:wayfinder' } });
});

test('a campaign child on wayfinder keeps the bare name when the dock lists it with no prefix', async () => {
  const posts = serve({ skills: () => json(withSkills('review', 'wayfinder')) });
  openChild();
  await ready();

  await waitFor(() => expect(box().value).toBe('/wayfinder'));
  fireEvent.click(launch());
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({ skill: { kind: 'skill', name: 'wayfinder' } });
});

test('a campaign child on wayfinder keeps the bare name, and the warning, when two plugins list a wayfinder', async () => {
  serve({ skills: () => json(withSkills('mattpocock-skills:wayfinder', 'other-plugin:wayfinder')) });
  openChild();
  await ready();

  await waitFor(() => expect(dialogText()).toContain('not installed on claude'));
  expect(box().value).toBe('/wayfinder');
});
