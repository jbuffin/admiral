import { signal } from '@preact/signals';
import { useState } from 'preact/hooks';
import type { Source } from './api';
import { Button } from './Button';
import { renderRestrictedInline } from './markdown';
import { descriptionSource } from './prSections';
import { campaignList, followReference } from './campaignsLink';
import { mooredShipOn, reviewChip, reviews, shipsOnOrder } from './moored';
import { campaignAction, openedLine, topLine, whyItsHere } from './orderDetail';
import { launchRef, orderRef, stackLaunchRef, type OrderRow, type ViewGroup } from './ordersView';
import { Failure } from './PeekItem';
import { Checks, Reviewers } from './PRDetailSections';
import { usePRDetail } from './usePRDetail';
import { GROUP_TITLES } from './shipList';
import { ShipMerge } from './PeekMerge';
import { campaignPath, go, shipPath } from './router';
import { fleetSearch } from './fleets';
import { ships } from './shipsLink';
import { webUrl } from './ShipOrder';
import type { OrderRef } from './LaunchDialog';

// Outside the pane, since the pane remounts when its ship leaves the list while the source still lists the PR.
const merged = signal<ReadonlySet<string>>(new Set());

function ReviewStatus({ source, url }: { source: string; url: string }) {
  const read = usePRDetail(source, url);
  if (read.state === 'loading') return <p class="odetail-sub">Loading review status…</p>;
  if (read.state === 'failed') return <Failure message={read.error} />;
  const d = read.detail;
  const decision = reviewChip(d.review) ?? (d.reviews.length === 0 ? { text: 'Not reviewed yet', tone: 'muted' as const } : undefined);
  return (
    <div class="pr-detail odetail-pr" aria-label="Review status">
      {decision || d.draft || d.mergeable === 'CONFLICTING' ? (
        <div class="pr-state">
          {decision ? <span class={`pr-chip ${decision.tone === 'muted' ? '' : decision.tone}`}>{decision.text}</span> : null}
          {d.draft ? <span class="pr-chip">Draft</span> : null}
          {d.mergeable === 'CONFLICTING' ? <span class="pr-chip err">Merge conflicts</span> : null}
        </div>
      ) : null}
      <Reviewers detail={d} fold={false} />
      <Checks detail={d} />
    </div>
  );
}

interface Props {
  row: OrderRow;
  /** A GitHub stack row's group: its layers, shown in the pane in place of the order's own facts. */
  group?: ViewGroup;
  /** The fleet's login on the row's source. */
  viewer?: string;
  sources: Source[];
  now: number;
  /** The sheet already shows the title as its heading. */
  heading: boolean;
  onLaunch: (o: OrderRef) => void;
  onClose?: () => void;
}

export function OrderPane({ row, group, viewer, sources, now, heading, onLaunch, onClose }: Props) {
  const o = row.order;
  const [error, setError] = useState('');
  const [followError, setFollowError] = useState('');
  const stack = row.stackRow ? group : undefined;
  const why = stack ? '' : whyItsHere(sources, row.source, o, now);
  const opened = stack ? '' : openedLine(o, now);
  const action = campaignAction(o, campaignList.value);
  const onOrder = stack ? [...new Map([o, ...stack.children.map((c) => c.order)].flatMap((l) => shipsOnOrder(ships.value, l)).map((s) => [s.session, s])).values()] : shipsOnOrder(ships.value, o);
  const ship = stack ? undefined : mooredShipOn(ships.value, o);
  const prUrl = o.pr && o.url && !row.stackRow && !row.chainRow && sources.find((s) => s.key === row.source)?.plugin === 'github' ? o.url : null;
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
      {why || opened || (o.sub_issues ?? 0) > 0 ? (
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
      ) : null}
      {followError ? (
        <p class="alert" role="alert">
          {followError}
        </p>
      ) : null}
      {stack ? <h3 class="odetail-sub">Layers</h3> : null}
      {stack ? (
        <ol class="odetail-ships" aria-label="Layers">
          {stack.children.map((c) => (
            <li key={c.key}>
              <span>
                <span class="mono">{orderRef(c.order)}</span> {c.order.title}
              </span>
              {c.state ? <span class="orow-state">{c.state}</span> : null}
            </li>
          ))}
        </ol>
      ) : null}
      {o.excerpt ? <p class="odetail-excerpt md" dangerouslySetInnerHTML={{ __html: renderRestrictedInline(descriptionSource(o.excerpt)) }} /> : null}
      {stack && onOrder.length > 0 ? <h3 class="odetail-sub">Ships on this stack</h3> : null}
      {onOrder.length > 0 ? (
        <ul class="odetail-ships" aria-label={stack ? 'Ships on this stack' : 'Ships on this order'}>
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
      {prUrl ? <ReviewStatus source={row.source} url={prUrl} /> : null}
      <div class="odetail-actions">
        {isMerged ? <p class="line">Merged</p> : ship?.stack_ready && !reviews(ship) ? <ShipMerge ship={ship} onMerged={() => (merged.value = new Set(merged.value).add(row.key))} onError={setError} /> : null}
        <Failure message={error} />
        {row.launchable ? (
          <Button
            variant="primary"
            class="odetail-launch"
            label={onOrder.length > 0 ? 'Launch another ship' : stack ? 'Launch ship on stack' : 'Launch ship'}
            onClick={() => onLaunch(stack ? { ...stackLaunchRef(stack, ships.value, viewer), ...(onOrder.length > 0 && { anyway: true }) } : { ...launchRef(row), anyway: onOrder.length > 0 })}
          />
        ) : null}
      </div>
    </div>
  );
}
