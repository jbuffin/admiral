import type { VisibleStatus } from './shipList';

// Paths copied from designs/redesign/icons/status-*.svg; the colour comes from the surrounding text colour.
const GLYPHS: Record<VisibleStatus, preact.ComponentChildren> = {
  needs_you: (
    <>
      <circle cx="8" cy="8" r="6.5" fill="currentColor" />
      <path d="M8 4.8v3.8M8 11h0" stroke="#fff" stroke-width="1.7" stroke-linecap="round" />
    </>
  ),
  underway: (
    <>
      <circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" stroke-width="1.6" />
      <path d="M8 1.5a6.5 6.5 0 0 1 0 13z" fill="currentColor" />
    </>
  ),
  anchored: (
    <g fill="none" stroke="currentColor">
      <circle cx="8" cy="8" r="6.5" stroke-width="1.6" />
      <path d="M5.8 5.8l4.4 4.4M10.2 5.8l-4.4 4.4" stroke-width="1.5" stroke-linecap="round" />
    </g>
  ),
};

export function StatusGlyph({ status }: { status: VisibleStatus }) {
  return (
    <svg class={`glyph glyph-${status}`} width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
      {GLYPHS[status]}
    </svg>
  );
}
