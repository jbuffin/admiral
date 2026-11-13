import { expect, test } from './fixtures';
import { launchShip, messageBox, received, rowLink, seedFleets, sendButton, sentBubble, setState, viewPanel, withFakeGh } from './shell';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
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

test('typing / offers the harness\'s skills above the box, and arrows with Tab or a tap complete one', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  for (const [name, description] of [['tdd', 'Test-driven development.'], ['triage', 'Sort the new issues.'], ['review', 'Review the diff.']]) {
    const dir = join(admiral.home, 'identity', 'zulu-claude', 'skills', name);
    mkdirSync(dir, { recursive: true });
    writeFileSync(join(dir, 'SKILL.md'), `---\nname: ${name}\ndescription: ${description}\n---\n`);
  }
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste', 'reply Aye'] });
  setState(admiral, ship, 'working');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, ship).tap();

  const menu = viewPanel(page).getByRole('listbox', { name: 'Skills' });
  await messageBox(page).pressSequentially('/');
  await expect(menu.getByRole('option')).toHaveText(['/reviewReview the diff.', '/tddTest-driven development.', '/triageSort the new issues.']);
  await messageBox(page).pressSequentially('t');
  await expect(menu.getByRole('option')).toHaveText([/^\/tdd/, /^\/triage/]);
  await messageBox(page).press('ArrowDown');
  await expect(menu.getByRole('option', { selected: true })).toContainText('/triage');
  await messageBox(page).press('Tab');
  await expect(messageBox(page)).toHaveValue('/triage ');
  await expect(menu).toBeHidden();
  await messageBox(page).pressSequentially('the inbox');
  await messageBox(page).press('Enter');
  await expect.poll(() => received(admiral, ship)).toContain('/triage the inbox');

  await messageBox(page).pressSequentially('/rev');
  await menu.getByRole('option', { name: /review/ }).tap();
  await expect(messageBox(page)).toHaveValue('/review ');
  await expect(messageBox(page)).toBeFocused();
  await messageBox(page).pressSequentially('/x');
  await expect(menu).toBeHidden();
});
