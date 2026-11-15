import { signal } from '@preact/signals';
import { useEffect, useRef, useState } from 'preact/hooks';
import { checkConnection, countSearch, fetchRepos, fetchSettings, saveSource, type Connection, type Fleet, type Repos, type Source } from './api';
import { Actions, Button } from './Button';
import { addLabel, BUILTIN_VIEWS, checkedWords, editSearch, forSave, HOT_LABEL_DEFAULTS, inOrder, intervalLabel, intervalOptions, isBuiltin, isRemovable, needsSignIn, onKey, plainWords, removeLabel, removeView, renameView, searchOf, setHidden, titleOf, viewKey, type ViewEntry } from './githubViews';
import { arranged, gripKey, pressGrip } from './gripReorder';
import { NewViewCard } from './NewViewCard';
import { addView } from './newView';
import { fleetPath, go } from './router';
import { copyOutcome } from './remoteTailscale';
import { sources } from './shipsLink';

const gitHubOf = (fleetKey: string): Source | undefined => sources.value.find((s) => s.fleet === fleetKey && s.plugin === 'github');

// A check or a refused search answers with a newer connection than the stream's last push; the later one wins.
const checked = signal<Record<string, Connection>>({});
const stamp = (c?: Connection) => (c?.checked_at ? Date.parse(c.checked_at) : 0);

export function connectionFor(src: Source | undefined): Connection | undefined {
  if (!src) return undefined;
  const mine = checked.value[src.key];
  return mine && stamp(mine) > stamp(src.connection) ? mine : src.connection;
}

/** The source behind a fleet's GitHub page, its connection read through the newest answer. */
export function githubSource(fleetKey: string): (Source & { connection?: Connection }) | undefined {
  const src = gitHubOf(fleetKey);
  return src ? { ...src, connection: connectionFor(src) } : undefined;
}

function CopyCommand({ command }: { command: string }) {
  const [state, setState] = useState<'copied' | 'select' | null>(null);
  const el = useRef<HTMLElement>(null);
  const copy = async () => {
    const outcome = await copyOutcome(command, navigator.clipboard);
    if (outcome === 'select' && el.current) {
      const range = document.createRange();
      range.selectNodeContents(el.current);
      getSelection()?.removeAllRanges();
      getSelection()?.addRange(range);
    }
    setState(outcome);
  };
  return (
    <div class="gh-cmd" role="group" aria-label={command}>
      <code ref={el}>{command}</code>
      <Button label="Copy" onClick={() => void copy()} />
      <span class="note" role="status">
        {state === 'copied' ? 'Copied.' : state === 'select' ? 'Select the command and copy it.' : ''}
      </span>
    </div>
  );
}

function ConnectionSection({ fleet, src, settings, put, error, onCheck }: { fleet: Fleet; src: Source; settings: Record<string, unknown> | null; put: (patch: Record<string, unknown>) => Promise<boolean>; error?: string; onCheck: () => void }) {
  const c = connectionFor(src);
  const [switching, setSwitching] = useState(false);
  const [editing, setEditing] = useState(false);
  const [checkError, setCheckError] = useState('');
  const hostField = useRef<HTMLInputElement>(null);
  const accountField = useRef<HTMLInputElement>(null);
  const check = async () => {
    setCheckError('');
    const r = await checkConnection(src.key);
    if (r.ok) checked.value = { ...checked.value, [src.key]: r.data };
    else setCheckError(r.error);
    onCheck();
  };
  const saveFields = async () => {
    const ok = await put({ host: hostField.current?.value.trim() ?? '', account: accountField.current?.value.trim() ?? '' });
    if (ok) setEditing(false);
  };
  const host = c?.host ?? 'github.com';
  const signedOut = c?.state === 'signed_out' || c?.state === 'gh_missing';
  return (
    <section class="gh-section" aria-labelledby="gh-connection">
      <h2 class="h2" id="gh-connection">
        Connection
      </h2>
      <div class="box gh-box">
        {!c || c.state === 'checking' ? <p class="line">Checking the connection…</p> : null}
        {signedOut ? (
          <>
            <p class="gh-status">
              <span class="dot err" aria-hidden="true" /> Not signed in to {host}
            </p>
            <p class="note">Orders is empty for {fleet.name}.</p>
            <p class="note">Run this in a terminal on the Mac, then check again.</p>
            {c.fix.map((cmd) => (
              <CopyCommand key={cmd} command={cmd} />
            ))}
            {c.state === 'signed_out' ? (
              <>
                <p class="note">No gh yet? Install it first.</p>
                <CopyCommand command="brew install gh" />
              </>
            ) : null}
          </>
        ) : null}
        {c?.state === 'connected' ? (
          <>
            <p class="gh-status">
              <span class="dot ok" aria-hidden="true" />
              <span>
                Signed in as <strong>@{c.login}</strong>
              </span>
              <span class="chip chip-ok">Connected</span>
            </p>
            <p class="note">
              {host} · through the gh CLI · token checked {checkedWords(c.checked_at, Date.now())}
            </p>
            {switching ? <CopyCommand command={`gh auth switch -h ${host}`} /> : null}
          </>
        ) : null}
        {c?.state === 'unreachable' ? (
          <p class="gh-status">
            <span class="dot wait" aria-hidden="true" /> Couldn't reach {host}
          </p>
        ) : null}
        {c?.message && c.state !== 'checking' ? <p class="note">{c.message}</p> : null}
        {checkError ? (
          <p class="note error" role="alert">
            {checkError}
          </p>
        ) : null}
        {editing ? (
          <div class="gh-fields">
            <label class="gh-field">
              <span class="note">Host</span>
              <input ref={hostField} type="text" class="vname" aria-label="Host" defaultValue={(settings?.host as string | undefined) ?? ''} placeholder="github.com" />
            </label>
            <label class="gh-field">
              <span class="note">Account</span>
              <input ref={accountField} type="text" class="vname" aria-label="Account" defaultValue={(settings?.account as string | undefined) ?? ''} placeholder="the active account" />
            </label>
            {error ? (
              <p class="note error" role="alert">
                {error}
              </p>
            ) : null}
            <Actions class="gh-actions">
              <Button label="Save host and account" variant="primary" onClick={saveFields} />
              <Button label="Cancel" variant="quiet" onClick={() => setEditing(false)} />
            </Actions>
          </div>
        ) : null}
        <Actions class="gh-actions">
          <Button label="Check again" onClick={check} />
          {c?.state === 'connected' && !switching ? <Button label="Switch account" onClick={() => setSwitching(true)} /> : null}
          {settings && !editing ? <Button label="Edit host and account" variant="quiet" onClick={() => setEditing(true)} /> : null}
        </Actions>
      </div>
    </section>
  );
}

function ReposSection({ fleet, src }: { fleet: Fleet; src: Source }) {
  const [repos, setRepos] = useState<Repos | null>(null);
  useEffect(() => {
    let live = true;
    void fetchRepos(src.key).then((r) => live && r.ok && setRepos(r.data));
    return () => void (live = false);
  }, [src.key]);
  return (
    <section class="gh-section" aria-labelledby="gh-repos">
      <h2 class="h2" id="gh-repos">
        Repos
      </h2>
      <div class="box gh-box">
        {repos ? (
          <>
            <p class="note">What goes to {fleet.name} from GitHub.</p>
            <ul class="gh-repos">
              {repos.owners.map((o) => (
                <li key={`o:${o}`}>org: {o}</li>
              ))}
              {repos.repos.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
            {repos.owners.length === 0 && repos.repos.length === 0 ? <p class="note">No repos yet. Add a search root or an owner in Fleet.</p> : null}
            {repos.default ? <p class="note">Anything no other fleet claims goes here too.</p> : null}
            {repos.warnings.map((w) => (
              <p class="note warning" key={w}>
                {w}
              </p>
            ))}
          </>
        ) : (
          <p class="line">Reading repos…</p>
        )}
        <a
          class="gh-link"
          href={fleetPath(fleet.key)}
          onClick={(e) => {
            if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
            e.preventDefault();
            go(fleetPath(fleet.key), '');
          }}
        >
          Edit in Fleet
        </a>
      </div>
    </section>
  );
}

type Count = { n: number } | { error: string };

function ViewRow({ entry, k, keys, inert, count, error, edit, reorder }: { entry: ViewEntry; k: string; keys: string[]; inert: boolean; count?: Count; error?: string; edit: (key: string, change: (list: ViewEntry[], i: number) => ViewEntry[]) => void; reorder: (order: string[]) => void }) {
  const [renaming, setRenaming] = useState(false);
  const [editingSearch, setEditingSearch] = useState(false);
  const cancelled = useRef(false);
  const title = titleOf(entry);
  const query = searchOf(entry);
  const builtin = isBuiltin(entry);
  const plain = BUILTIN_VIEWS.find((b) => b.id === entry.id);
  const titleField = useRef<HTMLInputElement>(null);
  const queryField = useRef<HTMLInputElement>(null);
  const saveSearch = () => {
    setEditingSearch(false);
    const t = titleField.current?.value ?? '';
    const q = queryField.current?.value ?? '';
    if ((t.trim() && t.trim() !== title) || (q.trim() && q.trim() !== query)) edit(k, (list, i) => editSearch(list, i, t, q));
  };
  return (
    <li class={inert ? 'vrow inert' : 'vrow'} data-reorder="views" data-key={k}>
      <button type="button" class="grip" aria-label={`Move ${title}`} disabled={inert} onPointerDown={(e) => pressGrip(e, 'views', k, keys, reorder)} onKeyDown={(e) => gripKey(e, k, keys, reorder)}>
        ⋮⋮
      </button>
      <div class="vmain">
        {renaming ? (
          <input
            class="vname"
            type="text"
            aria-label={`Name of ${title}`}
            defaultValue={title}
            autoFocus
            onKeyDown={(e) => {
              if (e.key === 'Enter') (e.target as HTMLInputElement).blur();
              else if (e.key === 'Escape') {
                cancelled.current = true;
                (e.target as HTMLInputElement).blur();
              }
            }}
            onBlur={(e) => {
              const name = (e.target as HTMLInputElement).value;
              setRenaming(false);
              if (!cancelled.current && name.trim() !== title) edit(k, (list, i) => renameView(list, i, name));
              cancelled.current = false;
            }}
          />
        ) : (
          <span class="vheading">
            <button type="button" class="vtitle" disabled={inert} onClick={() => setRenaming(true)}>
              {title}
            </button>
            {builtin ? <span class="chip">Built in</span> : null}
          </span>
        )}
        <span class="note">{plain && builtin && entry.query === undefined ? plain.plain : plainWords(query ?? '')}</span>
        {editingSearch ? (
          <div
            class="vedit"
            onFocusOut={(e) => {
              if (!e.currentTarget.contains(e.relatedTarget as Node | null)) saveSearch();
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') saveSearch();
              else if (e.key === 'Escape') setEditingSearch(false);
            }}
          >
            <input ref={titleField} class="vname" type="text" aria-label={`Title of ${title}`} defaultValue={title} autoFocus />
            <input ref={queryField} class="vname" type="text" aria-label={`Search of ${title}`} defaultValue={query} />
          </div>
        ) : !builtin && query ? (
          <code class="vquery">{query}</code>
        ) : null}
        {count && 'error' in count ? (
          <span class="note error" role="alert">
            {count.error}
          </span>
        ) : null}
        {error ? (
          <span class="note error" role="alert">
            {error}
          </span>
        ) : null}
      </div>
      <span class="vcount note">{entry.hidden ? '' : inert ? '—' : builtin && !query ? '' : count && 'n' in count ? `${count.n} open` : '—'}</span>
      {!builtin && !editingSearch && count && 'error' in count ? <Button label="Edit search" disabled={inert} onClick={() => setEditingSearch(true)} /> : null}
      {isRemovable(entry) ? (
        <Button label={`Remove view ${title}`} variant="quiet" disabled={inert} onClick={() => edit(k, (list, i) => removeView(list, i))}>
          Remove
        </Button>
      ) : null}
      <button type="button" class="switch" role="switch" aria-checked={!entry.hidden} aria-label={`Show ${title} in Orders`} disabled={inert} onClick={() => edit(k, (list, i) => setHidden(list, i, !entry.hidden))}>
        <span class="track" aria-hidden="true" />
      </button>
    </li>
  );
}

function ViewsSection({ sourceKey, views, inert, counts, errors, edit, reorder, onAdd }: { sourceKey: string; views: ViewEntry[]; inert: boolean; counts: Record<string, Count>; errors: Record<string, string>; edit: (key: string, change: (list: ViewEntry[], i: number) => ViewEntry[]) => void; reorder: (order: string[]) => void; onAdd: (title: string, query: string) => Promise<boolean> }) {
  const keys = views.map(viewKey);
  const shown = arranged('views', keys, (k) => k);
  const [adding, setAdding] = useState(false);
  const openButton = useRef<HTMLButtonElement>(null);
  return (
    <>
      <section class="gh-section" aria-labelledby="gh-views">
        <div class="gh-views-head">
          <div class="gh-views-title">
            <h2 class="h2" id="gh-views">
              Views
            </h2>
            <p class="note">The tabs across the top of Orders, in this order. Drag to reorder, switch off to hide.</p>
          </div>
          <button ref={openButton} type="button" class="btn" disabled={inert} aria-expanded={adding} onClick={() => setAdding(true)}>
            + New view
          </button>
        </div>
        {inert ? <p class="note">They run once GitHub is connected.</p> : null}
        <ul class="box list gh-views" aria-label="Views">
          {shown.map((k) => {
            const i = keys.indexOf(k);
            const entry = views[i];
            const q = searchOf(entry);
            return <ViewRow key={k} entry={entry} k={k} keys={keys} inert={inert} count={q ? counts[q] : undefined} error={errors[k]} edit={edit} reorder={reorder} />;
          })}
        </ul>
      </section>
      {adding && !inert ? (
        <NewViewCard
          sourceKey={sourceKey}
          error={errors['new-view']}
          onAdd={async (title, query) => {
            const ok = await onAdd(title, query);
            if (ok) setAdding(false);
            return ok;
          }}
          onCancel={() => {
            setAdding(false);
            setTimeout(() => openButton.current?.focus());
          }}
        />
      ) : null}
    </>
  );
}

function PrioritySection({ settings, put, error }: { settings: Record<string, unknown>; put: (patch: Record<string, unknown>) => Promise<boolean>; error?: string }) {
  const labels = (settings.hotLabels as string[] | undefined) ?? HOT_LABEL_DEFAULTS;
  const { options, selected } = intervalOptions(settings.refreshMinutes as number | undefined);
  const [adding, setAdding] = useState(false);
  const add = (value: string) => {
    setAdding(false);
    const next = addLabel(settings.hotLabels as string[] | undefined, value);
    if (next.length !== labels.length) put({ hotLabels: next });
  };
  return (
    <section class="gh-section" aria-labelledby="gh-priority">
      <h2 class="h2" id="gh-priority">
        Priority and refresh
      </h2>
      <div class="box gh-box">
        <p class="note">An item with one of these labels counts as high priority.</p>
        <ul class="gh-labels">
          {labels.map((l) => (
            <li key={l} class="chip">
              <span class="chip-label">{l}</span>
              <button type="button" class="chip-x" aria-label={`Remove label ${l}`} onClick={() => put({ hotLabels: removeLabel(settings.hotLabels as string[] | undefined, l) })}>
                ×
              </button>
            </li>
          ))}
          <li>
            {adding ? (
              <input
                type="text"
                class="vname"
                aria-label="Label"
                autoFocus
                onKeyDown={(e) => {
                  if (e.key === 'Enter') add((e.target as HTMLInputElement).value);
                  else if (e.key === 'Escape') setAdding(false);
                }}
                onBlur={() => setAdding(false)}
              />
            ) : (
              <Button label="Add label" onClick={() => setAdding(true)} />
            )}
          </li>
        </ul>
        <p class="note" id="gh-interval">
          How often to check
        </p>
        <fieldset class="seg" role="radiogroup" aria-labelledby="gh-interval">
          {options.map((m) => (
            <label key={m} class={m === selected ? 'seg-opt on' : 'seg-opt'}>
              <input type="radio" name="interval" checked={m === selected} onChange={() => m !== selected && put({ refreshMinutes: m })} />
              {intervalLabel(m)}
            </label>
          ))}
        </fieldset>
        {error ? (
          <p class="note error" role="alert">
            {error}
          </p>
        ) : null}
      </div>
    </section>
  );
}

type Snap = { settings: Record<string, unknown>; views: ViewEntry[] };
type Op = { id: number; area: string; apply: (s: Snap) => Snap };

export function GitHubPage({ fleet }: { fleet: Fleet }) {
  const src = githubSource(fleet.key);
  const key = src?.key;
  const [, redraw] = useState(0);
  const [counts, setCounts] = useState<Record<string, Count>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [round, setRound] = useState(0);
  const [retry, setRetry] = useState(0);
  // What the server holds, and the changes still on their way; the page shows the first with the second laid over it.
  const confirmed = useRef<Snap | null>(null);
  const pending = useRef<Op[]>([]);
  const chain = useRef<Promise<unknown>>(Promise.resolve());
  const seq = useRef(0);
  const alive = useRef(true);
  const asked = useRef(new Set<string>());
  const tries = useRef(new Map<string, number>());
  useEffect(() => () => void (alive.current = false), []);

  const fail = (area: string, message: string) => setErrors((e) => ({ ...e, [area]: message }));

  useEffect(() => {
    if (!key) return;
    void fetchSettings(key).then((r) => {
      if (!alive.current) return;
      if (!r.ok) return fail('page', r.error);
      const views = (r.data.settings.views as ViewEntry[] | undefined) ?? BUILTIN_VIEWS.map((b) => ({ id: b.id, builtin: true, hidden: false }));
      confirmed.current = { settings: r.data.settings, views };
      redraw((n) => n + 1);
    });
  }, [key]);

  const shown = confirmed.current ? pending.current.reduce((s, o) => o.apply(s), confirmed.current) : null;

  // Saves go one at a time, each as the confirmed settings plus that one change, so a refused change drops out alone and the later ones still save.
  const enqueue = (area: string, apply: (s: Snap) => Snap): Promise<boolean> => {
    const op: Op = { id: ++seq.current, area, apply };
    pending.current = [...pending.current, op];
    setErrors((e) => {
      const { [area]: _gone, ...rest } = e;
      return rest;
    });
    redraw((n) => n + 1);
    const run = chain.current.then(async () => {
      if (!alive.current || !key || !confirmed.current) return false;
      const body = op.apply(confirmed.current);
      const r = await saveSource(key, { settings: { ...body.settings, views: forSave(body.views) } });
      if (!alive.current) return false;
      pending.current = pending.current.filter((o) => o.id !== op.id);
      if (r.ok && confirmed.current) confirmed.current = op.apply(confirmed.current);
      else if (!r.ok) fail(area, r.error);
      redraw((n) => n + 1);
      return r.ok;
    });
    chain.current = run;
    return run;
  };
  const put = (area: string) => (patch: Record<string, unknown>) => enqueue(area, (s) => ({ ...s, settings: { ...s.settings, ...patch } }));
  const editView = (k: string, change: (list: ViewEntry[], i: number) => ViewEntry[]) => void enqueue(k, (s) => ({ ...s, views: onKey(s.views, k, (i) => change(s.views, i)) }));
  const reorder = (order: string[]) => void enqueue('views', (s) => ({ ...s, views: inOrder(s.views, order) }));
  // The save answers the Source, not the settings, so the new entry has no id until the settings are read again.
  const addNewView = async (title: string, query: string): Promise<boolean> => {
    const ok = await enqueue('new-view', (s) => ({ ...s, views: addView(s.views, title, query) }));
    if (!ok || !key) return ok;
    const r = await fetchSettings(key);
    if (alive.current && r.ok && r.data.settings.views) {
      confirmed.current = { settings: r.data.settings, views: r.data.settings.views as ViewEntry[] };
      redraw((n) => n + 1);
    }
    return ok;
  };

  const connected = connectionFor(src)?.state === 'connected';
  const inert = needsSignIn(src && { connection: connectionFor(src) });
  const views = shown?.views ?? [];
  const queries = [...new Set(views.filter((v) => !v.hidden).map(searchOf).filter((q): q is string => !!q))];
  const wanted = queries.join('\n');
  useEffect(() => {
    if (!key || !connected) return;
    for (const q of queries) {
      const id = `${round}\n${q}`;
      if (asked.current.has(id)) continue;
      asked.current.add(id);
      void countSearch(key, q).then((r) => {
        if (!alive.current) return;
        if (r.ok) setCounts((c) => ({ ...c, [q]: { n: r.data.count } }));
        else if (r.status === 422) setCounts((c) => ({ ...c, [q]: { error: r.error.startsWith('Search failed') ? r.error : `Search failed: ${r.error}` } }));
        // A save restarts the source, and a count that lands in that gap is refused as not running yet: ask again shortly.
        else if (r.status === 409 && (tries.current.get(id) ?? 0) < 5) {
          tries.current.set(id, (tries.current.get(id) ?? 0) + 1);
          setTimeout(() => {
            if (!alive.current) return;
            asked.current.delete(id);
            setRetry((n) => n + 1);
          }, 500);
        }
      });
    }
  }, [key, connected, wanted, round, retry, src?.views.length]);

  return (
    <>
      {!src ? (
        <p class="line">{fleet.name} has no GitHub source.</p>
      ) : (
        <>
          <p class="line">{fleet.name}'s issues and pull requests. They show in Orders, and routines can watch them.</p>
          {errors.page ? (
            <p class="note error" role="alert">
              {errors.page}
            </p>
          ) : null}
          <ConnectionSection fleet={fleet} src={src} settings={shown?.settings ?? null} put={put('connection')} error={errors.connection} onCheck={() => setRound((n) => n + 1)} />
          <ReposSection fleet={fleet} src={src} />
          {shown ? (
            <>
              <ViewsSection sourceKey={src.key} views={views} inert={inert} counts={counts} errors={errors} edit={editView} reorder={reorder} onAdd={addNewView} />
              {errors.views ? (
                <p class="note error" role="alert">
                  {errors.views}
                </p>
              ) : null}
              <PrioritySection settings={shown.settings} put={put('priority')} error={errors.priority} />
            </>
          ) : null}
        </>
      )}
    </>
  );
}
