import { signal } from '@preact/signals';
import { useState } from 'preact/hooks';
import { Menu, MenuItem } from './Menu';
import { SourceIcon } from './SourceIcon';
import { type Order, type Source, api, can, declares, sourcePath } from './sources';

const editing = signal<string | null>(null);

const isWebUrl = (u?: string) => !!u && /^https?:\/\//i.test(u);

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

export function OrderItem({ source, panelId, order, fail }: { source: Source; panelId: string; order: Order; fail: (m: string) => void }) {
  const id = `${source.key}/${panelId}/${order.id}`;
  const title = order.title.split('\n')[0];
  const hasEdit = declares(source, 'edit');
  const hasRemove = declares(source, 'remove');
  const path = sourcePath(source.key, `/orders/${encodeURIComponent(order.id)}`);

  if (editing.value === id) return <EditOrder path={path} order={order} fail={fail} onDone={() => (editing.value = null)} />;

  return (
    <li class={hasEdit || hasRemove ? 'order with-menu' : 'order'}>
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
      </div>
      {(hasEdit || hasRemove) && (
        <Menu id={id} label={`Actions for ${title}`} disabled={source.read_only}>
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
      <Grip />
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
