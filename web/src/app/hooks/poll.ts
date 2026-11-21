import { useEffect, useRef } from 'preact/hooks';

// Counts how often `deps` changed, so an effect can depend on one number
// instead of a caller-sized array.
function useDepsGeneration(deps: unknown[]): number {
  const last = useRef(deps);
  const generation = useRef(0);
  if (deps.length !== last.current.length || deps.some((d, i) => !Object.is(d, last.current[i]))) {
    last.current = deps;
    generation.current += 1;
  }
  return generation.current;
}

// Calls `tick` now and then every `everyMs` after each call settles, until it
// returns (or resolves to) true, `active` is false, `deps` change or the
// component unmounts. A change of `deps` restarts with an immediate call.
// `tick` gets `isLive`, which turns false once its run is stale: check it after
// each await, before writing state. Listed in `additionalHooks` of
// `exhaustive-deps`, so a caller's `deps` are checked against `tick`.
export function usePollUntil(tick: (isLive: () => boolean) => unknown, deps: unknown[], opts: { everyMs: number; active?: boolean }): void {
  const latest = useRef(tick);
  latest.current = tick;
  const generation = useDepsGeneration(deps);
  const active = opts.active ?? true;
  const { everyMs } = opts;
  useEffect(() => {
    if (!active) return;
    let live = true;
    const isLive = () => live;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const run = async () => {
      let done = false;
      try {
        done = (await latest.current(isLive)) === true;
      } catch {
        // A failed tick is a miss, not the end of the wait.
      }
      if (live && !done) timer = setTimeout(run, everyMs);
    };
    void run();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [active, everyMs, generation]);
}
