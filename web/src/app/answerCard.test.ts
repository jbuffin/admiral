import { describe, expect, it } from 'vitest';
import type { Item } from './api';
import { answerCard } from './answerCard';
import type { Conversation } from './conversation';

const ok = (...items: Item[]): Conversation => ({ state: 'ok', items, more: false });
const call = (id: string, name: string, summary: string, done = false): Item => ({ id, kind: 'tool_call', tool: { name, summary, done } });
const ask = (id: string, labels: string[], extra: { multiSelect?: boolean; pending?: boolean } = {}): Item => ({
  id,
  kind: 'question',
  question: { text: 'Which one?', options: labels.map((label) => ({ label })), pending: extra.pending ?? true, ...(extra.multiSelect ? { multiSelect: true } : {}) },
});
const needs = (reason: string, conv: Conversation, over: { harness?: string; status?: string } = {}) => answerCard({ harness: 'claude', status: 'needs_you', reason, conv, ...over });

describe('answerCard: permission', () => {
  it('names the tool and shows the oldest pending call of that tool', () => {
    const conv = ok(call('c1', 'Bash', 'ls', true), call('c2', 'Bash', 'rm -rf build'), call('c3', 'Bash', 'git push'), call('c4', 'Edit', 'a.go'));
    expect(needs('permission:Bash', conv)).toMatchObject({ kind: 'permission', tool: 'Bash', reason: 'permission:Bash', call: { id: 'c2' } });
  });

  it('is still a permission card when the conversation has no pending call of the tool', () => {
    const card = needs('permission:Bash', ok(call('c1', 'Edit', 'a.go')));
    expect(card).toMatchObject({ kind: 'permission', tool: 'Bash', reason: 'permission:Bash' });
    expect((card as { call?: Item }).call).toBeUndefined();
  });

  it('is a permission card while the conversation is not recorded yet', () => {
    expect(needs('permission:Write', { state: 'none', items: [], more: false })).toMatchObject({ kind: 'permission', tool: 'Write' });
  });
});

describe('answerCard: question', () => {
  it('gives the newest pending single-choice question one option per label', () => {
    const conv = ok(ask('q1#0', ['Old']), ask('q2#0', ['Red', 'Green', 'Blue']));
    expect(needs('question', conv)).toMatchObject({ kind: 'question', reason: 'question', item: 'q2#0', options: ['Red', 'Green', 'Blue'] });
  });

  it('shows the question card for permission:AskUserQuestion and posts that reason', () => {
    expect(needs('permission:AskUserQuestion', ok(ask('q#0', ['A', 'B'])))).toMatchObject({ kind: 'question', reason: 'permission:AskUserQuestion', item: 'q#0' });
  });

  it('takes one to nine options', () => {
    const nine = Array.from({ length: 9 }, (_, i) => `o${i}`);
    expect(needs('question', ok(ask('q#0', nine))).kind).toBe('question');
    expect(needs('question', ok(ask('q#0', ['only']))).kind).toBe('question');
  });
});

describe('answerCard: terminal', () => {
  it.each([
    ['a multi-select question', ok(ask('q#0', ['A', 'B'], { multiSelect: true }))],
    ['a question with no options', ok(ask('q#0', []))],
    ['a question with more than nine options', ok(ask('q#0', Array.from({ length: 10 }, (_, i) => `o${i}`)))],
    ['a multi-question AskUserQuestion', ok(ask('u#0', ['A', 'B'], { pending: false }), ask('u#1', ['C', 'D']))],
    ['a question reason with no pending question', ok(ask('q#0', ['A', 'B'], { pending: false }))],
    ['a question reason and an empty conversation', ok()],
  ])('sends %s to the terminal', (_, conv) => {
    expect(needs('question', conv).kind).toBe('terminal');
    expect(needs('permission:AskUserQuestion', conv).kind).toBe('terminal');
  });

  it('sends ExitPlanMode to the terminal, never to a permission card', () => {
    expect(needs('permission:ExitPlanMode', ok(call('c1', 'ExitPlanMode', 'plan'))).kind).toBe('terminal');
  });

  it.each(['trust', 'stop', 'bell', 'notification', 'something-new', ''])('sends the needs-you reason %j with no card to the terminal', (reason) => {
    expect(needs(reason, ok(call('c1', 'Bash', 'ls'))).kind).toBe('terminal');
  });
});

describe('answerCard: none', () => {
  const conv = ok(call('c1', 'Bash', 'ls'), ask('q#0', ['A', 'B']));

  it.each(['working', 'idle', 'anchored', 'sailing_off'])('shows nothing for a ship that is %s, whatever its reason', (status) => {
    expect(needs('permission:Bash', conv, { status }).kind).toBe('none');
    expect(needs('question', conv, { status }).kind).toBe('none');
  });

  it('shows nothing when the ship only needs you for its crew', () => {
    expect(needs('crew', conv).kind).toBe('none');
  });

  it.each(['codex', 'copilot'])('shows nothing for a %s harness', (harness) => {
    expect(needs('permission:Bash', conv, { harness }).kind).toBe('none');
    expect(needs('question', conv, { harness }).kind).toBe('none');
  });
});
