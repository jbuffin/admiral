import { useState } from 'preact/hooks';
import { OrderItem } from './OrderItem';
import { SignInCard } from './SignInCard';
import { SourceIcon } from './SourceIcon';
import { type PanelView, api, can, declares, filter, panelCollapsed, setPanelCollapsed, sourcePath } from './sources';

export const countLabel = (n: number) => `${n} ${n === 1 ? 'order' : 'orders'}`;

// A source's error with its Restart button; shared by panels and source rows.
export function SourceProblem({ sourceKey, message, extra }: { sourceKey: string; message: string | null; extra?: string }) {
  const [refusal, setRefusal] = useState<string | null>(null);
  const shown = refusal ?? extra ?? message;
  return (
    <div class="problem">
      {shown && <p role="alert">{shown}</p>}
      {message && (
        <button
          type="button"
          class="small-button press"
          onClick={async () => {
            const r = await api('POST', sourcePath(sourceKey, '/restart'));
            setRefusal(r.ok ? null : r.error);
          }}
        >
          Restart
        </button>
      )}
    </div>
  );
}

const Chevron = ({ open }: { open: boolean }) => (
  <svg
    class="chevron"
    style={{ transform: open ? 'none' : 'rotate(-90deg)' }}
    width="18"
    height="18"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2.8"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path d="M6 9l6 6 6-6" />
  </svg>
);

export function PanelSection({ view }: { view: PanelView }) {
  const { source, panel, orders } = view;
  const open = !panelCollapsed(view.key);
  const [refusal, setRefusal] = useState<string | null>(null);
  const total = orders.length;
  return (
    <section class="panel" id={`panel-${view.key}`} aria-label={view.title}>
      <div class="panel-head">
        <button type="button" class="panel-toggle" aria-expanded={open} onClick={() => setPanelCollapsed(view.key, open)}>
          <Chevron open={open} />
          <SourceIcon source={source} size={20} />
          <span class="panel-title">{view.title}</span>
        </button>
        <span class="count" role="img" aria-label={countLabel(total)}>
          {total}
        </span>
        {declares(source, 'refresh') && (
          <button
            type="button"
            class="icon-button press"
            aria-label="Refresh"
            disabled={!can(source, 'refresh')}
            onClick={async () => {
              const r = await api('POST', sourcePath(source.key, '/refresh'), { panel: panel.id });
              setRefusal(r.ok ? null : r.error);
            }}
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M20 11a8 8 0 1 0-2.3 5.7M20 4v7h-7" />
            </svg>
          </button>
        )}
      </div>
      {open && (
        <div class="panel-body">
          {source.state === 'auth_required' && source.auth && <SignInCard auth={source.auth} />}
          {(source.state === 'error' || refusal) && (
            <SourceProblem sourceKey={source.key} message={source.state === 'error' ? source.error : null} extra={refusal ?? undefined} />
          )}
          {declares(source, 'add') && <AddOrder view={view} onError={setRefusal} />}
          <ul class="orders-list">
            {orders.map((o) => (
              <OrderItem key={o.id} source={source} panelId={panel.id} order={o} fail={setRefusal} />
            ))}
          </ul>
          {total === 0 && (
            <p class="empty">{filter.value.trim() ? 'No orders match that filter.' : 'All hands deployed. Queue is clear.'}</p>
          )}
        </div>
      )}
    </section>
  );
}

function AddOrder({ view, onError }: { view: PanelView; onError: (m: string | null) => void }) {
  const [text, setText] = useState('');
  const [adding, setAdding] = useState(false);
  const { source, panel } = view;
  const submit = async () => {
    if (adding) return;
    setAdding(true);
    const r = await api('POST', sourcePath(source.key, '/orders'), { panel: panel.id, text });
    setAdding(false);
    if (r.ok) {
      setText('');
      onError(null);
    } else onError(r.error);
  };
  return (
    <div class="add-order">
      <textarea
        aria-label="Add order"
        rows={2}
        placeholder="Add order"
        value={text}
        disabled={!can(source, 'add')}
        onInput={(e) => setText((e.target as HTMLTextAreaElement).value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
            e.preventDefault();
            submit();
          }
        }}
      />
      <button type="button" class="small-button press" disabled={!can(source, 'add')} onClick={submit}>
        Add
      </button>
    </div>
  );
}
