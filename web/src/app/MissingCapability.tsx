import { checkKey, checking, recheck } from './capabilities';

export const harnessName = (harness: string) => harness.charAt(0).toUpperCase() + harness.slice(1);

/**
 * One line saying which capability a feature lacks on which harness, with a way to check again.
 * `reason` replaces the line when the server already worded it; `onChecked` runs after the check.
 */
export function MissingCapability({ fleet, harness, capability, status = 'failed', reason, onChecked }: {
  fleet: string;
  harness: string;
  capability?: string;
  status?: 'failed' | 'not_checked';
  reason?: string;
  onChecked?: () => void;
}) {
  const busy = !!checking.value[checkKey(fleet, harness)];
  const name = harnessName(harness);
  return (
    <p class="missing" role="note">
      <span>
        {reason ?? (
          <>
            <strong>{capability}</strong> {status === 'failed' ? 'failed' : 'has not been checked'} on {name}.
          </>
        )}
      </span>
      <button type="button" class="btn" disabled={busy} aria-label={`Check again ${capability ? `${capability} on ` : ''}${name}`} onClick={() => void recheck(fleet, harness).then(onChecked)}>
        Check again
      </button>
    </p>
  );
}
