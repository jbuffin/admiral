import { test } from './fixtures';
import { ordersJourney, seedOrders } from './orders';
import { withFakeGh } from './shell';

test('on the iPad, Orders opens from the sidebar with For you, view tabs, a filter and Refresh now, and the arrow launches', async ({ page, admiral }) => {
  seedOrders(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  await ordersJourney(page, admiral, (l) => l.tap(), { phone: false });
});
