import { useEffect, useRef, useState } from 'preact/hooks';
import { Button } from './Button';
import { fetchModulePage, fetchModuleRow, restartSource, type ModulePageData, type PageAction, type PageChip, type PageNotice, type PageRow, type RowDetail, type RowStep } from './api';
import { Icon } from './Icon';
import { builtinOf, ModuleActions, ModuleActionsProvider, useModuleActions, useUpdating } from './ModuleActions';
import { ListGroup } from './ListGroup';
import { chipTone, filterGroups, noticeTone, shouldRefetchRow } from './moduleView';
import { closeRow, openRow } from './moduleNav';
import { go, loc, moduleOf, modulePath } from './router';
import { openableRow } from './modules';
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

function NoticeLine({ n }: { n: PageNotice }) {
  const updating = useUpdating({ notice: true, id: n.id });
  return (
    <li class="mnotice" data-tone={noticeTone(n.tone)}>
      <span class="mnotice-text">{n.text}</span>
      {n.link && isWebUrl(n.link.url) ? (
        <a href={n.link.url} target="_blank" rel="noopener noreferrer">
          {n.link.label}
        </a>
      ) : null}
      {updating ? <span class="mact-updating">updating…</span> : null}
      <ModuleActions target={{ notice: true, id: n.id }} one={n.action} />
    </li>
  );
}

function Notices({ notices }: { notices: PageNotice[] }) {
  return notices.length === 0 ? null : (
    <ul class="mnotices">
      {notices.map((n) => (
        <NoticeLine key={n.id} n={n} />
      ))}
    </ul>
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

/** A row's actions split into the module actions and the built-in ones, which act on the row itself. */
function rowActions(row: PageRow) {
  const all = [...(row.action ? [row.action] : []), ...(row.actions ?? [])];
  return { module: all.filter((a) => !builtinOf(a)), builtin: all.filter((a) => builtinOf(a)) };
}

type Placed = { onTarget: PageAction[]; rowLevel: PageAction[]; byStep: Map<string, PageAction[]>; own: Map<string, PageAction[]> };

/**
 * Where a row's module actions show. A row with no target places none (its own actions stay as they are). On the row itself, actions naming the target step go
 * in the "Acts on" group and those naming no step (or any, when the target has no step) are row-level; the rest show on their step's card. In a detail, ids are
 * unique row-wide, the row's first, then each step's in order, and an action naming a step the detail lacks is row-level.
 */
function placeRowActions(row: PageRow | undefined, detail?: RowDetail): Placed {
  const placed: Placed = { onTarget: [], rowLevel: [], byStep: new Map(), own: new Map() };
  if (!row?.target) return placed;
  const module = rowActions(row).module;
  if (!detail) {
    placed.onTarget = row.target.step ? module.filter((a) => a.step === row.target?.step) : [];
    placed.rowLevel = module.filter((a) => !a.step || !row.target?.step);
    return placed;
  }
  const steps = detail.sections.flatMap((sec) => (sec.kind === 'steps' ? (sec.steps ?? []) : []));
  const seen = new Set<string>();
  const stepIds = new Set(steps.map((st) => st.id));
  for (const a of module) {
    if (seen.has(a.id)) continue;
    seen.add(a.id);
    if (a.step && stepIds.has(a.step)) placed.byStep.set(a.step, [...(placed.byStep.get(a.step) ?? []), a]);
    else placed.rowLevel.push(a);
  }
  for (const st of steps) {
    const mine = [...(st.action ? [st.action] : []), ...(st.actions ?? [])].filter((a) => !seen.has(a.id));
    mine.forEach((a) => seen.add(a.id));
    placed.own.set(st.id, mine);
  }
  return placed;
}

/** The target line: the target and its module actions, then the row-level actions after a divider and the built-in ones, on one line while it fits. */
function TargetLine({ row }: { row: PageRow & { target: NonNullable<PageRow['target']> } }) {
  const { builtin } = rowActions(row);
  const { onTarget, rowLevel } = placeRowActions(row);
  return (
    <div class="mtarget-line">
      <div class="mtarget" role="group" aria-label={`Acts on ${row.target.label}`}>
        <span class="mtarget-label">
          <span class="mtarget-arrow" aria-hidden="true">
            ↳
          </span>
          <strong>{row.target.label}</strong>
          <Chips chips={row.target.chips} />
        </span>
        <ModuleActions target={{ id: row.id }} many={onTarget} lead={row.action?.id} />
      </div>
      {rowLevel.length > 0 || builtin.length > 0 ? (
        <div class="mrowlevel">
          {rowLevel.length > 0 ? <span class="mdivider" role="separator" aria-orientation="vertical" /> : null}
          <ModuleActions target={{ id: row.id }} many={rowLevel} lead={row.action?.id} />
          <ModuleActions target={{ id: row.id }} many={builtin} />
        </div>
      ) : null}
    </div>
  );
}

function ModuleRow({ row, phone, selected, onOpen }: { row: PageRow; phone: boolean; selected: boolean; onOpen: (id: string) => void }) {
  const updating = useUpdating({ id: row.id });
  return (
    <li class={`${phone ? 'orow-phone' : 'orow'}${row.target ? ' mrow-target' : ''}${selected ? ' selected' : ''}`}>
      <button type="button" class="orow-open" aria-label={`Open ${row.title}`} aria-pressed={selected} disabled={!openableRow(row.id)} onClick={() => onOpen(row.id)}>
        <span class="orow-main">
          <span class="orow-title">{row.title}</span>
          {row.subtitle || row.reason ? (
            <span class="orow-meta">
              {row.subtitle ? <span>{row.subtitle}</span> : null}
              {row.reason ? <span>{row.reason}</span> : null}
            </span>
          ) : null}
        </span>
        {(row.chips ?? []).length > 0 ? (
          <span class="mchips">
            <Chips chips={row.chips} />
          </span>
        ) : null}
      </button>
      {updating ? <span class="mact-updating">updating…</span> : null}
      {row.target ? <TargetLine row={{ ...row, target: row.target }} /> : <ModuleActions target={{ id: row.id }} one={row.action} many={row.actions} />}
    </li>
  );
}

function OutLink({ url, children }: { url: string; children: preact.ComponentChildren }) {
  return (
    <a class="mlink" href={url} target="_blank" rel="noopener noreferrer">
      {children}
      <span class="mlink-out" aria-hidden="true">
        ↗
      </span>
    </a>
  );
}

const sectionCount = (sec: RowDetail['sections'][number]) => (sec.kind === 'links' ? sec.links?.length : sec.kind === 'steps' ? sec.steps?.length : undefined);

function StepCard({ step, rowId, row, placed }: { step: RowStep; rowId: string; row?: PageRow; placed: Placed }) {
  const next = !!row?.target?.step && row.target.step === step.id;
  const actions = row?.target ? [...(placed.byStep.get(step.id) ?? []), ...(placed.own.get(step.id) ?? [])] : [...(step.action ? [step.action] : []), ...(step.actions ?? [])];
  return (
    <li class={`mstep${next ? ' next' : ''}`}>
      <div class="mstep-head">
        <span class="mstep-title">{step.title}</span>
        {next ? <span class="mchip mnext">Next up</span> : null}
        <Chips chips={step.chips} />
      </div>
      {step.subtitle ? <p class="mstep-sub">{step.subtitle}</p> : null}
      {next && row?.reason ? <p class="mstep-sub">{row.reason}</p> : null}
      <ModuleActions target={{ id: rowId }} many={actions} lead={row?.target ? row.action?.id : undefined} small />
    </li>
  );
}

function Sections({ detail, rowId, row }: { detail: RowDetail; rowId: string; row?: PageRow }) {
  const next = row?.target?.step;
  const placed = placeRowActions(row, detail);
  const lastSteps = detail.sections.map((s) => s.kind).lastIndexOf('steps');
  const rowLevel =
    placed.rowLevel.length > 0 ? (
      <div class="mrowlevel-detail">
        <ModuleActions target={{ id: rowId }} many={placed.rowLevel} lead={row?.action?.id} small />
      </div>
    ) : null;
  return (
    <>
      {detail.sections.map((sec, i) => (
        <section key={i} class="msection">
          {sec.title ? (
            <h3>
              {sec.title}
              {sectionCount(sec) ? <span class="mcount">{sectionCount(sec)}</span> : null}
            </h3>
          ) : null}
          {sec.kind === 'links' ? (
            <ul class="mlinks">
              {(sec.links ?? []).map((l, j) => (
                <li key={j}>
                  {(l.chips ?? []).length > 0 ? (
                    <span class="mlink-chips">
                      <Chips chips={l.chips} />
                    </span>
                  ) : null}
                  {isWebUrl(l.url) ? <OutLink url={l.url}>{l.label}</OutLink> : l.label}
                  {l.subtitle ? <span class="mlink-sub">{l.subtitle}</span> : null}
                </li>
              ))}
            </ul>
          ) : null}
          {sec.kind === 'steps' ? (
            <>
              <ul class="msteps">
                {[...(sec.steps ?? []).filter((st) => st.id === next), ...(sec.steps ?? []).filter((st) => st.id !== next)].map((st) => (
                  <StepCard key={st.id} step={st} rowId={rowId} row={row} placed={placed} />
                ))}
              </ul>
              {i === lastSteps ? rowLevel : null}
            </>
          ) : null}
          {sec.kind === 'fields' ? (
            <dl class="odetail-facts">
              {(sec.fields ?? []).map((f, j) => (
                <>
                  <dt key={`k${j}`}>{f.label}</dt>
                  <dd key={`v${j}`}>{f.value}</dd>
                </>
              ))}
            </dl>
          ) : null}
        </section>
      ))}
      {lastSteps < 0 ? rowLevel : null}
    </>
  );
}

type Detail = { id: string; detail?: RowDetail; error?: string };

function RowDetailView({ detail, row, onBack, phone }: { detail: Detail; row?: PageRow; onBack: () => void; phone: boolean }) {
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
      {row ? (
        <>
          {row.subtitle || isWebUrl(row.url) ? <p class="odetail-sub">{isWebUrl(row.url) ? <OutLink url={row.url!}>{row.subtitle || 'Open'}</OutLink> : row.subtitle}</p> : null}
          <h2 class="odetail-title">{row.title}</h2>
          {(row.chips ?? []).length > 0 ? (
            <div class="odetail-labels">
              <Chips chips={row.chips} />
            </div>
          ) : null}
        </>
      ) : null}
      {detail.error ? (
        <p class="note error" role="alert">
          {`Couldn't load this row. ${detail.error}`}
        </p>
      ) : null}
      {detail.detail ? <Sections detail={detail.detail} rowId={detail.id} row={row} /> : !detail.error ? <div class="skeleton" role="status" aria-label="Loading" /> : null}
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

  // While the plugin has no good page yet its rows answer 404, so the row waits and the selection stays.
  const starting = source?.page?.state === 'starting';
  const seen = useRef({ version, starting });
  useEffect(() => {
    const prev = seen.current;
    seen.current = { version, starting };
    // The move out of starting refetches through the effect below already.
    if (selected && !prev.starting && prev.version !== undefined && version !== undefined && shouldRefetchRow(selected, prev.version, { version, rows: source?.page?.rows })) setAgain((n) => n + 1);
  }, [version, starting]);

  useEffect(() => {
    if (!selected) {
      setRow(null);
      return;
    }
    if (starting) return;
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
  }, [sourceKey, selected, again, starting]);

  const blocked = source?.page?.state === 'error' || page?.state === 'error';
  const { controller, sheet } = useModuleActions(sourceKey, version, blocked);

  const open = (id: string) => openRow(sourceKey, id);
  const close = () => closeRow(sourceKey);

  const restart = async () => {
    setRestarting(true);
    const r = await restartSource(sourceKey);
    setRestarting(false);
    setProblem(r.ok ? '' : r.error);
  };

  const groups = page ? filterGroups(page.groups, query) : [];
  const detail = selected ? (row?.id === selected ? row : { id: selected }) : null;
  const named = selected ? page?.groups.flatMap((g) => g.rows).find((r) => r.id === selected) : undefined;
  if (phone && detail) {
    return (
      <ModuleActionsProvider value={controller}>
        <main class="panel module-page phone">
          <RowDetailView detail={detail} row={named} onBack={close} phone />
        </main>
        {sheet}
      </ModuleActionsProvider>
    );
  }
  return (
    <ModuleActionsProvider value={controller}>
    <div class={`orders-split${detail ? ' has-pane' : ''}`}>
      <main class={phone ? 'panel module-page phone' : 'panel module-page'}>
        {title ? <h1>{source?.name ?? sourceKey}</h1> : null}
        {page?.notices ? <Notices notices={page.notices} /> : null}
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
          <RowDetailView detail={detail} row={named} onBack={close} phone={false} />
        </aside>
      ) : null}
    </div>
    {sheet}
    </ModuleActionsProvider>
  );
}
