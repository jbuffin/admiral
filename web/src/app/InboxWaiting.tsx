import { fleetSearch } from './fleets';
import { useInboxQueue } from './InboxPanel';
import { go, peekPath } from './router';

/** The row above a ship's chat box that says the ship waits on an Inbox item and links to it. */
export function InboxWaiting({ session }: { session: string }) {
  const { entries } = useInboxQueue();
  const open = entries.filter((e) => e.type === 'item' && e.folded === null && e.item?.session === session);
  if (open.length === 0) return null;
  const first = open.reduce((a, b) => ((b.item?.created ?? '') < (a.item?.created ?? '') ? b : a));
  const href = peekPath({ session, item: first.id });
  const search = fleetSearch.value;
  return (
    <div class="inbox-waiting">
      <span class="inbox-waiting-text">
        {open.length > 1 ? `Waiting on ${open.length} Inbox items` : 'Waiting on the Inbox'}
        <span class="inbox-waiting-title"> · {first.item?.title}</span>
      </span>
      <a
        class="btn inbox-waiting-go"
        href={href + search}
        onClick={(ev) => {
          if (ev.defaultPrevented || ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey) return;
          ev.preventDefault();
          go(href, search);
        }}
      >
        Go to Inbox
      </a>
    </div>
  );
}
