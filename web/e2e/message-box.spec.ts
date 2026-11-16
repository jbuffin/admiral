import { expect, test } from './fixtures';
import { launchShip, messageBox, received, rowLink, seedFleets, setState, withFakeGh } from './shell';

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
