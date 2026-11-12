import { describe, expect, it } from 'vitest';
import { needsTerminal, resetLabel, threadOf, youAnswered, type ChatSnapshot } from './chat';

const card = (id: string) => ({ version: 1, id, session: '_chat-harbor', kind: 'question' as const, created: '2026-01-02T03:04:05Z', title: 'Which panel?', question: { questions: [] } });
const snap = (over: Partial<ChatSnapshot>): ChatSnapshot => ({ fleet: 'harbor', running: true, state: 'idle', messages: [], questions: [], ...over });

describe('the thread', () => {
  it('lists messages oldest first, whatever order they arrive in', () => {
    const t = threadOf(
      snap({
        messages: [
          { id: 'a-2', kind: 'message', role: 'assistant', text: 'second', at: '2026-01-02T03:04:07Z' },
          { id: 'u-1', kind: 'message', role: 'user', text: 'first', at: '2026-01-02T03:04:05Z' },
        ],
      }),
    );
    expect(t.map((i) => i.id)).toEqual(['u-1', 'a-2']);
  });

  it('keeps the served order for entries with one time', () => {
    const at = '2026-01-02T03:04:05Z';
    const t = threadOf(
      snap({
        messages: [
          { id: 'u-1', kind: 'message', role: 'user', text: 'a', at },
          { id: 'a-1', kind: 'message', role: 'assistant', text: 'b', at },
        ],
      }),
    );
    expect(t.map((i) => i.id)).toEqual(['u-1', 'a-1']);
  });

  it('shows a reset entry as a marker between the messages around it', () => {
    const t = threadOf(
      snap({
        messages: [
          { id: 'u-1', kind: 'message', role: 'user', text: 'before', at: '2026-01-02T03:04:05Z' },
          { id: 'reset-1', kind: 'reset', text: 'New chat', at: '2026-01-02T03:04:06Z' },
          { id: 'u-2', kind: 'message', role: 'user', text: 'after', at: '2026-01-02T03:04:07Z' },
        ],
      }),
    );
    expect(t.map((i) => i.kind)).toEqual(['message', 'reset', 'message']);
  });

  it('shows a question as open while its answer is null, answered once it has one, and withdrawn when the fleet no longer lists it', () => {
    const at = '2026-01-02T03:04:05Z';
    const t = threadOf(
      snap({
        messages: [
          { id: 'q-open', kind: 'question', text: 'Which panel?', at, card: card('open') },
          { id: 'q-done', kind: 'question', text: 'Which panel?', at: '2026-01-02T03:04:06Z', card: card('done') },
          { id: 'q-gone', kind: 'question', text: 'Which panel?', at: '2026-01-02T03:04:07Z', card: card('gone') },
        ],
        questions: [
          { ...card('open'), answer: null },
          { ...card('done'), answer: { answers: { '1': { choice: 'Freeform' } } } },
        ],
      }),
    );
    expect(t.map((i) => (i.kind === 'question' ? i.state : i.kind))).toEqual(['open', 'answered', 'withdrawn']);
  });
});

describe('You answered', () => {
  it('names each choice and a typed answer', () => {
    expect(youAnswered({ answers: { '1': { choice: 'Freeform' } } })).toBe('You answered: Freeform');
    expect(youAnswered({ answers: { '1': { choice: ['A', 'B'] }, '2': { other: 'my own' } } })).toBe('You answered: A, B, my own');
  });
});

describe('the New chat marker', () => {
  it('reads "New chat · <time>"', () => {
    expect(resetLabel('2026-01-02T14:02:00Z')).toMatch(/^New chat · \S+/);
  });
  it('is just "New chat" when the entry has no usable time', () => {
    expect(resetLabel(undefined)).toBe('New chat');
    expect(resetLabel('not a time')).toBe('New chat');
  });
});

describe('needsTerminal', () => {
  it('is true only for the not-started error, not for an unreadable transcript', () => {
    expect(needsTerminal(snap({ error: 'Open the terminal to finish starting the chat' }))).toBe(true);
    expect(needsTerminal(snap({ error: "Admiral can't read this chat's transcript" }))).toBe(false);
    expect(needsTerminal(snap({}))).toBe(false);
  });
});
