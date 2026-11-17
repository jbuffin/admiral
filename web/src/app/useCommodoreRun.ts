import { useEffect, useRef, useState } from 'preact/hooks';
import { call, LAUNCH_TIMEOUT_MS } from './api';
import type { CommodoreRun } from './LaunchCommodoreOffer';

// A map by dock that a late async result can read and write without a stale closure.
function useByDock<T>() {
  const [map, setMap] = useState<Record<string, T>>({});
  const now = useRef(map);
  const put = (dock: string, value: T | undefined) => {
    const { [dock]: had, ...rest } = now.current;
    if (value === undefined && had === undefined) return;
    now.current = value === undefined ? rest : { ...rest, [dock]: value };
    setMap(now.current);
  };
  return { map, now, put };
}

/** An install or update per dock (fleet and harness), so a result shows whenever the dialog is on the dock it was for and on no other. */
export function useCommodoreRun(fleet: string, harness: string) {
  const dock = `${fleet}\n${harness}`;
  const dockNow = useRef(dock);
  dockNow.current = dock;
  const runs = useByDock<CommodoreRun>();
  const warnings = useByDock<string>();
  // The warning of a good run shows once the skills are read again, with the answer it belongs to.
  const warnNext = useRef<Record<string, string>>({});
  // Bumped after a good run, so the skills are read again for the same dock.
  const [refresh, setRefresh] = useState(0);

  const settle = (d: string) => {
    runs.put(d, undefined);
    const text = warnNext.current[d];
    delete warnNext.current[d];
    if (text) warnings.put(d, text);
  };

  // A dock left while its re-read was pending never sees that read land.
  useEffect(() => {
    for (const [d, r] of Object.entries(runs.now.current)) if (d !== dock && r.status === 'refreshing') settle(d);
  }, [dock]);

  const start = async (action: CommodoreRun['action']) => {
    const d = dock;
    const cur = runs.now.current[d];
    if (cur && cur.status !== 'failed') return;
    runs.put(d, { action, status: 'running' });
    warnings.put(d, undefined);
    delete warnNext.current[d];
    const r = await call<{ state: string; warning?: string }>('POST', '/launch-orders/commodore', { fleet, harness, action }, undefined, { timeoutMs: LAUNCH_TIMEOUT_MS });
    if (!r.ok) return runs.put(d, { action, status: 'failed', error: r.error, output: r.output, commands: r.commands });
    if (typeof r.data.warning === 'string') warnNext.current[d] = r.data.warning;
    if (dockNow.current !== d) return settle(d);
    runs.put(d, { action, status: 'refreshing' });
    setRefresh((n) => n + 1);
  };

  return {
    run: runs.map[dock] ?? null,
    warning: warnings.map[dock] ?? null,
    refresh,
    start,
    /** The skills of this dock have been read again. */
    read: () => {
      if (runs.now.current[dockNow.current]?.status === 'refreshing') settle(dockNow.current);
    },
  };
}
