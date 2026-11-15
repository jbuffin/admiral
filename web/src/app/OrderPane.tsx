import { signal } from '@preact/signals';
import { useState } from 'preact/hooks';
import type { Source } from './api';
import { Button } from './Button';
import { campaignList, followReference } from './campaignsLink';
import { mooredShipOn, reviews, shipsOnOrder } from './moored';
import { campaignAction, openedLine, topLine, whyItsHere } from './orderDetail';
import { launchRef, type OrderRow } from './ordersView';
import { Failure } from './PeekItem';
import { GROUP_TITLES } from './shipList';
import { ShipMerge } from './PeekMerge';
import { campaignPath, go, shipPath } from './router';
import { fleetSearch } from './fleets';
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
  const onOrder = shipsOnOrder(ships.value, o);
  const ship = mooredShipOn(ships.value, o);
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
                  <Button
                    variant="link"
                    class="odetail-act"
                    label="Follow as campaign"
                    pending={['order', row.source, row.order.id, 'follow']}
                    onClick={async () => setFollowError((await followReference(action.url)) ?? '')}
                  />
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
      {onOrder.length > 0 ? (
        <ul class="odetail-ships" aria-label="Ships on this order">
          {onOrder.map((s) => (
            <li key={s.session}>
              <a
                class="odetail-ship"
                href={`${shipPath(s.session)}${fleetSearch.value}`}
                onClick={(e) => {
                  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                  e.preventDefault();
                  go(shipPath(s.session), fleetSearch.value);
                }}
              >
                {s.name}
              </a>
              <span class="orow-state">{GROUP_TITLES[s.status as keyof typeof GROUP_TITLES]}</span>
            </li>
          ))}
        </ul>
      ) : null}
      <div class="odetail-actions">
        {isMerged ? <p class="line">Merged</p> : ship?.stack_ready && !reviews(ship) ? <ShipMerge ship={ship} onMerged={() => (merged.value = new Set(merged.value).add(row.key))} onError={setError} /> : null}
        <Failure message={error} />
        <Button variant="primary" class="odetail-launch" label={onOrder.length > 0 ? 'Launch another ship' : 'Launch ship'} onClick={() => onLaunch({ ...launchRef(row), anyway: onOrder.length > 0 })} />
      </div>
    </div>
  );
}
