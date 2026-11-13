import { useLayoutEffect, useState } from 'preact/hooks';
import { fetchPRDetail, type PRDetail, type Ship } from './api';
import { MergeControl } from './PeekMerge';
import { MooredActions } from './MooredActions';
import { Failure } from './PeekItem';
import { headerChips, orderPRs, prDot, prNumber, stackBarText } from './prTab';
import { staleNote } from './moored';

const REFRESH_MS = 30_000;

// What the tab last read, so reopening it shows the stack in order at once and the refresh only updates it.
const lastRead = new Map<string, PRDetail>();
const seen = (urls: string[]) => Object.fromEntries(urls.flatMap((u) => (lastRead.has(u) ? [[u, lastRead.get(u)!]] : [])));

/** Each carried PR's detail, read when the tab opens and every 30s while it stays open; a PR that failed to load has none. */
function useDetails(session: string, urls: string[]): Record<string, PRDetail> {
  const [details, setDetails] = useState<Record<string, PRDetail>>(() => seen(urls));
  const key = urls.join('\n');
  useLayoutEffect(() => {
    let live = true;
    const read = () =>
      urls.forEach((url) =>
        void fetchPRDetail(session, url).then((r) => {
          if (!live) return;
          if (r.ok) lastRead.set(url, r.data);
          else lastRead.delete(url);
          setDetails((d) => {
            if (r.ok) return { ...d, [url]: r.data };
            const { [url]: _gone, ...rest } = d;
            return rest;
          });
        }),
      );
    read();
    const timer = setInterval(read, REFRESH_MS);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, [session, key]);
  return details;
}

function PRHeader({ ship, url, detail }: { ship: Ship; url: string; detail: PRDetail | undefined }) {
  const pr = (ship.prs ?? []).find((p) => p.url === url)!;
  const note = staleNote(ship, Date.now());
  return (
    <div class="pr-detail">
      <div class="pr-head">
        <div class="pr-title-row">
          {detail ? <h3 class="pr-title">{detail.title}</h3> : <h3 class="pr-title">#{prNumber(url)}</h3>}
          {detail ? (
            <a class="btn" href={detail.url} target="_blank" rel="noreferrer">
              Open on GitHub
            </a>
          ) : null}
        </div>
        <div class="pr-meta">
          {detail ? <span class="mono">#{detail.number}</span> : null}
          {detail ? (
            <span class="mono">
              {detail.head} → {detail.base}
            </span>
          ) : (
            <span>Couldn't load this PR from GitHub.</span>
          )}
          {detail?.draft ? <span>Draft</span> : null}
        </div>
      </div>
      <div class="pr-status">
        {headerChips(pr).map((c) => (
          <span key={c.text} class={`pr-chip ${c.tone === 'muted' ? '' : c.tone}`}>
            {c.text}
          </span>
        ))}
        {note ? <span class="ship-tail">{note}</span> : null}
      </div>
    </div>
  );
}

/** A ship's PRs: sub-tabs in stack order, the selected PR's header and chips, and for a moored ship Merge and Don't follow reviews. */
export function PRTab({ ship }: { ship: Ship }) {
  const [error, setError] = useState('');
  const [merged, setMerged] = useState(false);
  const [acting, setActing] = useState(false);
  const [picked, setPicked] = useState('');
  const carried = ship.prs ?? [];
  const details = useDetails(ship.session, carried.map((p) => p.url));
  const stack = carried.map((p) => details[p.url]?.stack).find((s) => s) ?? null;
  const prs = orderPRs(carried, stack);
  const moored = ship.status === 'moored';
  if (prs.length === 0) return <p class="line">No pull request yet. A PR shows here as soon as the ship or its crew opens one.</p>;
  const current = prs.find((p) => p.url === picked) ?? prs[0];
  const bar = stackBarText(prs.length, stack !== null);
  return (
    <div class="pr-tab">
      {bar ? <div class="stack-bar">{bar}</div> : null}
      {prs.length > 1 ? (
        <div class="pr-subtabs" role="tablist" aria-label="Pull requests, bottom of the stack first">
          {prs.map((p) => (
            <button key={p.url} type="button" role="tab" aria-selected={p.url === current.url} class={`pr-subtab${p.state === 'MERGED' ? ' merged' : ''}`} onClick={() => setPicked(p.url)}>
              <span class={`dot ${prDot(p)}`} />
              <span class="num">#{prNumber(p.url)}</span>
              {details[p.url] ? <span class="ttl">{details[p.url].title}</span> : null}
            </button>
          ))}
        </div>
      ) : null}
      <PRHeader ship={ship} url={current.url} detail={details[current.url]} />
      {moored ? (
        <div class="peek-form">
          {merged ? (
            <p class="line">Merged</p>
          ) : (
            <>
              {ship.stack_ready ? <MergeControl ship={ship} onMerged={() => setMerged(true)} onError={setError} locked={acting} onBusy={setActing} /> : null}
              <MooredActions ship={ship} tab onError={setError} locked={acting} onBusy={setActing} />
            </>
          )}
          <Failure message={error} />
        </div>
      ) : null}
    </div>
  );
}
