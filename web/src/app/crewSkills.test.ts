import { describe, expect, it } from 'vitest';
import type { CrewMember } from './crew';
import { filterSkills, isChosen, pickSkill, rankSkills, skillWarning, typeSkill, type SkillEntry } from './crewSkills';

const entry = (name: string, description = '', harnesses = ['claude']): SkillEntry => ({ name, description, harnesses });
const names = (l: SkillEntry[]) => l.map((s) => s.name);

const crewMember = (name: string, role: string, skills: string[]): CrewMember => ({
  name,
  role,
  'based-on': '',
  skills,
  model: '',
  harness: '',
  'pick-me-when': `Pick ${name}.`,
  instructions: '',
  layer: 'fleet',
  customized: false,
  notes: { fleet: [], repos: {} },
});

describe('rankSkills', () => {
  const installed = [
    entry('aaa'),
    entry('bbb'),
    entry('word-match', 'A careful pass over diffs.'),
    entry('role-match', 'Acts as a reviewer on pull requests.'),
    entry('peer-skill'),
    entry('other-role-skill'),
    entry('zz-chosen'),
    entry('self-skill'),
  ];
  const ctx = {
    role: 'reviewer',
    pickMeWhen: 'Pick for diffs that need a second look.',
    chosen: ['zz-chosen'],
    self: 'lookout',
    members: [crewMember('lookout', 'reviewer', ['self-skill']), crewMember('mate', 'reviewer', ['peer-skill']), crewMember('ada', 'implementer', ['other-role-skill'])],
  };

  it('puts the current skills, then skills of the same role in other definitions, then description matches, then the rest A to Z', () => {
    expect(names(rankSkills(installed, ctx))).toEqual(['zz-chosen', 'peer-skill', 'role-match', 'word-match', 'aaa', 'bbb', 'other-role-skill', 'self-skill']);
  });
  it('lists the current skills in the order they were chosen', () => {
    expect(names(rankSkills(installed, { ...ctx, chosen: ['zz-chosen', 'aaa'] })).slice(0, 2)).toEqual(['zz-chosen', 'aaa']);
  });
  it('keeps every installed skill once', () => {
    expect(names(rankSkills(installed, ctx)).sort()).toEqual(names(installed).sort());
  });
});

describe('filterSkills', () => {
  it('keeps the ranked order of what matches, not the order a name match would give', () => {
    const ranked = [entry('zz-tdd'), entry('aaa'), entry('tdd'), entry('Test-Driven', 'Write TDD first.')];
    expect(names(filterSkills(ranked, 'tdd'))).toEqual(['zz-tdd', 'tdd', 'Test-Driven']);
  });
  it('shows everything for nothing typed', () => {
    const ranked = [entry('b'), entry('a')];
    expect(names(filterSkills(ranked, ''))).toEqual(['b', 'a']);
  });
});

describe('pickSkill', () => {
  it('appends the skill after the ones chosen', () => {
    expect(pickSkill(['tdd'], 'unslop')).toEqual(['tdd', 'unslop']);
  });
  it('does nothing for a skill already chosen', () => {
    expect(pickSkill(['tdd', 'unslop'], 'tdd')).toEqual(['tdd', 'unslop']);
  });
});

describe('typeSkill', () => {
  it('adds a name that is not installed when a saved member would accept it', () => {
    expect(typeSkill(['tdd'], 'my-repo-skill')).toEqual({ ok: true, skills: ['tdd', 'my-repo-skill'] });
    expect(typeSkill([], 'plugin:name.v2_x')).toEqual({ ok: true, skills: ['plugin:name.v2_x'] });
  });
  it.each(['not a skill', '/tdd', '$tdd', '-dash', '_under', ''])('refuses %j and says why', (typed) => {
    const r = typeSkill(['tdd'], typed);
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.reason).not.toBe('');
  });
  it('refuses a name already chosen and says why', () => {
    const r = typeSkill(['tdd', 'unslop'], 'tdd');
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.reason).not.toBe('');
  });
});

describe('a skill saved under a plugin-qualified name', () => {
  const tdd: SkillEntry = { name: 'tdd', description: '', harnesses: ['claude'], aliases: ['mattpocock-skills:tdd', 'tdd'] };
  it('is the listed skill: no warning, marked chosen, and typing it again is refused', () => {
    expect(skillWarning('mattpocock-skills:tdd', [tdd], 'claude')).toBeNull();
    expect(isChosen(['mattpocock-skills:tdd'], tdd)).toBe(true);
    expect(isChosen(['other'], tdd)).toBe(false);
    expect(typeSkill(['mattpocock-skills:tdd'], 'tdd', [tdd]).ok).toBe(false);
  });
  it('ranks first as a current skill and as a same-role peer skill', () => {
    const others = [entry('aaa'), tdd];
    expect(names(rankSkills(others, { role: 'reviewer', pickMeWhen: '', chosen: ['mattpocock-skills:tdd'], members: [] }))).toEqual(['tdd', 'aaa']);
    expect(names(rankSkills(others, { role: 'reviewer', pickMeWhen: '', chosen: [], members: [crewMember('mate', 'reviewer', ['mattpocock-skills:tdd'])] }))).toEqual(['tdd', 'aaa']);
  });
});

describe('skillWarning', () => {
  const listed = [entry('tdd')];
  it('says nothing for a skill the list has', () => {
    expect(skillWarning('tdd', listed, '')).toBeNull();
    expect(skillWarning('tdd', listed, 'claude')).toBeNull();
  });
  it('says not found for a skill the list lacks when no harness is locked', () => {
    expect(skillWarning('my-repo-skill', listed, '')).toBe('not found');
  });
  it('names the harness for a skill the locked harness lacks', () => {
    expect(skillWarning('unslop', listed, 'codex')).toBe('not installed on codex');
  });
  it('warns for every skill when the harness has no dock and the list is empty', () => {
    expect(skillWarning('tdd', [], 'codex')).toBe('not installed on codex');
  });
});
