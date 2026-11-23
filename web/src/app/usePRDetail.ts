import { useEffect, useState } from 'preact/hooks';
import { fetchSourcePR, type PRDetail } from './api';

export type PRRead = { state: 'loading' } | { state: 'ready'; detail: PRDetail } | { state: 'failed'; error: string };

/** One PR read through its source; a null url makes no request. */
export function usePRDetail(source: string, url: string | null): PRRead {
  const [read, setRead] = useState<PRRead>({ state: 'loading' });
  useEffect(() => {
    if (!url) return;
    let alive = true;
    setRead({ state: 'loading' });
    void fetchSourcePR(source, url).then((r) => {
      if (alive) setRead(r.ok ? { state: 'ready', detail: r.data } : { state: 'failed', error: r.error });
    });
    return () => {
      alive = false;
    };
  }, [source, url]);
  return read;
}
