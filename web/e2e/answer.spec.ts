import { expect, test } from './fixtures';
import type { Page } from '@playwright/test';
import { boatReceived, chip, launchCrew, launchShip, line, appendLines, received, rowLink, seedFleets, setBoatState, setReason, setState, viewPanel, viewTab, withFakeGh } from './shell';

const button = (page: Page, name: string) => viewPanel(page).getByRole('button', { name, exact: true });
const terminalCard = (page: Page) => viewPanel(page).getByText('Answer this in the terminal');

async function open(page: Page, admiral: { url: string }, ship: Parameters<typeof rowLink>[1]) {
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, ship).tap();
}

test('a permission card shows the tool and its oldest pending call, and Allow once answers it in place', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  await appendLines(admiral, ship, [line.toolUse('t1', 'Bash', { description: 'Tidy the build folder', command: 'rm -rf oldest-build' }), line.toolUse('t2', 'Bash', { description: 'Publish', command: 'git push newest' })]);
  setReason(admiral, ship, 'permission:Bash');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);

  await expect(button(page, 'Allow once')).toBeVisible();
  await expect(button(page, 'Deny')).toBeVisible();
  await expect(viewPanel(page).getByText('Bash').first()).toBeVisible();
  await expect(viewPanel(page).locator('.approve-input')).toHaveText('rm -rf oldest-build');
  await expect(viewPanel(page).getByText('Tidy the build folder').last()).toBeVisible();
  await button(page, 'Allow once').tap();

  await expect.poll(() => received(admiral, ship)).toBe('1');
  await expect(button(page, 'Allow once')).toHaveCount(0);
  await expect(terminalCard(page)).toHaveCount(0);

  // A Codex ship has no conversation to read yet; its permission card still answers with Codex's keys.
  const codex = await launchShip(admiral, { fleet: 'zulu', harness: 'codex', cues: ['bracketedpaste'] });
  setReason(admiral, codex, 'permission:apply_patch');
  setState(admiral, codex, 'needs_you');
  await open(page, admiral, codex);
  await expect(button(page, 'Allow once')).toBeVisible();
  await expect(viewPanel(page).getByText('apply_patch').first()).toBeVisible();
  await button(page, 'Allow once').tap();
  await expect.poll(() => received(admiral, codex)).toBe('1');
  await expect(button(page, 'Allow once')).toHaveCount(0);
});

test('the answer card follows the pane: one answer per tap, a refusal clears it, a prompt Admiral cannot answer goes to the terminal', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  // A long conversation with an answered question: the view scrolls, never the page, even with a card under it.
  const chatter = (from: number) => Array.from({ length: 30 }, (_, i) => line.assistant(`Step ${from + i}: still working through the build.`));
  await appendLines(admiral, ship, [
    ...chatter(1),
    line.ask('toolu_s1', [{ question: 'Which colour?', options: ['Red', 'Green'] }]),
    line.askResult('toolu_s1', { 'Which colour?': 'Green' }),
    ...chatter(31),
  ]);
  setReason(admiral, ship, 'permission:Bash');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);
  await expect(viewPanel(page).locator('.q-option.chosen')).toHaveText(/Green/);
  await expect(viewPanel(page).getByText('Step 60:')).toBeVisible();
  await expect(button(page, 'Allow once')).toBeVisible();
  const doc = await page.evaluate(() => ({ height: document.scrollingElement!.scrollHeight, view: document.scrollingElement!.clientHeight }));
  expect(doc.height).toBeLessThanOrEqual(doc.view);

  let refuse = false;
  await page.route('**/api/v1/ships/*/answer', (route) =>
    refuse ? route.fulfill({ status: 409, contentType: 'application/json', body: '{"error":"the pane is not on that card"}' }) : route.fulfill({ status: 200, contentType: 'application/json', body: '{"answered":true}' }),
  );
  // Both taps land in one task, before any request is out; the count is read in the page, where a second request would show.
  await page.evaluate(() => {
    const w = window as unknown as { answers: number };
    w.answers = 0;
    const real = window.fetch;
    window.fetch = (...args) => {
      if (String(args[0] instanceof Request ? args[0].url : args[0]).endsWith('/answer')) w.answers++;
      return real(...args);
    };
  });
  await button(page, 'Allow once').evaluate((el: HTMLElement) => {
    el.click();
    el.click();
  });
  await expect(button(page, 'Allow once')).toHaveAttribute('aria-busy', 'true');
  await expect(button(page, 'Deny')).toBeDisabled();
  expect(await page.evaluate(() => (window as unknown as { answers: number }).answers)).toBe(1);

  // The reason moves on without the ship leaving needs you: the permission card is replaced, never kept.
  // A finished turn waits for a message in the box, so it sends nobody to the terminal.
  setReason(admiral, ship, 'stop');
  await expect(button(page, 'Allow once')).toHaveCount(0);
  await expect(terminalCard(page)).toHaveCount(0);

  setReason(admiral, ship, 'permission:Edit');
  await expect(button(page, 'Allow once')).toBeVisible();
  refuse = true;
  await button(page, 'Allow once').tap();
  await expect(button(page, 'Allow once')).toHaveCount(0);
  await expect(button(page, 'Deny')).toHaveCount(0);
  expect(received(admiral, ship)).toBe('');

  // A refused card does not hide the next prompt, even one with the same reason.
  setReason(admiral, ship, 'bell');
  await expect(terminalCard(page)).toBeVisible();
  setReason(admiral, ship, 'permission:Edit');
  await expect(button(page, 'Allow once')).toBeEnabled();
  await expect(button(page, 'Deny')).toBeEnabled();

  // A prompt Admiral cannot answer (a multi-select of nine) shows the terminal card; only Open terminal leaves the Conversation tab.
  await appendLines(admiral, ship, [line.ask('toolu_m1', [{ question: 'Which colours?', options: ['Red', 'Green', 'Blue', 'Cyan', 'Pink', 'Gold', 'Gray', 'Teal', 'Plum'], multiSelect: true }])]);
  setReason(admiral, ship, 'question');
  await expect(terminalCard(page)).toBeVisible();
  await expect(button(page, 'Cyan')).toHaveCount(0);
  await expect(viewTab(page, 'Conversation')).toHaveAttribute('aria-selected', 'true');
  await button(page, 'Open terminal').tap();
  await expect(viewTab(page, 'Terminal')).toHaveAttribute('aria-selected', 'true');
  expect(received(admiral, ship)).toBe('');
});

test('a ship that needs you only for its crew has no card, and the asking boat\'s chip owns it', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  setState(admiral, ship, 'working');
  await launchCrew(admiral, ship, 'mate', 'reviewer', ['bracketedpaste']);
  setReason(admiral, ship, 'permission:Bash', 'mate');
  setBoatState(admiral, ship, 'mate', 'needs_you');
  await open(page, admiral, ship);

  await expect(chip(page, /reviewer/)).toBeVisible();
  await expect(button(page, 'Allow once')).toHaveCount(0);
  await expect(terminalCard(page)).toHaveCount(0);

  await chip(page, /reviewer/).tap();
  await expect(button(page, 'Allow once')).toBeVisible();
  await button(page, 'Allow once').tap();
  await expect.poll(() => boatReceived(admiral, ship, 'mate')).toBe('1');
  await expect(button(page, 'Allow once')).toHaveCount(0);
  expect(received(admiral, ship)).toBe('');
});
