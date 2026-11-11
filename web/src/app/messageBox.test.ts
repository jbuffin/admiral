import { describe, expect, it } from 'vitest';
import type { Item } from './api';
import { boxState, sentBubbleShows, userItemCount } from './messageBox';

const exit = (status: 'needs_you' | 'underway' | 'anchored' | 'sailing_off', exit_code: number | null = null, exit_signal: string | null = null) => ({ status, exit_code, exit_signal });
const user = (id: string, text: string): Item => ({ id, kind: 'user', text });
const assistant = (id: string): Item => ({ id, kind: 'assistant', text: 'ok' });

describe('boxState', () => {
  it('accepts a message while the ship works and while it waits for you', () => {
    expect(boxState(exit('underway')).enabled).toBe(true);
    expect(boxState(exit('needs_you')).enabled).toBe(true);
  });

  it('is read only on a ship that exited with an error', () => {
    expect(boxState(exit('anchored', 1))).toEqual({ enabled: false, note: 'Exited with error, read only' });
  });

  it('names the signal when the harness was killed', () => {
    const killed = boxState(exit('anchored', null, 'KILL'));
    expect(killed.enabled).toBe(false);
    expect(killed.note).toBe('Killed (KILL), read only');
  });

  it('is disabled while the ship sails off', () => {
    expect(boxState(exit('sailing_off', 0)).enabled).toBe(false);
  });
});

describe('sentBubbleShows', () => {
  const sent = { text: 'rebase please', userItemsAtSend: 1 };

  it('shows while the request is in flight', () => {
    expect(sentBubbleShows(sent, [user('u1', 'hi')], { hasConversation: true, answered: false })).toBe(true);
  });

  it('stays until a user item arrives, whatever its text', () => {
    const items = [user('u1', 'hi'), assistant('a1')];
    expect(sentBubbleShows(sent, items, { hasConversation: true, answered: true })).toBe(true);
    expect(sentBubbleShows(sent, [...items, user('u2', 'rebase please\n')], { hasConversation: true, answered: true })).toBe(false);
    expect(sentBubbleShows(sent, [...items, user('u2', 'something the harness recorded differently')], { hasConversation: true, answered: true })).toBe(false);
  });

  it('goes on the answer when the ship has no conversation', () => {
    expect(sentBubbleShows(sent, [], { hasConversation: false, answered: false })).toBe(true);
    expect(sentBubbleShows(sent, [], { hasConversation: false, answered: true })).toBe(false);
  });
});

describe('userItemCount', () => {
  it('counts only what the person said', () => {
    expect(userItemCount([user('u1', 'a'), assistant('a1'), user('u2', 'b')])).toBe(2);
  });
});
