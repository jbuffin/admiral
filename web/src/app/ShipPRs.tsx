import { Fragment } from 'preact';
import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { call, fetchPRDetail, fetchStackShape, linkStack, SLOW_TIMEOUT_MS, type MooredPR, type PRDetail, type Ship, type StackShape } from './api';
import { Actions, Button } from './Button';
import { MergeControl, UpdateBranchControl } from './PeekMerge';
import { MooredActions } from './MooredActions';
import { Icon } from './Icon';
import { Failure } from './PeekItem';
import { Sheet, useCancelFocus } from './Sheet';
import { StackSheet } from './StackSheet';
import { OpenPRs } from './OpenPRs';
import { chooseStackDetail, actingWhile, defaultPR, feedbackBarText, fixLine, fixStackInBar, hasOwnDraft, headerChips, mergeFor, mergeReasonLines, ownOpen, prDot, prNumber, reviewMark, reviewState, reviewStackBar, showAnswerAll, showAnswerReview, stackBarAction, stackBarLine, tabOrder } from './prTab';
import { MOOR_LINE, moorSheetParts, offersMoor, reReviewPlace, reviews, staleNote } from './moored';
import { AnswerReviewSheet } from './AnswerReview';
import { FixButton, FixSheet } from './Fix';
import { ReReviewSheet } from './ReReview';
import { PRSections } from './PRDetailSections';
import { MarkReadyButton, MarkReadySheet, type ReadyLine } from './ReadyForReview';

const REFRESH_MS = 30_000;

// What the tab last read, so reopening it shows the stack in order at once and the refresh only updates it.
const lastRead = new Map<string, PRDetail>();
// The newest read per PR; a slower, older answer must not overwrite a newer one.
const readSeq = new Map<string, number>();
const seen = (urls: string[]) => Object.fromEntries(urls.flatMap((u) => (lastRead.has(u) ? [[u, lastRead.get(u)!]] : [])));

/** Each carried PR's detail, read when the tab opens and every 30s while it stays open; a failed read keeps the last good detail, and a PR never read has none. `failed` holds the PRs whose last read failed. */
function useDetails(session: string, urls: string[]): { details: Record<string, PRDetail>; failed: Set<string>; reread: () => void; refresh: (url: string) => Promise<void> } {
  const [details, setDetails] = useState<Record<string, PRDetail>>(() => seen(urls));
  const [failed, setFailed] = useState<Set<string>>(() => new Set());
  const [rereads, setRereads] = useState(0);
  const mounted = useRef(true);
  const key = urls.join('\n');
  const mark = (url: string, bad: boolean) =>
    setFailed((f) => {
      if (f.has(url) === bad) return f;
      const n = new Set(f);
      if (bad) n.add(url);
      else n.delete(url);
      return n;
    });
  const readOne = (url: string, alive: () => boolean) => {
    const mine = (readSeq.get(url) ?? 0) + 1;
    readSeq.set(url, mine);
    return fetchPRDetail(session, url).then((r) => {
      if (!alive() || readSeq.get(url) !== mine) return;
      mark(url, !r.ok);
      if (!r.ok) return;
      lastRead.set(url, r.data);
      setDetails((d) => ({ ...d, [url]: r.data }));
    });
  };
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useLayoutEffect(() => {
    let live = true;
    const read = () => urls.forEach((url) => void readOne(url, () => live));
    read();
    const timer = setInterval(read, REFRESH_MS);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, [session, key, rereads]);
  const refresh = (url: string) => {
    void call('POST', '/moored/refresh');
    return readOne(url, () => mounted.current);
  };
  return { details, failed, reread: () => setRereads((n) => n + 1), refresh };
}

/** Where a PR's checks, description, reviewers and timeline go, held open while GitHub has not answered yet. */
function PRLoading({ failed }: { failed: boolean }) {
  return (
    <div class="pr-loading" role="status">
      <span class="spinner" aria-hidden="true" />
      <span>{failed ? 'Trying again…' : 'Loading from GitHub…'}</span>
    </div>
  );
}

/** The ship's PR shape, read when the tab opens, every 30s and on `read`; a failed read keeps the last good one, and a ship of one PR has none. */
function useShape(session: string, many: boolean): [StackShape | null, (s: StackShape) => void, () => Promise<StackShape | null>] {
  const [shape, setShape] = useState<StackShape | null>(null);
  const live = useRef(true);
  // A read that started before a newer answer (a link's, or a later read) must not overwrite it.
  const seq = useRef(0);
  const read = async () => {
    const mine = ++seq.current;
    const r = await fetchStackShape(session);
    if (!live.current || !r.ok) return null;
    if (mine === seq.current) setShape(r.data);
    return r.data;
  };
  const answer = (s: StackShape) => {
    seq.current++;
    setShape(s);
  };
  useLayoutEffect(() => {
    live.current = true;
    if (!many) return;
    void read();
    const timer = setInterval(() => void read(), REFRESH_MS);
    return () => {
      live.current = false;
      clearInterval(timer);
    };
  }, [session, many]);
  return [shape, answer, read];
}

function PRHeader({ ship, url, detail, failed, refresh }: { ship: Ship; url: string; detail: PRDetail | undefined; failed: boolean; refresh: () => Promise<void> }) {
  const pr = (ship.prs ?? []).find((p) => p.url === url)!;
  const note = staleNote(ship, Date.now());
  const state = reviewState(pr, detail);
  return (
    <>
      <div class="pr-head">
        <div class="pr-title-row">
          {detail ? <h3 class="pr-title">{detail.title}</h3> : <h3 class="pr-title">#{prNumber(url)}</h3>}
          <Button variant="quiet" icon class="pr-refresh" label="Refresh now" busyLabel="Refreshing" pending={['pr-refresh', ship.session, url]} onClick={refresh}>
            <Icon name="refresh" size={16} />
          </Button>
          {detail ? (
            <Button label="Open on GitHub" href={detail.url} external />
          ) : null}
        </div>
        <div class="pr-meta">
          {reviewMark(pr) ? <span class="role-mark">{reviewMark(pr)}</span> : null}
          {detail ? <span class="mono">#{detail.number}</span> : null}
          {detail ? (
            <span class="mono">
              {detail.head} → {detail.base}
            </span>
          ) : failed ? (
            <span>Couldn't load this PR from GitHub.</span>
          ) : null}
        </div>
      </div>
      <div class="pr-status">
        {state ? <span class={`pr-chip review-${state}`}>{state === 'draft' ? 'Draft' : 'Ready for review'}</span> : null}
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
  return (
    <div class="feedback-bar">
      <span class="fb-text">{feedbackBarText(pr)}</span>
      <Button label="Answer review" variant="primary" pending={['ship', ship.session, 'answer-review', pr.url]} disabled={locked || busy} onClick={() => setAsking(true)} />
      {asking ? (
        <AnswerReviewSheet
          ship={ship}
          pr={pr}
          onClose={() => setAsking(false)}
          onBusy={(b) => {
            setBusy(b);
            onBusy(b);
          }}
        />
      ) : null}
    </div>
  );
}

/** Answer all reviews in the stack bar: the sheet shows the server's command and posts the stack's URL on confirm. */
function AnswerAll({ ship, stackUrl, command, locked, onBusy }: { ship: Ship; stackUrl: string; command: string; locked: boolean; onBusy: (busy: boolean) => void }) {
  const [asking, setAsking] = useState(false);
  const [sent, setSent] = useState(false);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');
  useCancelFocus(asking);
  const run = async () => {
    setSending(true);
    onBusy(true);
    setError('');
    const r = await call('POST', `/ships/${encodeURIComponent(ship.session)}/answer-review`, { stack: stackUrl });
    setSending(false);
    if (!r.ok) {
      onBusy(false);
      return setError(r.error);
    }
    setSent(true);
    setAsking(false);
  };
  return (
    <>
      <Button
        label="Answer all reviews"
        variant="primary"
        pending={['ship', ship.session, 'answer-review', stackUrl]}
        disabled={locked || sent}
        onClick={() => {
          setError('');
          setAsking(true);
        }}
      />
      {asking ? (
        <Sheet label="Answer all reviews" kind="launch-backdrop stop-backdrop" onClose={() => !sending && setAsking(false)}>
          <p class="line">One Commodore run answers the feedback on every PR in the stack.</p>
          <p class="cmd-block mono">{command}</p>
          {error ? (
            <p class="launch-error" role="alert">
              {error}
            </p>
          ) : null}
          <Actions class="launch-foot">
            <Button label="Cancel" onClick={() => setAsking(false)} />
            <Button label="Answer all reviews" variant="primary" pending={['ship', ship.session, 'answer-review', stackUrl]} onClick={run} />
          </Actions>
        </Sheet>
      ) : null}
    </>
  );
}

/** Moor on a live ship's PR tab: the sheet lists the open PRs, and a refusal closes it and shows its message in the tab. The ship view stays; the ships push turns the row into Merge. */
function MoorRow({ ship, locked, onError, onBusy }: { ship: Ship; locked: boolean; onError: (message: string) => void; onBusy: (busy: boolean) => void }) {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  // Frozen when the sheet opens, so a poll never changes the list under the tap.
  const [parts, setParts] = useState(() => moorSheetParts(ship));
  useCancelFocus(asking);
  const run = async () => {
    setBusy(true);
    onBusy(true);
    onError('');
    const r = await call('POST', `/ships/${encodeURIComponent(ship.session)}/stop`, { moor: true }, undefined, { timeoutMs: SLOW_TIMEOUT_MS });
    setAsking(false);
    // The tab's lock must be free when the ship moors, or Merge arrives disabled.
    onBusy(false);
    // After a 200 the row stays off until the ships push marks the ship moored and removes it, so a second tap cannot post to a moored ship.
    if (r.ok) return;
    setBusy(false);
    onError(r.error);
  };
  return (
    <div class="peek-form">
      <div class="peek-merge">
        <Button
          label="Moor"
          busy={busy}
          disabled={locked}
          onClick={() => {
            onError('');
            setParts(moorSheetParts(ship));
            setAsking(true);
          }}
        />
        <span class="merge-hint">{MOOR_LINE}</span>
      </div>
      {asking ? (
        <Sheet label="Moor" kind="launch-backdrop stop-backdrop" onClose={() => !busy && setAsking(false)}>
          <OpenPRs name={ship.name} urls={parts.urls} />
          <p class="line">{parts.explain}</p>
          <Actions class="launch-foot">
            <Button label="Cancel" onClick={() => setAsking(false)} />
            <Button label="Moor" variant="primary" onClick={run} />
          </Actions>
        </Sheet>
      ) : null}
    </div>
  );
}

/** A ship's PRs: the stack bar, sub-tabs in stack order, the selected PR's header, chips and sections, and for a moored ship Merge and Don't follow reviews. */
export function PRTab({ ship }: { ship: Ship }) {
  const [error, setError] = useState('');
  const [mergeError, setMergeError] = useState('');
  const [mergeStale, setMergeStale] = useState(false);
  const [merged, setMerged] = useState<Set<string>>(new Set());
  const [mergedAll, setMergedAll] = useState(false);
  const [acting, setActing] = useState(false);
  const [picked, setPicked] = useState('');
  // The URL of the Re-review sheet that's open: a PR's, or '' for the stack.
  const [reReview, setReReview] = useState<string | null>(null);
  const [stacking, setStacking] = useState(false);
  const [fixing, setFixing] = useState(false);
  const [marking, setMarking] = useState<{ all: boolean; lines: ReadyLine[] } | null>(null);
  const [unseen, setUnseen] = useState(false);
  const carried = ship.prs ?? [];
  const { details, failed, reread, refresh } = useDetails(ship.session, carried.map((p) => p.url));
  const chosen = chooseStackDetail(carried, details);
  const stack = chosen?.stack ?? null;
  const [shape, setShape, read] = useShape(ship.session, carried.length > 1);
  const prs = tabOrder(carried, shape, stack);
  const moored = ship.status === 'moored';
  useEffect(() => setActing((a) => actingWhile(a, moored)), [moored]);
  const shownUrl = (prs.find((p) => p.url === picked) ?? defaultPR(prs))?.url;
  // A merge error belongs to the PR it failed on and to the mooring, which a poll can change under it.
  useEffect(() => {
    setMergeError('');
    setMergeStale(false);
  }, [shownUrl, moored]);
  if (prs.length === 0) return <p class="line">No pull request yet. A PR shows here as soon as the ship or its crew opens one.</p>;
  const current = prs.find((p) => p.url === picked) ?? defaultPR(prs)!;
  const reviewBar = reviewStackBar(ship);
  const place = reReviewPlace(ship);
  const respondedHere = moored && current.reviewing && current.responded && current.state === 'OPEN';
  const line = reviewBar ?? stackBarLine(shape, prs.find((p) => p.conflicts_with_base)?.conflicts_with_base);
  const fixOn = moored && !!ship.fix;
  const fixStack = fixOn && !!ship.fix?.stack;
  const fixText = fixOn ? fixLine(ship.fix) : '';
  const fixInBar = fixStack && fixStackInBar(!!line, !!reviewBar && place?.place === 'stack');
  const action = reviewBar ? 'none' : stackBarAction(shape, ship.status);
  const choice = mergeFor(shape, prs, current, !!ship.stack_ready);
  const justMerged = mergedAll || merged.has(current.url);
  const ownDraft = hasOwnDraft(prs, details);
  const markLines = ownOpen(prs).map((p): ReadyLine => {
    const state = reviewState(p, details[p.url]);
    return { url: p.url, draft: state === null ? undefined : state === 'draft', head: details[p.url]?.head, base: details[p.url]?.base };
  });
  const canMarkAll = markLines.length > 1 && ownDraft;
  const markAll = <MarkReadyButton session={ship.session} all lines={markLines} locked={acting} onAsk={() => setMarking({ all: true, lines: markLines })} />;
  const markOneLines: ReadyLine[] = [{ url: current.url, draft: true, head: details[current.url]?.head, base: details[current.url]?.base }];
  const markOne = ownOpen([current]).length > 0 && reviewState(current, details[current.url]) === 'draft';
  const reasonLines = mergeReasonLines(current, details[current.url]?.base);
  // The stack read decides: a layer of a GitHub stack is fixed as a stack, so it has no Update branch.
  const canUpdate = !!current.merge_reasons?.includes('behind') && !!details[current.url] && details[current.url].stack === null;
  // One error on the tab at a time: starting or failing either action drops the other's.
  const failMerge = (e: string) => {
    setMergeError(e);
    setError('');
  };
  const failAction = (e: string) => {
    setError(e);
    setMergeError('');
    setMergeStale(false);
  };
  const onMerged = () => {
    setMergeError('');
    setMergeStale(false);
    // The default PR is the first open one, so a merged PR would otherwise hand the view to the next.
    setPicked(current.url);
    if (choice.kind === 'pr') setMerged((m) => new Set(m).add(current.url));
    else setMergedAll(true);
    if (carried.length > 1) void read();
  };
  return (
    <div class="pr-tab">
      {line ? (
        <div class="stack-bar">
          <span class="stack-text">
            <strong>{line.lead}</strong>
            {line.rest}
          </span>
          {reviewBar && place?.place === 'stack' ? (
            <Button label="Re-review stack" variant="primary" onClick={() => setReReview('')} />
          ) : fixInBar ? (
            <>
              <span class="fix-line">{fixText}</span>
              <FixButton ship={ship} locked={acting} onAsk={() => setFixing(true)} />
            </>
          ) : moored && chosen && showAnswerAll(carried, stack, chosen.answerAll, ship.fix) ? (
            <AnswerAll ship={ship} stackUrl={stack!.url} command={chosen.answerAll!} locked={acting} onBusy={setActing} />
          ) : null}
          {action === 'stack' ? (
            <Button label="Stack on GitHub" onClick={() => setStacking(true)} />
          ) : null}
          {action === 'after-moor' ? <span class="merge-hint">Stack after the ship moors</span> : null}
          {unseen && shape?.shape === 'chained' ? <span>GitHub hasn't shown the stack yet. It shows on the next refresh.</span> : null}
          {canMarkAll ? markAll : null}
        </div>
      ) : canMarkAll ? (
        <div class="fix-bar">
          <span class="fb-text">Some of these PRs are drafts.</span>
          {markAll}
        </div>
      ) : null}
      {fixStack && !fixInBar ? (
        <div class="fix-bar">
          <span class="fb-text">{fixText}</span>
          <FixButton ship={ship} locked={acting} onAsk={() => setFixing(true)} />
        </div>
      ) : null}
      {marking ? <MarkReadySheet session={ship.session} all={marking.all} lines={marking.lines} onBusy={setActing} onDone={reread} onClose={() => setMarking(null)} /> : null}
      {fixing && fixOn ? <FixSheet ship={ship} onClose={() => setFixing(false)} onBusy={setActing} /> : null}
      {stacking ? (
        <StackSheet
          lines={prs.map((p) => ({ url: p.url, head: details[p.url]?.head, base: details[p.url]?.base }))}
          pending={['ship', ship.session, 'stack-link']}
          link={() => linkStack(ship.session)}
          onClose={() => setStacking(false)}
          onShape={(s) => {
            setShape(s);
            setUnseen(s.shape === 'chained');
          }}
          failed={async () => (await read())?.shape === 'stacked'}
        />
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
              <button type="button" role="tab" aria-selected={p.url === current.url} class={`pr-subtab${p.state === 'MERGED' ? ' merged' : ''}`} onClick={() => {
                  setMergeError('');
                  setMergeStale(false);
                  setPicked(p.url);
                }}>
                <span class={`dot ${prDot(p)}`} />
                <span class="num">#{prNumber(p.url)}</span>
                {details[p.url] ? <span class="ttl">{details[p.url].title}</span> : null}
                {reviewState(p, details[p.url]) === 'draft' ? <span class="tag-draft">Draft</span> : null}
                {reviewState(p, details[p.url]) === 'ready_for_review' ? <span class="visually-hidden">ready for review</span> : null}
              </button>
            </Fragment>
          ))}
        </div>
      ) : null}
      <div class="pr-detail">
        <PRHeader ship={ship} url={current.url} detail={details[current.url]} failed={failed.has(current.url)} refresh={() => refresh(current.url)} />
        {markOne ? (
          <div class="fix-bar">
            <span class="fb-text">This PR is a draft.</span>
            <MarkReadyButton session={ship.session} all={false} lines={markOneLines} locked={acting} onAsk={() => setMarking({ all: false, lines: markOneLines })} />
          </div>
        ) : null}
        {respondedHere ? (
          <div class="feedback-bar">
            <span class="fb-text">{current.author ? `@${current.author}` : 'The author'} responded since your review.</span>
            {place?.place === 'stack' ? null : (
              <Button label="Re-review" variant="primary" onClick={() => setReReview(current.url)} />
            )}
          </div>
        ) : null}
        {moored && !reviews(ship) && !justMerged && current.state === 'OPEN' && reasonLines.length > 0 ? (
          <div class="fix-bar">
            <span class="fb-text">
              {reasonLines.map((l) => (
                <div key={l}>{l}</div>
              ))}
            </span>
            {canUpdate ? <UpdateBranchControl key={current.url} ship={ship} pr={current.url} onError={failMerge} locked={acting} onBusy={setActing} /> : null}
          </div>
        ) : null}
        {fixOn && !fixStack && current.state === 'OPEN' && !current.reviewing ? (
          <div class="fix-bar">
            <span class="fb-text">{fixText}</span>
            <FixButton ship={ship} locked={acting} onAsk={() => setFixing(true)} />
          </div>
        ) : null}
        {moored && !current.reviewing && showAnswerReview(current, ship.fix) ? <FeedbackBar key={current.url} ship={ship} pr={current} locked={acting} onBusy={setActing} /> : null}
        {moored && (mergeError || (!reviews(ship) && (justMerged || choice.kind !== 'none' || choice.note))) ? (
          <div class="peek-form">
            {reviews(ship) ? null : justMerged ? (
              current.state === 'MERGED' ? null : <p class="line">Merged</p>
            ) : choice.kind === 'none' ? (
              <div class="peek-merge">
                <span class="merge-hint">{choice.note}</span>
              </div>
            ) : (
              <MergeControl key={current.url} ship={ship} pr={choice.kind === 'pr' ? current.url : undefined} stack={choice.kind === 'stack'} onMerged={onMerged} onError={failMerge} stale={mergeStale} onStale={setMergeStale} locked={acting} onBusy={setActing} />
            )}
            {mergeStale && (reviews(ship) || justMerged || choice.kind === 'none') ? (
              <div class="peek-merge">
                <Button label="Reload" onClick={() => location.reload()} />
              </div>
            ) : null}
            <Failure message={mergeError} />
          </div>
        ) : null}
        {offersMoor(ship) ? <MoorRow ship={ship} locked={acting} onError={setError} onBusy={setActing} /> : null}
        {!moored && error ? (
          <div class="peek-form">
            <Failure message={error} />
          </div>
        ) : null}
        {details[current.url] ? <PRSections key={current.url} detail={details[current.url]} session={ship.session} reread={reread} /> : <PRLoading failed={failed.has(current.url)} />}
        {moored && (!mergedAll || error) ? (
          <div class="peek-form">
            {mergedAll || reviews(ship) ? null : <MooredActions ship={ship} tab onError={failAction} locked={acting} onBusy={setActing} />}
            <Failure message={error} />
          </div>
        ) : null}
      </div>
      {reReview !== null ? <ReReviewSheet ship={ship} prUrl={reReview || undefined} onClose={() => setReReview(null)} /> : null}
    </div>
  );
}
