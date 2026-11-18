import { useEffect, useRef, useState } from 'preact/hooks';
import { call } from './api';
import { followReference } from './campaignsLink';
import { Icon } from './Icon';

interface Hit {
  title: string;
  reference: string;
  url: string;
  state: 'open' | 'closed';
  parent: boolean;
  progress: { closed: number; total: number };
  status: '' | 'followed' | 'unfollowed' | 'ended';
}

const TYPING_MS = 200;

function HitRow({ hit, busy, onPick }: { hit: Hit; busy: boolean; onPick: (h: Hit) => void }) {
  const followed = hit.status === 'followed';
  return (
    <li>
      <button type="button" class="ac-row" disabled={followed || busy} onClick={() => onPick(hit)}>
        <span class={`ac-kind${hit.parent ? ' parent' : ''}`}>{hit.parent ? `${hit.progress.closed}/${hit.progress.total}` : '·'}</span>
        <span class="ac-text">
          <span class="ac-title">{hit.title}</span>
          <span class="ac-ref mono">
            <span>{hit.reference}</span>
            {hit.state === 'closed' ? <span> · closed</span> : null}
          </span>
        </span>
        {followed ? <span class="ac-tag">Following</span> : null}
      </button>
    </li>
  );
}

function Group({ caption, hits, busy, onPick }: { caption: string; hits: Hit[]; busy: boolean; onPick: (h: Hit) => void }) {
  if (hits.length === 0) return null;
  return (
    <>
      <p class="ac-cap">{caption}</p>
      <ul class="ac-list">
        {hits.map((h) => (
          <HitRow key={h.reference} hit={h} busy={busy} onPick={onPick} />
        ))}
      </ul>
    </>
  );
}

/** The Add campaign modal: finds an issue in the fleet and follows it. */
export function AddCampaign({ fleet, onClose }: { fleet: string; onClose: () => void }) {
  const [q, setQ] = useState('');
  const [hits, setHits] = useState<Hit[] | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => input.current?.focus(), []);

  useEffect(() => {
    setHits(null);
    const ctl = new AbortController();
    const run = async () => {
      const r = await call<{ results: Hit[] | null }>('GET', `/fleets/${encodeURIComponent(fleet)}/campaigns/search?q=${encodeURIComponent(q.trim())}`, undefined, undefined, { signal: ctl.signal });
      if (ctl.signal.aborted) return;
      if (r.ok) {
        setError('');
        setHits(r.data.results ?? []);
      } else {
        setHits([]);
        setError(r.error);
      }
    };
    const t = setTimeout(() => void run(), q.trim() ? TYPING_MS : 0);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [fleet, q]);

  const pick = async (h: Hit) => {
    if (busy) return;
    setBusy(true);
    setError('');
    const err = await followReference(h.reference, fleet);
    if (err) {
      setBusy(false);
      setError(err);
    } else onClose();
  };

  const typed = q.trim() !== '';
  const list = hits ?? [];
  const parents = list.filter((h) => h.parent);
  const rest = list.filter((h) => !h.parent);
  return (
    <div class="backdrop launch-backdrop" onClick={(e) => e.target === e.currentTarget && !busy && onClose()}>
      <div class="sheet launch-form ac-form" role="dialog" aria-modal="true" aria-label="Add campaign" onKeyDown={(e) => e.key === 'Escape' && !busy && onClose()}>
        <h2 class="sheet-title">Add campaign</h2>
        <div class="launch-body">
          <div class="ac-search">
            <Icon name="search" size={16} />
            <input ref={input} type="search" aria-label="Find an issue" placeholder="Search, #n, owner/repo#n or a URL" value={q} onInput={(e) => setQ(e.currentTarget.value)} />
          </div>
          {error ? (
            <p class="alert" role="alert">
              {error}
            </p>
          ) : null}
          {typed ? (
            <>
              <Group caption="Parent issues" hits={parents} busy={busy} onPick={pick} />
              <Group caption="Other issues" hits={rest} busy={busy} onPick={pick} />
            </>
          ) : (
            <Group caption="Open parent issues in this fleet" hits={list} busy={busy} onPick={pick} />
          )}
          {typed && hits && list.length === 0 && !error ? <p class="note">No issue in this fleet matches.</p> : null}
          <button type="button" class="ac-row ac-new" disabled>
            <Icon name="plus" size={16} />
            <span>Start a new campaign</span>
          </button>
        </div>
      </div>
    </div>
  );
}
