import { expect, test } from './fixtures';
import type { Page } from '@playwright/test';
import { boatReceived, chip, launchCrew, launchShip, line, appendLines, received, rowLink, seedFleets, setBoatState, setReason, setState, viewPanel, viewTab, withFakeGh } from './shell';

const button = (page: Page, name: string) => viewPanel(page).getByRole('button', { name, exact: true });
const terminalCard = (page: Page) => viewPanel(page).getByText('Answer this in the terminal');

async function open(page: Page, admiral: { url: string }, ship: Parameters<typeof rowLink>[1]) {
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, ship).tap();
}

test('a permission card shows the tool and its oldest pending call, and Allow once answers it in place', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  await appendLines(admiral, ship, [line.toolUse('t1', 'Bash', { description: 'Tidy the build folder', command: 'rm -rf oldest-build' }), line.toolUse('t2', 'Bash', { description: 'Publish', command: 'git push newest' })]);
  setReason(admiral, ship, 'permission:Bash');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);

  await expect(button(page, 'Allow once')).toBeVisible();
  await expect(button(page, 'Deny')).toBeVisible();
  await expect(viewPanel(page).getByText('Bash').first()).toBeVisible();
  await expect(viewPanel(page).locator('.approve-input')).toHaveText('rm -rf oldest-build');
  await expect(viewPanel(page).getByText('Tidy the build folder').last()).toBeVisible();
  await button(page, 'Allow once').tap();

  await expect.poll(() => received(admiral, ship)).toBe('1');
  await expect(button(page, 'Allow once')).toHaveCount(0);
  await expect(terminalCard(page)).toHaveCount(0);

  // A Codex ship has no conversation to read yet; its permission card still answers with Codex's keys.
  const codex = await launchShip(admiral, { fleet: 'zulu', harness: 'codex', cues: ['bracketedpaste'] });
  setReason(admiral, codex, 'permission:apply_patch');
  setState(admiral, codex, 'needs_you');
  await open(page, admiral, codex);
  await expect(button(page, 'Allow once')).toBeVisible();
  await expect(viewPanel(page).getByText('apply_patch').first()).toBeVisible();
  await button(page, 'Allow once').tap();
  await expect.poll(() => received(admiral, codex)).toBe('1');
  await expect(button(page, 'Allow once')).toHaveCount(0);
});

test('a question card sends the number of the tapped option', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  await appendLines(admiral, ship, [line.ask('toolu_q1', [{ question: 'Which colour?', options: ['Red', 'Green', 'Blue'] }])]);
  setReason(admiral, ship, 'question');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);

  await expect(button(page, 'Red')).toBeVisible();
  await expect(button(page, 'Blue')).toBeVisible();
  await button(page, 'Green').tap();
  await expect.poll(() => received(admiral, ship)).toBe('2');
  await expect(button(page, 'Green')).toHaveCount(0);
});

test('a four-question Claude call is stepped through the card and answered with one request at iPad width', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const asked = [
    { question: 'Which colour?', header: 'Colour', options: ['Red', 'Green', 'Blue'], descriptions: ['A **bold** colour', 'A fresh colour', 'A calm colour'] },
    { question: 'Which toppings?', header: 'Toppings', options: ['Cheese', 'Olives', 'Mushrooms'], multiSelect: true, descriptions: ['Use `melted`', 'Briny', 'Earthy'] },
    { question: 'Which size?', header: 'Size', options: ['Small', 'Large'], descriptions: ['One person', 'To share'] },
    { question: 'How to deliver?', header: 'Delivery', options: ['Pickup', 'Courier'], descriptions: ['Collect it', 'To the door'] },
  ];
  const menu = asked.map((q) => ({ text: q.question, labels: q.options, multi: q.multiSelect ?? false }));
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste', `menu ${JSON.stringify(menu)}`] });
  await appendLines(admiral, ship, [line.ask('toolu_c4', asked)]);
  setReason(admiral, ship, 'permission:AskUserQuestion');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);

  const card = viewPanel(page).locator('.q-card');
  await expect(card.getByText('Question 1 of 4')).toBeVisible();
  // A description is Markdown inside its button, as it is in the answered card.
  await expect(card.getByRole('radio', { name: /^Red/ }).locator('strong')).toHaveText('bold');
  await card.getByRole('radio', { name: /^Green/ }).tap();
  await card.getByRole('button', { name: 'Next', exact: true }).tap();
  await card.getByRole('checkbox', { name: /^Cheese/ }).tap();
  await card.getByRole('checkbox', { name: /^Mushrooms/ }).tap();
  await card.getByRole('button', { name: 'Next', exact: true }).tap();
  await card.getByLabel('Other answer').fill('Medium, thanks');
  await card.getByRole('button', { name: 'Next', exact: true }).tap();
  await card.getByRole('radio', { name: /^Courier/ }).tap();
  await card.getByRole('button', { name: 'Next', exact: true }).tap();
  // Like Claude's own review screen, the card lists every answer before it sends, and Back still edits them.
  await expect(card.getByText('Review your answers')).toBeVisible();
  await expect(card.locator('.q-review-a')).toHaveText(['→ Green', '→ Cheese, Mushrooms', '→ Medium, thanks', '→ Courier']);
  expect(received(admiral, ship)).toBe('');
  await card.getByRole('button', { name: 'Back', exact: true }).tap();
  await expect(card.getByText('Question 4 of 4')).toBeVisible();
  await card.getByRole('button', { name: 'Next', exact: true }).tap();
  const sent = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/answer'));
  await card.getByRole('button', { name: 'Submit answers', exact: true }).tap();
  expect((await sent).postDataJSON().answers).toEqual([{ options: [2] }, { options: [1, 3] }, { options: [], other: 'Medium, thanks' }, { options: [2] }]);

  // The menu took the keys one step at a time and ended on the review screen's 1.
  const down = '\x1b[B';
  await expect.poll(() => received(admiral, ship)).toBe(['2', '\r', down + down, '\r', down + down, '\r', '3', 'Medium, thanks', '\r', '2', '1'].join(''));
  await expect(card.getByRole('button', { name: 'Submit answers', exact: true })).toHaveCount(0);

  // Once Claude records the answers, the same call is the read-only record: chips, rendered descriptions and chosen marks.
  await appendLines(admiral, ship, [
    line.askResult('toolu_c4', { 'Which colour?': 'Green', 'Which toppings?': 'Cheese, Mushrooms', 'Which size?': 'Medium, thanks', 'How to deliver?': 'Courier' }),
  ]);
  setState(admiral, ship, 'working');
  const record = viewPanel(page).locator('.q-record');
  await expect(record.locator('.q-chip')).toHaveText(['Colour', 'Toppings', 'Size', 'Delivery']);
  await expect(record.locator('.q-desc strong')).toHaveText('bold');
  await expect(record.locator('.q-desc code')).toHaveText('melted');
  await expect(record.locator('.q-option.chosen')).toHaveText([/Green/, /Cheese/, /Mushrooms/, /Courier/]);
  await expect(record.locator('.q-free')).toHaveText('Other: Medium, thanks');
});

test('a refused answer leaves the question in the transcript with the reason and an Open terminal button', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  // The fake pane draws no menu, so the sender finds nothing to step through and refuses.
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  await appendLines(admiral, ship, [line.ask('toolu_r1', [{ question: 'Which colour?', header: 'Colour', options: ['Red', 'Green'] }, { question: 'Which size?', header: 'Size', options: ['Small', 'Large'] }])]);
  setReason(admiral, ship, 'permission:AskUserQuestion');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);

  const card = viewPanel(page).locator('.q-card');
  await card.getByRole('radio', { name: /^Red/ }).tap();
  await card.getByRole('button', { name: 'Next', exact: true }).tap();
  await card.getByRole('radio', { name: /^Large/ }).tap();
  await card.getByRole('button', { name: 'Next', exact: true }).tap();
  await card.getByRole('button', { name: 'Submit answers', exact: true }).tap();

  await expect(card.getByRole('alert')).toContainText('terminal');
  await expect(card.getByText('Which colour?')).toBeVisible();
  await expect(card.getByText('Which size?')).toBeVisible();
  expect(received(admiral, ship)).toBe('');
  await button(page, 'Open terminal').tap();
  await expect(viewTab(page, 'Terminal')).toHaveAttribute('aria-selected', 'true');
});

test('a card goes when the pane moves on, and a refused answer removes it and sends nothing more', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  await appendLines(admiral, ship, [line.assistant('Working on it')]);
  setReason(admiral, ship, 'permission:Bash');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);
  await expect(button(page, 'Allow once')).toBeVisible();

  // The reason moves on without the ship leaving needs you: the permission card is replaced, never kept.
  setReason(admiral, ship, 'stop');
  await expect(button(page, 'Allow once')).toHaveCount(0);
  await expect(terminalCard(page)).toBeVisible();

  setReason(admiral, ship, 'permission:Edit');
  await expect(button(page, 'Allow once')).toBeVisible();
  await page.route('**/api/v1/ships/*/answer', (route) => route.fulfill({ status: 409, contentType: 'application/json', body: '{"error":"the pane is not on that card"}' }));
  await button(page, 'Allow once').tap();
  await expect(button(page, 'Allow once')).toHaveCount(0);
  await expect(button(page, 'Deny')).toHaveCount(0);
  expect(received(admiral, ship)).toBe('');

  // A refused card does not hide the next prompt, even one with the same reason.
  setReason(admiral, ship, 'stop');
  await expect(terminalCard(page)).toBeVisible();
  setReason(admiral, ship, 'permission:Edit');
  await expect(button(page, 'Allow once')).toBeEnabled();
  await expect(button(page, 'Deny')).toBeEnabled();
});

test('a double tap on Allow once posts one answer', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  setReason(admiral, ship, 'permission:Bash');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);
  let posts = 0;
  let answered!: () => void;
  const done = new Promise<void>((r) => (answered = r));
  await page.route('**/api/v1/ships/*/answer', async (route) => {
    posts++;
    await new Promise((r) => setTimeout(r, 300));
    await route.fulfill({ status: 200, contentType: 'application/json', body: '{"answered":true}' });
    answered();
  });
  await button(page, 'Allow once').evaluate((el: HTMLElement) => {
    el.click();
    el.click();
  });
  await expect(button(page, 'Allow once')).toBeDisabled();
  await done;
  expect(posts).toBe(1);
});

test('a prompt Admiral cannot answer shows the terminal card, and only Open terminal leaves the Conversation tab', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  await appendLines(admiral, ship, [line.ask('toolu_m1', [{ question: 'Which colours?', options: ['Red', 'Green', 'Blue', 'Cyan', 'Pink', 'Gold', 'Gray', 'Teal', 'Plum'], multiSelect: true }])]);
  setReason(admiral, ship, 'question');
  setState(admiral, ship, 'needs_you');
  await open(page, admiral, ship);

  await expect(terminalCard(page)).toBeVisible();
  await expect(button(page, 'Red')).toHaveCount(0);
  await expect(viewTab(page, 'Conversation')).toHaveAttribute('aria-selected', 'true');
  await button(page, 'Open terminal').tap();
  await expect(viewTab(page, 'Terminal')).toHaveAttribute('aria-selected', 'true');
  expect(received(admiral, ship)).toBe('');
});

test('a ship that needs you only for its crew has no card, and the asking boat\'s chip owns it', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  setState(admiral, ship, 'working');
  await launchCrew(admiral, ship, 'mate', 'reviewer', ['bracketedpaste']);
  setReason(admiral, ship, 'permission:Bash', 'mate');
  setBoatState(admiral, ship, 'mate', 'needs_you');
  await open(page, admiral, ship);

  await expect(chip(page, /reviewer/)).toBeVisible();
  await expect(button(page, 'Allow once')).toHaveCount(0);
  await expect(terminalCard(page)).toHaveCount(0);

  await chip(page, /reviewer/).tap();
  await expect(button(page, 'Allow once')).toBeVisible();
  await button(page, 'Allow once').tap();
  await expect.poll(() => boatReceived(admiral, ship, 'mate')).toBe('1');
  await expect(button(page, 'Allow once')).toHaveCount(0);
  expect(received(admiral, ship)).toBe('');
});
