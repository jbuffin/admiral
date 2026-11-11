import { expect, test } from './fixtures';
import { launchShip, messageBox, received, rowLink, seedFleets, sendButton, sentBubble, setState, viewPanel, withFakeGh } from './shell';
import { existsSync } from 'node:fs';
import { join } from 'node:path';

test('a refused or lost send shows an error beside the box and keeps the text', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste', 'reply Aye'] });
  setState(admiral, ship, 'working');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, ship).tap();

  await page.route('**/api/v1/ships/*/messages', (route) => route.fulfill({ status: 409, contentType: 'application/json', body: '{"error":"ship is anchored"}' }));
  await messageBox(page).fill('Keep this text');
  await sendButton(page).tap();
  await expect(viewPanel(page).getByRole('alert')).toBeVisible();
  await expect(messageBox(page)).toHaveValue('Keep this text');
  await expect(sentBubble(page, 'Keep this text')).toHaveCount(0);

  await page.unroute('**/api/v1/ships/*/messages');
  await page.route('**/api/v1/ships/*/messages', (route) => route.abort());
  await sendButton(page).tap();
  await expect(viewPanel(page).getByRole('alert')).toBeVisible();
  await expect(messageBox(page)).toHaveValue('Keep this text');
});

test('Shift+Enter adds a line to the message and Enter sends it whole', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste', 'reply Aye'] });
  setState(admiral, ship, 'working');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, ship).tap();

  await messageBox(page).fill('first line');
  await messageBox(page).press('Shift+Enter');
  await messageBox(page).pressSequentially('second line');
  await expect(messageBox(page)).toHaveValue('first line\nsecond line');
  await messageBox(page).press('Enter');
  await expect(messageBox(page)).toHaveValue('');
  await expect.poll(() => received(admiral, ship)).toContain('first line\nsecond line');
});

test('a message to a ship with no conversation is sent and its bubble goes when the server accepts it', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'alpha', harness: 'copilot' });
  await expect.poll(() => existsSync(join(admiral.home, 'rec', ship.session, 'cues-done'))).toBe(true);
  setState(admiral, ship, 'needs_you');
  await page.goto(admiral.url + 'app/ships?fleet=alpha');
  await rowLink(page, ship).tap();

  await expect(messageBox(page)).toBeEnabled();
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  await page.route('**/api/v1/ships/*/messages', async (route) => {
    await gate;
    await route.continue();
  });
  await messageBox(page).fill('hello copilot');
  await sendButton(page).tap();
  await expect(sentBubble(page, 'hello copilot')).toBeVisible();
  release();
  await expect(sentBubble(page, 'hello copilot')).toHaveCount(0);
  await expect(messageBox(page)).toHaveValue('');
  await expect.poll(() => received(admiral, ship)).toContain('hello copilot');
});
