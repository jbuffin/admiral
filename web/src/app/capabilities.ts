import { signal } from '@preact/signals';
import { checkHarness, fetchCapabilities, type CheckAnswer, type FleetCapabilities } from './api';

export const capabilities = signal<FleetCapabilities[]>([]);
export const capabilitiesLoaded = signal(false);
export const capabilitiesFailed = signal(false);

/** Harnesses being probed or whose last probe could not run, by `fleet/harness`. */
export const checking = signal<Record<string, boolean>>({});
export const checkErrors = signal<Record<string, string>>({});

export const checkKey = (fleet: string, harness: string) => `${fleet}/${harness}`;

const set = <T,>(s: { value: Record<string, T> }, key: string, value: T | null) => {
  const next = { ...s.value };
  if (value === null) delete next[key];
  else next[key] = value;
  s.value = next;
};

// A check's answer is newer than any list requested before it finished.
let epoch = 0;

export async function loadCapabilities() {
  const started = epoch;
  try {
    const list = await fetchCapabilities();
    if (started === epoch) capabilities.value = list;
    capabilitiesFailed.value = false;
  } catch {
    capabilitiesFailed.value = true;
  }
  capabilitiesLoaded.value = true;
}

/** Runs the probes for one harness in one fleet and swaps its answer into the list. */
export async function recheck(fleet: string, harness: string) {
  const key = checkKey(fleet, harness);
  if (checking.value[key]) return;
  set(checking, key, true);
  set(checkErrors, key, null);
  try {
    const { judge, ...answer }: CheckAnswer = await checkHarness(fleet, harness);
    epoch++;
    capabilities.value = capabilities.value.map((f) =>
      f.key === fleet ? { ...f, judge: judge ?? f.judge, harnesses: f.harnesses.map((h) => (h.harness === harness ? { ...h, ...answer } : h)) } : f,
    );
  } catch (e) {
    set(checkErrors, key, e instanceof Error ? e.message : 'The check could not run.');
    // A failed check can still have changed what the server stores (a version that no longer answers clears every pass).
    epoch++;
    await loadCapabilities();
  } finally {
    set(checking, key, null);
  }
}
