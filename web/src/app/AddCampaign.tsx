import { useEffect, useRef, useState } from 'preact/hooks';
import { call } from './api';
import { followReference } from './campaignsLink';
import { Icon } from './Icon';
import { isReference } from './makeCampaign';
import { ConfirmStep, TextStep, type Confirmed, type Filed } from './NewCampaign';

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

/** The Add campaign modal: finds an issue in the fleet and follows it, or starts a campaign from a few words. */
export function AddCampaign({ fleet, onClose }: { fleet: string; onClose: () => void }) {
  const [q, setQ] = useState('');
  const [hits, setHits] = useState<Hit[] | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [step, setStep] = useState<'search' | 'text' | 'confirm'>('search');
  // Seeded from the search box while empty, then it keeps its own words.
  const [text, setText] = useState('');
  const [confirmed, setConfirmed] = useState<Confirmed | null>(null);
  const [filed, setFiled] = useState<Filed | null>(null);
  const input = useRef<HTMLInputElement>(null);
  const dialog = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (step === 'search') input.current?.focus();
    // The confirm step has nothing to focus until its projects load; the dialog keeps Escape working meanwhile.
    else if (step === 'confirm') dialog.current?.focus();
  }, [step]);

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

  const startNew = () => {
    if (!text) setText(isReference(q) ? '' : q.trim());
    setStep('text');
  };

  const typed = q.trim() !== '';
  const list = hits ?? [];
  const parents = list.filter((h) => h.parent);
  const rest = list.filter((h) => !h.parent);
  return (
    <div class="backdrop launch-backdrop" onClick={(e) => e.target === e.currentTarget && !busy && onClose()}>
      <div ref={dialog} tabIndex={-1} class="sheet launch-form ac-form" role="dialog" aria-modal="true" aria-label="Add campaign" onKeyDown={(e) => e.key === 'Escape' && !busy && onClose()}>
        <h2 class="sheet-title">Add campaign</h2>
        {step === 'search' ? (
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
            <button type="button" class="ac-row ac-new" disabled={busy} onClick={startNew}>
              <Icon name="plus" size={16} />
              <span>{typed ? `Start a new campaign: '${q.trim()}'` : 'Start a new campaign'}</span>
            </button>
          </div>
        ) : step === 'text' ? (
          <TextStep
            fleet={fleet}
            text={text}
            onText={setText}
            onBack={() => setStep('search')}
            onConfirm={(c) => {
              setConfirmed(c);
              setStep('confirm');
            }}
            onBusy={setBusy}
          />
        ) : confirmed ? (
          <ConfirmStep fleet={fleet} start={confirmed} filed={filed} onFiled={setFiled} onClose={onClose} onBack={() => setStep('text')} onBusy={setBusy} />
        ) : null}
      </div>
    </div>
  );
}
