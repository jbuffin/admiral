import { signal } from '@preact/signals';
import { useState } from 'preact/hooks';
import type { Source } from './api';
import { campaignList, followReference } from './campaignsLink';
import { carryingShip, reviews } from './moored';
import { campaignAction, openedLine, topLine, whyItsHere } from './orderDetail';
import { launchRef, type OrderRow } from './ordersView';
import { Failure } from './PeekItem';
import { ShipMerge } from './PeekMerge';
import { campaignPath, go } from './router';
import { ships } from './shipsLink';
import { webUrl } from './ShipOrder';
import type { OrderRef } from './LaunchDialog';

// Outside the pane, since the pane remounts when its ship leaves the list while the source still lists the PR.
const merged = signal<ReadonlySet<string>>(new Set());

interface Props {
  row: OrderRow;
  sources: Source[];
  now: number;
  /** The sheet already shows the title as its heading. */
  heading: boolean;
  onLaunch: (o: OrderRef) => void;
  onClose?: () => void;
}

export function OrderPane({ row, sources, now, heading, onLaunch, onClose }: Props) {
  const o = row.order;
  const [error, setError] = useState('');
  const [followError, setFollowError] = useState('');
  const why = whyItsHere(sources, row.source, o, now);
  const opened = openedLine(o, now);
  const action = campaignAction(o, campaignList.value);
  const ship = o.pr && o.url ? carryingShip(ships.value, o.url) : undefined;
  const isMerged = merged.value.has(row.key);
  return (
    <div class="odetail">
      <div class="odetail-top">
        <span class="orow-meta">
          <span class="mono">{topLine(sources, row.source, o)}</span>
        </span>
        <span class="orders-spacer" />
        {webUrl(o.url) ? (
          <a class="odetail-act" href={o.url} target="_blank" rel="noopener noreferrer">
            Open on GitHub
          </a>
        ) : null}
        {onClose ? (
          <button type="button" class="orow-go odetail-close" aria-label="Close" onClick={onClose}>
            ×
          </button>
        ) : null}
      </div>
      {heading ? <h2 class="odetail-title">{o.title}</h2> : null}
      {(o.labels ?? []).length > 0 ? (
        <div class="odetail-labels">
          {o.labels!.map((l) => (
            <span key={l} class="orow-label">
              {l}
            </span>
          ))}
        </div>
      ) : null}
      <dl class="odetail-facts">
        {why ? (
          <>
            <dt>Why it's here</dt>
            <dd>{why}</dd>
          </>
        ) : null}
        {opened ? (
          <>
            <dt>Opened</dt>
            <dd>{opened}</dd>
          </>
        ) : null}
        {(o.sub_issues ?? 0) > 0 ? (
          <>
            <dt>Sub-issues</dt>
            <dd>
              {o.sub_issues}
              {action?.kind === 'follow' ? (
                <>
                  {' · '}
                  <button type="button" class="odetail-act" onClick={async () => setFollowError((await followReference(action.url)) ?? '')}>
                    Follow as campaign
                  </button>
                </>
              ) : null}
              {action?.kind === 'open' ? (
                <>
                  {' · '}
                  <a
                    class="odetail-act"
                    href={campaignPath(action.id) + `?fleet=${encodeURIComponent(action.fleet)}`}
                    onClick={(e) => {
                      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                      e.preventDefault();
                      go(campaignPath(action.id), `?fleet=${encodeURIComponent(action.fleet)}`);
                    }}
                  >
                    Open campaign
                  </a>
                </>
              ) : null}
            </dd>
          </>
        ) : null}
      </dl>
      {followError ? (
        <p class="alert" role="alert">
          {followError}
        </p>
      ) : null}
      {o.excerpt ? <p class="odetail-excerpt">{o.excerpt}</p> : null}
      <div class="odetail-actions">
        {isMerged ? <p class="line">Merged</p> : ship?.stack_ready && !reviews(ship) ? <ShipMerge ship={ship} onMerged={() => (merged.value = new Set(merged.value).add(row.key))} onError={setError} /> : null}
        <Failure message={error} />
        <button type="button" class="btn primary odetail-launch" onClick={() => onLaunch(launchRef(row))}>
          Launch ship
        </button>
      </div>
    </div>
  );
}
