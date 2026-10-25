import type { Page } from '@playwright/test';
import { Chat, firstChatSnapshot } from './chat';
import { expect, test } from './fixtures';

// The AdmiralBot chat on a phone (issue #119, u6), at the `phone` project's 402x874. The Chat tab is a region
// "Chat with AdmiralBot" in the tab bar ("Sections") button "Chat"; its name gains ", new reply" while the dot
// shows. The box, Send, the typing dots and the question cards are named as in chat.spec.ts.

test.beforeEach(({}, info) => test.skip(info.project.name !== 'phone', 'the phone tab is the phone project only'));

const chatTab = (page: Page) => page.getByRole('navigation', { name: 'Sections' }).getByRole('button', { name: /^Chat/ });
const pane = (page: Page) => page.getByRole('region', { name: 'Chat with AdmiralBot', exact: true });
const box = (page: Page) => pane(page).getByRole('textbox', { name: 'Message AdmiralBot' });
const send = (page: Page) => pane(page).getByRole('button', { name: 'Send message' });

test('C15: the Chat tab starts the chat, sends a message and shows the reply', { tag: '@critical' }, async ({ page, admiral }) => {
  const chat = new Chat(admiral);
  await admiral.start();
  await chat.begin();
  await page.goto(admiral.url);
  const started = page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/chat/start'));
  await chatTab(page).tap();
  await started;
  await expect(pane(page)).toBeVisible();

  await box(page).tap();
  await page.keyboard.type('Status of the fleet?');
  await send(page).tap();
  await expect.poll(() => chat.stdin()).toBe('\x1b[200~Status of the fleet?\x1b[201~\r');
  await expect(box(page)).toHaveValue('');

  chat.user('u-1', 'Status of the fleet?');
  chat.assistant('a-1', 'All ships are underway.');
  await expect(pane(page).getByText('All ships are underway.')).toBeVisible();

  // A tab is not a layer: Escape does not leave it.
  await page.keyboard.press('Escape');
  await expect(pane(page)).toBeVisible();
});

test('C15: the dot shows on the tab for a reply that lands while another tab shows, and goes when the tab opens', async ({ page, admiral }) => {
  const chat = new Chat(admiral);
  await admiral.start();
  await chat.beginWith([{ user: 'Hello' }, { assistant: 'Hello, engineer.' }]);
  const snapshot = firstChatSnapshot(page);
  await page.goto(admiral.url);
  await snapshot; // the first snapshot is what counts as seen
  await expect(chatTab(page)).toHaveAccessibleName('Chat');
  chat.assistant('a-late', 'One more thing.');
  await expect(chatTab(page)).toHaveAccessibleName('Chat, new reply');
  await chatTab(page).tap();
  await expect(pane(page).getByText('One more thing.')).toBeVisible();
  await expect(chatTab(page)).toHaveAccessibleName('Chat');
});

test('C19: at 874x402 the box, Send and a message are on screen without scrolling the page', async ({ page, admiral }) => {
  const chat = new Chat(admiral);
  await admiral.start();
  await chat.beginWith([{ user: 'Hello' }, { assistant: 'Hello, engineer.' }]);
  await page.setViewportSize({ width: 874, height: 402 });
  await page.goto(admiral.url);
  await chatTab(page).tap();
  await expect(box(page)).toBeInViewport({ ratio: 1 });
  await expect(send(page)).toBeInViewport({ ratio: 1 });
  await expect(pane(page).getByText('Hello, engineer.')).toBeInViewport({ ratio: 1 });
  expect(await page.evaluate(() => document.documentElement.scrollHeight <= window.innerHeight), 'the page does not scroll').toBe(true);
});
