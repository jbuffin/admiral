import { useEffect, useRef, useState } from 'preact/hooks';
import { call, type CrewBoat, type Ship } from './api';
import { cardFor } from './answerCard';
import { Composer, useComposer } from './Composer';
import { ConversationView } from './ConversationView';
import { fleetSearch } from './fleets';
import type { Entry } from './inbox';
import { rowShip, rowVerb } from './inboxRow';
import { discardFold, lockedBy, submitFold } from './itemActions';
import { CrewMemberBody, CrewNotesBody, isCrewKind, type MemberStep, readCrew, useCrewNotes } from './PeekCrew';
import { crewRemoveFold } from './crewItems';
import { blankReview, FindingsBody, RoutineProposalBody, useFindings, Failure, RemoveRow, ReviewBody, type ReviewUi, useActing } from './PeekItem';
import { MergeBody } from './PeekMerge';
import { PeekQuestions } from './PeekQuestions';
import { EVENTS } from './PeekItem';
import type { KeyAct } from './peekKeys';
import { bridgeSearch } from './peekLink';
import { go, shipPath, surfacePath } from './router';
import { targetFor, useShipConversation } from './ShipView';
import { lastTurn } from './turns';
import { chats, ships, shipsLoaded } from './shipsLink';
import { SURFACES } from './surfaces';
import { type AnswerBody, answeredQuestion, blankDraft, choose, foldText, pickOf, type Draft } from '../questions';

export const isChatSession = (session: string) => session.startsWith('_chat');
const WITHDRAWN = 'AdmiralBot withdrew this question.';
const ANSWERED = 'This question was already answered.';
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

/** back leaves Peek for the surface that opened it; the Inbox when none is given. */
export function Peek({ entry, fold, back, gone = false }: { entry: Entry; fold: (text: string) => void; back?: { href: string; go: () => void }; gone?: boolean }) {
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
  const crewNotes = useCrewNotes(entry);
  const [member, setMember] = useState<MemberStep>('idle');
  const crew = readCrew(entry);
  const admiral = entry.session === '_admiral';
  const chat = isChatSession(entry.session);
  // A chat question's refusal is told plainly: the server's text names its ids and its internal session.
  const [chatEnd, setChatEnd] = useState<'withdrawn' | 'answered' | null>(null);
  const answeredHere = entry.fleet?.key ? chats.value[entry.fleet.key]?.questions?.some((q) => q.id === entry.id && q.answer) : false;
  const notice = !chat || !question ? '' : chatEnd === 'answered' || (gone && answeredHere) ? ANSWERED : chatEnd === 'withdrawn' || gone ? WITHDRAWN : '';
  const send = async (body: AnswerBody) => {
    setDraft((d) => ({ ...d, sending: true, error: '' }));
    const r = await call('POST', `/inbox/${encodeURIComponent(entry.session)}/${encodeURIComponent(entry.id!)}/answer`, body);
    if (r.ok) return fold(foldText(body));
    if (chat && (r.status === 404 || lockedBy(r.status, r.error))) {
      setChatEnd(r.status === 404 ? 'withdrawn' : 'answered');
      return setDraft((d) => ({ ...d, sending: false }));
    }
    if (lockedBy(r.status, r.error)) setQuestionLocked(true);
    setDraft((d) => ({ ...d, sending: false, error: r.error }));
  };
  const kind = entry.item?.kind;
  const crewNoteIds = (kind === 'crew_notes' ? crewNotes.item.notes : crew.notes).map((n) => n.id);
  // Withdrawing one chat question is not specified, so only a ship's item can leave unanswered.
  const removable = item && kind !== 'notice' && kind !== 'routine_proposal' && !chat;
  const mine = (a: KeyAct) => {
    if (a.kind === 'remove' && removable && !acting.busy) {
      acting.clear();
      setRemoving(true);
    } else if (a.kind === 'pick' && question) {
      if (questionLocked || notice || draft.sending) return;
      const qs = entry.item?.question?.questions ?? [];
      const q = qs.find((x) => !answeredQuestion(pickOf(draft, x)));
      const o = q?.options[a.n - 1];
      if (q && o) setDraft((d) => choose(d, q, o.label));
    } else if (a.kind === 'pick' && kind === 'crew_member') {
      if (acting.busy || acting.locked || a.n > 2) return;
      acting.clear();
      setRemoving(false);
      setMember(a.n === 1 ? 'keep' : 'drop');
    } else if (a.kind === 'pick' && kind === 'review' && review.step !== 'discard' && !acting.busy && !acting.locked && EVENTS[a.n - 1]) {
      acting.clear();
      setRemoving(false);
      setReview((r) => ({ ...r, step: 'confirm', event: EVENTS[a.n - 1][0] }));
    }
  };
  // One stable handler per Peek that calls the latest render's, claimed at render so a key right after navigation finds it.
  const latest = useRef(mine);
  latest.current = mine;
  const slot = useRef((a: KeyAct) => latest.current(a)).current;
  peekKeys.current = slot;
  useEffect(() => () => void (peekKeys.current === slot && (peekKeys.current = null)), []);
  // A refusal that names a note shows on that note's card.
  const noteShown = !removing && crewNoteIds.includes(acting.note);
  const form = !item ? null : (
    <div class="peek-form">
      {question ? (
        <>
          <PeekQuestions questions={entry.item?.question?.questions ?? []} draft={draft} edit={(fn) => setDraft(fn)} onSend={(b) => void send(b)} locked={questionLocked || notice !== ''} />
          {notice ? (
            <p class="line" role="alert">
              {notice}
            </p>
          ) : null}
        </>
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
      ) : kind === 'routine_proposal' ? (
        <RoutineProposalBody entry={entry} acting={acting} />
      ) : kind === 'crew_notes' ? (
        <CrewNotesBody acting={acting} state={crewNotes} decided={() => setRemoving(false)} />
      ) : kind === 'crew_member' ? (
        <CrewMemberBody item={crew} step={member} setStep={setMember} acting={acting} decided={() => setRemoving(false)} />
      ) : null}
      {removable ? (
        <RemoveRow
          entry={entry}
          confirm={removing}
          setConfirm={setRemoving}
          acting={{ ...acting, locked: false }}
          fold={kind === 'crew_member' ? (data: { file_removed?: boolean }) => crewRemoveFold(data, crew.member) : undefined}
        />
      ) : null}
      {kind === 'review' || kind === 'findings' || kind === 'routine_proposal' || removing || (isCrewKind(kind) && !noteShown) ? <Failure message={acting.error} /> : null}
    </div>
  );
  return (
    <section class="peek" role="region" aria-label="Peek">
      <div class="peek-head">
        <a
          class="back-link"
          href={back?.href ?? surfacePath(inboxSurface) + fleetSearch.value}
          onClick={(e) => {
            if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
            e.preventDefault();
            (back?.go ?? toList)();
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
          {isCrewKind(kind) ? (
            <>
              {!entry.fleet && crew.fleet ? <p class="peek-fleet">{crew.fleet} (no longer configured)</p> : null}
              <p class="peek-fleet">{[crew.ship && `from ${crew.ship}`, crew.run_date].filter(Boolean).join(', ')}</p>
            </>
          ) : null}
        </div>
        {ship && !admiral ? (
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
        {entry.type === 'merge' ? (
          ship ? (
            <MergeBody ship={ship} fold={fold} />
          ) : shipsLoaded.value ? (
            <p class="line">This ship is gone.</p>
          ) : null
        ) : ship ? (
          <ShipTurn key={entry.key} ship={ship} boat={boat} fleetKey={fleetKey} item={item}>
            {form}
          </ShipTurn>
        ) : (
          <>
            {form}
            {shipsLoaded.value && !admiral && !isChatSession(entry.session) ? <p class="line">This ship is gone.</p> : null}
          </>
        )}
      </div>
    </section>
  );
}
