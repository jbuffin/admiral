import { describe, expect, it } from 'vitest';
import { parseCollapsed } from './collapsedRepoGroups';

describe('parseCollapsed', () => {
  it('reads an array of strings as a set', () => {
    expect(parseCollapsed(JSON.stringify(['a', 'b']))).toEqual(new Set(['a', 'b']));
  });

  it('starts empty for nothing, corrupt JSON, a non-array or non-string entries', () => {
    for (const raw of [null, '', 'not json', '{"a":1}', '"a"', '7', 'null', '[1]', '[null]', '["a",1]', '[["a"]]']) expect(parseCollapsed(raw)).toEqual(new Set());
  });
});
