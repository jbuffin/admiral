import { describe, expect, it } from 'vitest';
import { discardFold, lockedBy, REMOVED_FOLD, submitFold } from './itemActions';

describe('submitFold', () => {
  it('names the event, or says the review is no longer pending when GitHub reports it gone', () => {
    expect(submitFold({}, 'APPROVE')).toBe('Review submitted: APPROVE');
    expect(submitFold({ action: 'gone' }, 'COMMENT')).toBe('Review no longer pending on GitHub');
  });
});

describe('discardFold', () => {
  it('tells a removed-from-the-inbox discard, a gone review and a deleted draft apart', () => {
    expect(discardFold({ invalid_target: true })).toBe('Review removed from the inbox; nothing changed on GitHub');
    expect(discardFold({ action: 'gone' })).toBe('Review no longer pending on GitHub');
    expect(discardFold({})).toBe('Review discarded');
  });
});

describe('REMOVED_FOLD', () => {
  it('is the confirmation of Remove from inbox', () => {
    expect(REMOVED_FOLD).toBe('Removed from the inbox');
  });
});

describe('lockedBy', () => {
  it('locks the actions only for a 409 that says the item was already answered', () => {
    expect(lockedBy(409, 'inbox item is already answered')).toBe(true);
    expect(lockedBy(409, 'another action on this item is still running')).toBe(false);
    expect(lockedBy(400, 'inbox item is already answered')).toBe(false);
    expect(lockedBy(502, 'gh failed')).toBe(false);
  });
});
