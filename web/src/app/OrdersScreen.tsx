import { Fragment } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { Button } from './Button';
import { isExpanded, toggleExpanded } from './expandedParents';
import { activeFleet, fleetSearch } from './fleets';
import { Icon } from './Icon';
import { ListGroup } from './ListGroup';
import { OrderPane } from './OrderPane';
import { selectedRow, selectionGone, withSelectedOrder } from './orderDetail';
import { AnswerReviewSheet } from './AnswerReview';
import { FixSheet } from './Fix';
import { fixLabel } from './prTab';
import { orderRowAction, type RowAction } from './moored';
import { ReReviewSheet } from './ReReview';
import type { MooredPR, Ship } from './api';
import { MakeCampaignSheet } from './MakeCampaignSheet';
import { sheetIssues } from './makeCampaign';
import { Sheet } from './Sheet';
import { LaunchForm, type OrderRef } from './LaunchDialog';
import { sourcesForFleet } from './launchForm';
import { boxState, type BoxState, chainGroupCanStack, chainGroupLine, checkedUrls, FOR_YOU, forYou, headerTime, groupRowActions, isIssueTab, issuesLabel, launchRef, newCampaignRefs, newCampaignSearch, selectableRows, selectAfterChoose, selection, orderRef, parentNote, refreshBusy, refreshedLine, refreshTarget, repoOptions, sourceName, stackGroupLine, stackLaunchRef, tabs, viewErrors, viewGroups, viewRows, type OrderRow, type SelectState, type ViewGroup } from './ordersView';
import { linkSourceStack, refreshSource } from './api';
import { StackSheet } from './StackSheet';
import { go, loc, shipPath } from './router';
import { emptyLine, surfaceById } from './surfaces';
import { ships, sources } from './shipsLink';
import { useNow } from './useNow';

/** The Needs you count the nav carries; zero for no fleet or no GitHub source. */
export function needsYouCount(): number {
  const fleet = activeFleet.value;
  return fleet ? forYou(sourcesForFleet(sources.value, fleet.key), Date.now(), ships.value).needsYou.length : 0;
}

export function NeedsYouBadge() {
  const n = needsYouCount();
  return n > 0 ? <span class="nav-count needs">{n}</span> : null;
}

const MARK_PR = <path d="M4 5.1v5.8M12 10.9V6.5a2 2 0 0 0-2-2H7.5M9 3 7.5 4.5 9 6" />;

// A needs-you PR keeps its accent colour, so draft shows by a dashed stroke on every PR and ready for review by green on the rest.
function Mark({ pr, needs, stack, draft }: { pr: boolean; needs: boolean; stack?: boolean; draft?: boolean }) {
  const named = pr && !stack && draft !== undefined;
  return (
    <svg
      class={`orow-mark${stack ? ' stack' : needs ? (pr ? ' pr' : ' issue') : ''}${named ? (draft ? ' draft' : ' ready-for-review') : ''}`}
      width="16"
      height="16"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      stroke-width="1.5"
      {...(named ? { role: 'img', 'aria-label': draft ? 'Draft pull request' : 'Pull request ready for review' } : { 'aria-hidden': 'true' })}
    >
      {stack ? (
        <>
          <path d="M8 2 14 5 8 8 2 5Z" />
          <path d="M2 8l6 3 6-3" />
          <path d="M2 11l6 3 6-3" />
        </>
      ) : pr ? (
        <>
          <circle cx="4" cy="3.5" r="1.6" />
          <circle cx="4" cy="12.5" r="1.6" />
          <circle cx="12" cy="12.5" r="1.6" />
          {MARK_PR}
        </>
      ) : (
        <>
          <circle cx="8" cy="8" r="6" />
          <circle cx="8" cy="8" r="1.2" fill="currentColor" />
        </>
      )}
    </svg>
  );
}

interface RowProps {
  row: OrderRow;
  phone: boolean;
  muted: boolean;
  selected: boolean;
  onOpen: (r: OrderRow) => void;
  onLaunch: (o: OrderRef) => void;
  onAgain: (ship: Ship, again: NonNullable<Extract<RowAction, { kind: 'ship' }>['again']>) => void;
  /** The row's action when its group worked it out, as a stack's rows do together; else the row's own order decides. */
  action?: RowAction;
  /** The fleet's login on the row's source, which tells a GitHub stack's own layers from the ones it reviews. */
  viewer?: string;
  /** Set on a row that heads a parent group. */
  parentGroup?: { group: ViewGroup; open: boolean; onToggle: () => void };
  child?: boolean;
  /** Says `in #n` on the phone line: a child, or a For you row. */
  note?: boolean;
  /** A view tab's loose row, spaced as wide as a parent row's toggle. */
  spaced?: boolean;
  /** Set while selecting issues: the row's checkbox. */
  box?: { checked: boolean; onToggle: () => void; box: BoxState };
}

const join = (...p: string[]) => p.filter(Boolean).join(' · ');
const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;

function Row({ row, phone, muted, selected, onOpen, onLaunch, onAgain, action, viewer, parentGroup, child, note, spaced, box }: RowProps) {
  const o = row.order;
  const g = parentGroup?.group;
  const act = action ?? orderRowAction(ships.value, o);
  const folded = !!g && !parentGroup.open;
  const here = g ? `${plural(g.children.length, row.stackRow ? 'PR' : 'sub-issue')} here` : '';
  const whole = g ? stackGroupLine(g, phone) : null;
  const state = row.state ? <span class={`orow-state${row.needsYou ? ' needs' : ''}`}>{row.state}</span> : null;
  // A collapsed parent carries what its hidden children would show.
  const summary = folded ? (
    <>
      {g.high && row.state !== 'High' ? <span class="orow-state high">High</span> : null}
      {g.ships > 0 ? <span class="orow-state">{plural(g.ships, 'ship')}</span> : null}
    </>
  ) : null;
  const toggle = g ? (
    <Button
      icon
      variant="quiet"
      class="orow-toggle"
      label={`${parentGroup.open ? 'Collapse' : 'Expand'} ${o.title}`}
      expanded={parentGroup.open}
      onClick={parentGroup.onToggle}
    >
      <Icon name="chevron" size={16} />
    </Button>
  ) : null;
  const tick = box ? (
    <label class="orow-box">
      <input type="checkbox" aria-label={box.box.label} checked={box.checked} disabled={box.box.disabled} onChange={box.onToggle} />
    </label>
  ) : null;
  const spacer = spaced ? <span class="orow-spacer" aria-hidden="true" /> : null;
  const arrow = !act ? null : act.kind === 'launch' ? (
      row.launchable ? (
        <button type="button" class="orow-go" aria-label={`Launch ${o.title}`} onClick={() => onLaunch(row.stackRow && g ? stackLaunchRef(g, ships.value, viewer) : launchRef(row))}>
          <Icon name="arrow" size={18} />
        </button>
      ) : null
    ) : (
      <>
        {act.again ? (
          <Button class="orow-again" label={act.again.kind === 'rereview' ? (row.stackRow ? 'Re-review stack' : 'Re-review') : act.again.kind === 'fix' ? fixLabel(act.ship) : 'Wake'} onClick={() => onAgain(act.ship, act.again!)} />
        ) : null}
        <button type="button" class={`orow-pill${act.needsYou ? ' needs' : ''}`} aria-label={`${act.ship.name}, ${act.word}. Open ship`} onClick={() => go(shipPath(act.ship.session), fleetSearch.value)}>
          <span class="orow-pill-name">{act.ship.name}</span>
          <span class="orow-pill-word">{act.word}</span>
        </button>
      </>
    );
  if (phone) {
    return (
      <li class={`orow-phone${child ? ' child' : ''}${selected ? ' selected' : ''}`}>
        {tick}
        {toggle ?? spacer}
        <button type="button" class="orow-open" aria-label={`Open ${o.title}`} aria-pressed={selected} onClick={() => onOpen(row)}>
          <span class="orow-main">
            <span class="orow-title">{o.title}</span>
            <span class="orow-meta">
              <span class="mono">{orderRef(o)}</span>
              {note && parentNote(o) ? <span>{` · ${parentNote(o)}`}</span> : null}
              {!row.inView && !row.stackRow ? <span>{' · not in this view'}</span> : null}
              {g ? <span>{` · ${here}`}</span> : null}
              {row.state ? <span>{' · '}</span> : null}
              {state}
              {summary}
            </span>
            {whole ? <span class="orow-whole">{whole}</span> : null}
          </span>
        </button>
        {arrow}
      </li>
    );
  }
  return (
    <li class={`orow${muted ? ' waiting' : ''}${child ? ' child' : ''}${selected ? ' selected' : ''}`}>
      {tick}
      {toggle ?? spacer}
      <button type="button" class="orow-open" aria-label={`Open ${o.title}`} aria-pressed={selected} onClick={() => onOpen(row)}>
        <Mark pr={!!o.pr} needs={row.needsYou} stack={row.stackRow} draft={o.pr?.state === 'OPEN' ? o.pr.draft : undefined} />
        <span class="orow-main">
          <span class="orow-title">{o.title}</span>
          <span class="orow-meta">
            <span class="mono">{orderRef(o)}</span>
            {row.reason || here ? <span>{join(row.reason, here, whole ?? '')}</span> : null}
          </span>
        </span>
        {(o.labels ?? []).map((l) => (
          <span key={l} class="orow-label">
            {l}
          </span>
        ))}
        {state}
        {summary}
      </button>
      {arrow}
    </li>
  );
}

/** A chain group's header: its PRs build on each other by branch but GitHub has not stacked them, so it says so and, for the viewer's own chain, offers the stacking. */
function ChainRow({ group, phone, open, onToggle, onStack }: { group: ViewGroup; phone: boolean; open: boolean; onToggle: () => void; onStack: () => void }) {
  const line = chainGroupLine(group, phone)!;
  return (
    <li class={phone ? 'orow-phone' : 'orow'}>
      <Button icon variant="quiet" class="orow-toggle" label={`${open ? 'Collapse' : 'Expand'} ${group.row.order.title}`} expanded={open} onClick={onToggle}>
        <Icon name="chevron" size={16} />
      </Button>
      <div class="stack-bar chain-bar">
        <span class="stack-text">
          <strong>{line.lead}</strong>
          {line.rest}
        </span>
        {chainGroupCanStack(group) ? <Button label="Stack on GitHub" onClick={onStack}>Stack on GitHub</Button> : null}
      </div>
    </li>
  );
}

function Group({ id, title, rows, phone, muted, chosen, onOpen, onLaunch, onAgain }: { id: string; title: string; rows: OrderRow[]; phone: boolean; muted: boolean; chosen?: string; onOpen: (r: OrderRow) => void; onLaunch: (o: OrderRef) => void; onAgain: RowProps['onAgain'] }) {
  if (rows.length === 0) return null;
  return (
    <ListGroup id={id} title={title} count={rows.length} muted={muted}>
      {rows.map((r) => (
        <Row key={r.key} row={r} phone={phone} muted={muted} selected={r.key === chosen} onOpen={onOpen} onLaunch={onLaunch} onAgain={onAgain} note />
      ))}
    </ListGroup>
  );
}

const tabOf = () => new URLSearchParams(loc.value.search).get('tab') ?? FOR_YOU;

export function OrdersPanel({ phone, title }: { phone: boolean; title: boolean }) {
  const now = useNow();
  const fleet = activeFleet.value;
  const [query, setQuery] = useState('');
  const [repo, setRepo] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [pick, setPick] = useState<SelectState>({ fleet: '', tab: '', on: false, checked: new Set() });
  const [launching, setLaunching] = useState<OrderRef | null>(null);
  const [stacking, setStacking] = useState<ViewGroup | null>(null);
  const [again, setAgain] = useState<{ ship: Ship; kind: 'rereview'; url: string } | { ship: Ship; kind: 'fix' } | { ship: Ship; kind: 'wake'; pr?: MooredPR; stack?: string } | null>(null);
  const list = fleet ? sourcesForFleet(sources.value, fleet.key) : [];
  const all = tabs(list);
  const id = all.some((t) => t.id === tabOf()) ? tabOf() : (all[0]?.id ?? FOR_YOU);
  const choose = (next: string) => {
    const q = new URLSearchParams(fleetSearch.value);
    if (next !== FOR_YOU) q.set('tab', next);
    setQuery('');
    setRepo('');
    setError('');
    setPick(selectAfterChoose(pick, next, fleet?.key ?? ''));
    go('/app/orders', q.size ? `?${q}` : '', true);
  };
  const time = headerTime(list, id);
  const target = refreshTarget(list, id);
  const refresh = async () => {
    if (!target) return;
    setBusy(true);
    setError('');
    const r = await refreshSource(target);
    setBusy(false);
    if (!r.ok) setError(r.error);
  };
  const rows = viewRows(list, id, now, ships.value);
  const repos = repoOptions(rows);
  const shown = viewGroups(list, id, now, ships.value, {
    query,
    repo: repos.includes(repo) ? repo : '',
  });
  const issueTab = isIssueTab(list, id);
  const poolRows = selectableRows(list, id, now, ships.value);
  const cur = selection(pick, { tab: id, fleet: fleet?.key ?? '', rows: poolRows, issueTab });
  useEffect(() => {
    if (cur !== pick) setPick(cur);
  }, [cur]);
  const selecting = cur.on;
  const checked = cur.checked;
  const toggle = (key: string) => {
    const next = new Set(checked);
    if (!next.delete(key)) next.add(key);
    setPick({ ...cur, checked: next });
  };
  const boxFor = (r: OrderRow): RowProps['box'] => (selecting ? { checked: checked.has(r.key), onToggle: () => toggle(r.key), box: boxState(r, checked) } : undefined);
  const makeCampaign = () => go('/app/orders', newCampaignSearch(loc.value.search, checkedUrls(poolRows, checked)));
  const refs = newCampaignRefs(loc.value.search);
  const campaignRows = refs.length > 0 ? all.flatMap((t) => viewRows(list, t.id, now, ships.value)) : [];
  const closeCampaign = () => go('/app/orders', newCampaignSearch(loc.value.search, []), true);
  const errors = viewErrors(list, id);
  const needs = shown.map((g) => g.row).filter((r) => r.needsYou);
  const waiting = shown.map((g) => g.row).filter((r) => !r.needsYou);
  const onLaunch = (o: OrderRef) => setLaunching(o);
  const onAgain: RowProps['onAgain'] = (ship, a) => {
    if (a.kind === 'rereview') return setAgain({ ship, kind: 'rereview', url: a.url });
    if (a.kind === 'fix') return setAgain({ ship, kind: 'fix' });
    if ('stack' in a) return setAgain({ ship, kind: 'wake', stack: a.stack });
    const pr = (ship.prs ?? []).find((p) => p.url === a.pr);
    if (pr) setAgain({ ship, kind: 'wake', pr });
  };
  const selected = selectedRow(list, id, loc.value.search, now, ships.value);
  const chosen = selected?.key;
  const selectedGroup = selected?.stackRow ? shown.find((g) => g.row.key === selected.key) : undefined;
  const selectedViewer = list.find((src) => src.key === selected?.source)?.viewer;
  const onOpen = (r: OrderRow) => go('/app/orders', withSelectedOrder(loc.value.search, r.key));
  const close = (restoreFocus = false) => {
    // The pane unmounts with the focus in it; the row's button is the same element before and after.
    const row = restoreFocus ? document.querySelector<HTMLElement>('.orow-open[aria-pressed="true"]') : null;
    go('/app/orders', withSelectedOrder(loc.value.search, null), true);
    row?.focus();
  };
  const pane = !!selected;
  const gone = selectionGone(list, id, loc.value.search, now, ships.value);
  useEffect(() => {
    if (gone) close();
  }, [gone]);
  // The listener lives as long as the panel and reads refs, so it is there the moment the pane renders, before any effect could attach it.
  // It listens in the capture phase: the Launch form's own Escape closes it and re-renders before a bubbling listener would run, so the flag would already be clear.
  const state = useRef({ pane: false, launching: false });
  state.current = { pane: pane && !phone, launching: !!launching };
  // Whether the Escape being handled began with the Launch form open; the phone sheet reads it, as it closes on the same key after the form is gone.
  const formHadEscape = useRef(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      formHadEscape.current = e.key === 'Escape' && state.current.launching;
      if (e.key !== 'Escape' || e.defaultPrevented || !state.current.pane || state.current.launching) return;
      if (e.target instanceof Element && e.target.closest('[aria-modal="true"], [role="menu"]')) return;
      close(true);
    };
    const onKeyUp = () => (formHadEscape.current = false);
    document.addEventListener('keydown', onKey, true);
    document.addEventListener('keyup', onKeyUp, true);
    return () => {
      document.removeEventListener('keydown', onKey, true);
      document.removeEventListener('keyup', onKeyUp, true);
    };
  }, []);
  const age = time ? refreshedLine(time, now) : '';
  const name = sourceName(list, id);
  const sourceLine = [name, age].filter(Boolean).join(' · ');
  return (
    <>
      <div class={`orders-split${selected && !phone ? ' has-pane' : ''}`}>
        <main class="panel orders-screen">
          <header class="orders-head">
            <div class="orders-line">
              {title ? <h1>Orders</h1> : null}
              <span class="orders-source">{phone ? age : sourceLine && `${title ? '· ' : ''}${sourceLine}`}</span>
              {target ? (
                <Button variant="quiet" icon class="orders-refresh" label="Refresh now" busyLabel="Refreshing" busy={refreshBusy(list, id, busy)} onClick={refresh}>
                  <Icon name="refresh" size={16} />
                </Button>
              ) : null}
              {issueTab ? (
                <span class="orders-select">
                  {selecting ? <span aria-live="polite">{checked.size} selected</span> : null}
                  <Button
                    variant="quiet"
                    label={selecting ? 'Done' : 'Select'}
                    onClick={() => setPick(selecting ? { ...cur, on: false, checked: new Set() } : { ...cur, on: true })}
                  >
                    {selecting ? 'Done' : 'Select'}
                  </Button>
                </span>
              ) : null}
              {phone ? null : (
                <>
                  <span class="orders-spacer" />
                  <label class="orders-filter">
                    <Icon name="search" size={14} />
                    <input type="search" placeholder="Filter by title, repo, label" value={query} onInput={(e) => setQuery(e.currentTarget.value)} />
                  </label>
                  <select class="orders-repo" aria-label="Repo" value={repo} onChange={(e) => setRepo(e.currentTarget.value)}>
                    <option value="">All repos</option>
                    {repos.map((r) => (
                      <option key={r} value={r}>
                        {r}
                      </option>
                    ))}
                  </select>
                </>
              )}
            </div>
            <div class="otabs" role="tablist" aria-label="Views">
              {all.map((t) => (
                <button key={t.id} type="button" role="tab" class="otab" aria-selected={t.id === id} onClick={() => choose(t.id)}>
                  {t.title} <span class="count">{t.count}</span>
                </button>
              ))}
            </div>
          </header>
          {error ? (
            <p class="alert" role="alert">
              {error}
            </p>
          ) : null}
          {errors.map((e) => (
            <p key={e} class="alert" role="alert">
              {e}
            </p>
          ))}
          {id === FOR_YOU ? (
            <>
              <Group id="g-needs" title="Needs you" rows={needs} phone={phone} muted={false} chosen={chosen} onOpen={onOpen} onLaunch={onLaunch} onAgain={onAgain} />
              <Group id="g-waiting" title="Waiting on others" rows={waiting} phone={phone} muted chosen={chosen} onOpen={onOpen} onLaunch={onLaunch} onAgain={onAgain} />
            </>
          ) : (
            <ul class={`ship-list${checked.size > 0 ? ' has-bar' : ''}`}>
              {shown.map((g) => {
                const parent = g.children.length > 0;
                const isOpen = parent && isExpanded(id, g.row.order.id);
                const viewer = list.find((src) => src.key === g.row.source)?.viewer;
                const acts = groupRowActions(ships.value, g, viewer);
                return (
                  <Fragment key={g.row.key}>
                    {g.row.chainRow ? (
                      <ChainRow group={g} phone={phone} open={isOpen} onToggle={() => toggleExpanded(id, g.row.order.id)} onStack={() => setStacking(g)} />
                    ) : (
                      <Row row={g.row} phone={phone} muted={false} selected={g.row.key === chosen} onOpen={onOpen} onLaunch={onLaunch} onAgain={onAgain} action={acts.row} viewer={viewer} spaced={!parent} box={boxFor(g.row)} note={!!g.row.order.parent} parentGroup={parent ? { group: g, open: isOpen, onToggle: () => toggleExpanded(id, g.row.order.id) } : undefined} />
                    )}
                    {isOpen ? g.children.map((c, i) => <Row key={c.key} action={acts.children[i]} row={c} phone={phone} muted={false} selected={c.key === chosen} onOpen={onOpen} onLaunch={onLaunch} onAgain={onAgain} child note box={boxFor(c)} />) : null}
                  </Fragment>
                );
              })}
            </ul>
          )}
          {shown.length === 0 ? <p class="line">{all.length === 1 && rows.length === 0 ? emptyLine(surfaceById('orders'), true, true) : 'Nothing here.'}</p> : null}
          {checked.size > 0 ? (
            <section class="orders-bar" aria-label="Selection">
              <span class="orders-bar-count">{issuesLabel(checked.size)}</span>
              <Button variant="quiet" label="Clear" onClick={() => setPick({ ...cur, checked: new Set() })}>
                Clear
              </Button>
              <Button variant="primary" label="Make campaign" onClick={makeCampaign}>
                Make campaign
              </Button>
            </section>
          ) : null}
        </main>
        {selected && !phone ? (
          <aside class="odetail-pane" aria-label="Selected order">
            <OrderPane key={selected.key} row={selected} group={selectedGroup} viewer={selectedViewer} sources={list} now={now} heading onLaunch={onLaunch} onClose={() => close(true)} />
          </aside>
        ) : null}
      </div>
      {selected && phone ? (
        <Sheet label={selected.order.title} kind="order-sheet" onClose={() => !launching && !formHadEscape.current && close()}>
          <OrderPane key={selected.key} row={selected} group={selectedGroup} viewer={selectedViewer} sources={list} now={now} heading={false} onLaunch={onLaunch} />
        </Sheet>
      ) : null}
      {refs.length > 0 && fleet ? (
        <MakeCampaignSheet key={refs.join(',')} fleet={fleet.key} sourceKeys={list.filter((s) => s.plugin === 'github').map((s) => s.key)} issues={sheetIssues(refs, campaignRows)} projects={[]} onClose={closeCampaign} />
      ) : null}
      {again?.kind === 'rereview' ? <ReReviewSheet ship={again.ship} sendUrl={again.url} onClose={() => setAgain(null)} /> : null}
      {again?.kind === 'fix' ? <FixSheet ship={again.ship} onClose={() => setAgain(null)} onBusy={() => {}} /> : null}
      {again?.kind === 'wake' ? <AnswerReviewSheet ship={again.ship} pr={again.pr} stack={again.stack} onClose={() => setAgain(null)} onBusy={() => {}} /> : null}
      {stacking ? (
        <StackSheet
          lines={stacking.children.map((c) => ({ url: c.order.url ?? '', head: c.order.chain?.head, base: c.order.chain?.base }))}
          pending={['source', stacking.row.source, 'stack-link']}
          link={() => linkSourceStack(stacking.row.source, stacking.children.map((c) => c.order.url ?? ''))}
          onClose={() => setStacking(null)}
          onShape={(r) => {
            // The group comes back as Stack #N; keep it open if the chain was.
            const first = stacking.children[0]?.order.url ?? '';
            const url = r.stack_number ? first.replace(/\/pull\/\d+$/, `/stack/${r.stack_number}`) : '';
            if (url && isExpanded(id, stacking.row.order.id) && !isExpanded(id, url)) toggleExpanded(id, url);
          }}
          failed={async () => {
            await refreshSource(stacking.row.source);
            return false;
          }}
        />
      ) : null}
      {launching && fleet ? <LaunchForm fleet={fleet} order={launching} onClose={() => setLaunching(null)} onLaunched={(session) => go(shipPath(session), fleetSearch.value)} /> : null}
    </>
  );
}
