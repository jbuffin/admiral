import { useState } from 'preact/hooks';
import { call, type Ship } from './api';
import { reReviewPlace, respondedPRs } from './moored';
import { Failure } from './PeekItem';
import { Sheet } from './Sheet';
import { prNumber } from './prTab';

const who = (author: string | undefined) => (author ? `@${author}` : 'The author');

/** What a Re-review shows and sends for a ship, from `reReviewPlace`; `prUrl` picks the PR the line names. */
export function reReviewOf(ship: Ship, prUrl?: string) {
  const responded = respondedPRs(ship);
  const place = reReviewPlace(ship);
  const stack = place?.place === 'stack';
  const pr = responded.find((p) => p.url === prUrl) ?? responded[0];
  const label = stack ? 'Re-review stack' : 'Re-review';
  const what = stack ? 'the whole stack' : `#${pr ? prNumber(pr.url) : ''}`;
  return { url: place?.url ?? '', label, line: `${who(pr?.author)} has responded since your review. Commodore reviews ${what} again.` };
}

/** One POST of Re-review; `onDone` runs on success, and an error stays for the caller to show. */
function useReReview(ship: Ship, url: string, onDone: () => void) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const send = async () => {
    setBusy(true);
    setError('');
    const r = await call('POST', `/ships/${encodeURIComponent(ship.session)}/rereview`, { url });
    // On success the buttons stay off until the ships push moves the ship out of Moored.
    if (r.ok) return onDone();
    setError(r.error);
    setBusy(false);
  };
  return { busy, error, send };
}

const Command = ({ name, url }: { name: string; url: string }) => (
  <p class="line">
    Wakes {name} under the same name with <code class="rr-cmd">/commodore {url}</code>
  </p>
);

/** The confirm sheet behind the PR tab's Re-review buttons. */
export function ReReviewSheet({ ship, prUrl, onClose }: { ship: Ship; prUrl?: string; onClose: () => void }) {
  const { url, label, line } = reReviewOf(ship, prUrl);
  const rr = useReReview(ship, url, onClose);
  return (
    <Sheet label={label} kind="launch-backdrop stop-backdrop" onClose={() => !rr.busy && onClose()}>
      <p class="line">{line}</p>
      <Command name={ship.name} url={url} />
      {rr.error ? (
        <p class="launch-error" role="alert">
          {rr.error}
        </p>
      ) : null}
      <div class="launch-foot">
        <button type="button" class="btn" disabled={rr.busy} onClick={onClose}>
          Cancel
        </button>
        <button type="button" class="btn primary" disabled={rr.busy} onClick={() => void rr.send()}>
          {label}
        </button>
      </div>
    </Sheet>
  );
}

/** The Inbox entry's Peek body: Peek is the confirmation, so one tap wakes the ship. */
export function ReReviewBody({ ship, fold }: { ship: Ship; fold: (text: string) => void }) {
  const { url, label, line } = reReviewOf(ship);
  const rr = useReReview(ship, url, () => fold('Re-reviewing'));
  return (
    <div class="peek-form">
      <p class="line">{line}</p>
      <Command name={ship.name} url={url} />
      <div class="peek-merge">
        <button type="button" class="btn primary" disabled={rr.busy} onClick={() => void rr.send()}>
          {label}
        </button>
      </div>
      <Failure message={rr.error} />
    </div>
  );
}
