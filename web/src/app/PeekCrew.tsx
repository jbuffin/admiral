import { useRef, useState } from 'preact/hooks';
import type { Entry } from './inbox';
import { createNoteDecisions, crewMemberFold, missionFold, type MissionItem, type CrewMemberItem, type CrewNote, type NoteEdit, markHidden, noteEditProblems, NOTE_MAX, readCrewMember, readCrewNotes } from './crewItems';
import { Actions, Button } from './Button';
import { type Acting, itemKey } from './PeekItem';

// Every string below was written by a ship and renders as text.

export type MemberStep = 'idle' | 'keep' | 'drop';

export const isCrewKind = (kind: string | undefined) => kind === 'crew_notes' || kind === 'crew_member' || kind === 'mission';

/** The item's crew object, which both kinds share the head fields of. */
export const readCrew = (entry: Entry): CrewMemberItem => readCrewMember(entry.item?.kind === 'crew_member' ? entry.item.crew_member : entry.item?.kind === 'mission' ? entry.item.mission : entry.item?.crew_notes);

export function useCrewNotes(entry: Entry) {
  const item = useRef(readCrewNotes(entry.item?.kind === 'crew_notes' ? entry.item.crew_notes : undefined)).current;
  const d = useRef(createNoteDecisions(item.notes)).current;
  const [, setRev] = useState(0);
  const [drafts, setDrafts] = useState<Record<string, NoteEdit>>({});
  return { item, d, drafts, setDrafts, changed: () => setRev((n) => n + 1) };
}
export type CrewNotesState = ReturnType<typeof useCrewNotes>;

const scopeLine = (n: Pick<CrewNote, 'scope' | 'repo'>) => (n.scope === 'repo' ? `Repo ${n.repo ?? ''}` : 'Fleet');

const shown = (text: string) => markHidden(text).text;

function NoteText({ n, edited }: { n: CrewNote; edited?: NoteEdit }) {
  const cur = edited ?? n;
  return (
    <>
      <span class="inbox-eyebrow">
        {n.member}
        {n.role ? ` · ${n.role}` : ''}
        {edited ? ' · edited' : ''}
      </span>
      <span class="inbox-eyebrow">{scopeLine(cur)}</span>
      <p class="inbox-why">{shown(cur.note)}</p>
      {n.replaces !== null && (
        <p class="inbox-why">
          <b>Replaces:</b> {shown(n.replaces)}
        </p>
      )}
    </>
  );
}

function EditForm({ n, i, draft, setDraft, save, cancel, locked }: { n: CrewNote; i: number; draft: NoteEdit; setDraft: (e: NoteEdit) => void; save: () => void; cancel: () => void; locked: boolean }) {
  const problems = noteEditProblems(draft);
  return (
    <div class="inbox-proposal">
      <textarea class="inbox-note" aria-label={`Edit note ${i}`} rows={3} value={draft.note} readOnly={locked} onInput={(e) => setDraft({ ...draft, note: (e.target as HTMLTextAreaElement).value })} />
      {problems.note && <span class="inbox-hint">{problems.note}</span>}
      <div class="inbox-send-row" role="group" aria-label={`Scope for note ${i}`}>
        {(['fleet', 'repo'] as const).map((s) => (
          <Button key={s} label={s === 'fleet' ? 'Fleet' : 'Repo'} pressed={draft.scope === s} disabled={locked} onClick={() => setDraft({ ...draft, scope: s })} />
        ))}
      </div>
      {draft.scope === 'repo' && (
        <>
          <input class="inbox-input" aria-label="Repo (owner/repo)" placeholder="owner/repo" value={draft.repo ?? ''} readOnly={locked} onInput={(e) => setDraft({ ...draft, repo: (e.target as HTMLInputElement).value })} />
          {problems.repo && <span class="inbox-hint">{problems.repo}</span>}
        </>
      )}
      <div class="inbox-send-row">
        <Button label="Save" variant="primary" disabled={locked || Object.keys(problems).length > 0} onClick={save} />
        <Button label="Cancel" onClick={cancel} />
      </div>
      <span class="inbox-hint">At most {NOTE_MAX} characters, on one line. {n.replaces !== null ? 'The line it replaces stays as proposed.' : ''}</span>
    </div>
  );
}

export function CrewNotesBody({ entry, acting, state, decided: closeRemove }: { entry: Entry; acting: Acting; state: CrewNotesState; decided: () => void }) {
  const { item, d, drafts, setDrafts, changed } = state;
  const { decided, total } = d.progress();
  const open = Object.keys(drafts).length > 0;
  const off = acting.busy || acting.locked;
  const setDraft = (id: string, e: NoteEdit | undefined) =>
    setDrafts((m) => {
      const next = { ...m };
      if (e === undefined) delete next[id];
      else next[id] = e;
      return next;
    });
  const send = () => {
    if (!d.canSend() || open) return;
    const counts = { keep: 0, edit: 0, drop: 0 };
    for (const n of item.notes) counts[d.decision(n.id)!]++;
    const text = `Crew notes sent: ${counts.keep} kept, ${counts.edit} edited, ${counts.drop} dropped`;
    return acting.run('crew-notes', d.payload(), () => text);
  };
  return (
    <>
      {item.notes.map((n, idx) => {
        const i = idx + 1;
        const dec = d.decision(n.id);
        const draft = drafts[n.id];
        const refused = acting.note === n.id;
        const pick = (v: 'keep' | 'drop') => {
          if (refused) acting.clear();
          closeRemove();
          setDraft(n.id, undefined);
          if (v === 'keep') d.keep(n.id);
          else d.drop(n.id);
          changed();
        };
        return (
          <section key={n.id} class="inbox-finding" aria-label={`Note card ${i}`}>
            <NoteText n={n} edited={dec === 'edit' && draft === undefined ? d.edit(n.id) : undefined} />
            {draft !== undefined && (
              <EditForm
                n={n}
                i={i}
                draft={draft}
                setDraft={(e) => setDraft(n.id, e)}
                locked={off}
                cancel={() => setDraft(n.id, undefined)}
                save={() => {
                  if (d.save(n.id, draft)) setDraft(n.id, undefined);
                  changed();
                }}
              />
            )}
            {acting.note === n.id && acting.error && (
              <p class="inbox-error" role="alert">
                {acting.error}
              </p>
            )}
            <div class="inbox-send-row" role="group" aria-label={`Decision for note ${i}`}>
              <Button label="Keep" pressed={dec === 'keep'} disabled={off} onClick={() => pick('keep')} />
              <Button
                label="Edit"
                pressed={dec === 'edit'}
                disabled={off}
                onClick={() => {
                  if (refused) acting.clear();
                  closeRemove();
                  setDraft(n.id, d.edit(n.id) ?? { note: n.note, scope: n.scope, repo: n.repo });
                }}
              />
              <Button label="Drop" pressed={dec === 'drop'} disabled={off} onClick={() => pick('drop')} />
            </div>
          </section>
        );
      })}
      <div class="inbox-send-row">
        <Button label="Send" variant="primary" pending={itemKey(entry, 'crew-notes')} disabled={!d.canSend() || open || off} onClick={send} />
        <span class="inbox-progress">
          {decided} of {total} decided
        </span>
        {open && <span class="inbox-hint">Save or Cancel the open edit first.</span>}
      </div>
    </>
  );
}

const CONFIRM: Record<'keep' | 'drop', string> = {
  keep: "This adds the member to the fleet's crew for every later run and keeps its notes.",
  drop: 'The ad hoc file is deleted and its notes are discarded.',
};

export function CrewMemberBody({ entry, item, step, setStep, acting, decided }: { entry: Entry; item: CrewMemberItem; step: MemberStep; setStep: (s: MemberStep) => void; acting: Acting; decided: () => void }) {
  const off = acting.busy || acting.locked;
  const go = (s: MemberStep) => {
    acting.clear();
    if (s !== 'idle') decided();
    setStep(s);
  };
  const hidden = markHidden(item.definition);
  const answer = (decision: 'keep' | 'drop') => acting.run('crew-member', { decision }, (data) => crewMemberFold(decision, item.member, data));
  return (
    <>
      <div class="inbox-summary">
        <span class="inbox-eyebrow">Ad hoc member</span>
        <p class="inbox-summary-text">
          <b>{item.member}</b>
          {item.role ? ` · ${item.role}` : ''}
        </p>
        {item.ad_hoc_run && <span class="inbox-hint">Ad hoc run {item.ad_hoc_run}</span>}
      </div>
      <div class="inbox-proposal">
        <span class="inbox-eyebrow">Definition</span>
        {hidden.count > 0 && <span class="inbox-hint">This definition has {hidden.count} hidden characters, shown as ⟨U+…⟩.</span>}
        <pre class="inbox-pre inbox-definition">{hidden.text}</pre>
      </div>
      {item.notes.map((n, idx) => (
        <section key={n.id} class="inbox-finding" aria-label={`Note card ${idx + 1}`}>
          <NoteText n={n} />
          {acting.note === n.id && acting.error && (
            <p class="inbox-error" role="alert">
              {`${acting.error} These notes can't be edited here: Drop or Remove the item.`}
            </p>
          )}
        </section>
      ))}
      {step === 'idle' && (
        <div class="inbox-send-row">
          <Button label="Keep" variant="primary" disabled={off} onClick={() => go('keep')} />
          <Button label="Drop" variant="danger" disabled={off} onClick={() => go('drop')} />
        </div>
      )}
      {step !== 'idle' && (
        <div class={`inbox-ask confirm${step === 'drop' ? ' discard' : ''}`}>
          <p>{CONFIRM[step]}</p>
          <Actions class="inbox-send-row">
            <Button label={`${step === 'keep' ? 'Keep' : 'Drop'} ${item.member}`} variant={step === 'keep' ? 'primary' : 'danger'} pending={itemKey(entry, `crew-member:${step}`)} disabled={acting.locked} onClick={() => answer(step)} />
            <Button label="Cancel" onClick={() => go('idle')} />
          </Actions>
        </div>
      )}
    </>
  );
}

const MISSION_CONFIRM: Record<'keep' | 'drop', string> = {
  keep: 'The mission is saved for every later ship in the fleet.',
  drop: 'The mission is discarded and nothing is saved.',
};

export function MissionBody({ entry, item, step, setStep, acting, decided }: { entry: Entry; item: MissionItem; step: MemberStep; setStep: (s: MemberStep) => void; acting: Acting; decided: () => void }) {
  const off = acting.busy || acting.locked;
  const go = (s: MemberStep) => {
    acting.clear();
    if (s !== 'idle') decided();
    setStep(s);
  };
  const hidden = markHidden(item.text);
  const answer = (decision: 'keep' | 'drop') => acting.run('mission', { decision }, (data) => missionFold(decision, item.name, data));
  return (
    <>
      <div class="inbox-summary">
        <span class="inbox-eyebrow">Mission</span>
        <p class="inbox-summary-text">
          <b>{item.name}</b>
        </p>
        <span class="inbox-hint">{scopeLine(item)}</span>
      </div>
      <div class="inbox-proposal">
        <span class="inbox-eyebrow">Mission text</span>
        {hidden.count > 0 && <span class="inbox-hint">This mission has {hidden.count} hidden characters, shown as ⟨U+…⟩.</span>}
        <pre class="inbox-pre inbox-definition">{hidden.text}</pre>
      </div>
      {step === 'idle' && (
        <div class="inbox-send-row">
          <Button label="Keep" variant="primary" disabled={off} onClick={() => go('keep')} />
          <Button label="Drop" variant="danger" disabled={off} onClick={() => go('drop')} />
        </div>
      )}
      {step !== 'idle' && (
        <div class={`inbox-ask confirm${step === 'drop' ? ' discard' : ''}`}>
          <p>{MISSION_CONFIRM[step]}</p>
          <Actions class="inbox-send-row">
            <Button label={`${step === 'keep' ? 'Keep' : 'Drop'} ${item.name}`} variant={step === 'keep' ? 'primary' : 'danger'} pending={itemKey(entry, `mission:${step}`)} disabled={acting.locked} onClick={() => answer(step)} />
            <Button label="Cancel" onClick={() => go('idle')} />
          </Actions>
        </div>
      )}
    </>
  );
}
