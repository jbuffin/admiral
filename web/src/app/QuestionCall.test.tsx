import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Item } from './api';
import type { AnswerCard } from './answerCard';
import { QuestionCall } from './QuestionCall';

afterEach(() => vi.unstubAllGlobals());

const asked = [
  { id: 'q1', text: 'Which colour?', header: 'Colour', options: ['Red', 'Green', 'Blue'], multiSelect: false },
  { id: 'q2', text: 'Which toppings?', header: 'Toppings', options: ['Cheese', 'Olives', 'Mushrooms'], multiSelect: true },
  { id: 'q3', text: 'Which size?', header: 'Size', options: ['Small', 'Large'], multiSelect: false },
];

const items: Item[] = asked.map((q) => ({
  id: q.id,
  kind: 'question',
  question: { text: q.text, header: q.header, multiSelect: q.multiSelect, pending: true, options: q.options.map((label) => ({ label })) },
}));

const card: AnswerCard = {
  kind: 'question',
  reason: 'permission:AskUserQuestion',
  item: 'q1',
  text: asked[0].text,
  options: asked[0].options,
  descriptions: [],
  questions: asked.map((q) => ({ id: q.id, header: q.header, text: q.text, multiSelect: q.multiSelect, options: q.options, descriptions: q.options.map(() => ''), other: true })),
};

test('a three-question call is stepped through, reviewed, and answered with one request', async () => {
  const posts: { url: string; body: unknown }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      posts.push({ url, body: JSON.parse(init.body as string) });
      return new Response('{}');
    }),
  );
  render(<QuestionCall items={items} card={card} session="s1" openTerminal={() => undefined} />);
  const next = () => fireEvent.click(screen.getByRole('button', { name: 'Next' }));

  expect(screen.getByText('Question 1 of 3')).toBeTruthy();
  expect((screen.getByRole('button', { name: 'Next' }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole('radio', { name: /^Green/ }));
  next();
  fireEvent.click(screen.getByRole('checkbox', { name: /^Cheese/ }));
  fireEvent.click(screen.getByRole('checkbox', { name: /^Mushrooms/ }));
  next();
  fireEvent.input(screen.getByLabelText('Other answer'), { target: { value: 'Medium, thanks' } });
  next();

  expect(screen.getByText('Review your answers')).toBeTruthy();
  const review = within(screen.getByRole('list'));
  expect(review.getByText('→ Green')).toBeTruthy();
  expect(review.getByText('→ Cheese, Mushrooms')).toBeTruthy();
  expect(review.getByText('→ Medium, thanks')).toBeTruthy();
  expect(posts).toEqual([]);

  fireEvent.click(screen.getByRole('button', { name: 'Submit answers' }));
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].url).toBe('/api/v1/ships/s1/answer');
  expect(posts[0].body).toEqual({
    card: 'question',
    reason: 'permission:AskUserQuestion',
    item: 'q1',
    answers: [{ options: [2] }, { options: [1, 3] }, { options: [], other: 'Medium, thanks' }],
  });
});

interface Asked {
  header: string;
  text: string;
  options: string[];
  descriptions?: string[];
  multi?: boolean;
  answer?: string;
}

const callOf = (asked: Asked[], pending = true): Item[] =>
  asked.map((q, i) => ({
    id: `c#${i}`,
    kind: 'question',
    question: { text: q.text, header: q.header, multiSelect: q.multi ?? false, pending, ...(q.answer === undefined ? {} : { answer: q.answer }), options: q.options.map((label, n) => ({ label, description: q.descriptions?.[n] ?? '' })) },
  }));

const cardOf = (asked: Asked[], reason = 'permission:AskUserQuestion'): AnswerCard => ({
  kind: 'question',
  reason,
  item: 'c#0',
  text: asked[0].text,
  options: asked[0].options,
  descriptions: asked[0].descriptions ?? [],
  questions: asked.map((q, i) => ({ id: `c#${i}`, header: q.header, text: q.text, multiSelect: q.multi ?? false, options: q.options, descriptions: q.descriptions ?? q.options.map(() => ''), other: true })),
});

const four: Asked[] = [
  { header: 'Colour', text: 'Which colour?', options: ['Red', 'Green', 'Blue'], descriptions: ['A **bold** colour', 'A fresh colour', 'A calm colour'] },
  { header: 'Toppings', text: 'Which toppings?', options: ['Cheese', 'Olives', 'Mushrooms'], multi: true, descriptions: ['Use `melted`', 'Briny', 'Earthy'] },
  { header: 'Size', text: 'Which size?', options: ['Small', 'Large'], descriptions: ['One person', 'To share'] },
  { header: 'Delivery', text: 'How to deliver?', options: ['Pickup', 'Courier'], descriptions: ['Collect it', 'To the door'] },
];

// A button's pending key outlives its test for a moment, so each test names its own session.
const stubPosts = (reply: () => Response = () => new Response('{}')) => {
  const posts: { url: string; body: unknown }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      posts.push({ url, body: JSON.parse(init.body as string) });
      return reply();
    }),
  );
  return posts;
};

test('a four-question call renders descriptions as Markdown, goes Back from the review to the last question, and sends every answer in one request', async () => {
  const posts = stubPosts();
  render(<QuestionCall items={callOf(four)} card={cardOf(four)} session="four" openTerminal={() => undefined} />);
  const go = async (button: string, screenText: string) => {
    fireEvent.click(screen.getByRole('button', { name: button }));
    await screen.findByText(screenText);
  };

  expect(screen.getByText('Question 1 of 4')).toBeTruthy();
  expect(screen.getByRole('radio', { name: /^Red/ }).querySelector('strong')?.textContent).toBe('bold');
  fireEvent.click(screen.getByRole('radio', { name: /^Green/ }));
  await go('Next', 'Question 2 of 4');
  expect(screen.getByRole('checkbox', { name: /^Cheese/ }).querySelector('code')?.textContent).toBe('melted');
  fireEvent.click(screen.getByRole('checkbox', { name: /^Cheese/ }));
  fireEvent.click(screen.getByRole('checkbox', { name: /^Mushrooms/ }));
  await go('Next', 'Question 3 of 4');
  fireEvent.input(screen.getByLabelText('Other answer'), { target: { value: 'Medium, thanks' } });
  await go('Next', 'Question 4 of 4');
  fireEvent.click(screen.getByRole('radio', { name: /^Courier/ }));
  await go('Next', 'Review your answers');

  expect(Array.from(document.querySelectorAll('.q-review-a')).map((e) => e.textContent)).toEqual(['→ Green', '→ Cheese, Mushrooms', '→ Medium, thanks', '→ Courier']);
  expect(posts).toEqual([]);
  await go('Back', 'Question 4 of 4');
  expect(screen.getByRole('radio', { name: /^Courier/ }).getAttribute('aria-checked')).toBe('true');
  await go('Next', 'Review your answers');

  fireEvent.click(screen.getByRole('button', { name: 'Submit answers' }));
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].body).toEqual({ card: 'question', reason: 'permission:AskUserQuestion', item: 'c#0', answers: [{ options: [2] }, { options: [1, 3] }, { options: [], other: 'Medium, thanks' }, { options: [2] }] });
});

test('one single-choice question answers on the tap of an option, with its number', async () => {
  const posts = stubPosts();
  const one: Asked[] = [{ header: 'Q', text: 'Which colour?', options: ['Red', 'Green', 'Blue'] }];
  render(<QuestionCall items={callOf(one)} card={cardOf(one, 'question')} session="one" openTerminal={() => undefined} />);

  expect(screen.getByRole('button', { name: 'Red' })).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Green' }));
  fireEvent.click(screen.getByRole('button', { name: 'Green' }));
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0].body).toEqual({ card: 'question', reason: 'question', item: 'c#0', option: 2 });
  expect((screen.getByRole('button', { name: 'Blue' }) as HTMLButtonElement).disabled).toBe(true);
});

test('a refused answer leaves the questions in place with the reason and an Open terminal button', async () => {
  const posts = stubPosts(() => new Response('{"error":"the menu is not on this call: answer it in the terminal"}', { status: 409 }));
  const open = vi.fn();
  const two = four.slice(0, 1).concat(four.slice(2, 3));
  render(<QuestionCall items={callOf(two)} card={cardOf(two)} session="refused" openTerminal={open} />);

  fireEvent.click(screen.getByRole('radio', { name: /^Red/ }));
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  fireEvent.click(await screen.findByRole('radio', { name: /^Large/ }));
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Submit answers' }));

  expect((await screen.findByRole('alert')).textContent).toContain('terminal');
  expect(screen.getByText('Which colour?')).toBeTruthy();
  expect(screen.getByText('Which size?')).toBeTruthy();
  expect(posts).toHaveLength(1);
  fireEvent.click(screen.getByRole('button', { name: 'Open terminal' }));
  expect(open).toHaveBeenCalledOnce();
});

test('an answered call is the read-only record: chips, rendered descriptions, chosen marks and the free text', () => {
  const answered = four.map((q, i) => ({ ...q, answer: ['Green', 'Cheese, Mushrooms', 'Medium, thanks', 'Courier'][i] }));
  render(<QuestionCall items={callOf(answered, false)} card={{ kind: 'none' }} session="record" openTerminal={() => undefined} />);

  expect(screen.queryByRole('button', { name: 'Next' })).toBeNull();
  expect(Array.from(document.querySelectorAll('.q-chip')).map((e) => e.textContent)).toEqual(['Colour', 'Toppings', 'Size', 'Delivery']);
  expect(document.querySelector('.q-desc strong')?.textContent).toBe('bold');
  expect(document.querySelector('.q-desc code')?.textContent).toBe('melted');
  const chosen = screen.getAllByRole('listitem').filter((li) => li.textContent?.includes('(chosen)'));
  expect(chosen.map((li) => li.querySelector('strong')?.textContent)).toEqual(['Green', 'Cheese', 'Mushrooms', 'Courier']);
  expect(screen.getByText('Other: Medium, thanks')).toBeTruthy();
});
