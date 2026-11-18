import { expect, test } from './fixtures';
import { entry, expectRowsDrawn, PAGE, rowDetail, seedModule } from './modulePage';

test('on the iPad a module has a nav entry after Crew with its waiting count, draws its groups and rows, filters them, opens a row in the side pane that a reload keeps, and redraws on a page change', async ({ page, admiral }) => {
  const mod = seedModule(admiral);
  mod.form('pass', {
    title: 'Pass this review',
    fields: [
      { id: 'whose', label: 'Whose work', type: 'choice', options: [{ id: 'a', label: 'Amy' }, { id: 'b', label: 'Bo' }], default: 'b' },
      { id: 'files', label: 'Files', type: 'checklist', options: [{ id: 'x', label: 'X file' }, { id: 'y', label: 'Y file' }] },
      { id: 'time', label: 'Time', type: 'duration', default: '01:30', min: '00:15' },
    ],
  });
  mod.run('pass', { message: 'Review passed', url: 'https://example.com/review/t1' });
  await admiral.start();
  await page.goto(admiral.url + 'app/ships?fleet=zulu');

  const side = page.getByRole('navigation', { name: 'Main' });
  await expect(side.getByRole('link').filter({ hasText: /^(Crew|Deploys|Preferences)/ })).toHaveText([/^Crew/, /^Deploys\s*2$/, /^Preferences/]);

  await entry(side).tap();
  await expect(page).toHaveURL(/\/app\/deploys(\?|$)/);
  await expect(page.getByRole('heading', { name: 'Deploys', exact: true })).toBeVisible();
  await expectRowsDrawn(page);

  // The filter narrows on the client: a group with no match hides.
  const main = page.getByRole('main');
  await main.getByRole('searchbox', { name: /filter/i }).fill('docs');
  await expect(main.getByRole('listitem').filter({ hasText: 'Ship docs' })).toBeVisible();
  await expect(main.getByText('Fix login')).toBeHidden();
  await expect(main.getByRole('heading', { name: /^Needs review/ })).toBeHidden();
  await main.getByRole('searchbox', { name: /filter/i }).fill('');
  await expect(main.getByText('Fix login')).toBeVisible();

  // A row opens in the side pane, with its id in the URL.
  await main.getByRole('button', { name: 'Open Fix login' }).tap();
  await expect(page).toHaveURL(/\/app\/deploys\/t1(\?|$)/);
  const pane = page.getByRole('complementary', { name: 'Selected row' });
  await expect(pane.getByRole('link', { name: 'Ticket' })).toHaveAttribute('href', 'https://tracker.example/t/1');
  await expect(pane.getByText('Code review')).toBeVisible();
  await expect(pane.getByText('amy')).toBeVisible();
  await expect(pane.getByRole('button', { name: 'Recheck' })).toBeEnabled();

  // A reload on the row's URL keeps the selection.
  await page.reload();
  await expect(page).toHaveURL(/\/app\/deploys\/t1(\?|$)/);
  await expect(page.getByRole('complementary', { name: 'Selected row' }).getByText('Code review')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Open Fix login' })).toHaveAttribute('aria-pressed', 'true');

  // A form module action on the row opens its fetched form, and its result shows as a toast with its link.
  const fix = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Fix login' });
  await fix.getByRole('button', { name: 'Pass review' }).tap();
  const sheet = page.getByRole('dialog', { name: 'Pass this review' });
  await expect(sheet.getByText('Whose work')).toBeVisible();
  await expect(sheet.getByRole('radio', { name: 'Bo' })).toBeChecked();
  await sheet.getByLabel('Time').fill('00:05');
  await expect(sheet.getByRole('button', { name: 'Submit' })).toBeDisabled();
  await sheet.getByLabel('Time').fill('02:00');
  await sheet.getByRole('button', { name: 'Submit' }).tap();
  const toast = page.locator('.toasts');
  await expect(toast.getByText('Review passed')).toBeVisible();
  await expect(toast.getByRole('link')).toHaveAttribute('href', 'https://example.com/review/t1');
  await expect(fix.getByText('updating…')).toBeVisible();
  await expect(fix.getByRole('button', { name: 'Pass review' })).toBeDisabled();
  await toast.getByRole('button', { name: 'Dismiss' }).tap();

  // The plugin says the page moved: the row's module actions come back.
  mod.changed(['t1']);
  await expect(fix.getByText('updating…')).toBeHidden();
  await expect(fix.getByRole('button', { name: 'Pass review' })).toBeEnabled();

  // The plugin says the row moved: the page and the open row redraw, and the badge follows.
  mod.row('t1', rowDetail('bo'));
  mod.page({ ...PAGE, waiting: 3, groups: [{ ...PAGE.groups[0], rows: [...PAGE.groups[0].rows, { id: 't4', title: 'Audit logs', chips: [] }] }, PAGE.groups[1]] });
  mod.changed(['t1']);
  await expect(page.getByRole('main').getByText('Audit logs')).toBeVisible();
  await expect(page.getByRole('complementary', { name: 'Selected row' }).getByText('bo')).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: /^Deploys\s*3$/ })).toBeVisible();
});
