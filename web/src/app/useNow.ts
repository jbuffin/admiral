import { useEffect, useState } from 'preact/hooks';

// Ages count from a timestamp, so they refresh on a timer rather than on a ships message.
export function useNow(everyMs = 15_000): number {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), everyMs);
    return () => clearInterval(t);
  }, [everyMs]);
  return now;
}
