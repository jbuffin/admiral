import { useEffect, useRef, useState } from 'preact/hooks';
import { Button } from './Button';
import { fetchModulePage, fetchModuleRow, restartSource, type ModulePageData, type PageAction, type PageChip, type PageRow, type RowDetail } from './api';
import { Icon } from './Icon';
import { ListGroup } from './ListGroup';
import { chipTone, filterGroups, shouldRefetchRow } from './moduleView';
import { go, loc, moduleOf, modulePath } from './router';
import { sources } from './shipsLink';

const isWebUrl = (u?: string) => !!u && /^https?:\/\//i.test(u);

function SignIn({ auth }: { auth: NonNullable<ModulePageData['auth']> }) {
  return (
    <div class="signin">
      <strong>Sign-in needed</strong>
      <p>{auth.message}</p>
      {isWebUrl(auth.url) ? (
        <a href={auth.url} target="_blank" rel="noopener noreferrer">
          {auth.url}
        </a>
      ) : null}
      {auth.code ? (
        <div class="signin-code">
          <code>{auth.code}</code>
        </div>
      ) : null}
    </div>
  );
}

/** The page's place while its route is still pending or its plugin is starting: nothing is fetched. */
export function ModuleSkeleton({ name, title, phone }: { name: string; title: boolean; phone?: boolean }) {
  return (
    <main class={phone ? 'panel module-page phone' : 'panel module-page'}>
      {title ? <h1>{name}</h1> : null}
      <div class="skeleton" role="status" aria-label="Loading" />
    </main>
  );
}

function Chips({ chips }: { chips?: PageChip[] }) {
  return (
    <>
      {(chips ?? []).map((c, i) => (
        <span key={i} class="mchip" data-tone={chipTone(c.tone)}>
          {c.label}
        </span>
      ))}
    </>
  );
}

// A module action is drawn but inert until module actions are wired.
function ModuleActions({ one, many }: { one?: PageAction; many?: PageAction[] }) {
  const all = [...(one ? [one] : []), ...(many ?? [])];
  return all.length === 0 ? null : (
    <span class="mactions">
      {all.map((a) => (
        <button key={a.id} type="button" class="btn mact" disabled>
          {a.label}
        </button>
      ))}
    </span>
  );
}

function ModuleRow({ row, phone, selected, onOpen }: { row: PageRow; phone: boolean; selected: boolean; onOpen: (id: string) => void }) {
  return (
    <li class={`${phone ? 'orow-phone' : 'orow'}${selected ? ' selected' : ''}`}>
      <button type="button" class="orow-open" aria-label={`Open ${row.title}`} aria-pressed={selected} onClick={() => onOpen(row.id)}>
        <span class="orow-main">
          <span class="orow-title">{row.title}</span>
          {row.subtitle || row.reason ? (
            <span class="orow-meta">
              {row.subtitle ? <span>{row.subtitle}</span> : null}
              {row.reason ? <span>{row.reason}</span> : null}
            </span>
          ) : null}
        </span>
        <Chips chips={row.chips} />
      </button>
      <ModuleActions one={row.action} many={row.actions} />
    </li>
  );
}

function Sections({ detail }: { detail: RowDetail }) {
  return (
    <>
      {detail.sections.map((sec, i) => (
        <section key={i} class="msection">
          {sec.title ? <h3>{sec.title}</h3> : null}
          {sec.kind === 'links' ? (
            <ul class="mlinks">
              {sec.links.map((l, j) => (
                <li key={j}>
                  {isWebUrl(l.url) ? (
                    <a href={l.url} target="_blank" rel="noopener noreferrer">
                      {l.label}
                    </a>
                  ) : (
                    l.label
                  )}
                </li>
              ))}
            </ul>
          ) : null}
          {sec.kind === 'steps' ? (
            <ul class="msteps">
              {sec.steps.map((st) => (
                <li key={st.id}>
                  <span class="mstep-title">{st.title}</span>
                  <Chips chips={st.chips} />
                  <ModuleActions one={st.action} many={st.actions} />
                </li>
              ))}
            </ul>
          ) : null}
          {sec.kind === 'fields' ? (
            <dl class="odetail-facts">
              {sec.fields.map((f, j) => (
                <>
                  <dt key={`k${j}`}>{f.label}</dt>
                  <dd key={`v${j}`}>{f.value}</dd>
                </>
              ))}
            </dl>
          ) : null}
        </section>
      ))}
    </>
  );
}

type Detail = { id: string; detail?: RowDetail; error?: string };

function RowDetailView({ detail, onBack, phone }: { detail: Detail; onBack: () => void; phone: boolean }) {
  return (
    <div class="odetail">
      <div class="odetail-top">
        {phone ? (
          <Button variant="quiet" label="Back" onClick={onBack}>
            Back
          </Button>
        ) : (
          <>
            <span class="orders-spacer" />
            <button type="button" class="orow-go odetail-close" aria-label="Close" onClick={onBack}>
              ×
            </button>
          </>
        )}
      </div>
      {detail.error ? (
        <p class="note error" role="alert">
          {detail.error}
        </p>
      ) : null}
      {detail.detail ? <Sections detail={detail.detail} /> : !detail.error ? <div class="skeleton" role="status" aria-label="Loading" /> : null}
    </div>
  );
}

export function ModulePage({ sourceKey, title, phone }: { sourceKey: string; title: boolean; phone?: boolean }) {
  const source = sources.value.find((s) => s.key === sourceKey);
  const version = source?.page?.version;
  const [page, setPage] = useState<ModulePageData | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [problem, setProblem] = useState('');
  const [query, setQuery] = useState('');
  const [row, setRow] = useState<Detail | null>(null);
  const [gone, setGone] = useState('');
  const [again, setAgain] = useState(0);
  const selected = moduleOf(loc.value.path)?.row ?? null;

  // Refetch whenever the page version moves.
  useEffect(() => {
    let stale = false;
    void fetchModulePage(sourceKey).then((r) => {
      if (stale) return;
      if (r.ok) {
        setPage(r.data);
        setProblem('');
      } else setProblem(r.error);
    });
    return () => {
      stale = true;
    };
  }, [sourceKey, version]);

  const seen = useRef(version);
  useEffect(() => {
    const prev = seen.current;
    seen.current = version;
    if (selected && prev !== undefined && version !== undefined && shouldRefetchRow(selected, prev, { version, rows: source?.page?.rows })) setAgain((n) => n + 1);
  }, [version]);

  useEffect(() => {
    if (!selected) {
      setRow(null);
      return;
    }
    setGone('');
    let stale = false;
    setRow((cur) => (cur?.id === selected ? cur : { id: selected }));
    void fetchModuleRow(sourceKey, selected).then((r) => {
      if (stale) return;
      if (r.ok) setRow({ id: selected, detail: r.data });
      else if (r.status === 404) {
        setRow(null);
        setGone('That row is no longer on this page.');
        go(modulePath(sourceKey), loc.value.search, true);
      } else setRow((cur) => ({ id: selected, detail: cur?.id === selected ? cur.detail : undefined, error: r.error }));
    });
    return () => {
      stale = true;
    };
  }, [sourceKey, selected, again]);

  const open = (id: string) => go(modulePath(sourceKey, id), loc.value.search);
  const close = () => go(modulePath(sourceKey), loc.value.search);

  const restart = async () => {
    setRestarting(true);
    const r = await restartSource(sourceKey);
    setRestarting(false);
    setProblem(r.ok ? '' : r.error);
  };

  const groups = page ? filterGroups(page.groups, query) : [];
  const detail = selected ? (row?.id === selected ? row : { id: selected }) : null;
  if (phone && detail) {
    return (
      <main class="panel module-page phone">
        <RowDetailView detail={detail} onBack={close} phone />
      </main>
    );
  }
  return (
    <div class="orders-split">
      <main class={phone ? 'panel module-page phone' : 'panel module-page'}>
        {title ? <h1>{source?.name ?? sourceKey}</h1> : null}
        {(!page && !problem) || page?.state === 'starting' ? <div class="skeleton" role="status" aria-label="Loading" /> : null}
        {problem ? (
          <p class="note error" role="alert">
            {problem}
          </p>
        ) : null}
        {gone ? (
          <p class="note" role="status">
            {gone}
          </p>
        ) : null}
        {page?.state === 'auth_required' && page.auth ? <SignIn auth={page.auth} /> : null}
        {page?.state === 'error' ? (
          <div class="note error" role="alert">
            <p>{page.error}</p>
            <Button label="Restart" busy={restarting} onClick={() => void restart()} />
          </div>
        ) : null}
        {page && page.groups.length > 0 ? (
          <label class="orders-filter module-filter">
            <Icon name="search" size={14} />
            <input type="search" aria-label="Filter" placeholder="Filter by title, subtitle, chip" value={query} onInput={(e) => setQuery(e.currentTarget.value)} />
          </label>
        ) : null}
        {groups.map((g) => (
          <ListGroup key={g.id} id={`mg-${g.id}`} title={g.title} count={g.rows.length} muted>
            {g.rows.map((r) => (
              <ModuleRow key={r.id} row={r} phone={!!phone} selected={r.id === selected} onOpen={open} />
            ))}
          </ListGroup>
        ))}
        {page && page.groups.length > 0 && groups.length === 0 ? <p class="line">Nothing matches.</p> : null}
      </main>
      {detail ? (
        <aside class="odetail-pane" aria-label="Selected row">
          <RowDetailView detail={detail} onBack={close} phone={false} />
        </aside>
      ) : null}
    </div>
  );
}
