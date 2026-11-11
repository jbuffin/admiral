import { checkKey, checking, recheck } from './capabilities';

export const harnessName = (harness: string) => harness.charAt(0).toUpperCase() + harness.slice(1);

/** One line saying which capability a feature lacks on which harness, with a way to check again. */
export function MissingCapability({ fleet, harness, capability }: { fleet: string; harness: string; capability: string }) {
  const busy = !!checking.value[checkKey(fleet, harness)];
  const name = harnessName(harness);
  return (
    <p class="missing" role="note">
      <span>
        <strong>{capability}</strong> failed on {name}.
      </span>
      <button type="button" class="btn" disabled={busy} aria-label={`Check again ${capability} on ${name}`} onClick={() => void recheck(fleet, harness)}>
        Check again
      </button>
    </p>
  );
}
