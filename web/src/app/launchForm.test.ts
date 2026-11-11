import { describe, expect, it } from 'vitest';
import { campaignLaunch, campaignOrder, type CampaignLaunchState, sliceOrder, type CrewMemberView, crewOptions, defaultSkill, effortsFor, type LaunchFormState, launchBody, memberRun, sourcesForFleet, togglePin } from './launchForm';

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

describe('campaignOrder', () => {
  it('names the child by its campaign and URL, for the checkout preselect', () => {
    expect(campaignOrder('3f9c0a41b7d2e8a5', 'https://github.com/acme/widgets/issues/21')).toEqual({
      source: 'campaign',
      id: '3f9c0a41b7d2e8a5/https://github.com/acme/widgets/issues/21',
    });
  });
});

describe('sliceOrder', () => {
  const u = (n: number) => `https://github.com/acme/widgets/issues/${n}`;
  it('names the first child in full and the others of its repo by number, in launch order', () => {
    expect(sliceOrder('c0ffee', [u(31), u(32), u(34)])).toEqual({ source: 'campaign', id: 'c0ffee/https://github.com/acme/widgets/issues/31,32,34' });
  });
  it('writes a child of another repo in full', () => {
    const other = 'https://github.com/acme/gadgets/issues/7';
    expect(sliceOrder('c0ffee', [u(31), other, u(32)]).id).toBe(`c0ffee/${u(31)},${other},32`);
  });
});

describe('campaignLaunch', () => {
  const form: CampaignLaunchState = {
    campaignId: '3f9c0a41b7d2e8a5',
    childUrl: 'https://github.com/acme/widgets/issues/21',
    harness: 'claude',
    dir: '/code/widgets',
    clone: false,
    startAnyway: false,
    model: '',
    effort: '',
  };

  it('posts to the campaign\'s launch route with the child, the harness and the checkout', () => {
    const { path, body } = campaignLaunch({ ...form, model: 'opus', effort: 'high' });
    expect(path).toBe('/campaigns/3f9c0a41b7d2e8a5/launch');
    expect(body).toEqual({ child_url: form.childUrl, harness: 'claude', dir: '/code/widgets', model: 'opus', effort: 'high' });
  });
  it('sends no skill, text, autopilot, resources or order: the server picks them', () => {
    const { body } = campaignLaunch(form);
    for (const field of ['skill', 'text', 'autopilot', 'resources', 'fleet', 'source', 'order_id']) expect(body).not.toHaveProperty(field);
  });
  it('sends clone and the repo shown for it, and no dir, when the clone is chosen', () => {
    const { body } = campaignLaunch({ ...form, clone: true, cloneRepo: 'github.com/acme/widgets' });
    expect(body).toMatchObject({ clone: true, clone_repo: 'github.com/acme/widgets' });
    expect(body).not.toHaveProperty('dir');
  });
  it('sends a slice\'s issues in the order given and no child_url', () => {
    const issues = [form.childUrl!, 'https://github.com/acme/widgets/issues/22'];
    const { path, body } = campaignLaunch({ ...form, childUrl: undefined, issues, model: 'opus' });
    expect(path).toBe('/campaigns/3f9c0a41b7d2e8a5/launch');
    expect(body).toEqual({ issues, harness: 'claude', dir: '/code/widgets', model: 'opus' });
    expect(body).not.toHaveProperty('child_url');
  });
  it('sends start_anyway only when it is chosen', () => {
    expect(campaignLaunch(form).body).not.toHaveProperty('start_anyway', true);
    expect(campaignLaunch({ ...form, startAnyway: true }).body).toMatchObject({ start_anyway: true });
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
  it('can be pinned when the member is locked to another harness, with a hint where it runs', () => {
    expect(crewOptions(members, 'claude').map((o) => [o.name, o.hint])).toEqual([
      ['scout', null],
      ['coder', 'runs on codex'],
    ]);
    expect(crewOptions(members, 'codex').map((o) => [o.name, o.hint])).toEqual([
      ['scout', null],
      ['coder', null],
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

describe('launching as a member', () => {
  const form: LaunchFormState = {
    fleet: 'work',
    harness: 'claude',
    dir: '/code/api',
    clone: false,
    skill: { kind: 'none' },
    text: 'go',
    model: '',
    effort: '',
    autopilot: false,
    resources: [],
  };
  const members: CrewMemberView[] = [
    { name: 'scout', role: 'scribe', layer: 'built-in', harness: null },
    { name: 'coder', role: 'implementer', layer: 'fleet', harness: 'codex' },
  ];

  it('sends the member, and nothing when none is chosen', () => {
    expect(launchBody({ ...form, member: 'scout' })).toMatchObject({ member: 'scout', harness: 'claude' });
    expect(launchBody(form)).not.toHaveProperty('member');
    expect(launchBody({ ...form, member: '' })).not.toHaveProperty('member');
  });
  it('sends the harness the member is locked to', () => {
    expect(launchBody({ ...form, member: 'coder', memberHarness: 'codex' })).toMatchObject({ member: 'coder', harness: 'codex' });
  });
  it('is sent only with Just launch, and never with pinned crew', () => {
    expect(launchBody({ ...form, skill: { kind: 'commodore' }, member: 'scout' })).not.toHaveProperty('member');
    expect(launchBody({ ...form, skill: { kind: 'skill', name: 'grill' }, member: 'scout' })).not.toHaveProperty('member');
    expect(launchBody({ ...form, member: 'scout', crew: ['mate'] })).not.toHaveProperty('crew');
  });
  it('runs on the lock, with a hint, or on the form harness without one', () => {
    expect(memberRun(members, 'claude', 'coder')).toEqual({ harness: 'codex', hint: 'runs on codex' });
    expect(memberRun(members, 'codex', 'coder')).toEqual({ harness: 'codex', hint: null });
    expect(memberRun(members, 'claude', 'scout')).toEqual({ harness: 'claude', hint: null });
    expect(memberRun(members, 'claude', 'ghost')).toBeNull();
  });
  it('keeps the form harness and no hint for a fix checker, whatever its lock', () => {
    const checker: CrewMemberView[] = [{ name: 'check', role: 'fix-checker', layer: 'fleet', harness: 'codex' }];
    expect(memberRun(checker, 'claude', 'check')).toEqual({ harness: 'claude', hint: null });
  });
  it('drops the dock harness model and an effort the lock does not offer', () => {
    const base = { ...form, skill: { kind: 'none' } as const, model: 'opus', effort: 'max' };
    const moved = launchBody({ ...base, member: 'coder', memberHarness: 'codex' });
    expect(moved).toMatchObject({ harness: 'codex' });
    expect(moved).not.toHaveProperty('model');
    expect(moved).not.toHaveProperty('effort');
    expect(launchBody({ ...base, effort: 'high', member: 'coder', memberHarness: 'codex' })).toMatchObject({ effort: 'high' });
    expect(launchBody({ ...base, effort: 'high', member: 'coder', memberHarness: 'copilot' })).not.toHaveProperty('effort');
  });
  it('keeps the model and effort of a member that does not move the harness', () => {
    const base = { ...form, skill: { kind: 'none' } as const, model: 'opus', effort: 'max' };
    expect(launchBody({ ...base, member: 'scout', memberHarness: null })).toMatchObject({ model: 'opus', effort: 'max' });
    expect(launchBody({ ...base, member: 'scout', memberHarness: 'claude' })).toMatchObject({ model: 'opus', effort: 'max' });
  });
});
