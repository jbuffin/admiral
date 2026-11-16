import { useRef, useState } from 'preact/hooks';
import type { Result, StackShape } from './api';
import { Actions, Button } from './Button';
import type { PendingKey } from './busy';
import { Failure } from './PeekItem';
import { prNumber } from './prTab';
import { Sheet } from './Sheet';

const STALE_LINK = 'Admiral restarted, reload to stack';

export interface StackLine {
  url: string;
  head?: string;
  base?: string;
}

interface Props {
  /** The PRs the link will join, bottom to top, each with its branches when known. */
  lines: StackLine[];
  pending: PendingKey;
  link: () => Promise<Result<StackShape>>;
  onClose: () => void;
  onShape: (s: StackShape) => void;
  /** Asked after a failed link, which can come after GitHub took it: true means the PRs are stacked, so the sheet closes. */
  failed: () => Promise<boolean>;
}

/** The confirmation for Stack on GitHub: the PRs bottom to top, and one link call that keeps its button busy while it runs. */
export function StackSheet({ lines, pending, link, onClose, onShape, failed }: Props) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [stale, setStale] = useState(false);
  const running = useRef(false);
  const run = async () => {
    if (running.current) return;
    running.current = true;
    setBusy(true);
    setError('');
    const r = await link();
    if (r.ok) {
      onShape(r.data);
      return onClose();
    }
    if (r.status === 403) {
      running.current = false;
      setBusy(false);
      setStale(true);
      return setError(STALE_LINK);
    }
    if (await failed()) return onClose();
    running.current = false;
    setBusy(false);
    setError(r.error);
  };
  return (
    <Sheet label="Stack on GitHub" kind="launch-backdrop stop-backdrop" onClose={() => !busy && onClose()}>
      <p class="sheet-sub">Make these {lines.length} PRs one GitHub stack, bottom to top. Nothing is rebased or pushed; GitHub links the PRs so Merge can take them together.</p>
      <ul class="sheet-prs">
        {lines.map((l) => (
          <li key={l.url}>
            <span class="mono">#{prNumber(l.url)}</span>{' '}
            {l.head && l.base ? (
              <span class="mono">
                {l.head} → {l.base}
              </span>
            ) : null}
          </li>
        ))}
      </ul>
      <Failure message={error} />
      <Actions class="launch-foot">
        <Button label="Cancel" onClick={onClose} />
        {stale ? <Button label="Reload" onClick={() => location.reload()} /> : null}
        <Button label="Stack on GitHub" variant="primary" pending={pending} onClick={run} />
      </Actions>
    </Sheet>
  );
}
