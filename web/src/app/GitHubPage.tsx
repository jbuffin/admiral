import { signal } from '@preact/signals';
import { useEffect, useRef, useState } from 'preact/hooks';
import { checkConnection, countSearch, fetchRepos, fetchSettings, saveSource, type Connection, type Fleet, type Repos, type Source } from './api';
import { Actions, Button } from './Button';
import { addLabel, BUILTIN_VIEWS, forSave, HOT_LABEL_DEFAULTS, intervalLabel, intervalOptions, isRemovable, moveView, plainWords, removeLabel, removeView, renameView, setHidden, type ViewEntry } from './githubViews';
import { arranged, gripKey, pressGrip } from './gripReorder';
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

function ConnectionSection({ src, onCheck }: { src: Source; onCheck: () => Promise<void> }) {
  const c = connectionFor(src);
  const [switching, setSwitching] = useState(false);
  const [error, setError] = useState('');
  const check = async () => {
    setError('');
    const r = await checkConnection(src.key);
    if (r.ok) checked.value = { ...checked.value, [src.key]: r.data };
    else setError(r.error);
    await onCheck();
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
              {host} · through the gh CLI · token checked {c.checked_at ? new Date(c.checked_at).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }) : ''}
            </p>
            {switching ? <CopyCommand command={`gh auth switch -h ${host}`} /> : null}
          </>
        ) : null}
        {c?.state === 'unreachable' ? (
          <p class="gh-status">
            <span class="dot wait" aria-hidden="true" /> Couldn't reach {host}
          </p>
        ) : null}
        {c?.message && c.state !== 'connected' && c.state !== 'checking' ? <p class="note">{c.message}</p> : null}
        {error ? (
          <p class="note error" role="alert">
            {error}
          </p>
        ) : null}
        <Actions class="gh-actions">
          <Button label="Check again" onClick={check} />
          {c?.state === 'connected' && !switching ? <Button label="Switch account" onClick={() => setSwitching(true)} /> : null}
        </Actions>
      </div>
    </section>
  );
}

function ReposSection({ fleet, src, version }: { fleet: Fleet; src: Source; version: number }) {
  const [repos, setRepos] = useState<Repos | null>(null);
  useEffect(() => {
    let live = true;
    void fetchRepos(src.key).then((r) => live && r.ok && setRepos(r.data));
    return () => void (live = false);
  }, [src.key, version]);
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
            {repos.owners.length === 0 && repos.repos.length === 0 ? <p class="note">Nothing is checked out or listed for {fleet.name} yet.</p> : null}
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
const queryOf = (e: ViewEntry) => e.query ?? BUILTIN_VIEWS.find((b) => b.id === e.id)?.query;
const titleOf = (e: ViewEntry) => e.title || BUILTIN_VIEWS.find((b) => b.id === e.id)?.title || '';

function ViewRow({ entry, i, keys, connected, count, commit }: { entry: ViewEntry; i: number; keys: string[]; connected: boolean; count?: Count; commit: (next: (list: ViewEntry[]) => ViewEntry[]) => void }) {
  const [renaming, setRenaming] = useState(false);
  const [editingSearch, setEditingSearch] = useState(false);
  const cancelled = useRef(false);
  const title = titleOf(entry);
  const query = queryOf(entry);
  const builtin = BUILTIN_VIEWS.find((b) => b.id === entry.id && entry.query === undefined);
  return (
    <li class="vrow" data-reorder="views" data-key={String(i)}>
      <button
        type="button"
        class="grip"
        aria-label={`Move ${title}`}
        onPointerDown={(e) => pressGrip(e, 'views', String(i), keys, (order) => commit((list) => order.map((k) => list[Number(k)])))}
        onKeyDown={(e) => gripKey(e, String(i), keys, (order) => commit((list) => order.map((k) => list[Number(k)])))}
      >
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
              if (!cancelled.current && name.trim() !== title) commit((list) => renameView(list, i, name));
              cancelled.current = false;
            }}
          />
        ) : (
          <button type="button" class="vtitle" onClick={() => setRenaming(true)}>
            {title}
          </button>
        )}
        <span class="note">{builtin ? builtin.plain : plainWords(query ?? '')}</span>
        {editingSearch ? (
          <input
            class="vname"
            type="text"
            aria-label={`Search of ${title}`}
            defaultValue={query}
            autoFocus
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                const q = (e.target as HTMLInputElement).value.trim();
                setEditingSearch(false);
                if (q && q !== query) commit((list) => list.map((x, j) => (j === i ? { ...x, query: q } : x)));
              } else if (e.key === 'Escape') setEditingSearch(false);
            }}
            onBlur={() => setEditingSearch(false)}
          />
        ) : !builtin && query ? (
          <code class="vquery">{query}</code>
        ) : null}
        {count && 'error' in count ? (
          <span class="note error" role="alert">
            {count.error}
          </span>
        ) : null}
      </div>
      <span class="vcount note">{!connected ? '—' : builtin && !query ? '' : count && 'n' in count ? `${count.n} open` : '—'}</span>
      {!builtin && count && 'error' in count ? (
        <Button label="Edit search" onClick={() => setEditingSearch(true)} />
      ) : null}
      {isRemovable(entry) ? <Button label={`Remove view ${title}`} variant="quiet" onClick={() => commit((list) => removeView(list, i))}>Remove</Button> : null}
      <button type="button" class="switch" role="switch" aria-checked={!entry.hidden} aria-label={`Show ${title} in Orders`} disabled={!connected} onClick={() => commit((list) => setHidden(list, i, !entry.hidden))}>
        <span class="track" aria-hidden="true" />
      </button>
    </li>
  );
}

function ViewsSection({ views, connected, counts, edit }: { views: ViewEntry[]; connected: boolean; counts: Record<string, Count>; edit: (next: (list: ViewEntry[]) => ViewEntry[]) => void }) {
  const keys = views.map((_, i) => String(i));
  const shown = arranged('views', keys, (k) => k);
  return (
    <section class="gh-section" aria-labelledby="gh-views">
      <h2 class="h2" id="gh-views">
        Views
      </h2>
      {connected ? null : <p class="note">They run once GitHub is connected.</p>}
      <ul class="box list gh-views" aria-label="Views">
        {shown.map((k) => {
          const entry = views[Number(k)];
          const q = queryOf(entry);
          return <ViewRow key={k} entry={entry} i={Number(k)} keys={keys} connected={connected} count={q ? counts[q] : undefined} commit={edit} />;
        })}
      </ul>
    </section>
  );
}

function PrioritySection({ settings, put }: { settings: Record<string, unknown>; put: (patch: Record<string, unknown>) => void }) {
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
      </div>
    </section>
  );
}

export function GitHubPage({ fleet }: { fleet: Fleet }) {
  const src = githubSource(fleet.key);
  const key = src?.key;
  const [settings, setSettings] = useState<Record<string, unknown> | null>(null);
  const [views, setViews] = useState<ViewEntry[]>([]);
  const [counts, setCounts] = useState<Record<string, Count>>({});
  const [error, setError] = useState('');
  const [version, setVersion] = useState(0);
  const latest = useRef<{ settings: Record<string, unknown>; views: ViewEntry[] } | null>(null);
  const alive = useRef(true);
  useEffect(() => () => void (alive.current = false), []);

  useEffect(() => {
    if (!key) return;
    void fetchSettings(key).then((r) => {
      if (!alive.current) return;
      if (!r.ok) return setError(r.error);
      const list = (r.data.settings.views as ViewEntry[] | undefined) ?? BUILTIN_VIEWS.map((b) => ({ id: b.id, hidden: false }));
      latest.current = { settings: r.data.settings, views: list };
      setSettings(r.data.settings);
      setViews(list);
    });
  }, [key]);

  // Each change saves the whole settings at once and shows at once; a refusal puts back what the server has.
  const write = async (nextSettings: Record<string, unknown>, nextViews: ViewEntry[]) => {
    if (!key) return;
    const before = latest.current;
    latest.current = { settings: nextSettings, views: nextViews };
    setSettings(nextSettings);
    setViews(nextViews);
    setError('');
    const r = await saveSource(key, { settings: { ...nextSettings, views: forSave(nextViews) } });
    if (!alive.current) return;
    if (r.ok) return setVersion((v) => v + 1);
    setError(r.error);
    if (before) {
      latest.current = before;
      setSettings(before.settings);
      setViews(before.views);
    }
  };
  const editViews = (next: (list: ViewEntry[]) => ViewEntry[]) => {
    const cur = latest.current;
    if (cur) void write(cur.settings, next(cur.views));
  };
  const put = (patch: Record<string, unknown>) => {
    const cur = latest.current;
    if (cur) void write({ ...cur.settings, ...patch }, cur.views);
  };

  const connected = src?.connection?.state === 'connected';
  const queries = [...new Set(views.map(queryOf).filter((q): q is string => !!q))];
  const asked = queries.join('\n');
  useEffect(() => {
    if (!key || !connected) return;
    let live = true;
    for (const q of queries) {
      if (q in counts) continue;
      void countSearch(key, q).then((r) => {
        if (!live) return;
        if (r.ok) setCounts((c) => ({ ...c, [q]: { n: r.data.count } }));
        else if (r.status === 422) setCounts((c) => ({ ...c, [q]: { error: r.error.startsWith('Search failed') ? r.error : `Search failed: ${r.error}` } }));
      });
    }
    return () => void (live = false);
  }, [key, connected, asked, src?.views.length]);

  return (
    <>
      {!src ? (
        <p class="line">{fleet.name} has no GitHub source.</p>
      ) : (
        <>
          <p class="line">{fleet.name}'s issues and pull requests. They show in Orders, and routines can watch them.</p>
          {error ? (
            <p class="note error" role="alert">
              {error}
            </p>
          ) : null}
          <ConnectionSection src={src} onCheck={async () => setCounts({})} />
          <ReposSection fleet={fleet} src={src} version={version} />
          {settings ? (
            <>
              <ViewsSection views={views} connected={connected} counts={counts} edit={editViews} />
              <PrioritySection settings={settings} put={put} />
            </>
          ) : null}
        </>
      )}
    </>
  );
}
