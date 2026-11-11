import { useEffect, useRef, useState } from 'preact/hooks';
import { call, type CrewBoat, type Ship } from './api';
import { cardFor } from './answerCard';
import { Composer, useComposer } from './Composer';
import { ConversationView } from './ConversationView';
import { fleetSearch } from './fleets';
import type { Entry } from './inbox';
import { rowShip, rowVerb } from './inboxRow';
import { discardFold, lockedBy, submitFold } from './itemActions';
import { blankReview, FindingsBody, useFindings, Failure, RemoveRow, ReviewBody, type ReviewUi, useActing } from './PeekItem';
import { PeekQuestions } from './PeekQuestions';
import { EVENTS } from './PeekItem';
import type { KeyAct } from './peekKeys';
import { bridgeSearch } from './peekLink';
import { go, shipPath, surfacePath } from './router';
import { targetFor, useShipConversation } from './ShipView';
import { lastTurn } from './turns';
import { ships, shipsLoaded } from './shipsLink';
import { SURFACES } from './surfaces';
import { type AnswerBody, answeredQuestion, blankDraft, choose, foldText, pickOf, type Draft } from '../questions';

const inboxSurface = SURFACES.find((s) => s.id === 'inbox')!;
/** The open Peek's answer to a key the Inbox heard; the Inbox owns the listener. */
export const peekKeys: { current: ((a: KeyAct) => void) | null } = { current: null };

const toList = () => go(surfacePath(inboxSurface), fleetSearch.value);

/** The ship's last turn with its card and message box; a file item's form sits between the turn and the box. */
function ShipTurn({ ship, boat, fleetKey, item, children }: { ship: Ship; boat?: CrewBoat; fleetKey?: string; item: boolean; children?: preact.ComponentChildren }) {
  const { conv, loadOlder } = useShipConversation(ship, boat);
  const target = targetFor(ship, boat, conv);
  const box = useComposer(target, conv);
  // Only what the chat shows is cut; the card and the box read the whole conversation.
  const shown = { ...conv, items: lastTurn(conv.items), more: false };
  const readable = conv.state === 'ok';
  if (item && !readable) return <>{children}</>;
  return (
    <>
      <div class="convo-area">
        <ConversationView
          conv={shown}
          harness={(boat ?? ship).harness}
          card={item ? { kind: 'none' } : cardFor(ship, boat, conv)}
          session={ship.session}
          crew={boat?.name}
          anchored={boat ? boat.status === 'anchored' : ship.status === 'anchored'}
          working={(boat ?? ship).status === 'underway'}
          noneTitle={boat ? 'No conversation for this boat' : undefined}
          openTerminal={() => go(shipPath(ship.session), bridgeSearch(fleetKey, 'terminal'))}
          loadOlder={loadOlder}
        />
      </div>
      {children}
      <Composer target={target} box={box} />
    </>
  );
}

export function Peek({ entry, fold }: { entry: Entry; fold: (text: string) => void }) {
  const ship = ships.value.find((s) => s.session === entry.session);
  const boat = entry.crew ? ship?.crew.find((b) => b.name === entry.crew!.name) : undefined;
  const fleetKey = entry.fleet?.key ?? ship?.fleet;
  const item = entry.type === 'item';
  const title = entry.item?.title;
  const question = entry.item?.kind === 'question';
  // The draft lives here, above the ship's arrival, so it survives ShipTurn replacing the bare form.
  const [draft, setDraft] = useState<Draft>(blankDraft);
  const [questionLocked, setQuestionLocked] = useState(false);
  const [review, setReview] = useState<ReviewUi>(blankReview);
  const [removing, setRemoving] = useState(false);
  const acting = useActing(entry, fold, lockedBy);
  const findings = useFindings(entry);
  const send = async (body: AnswerBody) => {
    setDraft((d) => ({ ...d, sending: true, error: '' }));
    const r = await call('POST', `/inbox/${encodeURIComponent(entry.session)}/${encodeURIComponent(entry.id!)}/answer`, body);
    if (r.ok) return fold(foldText(body));
    if (lockedBy(r.status, r.error)) setQuestionLocked(true);
    setDraft((d) => ({ ...d, sending: false, error: r.error }));
  };
  const kind = entry.item?.kind;
  const removable = item && kind !== 'notice';
  const mine = (a: KeyAct) => {
    if (a.kind === 'remove' && removable && !acting.busy) {
      acting.clear();
      setRemoving(true);
    } else if (a.kind === 'pick' && question) {
      if (questionLocked || draft.sending) return;
      const qs = entry.item?.question?.questions ?? [];
      const q = qs.find((x) => !answeredQuestion(pickOf(draft, x)));
      const o = q?.options[a.n - 1];
      if (q && o) setDraft((d) => choose(d, q, o.label));
    } else if (a.kind === 'pick' && kind === 'review' && review.step !== 'discard' && !acting.busy && !acting.locked && EVENTS[a.n - 1]) {
      acting.clear();
      setRemoving(false);
      setReview((r) => ({ ...r, step: 'confirm', event: EVENTS[a.n - 1][0] }));
    }
  };
  // The slot calls the latest render's handler, so it is claimed once per Peek.
  const latest = useRef(mine);
  latest.current = mine;
  useEffect(() => {
    const slot = (a: KeyAct) => latest.current(a);
    peekKeys.current = slot;
    return () => void (peekKeys.current === slot && (peekKeys.current = null));
  }, []);
  const form = !item ? null : (
    <div class="peek-form">
      {question ? (
        <PeekQuestions questions={entry.item?.question?.questions ?? []} draft={draft} edit={(fn) => setDraft(fn)} onSend={(b) => void send(b)} locked={questionLocked} />
      ) : kind === 'review' ? (
        <ReviewBody
          entry={entry}
          ui={review}
          set={(p) => setReview((r) => ({ ...r, ...p }))}
          acting={acting}
          submit={() => void acting.run('submit', { event: review.event, edited: review.edited }, (d) => submitFold(d, review.event))}
          discard={() => void acting.run('discard', {}, discardFold)}
        />
      ) : kind === 'findings' ? (
        <FindingsBody entry={entry} acting={acting} state={findings} />
      ) : null}
      {removable ? <RemoveRow entry={entry} confirm={removing} setConfirm={setRemoving} acting={{ ...acting, locked: false }} /> : null}
      {kind === 'review' || kind === 'findings' || removing ? <Failure message={acting.error} /> : null}
    </div>
  );
  return (
    <section class="peek" role="region" aria-label="Peek">
      <div class="peek-head">
        <a
          class="back-link"
          href={surfacePath(inboxSurface) + fleetSearch.value}
          onClick={(e) => {
            if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
            e.preventDefault();
            toList();
          }}
        >
          Back
        </a>
        <div class="peek-who">
          <h2>
            {rowShip(entry)} <span class="inbox-verb">{rowVerb(entry)}</span>
          </h2>
          {title ? <p class="peek-title">{title}</p> : null}
          {entry.fleet?.name ? <p class="peek-fleet">{entry.fleet.name}</p> : null}
        </div>
        {ship ? (
          <a
            class="btn peek-bridge"
            href={shipPath(ship.session) + bridgeSearch(fleetKey)}
            onClick={(e) => {
              if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
              e.preventDefault();
              go(shipPath(ship.session), bridgeSearch(fleetKey));
            }}
          >
            Open bridge
          </a>
        ) : null}
      </div>
      <div class="peek-body">
        {ship ? (
          <ShipTurn key={entry.key} ship={ship} boat={boat} fleetKey={fleetKey} item={item}>
            {form}
          </ShipTurn>
        ) : (
          <>
            {form}
            {shipsLoaded.value ? <p class="line">This ship is gone.</p> : null}
          </>
        )}
      </div>
    </section>
  );
}
