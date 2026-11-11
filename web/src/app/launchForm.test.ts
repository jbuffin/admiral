import { describe, expect, it } from 'vitest';
import { defaultSkill, effortsFor, type LaunchFormState, launchBody, sourcesForFleet } from './launchForm';

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
    const body = launchBody({ ...form, clone: true });
    expect(body.clone).toBe(true);
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
