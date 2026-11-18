import { useEffect, useRef, useState } from 'preact/hooks';
import { itemConversationUrl, type ConversationPage, type Item } from './api';
import { AddCampaign } from './AddCampaign';
import { Button } from './Button';
import { ConversationView } from './ConversationView';
import type { Conversation } from './conversation';
import type { Entry } from './inbox';

/** The ended ship's conversation, read once through the item's own route: the ship is gone, so nothing follows it over the ships socket. */
function useItemConversation(entry: Entry) {
  const [conv, setConv] = useState<Conversation>({ state: 'loading', items: [], more: false });
  const latest = useRef(conv);
  latest.current = conv;
  useEffect(() => {
    let stopped = false;
    setConv({ state: 'loading', items: [], more: false });
    void (async () => {
      try {
        const res = await fetch(itemConversationUrl(entry.session, entry.id!));
        if (stopped) return;
        if (res.status === 404) return setConv({ state: 'gone', items: [], more: false });
        if (!res.ok) return;
        const page = (await res.json()) as ConversationPage;
        if (!stopped) setConv(page.state === 'ok' ? { state: 'ok', items: page.items ?? [], more: page.more } : { state: page.state, items: [], more: false });
      } catch {
        // The Peek keeps its loading line; opening it again reads again.
      }
    })();
    return () => void (stopped = true);
  }, [entry.session, entry.id]);
  const loadOlder = async () => {
    const first = latest.current.items[0];
    if (!first || !latest.current.more) return;
    try {
      const res = await fetch(itemConversationUrl(entry.session, entry.id!, first.id));
      if (!res.ok) return;
      const page = (await res.json()) as ConversationPage;
      const have = new Set(latest.current.items.map((i: Item) => i.id));
      setConv((c) => ({ ...c, items: [...(page.items ?? []).filter((i) => !have.has(i.id)), ...c.items], more: page.more }));
    } catch {
      // The button stays, so another tap tries again.
    }
  };
  return { conv, loadOlder: () => void loadOlder() };
}

/** What an ended drafting ship said, read-only, with the way to follow an issue by hand. */
export function DraftingEndedBody({ entry, children }: { entry: Entry; children?: preact.ComponentChildren }) {
  const ended = entry.item?.drafting_ended;
  const { conv, loadOlder } = useItemConversation(entry);
  const [following, setFollowing] = useState(false);
  return (
    <>
      <div class="convo-area">
        <ConversationView conv={conv} harness={ended?.harness ?? ''} card={{ kind: 'none' }} session={entry.session} working={false} noneTitle="No conversation recorded" loadOlder={loadOlder} />
      </div>
      <div class="inbox-remove-row">
        <Button label="Follow an issue…" onClick={() => setFollowing(true)} />
      </div>
      {following ? <AddCampaign fleet={ended?.fleet ?? entry.fleet?.key ?? ''} onClose={() => setFollowing(false)} /> : null}
      {children}
    </>
  );
}
