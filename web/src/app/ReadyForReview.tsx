import { useRef, useState } from 'preact/hooks';
import { markReadyForReview, type ReadyForReviewResult } from './api';
import { Actions, Button } from './Button';
import { Failure } from './PeekItem';
import { prNumber } from './prTab';
import { Sheet, useCancelFocus } from './Sheet';

const STALE_MARK = 'Admiral restarted, reload to mark ready for review';

export interface ReadyLine {
  url: string;
  /** Unset while the PR's detail has not loaded, so the sheet does not call it a draft. */
  draft?: boolean;
  head?: string;
  base?: string;
}

const stateText = (draft?: boolean): string => (draft === undefined ? 'state unknown' : draft ? 'draft' : 'already ready for review');

const outcomeText = (r: ReadyForReviewResult): string =>
  r.outcome === 'marked_ready' ? 'marked ready for review' : r.outcome === 'already_ready_for_review' ? 'already ready for review, skipped' : `failed: ${r.error ?? 'GitHub gave no reason'}`;

const markLabel = (all: boolean) => (all ? 'Mark all ready for review' : 'Mark ready for review');
const pendingOf = (session: string, lines: ReadyLine[]) => ['ship', session, 'ready-for-review', lines.map((l) => l.url).join(',')] as const;

/** The button only asks; the tab holds the sheet, so a re-read that moves or removes the button leaves an open sheet alone. */
export function MarkReadyButton({ session, lines, all, locked, onAsk }: { session: string; lines: ReadyLine[]; all: boolean; locked: boolean; onAsk: () => void }) {
  return <Button label={markLabel(all)} pending={pendingOf(session, lines)} disabled={locked} onClick={onAsk} />;
}

interface SheetProps {
  session: string;
  /** The PRs the request names, bottom to top, frozen by the tab when the sheet opened. */
  lines: ReadyLine[];
  all: boolean;
  onBusy: (busy: boolean) => void;
  /** Called once the server has answered, so the tab can read the PRs again. */
  onDone: () => void;
  onClose: () => void;
}

/** The confirm sheet of Mark ready for review (one PR) or Mark all (the stack): names the PRs, posts on confirm and then shows each PR's outcome. */
export function MarkReadySheet({ session, lines, all, onBusy, onDone, onClose }: SheetProps) {
  const label = markLabel(all);
  const pending = pendingOf(session, lines);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');
  const [stale, setStale] = useState(false);
  const [results, setResults] = useState<ReadyForReviewResult[] | null>(null);
  const running = useRef(false);
  useCancelFocus(true);
  const run = async () => {
    if (running.current) return;
    running.current = true;
    setSending(true);
    onBusy(true);
    setError('');
    const r = await markReadyForReview(
      session,
      lines.map((l) => l.url),
    );
    running.current = false;
    setSending(false);
    onBusy(false);
    if (!r.ok) {
      if (r.status === 403) setStale(true);
      return setError(r.status === 403 ? STALE_MARK : r.error);
    }
    onDone();
    if (r.data.results.every((x) => x.outcome !== 'failed')) return onClose();
    setResults(r.data.results);
  };
  const outcome = (url: string) => results?.find((r) => r.url === url);
  return (
    <Sheet label={label} kind="launch-backdrop stop-backdrop" onClose={() => !sending && onClose()}>
      <p class="sheet-sub">
        {all
          ? `Take these ${lines.length} PRs out of draft on GitHub, bottom to top. A PR that is already ready for review is skipped, and nothing is merged.`
          : `Take #${prNumber(lines[0].url)} out of draft on GitHub. It asks for review and merges nothing.`}
      </p>
      <ul class="sheet-prs">
        {lines.map((l) => {
          const done = outcome(l.url);
          return (
            <li key={l.url}>
              <span class="mono">#{prNumber(l.url)}</span>{' '}
              {l.head && l.base ? (
                <span class="mono">
                  {l.head} → {l.base}
                </span>
              ) : null}{' '}
              <span class={done?.outcome === 'failed' ? 'ready-failed' : undefined}>{done ? outcomeText(done) : stateText(l.draft)}</span>
            </li>
          );
        })}
      </ul>
      <Failure message={error} />
      <Actions class="launch-foot">
        <Button label={results ? 'Close' : 'Cancel'} onClick={onClose} />
        {stale ? <Button label="Reload" onClick={() => location.reload()} /> : null}
        {results ? null : <Button label={label} variant="primary" pending={pending} onClick={run} />}
      </Actions>
    </Sheet>
  );
}
