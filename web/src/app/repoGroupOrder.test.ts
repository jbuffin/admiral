import { describe, expect, it } from 'vitest';
import { parseRepoOrder } from './repoGroupOrder';

describe('parseRepoOrder', () => {
  it('reads an array of strings in order', () => {
    expect(parseRepoOrder(JSON.stringify(['b/small', 'acme/widgets']))).toEqual(['b/small', 'acme/widgets']);
  });

  it('starts empty for nothing, corrupt JSON, a non-array or non-string entries', () => {
    for (const raw of [null, '', 'not json', '{"a":1}', '"a"', '7', 'null', '[1]', '[null]', '["a",1]', '[["a"]]']) expect(parseRepoOrder(raw)).toEqual([]);
  });
});
