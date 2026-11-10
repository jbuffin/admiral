import { useState } from 'preact/hooks';
import { logoUrl } from './api';
import { HARNESS_PLACEHOLDERS } from './shell-tokens';

/** A dock's harness flag: the rounded tile with the logo loaded as an image (never inlined), or its placeholder letters. */
export function Flag({ harness, size }: { harness: string; size: 'row' | 'header' }) {
  const [failed, setFailed] = useState(false);
  const known = harness !== 'unknown' && harness in HARNESS_PLACEHOLDERS;
  const key = known ? harness : 'unknown';
  return (
    <span
      class={`flag flag-${size}`}
      style={{
        background: `var(--harness-${key}-field)`,
        color: `var(--harness-${key}-placeholder-ink)`,
      }}
    >
      {known && !failed ? (
        <img src={logoUrl(harness)} alt="" onError={() => setFailed(true)} />
      ) : (
        <span class="flag-letters">{HARNESS_PLACEHOLDERS[key]}</span>
      )}
    </span>
  );
}
