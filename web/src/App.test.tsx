import { fireEvent, render, screen } from '@testing-library/preact';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const hoisted = vi.hoisted(() => ({ handlers: new Map<string, (msg: unknown) => void>() }));
vi.mock('./connection', async (original) => ({
  ...(await original<typeof import('./connection')>()),
  connect: () => () => {},
  onMessage: (type: string, fn: (msg: unknown) => void) => {
    hoisted.handlers.set(type, fn);
    return () => {};
  },
}));
vi.mock('./chime', async (original) => ({ ...(await original<typeof import('./chime')>()), chime: vi.fn() }));

import { App } from './App';
import { chime } from './chime';
import { applyConfig, config, type Fleet } from './config';
import { deckLabel } from './crewSea';
import { phone } from './sizeClass';
import { boarded, ships, type WireCrew, type WireShip } from './ships';

const fleet = (key: string, name: string): Fleet => ({
  key,
  name,
  color: '#336699',
  ink: '#ffffff',
  search_roots: ['/work'],
  can_launch: true,
  docks: [
    { harness: 'claude', executable: 'claude', executable_found: true, warnings: [] },
    { harness: 'codex', executable: 'codex', executable_found: true, warnings: [] },
  ],
});

const crewBoat = (name: string, status: WireCrew['status'] = 'underway'): WireCrew => ({ name, harness: 'claude', dir: '/work', status, launched: '2026-01-01T00:00:00Z' });

const ship = (name: string, over: Partial<WireShip> = {}): WireShip => ({
  session: `s-${name}`,
  name,
  fleet: 'zulu',
  fleet_name: 'Zulu',
  fleet_color: '#336699',
  harness: 'claude',
  dir: '/work/widgets',
  repo: 'acme/widgets',
  skill: null,
  source: '',
  title: '',
  url: '',
  order_id: '',
  launched: '2026-01-01T00:00:00Z',
  status: 'underway',
  exit_code: null,
  exit_signal: null,
  position: { x: 0.5, y: 0.3 },
  crew: [],
  subagents: [],
  crew_live: 0,
  ...over,
});

const subagents = [
  { id: 'a1', type: 'Explore', start: '2026-01-01T00:00:00Z' },
  { id: 'a2', type: 'general-purpose', start: '2026-01-01T00:00:00Z' },
];

const fetchSpy = vi.fn(async () => new Response('{}'));

beforeEach(() => {
  vi.stubGlobal('fetch', fetchSpy);
  phone.value = false;
  boarded.value = null;
  ships.value = [];
  applyConfig({ fleets: [fleet('zulu', 'Zulu')], error: null });
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  phone.value = false;
  ships.value = [];
  config.value = null;
});

const seaWithCrew = () => ship('Saltpaw', { crew: [crewBoat('Scout'), crewBoat('Mason')], subagents });
const classes = (root: ParentNode) => [...root.querySelectorAll('*')].map((el) => el.getAttribute('class') ?? '').join('|');

describe('the classic view has no frame', () => {
  it('shows none of the header, orders, inbox, chat or launch controls', () => {
    ships.value = [seaWithCrew()];
    const { container } = render(<App />);
    expect(screen.queryByRole('banner')).toBeNull();
    expect(screen.queryByRole('navigation')).toBeNull();
    expect(screen.queryByRole('complementary')).toBeNull();
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(screen.queryByText(/Orders/)).toBeNull();
    expect(screen.queryByText(/Reconnecting/)).toBeNull();
    expect(screen.queryByText(/Admiral/i)).toBeNull();
    expect(container.querySelector('.hint')).toBeNull();
    expect(screen.queryByText(/drag ships to arrange/)).toBeNull();
  });
});

describe('nothing in the classic view is interactive', () => {
  it('has no button, link, text box or focusable element on the wide sea', () => {
    ships.value = [seaWithCrew(), ship('Marlin', { crew: [1, 2, 3, 4, 5].map((n) => crewBoat(`Boat${n}`)) })];
    const { container } = render(<App />);
    for (const role of ['button', 'link', 'textbox', 'tab', 'menuitem']) expect(screen.queryAllByRole(role)).toEqual([]);
    expect(container.querySelectorAll('button, a[href], input, textarea, select, [tabindex], [contenteditable]')).toHaveLength(0);
  });

  it('has none at the phone size either', () => {
    phone.value = true;
    ships.value = [seaWithCrew()];
    const { container } = render(<App />);
    for (const role of ['button', 'link', 'textbox', 'tab']) expect(screen.queryAllByRole(role)).toEqual([]);
    expect(container.querySelectorAll('button, a[href], input, textarea, select, [tabindex], [contenteditable]')).toHaveLength(0);
  });

  it('changes nothing when a ship, crew boat, deck or dock is clicked, pressed or dragged', () => {
    ships.value = [seaWithCrew()];
    const { container } = render(<App />);
    const before = container.innerHTML;
    const targets = [
      screen.getByRole('img', { name: /^Saltpaw/ }),
      screen.getByRole('img', { name: /Scout/ }),
      screen.getByRole('img', { name: deckLabel('Saltpaw', subagents) }),
      screen.getByRole('img', { name: /Claude dock, Zulu/ }),
    ];
    for (const el of targets) {
      fireEvent.pointerDown(el, { button: 0, isPrimary: true, pointerId: 1, clientX: 10, clientY: 10 });
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 90, clientY: 90 });
      fireEvent.pointerUp(window, { pointerId: 1, clientX: 90, clientY: 90 });
      fireEvent.click(el);
      fireEvent.keyDown(el, { key: 'Enter' });
    }
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(container.innerHTML).toBe(before);
    expect(boarded.value).toBeNull();
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});

describe('the classic view still shows what is happening', () => {
  it('shows the sea, the marina with its docks, each ship, its crew boats and its deck crew', () => {
    ships.value = [seaWithCrew(), ship('Marlin', { status: 'anchored', exit_code: 1 })];
    const { container } = render(<App />);
    expect(container.querySelector('.sea')).not.toBeNull();
    expect(screen.getByRole('region', { name: 'Zulu marina' })).toBeTruthy();
    expect(screen.getByRole('img', { name: /Claude dock, Zulu/ })).toBeTruthy();
    expect(screen.getByRole('img', { name: /Codex dock, Zulu/ })).toBeTruthy();
    const saltpaw = screen.getByRole('img', { name: /^Saltpaw/ });
    expect(saltpaw.getAttribute('aria-label')).toMatch(/underway/);
    expect(screen.getByRole('img', { name: /^Marlin/ }).getAttribute('aria-label')).toMatch(/anchored/);
    expect(screen.getByRole('img', { name: /Scout/ })).toBeTruthy();
    expect(screen.getByRole('img', { name: /Mason/ })).toBeTruthy();
    expect(screen.getByRole('img', { name: deckLabel('Saltpaw', subagents) })).toBeTruthy();
  });

  it('says "+N crew" for crew boats beyond the raft, without telling anyone to board', () => {
    ships.value = [ship('Marlin', { crew: [1, 2, 3, 4, 5].map((n) => crewBoat(`Boat${n}`)) })];
    render(<App />);
    expect(screen.getByRole('img', { name: /2 more crew boats/ }).getAttribute('aria-label')).not.toMatch(/board/i);
    expect(screen.getByText('+2 crew')).toBeTruthy();
  });

  it('draws a ship the server pushes over the live feed', async () => {
    render(<App />);
    hoisted.handlers.get('ships')!({ ships: [ship('Saltpaw')] });
    expect(await screen.findByRole('img', { name: /^Saltpaw/ })).toBeTruthy();
  });

  it('tells a fleetless person to run admiral setup, inertly', () => {
    applyConfig({ fleets: [], error: null });
    render(<App />);
    expect(screen.getByText(/admiral setup/)).toBeTruthy();
    expect(screen.queryByRole('button')).toBeNull();
  });
});

describe('a ship that needs you looks like one that is working', () => {
  const needing = () => ship('Saltpaw', { status: 'needs_you', reason: 'permission', crew: [crewBoat('Scout', 'needs_you'), crewBoat('Mason')] });
  const working = () => ship('Saltpaw', { crew: [crewBoat('Scout'), crewBoat('Mason')] });

  it('has no shake, frantic flag, bang, asking plaque or "needs you" wording', () => {
    ships.value = [needing()];
    const { container } = render(<App />);
    expect(container.querySelector('.ship-needs-you, .ship-bang, .crew-bang, .asking, .frantic, .ping')).toBeNull();
    expect(container.textContent).not.toMatch(/needs you|!/i);
    for (const img of screen.getAllByRole('img')) expect(img.getAttribute('aria-label') ?? '').not.toMatch(/needs you/i);
  });

  it('has the same classes and labels as the same ship working', () => {
    ships.value = [working()];
    const first = render(<App />);
    const workingClasses = classes(first.container);
    const workingLabels = screen.getAllByRole('img').map((e) => e.getAttribute('aria-label'));
    first.unmount();
    ships.value = [needing()];
    const second = render(<App />);
    expect(classes(second.container)).toBe(workingClasses);
    expect(screen.getAllByRole('img').map((e) => e.getAttribute('aria-label'))).toEqual(workingLabels);
  });

  it('shows "+N crew" without a bang when a hidden crew boat needs you', () => {
    const crew = [1, 2, 3].map((n) => crewBoat(`Boat${n}`));
    ships.value = [ship('Marlin', { crew: [...crew, crewBoat('Boat4', 'needs_you')] })];
    render(<App />);
    expect(screen.getByText('+1 crew')).toBeTruthy();
  });

  it('does not chime when a ship turns needs you', () => {
    render(<App />);
    const push = hoisted.handlers.get('ships')!;
    push({ ships: [ship('Saltpaw', { crew: [crewBoat('Scout')] })] });
    push({ ships: [ship('Saltpaw', { status: 'needs_you', crew: [crewBoat('Scout')] })] });
    expect(chime).not.toHaveBeenCalled();
  });

  it('does not chime when a crew boat turns needs you', () => {
    render(<App />);
    const push = hoisted.handlers.get('ships')!;
    push({ ships: [ship('Saltpaw', { crew: [crewBoat('Scout')] })] });
    push({ ships: [ship('Saltpaw', { crew: [crewBoat('Scout', 'needs_you')] })] });
    expect(chime).not.toHaveBeenCalled();
  });
});

describe('the classic view on a phone', () => {
  it('shows the sea, ships, crew boats and docks, with no tabs or list', () => {
    phone.value = true;
    ships.value = [seaWithCrew()];
    const { container } = render(<App />);
    expect(container.querySelector('.sea')).not.toBeNull();
    expect(screen.getByRole('img', { name: /^Saltpaw/ })).toBeTruthy();
    expect(screen.getByRole('img', { name: /Scout/ })).toBeTruthy();
    expect(screen.getByRole('img', { name: /Claude dock, Zulu/ })).toBeTruthy();
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(screen.queryByRole('tab')).toBeNull();
    expect(screen.queryByRole('banner')).toBeNull();
  });
});
