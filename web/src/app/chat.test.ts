import { describe, expect, it, vi } from 'vitest';

// router.ts reads the page's location when it loads; the card's ship link comes from it.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/inbox', search: '' });
  vi.stubGlobal('addEventListener', () => {});
  vi.stubGlobal('history', { replaceState: () => {} });
});


import { commitmentCardView, needsTerminal, resetLabel, threadOf, youAnswered, type ChatSnapshot, type ThreadItem } from './chat';
import { chatSettingsPath, shipPath } from './router';

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

  it('carries a reset entry\'s own text to its marker', () => {
    const t = threadOf(snap({ messages: [{ id: 'reset-2', kind: 'reset', text: 'Context refreshed', at: '2026-01-02T03:04:06Z' }] }));
    expect(t[0]).toMatchObject({ kind: 'reset', text: 'Context refreshed' });
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

  it('shows a question the server removed after its answer as answered, with the answer the log kept', () => {
    const at = '2026-01-02T03:04:05Z';
    const t = threadOf(
      snap({
        messages: [
          { id: 'q-campaign-c1-ab12-1', kind: 'question', text: 'Start it?', at, card: card('campaign-c1-ab12-1') },
          { id: 'a-campaign-c1-ab12-1', kind: 'answer', text: 'Yes, start it with you', at: '2026-01-02T03:04:06Z' },
        ],
        questions: [],
      }),
    );
    expect(t).toHaveLength(1);
    expect(t[0]).toMatchObject({ kind: 'question', state: 'answered', kept: 'Yes, start it with you' });
  });
});

describe('You answered', () => {
  it('names each choice and a typed answer', () => {
    expect(youAnswered({ answers: { '1': { choice: 'Freeform' } } })).toBe('You answered: Freeform');
    expect(youAnswered({ answers: { '1': { choice: ['A', 'B'] }, '2': { other: 'my own' } } })).toBe('You answered: A, B, my own');
  });
});

describe('the reset marker', () => {
  it('reads "<text> · <time>"', () => {
    expect(resetLabel('New chat', '2026-01-02T14:02:00Z')).toMatch(/^New chat · \S+/);
    expect(resetLabel('Context refreshed', '2026-01-02T14:02:00Z')).toMatch(/^Context refreshed · \S+/);
  });
  it('is just the text when the entry has no usable time', () => {
    expect(resetLabel('Context refreshed', undefined)).toBe('Context refreshed');
    expect(resetLabel('New chat', 'not a time')).toBe('New chat');
  });
});

describe('needsTerminal', () => {
  it('is true only for the not-started error, not for an unreadable transcript', () => {
    expect(needsTerminal(snap({ error: 'Open the terminal to finish starting the chat' }))).toBe(true);
    expect(needsTerminal(snap({ error: "Admiral can't read this chat's transcript" }))).toBe(false);
    expect(needsTerminal(snap({}))).toBe(false);
  });
});

describe('a commitment card', () => {
  const entry = (outcome: 'launched' | 'reminded' | 'cant_run' | 'dropped' | 'aged', extra: { ship?: string; reason?: string } = {}) => ({
    id: `commitment-c1-${outcome}`,
    kind: 'commitment' as const,
    text: 'ferry the harbour quartz',
    at: '2026-01-02T03:04:06Z',
    card: { commitment: 'c1', outcome, ...extra },
  });
  const itemOf = (outcome: 'launched' | 'reminded' | 'cant_run' | 'dropped' | 'aged', extra = {}) => {
    const item = threadOf(snap({ messages: [entry(outcome, extra)] })).find((i) => i.kind === 'commitment');
    expect(item, 'threadOf kept no commitment item').toBeDefined();
    return item as Extract<ThreadItem, { kind: 'commitment' }>;
  };

  it('stays in the thread among the messages around it', () => {
    const t = threadOf(
      snap({
        messages: [
          { id: 'u-1', kind: 'message', role: 'user', text: 'before', at: '2026-01-02T03:04:05Z' },
          entry('reminded'),
          { id: 'u-2', kind: 'message', role: 'user', text: 'after', at: '2026-01-02T03:04:07Z' },
        ],
      }),
    );
    expect(t.map((i) => i.id)).toEqual(['u-1', 'commitment-c1-reminded', 'u-2']);
  });

  it('says what happened, with the commitment text, and links a launched ship by its page', () => {
    const v = commitmentCardView(itemOf('launched', { ship: 'saltpaw-2' }));
    expect(v.text).toBe('ferry the harbour quartz');
    expect(v.eyebrow.toLowerCase()).toContain('launched');
    expect(v.shipHref).toBe(shipPath('saltpaw-2'));
  });

  it('shows a reminder and a drop without a ship link, and gives the reason of a refusal', () => {
    expect(commitmentCardView(itemOf('reminded')).eyebrow.toLowerCase()).toContain('remind');
    expect(commitmentCardView(itemOf('reminded')).shipHref).toBe('');
    const cant = commitmentCardView(itemOf('cant_run', { reason: 'no such checkout' }));
    expect(cant.eyebrow.toLowerCase()).toContain("can't");
    expect(cant.reason).toBe('no such checkout');
    expect(commitmentCardView(itemOf('dropped', { reason: 'the ship was dismissed' })).reason).toBe('the ship was dismissed');
  });

  it('keeps an aged card in the thread and gives it an eyebrow of its own that asks whether you still want it', () => {
    const t = threadOf(snap({ messages: [entry('aged')] }));
    expect(t.map((i) => i.id)).toEqual(['commitment-c1-aged']);
    const v = commitmentCardView(itemOf('aged'));
    expect(v.eyebrow, 'an aged card has no eyebrow').toBeTruthy();
    expect(v.eyebrow.toLowerCase()).toContain('still want');
    expect(v.text).toBe('ferry the harbour quartz');
    expect(v.shipHref).toBe('');
    const others = (['launched', 'reminded', 'cant_run', 'dropped'] as const).map((o) => commitmentCardView(itemOf(o)).eyebrow);
    expect(others).not.toContain(v.eyebrow);
  });

  it('points an aged card to the fleet\'s AdmiralBot page, where Cancel is, and no other outcome', () => {
    expect(commitmentCardView(itemOf('aged'), 'harbor').settingsHref).toBe(chatSettingsPath('harbor'));
    for (const o of ['launched', 'reminded', 'cant_run', 'dropped'] as const) expect(commitmentCardView(itemOf(o), 'harbor').settingsHref).toBe('');
  });
});
