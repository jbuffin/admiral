import { useRef, useState } from 'preact/hooks';
import { markReadyForReview, type ReadyForReviewResult } from './api';
import { Actions, Button } from './Button';
import { Failure } from './PeekItem';
import { prNumber } from './prTab';
import { Sheet, useCancelFocus } from './Sheet';

const STALE_MARK = 'Admiral restarted, reload to mark ready for review';

export interface ReadyLine {
  url: string;
  draft: boolean;
  head?: string;
  base?: string;
}

const outcomeText = (r: ReadyForReviewResult): string =>
  r.outcome === 'marked_ready' ? 'marked ready for review' : r.outcome === 'already_ready_for_review' ? 'already ready for review, skipped' : `failed: ${r.error ?? 'GitHub gave no reason'}`;

interface Props {
  session: string;
  /** The PRs the request names, bottom to top; one for Mark ready for review, the stack's for Mark all. */
  lines: ReadyLine[];
  all: boolean;
  locked: boolean;
  onBusy: (busy: boolean) => void;
  /** Called once the server has answered, so the tab can read the PRs again. */
  onDone: () => void;
}

/** Mark ready for review (one PR) or Mark all ready for review (the stack): a button, and a sheet that names the PRs, posts on confirm and then shows each PR's outcome. */
export function MarkReady({ session, lines, all, locked, onBusy, onDone }: Props) {
  const label = all ? 'Mark all ready for review' : 'Mark ready for review';
  const pending = ['ship', session, 'ready-for-review', lines.map((l) => l.url).join(',')] as const;
  const [asking, setAsking] = useState(false);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');
  const [stale, setStale] = useState(false);
  const [results, setResults] = useState<ReadyForReviewResult[] | null>(null);
  // Frozen when the sheet opens, so a read never changes the list under the tap.
  const [frozen, setFrozen] = useState(lines);
  const running = useRef(false);
  useCancelFocus(asking);
  const close = () => {
    setAsking(false);
    setResults(null);
    setError('');
    setStale(false);
  };
  const run = async () => {
    if (running.current) return;
    running.current = true;
    setSending(true);
    onBusy(true);
    setError('');
    const r = await markReadyForReview(
      session,
      frozen.map((l) => l.url),
    );
    running.current = false;
    setSending(false);
    onBusy(false);
    if (!r.ok) {
      if (r.status === 403) setStale(true);
      return setError(r.status === 403 ? STALE_MARK : r.error);
    }
    onDone();
    if (r.data.results.every((x) => x.outcome !== 'failed')) return close();
    setResults(r.data.results);
  };
  const outcome = (url: string) => results?.find((r) => r.url === url);
  return (
    <>
      <Button
        label={label}
        pending={pending}
        disabled={locked}
        onClick={() => {
          setFrozen(lines);
          setError('');
          setStale(false);
          setResults(null);
          setAsking(true);
        }}
      />
      {asking ? (
        <Sheet label={label} kind="launch-backdrop stop-backdrop" onClose={() => !sending && close()}>
          <p class="sheet-sub">
            {all
              ? `Take these ${frozen.length} PRs out of draft on GitHub, bottom to top. A PR that is already ready for review is skipped, and nothing is merged.`
              : `Take #${prNumber(frozen[0].url)} out of draft on GitHub. It asks for review and merges nothing.`}
          </p>
          <ul class="sheet-prs">
            {frozen.map((l) => {
              const done = outcome(l.url);
              return (
                <li key={l.url}>
                  <span class="mono">#{prNumber(l.url)}</span>{' '}
                  {l.head && l.base ? (
                    <span class="mono">
                      {l.head} → {l.base}
                    </span>
                  ) : null}{' '}
                  <span class={done?.outcome === 'failed' ? 'ready-failed' : undefined}>{done ? outcomeText(done) : l.draft ? 'draft' : 'already ready for review'}</span>
                </li>
              );
            })}
          </ul>
          <Failure message={error} />
          <Actions class="launch-foot">
            <Button label={results ? 'Close' : 'Cancel'} onClick={close} />
            {stale ? <Button label="Reload" onClick={() => location.reload()} /> : null}
            {results ? null : <Button label={label} variant="primary" pending={pending} onClick={run} />}
          </Actions>
        </Sheet>
      ) : null}
    </>
  );
}
