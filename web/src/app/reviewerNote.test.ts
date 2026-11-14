import { describe, expect, it } from 'vitest';
import { reviewerNote } from './reviewerNote';

describe('the reviewer note on an event card or proposal', () => {
  it('names the Check, its id and the reason for a block', () => {
    expect(reviewerNote({ verdict: 'block', check_id: '0bbdf84d', check: 'Never touch the release workflow', reviewer_reason: 'the diff edits release.yml' })).toEqual({
      heading: 'Blocked by Check 0bbdf84d',
      check: 'Never touch the release workflow',
      checkId: '0bbdf84d',
      reason: 'the diff edits release.yml',
    });
    // an event the reviewer allowed or was not asked about has no note
    expect(reviewerNote({})).toBeNull();
    expect(reviewerNote({ verdict: 'allow', reviewer_reason: 'fine' })).toBeNull();
  });

  it('names the Check an ask was about, and says the reviewer asked when no Check decided it', () => {
    expect(reviewerNote({ verdict: 'ask', check_id: '0bbdf84d', check: 'Never touch the release workflow', reviewer_reason: 'touches the release path' })).toMatchObject({
      heading: 'Asked about Check 0bbdf84d',
      check: 'Never touch the release workflow',
    });
    expect(reviewerNote({ verdict: 'ask', reviewer_reason: 'reviewer failed: timeout' })).toEqual({
      heading: 'Asked by the reviewer',
      check: '',
      checkId: '',
      reason: 'reviewer failed: timeout',
    });
  });

  it('hands reviewer and Check text back untouched, for the view to render as text', () => {
    const hostile = '<img src=x onerror="window.__pwned=1"> & **bold**';
    const n = reviewerNote({ verdict: 'block', check_id: '0bbdf84d', check: hostile, reviewer_reason: hostile });
    expect(n?.check).toBe(hostile);
    expect(n?.reason).toBe(hostile);
  });
});
