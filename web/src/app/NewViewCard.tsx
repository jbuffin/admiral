import { useEffect, useRef, useState } from 'preact/hooks';
import { countSearch } from './api';
import { Actions, Button } from './Button';
import { buildSearch, labelProblem, MAX_SEARCH_BYTES, searchBytes, shownSearch, viewPreset, VIEW_PRESETS, withHandEdit, withState, type BuilderState, type Draft, type Show, type Where } from './newView';

type Count = 'counting' | { n: number } | { error: string };

const SHOWS: { show: Show; label: string }[] = [
  { show: 'pr', label: 'Pull requests' },
  { show: 'issue', label: 'Issues' },
  { show: 'both', label: 'Both' },
];
const WHERES: { where: Where; label: string }[] = [
  { where: 'reviewer', label: 'Reviewer' },
  { where: 'author', label: 'Author' },
  { where: 'assignee', label: 'Assignee' },
  { where: 'mentioned', label: 'Mentioned' },
];

const COUNT_DELAY_MS = 400;

/** The match count of a search, asked once it has held still; an answer for an older search never shows. */
function useCount(sourceKey: string, search: string, ask: boolean): Count | undefined {
  const [count, setCount] = useState<Count>();
  useEffect(() => {
    if (!ask) return setCount(undefined);
    setCount('counting');
    let live = true;
    const timer = setTimeout(() => {
      void countSearch(sourceKey, search).then((r) => {
        if (!live) return;
        if (r.ok) setCount({ n: r.data.count });
        else setCount({ error: r.status === 422 && !r.error.startsWith('Search failed') ? `Search failed: ${r.error}` : r.error });
      });
    }, COUNT_DELAY_MS);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [sourceKey, search, ask]);
  return count;
}

const countWords = (c: Count | undefined): string => (c === 'counting' ? 'Counting…' : c && 'n' in c ? (c.n === 1 ? '1 match' : `${c.n} matches`) : c ? c.error : '');

/** A label field that commits on Enter, on blur and on its Add button, so a tap elsewhere keeps what was typed. */
function LabelField({ name, addName, onCommit, autoFocus }: { name: string; addName: string; onCommit: (label: string) => void; autoFocus?: boolean }) {
  const [text, setText] = useState('');
  const [problem, setProblem] = useState<string>();
  const commit = () => {
    const label = text.trim();
    const bad = labelProblem(label);
    setProblem(bad);
    if (label && !bad) onCommit(label);
  };
  return (
    <div class="nv-label-field">
      <input
        type="text"
        class="vname"
        aria-label={name}
        value={text}
        autoFocus={autoFocus}
        onInput={(e) => setText((e.target as HTMLInputElement).value)}
        onKeyDown={(e) => {
          if (e.key !== 'Enter') return;
          e.preventDefault();
          e.stopPropagation();
          commit();
        }}
        onBlur={commit}
      />
      <Button label={addName} onClick={commit}>
        Add
      </Button>
      {problem ? (
        <p class="note error" role="alert">
          {problem}
        </p>
      ) : null}
    </div>
  );
}

export function NewViewCard({ sourceKey, error, onAdd, onCancel }: { sourceKey: string; error?: string; onAdd: (title: string, query: string) => Promise<boolean>; onCancel: () => void }) {
  const [presetId, setPresetId] = useState<string | undefined>('needs-my-review');
  const [draft, setDraft] = useState<Draft>({ state: viewPreset('needs-my-review').state });
  const [name, setName] = useState(viewPreset('needs-my-review').name);
  const [adding, setAdding] = useState(false);
  const [withoutOpen, setWithoutOpen] = useState(false);
  const first = useRef<HTMLButtonElement>(null);
  const saving = useRef(false);
  useEffect(() => first.current?.focus(), []);

  const state = draft.state;
  const pr = state.show === 'pr';
  const search = shownSearch(draft).trim();
  const awaitingLabel = presetId === 'has-label' && !state.extra;
  const tooLong = searchBytes(search) > MAX_SEARCH_BYTES;
  const count = useCount(sourceKey, search, !!search && !tooLong && !awaitingLabel);
  const canAdd = !!name.trim() && !!search && !tooLong && !awaitingLabel;

  const choose = (id: string, label?: string) => {
    const p = viewPreset(id, label);
    setPresetId(id);
    setDraft({ state: p.state });
    setName(p.name);
  };
  const change = (patch: Partial<BuilderState>) => {
    setPresetId(undefined);
    setDraft((d) => withState(d, { ...d.state, ...patch }));
  };
  const leaveOut = (label: string) => {
    setWithoutOpen(false);
    if (!state.without.includes(label)) change({ without: [...state.without, label] });
  };
  const submit = async () => {
    if (!canAdd || saving.current) return;
    saving.current = true;
    setAdding(true);
    await onAdd(name, search);
    saving.current = false;
    setAdding(false);
  };

  return (
    <section class="box gh-newview" aria-labelledby="gh-newview">
      <div class="nv-head">
        <h2 class="h2" id="gh-newview">
          New view
        </h2>
        <p class="note">Start from a preset or build one. No search syntax needed.</p>
      </div>
      <div class="nv-chips">
        {VIEW_PRESETS.map((p, i) => (
          <button key={p.id} ref={i === 0 ? first : undefined} type="button" class="chip" aria-pressed={presetId === p.id} onClick={() => choose(p.id)}>
            {p.label}
          </button>
        ))}
      </div>
      {presetId === 'has-label' ? (
        <div class="nv-row">
          <span class="nv-key">Label</span>
          <LabelField name="Label" addName="Use label" autoFocus onCommit={(label) => choose('has-label', label)} />
        </div>
      ) : null}
      <label class="nv-row">
        <span class="nv-key">Name</span>
        <input
          type="text"
          class="vname nv-name"
          value={name}
          onInput={(e) => setName((e.target as HTMLInputElement).value)}
          onKeyDown={(e) => {
            if (e.key !== 'Enter') return;
            e.preventDefault();
            void submit();
          }}
        />
      </label>
      <div class="nv-row" role="group" aria-label="Show">
        <span class="nv-key" aria-hidden="true">
          Show
        </span>
        <div class="nv-seg">
          {SHOWS.map((s) => (
            <button key={s.show} type="button" class={state.show === s.show ? 'seg-opt on' : 'seg-opt'} aria-pressed={state.show === s.show} onClick={() => change({ show: s.show })}>
              {s.label}
            </button>
          ))}
        </div>
      </div>
      <div class="nv-row" role="group" aria-label="Where I'm">
        <span class="nv-key" aria-hidden="true">
          Where I'm
        </span>
        <div class="nv-seg">
          {WHERES.map((w) => (
            <button
              key={w.where}
              type="button"
              class={state.where === w.where ? 'seg-opt on' : 'seg-opt'}
              aria-pressed={state.where === w.where}
              disabled={w.where === 'reviewer' && !pr}
              onClick={() => change({ where: state.where === w.where ? undefined : w.where })}
            >
              {w.label}
            </button>
          ))}
        </div>
      </div>
      <div class="nv-row" role="group" aria-label="Leave out">
        <span class="nv-key" aria-hidden="true">
          Leave out
        </span>
        <div class="nv-chips">
          <button type="button" class="chip" aria-pressed={state.drafts} disabled={!pr} onClick={() => change({ drafts: !state.drafts })}>
            Drafts
          </button>
          <button type="button" class="chip" aria-pressed={state.approved && state.extra !== 'review:approved'} disabled={!pr || state.extra === 'review:approved'} onClick={() => change({ approved: !state.approved })}>
            Already approved
          </button>
          {state.without.map((l) => (
            <button key={l} type="button" class="chip" aria-pressed="true" aria-label={`Leave out label ${l}`} onClick={() => change({ without: state.without.filter((x) => x !== l) })}>
              {l} ×
            </button>
          ))}
          <button type="button" class="chip" aria-expanded={withoutOpen} onClick={() => setWithoutOpen(!withoutOpen)}>
            + Label
          </button>
        </div>
        {withoutOpen ? <LabelField name="Label to leave out" addName="Leave this label out" autoFocus onCommit={leaveOut} /> : null}
      </div>
      <details class="nv-hand">
        <summary>Edit as a GitHub search</summary>
        <input type="text" class="vname nv-search" aria-label="GitHub search" value={shownSearch(draft)} onInput={(e) => setDraft((d) => withHandEdit(d, (e.target as HTMLInputElement).value))} />
        {draft.hand !== undefined ? <p class="note">Changing an option above rebuilds the search.</p> : null}
      </details>
      <div class="gh-search" role="group" aria-label="Search">
        <code class="vquery">{search || buildSearch(state)}</code>
        <span class={count && typeof count === 'object' && 'error' in count ? 'note warn' : 'note'} role="status">
          {tooLong ? 'Too long for GitHub search' : countWords(count)}
        </span>
      </div>
      {error ? (
        <p class="note error" role="alert">
          {error}
        </p>
      ) : null}
      <Actions class="nv-actions">
        <Button label="Cancel" onClick={onCancel} />
        <Button label="Add view" variant="primary" disabled={!canAdd || adding} onClick={submit} />
      </Actions>
    </section>
  );
}
