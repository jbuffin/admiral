import { Fragment } from 'preact';
import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { call, fetchPRDetail, type MooredPR, type PRDetail, type Ship } from './api';
import { MergeControl } from './PeekMerge';
import { MooredActions } from './MooredActions';
import { Failure } from './PeekItem';
import { Sheet } from './Sheet';
import { chooseStackDetail, actingWhile, defaultPR, feedbackBarText, headerChips, orderPRs, prDot, prNumber, showAnswerAll, stackBarText, stackedPRs } from './prTab';
import { staleNote } from './moored';
import { PRSections } from './PRDetailSections';

const REFRESH_MS = 30_000;

// What the tab last read, so reopening it shows the stack in order at once and the refresh only updates it.
const lastRead = new Map<string, PRDetail>();
const seen = (urls: string[]) => Object.fromEntries(urls.flatMap((u) => (lastRead.has(u) ? [[u, lastRead.get(u)!]] : [])));

/** Each carried PR's detail, read when the tab opens and every 30s while it stays open; a failed read keeps the last good detail, and a PR never read has none. */
function useDetails(session: string, urls: string[]): Record<string, PRDetail> {
  const [details, setDetails] = useState<Record<string, PRDetail>>(() => seen(urls));
  const key = urls.join('\n');
  useLayoutEffect(() => {
    let live = true;
    const read = () =>
      urls.forEach((url) =>
        void fetchPRDetail(session, url).then((r) => {
          if (!live) return;
          if (!r.ok) return;
          lastRead.set(url, r.data);
          setDetails((d) => ({ ...d, [url]: r.data }));
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
    <>
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
    </>
  );
}

/** The feedback bar of a moored PR with new feedback; Answer review shows the exact command in a sheet and starts the round on confirm. */
function FeedbackBar({ ship, pr, locked, onBusy }: { ship: Ship; pr: MooredPR; locked: boolean; onBusy: (busy: boolean) => void }) {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (asking) cancel.current?.focus();
  }, [asking]);
  const run = async () => {
    setBusy(true);
    onBusy(true);
    setError('');
    const r = await call('POST', `/ships/${encodeURIComponent(ship.session)}/answer-review`, { pr: pr.url });
    if (!r.ok) {
      setBusy(false);
      onBusy(false);
      return setError(r.error);
    }
    // The bar stays off until the ships push removes it, so a second tap cannot meet the live-ship error.
    setAsking(false);
  };
  return (
    <div class="feedback-bar">
      <span class="fb-text">{feedbackBarText(pr)}</span>
      <button
        type="button"
        class="btn primary"
        disabled={locked || busy}
        onClick={() => {
          setError('');
          setAsking(true);
        }}
      >
        Answer review
      </button>
      {asking ? (
        <Sheet label="Answer review" kind="launch-backdrop stop-backdrop" onClose={() => !busy && setAsking(false)}>
          <p class="line">Starts a feedback round on #{prNumber(pr.url)} in the same Commodore run.</p>
          <p class="cmd-block mono">{pr.answer}</p>
          {error ? (
            <p class="launch-error" role="alert">
              {error}
            </p>
          ) : null}
          <div class="launch-foot">
            <button type="button" ref={cancel} class="btn" disabled={busy} onClick={() => setAsking(false)}>
              Cancel
            </button>
            <button type="button" class="btn primary" disabled={busy} onClick={() => void run()}>
              Answer review
            </button>
          </div>
        </Sheet>
      ) : null}
    </div>
  );
}

/** Answer all reviews in the stack bar: the sheet shows the server's command and posts the stack's URL on confirm. */
function AnswerAll({ ship, stackUrl, command, locked, onBusy }: { ship: Ship; stackUrl: string; command: string; locked: boolean; onBusy: (busy: boolean) => void }) {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (asking) cancel.current?.focus();
  }, [asking]);
  const run = async () => {
    setBusy(true);
    onBusy(true);
    setError('');
    const r = await call('POST', `/ships/${encodeURIComponent(ship.session)}/answer-review`, { stack: stackUrl });
    if (!r.ok) {
      setBusy(false);
      onBusy(false);
      return setError(r.error);
    }
    setAsking(false);
  };
  return (
    <>
      <button
        type="button"
        class="btn primary"
        disabled={locked || busy}
        onClick={() => {
          setError('');
          setAsking(true);
        }}
      >
        Answer all reviews
      </button>
      {asking ? (
        <Sheet label="Answer all reviews" kind="launch-backdrop stop-backdrop" onClose={() => !busy && setAsking(false)}>
          <p class="line">One Commodore run answers the feedback on every PR in the stack.</p>
          <p class="cmd-block mono">{command}</p>
          {error ? (
            <p class="launch-error" role="alert">
              {error}
            </p>
          ) : null}
          <div class="launch-foot">
            <button type="button" ref={cancel} class="btn" disabled={busy} onClick={() => setAsking(false)}>
              Cancel
            </button>
            <button type="button" class="btn primary" disabled={busy} onClick={() => void run()}>
              Answer all reviews
            </button>
          </div>
        </Sheet>
      ) : null}
    </>
  );
}

/** A ship's PRs: sub-tabs in stack order, the selected PR's header, chips and sections, and for a moored ship Merge and Don't follow reviews. */
export function PRTab({ ship }: { ship: Ship }) {
  const [error, setError] = useState('');
  const [merged, setMerged] = useState(false);
  const [acting, setActing] = useState(false);
  const [picked, setPicked] = useState('');
  const carried = ship.prs ?? [];
  const details = useDetails(ship.session, carried.map((p) => p.url));
  const chosen = chooseStackDetail(carried, details);
  const stack = chosen?.stack ?? null;
  const prs = orderPRs(carried, stack);
  const moored = ship.status === 'moored';
  useEffect(() => setActing((a) => actingWhile(a, moored)), [moored]);
  if (prs.length === 0) return <p class="line">No pull request yet. A PR shows here as soon as the ship or its crew opens one.</p>;
  const current = prs.find((p) => p.url === picked) ?? defaultPR(prs)!;
  const bar = stackBarText(stackedPRs(prs, stack).length, stack !== null);
  return (
    <div class="pr-tab">
      {bar ? (
        <div class="stack-bar">
          <span class="stack-text">{bar}</span>
          {moored && chosen && showAnswerAll(carried, stack, chosen.answerAll) ? (
            <AnswerAll ship={ship} stackUrl={stack!.url} command={chosen.answerAll!} locked={acting} onBusy={setActing} />
          ) : null}
        </div>
      ) : null}
      {prs.length > 1 ? (
        <div class="pr-subtabs" role="tablist" aria-label="Pull requests, bottom of the stack first">
          {prs.map((p, i) => (
            <Fragment key={p.url}>
              {i > 0 ? (
                <span class="stack-arrow" aria-hidden="true">
                  ›
                </span>
              ) : null}
              <button type="button" role="tab" aria-selected={p.url === current.url} class={`pr-subtab${p.state === 'MERGED' ? ' merged' : ''}`} onClick={() => setPicked(p.url)}>
                <span class={`dot ${prDot(p)}`} />
                <span class="num">#{prNumber(p.url)}</span>
                {details[p.url] ? <span class="ttl">{details[p.url].title}</span> : null}
              </button>
            </Fragment>
          ))}
        </div>
      ) : null}
      <div class="pr-detail">
        <PRHeader ship={ship} url={current.url} detail={details[current.url]} />
        {moored && feedbackBarText(current) ? <FeedbackBar key={current.url} ship={ship} pr={current} locked={acting} onBusy={setActing} /> : null}
        {moored && (merged || ship.stack_ready) ? (
          <div class="peek-form">
            {merged ? <p class="line">Merged</p> : <MergeControl ship={ship} onMerged={() => setMerged(true)} onError={setError} locked={acting} onBusy={setActing} />}
          </div>
        ) : null}
        {details[current.url] ? <PRSections key={current.url} detail={details[current.url]} /> : null}
        {moored && (!merged || error) ? (
          <div class="peek-form">
            {merged ? null : <MooredActions ship={ship} tab onError={setError} locked={acting} onBusy={setActing} />}
            <Failure message={error} />
          </div>
        ) : null}
      </div>
    </div>
  );
}
