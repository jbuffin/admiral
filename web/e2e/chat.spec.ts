import type { Locator, Page, TestInfo } from '@playwright/test';
import { Chat, askBody } from './chat';
import { expect, test } from './fixtures';
import { screenText } from './bridge';

// The AdmiralBot chat at the middle and wide sizes (issue #119, u6). The phone tab is chat-phone.spec.ts.
// Names it is built to, from the prototype (designs/prototype/index.html):
//
//   entry: button "Open the chat with AdmiralBot" ("Close the chat with AdmiralBot" while open), plus ", new reply"
//     while the dot shows. Wide: the round button at the bottom right. Middle: the header's Chat button.
//   panel (wide) / sheet (middle): dialog "Chat with AdmiralBot"; the sheet is aria-modal. Button "Close the chat".
//   box: textbox "Message AdmiralBot", button "Send message"; the typing dots are a status "AdmiralBot is typing"
//   actions: "New chat", "Open terminal", "Chat settings" (icon-only on wide). New chat shows
//     "Start a new chat? This clears the conversation." with "Start new chat" and "Keep chatting"
//   terminal view: "Chat session", button "Back to chat"
//   a chat question is a card (its question text and an option per radio or checkbox); a one-tap answer folds it
//     to "Answered: <label>"; a multi-choice one has "Send answers"

const sizeOf = (info: TestInfo) => (info.project.name === 'ipad-portrait' ? 'middle' : 'wide');
const touch = (info: TestInfo) => !!info.project.use.hasTouch;
const press = (l: Locator, info: TestInfo) => (touch(info) ? l.tap() : l.click());

const entry = (page: Page) => page.getByRole('button', { name: /^(Open|Close) the chat with AdmiralBot/ });
const openName = 'Open the chat with AdmiralBot';
const panel = (page: Page) => page.getByRole('dialog', { name: 'Chat with AdmiralBot', exact: true });
const box = (page: Page) => panel(page).getByRole('textbox', { name: 'Message AdmiralBot' });
const startRequest = (page: Page) => page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/chat/start'));
const inChat = (page: Page) => panel(page);
const card = (page: Page) => panel(page).getByText('Which panel?');

async function openChat(page: Page, info: TestInfo) {
  await press(page.getByRole('button', { name: openName, exact: true }), info);
  await expect(panel(page)).toBeVisible();
}

test.describe('where it opens', () => {
  test('C15: the entry opens the chat as a sheet (middle) or a panel (wide) and starts the session', async ({ page, admiral }, info) => {
    const chat = new Chat(admiral);
    await admiral.start();
    await page.goto(admiral.url);
    const started = startRequest(page);
    await openChat(page, info);
    await started;
    await expect(box(page)).toBeVisible();
    await expect.poll(async () => (await chat.session()).running).toBe(true);
    if (sizeOf(info) === 'middle') {
      await expect(panel(page)).toHaveAttribute('aria-modal', 'true');
    } else {
      // With the inbox drawer open, the panel sits left of it.
      await press(panel(page).getByRole('button', { name: 'Close the chat' }), info);
      await expect(panel(page)).toBeHidden();
      await press(page.getByRole('button', { name: /^Needs you/ }), info);
      const drawer = page.getByRole('complementary', { name: 'Needs you inbox' });
      await expect(drawer).toBeVisible();
      await openChat(page, info);
      const d = (await drawer.boundingBox())!;
      const p = (await panel(page).boundingBox())!;
      expect(p.x + p.width, 'the panel ends left of the inbox drawer').toBeLessThanOrEqual(d.x);
    }
  });

  test('C15: resizing from wide to middle with the chat and the inbox open keeps the chat and closes the inbox', async ({ page, admiral }, info) => {
    test.skip(info.project.name !== 'desktop', 'resizes from wide; the desktop project is the one that can');
    const chat = new Chat(admiral);
    await admiral.start();
    await page.goto(admiral.url);
    await press(page.getByRole('button', { name: /^Needs you/ }), info);
    await expect(page.getByRole('complementary', { name: 'Needs you inbox' })).toBeVisible();
    await openChat(page, info);
    await page.setViewportSize({ width: 834, height: 1194 });
    await expect(panel(page)).toBeVisible();
    await expect(page.getByRole('dialog', { name: 'Needs you inbox' })).toBeHidden();
    await expect.poll(async () => (await chat.session()).running).toBe(true);
  });

  test('C15: the dot shows on the entry for a reply that lands while the chat is closed, and goes when it opens', async ({ page, admiral }, info) => {
    const chat = new Chat(admiral);
    await admiral.start();
    await chat.beginWith([{ user: 'Where is the fleet?' }, { assistant: 'Safe in the harbour.' }]);
    await page.goto(admiral.url);
    await expect(entry(page)).toHaveAccessibleName(openName);
    chat.assistant('a-late', 'A second reply.');
    await expect(entry(page)).toHaveAccessibleName(`${openName}, new reply`);
    await openChat(page, info);
    await expect(panel(page).getByText('A second reply.')).toBeVisible();
    await press(panel(page).getByRole('button', { name: 'Close the chat' }), info);
    await expect(entry(page)).toHaveAccessibleName(openName);
  });
});

test.describe('the conversation', () => {
  test('C10: Shift+Enter then Enter sends one message with the line break, the draft clears, replies show in order, working shows the typing dots', async ({ page, admiral }, info) => {
    const chat = new Chat(admiral, ['wait go-work', 'event UserPromptSubmit {"prompt":"hi"}', 'wait go-stop', 'event Stop {"background_tasks":[]}']);
    await admiral.start();
    await chat.begin();
    await page.goto(admiral.url);
    await openChat(page, info);

    await box(page).click();
    await page.keyboard.type('line one');
    await page.keyboard.press('Shift+Enter');
    await page.keyboard.type('line two');
    await page.keyboard.press('Enter');
    await expect.poll(() => chat.stdin()).toBe('\x1b[200~line one\nline two\x1b[201~\r');
    await expect(box(page)).toHaveValue('');

    // The server serves the transcript, not the page's own echo.
    chat.user('u-1', 'line one\nline two');
    chat.assistant('a-1', 'Got both lines.');
    const mine = panel(page).getByText('Got both lines.');
    await expect(mine).toBeVisible();
    const sent = panel(page).getByText('line one', { exact: false });
    const [a, b] = [(await sent.boundingBox())!, (await mine.boundingBox())!];
    expect(a.y, 'the engineer\'s message is above the reply').toBeLessThan(b.y);

    chat.release('go-work');
    await expect(panel(page).getByRole('status', { name: 'AdmiralBot is typing' })).toBeVisible();
    chat.release('go-stop');
    await expect(panel(page).getByRole('status', { name: 'AdmiralBot is typing' })).toBeHidden();
  });

  test('before the first SessionStart the notice shows, and a send keeps the draft and shows the server error', async ({ page, admiral }, info) => {
    const chat = new Chat(admiral);
    await admiral.start();
    await chat.start();
    await page.goto(admiral.url);
    await openChat(page, info);
    await expect(inChat(page).getByText('Open the terminal to finish starting the chat').first()).toBeVisible();
    await box(page).fill('too early');
    const refused = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith('/api/chat/messages'));
    await page.keyboard.press('Enter');
    expect((await refused).status()).toBe(409);
    await expect(inChat(page).getByText('Open the terminal to finish starting the chat').first()).toBeVisible();
    await expect(box(page)).toHaveValue('too early');
    expect(chat.stdin()).toBe('');
  });
});

test.describe('chat questions', () => {
  test('C16: one tap on a single-choice option answers it and folds the card here and in a second browser; the inbox is untouched', async ({ page, admiral, browser }, info) => {
    const chat = new Chat(admiral);
    await admiral.start();
    await chat.begin();
    const before = await chat.inbox();
    const ask = chat.ask(askBody('Where does it go?'));
    try {
      await expect.poll(async () => (await chat.session()).questions.length).toBe(1);
      await page.goto(admiral.url);
      await openChat(page, info);
      await expect(card(page)).toBeVisible();
      expect(await chat.inbox(), 'a chat question adds nothing to the inbox').toBe(before);

      await press(panel(page).getByRole('radio', { name: /^Freeform/ }), info);
      await expect(panel(page).getByText('Answered: Freeform')).toBeVisible();
      expect(await ask.exited).toBe(0);
      expect(ask.out()).toContain('Freeform');
      expect(await chat.inbox()).toBe(before);

      const second = await browser.newContext({ viewport: page.viewportSize()!, hasTouch: touch(info), isMobile: !!info.project.use.isMobile });
      try {
        const other = await second.newPage();
        await other.goto(admiral.url);
        await openChat(other, info);
        await expect(panel(other).getByText('Answered: Freeform')).toBeVisible();
      } finally {
        await second.close();
      }
    } finally {
      chat.stop();
    }
  });

  test('C16: a multi-choice question needs Send answers', async ({ page, admiral }, info) => {
    const chat = new Chat(admiral);
    await admiral.start();
    await chat.begin();
    chat.writeQuestion('multi-1', askBody('Pick some', { multi: true }));
    await expect.poll(async () => (await chat.session()).questions.length).toBe(1);
    await page.goto(admiral.url);
    await openChat(page, info);
    await press(panel(page).getByRole('checkbox', { name: /^Freeform/ }), info);
    expect((await chat.session()).questions[0].answer, 'a tap on a multi-choice option does not send').toBeNull();
    await press(panel(page).getByRole('button', { name: 'Send answers' }), info);
    await expect(panel(page).getByText('Answered: Freeform')).toBeVisible();
    expect((await chat.session()).questions[0].answer).not.toBeNull();
  });
});

test.describe('New chat, the terminal and closing', () => {
  test('C10: New chat asks first; Keep chatting leaves the thread; Start new chat empties it and drops a waiting card', async ({ page, admiral }, info) => {
    const chat = new Chat(admiral, ['wait clear', 'event SessionStart {"source":"clear","transcript_path":"{transcript}"}']);
    await admiral.start();
    await chat.beginWith([{ user: 'Hello bot' }, { assistant: 'Hello engineer.' }]);
    chat.writeQuestion('wait-1', askBody('Still open'));
    await expect.poll(async () => (await chat.session()).questions.length).toBe(1);
    await page.goto(admiral.url);
    await openChat(page, info);
    await expect(card(page)).toBeVisible();

    await press(panel(page).getByRole('button', { name: 'New chat' }), info);
    await expect(panel(page).getByText('Start a new chat? This clears the conversation.')).toBeVisible();
    await press(panel(page).getByRole('button', { name: 'Keep chatting' }), info);
    await expect(panel(page).getByText('Start a new chat?')).toBeHidden();
    await expect(panel(page).getByText('Hello engineer.')).toBeVisible();

    await press(panel(page).getByRole('button', { name: 'New chat' }), info);
    await press(panel(page).getByRole('button', { name: 'Start new chat' }), info);
    await expect.poll(() => chat.stdin()).toContain('/clear');
    chat.release('clear');
    await expect(panel(page).getByText('Hello engineer.')).toBeHidden();
    await expect(card(page)).toBeHidden();
  });

  test('C11, C17: Open terminal shows the chat session and its output; Escape typed there does not close the chat; Back to chat returns', async ({ page, admiral }, info) => {
    const chat = new Chat(admiral, ['lines 6']);
    await admiral.start();
    await chat.begin();
    await page.goto(admiral.url);
    await openChat(page, info);
    await press(panel(page).getByRole('button', { name: 'Open terminal' }), info);
    await expect(panel(page).getByText('Chat session')).toBeVisible();
    await expect.poll(() => screenText(page), { message: 'the fake\'s output shows in the terminal' }).toContain('line 0006');

    await panel(page).locator('.xterm textarea').focus();
    await page.keyboard.press('Escape');
    await expect(panel(page)).toBeVisible();
    await expect.poll(() => chat.stdin(), { message: 'Escape reached Claude' }).toContain('\x1b');

    await press(panel(page).getByRole('button', { name: 'Back to chat' }), info);
    await expect(box(page)).toBeVisible();
  });

  test('C17: closing returns focus to the entry, and Escape closes Settings first, then the chat', async ({ page, admiral }, info) => {
    const chat = new Chat(admiral);
    await admiral.start();
    await chat.begin();
    await page.goto(admiral.url);
    const opener = page.getByRole('button', { name: openName, exact: true });

    // Wide closes on Escape, middle on the close button; each puts focus back on its entry.
    await press(opener, info);
    await expect(panel(page)).toBeVisible();
    if (sizeOf(info) === 'wide') await page.keyboard.press('Escape');
    else await press(panel(page).getByRole('button', { name: 'Close the chat' }), info);
    await expect(panel(page)).toBeHidden();
    await expect(entry(page)).toBeFocused();

    // Settings opened from Chat settings closes first and gives focus back to that button.
    await openChat(page, info);
    const settings = panel(page).getByRole('button', { name: 'Chat settings' });
    await press(settings, info);
    await expect(page.getByRole('dialog', { name: 'Settings' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog', { name: 'Settings' })).toBeHidden();
    await expect(panel(page)).toBeVisible();
    await expect(settings).toBeFocused();
  });

  test('C18: on wide the actions are icon-only with names and a title only on a fine pointer; on middle they show their labels', async ({ page, admiral }, info) => {
    const chat = new Chat(admiral);
    await admiral.start();
    await chat.begin();
    await page.goto(admiral.url);
    await openChat(page, info);
    for (const name of ['New chat', 'Open terminal', 'Chat settings']) {
      const b = panel(page).getByRole('button', { name, exact: true });
      await expect(b).toBeVisible();
      if (sizeOf(info) === 'wide') {
        await expect(b).toHaveText('');
        if (touch(info)) expect(await b.getAttribute('title'), `${name} has no title on touch`).toBeNull();
        else expect(await b.getAttribute('title')).toBeTruthy();
      } else {
        await expect(b).toContainText(name);
      }
    }
  });
});
