import { expect, test } from './fixtures';
import { holds, launchShip, messageBox, received, rowLink, seedFleets, seedTurn, setState, viewPanel, viewTab, withFakeGh } from './shell';

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

test.describe('with a mouse', () => {
  test.use({ hasTouch: false, isMobile: false });

  test('boarding a ship focuses its message box once', async ({ page, admiral }) => {
    seedFleets(admiral, { live: true });
    await admiral.start({ env: withFakeGh(admiral) });
    const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste', 'reply Aye'] });
    await seedTurn(admiral, ship);
    setState(admiral, ship, 'working');
    await page.goto(admiral.url + 'app/ships?fleet=zulu');
    await rowLink(page, ship).click();

    await expect(viewPanel(page)).toContainText('Rebase onto main please');
    await expect(messageBox(page)).toBeFocused();

    await viewTab(page, 'Terminal').click();
    await viewTab(page, 'Conversation').click();
    await expect(messageBox(page)).toBeEnabled();
    await holds(() => messageBox(page).evaluate((el) => el !== document.activeElement), 'coming back to Conversation does not refocus the message box');
  });
});
