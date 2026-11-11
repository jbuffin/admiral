import { signal } from '@preact/signals';
import { useEffect, useRef, useState } from 'preact/hooks';
import { call } from './api';
import {
  blockerLine,
  campaignMeta,
  childShipsKey,
  groupChildren,
  hasUnreadable,
  kindLabel,
  leaves,
  modeLabel,
  nextPoll,
  pasteHint,
  shipOfChild,
  skillLabel,
  statusLabel,
  typeLabel,
  type Campaign,
  type CampaignNode,
  type Mode,
  type Row,
} from './campaign';
import { campaignList, endedOpen, followReference, noteCampaign, refreshCampaigns } from './campaignsLink';
import { activeFleet, fleetPaint, fleets } from './fleets';
import { Icon } from './Icon';
import { LaunchForm } from './LaunchDialog';
import { campaignPath, go } from './router';
import { Sheet } from './Sheet';
import { ships } from './shipsLink';

const when = (iso: string | undefined) => {
  const t = iso ? Date.parse(iso) : NaN;
  return Number.isNaN(t) ? '' : new Date(t).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
};

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

/** The field that follows a pasted issue; answers its refusal in place. */
function PasteField() {
  const [text, setText] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const hint = pasteHint(text);
  const submit = async () => {
    const t = text.trim();
    if (!t || hint || busy) return;
    setBusy(true);
    setError('');
    const err = await followReference(t);
    setBusy(false);
    if (err) setError(err);
    else {
      setText('');
      addOpen.value = false;
    }
  };
  return (
    <div class="paste">
      <input
        type="text"
        class="paste-input"
        aria-label="Campaign issue"
        placeholder="Paste an issue URL or owner/repo#n"
        value={text}
        autoFocus={addOpen.value}
        onInput={(e) => {
          setText((e.target as HTMLInputElement).value);
          setError('');
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter') void submit();
        }}
      />
      {hint ? <p class="note hint">{hint}</p> : null}
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}

export const addOpen = signal(false);

export function CampaignSidebar({ onNavigate }: { onNavigate?: () => void }) {
  const fleet = activeFleet.value;
  const mine = campaignList.value.filter((c) => c.fleet === fleet?.key);
  const active = mine.filter((c) => !c.ended);
  const ended = mine.filter((c) => c.ended).length;
  const here = typeof location === 'undefined' ? '' : location.pathname;
  return (
    <div class="camp-side">
      <div class="sec">
        <span>Campaigns</span>
        <button type="button" class="icon-btn" aria-label="Add campaign" aria-expanded={addOpen.value} onClick={() => (addOpen.value = !addOpen.value)}>
          <Icon name="plus" size={14} />
        </button>
      </div>
      {addOpen.value ? <PasteField /> : null}
      <ul class="camp-list">
        {active.map((c) => (
          <li key={c.id}>
            <a class="camp-row" href={campaignPath(c.id) + search(c.fleet)} aria-current={here.endsWith(`/campaigns/${c.id}`) ? 'page' : undefined} onClick={(e) => (open(e, c), onNavigate?.())}>
              <Ring done={c.progress.closed} total={c.progress.total} />
              <span class="camp-name">{c.title}</span>
              <span class="camp-meta">{campaignMeta(c)}</span>
              {c.ready_with_you > 0 ? <span class="camp-ready">{c.ready_with_you}</span> : null}
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
            onNavigate?.();
          }}
        >
          Ended ({ended})
        </button>
      ) : null}
    </div>
  );
}

/** The Campaigns surface: the followed campaigns of the fleet, or the ended ones; the phone also gets the paste field. */
export function CampaignsList({ phone, title }: { phone: boolean; title: boolean }) {
  const fleet = activeFleet.value;
  const mine = campaignList.value.filter((c) => c.fleet === fleet?.key);
  const ended = mine.filter((c) => c.ended);
  const showEnded = endedOpen.value;
  const list = showEnded ? ended : mine.filter((c) => !c.ended);
  return (
    <main class="panel">
      {title ? <h1>Campaigns</h1> : null}
      {phone ? <PasteField /> : null}
      <div class="camp-tabs">
        <button type="button" class={`btn${showEnded ? '' : ' primary'}`} aria-pressed={!showEnded} onClick={() => (endedOpen.value = false)}>
          Following ({mine.length - ended.length})
        </button>
        <button type="button" class={`btn${showEnded ? ' primary' : ''}`} aria-pressed={showEnded} onClick={() => (endedOpen.value = true)}>
          Ended ({ended.length})
        </button>
      </div>
      {list.length === 0 ? <p class="line">{showEnded ? 'No campaign has ended.' : 'Follow a campaign by pasting its issue, or with Follow on an order.'}</p> : null}
      <ul class="camp-cards">
        {list.map((c) => (
          <li key={c.id}>
            <a class="camp-card" href={campaignPath(c.id) + search(c.fleet)} onClick={(e) => open(e, c)}>
              <Ring done={c.progress.closed} total={c.progress.total} />
              <span class="camp-name">{c.title}</span>
              <span class="camp-meta">{campaignMeta(c)}</span>
            </a>
          </li>
        ))}
      </ul>
    </main>
  );
}

function Detail({ campaign, node, onClose, onLaunch, onSaved }: { campaign: Campaign; node: CampaignNode; onClose: () => void; onLaunch: (anyway: boolean) => void; onSaved: (c: Campaign) => void }) {
  const [mode, setMode] = useState<Mode | ''>(node.mode ?? '');
  const [kind, setKind] = useState(node.skill?.kind || 'none');
  const [name, setName] = useState(node.skill?.name ?? '');
  const [args, setArgs] = useState(node.skill?.args ?? '');
  const [error, setError] = useState('');
  const save = async (clear: boolean) => {
    setError('');
    const body = clear ? { child_url: node.url, mode: '' } : { child_url: node.url, mode, skill: { kind, name: kind === 'skill' ? name : '', args: kind === 'none' ? '' : args } };
    const r = await call<Campaign>('PUT', `/campaigns/${campaign.id}/override`, body);
    if (!r.ok) return setError(r.error);
    onSaved(r.data);
    onClose();
  };
  const status = node.status ?? 'ready';
  const canLaunch = !campaign.ended && (status === 'ready' || status === 'claimed-no-ship');
  return (
    <Sheet label={node.title} onClose={onClose}>
      <div class="detail">
        <p class="mono">
          {node.repo ? `${node.repo}#${node.number}` : `#${node.number}`} · {statusLabel(status)}
          {node.ship ? ` · ${node.ship}` : ''}
        </p>
        <a href={node.url} target="_blank" rel="noopener noreferrer">
          Open the issue on GitHub
        </a>
        {(node.blockers ?? []).length > 0 ? (
          <div>
            <h3 class="detail-head">Waits on</h3>
            <ul class="blockers">
              {node.blockers!.map((b) => {
                const l = blockerLine(b);
                return (
                  <li key={b.url + l.text}>
                    {l.kind === 'link' ? (
                      <a href={l.url} target="_blank" rel="noopener noreferrer">
                        {l.text}
                      </a>
                    ) : (
                      <span class="muted">{l.text}</span>
                    )}
                  </li>
                );
              })}
            </ul>
          </div>
        ) : null}
        <p>
          {modeLabel(node.mode ?? 'with-you')} · {skillLabel(node.skill)}
          {node.overridden ? ' · overridden' : ''}
        </p>
        {!campaign.ended && status !== 'done' ? (
          <details class="override">
            <summary>Change mode or skill</summary>
            <label class="field">
              <span>Mode</span>
              <select value={mode} onChange={(e) => setMode((e.target as HTMLSelectElement).value as Mode)}>
                <option value="with-you">With you</option>
                <option value="without-you">Without you</option>
              </select>
            </label>
            <label class="field">
              <span>Starts on</span>
              <select value={kind} onChange={(e) => setKind((e.target as HTMLSelectElement).value as typeof kind)}>
                <option value="commodore">Commodore</option>
                <option value="skill">A skill</option>
                <option value="none">Just launch</option>
              </select>
            </label>
            {kind === 'skill' ? (
              <label class="field">
                <span>Skill name</span>
                <input type="text" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} />
              </label>
            ) : null}
            {kind !== 'none' ? (
              <label class="field">
                <span>Arguments</span>
                <input type="text" value={args} onInput={(e) => setArgs((e.target as HTMLInputElement).value)} />
              </label>
            ) : null}
            <div class="actions">
              <button type="button" class="btn" onClick={() => void save(false)}>
                Save
              </button>
              {node.overridden ? (
                <button type="button" class="btn" onClick={() => void save(true)}>
                  Reset
                </button>
              ) : null}
            </div>
          </details>
        ) : null}
        {error ? (
          <p class="alert" role="alert">
            {error}
          </p>
        ) : null}
        {canLaunch ? <p class="note">A launch from here runs with you watching, with autopilot off.</p> : null}
        <div class="actions">
          <button type="button" class="btn" onClick={onClose}>
            Close
          </button>
          {canLaunch ? (
            <button type="button" class="btn primary" onClick={() => onLaunch(status === 'claimed-no-ship')}>
              {status === 'claimed-no-ship' ? 'Start anyway' : 'Launch'}
            </button>
          ) : null}
        </div>
      </div>
    </Sheet>
  );
}

function stateOf(r: Row, ship: string | undefined, ended: boolean): string {
  const n = r.node;
  if (n.status === 'done') return 'closed';
  if (n.status === 'blocked') return '';
  if (n.status === 'underway') return `Underway · ${ship ?? n.ship ?? ''}`;
  if (ship) return `Underway · ${ship}`;
  if (ended) return '';
  return n.status === 'claimed-no-ship' ? 'Start anyway' : 'Start';
}

function WorkRepo({ campaign, onSaved }: { campaign: Campaign; onSaved: (c: Campaign) => void }) {
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(campaign.work_repo);
  const [error, setError] = useState('');
  const save = async () => {
    const r = await call<Campaign>('PUT', `/campaigns/${campaign.id}/work-repo`, { repo: text.trim() });
    if (!r.ok) return setError(r.error);
    setError('');
    setEditing(false);
    onSaved(r.data);
  };
  if (campaign.ended) return campaign.work_repo ? <span class="pill mono">{campaign.work_repo.replace(/^https?:\/\//, '')}</span> : null;
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
        onKeyDown={(e) => e.key === 'Enter' && void save()}
      />
      <button type="button" class="btn" onClick={() => void save()}>
        Save
      </button>
      {error ? <span class="alert">{error}</span> : null}
    </span>
  );
}

export function CampaignPage({ id, title }: { id: string; title: boolean }) {
  const [campaign, setCampaign] = useState<Campaign | null>(null);
  const [missing, setMissing] = useState('');
  const [picked, setPicked] = useState<CampaignNode | null>(null);
  const [launching, setLaunching] = useState<{ node: CampaignNode; anyway: boolean } | null>(null);
  const fastSince = useRef<number | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const live = useRef(true);

  const load = async () => {
    const r = await call<Campaign>('GET', `/campaigns/${encodeURIComponent(id)}`);
    if (!live.current) return;
    if (!r.ok) {
      setMissing(r.error);
    } else {
      setMissing('');
      setCampaign(r.data);
      noteCampaign(r.data);
      if (r.data.reading) fastSince.current ??= Date.now();
      else fastSince.current = null;
      clearTimeout(timer.current);
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
  const shipsKey = childShipsKey(children, ships.value);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    void load();
  }, [shipsKey]);

  if (!campaign) {
    return (
      <main class="panel">
        <p class="line">{missing || 'Reading the campaign…'}</p>
      </main>
    );
  }

  const fleet = fleets.value.find((f) => f.key === campaign.fleet);
  const groups = campaign.root ? groupChildren(campaign.root) : [];
  const { closed, total } = campaign.progress;
  const shipName = (n: CampaignNode) => shipOfChild(n, ships.value)?.name;
  const unreadable = hasUnreadable(campaign.root);
  const kind = kindLabel(campaign.root?.labels);

  const unfollow = async () => {
    const r = await call<Campaign>('POST', `/campaigns/${campaign.id}/unfollow`);
    if (r.ok) {
      setCampaign({ ...campaign, ...r.data, root: r.data.root ?? campaign.root });
      noteCampaign(r.data);
      void refreshCampaigns();
    }
  };

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
        <span class="mono muted">
          {campaign.repo}#{campaign.number}
        </span>
        {fleet ? (
          <span class="pill">
            <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />
            {fleet.name}
          </span>
        ) : null}
        {campaign.ended ? <span class="pill ended">Ended</span> : null}
        <WorkRepo campaign={campaign} onSaved={(c) => setCampaign({ ...campaign, ...c, root: c.root ?? campaign.root })} />
        {campaign.ended ? null : (
          <button type="button" class="btn danger" onClick={() => void unfollow()}>
            Unfollow
          </button>
        )}
      </div>
      <p class="kind">{kind}</p>
      {title ? <h1>{campaign.title}</h1> : <h2 class="camp-title">{campaign.title}</h2>}
      <div class="progress" role="img" aria-label={`${closed} of ${total} done`}>
        <span style={{ width: `${total > 0 ? (closed / total) * 100 : 0}%` }} />
      </div>
      <p class="meta">
        {closed} of {total} done · {campaign.ready_with_you} ready, with you · {campaign.ready_without_you} ready, without you · {campaign.counts?.blocked ?? 0} blocked · {campaign.counts?.underway ?? 0} underway
      </p>
      <p class="meta">
        {campaign.reading ? 'Reading the tree…' : campaign.last_read ? `Last read ${when(campaign.last_read)}` : 'Not read yet'}
      </p>
      {campaign.read_error ? (
        <p class="alert" role="alert">
          The last read failed: {campaign.read_error}
        </p>
      ) : null}
      {unreadable ? <p class="note">Some sub-issues couldn't be read, so this list may be short.</p> : null}
      {groups.map((g) => (
        <section key={g.key} class="camp-group" aria-labelledby={`cg-${g.key}`}>
          <h2 id={`cg-${g.key}`} class="group-head">
            {g.title} <span class="count">{g.rows.length}</span>
            {g.note ? <span class="group-note">{g.note}</span> : null}
          </h2>
          <ul class="ti-list">
            {g.rows.map((r) => {
              const ship = shipName(r.node);
              const blockers = (r.node.blockers ?? []).map((b) => b.title || b.url);
              return (
                <li key={r.node.url}>
                  <button type="button" class="ti" onClick={() => setPicked(r.node)}>
                    <span class="mono ti-num">#{r.node.number}</span>
                    <span class="ti-type">{typeLabel(r.node.labels)}</span>
                    <span class="ti-main">
                      <span class="ti-title">
                        {r.prefix ? <span class="muted">{r.prefix} / </span> : null}
                        {r.node.title}
                      </span>
                      {blockers.length > 0 ? <span class="ti-sub">Waits on {blockers.join(', ')}</span> : null}
                    </span>
                    <span class="ti-state">{stateOf(r, ship, campaign.ended)}</span>
                  </button>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
      {picked ? (
        <Detail
          campaign={campaign}
          node={picked}
          onClose={() => setPicked(null)}
          onLaunch={(anyway) => {
            setLaunching({ node: picked, anyway });
            setPicked(null);
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
          campaign={{ campaignId: campaign.id, url: launching.node.url, title: launching.node.title, mode: launching.node.mode ?? 'with-you', skill: launching.node.skill, startAnyway: launching.anyway }}
          onClose={() => setLaunching(null)}
          onLaunched={() => void load()}
        />
      ) : null}
    </main>
  );
}
