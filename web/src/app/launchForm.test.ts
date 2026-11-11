import { describe, expect, it } from 'vitest';
import { type CrewMemberView, crewOptions, defaultSkill, effortsFor, type LaunchFormState, launchBody, sourcesForFleet, togglePin } from './launchForm';

describe('sourcesForFleet', () => {
  it('keeps the sources of the active fleet only', () => {
    const all = [
      { key: 'a', fleet: 'work' },
      { key: 'b', fleet: 'stay' },
      { key: 'c', fleet: 'work' },
    ];
    expect(sourcesForFleet(all, 'work').map((s) => s.key)).toEqual(['a', 'c']);
    expect(sourcesForFleet(all, 'none')).toEqual([]);
  });
});

describe('defaultSkill', () => {
  const skills = (found: boolean, favorites: string[]) => ({
    commodore: { found, name: found ? 'commodore' : '' },
    skills: [{ name: 'other', favorite: false }, ...favorites.map((name) => ({ name, favorite: true }))],
  });

  it('is Commodore when it is installed', () => {
    expect(defaultSkill(skills(true, ['grill']))).toEqual({ kind: 'commodore' });
  });
  it('is the first favorite when Commodore is missing', () => {
    expect(defaultSkill(skills(false, ['grill', 'tdd']))).toEqual({ kind: 'skill', name: 'grill' });
  });
  it('is Just launch when Commodore is missing and nothing is a favorite', () => {
    expect(defaultSkill(skills(false, []))).toEqual({ kind: 'none' });
  });
});

describe('launchBody', () => {
  const form: LaunchFormState = {
    fleet: 'work',
    harness: 'claude',
    dir: '/code/api',
    clone: false,
    skill: { kind: 'skill', name: 'review' },
    text: 'look at the parser',
    model: '',
    effort: '',
    autopilot: false,
    resources: [],
  };

  it('sends the fields the server reads, with the dir', () => {
    expect(launchBody({ ...form, model: 'opus', effort: 'high', autopilot: true, resources: ['db'] })).toMatchObject({
      fleet: 'work',
      harness: 'claude',
      dir: '/code/api',
      skill: { kind: 'skill', name: 'review' },
      text: 'look at the parser',
      model: 'opus',
      effort: 'high',
      autopilot: true,
      resources: ['db'],
    });
  });
  it('sends the order a launch came from', () => {
    expect(launchBody({ ...form, source: 'orders', orderId: 'o1' })).toMatchObject({ source: 'orders', order_id: 'o1' });
  });
  it('sends clone: true and no dir when the clone is chosen', () => {
    const body = launchBody({ ...form, clone: true, cloneRepo: 'github.com/acme/widget' });
    expect(body.clone).toBe(true);
    expect(body.clone_repo).toBe('github.com/acme/widget');
    expect(body).not.toHaveProperty('dir');
  });
  it('sends no clone when a checkout is chosen', () => {
    expect(launchBody(form).clone).not.toBe(true);
  });
});

describe('effortsFor', () => {
  it('offers each harness its own efforts', () => {
    expect(effortsFor('claude')).toEqual(['low', 'medium', 'high', 'max']);
    expect(effortsFor('codex')).toEqual(['low', 'medium', 'high']);
    expect(effortsFor('copilot')).toEqual([]);
  });
});

describe('pinned crew members', () => {
  const form: LaunchFormState = {
    fleet: 'work',
    harness: 'claude',
    dir: '/code/api',
    clone: false,
    skill: { kind: 'commodore' },
    text: 'go',
    model: '',
    effort: '',
    autopilot: false,
    resources: [],
  };

  it('are sent with a Commodore launch', () => {
    expect(launchBody({ ...form, crew: ['scout', 'mate'] })).toMatchObject({ crew: ['scout', 'mate'] });
  });
  it('are left out when none are pinned', () => {
    expect(launchBody({ ...form, crew: [] })).not.toHaveProperty('crew');
    expect(launchBody(form)).not.toHaveProperty('crew');
  });
  it('are left out of a launch that is not Commodore', () => {
    expect(launchBody({ ...form, skill: { kind: 'none' }, crew: ['scout'] })).not.toHaveProperty('crew');
    expect(launchBody({ ...form, skill: { kind: 'skill', name: 'grill' }, crew: ['scout'] })).not.toHaveProperty('crew');
  });

  const members: CrewMemberView[] = [
    { name: 'scout', role: 'scribe', layer: 'built-in', harness: null },
    { name: 'coder', role: 'implementer', layer: 'fleet', harness: 'codex' },
  ];
  it('cannot be pinned when the member is locked to another harness', () => {
    expect(crewOptions(members, 'claude').map((o) => [o.name, o.disabled])).toEqual([
      ['scout', false],
      ['coder', true],
    ]);
    expect(crewOptions(members, 'codex').map((o) => [o.name, o.disabled])).toEqual([
      ['scout', false],
      ['coder', false],
    ]);
  });
});

describe('togglePin', () => {
  const members: CrewMemberView[] = [
    { name: 'scout', role: 'scribe', layer: 'built-in', harness: null },
    { name: 'quill', role: 'scribe', layer: 'fleet', harness: null },
    { name: 'mate', role: 'reviewer', layer: 'fleet', harness: null },
  ];
  it('pins a member and unpins it again', () => {
    expect(togglePin([], members, 'scout')).toEqual(['scout']);
    expect(togglePin(['scout'], members, 'scout')).toEqual([]);
  });
  it('keeps at most one member per role', () => {
    expect(togglePin(['scout', 'mate'], members, 'quill')).toEqual(['mate', 'quill']);
  });
});
