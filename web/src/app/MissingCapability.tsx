import { checkKey, checking, recheck } from './capabilities';
import { harnessName } from './harnessName';

/**
 * One line saying which capability a feature lacks on which harness, with a way to check again.
 * `reason` replaces the line when the server already worded it, and `id` names it for aria-describedby.
 */
export function MissingCapability({ fleet, harness, capability, reason, id }: {
  fleet: string;
  harness: string;
  capability?: string;
  reason?: string;
  id?: string;
}) {
  const busy = !!checking.value[checkKey(fleet, harness)];
  const name = harnessName(harness);
  return (
    <p class="missing" role="note">
      <span id={id}>
        {reason ?? (
          <>
            <strong>{capability}</strong> failed on {name}.
          </>
        )}
      </span>
      <button type="button" class="btn" disabled={busy} aria-label={`Check again ${capability ? `${capability} on ` : ''}${name}`} onClick={() => void recheck(fleet, harness)}>
        Check again
      </button>
    </p>
  );
}
