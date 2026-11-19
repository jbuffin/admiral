import { expect, test } from './fixtures';
import { chatReceived } from './shell';
import { entry, expectRowsDrawn, MESSAGE_JOB, MESSAGE_LINK, PAGE, rowDetail, seedModule } from './modulePage';

test('on the iPad a module has a nav entry after Crew with its waiting count, draws its groups and rows, filters them, opens a row in the side pane that a reload keeps, and redraws on a page change', async ({ page, admiral }) => {
  const mod = seedModule(admiral, { chat: true });
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
  // Opening the module's fleet chat starts it, so the message below has a running chat to reach.
  await page.goto(admiral.url + 'app/chat?fleet=harbor');
  await expect(page.getByRole('main').getByRole('button', { name: 'Open terminal', exact: true })).toHaveCount(1);
  await page.goto(admiral.url + 'app/ships?fleet=harbor');

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

  // A module action's step, a link's chips and its subtitle reach the page from the plugin.
  const run = main.getByRole('listitem').filter({ hasText: 'Release build' });
  const onTarget = run.getByRole('group', { name: 'Acts on Deploy stage' });
  await expect(onTarget.getByRole('button', { name: 'Rerun stage' })).toBeVisible();
  await expect(run.getByRole('button', { name: 'Skip stage' })).toHaveCount(0);
  await main.getByRole('button', { name: 'Open Release build' }).tap();
  const stages = page.getByRole('complementary', { name: 'Selected row' });
  await expect(stages.getByText('Raise the retry limit')).toBeVisible();
  await expect(stages.getByText('Merged')).toBeVisible();
  await expect(stages.getByRole('button', { name: 'Skip stage' })).toBeVisible();

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

  // Built-in actions: Open link is a link.
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Fix login' });
  const open = row.getByRole('link', { name: 'Open docs' });
  await expect(open).toHaveAttribute('href', 'https://tracker.example/docs');
  await expect(open).toHaveAttribute('target', '_blank');
  await expect(open).toHaveAttribute('rel', /noopener/);
  await expect(open).toHaveAttribute('rel', /noreferrer/);

  // Message AdmiralBot: the confirm shows the composed text, and the chat receives exactly that text.
  const preview = page.waitForResponse((r) => /\/actions\/tell\/message$/.test(r.url()) && r.request().method() === 'GET');
  await row.getByRole('button', { name: 'Message AdmiralBot' }).tap();
  const { text, chat } = (await (await preview).json()) as { text: string; chat: string };
  expect(text).toContain(MESSAGE_JOB);
  expect(text).toContain('T-1');
  expect(text).toContain('Fix login');
  expect(text.endsWith(MESSAGE_LINK)).toBe(true);
  const confirm = page.getByRole('dialog');
  await expect(confirm.locator('.mmessage')).toBeVisible();
  expect(await confirm.locator('.mmessage').textContent()).toBe(text);
  expect(chatReceived(admiral, 'harbor')).not.toContain(MESSAGE_JOB);
  await confirm.getByRole('button', { name: 'Message AdmiralBot' }).tap();
  const sent = page.locator('.toasts');
  await expect(sent.getByText('Sent to AdmiralBot')).toBeVisible();
  await expect(sent.getByRole('link')).toHaveAttribute('href', chat);
  await expect.poll(() => chatReceived(admiral, 'harbor')).toContain(text);
  await sent.getByRole('button', { name: 'Dismiss' }).tap();

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
