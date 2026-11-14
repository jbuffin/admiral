import type { Ship } from './api';
import { reviews } from './moored';
import type { VisibleStatus } from './shipList';

// Paths copied from designs/redesign/icons/status-*.svg; the colour comes from the surrounding text colour.
const GLYPHS: Record<VisibleStatus | 'ready', preact.ComponentChildren> = {
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
  moored: (
    <g fill="none" stroke="currentColor">
      <circle cx="8" cy="8" r="6.5" stroke-width="1.6" />
      <path d="M8 5v3.2l2 1.4" stroke-width="1.5" stroke-linecap="round" />
    </g>
  ),
  ready: (
    <>
      <circle cx="8" cy="8" r="6.5" fill="currentColor" />
      <path d="M5.2 8.2 7.2 10.2 10.9 6" stroke="#fff" stroke-width="1.6" fill="none" stroke-linecap="round" stroke-linejoin="round" />
    </>
  ),
  anchored: (
    <g fill="none" stroke="currentColor">
      <circle cx="8" cy="8" r="6.5" stroke-width="1.6" />
      <path d="M5.8 5.8l4.4 4.4M10.2 5.8l-4.4 4.4" stroke-width="1.5" stroke-linecap="round" />
    </g>
  ),
};

export function StatusGlyph({ status }: { status: VisibleStatus | 'ready' }) {
  return (
    <svg class={`glyph glyph-${status}`} width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
      {GLYPHS[status]}
    </svg>
  );
}

/** A moored ship whose whole stack can merge now; it stays in the Moored group. */
export function ReadyToMerge({ ship }: { ship: Pick<Ship, 'status' | 'stack_ready' | 'prs'> }) {
  // Admiral doesn't merge a reviewed PR, though the server serves stack_ready for it.
  if (ship.status !== 'moored' || !ship.stack_ready || reviews(ship)) return null;
  return (
    <span class="ready-badge">
      <StatusGlyph status="ready" /> Ready to merge
    </span>
  );
}
