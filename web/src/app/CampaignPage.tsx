import { signal } from '@preact/signals';
import { useEffect, useRef, useState } from 'preact/hooks';
import { call } from './api';
import { Actions, Button } from './Button';
import { runPending } from './busy';
import { isRunning, runningFor } from './shipList';
import { useNow } from './useNow';
import {
  blockerLine,
  campaignMeta,
  canJoinSlice,
  childShipsKey,
  groupChildren,
  hasUnreadable,
  keepSelectable,
  labelsDisagree,
  overrideBody,
  safeHref,
  kindLabel,
  leaves,
  modeLabel,
  nextPoll,
  SLOW_POLL_MS,
  shipOfChild,
  skillLabel,
  statusLabel,
  typeLabel,
  type Campaign,
  type CampaignNode,
  type Mode,
  type Row,
} from './campaign';
import { campaignList, endedOpen, noteCampaign, refreshCampaigns } from './campaignsLink';
import { AddCampaign } from './AddCampaign';
import { activeFleet, fleetPaint, fleets } from './fleets';
import { Choice } from './Choice';
import { Icon } from './Icon';
import { Link } from './NavLink';
import { surfaceById } from './surfaces';
import { IssueLabels } from './IssueLabels';
import { LaunchForm } from './LaunchDialog';
import { campaignPath, go, shipPath } from './router';
import { Sheet } from './Sheet';
import { commitmentMarks } from './chatSettings';
import { ships } from './shipsLink';
import { when } from './when';

function Ring({ done, total }: { done: number; total: number }) {
  const r = 5.5;
  const c = 2 * Math.PI * r;
  const frac = total > 0 ? done / total : 0;
  return (
    <svg class="ring" width="15" height="15" viewBox="0 0 15 15" aria-hidden="true">
      <circle cx="7.5" cy="7.5" r={r} fill="none" stroke="var(--color-line2)" stroke-width="2" />
      <circle cx="7.5" cy="7.5" r={r} fill="none" stroke="var(--color-ok)" stroke-width="2" stroke-dasharray={`${frac * c} ${c}`} transform="rotate(-90 7.5 7.5)" />
    </svg>
  );
}

const search = (fleet: string) => `?fleet=${encodeURIComponent(fleet)}`;

function open(e: MouseEvent, c: Campaign) {
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  go(campaignPath(c.id), search(c.fleet));
}

export const addOpen = signal(false);

// The digit is hidden from assistive tech so the link's name reads "2 ready, with you", not a bare number.
function ReadyBadge({ n }: { n: number }) {
  if (n <= 0) return null;
  return (
    <span class="camp-ready">
      <span aria-hidden="true">{n}</span>
      <span class="visually-hidden">{n} ready, with you</span>
    </span>
  );
}

function UnderwayCount({ n }: { n: number }) {
  if (n <= 0) return null;
  return (
    <span class="camp-underway">
      <span aria-hidden="true">{n}</span>
      <span class="visually-hidden">{n} underway</span>
    </span>
  );
}

// The mark before the skill's name; one place to change if the skill's own mark is wanted.
const SKILL_MARK = '/';

function draftingShips(fleet: string | undefined) {
  return ships.value.filter((s) => s.drafting && s.fleet === fleet);
}

function DraftingRows({ fleet, cls }: { fleet: string | undefined; cls: string }) {
  const here = draftingShips(fleet);
  if (here.length === 0) return null;
  return (
    <ul class="camp-list drafting-list">
      {here.map((s) => (
        <li key={s.session}>
          <a
            class={`${cls} drafting-row`}
            href={shipPath(s.session) + search(s.fleet)}
            onClick={(e) => {
              if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
              e.preventDefault();
              go(shipPath(s.session), search(s.fleet));
            }}
          >
            <span class="drafting-dot" aria-hidden="true" />
            <span class="camp-name">Drafting a campaign</span>
            <span class="visually-hidden"> · </span>
            <span class="camp-meta">
              {s.skill ? `${SKILL_MARK}${s.skill} · ` : ''}
              {s.name}
            </span>
          </a>
        </li>
      ))}
    </ul>
  );
}

function CampaignSidebar() {
  const fleet = activeFleet.value;
  const mine = campaignList.value.filter((c) => c.fleet === fleet?.key);
  const active = mine.filter((c) => !c.ended);
  const ended = mine.filter((c) => c.ended).length;
  const here = typeof location === 'undefined' ? '' : location.pathname;
  return (
    <div class="camp-side">
      <DraftingRows fleet={fleet?.key} cls="camp-row" />
      <ul class="camp-list">
        {active.map((c) => (
          <li key={c.id}>
            <a class="camp-row" href={campaignPath(c.id) + search(c.fleet)} aria-current={here.endsWith(`/campaigns/${c.id}`) ? 'page' : undefined} onClick={(e) => open(e, c)}>
              <Ring done={c.progress.closed} total={c.progress.total} />
              <span class="camp-name">{c.title}</span>
              <span class="camp-meta">{campaignMeta(c)}</span>
              <ReadyBadge n={c.ready_with_you} />
            </a>
          </li>
        ))}
      </ul>
      {ended > 0 ? (
        <button
          type="button"
          class="camp-ended"
          onClick={() => {
            endedOpen.value = true;
            go('/app/campaigns', `?fleet=${encodeURIComponent(fleet?.key ?? '')}`);
          }}
        >
          Ended ({ended})
        </button>
      ) : null}
    </div>
  );
}

function AddCampaignModal() {
  const fleet = activeFleet.value;
  if (!addOpen.value || !fleet) return null;
  return <AddCampaign fleet={fleet.key} onClose={() => (addOpen.value = false)} />;
}

/** The desktop sidebar's Campaigns row and what follows it. */
export function CampaignsNav() {
  return (
    <>
      <div class="nav-row">
        <Link surface={surfaceById('campaigns')} class="nav-link">
          <Icon name="campaigns" />
          Campaigns
        </Link>
        <Button variant="quiet" icon label="Add campaign" expanded={addOpen.value} onClick={() => (addOpen.value = !addOpen.value)}>
          <Icon name="plus" size={14} />
        </Button>
      </div>
      <CampaignSidebar />
      <AddCampaignModal />
    </>
  );
}

/** The Campaigns surface: the followed campaigns of the fleet, or the ended ones; the phone also gets the Add campaign button. */
export function CampaignsList({ phone, title }: { phone: boolean; title: boolean }) {
  const fleet = activeFleet.value;
  const mine = campaignList.value.filter((c) => c.fleet === fleet?.key);
  const ended = mine.filter((c) => c.ended);
  const showEnded = endedOpen.value;
  const list = showEnded ? ended : mine.filter((c) => !c.ended);
  return (
    <main class="panel">
      {title ? <h1>Campaigns</h1> : null}
      {phone ? (
        <>
          <Button label="Add campaign" onClick={() => (addOpen.value = true)} />
          <AddCampaignModal />
        </>
      ) : null}
      <div class="camp-tabs">
        <Button label={`Following (${mine.length - ended.length})`} pressed={!showEnded} onClick={() => (endedOpen.value = false)} />
        <Button label={`Ended (${ended.length})`} pressed={showEnded} onClick={() => (endedOpen.value = true)} />
      </div>
      {showEnded ? null : <DraftingRows fleet={fleet?.key} cls="camp-card" />}
      {list.length === 0 && (showEnded || draftingShips(fleet?.key).length === 0) ? <p class="line">{showEnded ? 'No campaign has ended.' : 'Follow a campaign with Add campaign, or with Follow on an order.'}</p> : null}
      <ul class="camp-cards">
        {list.map((c) => (
          <li key={c.id}>
            <a class="camp-card" href={campaignPath(c.id) + search(c.fleet)} onClick={(e) => open(e, c)}>
              <Ring done={c.progress.closed} total={c.progress.total} />
              <span class="camp-name">{c.title}</span>
              <span class="camp-meta">{campaignMeta(c)}</span>
              <ReadyBadge n={c.ready_with_you} />
              <UnderwayCount n={c.counts.underway ?? 0} />
            </a>
          </li>
        ))}
      </ul>
    </main>
  );
}

// The root's mode and skill come from its labels and change nothing a hand launch does, so its sheet only reads them.
function Detail({ campaign, node, canStart, onClose, onLaunch, onSaved }: { campaign: Campaign; node: CampaignNode; canStart: boolean; onClose: () => void; onLaunch: (anyway: boolean) => void; onSaved: (c: Campaign) => void }) {
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const saveOverride = async (mode: Mode | null) => {
    setError('');
    setBusy(true);
    const r = await call<Campaign>('PUT', `/campaigns/${campaign.id}/override`, overrideBody(node, mode));
    setBusy(false);
    if (!r.ok) return setError(r.error);
    onSaved(r.data);
  };
  const status = node.status ?? 'ready';
  const canLaunch = canStart && !campaign.ended && (status === 'ready' || status === 'claimed-no-ship');
  return (
    <Sheet label={node.title} onClose={onClose}>
      <Actions class="detail">
        <p class="mono">
          {node.repo ? `${node.repo}#${node.number}` : `#${node.number}`} · {statusLabel(status)}
          {node.ship ? ` · ${node.ship}` : ''}
        </p>
        <IssueLabels labels={node.labels} />
        {safeHref(node.url) ? (
          <a href={safeHref(node.url)!} target="_blank" rel="noopener noreferrer">
            Open the issue on GitHub
          </a>
        ) : null}
        {(node.blockers ?? []).length > 0 ? (
          <div>
            <h3 class="detail-head">Waits on</h3>
            <ul class="blockers">
              {node.blockers!.map((b) => {
                const l = blockerLine(b);
                return (
                  <li key={b.url + l.text}>
                    {l.kind === 'link' && safeHref(l.url) ? (
                      <a href={safeHref(l.url)!} target="_blank" rel="noopener noreferrer">
                        {l.text}
                      </a>
                    ) : (
                      <span class={l.kind === 'note' ? 'muted' : undefined}>{l.text}</span>
                    )}
                  </li>
                );
              })}
            </ul>
          </div>
        ) : null}
        <p>
          {modeLabel(node.mode ?? 'with-you')} · {skillLabel(node.default_skill)}
        </p>
        {status !== 'done' && node !== campaign.root ? (
          <>
            <Choice
              legend="Mode"
              help="With you waits for you, one at a time per fleet. Without you can start on its own while AdmiralBot works the campaign."
              values={['with-you', 'without-you'] as Mode[]}
              value={node.mode ?? 'with-you'}
              label={modeLabel}
              disabled={busy}
              onPick={saveOverride}
            />
            {labelsDisagree(node) ? (
              <p class="note">
                Labels say {modeLabel(labelsDisagree(node)!)}.{' '}
                <Button variant="link" class="use-labels" label="Use labels" disabled={busy} onClick={() => saveOverride(null)} />
              </p>
            ) : null}
          </>
        ) : null}
        {error ? (
          <p class="alert" role="alert">
            {error}
          </p>
        ) : null}
        {canLaunch ? <p class="note">A launch from here runs with you watching, with autopilot off.</p> : null}
        <div class="actions">
          <Button label="Close" onClick={onClose} />
          {canLaunch ? <Button variant="primary" label={status === 'claimed-no-ship' ? 'Start anyway' : 'Launch'} onClick={() => onLaunch(status === 'claimed-no-ship')} /> : null}
        </div>
      </Actions>
    </Sheet>
  );
}

function stateOf(r: Row, ship: string | undefined, ended: boolean, clock: string): string {
  const n = r.node;
  const running = clock ? ` · ${clock}` : '';
  if (n.status === 'done') return 'closed';
  if (n.status === 'blocked') return '';
  if (n.status === 'underway') return `Underway · ${ship ?? n.ship ?? ''}${running}`;
  if (ship) return `Underway · ${ship}${running}`;
  if (ended) return '';
  return n.status === 'claimed-no-ship' ? 'Start anyway' : 'Start';
}

function WorkRepo({ campaign, onSaved }: { campaign: Campaign; onSaved: (c: Campaign) => void }) {
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(campaign.work_repo);
  const [error, setError] = useState('');
  const workRepoKey = ['campaign', campaign.id, 'work-repo'] as const;
  const save = async () => {
    const r = await call<Campaign>('PUT', `/campaigns/${campaign.id}/work-repo`, { repo: text.trim() });
    if (!r.ok) return setError(r.error);
    setError('');
    setEditing(false);
    onSaved(r.data);
  };
  if (!editing)
    return (
      <button type="button" class="pill mono" onClick={() => setEditing(true)}>
        {campaign.work_repo ? campaign.work_repo.replace(/^https?:\/\//, '') : 'Set work repo'}
      </button>
    );
  return (
    <span class="work-repo">
      <input
        type="text"
        aria-label="Work repo"
        value={text}
        placeholder="github.com/owner/repo"
        onInput={(e) => setText((e.target as HTMLInputElement).value)}
        onKeyDown={(e) => e.key === 'Enter' && void runPending(workRepoKey, save)}
      />
      <Button label="Save" pending={workRepoKey} onClick={save} />
      {error ? (
        <span class="alert" role="alert">
          {error}
        </span>
      ) : null}
    </span>
  );
}

export function CampaignPage({ id, title }: { id: string; title: boolean }) {
  const [campaign, setCampaign] = useState<Campaign | null>(null);
  const [missing, setMissing] = useState('');
  const [pickedUrl, setPickedUrl] = useState('');
  const [launching, setLaunching] = useState<{ node: CampaignNode; anyway: boolean; slice?: CampaignNode[] } | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [refreshFailed, setRefreshFailed] = useState(false);
  const [followChangeError, setFollowChangeError] = useState('');
  const [doneOpen, setDoneOpen] = useState(false);
  const fastSince = useRef<number | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const live = useRef(true);

  const load = async () => {
    const r = await call<Campaign>('GET', `/campaigns/${encodeURIComponent(id)}`);
    if (!live.current) return;
    clearTimeout(timer.current);
    if (!r.ok) {
      setMissing(r.error);
      setRefreshFailed(true);
      timer.current = setTimeout(() => void load(), SLOW_POLL_MS);
    } else {
      setMissing('');
      setRefreshFailed(false);
      setCampaign(r.data);
      noteCampaign(r.data);
      if (r.data.reading) fastSince.current ??= Date.now();
      else fastSince.current = null;
      timer.current = setTimeout(() => void load(), nextPoll({ reading: r.data.reading, fastForMs: fastSince.current ? Date.now() - fastSince.current : 0 }));
    }
  };

  useEffect(() => {
    live.current = true;
    setCampaign(null);
    void load();
    return () => {
      live.current = false;
      clearTimeout(timer.current);
    };
  }, [id]);

  const children = leaves(campaign?.root);
  const shipsKey = childShipsKey(campaign?.root ? [campaign.root, ...children] : children, ships.value);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    void load();
  }, [shipsKey]);

  const fleet = fleets.value.find((f) => f.key === campaign?.fleet);
  const canStart = !!fleet && fleet.docks.length > 0;
  const sliceCtx = { ended: !!campaign?.ended, canStart };
  const checked = keepSelectable(selected, children, sliceCtx, ships.value);
  useEffect(() => {
    if (checked.length !== selected.length) setSelected(checked);
  }, [checked.join(',')]);

  if (!campaign) {
    return (
      <main class="panel">
        <p class="line">{missing || 'Reading the campaign…'}</p>
      </main>
    );
  }

  const groups = campaign.root ? groupChildren(campaign.root) : [];
  const { closed, total } = campaign.progress;
  const shipName = (n: CampaignNode) => shipOfChild(n, ships.value)?.name;
  const now = useNow();
  const clockOf = (n: CampaignNode) => {
    const s = shipOfChild(n, ships.value);
    return s && isRunning(s) ? runningFor(s.launched, now) : '';
  };
  const unreadable = hasUnreadable(campaign.root);
  const kind = kindLabel(campaign.root?.labels);
  const picked = [campaign.root, ...children].find((c) => c?.url === pickedUrl) ?? null;
  const toggle = (url: string) => setSelected(checked.includes(url) ? checked.filter((u) => u !== url) : [...checked, url]);

  const changeFollow = async (path: string, method: string, body?: unknown) => {
    setFollowChangeError('');
    const r = await call<Campaign>(method, path, body);
    if (!r.ok) setFollowChangeError(r.error);
    else {
      setCampaign({ ...r.data, root: r.data.root ?? campaign.root });
      noteCampaign(r.data);
      void refreshCampaigns();
    }
  };

  const unfollow = () => changeFollow(`/campaigns/${campaign.id}/unfollow`, 'POST');
  const followAgain = () => changeFollow('/campaigns', 'POST', { reference: campaign.url });

  const rootHref = safeHref(campaign.url);
  const ref = `${campaign.repo.replace(/^https?:\/\//, '')}#${campaign.number}`;
  const [workHost, ...workPath] = campaign.repo.replace(/^https?:\/\//, '').split('/');
  const workRef = workHost.toLowerCase() === 'github.com' ? `${workPath.join('/')}#${campaign.number}` : campaign.url;
  const heading = campaign.root ? (
    <button type="button" class="camp-title-open" onClick={() => setPickedUrl(campaign.root!.url)}>
      {campaign.title}
    </button>
  ) : (
    campaign.title
  );

  return (
    <main class="panel camp-page">
      <div class="camp-bar">
        <a
          href={`/app/campaigns${search(campaign.fleet)}`}
          class="crumb"
          onClick={(e) => {
            if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
            e.preventDefault();
            endedOpen.value = campaign.ended;
            go('/app/campaigns', search(campaign.fleet));
          }}
        >
          Campaigns
        </a>
        {rootHref ? (
          <a class="mono muted issue-link" href={rootHref} target="_blank" rel="noopener noreferrer">
            {ref}
          </a>
        ) : (
          <span class="mono muted">{ref}</span>
        )}
        {fleet ? (
          <span class="pill">
            <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />
            {fleet.name}
          </span>
        ) : null}
        {campaign.ended ? <span class="pill ended">Ended</span> : null}
        <WorkRepo campaign={campaign} onSaved={(c) => setCampaign({ ...campaign, ...c, root: c.root ?? campaign.root })} />
        {!campaign.ended ? (
          <Button variant="quiet" label="Unfollow" pending={['campaign', campaign.id, 'unfollow']} onClick={unfollow} />
        ) : campaign.end_reason === 'unfollowed' ? (
          <Button variant="quiet" label="Follow again" pending={['campaign', campaign.id, 'follow']} onClick={followAgain} />
        ) : null}
      </div>
      <p class="kind">{kind}</p>
      {title ? <h1>{heading}</h1> : <h2 class="camp-title">{heading}</h2>}
      <div class="progress" role="img" aria-label={`${closed} of ${total} done`}>
        <span style={{ width: `${total > 0 ? (closed / total) * 100 : 0}%` }} />
      </div>
      <p class="meta">
        {closed} of {total} done · {campaign.ready_with_you} ready, with you · {campaign.ready_without_you} ready, without you · {campaign.counts?.blocked ?? 0} blocked · {campaign.counts?.underway ?? 0} underway
      </p>
      <p class="meta">
        {campaign.reading ? 'Reading the tree…' : campaign.last_read ? `Last read ${when(campaign.last_read)}` : 'Not read yet'}
      </p>
      {campaign.commitment ? (
        <p class="meta">{['AdmiralBot is working on this campaign', ...commitmentMarks(campaign.commitment)].join(' · ')}</p>
      ) : !campaign.ended ? (
        <p class="meta">Admiral is only following this campaign; AdmiralBot isn't working on it. Tell AdmiralBot "work on {workRef}" to start.</p>
      ) : null}
      {followChangeError ? (
        <p class="alert" role="alert">
          {followChangeError}
        </p>
      ) : null}
      {refreshFailed ? <p class="note">Couldn't refresh; showing the last read.</p> : null}
      {campaign.read_error ? (
        <p class="alert" role="alert">
          The last read failed: {campaign.read_error}
        </p>
      ) : null}
      {unreadable ? <p class="note">Some sub-issues couldn't be read, so this list may be short.</p> : null}
      {groups.map((g) => {
        const folded = g.key === 'done' && !doneOpen;
        const joinable = new Set(g.rows.filter((r) => canJoinSlice(r.node, sliceCtx, ships.value)).map((r) => r.node.url));
        return (
          <section key={g.key} class="camp-group" aria-labelledby={`cg-${g.key}`}>
            <h2 id={`cg-${g.key}`} class="group-head">
              {g.key === 'done' ? (
                <button type="button" class="group-toggle" aria-expanded={doneOpen} onClick={() => setDoneOpen(!doneOpen)}>
                  <span class={`fold-chevron${doneOpen ? ' open' : ''}`}>
                    <Icon name="chevron" size={14} />
                  </span>
                  {g.title} <span class="count">{g.rows.length}</span>
                </button>
              ) : (
                <>
                  {g.title} <span class="count">{g.rows.length}</span>
                </>
              )}
              {g.note ? <span class="group-note">{g.note}</span> : null}
            </h2>
            {folded ? null : (
              <ul class="ti-list">
                {g.rows.map((r) => {
                  const ship = shipName(r.node);
                  const blockers = (r.node.blockers ?? []).map((b) => b.title || b.url);
                  return (
                    <li key={r.node.url} class="ti-row">
                      {joinable.has(r.node.url) ? (
                        <label class="ti-pick">
                          <input type="checkbox" aria-label={r.node.title} checked={checked.includes(r.node.url)} onChange={() => toggle(r.node.url)} />
                        </label>
                      ) : joinable.size > 0 ? (
                        // An empty cell keeps the group's columns lined up with the rows that have a checkbox.
                        <span class="ti-pick" aria-hidden="true" />
                      ) : null}
                      {safeHref(r.node.url) ? (
                        <a class="mono ti-num ti-num-link" href={safeHref(r.node.url)!} target="_blank" rel="noopener noreferrer" aria-label={`Open issue #${r.node.number} on GitHub`}>
                          #{r.node.number}
                        </a>
                      ) : (
                        <span class="mono ti-num ti-num-link">#{r.node.number}</span>
                      )}
                      <button type="button" class="ti" onClick={() => setPickedUrl(r.node.url)}>
                        <span class="ti-type">{typeLabel(r.node.labels)}</span>
                        <span class="ti-main">
                          <span class="ti-title">
                            {r.prefix ? <span class="muted">{r.prefix} / </span> : null}
                            {r.node.title}
                          </span>
                          {blockers.length > 0 ? <span class="ti-sub">Waits on {blockers.join(', ')}</span> : null}
                        </span>
                        <span class="ti-state">
                          {stateOf(r, ship, campaign.ended, clockOf(r.node))}
                          {!['ready', 'underway'].includes(r.node.status ?? '') && r.node.mode ? <span class="ti-mode">{r.node.mode === 'with-you' ? 'with you' : 'without you'}</span> : null}
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
            {g.key === 'ready-without-you' && checked.length >= 2 ? (
              <div class="slice-bar">
                <Button variant="primary" label={`Launch slice (${checked.length})`} onClick={() => setLaunching({ node: children.find((c) => c.url === checked[0])!, anyway: false, slice: checked.map((u) => children.find((c) => c.url === u)!) })} />
                <Button variant="quiet" label="Clear" onClick={() => setSelected([])} />
              </div>
            ) : null}
          </section>
        );
      })}
      {picked ? (
        <Detail
          campaign={campaign}
          node={picked}
          canStart={canStart}
          onClose={() => setPickedUrl('')}
          onLaunch={(anyway) => {
            setLaunching({ node: picked, anyway });
            setPickedUrl('');
          }}
          onSaved={(c) => {
            setCampaign({ ...campaign, ...c, root: c.root ?? campaign.root });
            noteCampaign(c);
          }}
        />
      ) : null}
      {launching && fleet && fleet.docks.length > 0 ? (
        <LaunchForm
          fleet={fleet}
          campaign={{ campaignId: campaign.id, url: launching.node.url, ...(launching.slice && { issues: launching.slice.map((c) => c.url) }), title: launching.slice ? `${launching.slice.length} children: ${launching.node.title}` : launching.node.title, mode: launching.node.mode ?? 'with-you', skill: launching.node.skill, ...(!launching.slice && { labels: launching.node.labels }), startAnyway: launching.anyway }}
          onClose={() => setLaunching(null)}
          onLaunched={() => {
            if (launching.slice) setSelected([]);
            void load();
          }}
        />
      ) : null}
    </main>
  );
}
