import { expect, test } from './fixtures';
import { expectMoreWithoutOrders, ordersJourney, seedOrders } from './orders';
import { withFakeGh } from './shell';

test('on the phone, Orders is a tab with For you, view tabs and Refresh now, the rows are two lines and the arrow launches', async ({ page, admiral }) => {
  seedOrders(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  await ordersJourney(page, admiral, (l) => l.tap(), { phone: true });
  // A row is a line of text with the arrow beside it, not a column with the arrow below.
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Review the cache layer' });
  const rowBox = (await row.boundingBox())!;
  const titleBox = (await row.locator('.orow-title').boundingBox())!;
  const arrowBox = (await row.getByRole('button', { name: /^Launch / }).boundingBox())!;
  const arrowMid = arrowBox.y + arrowBox.height / 2;
  expect(rowBox.height).toBeLessThan(80);
  expect(arrowMid).toBeGreaterThanOrEqual(titleBox.y);
  expect(arrowMid).toBeLessThanOrEqual(titleBox.y + titleBox.height);
  await expectMoreWithoutOrders(page, (l) => l.tap());
});
