/** A module's waiting count, in the Orders badge's style but its own number. */
export function WaitingBadge({ count }: { count: number }) {
  return count > 0 ? <span class="nav-count needs">{count}</span> : null;
}
