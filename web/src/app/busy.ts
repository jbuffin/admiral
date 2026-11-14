import { signal } from '@preact/signals';

export const SPINNER_MIN_MS = 300;

export type PendingKey = readonly string[];

interface Options {
  now: () => number;
  after: (fn: () => void, ms: number) => void;
  onChange?: () => void;
}

/** Requests in flight by key. A spinner that has shown stays until SPINNER_MIN_MS after it first showed. */
export function createPending({ now, after, onChange = () => {} }: Options) {
  const shown = new Map<string, number>();
  const id = (key: PendingKey) => JSON.stringify(key);

  function release(k: string) {
    shown.delete(k);
    onChange();
  }

  return {
    isBusy: (key: PendingKey) => shown.has(id(key)),
    shownAt: (key: PendingKey) => shown.get(id(key)),
    /** Resolves once the request settles, never rejects; undefined when fn gave no promise or the key is busy. */
    run(key: PendingKey, fn: () => unknown): Promise<void> | undefined {
      const k = id(key);
      if (shown.has(k)) return undefined;
      const result = fn();
      if (!result || typeof (result as PromiseLike<unknown>).then !== 'function') return undefined;
      const start = now();
      shown.set(k, start);
      onChange();
      const settled = () => {
        const left = start + SPINNER_MIN_MS - now();
        if (left > 0) after(() => release(k), left);
        else release(k);
      };
      return Promise.resolve(result).then(settled, settled);
    },
  };
}

/** Which buttons of one action group are busy; the others are disabled meanwhile. */
export function createActionGroup() {
  const busy = new Set<string>();
  return {
    setBusy(id: string, on: boolean) {
      if (on) busy.add(id);
      else busy.delete(id);
    },
    disabledFor: (id: string) => [...busy].some((b) => b !== id),
  };
}

const version = signal(0);
const pending = createPending({
  now: Date.now,
  after: (fn, ms) => void setTimeout(fn, ms),
  onChange: () => version.value++,
});

export const runPending = pending.run;
export function isPending(key: PendingKey): boolean {
  version.value;
  return pending.isBusy(key);
}
