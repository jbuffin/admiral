import { useEffect, useRef, useState } from 'preact/hooks';
import { call, type DirsAnswer } from './api';
import { fleets } from './fleets';
import type { SkillsAnswer } from './launchForm';

export interface Drafting {
  skill: string;
  harnesses: { harness: string; names: string[] }[];
}

export const plainName = (name: string) => name.slice(name.lastIndexOf(':') + 1);

const checkouts = (d: DirsAnswer) => d.entries.filter((e) => e.kind === 'checkout');
const startDir = (d: DirsAnswer) => {
  const list = checkouts(d);
  return list.find((e) => e.path === d.selected)?.path ?? list[0]?.path ?? '';
};

export interface DraftingPick {
  harness: string;
  dir: string;
  /** The fleet's dirs for the harness, checkouts only. */
  dirs: DirsAnswer | null;
  /** The plain name or saved name the select holds; '' is No skill. */
  skill: string;
  saved: string;
  skills: SkillsAnswer | null;
  loading: boolean;
  error: string;
  /** The docks a Change may offer. */
  docks: { harness: string }[];
  /** Installed invocation names for the selected skill on the Ship line's harness and repo. */
  names: string[];
  installed: boolean;
  ambiguous: boolean;
  picked: string;
  /** The name a launch sends, '' while an ambiguity is unanswered. */
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
  const [error, setError] = useState('');
  const gen = useRef(0);
  const started = useRef(false);
  const first = all[0]?.harness ?? '';

  const readDirs = async (h: string) => {
    const r = await call<DirsAnswer>('POST', '/launch-orders/dirs', { fleet, harness: h });
    return r.ok ? r.data : r.error;
  };
  const q = (h: string, d: string) => new URLSearchParams({ fleet, harness: h, dir: d });
  const readDrafting = (d: string) => call<Drafting>('GET', `/fleets/${encodeURIComponent(fleet)}/drafting?${new URLSearchParams({ dir: d })}`);
  const readSkills = (h: string, d: string) => call<SkillsAnswer>('GET', `/launch-orders/skills?${q(h, d)}`);

  // Reads the drafting skill and the dock's skills for a repo; a saved skill that is installed only elsewhere moves the harness once when switching is allowed.
  const read = async (h: string, d: string, mine: number, initial: boolean, movable = initial) => {
    setLoading(true);
    setError('');
    const dp = readDrafting(d);
    const sp = readSkills(h, d);
    const dr = await dp;
    if (mine !== gen.current) return;
    if (!dr.ok) {
      setDrafting(null);
      setSkills(null);
      setError(dr.error);
      setLoading(false);
      return;
    }
    if (movable && dr.data.skill && !dr.data.harnesses.some((x) => x.harness === h)) {
      const move = dr.data.harnesses.find((x) => all.some((a) => a.harness === x.harness));
      if (move) {
        const ans = await readDirs(move.harness);
        if (mine !== gen.current) return;
        if (typeof ans === 'string') {
          setError(ans);
          setLoading(false);
          return;
        }
        setHarnessState(move.harness);
        setDirs({ ...ans, entries: checkouts(ans) });
        const next = startDir(ans);
        setDirState(next);
        return read(move.harness, next, mine, true, false);
      }
    }
    setDrafting(dr.data);
    if (initial) setSkillState(dr.data.skill);
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
      const ans = await readDirs(first);
      if (mine !== gen.current) return;
      if (typeof ans === 'string') {
        setError(ans);
        return setLoading(false);
      }
      setHarnessState(first);
      setDirs({ ...ans, entries: checkouts(ans) });
      const d = startDir(ans);
      setDirState(d);
      if (!d) {
        setError('This fleet has no git checkout to run a drafting ship in.');
        return setLoading(false);
      }
      await read(first, d, mine, true);
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
  const names = (() => {
    if (!skill) return [];
    if (skill === saved) return drafting?.harnesses.find((h) => h.harness === harness)?.names ?? [];
    return (skills?.skills ?? []).map((s) => s.name).filter((n) => n === skill || plainName(n) === plainName(skill));
  })();
  const ambiguous = names.length >= 2;
  const carry = skill === saved && drafting ? drafting.harnesses.filter((h) => all.some((a) => a.harness === h.harness)) : [];
  const docks = carry.length > 0 ? carry.map((h) => ({ harness: h.harness })) : all;

  const setHarness = (h: string) => {
    if (h === harness) return;
    const mine = ++gen.current;
    setHarnessState(h);
    setPicked('');
    setLoading(true);
    void (async () => {
      const ans = await readDirs(h);
      if (mine !== gen.current) return;
      if (typeof ans === 'string') {
        setError(ans);
        return setLoading(false);
      }
      setDirs({ ...ans, entries: checkouts(ans) });
      const d = startDir(ans);
      setDirState(d);
      if (!d) {
        setError('This dock has no git checkout to run a drafting ship in.');
        return setLoading(false);
      }
      await read(h, d, mine, false);
    })();
  };
  const setDir = (d: string) => {
    if (d === dir) return;
    const mine = ++gen.current;
    setDirState(d);
    setPicked('');
    void read(harness, d, mine, false);
  };

  return {
    harness,
    dir,
    dirs,
    skill,
    saved,
    skills,
    loading,
    error,
    docks,
    names,
    installed: names.length > 0,
    ambiguous,
    picked,
    name: ambiguous ? picked : skill,
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
