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
