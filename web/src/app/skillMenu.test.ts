import { describe, expect, it } from 'vitest';
import { acceptSkill, matchSkills, skillQuery } from './skillMenu';

const skills = (...names: string[]) => names.map((name) => ({ name, description: '' }));
const names = (list: { name: string }[]) => list.map((s) => s.name);

describe('skillQuery', () => {
  it('reads the first word after the mark while the caret is in it', () => {
    expect(skillQuery('/', 1, '/')).toBe('');
    expect(skillQuery('/td', 3, '/')).toBe('td');
    expect(skillQuery('$rev', 2, '$')).toBe('rev');
  });

  it('is off once the caret leaves the first word, or the box does not start with the harness mark', () => {
    expect(skillQuery('/tdd fix it', 8, '/')).toBeNull();
    expect(skillQuery('/tdd ', 5, '/')).toBeNull();
    expect(skillQuery('fix /tdd', 8, '/')).toBeNull();
    expect(skillQuery('/tdd', 4, '$')).toBeNull();
    expect(skillQuery('/tdd', 0, '/')).toBeNull();
  });
});

describe('matchSkills', () => {
  it('puts names that start with the text first, then plugin skills whose own part does, then names that hold it', () => {
    const list = skills('mattpocock-skills:tdd', 'tdd', 'review', 'pr-review', 'Triage');
    expect(names(matchSkills(list, 're'))).toEqual(['review', 'pr-review']);
    expect(names(matchSkills(list, 'td'))).toEqual(['tdd', 'mattpocock-skills:tdd']);
    expect(names(matchSkills(list, 'tri'))).toEqual(['Triage']);
    expect(names(matchSkills(list, ''))).toEqual(['mattpocock-skills:tdd', 'pr-review', 'review', 'tdd', 'Triage']);
    expect(matchSkills(list, 'zzz')).toEqual([]);
  });
});

describe('acceptSkill', () => {
  it('replaces the typed word with the skill and a space, keeps what followed, and puts the caret after the space', () => {
    expect(acceptSkill('/td', '/', 'tdd')).toEqual({ text: '/tdd ', caret: 5 });
    expect(acceptSkill('$re  fix the bug', '$', 'review')).toEqual({ text: '$review fix the bug', caret: 8 });
  });
});
