import { expect, test } from '@playwright/test';

// S7: the page loads, shows the header, has no console errors and talks to no
// host but the server (story 10).
test('page shows Admiral, logs no errors and requests only the server', async ({ page, baseURL }) => {
  const errors: string[] = [];
  const foreign: string[] = [];
  const server = new URL(baseURL!);

  page.on('console', (m) => m.type() === 'error' && errors.push(m.text()));
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('request', (r) => {
    const u = new URL(r.url());
    if (['data:', 'blob:', 'about:'].includes(u.protocol)) return;
    if (u.host !== server.host) foreign.push(r.url());
  });

  await page.goto('/');
  await expect(page.getByText('Admiral', { exact: true })).toBeVisible();
  await expect(page.getByText('Fleet command for coding agents')).toBeVisible();
  await page.waitForLoadState('networkidle');

  expect(errors).toEqual([]);
  expect(foreign).toEqual([]);
});
