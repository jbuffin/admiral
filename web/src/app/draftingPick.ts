import { useEffect, useRef, useState } from 'preact/hooks';
import { call, type DirsAnswer } from './api';
import { fleets } from './fleets';
import type { SkillsAnswer } from './launchForm';

export interface Drafting {
  skill: string;
  harnesses: { harness: string; names: string[] }[];
}

export const plainName = (name: string) => name.slice(name.lastIndexOf(':') + 1);

export interface DraftingOption {
  name: string;
  installed: boolean;
}

export interface DraftingPick {
  harness: string;
  dir: string;
  /** The fleet's checkouts for the harness. */
  dirs: DirsAnswer | null;
  /** The name the select holds; '' is No skill. */
  skill: string;
  skills: SkillsAnswer | null;
  /** True until the first drafting read has answered, so Create can't save a pick the engineer never saw. */
  pending: boolean;
  /** True once the saved skill is known: only then may a Create save the pick. */
  known: boolean;
  loading: boolean;
  error: string;
  /** The docks a Change may offer. */
  docks: { harness: string }[];
  /** The select's skill options after No skill, each with whether it is installed on the Ship line's harness and repo. */
  options: DraftingOption[];
  installed: boolean;
  names: string[];
  ambiguous: boolean;
  picked: string;
  /** Whether a drafting ship can launch now, apart from the text. */
  launchable: boolean;
  /** The name a launch sends. */
  name: string;
  setSkill: (s: string) => void;
  setPicked: (n: string) => void;
  setHarness: (h: string) => void;
  setDir: (d: string) => void;
}

/** The new-campaign step's drafting skill, with the Ship line's harness and repo; kept above the step so Back keeps them. */
export function useDraftingPick(fleet: string, enabled: boolean): DraftingPick | null {
  const all = (fleets.value.find((f) => f.key === fleet)?.docks ?? []).map((d) => ({ harness: d.harness }));
  const [harness, setHarnessState] = useState('');
  const [dir, setDirState] = useState('');
  const [dirs, setDirs] = useState<DirsAnswer | null>(null);
  const [drafting, setDrafting] = useState<Drafting | null>(null);
  const [skills, setSkills] = useState<SkillsAnswer | null>(null);
  const [skill, setSkillState] = useState('');
  const [picked, setPicked] = useState('');
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState(true);
  const [known, setKnown] = useState(false);
  const [error, setError] = useState('');
  const gen = useRef(0);
  const started = useRef(false);
  const first = all[0]?.harness ?? '';

  const fail = (message: string) => {
    setError(message);
    setLoading(false);
    setPending(false);
  };
  // Reads a dock's dirs and settles the harness and repo on its last-used checkout; null when it can't.
  const settle = async (h: string, mine: number): Promise<string | null> => {
    const r = await call<DirsAnswer>('POST', '/launch-orders/dirs', { fleet, harness: h });
    if (mine !== gen.current) return null;
    if (!r.ok) return void fail(r.error), null;
    const list = r.data.entries.filter((e) => e.kind === 'checkout');
    setHarnessState(h);
    setDirs({ ...r.data, entries: list });
    const d = list.find((e) => e.path === r.data.selected)?.path ?? list[0]?.path ?? '';
    setDirState(d);
    if (!d) return void fail('This dock has no git checkout to run a drafting ship in.'), null;
    return d;
  };

  // Reads the drafting skill and the dock's skills for a repo; a saved skill installed only elsewhere moves the harness once.
  const read = async (h: string, d: string, mine: number, initial: boolean, movable = initial) => {
    setLoading(true);
    setError('');
    const dp = call<Drafting>('GET', `/fleets/${encodeURIComponent(fleet)}/drafting?${new URLSearchParams({ dir: d })}`);
    const sp = call<SkillsAnswer>('GET', `/launch-orders/skills?${new URLSearchParams({ fleet, harness: h, dir: d })}`);
    const dr = await dp;
    if (mine !== gen.current) return;
    if (!dr.ok) {
      setDrafting(null);
      setSkills(null);
      return fail(dr.error);
    }
    if (movable && dr.data.skill && !dr.data.harnesses.some((x) => x.harness === h)) {
      const move = dr.data.harnesses.find((x) => all.some((a) => a.harness === x.harness));
      if (move) {
        const next = await settle(move.harness, mine);
        if (next === null) return;
        return read(move.harness, next, mine, true, false);
      }
    }
    setDrafting(dr.data);
    if (initial) {
      setSkillState(dr.data.skill);
      setKnown(true);
      setPending(false);
    }
    const sk = await sp;
    if (mine !== gen.current) return;
    setSkills(sk.ok ? sk.data : null);
    if (!sk.ok) setError(sk.error);
    setLoading(false);
  };

  useEffect(() => {
    if (!enabled || started.current || !first) return;
    started.current = true;
    const mine = ++gen.current;
    void (async () => {
      const d = await settle(first, mine);
      if (d !== null) await read(first, d, mine, true);
    })();
  }, [enabled, first]);

  useEffect(
    () => () => {
      gen.current++;
    },
    [],
  );

  if (!first) return null;

  const saved = drafting?.skill ?? '';
  // The drafting route reports installs only for the saved name; any other name is read off the dock's skills.
  const namesOf = (n: string) => {
    if (!n) return [];
    if (n === saved) return drafting?.harnesses.find((h) => h.harness === harness)?.names ?? [];
    return (skills?.skills ?? []).map((s) => s.name).filter((x) => x === n || plainName(x) === plainName(n));
  };
  const names = namesOf(skill);
  const ambiguous = names.length >= 2;
  const listed = [...new Set((skills?.skills ?? []).map((s) => plainName(s.name)))];
  for (const n of [skill, saved]) if (n && !listed.includes(n)) listed.push(n);
  const carry = skill === saved && drafting ? drafting.harnesses.filter((h) => all.some((a) => a.harness === h.harness)) : [];
  const name = ambiguous ? picked : skill;
  const installed = names.length > 0;

  const moved = () => {
    setPicked('');
    return ++gen.current;
  };
  const setHarness = (h: string) => {
    if (h === harness) return;
    const mine = moved();
    setLoading(true);
    void (async () => {
      const d = await settle(h, mine);
      if (d !== null) await read(h, d, mine, false);
    })();
  };
  const setDir = (d: string) => {
    if (d === dir) return;
    const mine = moved();
    setDirState(d);
    void read(harness, d, mine, false);
  };

  return {
    harness,
    dir,
    dirs,
    skill,
    skills,
    pending,
    known,
    loading,
    error,
    docks: carry.length > 0 ? carry.map((h) => ({ harness: h.harness })) : all,
    options: listed.map((n) => ({ name: n, installed: namesOf(n).length > 0 })),
    installed,
    names,
    ambiguous,
    picked,
    launchable: !loading && installed && !!name && !!dir,
    name,
    setSkill: (s) => {
      setSkillState(s);
      setPicked('');
    },
    setPicked,
    setHarness,
    setDir,
  };
}

/** Saves the fleet's drafting skill after a successful create; a failure only costs the next open its pick. */
export async function saveDraftingSkill(fleet: string, skill: string) {
  await call('PUT', `/fleets/${encodeURIComponent(fleet)}/drafting`, { skill });
}
