import { test } from './fixtures';
import { expectMoreWithoutOrders, ordersJourney, seedOrders } from './orders';
import { withFakeGh } from './shell';

test('on the phone, Orders is a tab with For you, view tabs and Refresh now, the rows are two lines and the arrow launches', async ({ page, admiral }) => {
  seedOrders(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  await ordersJourney(page, admiral, (l) => l.tap(), { phone: true });
  await expectMoreWithoutOrders(page, (l) => l.tap());
});
