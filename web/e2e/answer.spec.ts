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
  await appendLines(admiral, ship, [line.toolUse('t1', 'Bash', { command: 'rm -rf oldest-build' }), line.toolUse('t2', 'Bash', { command: 'git push newest' })]);
  setReason(admiral, ship, 'permission:Bash');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);

  await expect(button(page, 'Allow once')).toBeVisible();
  await expect(button(page, 'Deny')).toBeVisible();
  await expect(viewPanel(page).getByText('Bash').first()).toBeVisible();
  await expect(viewPanel(page).getByText(/oldest-build/).last()).toBeVisible();
  await button(page, 'Allow once').tap();

  await expect.poll(() => received(admiral, ship)).toBe('1');
  await expect(button(page, 'Allow once')).toHaveCount(0);
  await expect(terminalCard(page)).toHaveCount(0);
});

test('a question card sends the number of the tapped option', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  await appendLines(admiral, ship, [line.ask('toolu_q1', [{ question: 'Which colour?', options: ['Red', 'Green', 'Blue'] }])]);
  setReason(admiral, ship, 'question');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);

  await expect(button(page, 'Red')).toBeVisible();
  await expect(button(page, 'Blue')).toBeVisible();
  await button(page, 'Green').tap();
  await expect.poll(() => received(admiral, ship)).toBe('2');
  await expect(button(page, 'Green')).toHaveCount(0);
});

test('a card goes when the pane moves on, and a refused answer removes it and sends nothing more', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  setReason(admiral, ship, 'permission:Bash');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);
  await expect(button(page, 'Allow once')).toBeVisible();

  // The reason moves on without the ship leaving needs you: the permission card is replaced, never kept.
  setReason(admiral, ship, 'stop');
  await expect(button(page, 'Allow once')).toHaveCount(0);
  await expect(terminalCard(page)).toBeVisible();

  setReason(admiral, ship, 'permission:Edit');
  await expect(button(page, 'Allow once')).toBeVisible();
  await page.route('**/api/v1/ships/*/answer', (route) => route.fulfill({ status: 409, contentType: 'application/json', body: '{"error":"the pane is not on that card"}' }));
  await button(page, 'Allow once').tap();
  await expect(button(page, 'Allow once')).toHaveCount(0);
  await expect(button(page, 'Deny')).toHaveCount(0);
  expect(received(admiral, ship)).toBe('');
});

test('a prompt Admiral cannot answer shows the terminal card, and only Open terminal leaves the Conversation tab', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  await appendLines(admiral, ship, [line.ask('toolu_m1', [{ question: 'Which colours?', options: ['Red', 'Green'], multiSelect: true }])]);
  setReason(admiral, ship, 'question');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);

  await expect(terminalCard(page)).toBeVisible();
  await expect(button(page, 'Red')).toHaveCount(0);
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
