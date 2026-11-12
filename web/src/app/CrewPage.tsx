import type { ComponentChildren } from 'preact';
import { useEffect, useId, useReducer, useRef, useState } from 'preact/hooks';
import { call, type Fleet, type Result } from './api';
import { Flag } from './Flag';
import { fleetSearch } from './fleets';
import { crewPath, go, loc } from './router';
import { Sheet } from './Sheet';
import {
  ADD_CREW_START,
  BASED_ON_ROLES,
  CUSTOM_ROLE,
  HARNESSES,
  ROLES,
  addCrewStep,
  addShownFields,
  canDraft,
  createBody,
  createMember,
  crewApi,
  draftCrew,
  fieldError,
  formOf,
  groupMembers,
  memberBody,
  moveChoices,
  moveTarget,
  noteCount,
  normalizeCrew,
  scopesOf,
  shownFields,
  splitNote,
  type CrewData,
  type CrewMember,
  type DraftProblem,
  type NewMemberForm,
  type Notes,
  type Scope,
} from './crew';

const CREW_COLORS = 5;

function Avatar({ name }: { name: string }) {
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) % 9973;
  return (
    <svg class="crew-avatar" width="22" height="22" viewBox="0 0 8.5 8.5" aria-hidden="true" style={{ '--crew': `var(--crew-${h % CREW_COLORS}, #7FC8FF)` }}>
      <rect x="0.8" y="0.8" width="6.9" height="6.9" rx="1.3" style="fill:var(--crew,#7FC8FF)" />
      <rect x="2.4" y="3.1" width="1.2" height="1.2" fill="#14304A" />
      <rect x="4.9" y="3.1" width="1.2" height="1.2" fill="#14304A" />
    </svg>
  );
}

function roleLine(m: CrewMember) {
  return ROLES.includes(m.role) || !m['based-on'] ? m.role : `${m.role}, based on ${m['based-on']}`;
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;

function MemberRow({ m, current }: { m: CrewMember; current: boolean }) {
  const search = fleetSearch.value;
  const n = noteCount(m.notes);
  return (
    <li class="crew-item">
      <a
        class="crew-row"
        href={crewPath(m.name) + search}
        aria-current={current ? 'page' : undefined}
        onClick={(e) => {
          if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
          e.preventDefault();
          go(crewPath(m.name), search);
        }}
      >
        <Avatar name={m.name} />
        <span class="crew-main">
          <span class="crew-title">
            <span class="crew-name">{m.name}</span>
            {m.customized ? <span class="pill">customized</span> : null}
          </span>
          <span class="crew-sub">{roleLine(m)}</span>
          {m['pick-me-when'] ? <span class="crew-pick">{m['pick-me-when']}</span> : null}
        </span>
        <span class="crew-side">
          {m.harness ? <Flag harness={m.harness} size="row" label={`runs on ${m.harness}`} /> : null}
          {n > 0 ? <span class="crew-count">{plural(n, 'note')}</span> : null}
        </span>
      </a>
    </li>
  );
}

function Group({ id, title, empty, members, selected }: { id: string; title: string; empty: string; members: CrewMember[]; selected: string | null }) {
  return (
    <section class="crew-group" aria-labelledby={id}>
      <h2 id={id} class="group-head">
        {title} <span class="count">{members.length}</span>
      </h2>
      {members.length === 0 ? (
        empty ? <p class="muted">{empty}</p> : null
      ) : (
        <ul class="ship-list">
          {members.map((m) => (
            <MemberRow key={m.name} m={m} current={m.name === selected} />
          ))}
        </ul>
      )}
    </section>
  );
}

function Field({ label, error, children }: { label: string; error?: string; children: (a: { id: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean }) => ComponentChildren }) {
  const id = useId();
  const errId = `${id}-err`;
  return (
    <div class="field crew-field">
      <label for={id}>{label}</label>
      {children({ id, 'aria-describedby': error ? errId : undefined, 'aria-invalid': error ? true : undefined })}
      {error ? (
        <p id={errId} class="note error">
          {error}
        </p>
      ) : null}
    </div>
  );
}

const PROBLEM_FIELD: Partial<Record<keyof NewMemberForm, string>> = { customRole: 'role', basedOn: 'based-on', pickMeWhen: 'pick-me-when' };

/** The form an edit and an Add crew draft share. A draft's problems sit beside their fields until the field changes. */
function MemberEditor({
  label,
  initial,
  named,
  lockedTo,
  problems = [],
  saveLabel = 'Save',
  secondaryLabel,
  onSecondary,
  onSubmit,
  onSaved,
}: {
  label: string;
  initial: NewMemberForm;
  named: boolean;
  lockedTo?: string;
  problems?: DraftProblem[];
  saveLabel?: string;
  secondaryLabel: string;
  onSecondary: () => void;
  onSubmit: (f: NewMemberForm) => Promise<Result<unknown>>;
  onSaved: (f: NewMemberForm) => Promise<void> | void;
}) {
  const [f, setF] = useState<NewMemberForm>(initial);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<{ field: string | null; message: string } | null>(null);
  const [open, setOpen] = useState<DraftProblem[]>(problems);
  const shown = named ? addShownFields(f.role) : shownFields(f.role);
  const set = (k: keyof NewMemberForm) => (e: Event) => {
    const field = PROBLEM_FIELD[k] ?? k;
    setF({ ...f, [k]: (e.currentTarget as HTMLInputElement).value });
    setOpen(open.filter((p) => p.field !== field));
  };
  const custom = f.role === CUSTOM_ROLE;
  const fixChecker = f.role === 'fix-checker';
  const at = (field: string) => (err?.field === field ? err.message : open.find((p) => p.field === field)?.message);
  const unplaced = open.filter((p) => !shown.includes(p.field));
  const save = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    const r = await onSubmit(f);
    if (!r.ok) {
      setErr(fieldError(r, shown));
      setBusy(false);
      return;
    }
    await onSaved(f);
  };
  return (
    <form class="crew-form" aria-label={label} onSubmit={save}>
      {err && err.field === null ? (
        <p class="alert" role="alert">
          {err.message}
        </p>
      ) : null}
      {unplaced.map((p) => (
        <p key={p.field} class="note error">
          {p.message}
        </p>
      ))}
      {lockedTo ? (
        <p class="note">
          {custom ? 'Role and based on come' : 'Role comes'} from the built-in {lockedTo}, so they stay as they are.
        </p>
      ) : null}
      {named ? (
        <Field label="Name" error={at('name')}>
          {(a) => <input {...a} type="text" value={f.name} autocapitalize="off" autocomplete="off" spellcheck={false} onInput={set('name')} />}
        </Field>
      ) : null}
      <Field label="Role" error={at('role')}>
        {(a) => (
          <select {...a} value={f.role} disabled={!!lockedTo} onChange={set('role')}>
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
            <option value={CUSTOM_ROLE}>Custom…</option>
          </select>
        )}
      </Field>
      {custom ? (
        <Field label="Custom role" error={at('role')}>
          {(a) => <input {...a} type="text" value={f.customRole} readOnly={!!lockedTo} onInput={set('customRole')} />}
        </Field>
      ) : null}
      {custom ? (
        <Field label="Based on" error={at('based-on')}>
          {(a) => (
            <select {...a} value={f.basedOn} disabled={!!lockedTo} onChange={set('basedOn')}>
              {f.basedOn === '' ? <option value="">Choose a built-in role</option> : null}
              {BASED_ON_ROLES.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          )}
        </Field>
      ) : null}
      <Field label="Pick me when" error={at('pick-me-when')}>
        {(a) => <input {...a} type="text" value={f.pickMeWhen} onInput={set('pickMeWhen')} />}
      </Field>
      {fixChecker ? null : (
        <Field label="Skills" error={at('skills')}>
          {(a) => <textarea {...a} rows={3} value={f.skills} onInput={set('skills')} />}
        </Field>
      )}
      <div class="pair">
        <Field label="Model" error={at('model')}>
          {(a) => <input {...a} type="text" value={f.model} onInput={set('model')} />}
        </Field>
        <Field label="Harness" error={at('harness')}>
          {(a) => (
            <select {...a} value={f.harness} onChange={set('harness')}>
              <option value="">none</option>
              {f.harness && !HARNESSES.includes(f.harness) ? <option value={f.harness}>{f.harness}</option> : null}
              {HARNESSES.map((h) => (
                <option key={h} value={h}>
                  {h}
                </option>
              ))}
            </select>
          )}
        </Field>
      </div>
      {fixChecker ? null : (
        <Field label="Instructions" error={at('instructions')}>
          {(a) => <textarea {...a} rows={8} value={f.instructions} onInput={set('instructions')} />}
        </Field>
      )}
      <div class="crew-actions">
        <button type="submit" class="btn primary" disabled={busy}>
          {saveLabel}
        </button>
        <button type="button" class="btn" disabled={busy} onClick={onSecondary}>
          {secondaryLabel}
        </button>
      </div>
    </form>
  );
}

function EditForm({ member, fleetKey, harness, onDone, onSaved }: { member: CrewMember; fleetKey: string; harness: string; onDone: () => void; onSaved: () => Promise<void> }) {
  return (
    <MemberEditor
      label={`Edit ${member.name}`}
      initial={{ ...formOf(member), name: '' }}
      named={false}
      lockedTo={member.customized ? member.name : undefined}
      secondaryLabel="Cancel"
      onSecondary={onDone}
      onSubmit={(f) => call('PUT', crewApi(fleetKey, harness, `/members/${encodeURIComponent(member.name)}`), memberBody(f))}
      onSaved={async () => {
        await onSaved();
        onDone();
      }}
    />
  );
}

/** Describe, draft, review: nothing reaches disk until the editor's Save. */
function AddCrewPanel({ fleetKey, onClose, onCreated }: { fleetKey: string; onClose: () => void; onCreated: (name: string) => Promise<void> }) {
  const [s, send] = useReducer(addCrewStep, ADD_CREW_START);
  const abort = useRef<AbortController | null>(null);
  const textId = useId();
  const drafting = s.phase === 'drafting';
  useEffect(() => {
    if (!drafting) return;
    const t = setInterval(() => send({ type: 'tick' }), 1000);
    return () => clearInterval(t);
  }, [drafting]);
  useEffect(() => () => abort.current?.abort(), []);
  const draft = async () => {
    if (!canDraft(s.description)) return;
    send({ type: 'draft' });
    abort.current?.abort();
    const ctl = new AbortController();
    abort.current = ctl;
    const result = await draftCrew(fleetKey, s.description, ctl.signal);
    if (abort.current === ctl) send({ type: 'result', result });
  };
  const stop = () => {
    abort.current?.abort();
    abort.current = null;
    send({ type: 'stop' });
  };
  const describing = s.phase === 'describe' || s.phase === 'failed' || s.phase === 'nojudge';
  return (
    <section class="crew-add" aria-label="Add crew">
      <h2 class="crew-add-head">Add crew</h2>
      {s.phase === 'draft' && s.form ? (
        <>
          <p class="note">Drafted from your description. Check every field before you save; nothing is written until you do.</p>
          <MemberEditor
            label="New crew member"
            initial={s.form}
            named
            problems={s.problems}
            secondaryLabel="Discard"
            onSecondary={() => send({ type: 'discard' })}
            onSubmit={(f) => createMember(fleetKey, createBody(f))}
            onSaved={(f) => onCreated(f.name.trim())}
          />
        </>
      ) : (
        <>
          <div class="field crew-field">
            <label for={textId}>Describe the member</label>
            <textarea id={textId} rows={6} value={s.description} readOnly={drafting} onInput={(e) => send({ type: 'edit', description: (e.currentTarget as HTMLTextAreaElement).value })} />
          </div>
          {s.phase === 'nojudge' ? (
            <p class="alert" role="alert">
              Drafting needs a harness proven to run with no tools and no network. Run the harness checks in Settings.{s.reason ? ` ${s.reason}` : ''}
            </p>
          ) : null}
          {s.phase === 'failed' ? (
            <p class="alert" role="alert">
              {s.error}
            </p>
          ) : null}
          {drafting ? (
            <p class="line" role="status">
              Drafting… {s.seconds}s
            </p>
          ) : null}
          <div class="crew-actions">
            {drafting ? (
              <button type="button" class="btn" onClick={stop}>
                Stop
              </button>
            ) : (
              <button type="button" class="btn primary" disabled={!canDraft(s.description)} onClick={() => void draft()}>
                {s.phase === 'failed' ? 'Try again' : 'Draft'}
              </button>
            )}
            {describing ? (
              <button type="button" class="btn" onClick={onClose}>
                Cancel
              </button>
            ) : null}
          </div>
        </>
      )}
    </section>
  );
}

function RenameSheet({ member, fleetKey, harness, onClose, onRenamed }: { member: CrewMember; fleetKey: string; harness: string; onClose: () => void; onRenamed: (to: string) => Promise<void> }) {
  const [to, setTo] = useState(member.name);
  const [carry, setCarry] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submit = async (e: Event) => {
    e.preventDefault();
    const name = to.trim();
    setBusy(true);
    setError('');
    const r = await call('POST', crewApi(fleetKey, harness, `/members/${encodeURIComponent(member.name)}/rename`), { to: name, carry_notes: carry });
    if (!r.ok) {
      setError(r.error);
      setBusy(false);
      return;
    }
    await onRenamed(name);
  };
  const left = member.customized ? `with the built-in ${member.name}` : 'under Unclaimed';
  return (
    <Sheet label={`Rename ${member.name}`} onClose={onClose}>
      <form class="crew-form" onSubmit={submit}>
        <Field label="New name">{(a) => <input {...a} type="text" value={to} autocapitalize="none" autocomplete="off" onInput={(e) => setTo((e.currentTarget as HTMLInputElement).value)} />}</Field>
        <label class="check">
          <input type="checkbox" checked={carry} onChange={(e) => setCarry((e.currentTarget as HTMLInputElement).checked)} />
          Carry notes
        </label>
        <p class="note">Checked moves its notes to the new name; unchecked leaves the notes {left}.</p>
        {error ? (
          <p class="alert" role="alert">
            {error}
          </p>
        ) : null}
        <div class="crew-actions">
          <button type="submit" class="btn primary" disabled={busy || !to.trim() || to.trim() === member.name}>
            Rename
          </button>
          <button type="button" class="btn" disabled={busy} onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Sheet>
  );
}

type NoteMode = { kind: 'edit'; text: string } | { kind: 'move'; to: string; typed: string };

function NoteItem({ text, scope, notes, mode, setMode, busy, error, run }: { text: string; scope: Scope; notes: Notes; mode: NoteMode | null; setMode: (m: NoteMode | null) => void; busy: boolean; error: string; run: (body: object) => Promise<void> }) {
  const base = { scope: scope.scope, repo: scope.repo, note: text };
  if (mode?.kind === 'edit') {
    return (
      <li class="crew-note editing">
        {splitNote(text).stamp ? <span class="crew-note-stamp">{splitNote(text).stamp}</span> : null}
        <textarea aria-label="Note text" rows={2} value={mode.text} onInput={(e) => setMode({ kind: 'edit', text: (e.currentTarget as HTMLTextAreaElement).value })} />
        {error ? <p class="note error">{error}</p> : null}
        <div class="crew-actions">
          <button type="button" class="btn primary" disabled={busy || !mode.text.trim()} onClick={() => run({ op: 'edit', ...base, text: mode.text.trim() })}>
            Save
          </button>
          <button type="button" class="btn" onClick={() => setMode(null)}>
            Cancel
          </button>
        </div>
      </li>
    );
  }
  if (mode?.kind === 'move') {
    const choices = moveChoices(notes, scope);
    const target = moveTarget(mode.to, mode.typed);
    return (
      <li class="crew-note editing">
        <span class="crew-note-text">{text}</span>
        <select aria-label="Move to" value={mode.to} onChange={(e) => setMode({ ...mode, to: (e.currentTarget as HTMLSelectElement).value })}>
          {choices.map((c) => (
            <option key={c.value} value={c.value}>
              {c.label}
            </option>
          ))}
        </select>
        {mode.to === 'typed' ? <input type="text" aria-label="Repository, owner/repo" placeholder="owner/repo" value={mode.typed} autocapitalize="none" onInput={(e) => setMode({ ...mode, typed: (e.currentTarget as HTMLInputElement).value })} /> : null}
        {error ? <p class="note error">{error}</p> : null}
        <div class="crew-actions">
          <button type="button" class="btn primary" disabled={busy || !target} onClick={() => target && run({ op: 'move', ...base, to: target })}>
            Move
          </button>
          <button type="button" class="btn" onClick={() => setMode(null)}>
            Cancel
          </button>
        </div>
      </li>
    );
  }
  return (
    <li class="crew-note">
      <span class="crew-note-text">{text}</span>
      <span class="crew-actions">
        <button type="button" class="btn quiet" aria-label="Edit note" disabled={busy} onClick={() => setMode({ kind: 'edit', text: splitNote(text).fact })}>
          Edit
        </button>
        <button type="button" class="btn quiet" aria-label="Move note" disabled={busy} onClick={() => setMode({ kind: 'move', to: moveChoices(notes, scope)[0].value, typed: '' })}>
          Move
        </button>
        <button type="button" class="btn quiet" aria-label="Delete note" disabled={busy} onClick={() => run({ op: 'delete', ...base })}>
          Delete
        </button>
      </span>
    </li>
  );
}

/** A member's notes by scope. Regions in the detail; plain headings under Unclaimed, so a scope name is one region at most. */
function NoteScopes({ member, notes, regions, fleetKey, harness, reload }: { member: string; notes: Notes; regions: boolean; fleetKey: string; harness: string; reload: () => Promise<void> }) {
  const [open, setOpen] = useState<{ key: string; text: string; mode: NoteMode } | null>(null);
  const [busy, setBusy] = useState(false);
  const [raised, setRaised] = useState<{ message: string; seen: string } | null>(null);
  const uid = useId();
  const seen = JSON.stringify(notes);
  // An error holds only while the notes are the ones it was raised on; a reload that changes them supersedes it.
  const error = raised && raised.seen === seen ? raised.message : '';
  const run = async (body: object) => {
    setBusy(true);
    setRaised(null);
    const r = await call('POST', crewApi(fleetKey, harness, '/notes'), { member, ...body });
    if (!r.ok) {
      setRaised({ message: r.status === 409 ? 'That note changed or the scope is full, so the notes are reloaded. Try again.' : r.error, seen });
      if (r.status === 409) {
        setOpen(null);
        await reload();
      }
    } else {
      setOpen(null);
      await reload();
    }
    setBusy(false);
  };
  const scopes = scopesOf(notes);
  if (scopes.length === 0) return null;
  return (
    <>
      {error && !open ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      {scopes.map((s) => {
        const headId = `${uid}-${s.key}`;
        const list = (
          <ul class="crew-notes">
            {s.notes.map((text, i) => {
              const mine = open?.key === s.key && open.text === text;
              return <NoteItem key={`${i}:${text}`} text={text} scope={s} notes={notes} mode={mine ? open!.mode : null} setMode={(m) => setOpen(m ? { key: s.key, text, mode: m } : null)} busy={busy} error={mine ? error : ''} run={run} />;
            })}
          </ul>
        );
        return regions ? (
          <section key={s.key} class="crew-scope" aria-labelledby={headId}>
            <h3 id={headId}>{s.title}</h3>
            {list}
          </section>
        ) : (
          <div key={s.key} class="crew-scope">
            <h4>{s.title}</h4>
            {list}
          </div>
        );
      })}
    </>
  );
}

function Detail({ member, fleetKey, harness, reload, afterRename, afterDelete }: { member: CrewMember; fleetKey: string; harness: string; reload: () => Promise<void>; afterRename: (to: string) => Promise<void>; afterDelete: () => Promise<void> }) {
  const [editing, setEditing] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const act = async (fn: () => Promise<{ ok: boolean; error?: string }>, then: () => Promise<void>) => {
    setBusy(true);
    setError('');
    const r = await fn();
    if (!r.ok) setError(r.error ?? 'The request failed.');
    else await then();
    setBusy(false);
  };
  const path = `/members/${encodeURIComponent(member.name)}`;
  const builtIn = member.layer === 'built-in';
  return (
    <article class="crew-detail">
      <header class="crew-head">
        <Avatar name={member.name} />
        <h2>{member.name}</h2>
        {member.customized ? <span class="pill">customized</span> : null}
        {member.harness ? <Flag harness={member.harness} size="header" label={`runs on ${member.harness}`} /> : null}
      </header>
      <p class="crew-sub">{roleLine(member)}</p>
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      <div class="crew-actions">
        {builtIn ? (
          <button type="button" class="btn primary" disabled={busy} onClick={() => act(() => call('POST', crewApi(fleetKey, harness, `${path}/customize`)), reload)}>
            Customize for this fleet
          </button>
        ) : (
          <>
            <button type="button" class="btn" disabled={busy || editing} onClick={() => setEditing(true)}>
              Edit
            </button>
            <button type="button" class="btn" disabled={busy} onClick={() => setRenaming(true)}>
              Rename
            </button>
            <button type="button" class="btn danger" disabled={busy || confirming} onClick={() => setConfirming(true)}>
              Delete
            </button>
          </>
        )}
      </div>
      {confirming ? (
        <div class="crew-confirm">
          <p>{member.customized ? `Delete this fleet's ${member.name}? The built-in one takes its place.` : `Delete ${member.name}? Its notes stay under Unclaimed.`}</p>
          <div class="crew-actions">
            <button type="button" class="btn danger" disabled={busy} onClick={() => act(() => call('DELETE', crewApi(fleetKey, harness, path)), afterDelete)}>
              Confirm delete
            </button>
            <button type="button" class="btn" disabled={busy} onClick={() => setConfirming(false)}>
              Cancel
            </button>
          </div>
        </div>
      ) : null}
      {editing ? (
        <EditForm member={member} fleetKey={fleetKey} harness={harness} onDone={() => setEditing(false)} onSaved={reload} />
      ) : (
        <dl class="crew-facts">
          {member['pick-me-when'] ? (
            <>
              <dt>Pick me when</dt>
              <dd>{member['pick-me-when']}</dd>
            </>
          ) : null}
          {member['based-on'] && !ROLES.includes(member.role) ? (
            <>
              <dt>Based on</dt>
              <dd>{member['based-on']}</dd>
            </>
          ) : null}
          {member.model ? (
            <>
              <dt>Model</dt>
              <dd>{member.model}</dd>
            </>
          ) : null}
          {member.skills.length > 0 ? (
            <>
              <dt>Skills</dt>
              <dd>
                <ul class="crew-skills">
                  {member.skills.map((s) => (
                    <li key={s}>{s}</li>
                  ))}
                </ul>
              </dd>
            </>
          ) : null}
          {member.instructions ? (
            <>
              <dt>Instructions</dt>
              <dd class="crew-instructions">{member.instructions}</dd>
            </>
          ) : null}
        </dl>
      )}
      <NoteScopes member={member.name} notes={member.notes} regions fleetKey={fleetKey} harness={harness} reload={reload} />
      {renaming ? (
        <RenameSheet
          member={member}
          fleetKey={fleetKey}
          harness={harness}
          onClose={() => setRenaming(false)}
          onRenamed={async (to) => {
            setRenaming(false);
            await afterRename(to);
          }}
        />
      ) : null}
    </article>
  );
}

function Unclaimed({ data, fleetKey, harness, reload }: { data: CrewData; fleetKey: string; harness: string; reload: () => Promise<void> }) {
  if (data.unclaimed.length === 0) return null;
  return (
    <section class="crew-group" aria-labelledby="crew-unclaimed">
      <h2 id="crew-unclaimed" class="group-head">
        Unclaimed notes
      </h2>
      <p class="muted">Notes no built-in or fleet member uses.</p>
      {data.unclaimed.map((u) => (
        <div key={u.member} class="crew-unclaimed">
          <h3>{u.member}</h3>
          <NoteScopes member={u.member} notes={u.notes} regions={false} fleetKey={fleetKey} harness={harness} reload={reload} />
        </div>
      ))}
    </section>
  );
}

export function CrewPage({ fleet, name, title, phone }: { fleet: Fleet; name: string | null; title: boolean; phone: boolean }) {
  const [data, setData] = useState<CrewData | null>(null);
  const [failed, setFailed] = useState('');
  const [harness, setHarness] = useState('');
  const seq = useRef(0);
  const search = fleetSearch.value;

  const load = async () => {
    const mine = ++seq.current;
    const r = await call<Record<string, unknown>>('GET', crewApi(fleet.key, harness));
    if (mine !== seq.current) return;
    if (r.ok) {
      setData(normalizeCrew(r.data ?? {}));
      setFailed('');
    } else setFailed(r.error);
  };
  useEffect(() => {
    void load();
  }, [fleet.key, harness]);

  const adding = name === null && new URLSearchParams(loc.value.search).get('add') === '1';
  const toList = (replace = false) => go('/app/crew', search, replace);
  const openAdd = () => go('/app/crew', `${search}${search ? '&' : '?'}add=1`);
  const heading = title ? <h1>Crew</h1> : null;

  if (!data) {
    return (
      <main class="panel crew">
        {heading}
        {failed ? (
          <>
            <p class="alert" role="alert">
              {failed}
            </p>
            <button type="button" class="btn" onClick={() => void load()}>
              Try again
            </button>
          </>
        ) : (
          <p class="line">Loading…</p>
        )}
      </main>
    );
  }

  const groups = groupMembers(data.members);
  const selected = name === null ? null : (data.members.find((m) => m.name === name) ?? null);
  const showList = !phone || (name === null && !adding);
  const showDetail = name !== null || adding;
  const dock = fleet.docks.length > 1 ? (
    <label class="crew-dock">
      <span>Dock</span>
      <select value={harness || data.harness || fleet.docks[0].harness} onChange={(e) => setHarness((e.currentTarget as HTMLSelectElement).value)}>
        {fleet.docks.map((d) => (
          <option key={d.harness} value={d.harness}>
            {d.harness}
          </option>
        ))}
      </select>
    </label>
  ) : null;
  const back = (
    <a class="back-link" href={'/app/crew' + search} onClick={(e) => {
      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      e.preventDefault();
      toList();
    }}>
      Back to crew
    </a>
  );

  return (
    <main class={`panel crew${showList && showDetail ? ' split' : ''}`}>
      {heading}
      {failed ? (
        <p class="alert" role="alert">
          {failed}
        </p>
      ) : null}
      {data.errors.length > 0 ? (
        <div class="alert" role="alert">
          <p>Admiral can't read some crew files. Fix the file on disk, then reload.</p>
          <button type="button" class="btn" onClick={() => void load()}>
            Reload
          </button>
          <ul>
            {data.errors.map((d, i) => (
              <li key={i}>
                {d.file ? <code>{d.file}</code> : null} {d.message}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {data.warnings.length > 0 ? (
        <ul class="crew-warnings muted">
          {data.warnings.map((d, i) => (
            <li key={i}>
              {d.file ? `${d.file}: ` : ''}
              {d.message}
            </li>
          ))}
        </ul>
      ) : null}
      <div class={`crew-grid${showList && showDetail ? ' split' : ''}`}>
        {showList ? (
          <div class="crew-list">
            <div class="crew-toolbar">
              {dock}
              <button type="button" class="btn" onClick={openAdd}>
                Add crew
              </button>
            </div>
            {data.members.length === 0 && data.errors.length === 0 ? <p class="line">No crew members yet.</p> : null}
            <Group id="crew-fleet" title={groups[0].title} empty={data.errors.length > 0 ? '' : 'No fleet members yet.'} members={groups[0].members} selected={name} />
            <Group id="crew-built-in" title={groups[1].title} empty={data.errors.length > 0 ? '' : 'No built-in members here.'} members={groups[1].members} selected={name} />
            <Unclaimed data={data} fleetKey={fleet.key} harness={harness} reload={load} />
          </div>
        ) : null}
        {showDetail ? (
          <div class="crew-pane">
            {adding ? (
              <AddCrewPanel
                key={fleet.key}
                fleetKey={fleet.key}
                onClose={() => toList()}
                onCreated={async (to) => {
                  await load();
                  go(crewPath(to), search);
                }}
              />
            ) : null}
            {!adding && (phone || !selected) ? back : null}
            {adding ? null : selected ? (
              <Detail
                key={`${harness}:${selected.name}`}
                member={selected}
                fleetKey={fleet.key}
                harness={harness}
                reload={load}
                afterRename={async (to) => {
                  await load();
                  go(crewPath(to), search, true);
                }}
                afterDelete={async () => {
                  await load();
                  toList();
                }}
              />
            ) : (
              <p class="line">Member not found.</p>
            )}
          </div>
        ) : null}
      </div>
    </main>
  );
}
