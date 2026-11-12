import { describe, expect, it } from 'vitest';
import type { Item } from './api';
import { questionCallView } from './questionView';

const q = (id: string, over: Partial<NonNullable<Item['question']>> = {}): Item => ({
  id,
  kind: 'question',
  question: {
    header: 'Checks',
    text: 'Which checks run?',
    options: [
      { label: 'Lint', description: 'Static **analysis**.' },
      { label: 'Unit', description: 'Fast tests.' },
      { label: 'E2E' },
    ],
    pending: false,
    ...over,
  },
});

describe('questionCallView', () => {
  it('shows a pending question with its header, text and every option with its description, nothing chosen', () => {
    const [view] = questionCallView([q('c#0', { pending: true })]);
    expect(view).toMatchObject({ id: 'c#0', header: 'Checks', text: 'Which checks run?', pending: true });
    expect(view.options.map((o) => o.label)).toEqual(['Lint', 'Unit', 'E2E']);
    expect(view.options.map((o) => o.description)).toEqual(['Static **analysis**.', 'Fast tests.', '']);
    expect(view.options.some((o) => o.chosen)).toBe(false);
    expect(view.freeText).toBeUndefined();
  });

  it('marks the chosen option of an answered single-choice question', () => {
    const [view] = questionCallView([q('c#0', { answer: 'Unit' })]);
    expect(view.pending).toBe(false);
    expect(view.options.map((o) => o.chosen)).toEqual([false, true, false]);
    expect(view.freeText).toBeUndefined();
  });

  it('marks every label of a multi-select answer joined by commas', () => {
    const [view] = questionCallView([q('c#0', { multiSelect: true, answer: 'Lint, E2E' })]);
    expect(view.multiSelect).toBe(true);
    expect(view.options.map((o) => o.chosen)).toEqual([true, false, true]);
  });

  it('shows an answer that matches no option as the free-text answer', () => {
    const [view] = questionCallView([q('c#0', { answer: 'Something else entirely' })]);
    expect(view.options.some((o) => o.chosen)).toBe(false);
    expect(view.freeText).toBe('Something else entirely');
  });

  it('shows the left-over text of a multi-select answer as free text beside the chosen labels', () => {
    const [view] = questionCallView([q('c#0', { multiSelect: true, answer: 'Unit, my own check' })]);
    expect(view.options.map((o) => o.chosen)).toEqual([false, true, false]);
    expect(view.freeText).toBe('my own check');
  });

  it('keeps each question of a batch with its own header and answer, in order', () => {
    const views = questionCallView([
      q('c#0', { header: 'Database', text: 'Which database?', options: [{ label: 'Postgres' }, { label: 'SQLite' }], answer: 'SQLite' }),
      q('c#1', { multiSelect: true, answer: 'Lint, Unit' }),
    ]);
    expect(views.map((v) => v.header)).toEqual(['Database', 'Checks']);
    expect(views[0].options.map((o) => o.chosen)).toEqual([false, true]);
    expect(views[1].options.map((o) => o.chosen)).toEqual([true, true, false]);
  });

  it('copes with a question that has no header and no options', () => {
    const [view] = questionCallView([q('c#0', { header: undefined, options: undefined, answer: 'typed' })]);
    expect(view.header).toBe('');
    expect(view.options).toEqual([]);
    expect(view.freeText).toBe('typed');
  });
});
