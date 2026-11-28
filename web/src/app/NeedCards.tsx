import { useMemo, useRef, useState } from 'preact/hooks';
import { AllowRulesBody } from './PeekAllowRules';
import { blankReview, CampaignDraftBody, Failure, FindingsBody, ReviewBody, RoutineProposalBody, useFindings, type Acting } from './PeekItem';
import { PeekQuestions } from './PeekQuestions';
import { markHidden } from './crewItems';
import { fleets } from './fleets';
import { ships, inboxItems } from './shipsLink';
import { blankDraft } from '../questions';
import type { Entry, InboxItem } from './inbox';
import type { NeedCard, ThreadItem } from './chat';
import type { useChat } from './useChat';

type Chat = ReturnType<typeof useChat>;
type Need = Extract<ThreadItem, { kind: 'need' }>;

const FLEET_EYEBROW: Record<string, string> = { routine_proposal: 'Routine proposal', allow_rules: 'Allow rules', campaign_draft: 'Campaign draft' };
const noFold = () => '';

// The Inbox routes take an empty body for these verbs; the card route names the choice.
const cardBody = (verb: string, body: unknown) => (verb === 'launch' || verb === 'dismiss' || verb === 'discard' ? { [verb]: true } : body);

/** The Inbox bodies' Acting seam, answering through the card's route in place of the Inbox's. */
function useNeedActing(id: string, chat: Chat): Acting {
  const d = chat.drafts[id];
  return {
    busy: !!d?.sending,
    error: d?.error ?? '',
    note: '',
    locked: false,
    clear: () => chat.editDraft(id, (x) => ({ ...x, error: '' })),
    run: async (verb, body) => void (await chat.answerNeed(id, cardBody(verb, body))),
  };
}

/** The Inbox item the card stands for: the joined one, else what the card itself carries. */
function itemOf(card: NeedCard, joined: InboxItem | undefined, fleet: string): InboxItem {
  const base = { id: card.item, session: 'ship' in card ? card.ship : '_admiral' };
  if (joined) return joined;
  switch (card.item_kind) {
    case 'review':
      return { ...base, kind: 'review', review: { pr_url: card.pr_url, summary: '' } } as InboxItem;
    case 'findings':
      return { ...base, kind: 'findings', findings: { cards: card.cards } } as InboxItem;
    case 'routine_proposal':
      return { ...base, kind: 'routine_proposal', routine_proposal: { action: card.action, reason: card.reason, expires_at: card.expires_at, routine_name: card.routine_name } } as InboxItem;
    case 'allow_rules':
      return { ...base, kind: 'allow_rules', allow_rules: { fleet, cards: card.cards } } as InboxItem;
    case 'campaign_draft':
      return { ...base, kind: 'campaign_draft', campaign_draft: { fleet, repo: card.repo, title: card.title, body: card.body } } as InboxItem;
    default:
      return base as InboxItem;
  }
}

function ReviewCardBody({ entry, acting }: { entry: Entry; acting: Acting }) {
  const [ui, setUi] = useState(blankReview);
  return <ReviewBody entry={entry} ui={ui} set={(p) => setUi((r) => ({ ...r, ...p }))} acting={acting} submit={() => acting.run('submit', { event: ui.event, edited: ui.edited }, noFold)} discard={() => acting.run('discard', {}, noFold)} />;
}

function FindingsCardBody({ entry, acting }: { entry: Entry; acting: Acting }) {
  return <FindingsBody entry={entry} acting={acting} state={useFindings(entry)} />;
}

function OpenNeedCard({ item, chat, fleet }: { item: Need; chat: Chat; fleet: string }) {
  const card = item.card;
  const acting = useNeedActing(item.id, chat);
  const session = 'ship' in card ? card.ship : '_admiral';
  const joined = inboxItems.value.find((i) => i.session === session && i.id === card.item);
  const fleetName = fleets.value.find((f) => f.key === fleet)?.name || fleet;
  // A reread serves a new object for the same card; the body keeps its decisions only while the item it reads is the same one.
  const held = useRef(card);
  if (JSON.stringify(held.current) !== JSON.stringify(card)) held.current = card;
  const stable = held.current;
  const entry = useMemo<Entry>(() => ({ key: `need:${item.id}`, type: 'item', session, id: stable.item, item: itemOf(stable, joined, fleet), folded: null, since: 0, fleet: { key: fleet, name: fleetName, color: '' } }), [item.id, session, stable, joined, fleet, fleetName]);
  const title = markHidden(item.text).text;
  const name = ships.value.find((s) => s.session === session)?.name || session;
  const eyebrow = FLEET_EYEBROW[card.item_kind] ?? name;
  const details = (card.item_kind === 'review' || card.item_kind === 'findings') && !joined;
  if (card.item_kind === 'question') {
    const draft = chat.drafts[item.id] ?? blankDraft();
    return (
      <div class="convo-card chat-card">
        <p class="chat-card-eyebrow">{eyebrow}</p>
        {card.questions.length === 1 && item.text.trim() === card.questions[0].text.trim() ? null : <p class="line">{title}</p>}
        <PeekQuestions questions={card.questions} draft={draft} edit={(fn) => chat.editDraft(item.id, fn)} onSend={(b) => void chat.answerNeed(item.id, b)} />
      </div>
    );
  }
  return (
    <div class="convo-card chat-card">
      <p class="chat-card-eyebrow">{eyebrow}</p>
      <p class="line">{title}</p>
      {details ? <p class="inbox-hint">Details are in the Inbox</p> : null}
      {card.item_kind === 'review' ? (
        <ReviewCardBody entry={entry} acting={acting} />
      ) : card.item_kind === 'findings' ? (
        <FindingsCardBody key={joined ? 'joined' : 'bare'} entry={entry} acting={acting} />
      ) : card.item_kind === 'routine_proposal' ? (
        <RoutineProposalBody entry={entry} acting={acting} />
      ) : card.item_kind === 'allow_rules' ? (
        <AllowRulesBody entry={entry} acting={acting} fleet={fleetName} />
      ) : (
        <CampaignDraftBody entry={entry} acting={acting} fold={noFold} createWith={(b) => chat.answerNeed(item.id, b, true)} />
      )}
      <Failure message={acting.error} />
    </div>
  );
}

const WORDS = { removed: 'Removed', moved_on: 'Moved on' } as const;

export function NeedCardView({ item, chat, fleet }: { item: Need; chat: Chat; fleet: string }) {
  const { card } = item;
  if (card.state === 'open') return <OpenNeedCard item={item} chat={chat} fleet={fleet} />;
  const words = card.state === 'answered' ? (card.answered_in === 'inbox' ? 'Answered in the Inbox' : 'Answered in the chat') : WORDS[card.state];
  const name = 'ship' in card ? ships.value.find((s) => s.session === card.ship)?.name || card.ship : '';
  return (
    <div class="convo-card chat-card quiet">
      <p class="convo-card-title">{markHidden(item.text).text}</p>
      <p class="line">{name ? `${name} · ${words}` : words}</p>
    </div>
  );
}
