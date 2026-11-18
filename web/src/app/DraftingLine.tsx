import type { Ship } from './api';

/** The server's line on why a drafting ship's campaign was not followed, shown as plain text. */
export function DraftingLine({ ship }: { ship: Ship }) {
  return ship.drafting_notice ? <p class="note hand-note">{ship.drafting_notice}</p> : null;
}
