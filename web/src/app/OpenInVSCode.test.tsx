import { render, screen, waitFor } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Ship } from './api';
import { OpenInVSCode, OpenInVSCodeNote } from './OpenInVSCode';

afterEach(() => vi.unstubAllGlobals());

const REMOTE = 'Not available for remote users yet.';
const MISSING = "VS Code isn't installed on this Mac.";

const ship = (over: Partial<Ship> = {}) => ({ session: 'vscode-ship', name: 'Zulu', dir: '/Users/me/my repo', ...over }) as Ship;

function stub(answer: () => Response | Promise<Response>) {
  const paths: string[] = [];
  vi.stubGlobal('fetch', vi.fn(async (path: string) => {
    paths.push(path);
    return answer();
  }));
  return paths;
}
const editors = (local: boolean, installed: boolean) => () => new Response(JSON.stringify({ local, vscode: { installed } }), { status: 200 });

const view = (s: Ship) =>
  render(
    <>
      <OpenInVSCode ship={s} />
      <OpenInVSCodeNote ship={s} />
    </>,
  );
const control = () => screen.getByText('Open in VS Code').closest('a,button') as HTMLElement;

test('on this Mac with VS Code installed it is a link to the ship folder with a trailing slash and the spaces encoded', async () => {
  const paths = stub(editors(true, true));
  view(ship());
  await waitFor(() => expect(control().tagName).toBe('A'));
  expect(control().getAttribute('href')).toBe('vscode://file/Users/me/my%20repo/');
  expect(control().getAttribute('target')).toBeNull();
  expect(paths).toContain('/api/v1/editors');
  expect(screen.queryByText(REMOTE)).toBeNull();
  expect(screen.queryByText(MISSING)).toBeNull();
});

test('a ship with a worktree opens the worktree and not its checkout', async () => {
  stub(editors(true, true));
  view(ship({ worktree: '/Users/me/.admiral/worktrees/vscode-ship' }));
  await waitFor(() => expect(control().getAttribute('href')).toBe('vscode://file/Users/me/.admiral/worktrees/vscode-ship/'));
});

test('a # in the path is encoded so it survives the link', async () => {
  stub(editors(true, true));
  view(ship({ dir: '/work/c#/app' }));
  await waitFor(() => expect(control().getAttribute('href')).toBe('vscode://file/work/c%23/app/'));
});

test('a remote request gets a disabled control and the remote note, with no link', async () => {
  stub(editors(false, true));
  view(ship());
  await screen.findByText(REMOTE);
  expect(control().tagName).toBe('BUTTON');
  expect((control() as HTMLButtonElement).disabled).toBe(true);
  expect(control().getAttribute('href')).toBeNull();
});

test('a remote request wins over a missing VS Code', async () => {
  stub(editors(false, false));
  view(ship());
  await screen.findByText(REMOTE);
  expect(screen.queryByText(MISSING)).toBeNull();
});

test('on this Mac without VS Code it is disabled with the install note and no link', async () => {
  stub(editors(true, false));
  view(ship());
  await screen.findByText(MISSING);
  expect((control() as HTMLButtonElement).disabled).toBe(true);
  expect(control().getAttribute('href')).toBeNull();
  expect(screen.queryByText(REMOTE)).toBeNull();
});

test('while the answer is loading it is disabled with no note', async () => {
  stub(() => new Promise<Response>(() => undefined));
  view(ship());
  await waitFor(() => expect(screen.getByText('Open in VS Code')).toBeTruthy());
  expect((control() as HTMLButtonElement).disabled).toBe(true);
  expect(control().getAttribute('href')).toBeNull();
  expect(screen.queryByText(REMOTE)).toBeNull();
  expect(screen.queryByText(MISSING)).toBeNull();
});

test('when the request fails it stays disabled with no note and no link', async () => {
  const paths = stub(() => new Response('{"error":"boom"}', { status: 500 }));
  view(ship());
  await waitFor(() => expect(paths).toContain('/api/v1/editors'));
  await waitFor(() => expect(screen.getByText('Open in VS Code')).toBeTruthy());
  expect((control() as HTMLButtonElement).disabled).toBe(true);
  expect(control().getAttribute('href')).toBeNull();
  expect(screen.queryByText(REMOTE)).toBeNull();
  expect(screen.queryByText(MISSING)).toBeNull();
});

test('a ship whose path is not absolute or is missing gets no enabled link, beside one that does', async () => {
  stub(editors(true, true));
  render(
    <>
      <div data-testid="good"><OpenInVSCode ship={ship()} /></div>
      <div data-testid="relative"><OpenInVSCode ship={ship({ dir: 'relative/repo' })} /></div>
      <div data-testid="none"><OpenInVSCode ship={ship({ dir: '' })} /></div>
    </>,
  );
  await waitFor(() => expect(screen.getByTestId('good').querySelector('a')?.getAttribute('href')).toBe('vscode://file/Users/me/my%20repo/'));
  expect(screen.getByTestId('relative').querySelector('a[href]')).toBeNull();
  expect(screen.getByTestId('none').querySelector('a[href]')).toBeNull();
});
