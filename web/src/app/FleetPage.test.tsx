import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Fleet } from './api';
import { FleetPage } from './FleetPage';
import { fleets } from './fleets';

const base = {
  key: 'zulu',
  name: 'Westwind',
  search_roots: ['~/proj'],
  owners: [] as string[],
  wake_reviewers: [] as string[],
  clone_root: '',
  color: '#336699',
  ink: '#fff',
  notify: true,
  crew_sharing: { enabled: false, share_repo: '', sources: [] },
  docks: [
    { harness: 'claude', executable: '', executable_found: true, warnings: [] },
    { harness: 'codex', executable: '/bin/codex', executable_found: false, warnings: ['codex is old'] },
  ],
} as unknown as Fleet;

let stored: Fleet;
let puts: Record<string, unknown>[];
let refuse: string | null;
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

beforeEach(() => {
  stored = { ...base };
  puts = [];
  refuse = null;
  fleets.value = [stored];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      if (init?.method === 'PUT' && path.endsWith('/fleets/zulu')) {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>;
        puts.push(body);
        if (refuse) return json({ error: refuse }, refuse.startsWith('conflict') ? 409 : 400);
        stored = { ...stored, ...body } as Fleet;
        if (body.color === '') stored = { ...stored, color: '#8899aa' };
        return json({ fleets: [stored] });
      }
      return json({ fleets: [stored] });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

const save = () => screen.getByRole('button', { name: 'Save' });
const settled = (b: HTMLElement) => waitFor(() => expect(b.getAttribute('aria-busy')).not.toBe('true'));

test('adding an owner and a clone root sends only those two fields and keeps the stored search root', async () => {
  const view = render(<FleetPage fleet={stored} />);
  expect(save().hasAttribute('disabled')).toBe(true);
  fireEvent.click(within(screen.getByRole('group', { name: 'Owners' })).getByRole('button', { name: 'Add owner' }));
  fireEvent.input(screen.getByRole('textbox', { name: 'Owner 1' }), { target: { value: 'acme' } });
  fireEvent.input(screen.getByRole('textbox', { name: 'Clone root' }), { target: { value: '/srv/clones' } });
  fireEvent.click(save());
  await waitFor(() => expect(puts).toEqual([{ owners: ['acme'], clone_root: '/srv/clones' }]));
  view.rerender(<FleetPage fleet={stored} />);
  await settled(save());
  expect((screen.getByRole('textbox', { name: 'Owner 1' }) as HTMLInputElement).value).toBe('acme');
  expect((screen.getByRole('textbox', { name: 'Search root 1' }) as HTMLInputElement).value).toBe('~/proj');
});

test('a reviewer list saves alone, and changing a root list sends the whole list as shown', async () => {
  render(<FleetPage fleet={stored} />);
  fireEvent.click(screen.getByRole('button', { name: 'Add reviewer' }));
  fireEvent.input(screen.getByRole('textbox', { name: 'Reviewer 1' }), { target: { value: 'Alice-Dev' } });
  fireEvent.click(save());
  await waitFor(() => expect(puts).toEqual([{ wake_reviewers: ['Alice-Dev'] }]));
  await settled(save());

  fireEvent.click(screen.getByRole('button', { name: 'Add search root' }));
  fireEvent.input(screen.getByRole('textbox', { name: 'Search root 2' }), { target: { value: '/second' } });
  fireEvent.click(save());
  await waitFor(() => expect(puts[1]).toEqual({ search_roots: ['~/proj', '/second'] }));
  await settled(save());
});

test('Notify saves at once, sends only itself and shows the stored state', async () => {
  const view = render(<FleetPage fleet={stored} />);
  const notify = screen.getByRole('switch', { name: 'Notify' });
  expect(notify.getAttribute('aria-checked')).toBe('true');
  fireEvent.click(notify);
  await waitFor(() => expect(puts).toEqual([{ notify: false }]));
  view.rerender(<FleetPage fleet={stored} />);
  await waitFor(() => expect(screen.getByRole('switch', { name: 'Notify' }).getAttribute('aria-checked')).toBe('false'));
});

test('a colour-only save sends only the colour, and Use default sends an empty one', async () => {
  const view = render(<FleetPage fleet={stored} />);
  fireEvent.input(screen.getByLabelText('Color'), { target: { value: '#1a2b3c' } });
  expect(screen.getByRole('img', { name: 'Fleet colour' })).toBeTruthy();
  fireEvent.click(save());
  await waitFor(() => expect(puts).toEqual([{ color: '#1a2b3c' }]));
  view.rerender(<FleetPage fleet={stored} />);
  await settled(save());

  fireEvent.click(screen.getByRole('button', { name: 'Use default' }));
  expect(screen.getByRole('img', { name: 'Default colour' })).toBeTruthy();
  fireEvent.click(save());
  await waitFor(() => expect(puts[1]).toEqual({ color: '' }));
  await settled(save());
});

test('a clone root the server refuses shows its message at the field', async () => {
  render(<FleetPage fleet={stored} />);
  refuse = 'fleets.zulu.clone_root: must be an absolute path or start with ~/';
  fireEvent.input(screen.getByRole('textbox', { name: 'Clone root' }), { target: { value: 'relative/x' } });
  fireEvent.click(save());
  await waitFor(() => expect(screen.getByRole('textbox', { name: 'Clone root' }).getAttribute('aria-describedby')).toBe('clone-root-err'));
  expect(document.getElementById('clone-root-err')!.textContent).toMatch(/must be an absolute path or start with ~\//);
  await settled(save());
});

test('removing every search root says the fleet then cannot launch, before anything is saved', () => {
  render(<FleetPage fleet={stored} />);
  expect(screen.queryByText(/can’t launch/)).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Remove search root 1' }));
  expect(screen.getAllByRole('status').some((s) => /launch/i.test(s.textContent ?? ''))).toBe(true);
  expect(puts).toEqual([]);
});

test('docks are one read-only row per harness with the way to change them', () => {
  render(<FleetPage fleet={stored} />);
  const docks = screen.getByRole('list', { name: 'Docks' });
  expect(within(docks).getAllByRole('listitem')).toHaveLength(2);
  expect(docks.textContent).toContain('claude');
  expect(docks.textContent).toContain('codex is old');
  expect(within(docks).queryAllByRole('textbox')).toHaveLength(0);
  expect(document.body.textContent).toContain('admiral setup');
  expect(document.body.textContent).not.toContain('CLAUDE_CONFIG_DIR');
});

test('Crew sharing shows the stored state and sends only enabled, so the share repo and sources stay', async () => {
  stored = { ...stored, crew_sharing: { enabled: false, share_repo: 'octocat/crew', sources: ['hubot/crew'] } };
  fleets.value = [stored];
  const view = render(<FleetPage fleet={stored} />);
  const sharing = screen.getByRole('switch', { name: 'Crew sharing' });
  expect(sharing.getAttribute('aria-checked')).toBe('false');
  fireEvent.click(sharing);
  await waitFor(() => expect(puts).toEqual([{ crew_sharing: { enabled: true } }]));
  view.rerender(<FleetPage fleet={fleets.value[0]} />);
  await waitFor(() => expect(screen.getByRole('switch', { name: 'Crew sharing' }).getAttribute('aria-checked')).toBe('true'));
  expect(screen.getByText("Lets you publish this fleet's crew members to a GitHub repo you own, public or private.")).toBeTruthy();
});

test('a refused Crew sharing save shows the error beside the switch and leaves it off', async () => {
  refuse = 'crew_sharing is invalid';
  render(<FleetPage fleet={stored} />);
  fireEvent.click(screen.getByRole('switch', { name: 'Crew sharing' }));
  expect((await screen.findByRole('alert')).textContent).toContain('crew_sharing is invalid');
  expect(screen.getByRole('switch', { name: 'Crew sharing' }).getAttribute('aria-checked')).toBe('false');
});

test('Reload on a conflicting Crew sharing save clears its error', async () => {
  refuse = 'conflict: the file changed';
  render(<FleetPage fleet={stored} />);
  fireEvent.click(screen.getByRole('switch', { name: 'Crew sharing' }));
  const alert = await screen.findByRole('alert');
  expect(alert.textContent).toContain('nothing was saved');
  refuse = null;
  fireEvent.click(within(alert).getByRole('button', { name: 'Reload' }));
  await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
});

test('a Share repo field shows with Crew sharing on, and a typo shows an inline error and saves nothing', async () => {
  stored = { ...stored, crew_sharing: { enabled: true, share_repo: 'octocat/crew', sources: [] } };
  render(<FleetPage fleet={stored} />);
  const field = screen.getByRole('textbox', { name: 'Share repo' }) as HTMLInputElement;
  expect(field.value).toBe('octocat/crew');
  fireEvent.input(field, { target: { value: 'not a repo' } });
  fireEvent.click(save());
  expect((await screen.findByRole('alert')).textContent).toMatch(/owner\/repo/i);
  expect(puts).toEqual([]);
});

test('a valid Share repo saves as crew_sharing.share_repo alone, and the field is hidden with Crew sharing off', async () => {
  stored = { ...stored, crew_sharing: { enabled: true, share_repo: '', sources: ['hubot/crew'] } };
  const view = render(<FleetPage fleet={stored} />);
  fireEvent.input(screen.getByRole('textbox', { name: 'Share repo' }), { target: { value: 'octocat/crew' } });
  fireEvent.click(save());
  await waitFor(() => expect(puts).toEqual([{ crew_sharing: { share_repo: 'octocat/crew' } }]));
  await settled(save());
  view.unmount();
  render(<FleetPage fleet={{ ...base } as Fleet} />);
  expect(screen.queryByRole('textbox', { name: 'Share repo' })).toBeNull();
});

test('the Share repo field says it takes a GitHub repo as owner/repo and that sharing a member can create it', () => {
  stored = { ...base, crew_sharing: { enabled: true, share_repo: '', sources: [] } } as Fleet;
  fleets.value = [stored];
  render(<FleetPage fleet={stored} />);
  expect(screen.getByText('The GitHub repo, as owner/repo, that this fleet\'s crew is published to. Sharing a member can create it.')).toBeTruthy();
  expect(screen.queryByText(/public GitHub repo/)).toBeNull();
});
