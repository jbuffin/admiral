import { describe, expect, it } from 'vitest';
import { CUSTOM_ROLE, fieldError, groupMembers, memberBody, type CrewMember, type MemberForm } from './crew';

const member = (name: string, layer: 'built-in' | 'fleet', customized = false): CrewMember => ({
  name,
  role: 'implementer',
  'based-on': '',
  skills: [],
  model: '',
  harness: '',
  'pick-me-when': `Pick ${name}.`,
  instructions: '',
  layer,
  customized,
  notes: { fleet: [], repos: {} },
});

const form = (over: Partial<MemberForm> = {}): MemberForm => ({
  role: 'implementer',
  customRole: '',
  basedOn: '',
  skills: '',
  model: '',
  harness: '',
  pickMeWhen: 'Pick me.',
  instructions: 'Work.',
  ...over,
});

describe('groupMembers', () => {
  it('lists Fleet then Built-in, each in the order given, a customized member under Fleet', () => {
    const groups = groupMembers([member('scout', 'built-in'), member('ada', 'fleet'), member('lookout', 'fleet', true), member('mason', 'built-in')]);
    expect(groups.map((g) => g.title)).toEqual(['Fleet', 'Built-in']);
    expect(groups[0].members.map((m) => m.name)).toEqual(['ada', 'lookout']);
    expect(groups[0].members.map((m) => m.customized)).toEqual([false, true]);
    expect(groups[1].members.map((m) => m.name)).toEqual(['scout', 'mason']);
  });
  it('keeps both groups when a fleet has no Commodore or no members of its own', () => {
    expect(groupMembers([member('ada', 'fleet')]).map((g) => g.members.length)).toEqual([1, 0]);
    expect(groupMembers([]).map((g) => g.members.length)).toEqual([0, 0]);
  });
});

describe('memberBody', () => {
  it('sends a built-in role with no based-on, and skills one per line without blanks', () => {
    const body = memberBody(form({ skills: 'review\n  write-docs  \n\n', model: 'opus', harness: 'codex' }));
    expect(body).toEqual({ role: 'implementer', skills: ['review', 'write-docs'], model: 'opus', harness: 'codex', 'pick-me-when': 'Pick me.', instructions: 'Work.' });
    expect(body).not.toHaveProperty('based-on');
  });
  it('sends a custom role with the built-in role it is based on', () => {
    const body = memberBody(form({ role: CUSTOM_ROLE, customRole: ' cartographer ', basedOn: 'scribe' }));
    expect(body).toMatchObject({ role: 'cartographer', 'based-on': 'scribe' });
  });
  it('leaves the harness key out for none', () => {
    expect(memberBody(form({ harness: '' }))).not.toHaveProperty('harness');
  });
  it('drops skills and instructions for the fix checker', () => {
    const body = memberBody(form({ role: 'fix-checker', skills: 'review', instructions: 'Check.' }));
    expect(body.role).toBe('fix-checker');
    expect(body.skills ?? []).toEqual([]);
    expect(body.instructions ?? '').toBe('');
  });
});

describe('fieldError', () => {
  it('names the input a 400 points at', () => {
    for (const field of ['role', 'based-on', 'skills', 'model', 'harness', 'pick-me-when', 'instructions']) {
      expect(fieldError({ error: 'no good', field })).toEqual({ field, message: 'no good' });
    }
  });
  it('puts an error with no field, or one the form has no input for, at the top of the form', () => {
    expect(fieldError({ error: 'no good' })).toEqual({ field: null, message: 'no good' });
    expect(fieldError({ error: 'no good', field: 'name' })).toEqual({ field: null, message: 'no good' });
  });
});
