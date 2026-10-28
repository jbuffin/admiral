import { describe, expect, it } from 'vitest';
import { type Order, matches } from './sources';

const pr: Order = { id: '#7', title: 'Fix login', text: 'https://github.com/o/r/pull/7', url: 'https://github.com/o/r/pull/7' };
const note: Order = { id: 'a1', title: 'Review the release', text: 'Review the release\nSee https://github.com/o/r/issues/12.' };
const plain: Order = { id: 'b2', title: 'Tidy the docs', text: 'Tidy the docs', labels: ['Needs-Design'] };
const filtered = (q: string) => [pr, note, plain].filter((o) => matches(o, q.trim().toLowerCase())).map((o) => o.id);

describe('filter by a pasted web address', () => {
  it('matches the GitHub link, and a page under it, whatever the scheme, case or fragment', () => {
    expect(filtered('https://github.com/o/r/pull/7')).toEqual(['#7']);
    expect(filtered('http://www.GitHub.com/o/r/pull/7/files#diff-1')).toEqual(['#7']);
    expect(filtered('https://github.com/o/r/pull/70')).toEqual([]);
  });

  it('matches a web address anywhere in a freeform order', () => {
    expect(filtered('https://github.com/o/r/issues/12')).toEqual(['a1']);
  });

  it('a repository address matches every order under it', () => {
    expect(filtered('https://github.com/o/r/')).toEqual(['#7', 'a1']);
  });

  it('anything else still matches the ID, title or a label', () => {
    expect(filtered('tidy')).toEqual(['b2']);
    expect(filtered('needs-design')).toEqual(['b2']);
    expect(filtered('github.com')).toEqual([]);
  });
});
