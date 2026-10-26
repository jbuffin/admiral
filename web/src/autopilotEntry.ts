// The entry a save of the autopilot settings form sends (#181). The form holds a Draft; buildEntry turns it
// into the entry for config.json. A key the engineer didn't touch goes back as stored, so an untouched save
// equals the stored entry and the server skips the write.

export type EntryKey = 'enabled' | 'fleet' | 'skill' | 'args' | 'max_ships' | 'low_stakes' | 'retro_every' | 'auto_merge' | 'resources';

export interface Skill {
  kind: string;
  name?: string;
}

export interface Draft {
  enabled: boolean;
  fleet: string;
  skill: Skill;
  args: string[];
  max_ships: number;
  low_stakes: string;
  retro_every: number;
  auto_merge: string; // '' is Off
  resources: string[];
}

type Entry = Record<string, any>;

// What the parser reads from a key the entry lacks (internal/config/autopilot.go).
const defaults: Pick<Draft, 'enabled' | 'max_ships' | 'low_stakes' | 'retro_every' | 'args' | 'resources'> = {
  enabled: false,
  max_ships: 1,
  low_stakes: 'advisor',
  retro_every: 5,
  args: [],
  resources: [],
};

export function draftOf(stored: Entry | null, firstClaudeFleet: string): Draft {
  if (!stored) return { ...defaults, enabled: true, fleet: firstClaudeFleet, skill: { kind: 'commodore' }, auto_merge: '' };
  return {
    enabled: stored.enabled === true,
    fleet: stored.fleet ?? firstClaudeFleet,
    skill: stored.skill ?? { kind: 'commodore' },
    args: stored.args ?? [],
    max_ships: stored.max_ships ?? defaults.max_ships,
    low_stakes: stored.low_stakes ?? defaults.low_stakes,
    retro_every: stored.retro_every ?? defaults.retro_every,
    auto_merge: stored.auto_merge ?? '',
    resources: stored.resources ?? [],
  };
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

export function buildEntry(stored: Entry | null, drafted: Draft, touched: ReadonlySet<EntryKey>): Entry {
  // Blank rows from "+ Add argument" are not sent; a stored "" in untouched args still goes back as it was.
  const draft = touched.has('args') ? { ...drafted, args: drafted.args.filter((a) => a !== '') } : drafted;
  const out: Entry = { ...stored };
  const had = (k: string) => !!stored && k in stored;
  // A new entry is written from its draft; a stored one only where the engineer changed it.
  const writes = (k: EntryKey) => touched.has(k) || !stored;

  for (const k of ['enabled', 'max_ships', 'low_stakes', 'retro_every', 'args', 'resources'] as const) {
    if (!writes(k)) continue;
    if (same(draft[k], defaults[k]) && !had(k)) delete out[k];
    else out[k] = draft[k];
  }
  if (writes('auto_merge')) {
    if (draft.auto_merge) out.auto_merge = draft.auto_merge;
    else delete out.auto_merge; // the parser refuses ""
  }
  // The parser requires these three.
  if (touched.has('fleet') || !had('fleet')) out.fleet = draft.fleet;
  if (touched.has('skill') || !had('skill')) out.skill = draft.skill;
  out.harness = 'claude';
  return out;
}
