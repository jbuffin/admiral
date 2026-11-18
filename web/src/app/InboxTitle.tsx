import type { Entry } from './inbox';

/** A line of an item's title; a drafting ship's name is italic, built from the item's data so no markup in it counts. */
export function InboxTitle({ entry, text }: { entry: Entry; text: string }) {
  const ended = entry.item?.kind === 'drafting_ended' ? entry.item.drafting_ended : undefined;
  if (!ended) return <>{text}</>;
  return (
    <>
      <em>{ended.name}</em> ended without filing a campaign
    </>
  );
}
