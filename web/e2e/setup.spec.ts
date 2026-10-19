import type { Locator, Page } from '@playwright/test';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, symlinkSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { screenText } from './bridge';
import { expect, test } from './fixtures';
import { isTouch, spyOnPage } from './sea';

// The setup terminal (u12 S5): a bridge-style terminal on Admiral's tmux socket
// running `admiral setup`, in the tmux session `_setup`. The names below are
// the contract the UI and `admiral setup` are built to.
//
//   no fleets: the "Set up a fleet" marina is a button named "Set up a fleet"
//   the header's fleet count is a button named "1 fleet" / "2 fleets" (in the banner)
//   either one opens the setup terminal: a region "Setup terminal" holding a `.xterm` element; typing
//     into it goes straight to the terminal; window.__admiral.screen() returns its text (see bridge.ts)
//   a tap while setup runs joins it: there is never more than one `_setup` session
//   the region goes away when the `_setup` session ends, whether setup wrote config.json or not, and the page
//     then re-reads the config; it does not go away because config.json changed
//
// The interactive questions of `admiral setup` in the order it asks them, each
// a line holding the label below (a default in [brackets] is taken with Enter).
// The detected harnesses are those on PATH (the fakes), the identity directory
// of claude defaults to ~/.claude, a suggested search root is listed and none
// is picked for you:
//
//   Fleet key                       crew
//   Fleet name                      Crew
//   Color                           (Enter: the next palette color)
//   Harnesses                       claude          (comma-separated; default: all detected)
//   Executable for claude           (Enter: the detected absolute path)
//   Identity directory for claude   ~/.claude-crew
//   Extra variables                 (Enter: none; NAME=value lines, blank to finish)
//   Search roots                    <HOME>/code     (comma-separated paths)
//   Add another fleet               (Enter: no)
//   Write                           y               ("Write config.json? [y/N]", after the diff)
//
// Re-running with fleets in the file starts again at "Fleet key"; Ctrl-C there
// ends setup without writing.

const fakeharness = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'fakeharness');

const terminal = (page: Page) => page.getByRole('region', { name: 'Setup terminal' });
const countButton = (page: Page, text: string) => page.getByRole('banner').getByRole('button', { name: text, exact: true });
const press = (l: Locator, touch: boolean) => (touch ? l.tap() : l.click());

/** Resolves once the terminal shows the text. */
const shows = (page: Page, text: string) => expect.poll(() => screenText(page), { timeout: 15_000, message: `the setup terminal never showed "${text}"` }).toContain(text);

/** Types a line into the focused terminal. */
async function answer(page: Page, text: string) {
  if (text) await page.keyboard.type(text);
  await page.keyboard.press('Enter');
}

const sessions = (home: string) => {
  const r = spawnSync('tmux', ['-S', join(home, 'tmux.sock'), 'list-sessions', '-F', '#{session_name}'], { encoding: 'utf8' });
  return r.stdout.split('\n').filter(Boolean);
};

test.describe('S5: the setup terminal', () => {
  test('no fleets: set up one in the browser; open it again and leave without writing', async ({ page, admiral }, info) => {
    test.setTimeout(90_000); // about twenty terminal round trips, two page loads and two closes
    const touch = isTouch(info);
    const user = join(admiral.home, 'u');
    const code = join(user, 'code');
    mkdirSync(join(code, 'alpha'), { recursive: true });
    execFileSync('git', ['init', '-q'], { cwd: join(code, 'alpha'), env: { PATH: process.env.PATH ?? '', HOME: user, GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null' } });
    // The setup in Admiral's tmux inherits the server's environment: a temp HOME and the fakes on PATH.
    // PATH is the fakes, tmux and the system directories, so no wrapper of the developer's own can change the defaults.
    const tools = join(admiral.home, 'tools');
    mkdirSync(tools);
    symlinkSync(execFileSync('which', ['tmux'], { encoding: 'utf8' }).trim(), join(tools, 'tmux'));
    await admiral.start({ env: { HOME: user, PATH: `${fakeharness}:${tools}:/usr/bin:/bin:/usr/sbin:/sbin` } });
    await spyOnPage(page);
    // The session ending, however it ends, is a normal close and logs nothing.
    const problems: string[] = [];
    page.on('console', (m) => m.type() === 'error' && problems.push(m.text()));
    page.on('pageerror', (e) => problems.push(e.message));
    await page.goto(admiral.url);

    // ---- no fleets: tap the marina, answer, and the marina sails in ----
    await press(page.getByRole('button', { name: 'Set up a fleet' }), touch);
    await expect(terminal(page)).toBeVisible();
    await expect(terminal(page).locator('.xterm')).toBeVisible();
    // Like the bridge: no prompt, keys on touch screens only, the terminal and the bar on the screen, and the
    // page behind doesn't scroll.
    await expect(terminal(page).getByRole('button', { name: /^Send / })).toHaveCount(touch ? 7 : 0);
    const view = page.viewportSize()!;
    for (const part of ['.term-body', '.term-keys']) {
      const box = (await terminal(page).locator(part).boundingBox())!;
      expect(box.y, `${part} starts on the screen`).toBeGreaterThanOrEqual(0);
      expect(box.y + box.height, `${part} ends on the screen`).toBeLessThanOrEqual(view.height);
    }
    await expect(page.locator('html')).toHaveClass(/bridge-open/);
    await press(terminal(page).locator('.xterm'), touch);

    await shows(page, 'Fleet key');
    await answer(page, 'crew');
    await shows(page, 'Fleet name');
    await answer(page, 'Crew');
    await shows(page, 'Color');
    await answer(page, '');
    await shows(page, 'Harnesses');
    await answer(page, 'claude');
    await shows(page, 'Executable for claude');
    await answer(page, '');
    await shows(page, 'Identity directory for claude');
    await answer(page, '~/.claude-crew');
    await shows(page, 'Extra variables');
    await answer(page, '');
    await shows(page, 'Search roots');
    await answer(page, code);
    await shows(page, 'Add another fleet');
    await answer(page, '');
    await shows(page, 'Write');
    await answer(page, 'y');

    await expect(terminal(page)).toBeHidden({ timeout: 15_000 });
    await expect(page.locator('html')).not.toHaveClass(/bridge-open/);
    await expect(page.getByRole('region', { name: 'Crew marina' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Set up a fleet' })).toHaveCount(0);
    await expect(countButton(page, '1 fleet')).toBeVisible();

    const written = JSON.parse(readFileSync(join(admiral.home, 'config.json'), 'utf8'));
    expect(Object.keys(written.fleets)).toEqual(['crew']);
    expect(written.fleets.crew.name).toBe('Crew');
    expect(Object.keys(written.fleets.crew).filter((k) => ['claude', 'codex', 'copilot'].includes(k))).toEqual(['claude']);
    expect(written.fleets.crew.claude.executable).toBe(join(fakeharness, 'claude'));
    expect(String(written.fleets.crew.claude.env.CLAUDE_CONFIG_DIR).replace(/^~/, user)).toBe(join(user, '.claude-crew'));
    expect(written.fleets.crew.search_roots.map((r: string) => r.replace(/^~/, user))).toEqual([code]);
    // The setup session is not a ship, and it ended with setup.
    expect(sessions(admiral.home)).not.toContain('_setup');
    const ships = await (await fetch(`${admiral.url}api/ships`)).json();
    expect(JSON.stringify(ships)).not.toContain('_setup');

    // ---- the header count opens it again; a second browser joins the same session ----
    const before = readFileSync(join(admiral.home, 'config.json'), 'utf8');
    await press(countButton(page, '1 fleet'), touch);
    await expect(terminal(page)).toBeVisible();
    await shows(page, 'Fleet key');

    const other = await page.context().newPage();
    await spyOnPage(other);
    await other.goto(admiral.url);
    await press(countButton(other, '1 fleet'), touch);
    await expect(terminal(other)).toBeVisible();
    await shows(other, 'Fleet key');
    expect(sessions(admiral.home).filter((s) => s === '_setup')).toHaveLength(1);

    // ---- leave without writing: both terminals close and nothing changes ----
    await press(terminal(other).locator('.xterm'), touch);
    await other.keyboard.press('Control+c');
    await expect(terminal(other)).toBeHidden({ timeout: 15_000 });
    await expect(terminal(page)).toBeHidden({ timeout: 15_000 });
    expect(sessions(admiral.home)).not.toContain('_setup');
    expect(readFileSync(join(admiral.home, 'config.json'), 'utf8')).toBe(before);
    await expect(page.getByRole('region', { name: /marina$/ })).toHaveCount(1);
    await expect(countButton(page, '1 fleet')).toBeVisible();
    expect(problems).toEqual([]);
  });
});
