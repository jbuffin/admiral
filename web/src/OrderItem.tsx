import { signal } from '@preact/signals';
import { useState } from 'preact/hooks';
import { Menu, MenuItem } from './Menu';
import { SourceIcon } from './SourceIcon';
import { isSelected, openDockSheet, pressOrder } from './launch';
import { phone } from './sizeClass';
import { type Order, type Source, api, can, declares, isWebUrl, sourcePath } from './sources';

const editing = signal<string | null>(null);

const Grip = () => (
  <svg class="grip" width="12" height="20" viewBox="0 0 12 20" fill="#9AAFC0" aria-hidden="true">
    <circle cx="3" cy="4" r="1.7" />
    <circle cx="9" cy="4" r="1.7" />
    <circle cx="3" cy="10" r="1.7" />
    <circle cx="9" cy="10" r="1.7" />
    <circle cx="3" cy="16" r="1.7" />
    <circle cx="9" cy="16" r="1.7" />
  </svg>
);

const Chevron = () => (
  <svg class="order-go" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#9AAFC0" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M8 5l8 7-8 7" />
  </svg>
);

const SHOWN_LABELS = 3;

function Labels({ labels }: { labels: string[] }) {
  const hidden = labels.slice(SHOWN_LABELS);
  return (
    <ul class="order-labels" aria-label="Labels">
      {labels.slice(0, SHOWN_LABELS).map((l) => (
        <li class="order-label" key={l} title={l}>
          {l}
        </li>
      ))}
      {hidden.length > 0 && (
        <li class="order-label" title={hidden.join(', ')} aria-label={`+${hidden.length}: ${hidden.join(', ')}`}>
          +{hidden.length}
        </li>
      )}
    </ul>
  );
}

export function OrderItem({ source, panelId, order, fail }: { source: Source; panelId: string; order: Order; fail: (m: string) => void }) {
  const id = `${source.key}/${panelId}/${order.id}`;
  const title = order.title.split('\n')[0];
  const labels = order.labels ?? [];
  const hasEdit = declares(source, 'edit');
  const hasRemove = declares(source, 'remove');
  const path = sourcePath(source.key, `/orders/${encodeURIComponent(order.id)}`);

  if (editing.value === id) return <EditOrder path={path} order={order} fail={fail} onDone={() => (editing.value = null)} />;

  return (
    <li
      class={`${hasEdit || hasRemove ? 'order with-menu' : 'order'}${isSelected({ source: source.key, panel: panelId, order }) ? ' selected' : ''}`}
      onPointerDown={phone.value ? undefined : (e) => pressOrder(e, { source: source.key, panel: panelId, order })}
    >
      {phone.value && (
        <button
          type="button"
          class="order-hit"
          aria-label={`${source.name}${isWebUrl(order.url) ? ` ${order.id}` : ''}, ${title}. Tap to pick a dock and launch it.${labels.length ? ` Labels: ${labels.join(', ')}.` : ''}`}
          onClick={(e) => openDockSheet({ source: source.key, panel: panelId, order }, e.currentTarget)}
        />
      )}
      <div class="order-main">
        <div class="order-meta">
          <SourceIcon source={source} size={16} />
          {isWebUrl(order.url) && (
            <a class="order-id" href={order.url} target="_blank" rel="noopener noreferrer">
              {order.id}
            </a>
          )}
          {order.hot && <span class="high">High</span>}
        </div>
        <span class="order-title">{title}</span>
        {labels.length > 0 && <Labels labels={labels} />}
      </div>
      {(hasEdit || hasRemove) && (
        <Menu id={id} label={`Actions for ${title}`} disabled={!can(source, 'edit') && !can(source, 'remove')}>
          {hasEdit && (
            <MenuItem disabled={!can(source, 'edit')} onSelect={() => (editing.value = id)}>
              Edit
            </MenuItem>
          )}
          {hasRemove && (
            <MenuItem
              disabled={!can(source, 'remove')}
              onSelect={async () => {
                const r = await api('DELETE', path);
                if (!r.ok) fail(r.error);
              }}
            >
              Remove
            </MenuItem>
          )}
        </Menu>
      )}
      {phone.value ? <Chevron /> : <Grip />}
    </li>
  );
}

function EditOrder({ path, order, fail, onDone }: { path: string; order: Order; fail: (m: string) => void; onDone: () => void }) {
  const [text, setText] = useState(order.text ?? order.title);
  const save = async () => {
    const r = await api('PUT', path, { text });
    if (r.ok) onDone();
    else fail(r.error);
  };
  return (
    <li class="order editing">
      <textarea aria-label="Edit order" rows={4} value={text} onInput={(e) => setText((e.target as HTMLTextAreaElement).value)} />
      <div class="row">
        <button type="button" class="small-button press" onClick={save}>
          Save
        </button>
        <button type="button" class="small-button press" onClick={onDone}>
          Cancel
        </button>
      </div>
    </li>
  );
}
