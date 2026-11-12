import { useState } from 'preact/hooks';
import { logoUrl } from './api';
import { HARNESS_PLACEHOLDERS } from './shell-tokens';

// Harnesses whose logo failed to load: a missing logo is requested once per page load.
const missing = new Set<string>();

/** A dock's harness flag: the rounded tile with the logo loaded as an image (never inlined), or its placeholder letters. */
export function Flag({ harness, size, label }: { harness: string; size: 'row' | 'header'; label?: string }) {
  const [failed, setFailed] = useState(missing.has(harness));
  const known = harness !== 'unknown' && Object.hasOwn(HARNESS_PLACEHOLDERS, harness);
  const key = known ? harness : 'unknown';
  return (
    <span
      class={`flag flag-${size}`}
      role={label ? 'img' : undefined}
      aria-label={label}
      style={{
        background: `var(--harness-${key}-field)`,
        color: `var(--harness-${key}-placeholder-ink)`,
      }}
    >
      {known && !failed ? (
        <img src={logoUrl(harness)} alt="" onError={() => {
            missing.add(harness);
            setFailed(true);
          }} />
      ) : (
        <span class="flag-letters">{HARNESS_PLACEHOLDERS[key]}</span>
      )}
    </span>
  );
}
