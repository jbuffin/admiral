import { describe, expect, it } from 'vitest';
import { type Draft, type EntryKey, buildEntry, draftOf } from './autopilotEntry';

// The entry a save of the autopilot form sends (#181, item 3). The module under test:
//
//   web/src/autopilotEntry.ts
//     type EntryKey = 'enabled' | 'fleet' | 'skill' | 'args' | 'max_ships' | 'low_stakes' | 'retro_every' | 'auto_merge' | 'resources'
//     type Draft    = the form's state, one field per key, every key filled:
//                     { enabled, fleet, skill: {kind, name?}, args: string[], max_ships, low_stakes, retro_every,
//                       auto_merge ('' is Off), resources: string[] }
//     draftOf(stored: object | null, firstClaudeFleet: string): Draft
//       a panel with no entry gets the defaults, switched on, on the first fleet with a claude dock, with Commodore
//     buildEntry(stored: object | null, draft: Draft, touched: ReadonlySet<EntryKey>): object
//       a key the engineer didn't touch goes back as stored (absent stays absent); a touched key is written from the
//       draft, except a value equal to the parser default is left out when the stored entry lacked the key;
//       fleet, harness and skill are always written; auto_merge '' removes the key

const none = new Set<EntryKey>();

// What a save sends for a form that was opened on `stored` and saved with only `keys` changed by `patch`.
function save(stored: object | null, keys: EntryKey[], patch: Partial<Draft> = {}) {
  const draft = { ...draftOf(stored, 'work'), ...patch };
  return buildEntry(stored, draft, new Set(keys));
}

const base = { enabled: true, fleet: 'work', harness: 'claude', skill: { kind: 'none' } };

describe('an untouched save sends the stored entry back as it is', () => {
  const stored: object[] = [
    base,
    { fleet: 'work', harness: 'claude', skill: { kind: 'none' } }, // no enabled key
    { ...base, enabled: null },
    { ...base, resources: ['simulator', 'phone'] },
    { ...base, resources: ['phone', 'phone'] },
    { ...base, resources: [] },
    { ...base, args: ['--model', ''] },
    { ...base, skill: { kind: 'commodore', name: 'stray' } },
    { ...base, skill: { kind: 'skill', name: '/tdd' } },
    { ...base, max_ships: 1, low_stakes: 'advisor', retro_every: 5 }, // defaults the hand edit spelled out
  ];
  it.each(stored.map((s) => [JSON.stringify(s), s] as const))('%s', (_, s) => {
    expect(save(s, [])).toStrictEqual(s);
  });
});

describe('a changed field is written', () => {
  it('turning enabled off sends false and keeps every other field as stored', () => {
    const s = { ...base, args: ['--model', 'opus'], resources: ['b', 'a'], skill: { kind: 'commodore', name: 'stray' } };
    expect(save(s, ['enabled'], { enabled: false })).toStrictEqual({ ...s, enabled: false });
  });

  it('a value equal to the parser default is left out when the stored entry lacked the key', () => {
    expect(save(base, ['max_ships'], { max_ships: 1 })).toStrictEqual(base);
    expect(save(base, ['low_stakes'], { low_stakes: 'advisor' })).toStrictEqual(base);
    expect(save(base, ['retro_every'], { retro_every: 5 })).toStrictEqual(base);
    expect(save(base, ['args'], { args: [] })).toStrictEqual(base);
    expect(save(base, ['resources'], { resources: [] })).toStrictEqual(base);
    expect(save({ fleet: 'work', harness: 'claude', skill: { kind: 'none' } }, ['enabled'], { enabled: false })).toStrictEqual({
      fleet: 'work',
      harness: 'claude',
      skill: { kind: 'none' },
    });
  });

  it('a value equal to the parser default is written when the stored entry had the key', () => {
    const s = { ...base, max_ships: 3, low_stakes: 'never', retro_every: 9, args: ['--model', 'opus'], resources: ['phone'] };
    const out = save(s, ['max_ships', 'low_stakes', 'retro_every', 'args', 'resources'], {
      max_ships: 1,
      low_stakes: 'advisor',
      retro_every: 5,
      args: [],
      resources: [],
    });
    expect(out).toStrictEqual({ ...base, max_ships: 1, low_stakes: 'advisor', retro_every: 5, args: [], resources: [] });
  });

  it('a value other than the default is written, and leaves the untouched keys as stored', () => {
    const s = { ...base, args: ['--model', ''], resources: ['b', 'a'] };
    expect(save(s, ['max_ships', 'retro_every', 'low_stakes'], { max_ships: 4, retro_every: 0, low_stakes: 'never' })).toStrictEqual({
      ...s,
      max_ships: 4,
      retro_every: 0,
      low_stakes: 'never',
    });
  });

  it('a changed skill, fleet and args are written from the draft', () => {
    const out = save(base, ['skill', 'fleet', 'args'], { skill: { kind: 'skill', name: 'triage' }, fleet: 'home', args: ['--effort', 'high'] });
    expect(out).toStrictEqual({ ...base, fleet: 'home', skill: { kind: 'skill', name: 'triage' }, args: ['--effort', 'high'] });
  });
});

describe('blank argument rows', () => {
  it('are dropped when args is touched, and kept when it is not', () => {
    const s = { ...base, args: ['--model', ''] };
    expect(save(s, ['args'], { args: ['--model', 'opus', '', ''] })).toStrictEqual({ ...s, args: ['--model', 'opus'] });
    expect(save(s, [])).toStrictEqual(s);
  });
});

describe('auto_merge', () => {
  it('Off removes the key the stored entry had', () => {
    expect(save({ ...base, auto_merge: 'squash' }, ['auto_merge'], { auto_merge: '' })).toStrictEqual(base);
  });

  it('a pick is written', () => {
    expect(save(base, ['auto_merge'], { auto_merge: 'rebase' })).toStrictEqual({ ...base, auto_merge: 'rebase' });
  });

  it('untouched, the stored value goes back', () => {
    expect(save({ ...base, auto_merge: 'merge' }, [])).toStrictEqual({ ...base, auto_merge: 'merge' });
  });
});

describe('a new entry', () => {
  it('starts switched on, on the first claude fleet, with Commodore', () => {
    expect(draftOf(null, 'home')).toMatchObject({ enabled: true, fleet: 'home', skill: { kind: 'commodore' }, max_ships: 1, retro_every: 5, low_stakes: 'advisor', auto_merge: '' });
  });

  it('saved untouched sends only what the parser requires, and enabled', () => {
    expect(buildEntry(null, draftOf(null, 'home'), none)).toStrictEqual({ enabled: true, fleet: 'home', harness: 'claude', skill: { kind: 'commodore' } });
  });

  it('a touched field equal to a default is left out; one that differs is written', () => {
    const out = save(null, ['max_ships', 'retro_every'], { max_ships: 1, retro_every: 2 });
    expect(out).toStrictEqual({ enabled: true, fleet: 'work', harness: 'claude', skill: { kind: 'commodore' }, retro_every: 2 });
  });
});
