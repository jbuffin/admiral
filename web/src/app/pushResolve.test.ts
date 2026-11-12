import { describe, expect, it } from 'vitest';
import { canResolve } from './pushResolve';

describe('when a tapped push may resolve', () => {
  it('resolves at once while the link is open and both lists have arrived on it', () => {
    expect(canResolve({ link: 'open', shipsFresh: true, inboxFresh: true })).toBe(true);
  });

  it('waits for both lists, not one', () => {
    expect(canResolve({ link: 'open', shipsFresh: true, inboxFresh: false })).toBe(false);
    expect(canResolve({ link: 'open', shipsFresh: false, inboxFresh: true })).toBe(false);
  });

  it('waits after a reconnect, until the new connection has delivered both lists', () => {
    expect(canResolve({ link: 'reconnecting', shipsFresh: false, inboxFresh: false })).toBe(false);
    expect(canResolve({ link: 'open', shipsFresh: false, inboxFresh: false })).toBe(false);
  });

  it('does not resolve over a link that is not open, whatever the lists held', () => {
    expect(canResolve({ link: 'unreachable', shipsFresh: true, inboxFresh: true })).toBe(false);
  });
});
