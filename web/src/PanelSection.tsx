import { useEffect, useState } from 'preact/hooks';
import { touchKeyboard } from './keys';
import { OrderItem } from './OrderItem';
import { gripKey, pressGrip, reordering, savePanelOrder } from './reorder';
import { SignInCard } from './SignInCard';
import { phone } from './sizeClass';
import { SourceIcon } from './SourceIcon';
import { type PanelView, api, can, declares, filter, panelCollapsed, setPanelCollapsed, sourcePath } from './sources';

export const countLabel = (n: number) => `${n} ${n === 1 ? 'order' : 'orders'}`;

// A source's error with its Restart button; shared by panels and source rows.
export function SourceProblem({ sourceKey, message, extra }: { sourceKey: string; message: string | null; extra?: string }) {
  const [refusal, setRefusal] = useState<string | null>(null);
  useEffect(() => setRefusal(null), [message]); // a refused Restart's message is about the old error
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

// Shows a panel's autopilot is on, or paused. Not drawn for a panel that has none.
export function AutopilotBadge({ paused }: { paused: boolean }) {
  return (
    <span class={paused ? 'autopilot-badge paused' : 'autopilot-badge'} role="img" aria-label={paused ? 'Autopilot paused' : 'Autopilot on'}>
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        {paused ? <path d="M9 5v14M15 5v14" /> : <path d="M12 3l9 18-9-4-9 4z" />}
      </svg>
    </span>
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

// The handle that moves a source or a panel within its list, by drag or by the
// arrow keys.
export function ReorderGrip({ label, group, itemKey, keys, commit }: { label: string; group: string; itemKey: string; keys: string[]; commit: (keys: string[]) => void }) {
  if (phone.value) return null; // a phone's Orders tab has no dragging
  return (
    <button
      type="button"
      class="reorder-grip"
      aria-label={label}
      onPointerDown={(e) => pressGrip(e, group, itemKey, keys, commit)}
      onKeyDown={(e) => gripKey(e, itemKey, keys, commit)}
    >
      <svg width="12" height="16" viewBox="0 0 12 16" fill="currentColor" aria-hidden="true">
        <circle cx="3" cy="3" r="1.6" />
        <circle cx="9" cy="3" r="1.6" />
        <circle cx="3" cy="8" r="1.6" />
        <circle cx="9" cy="8" r="1.6" />
        <circle cx="3" cy="13" r="1.6" />
        <circle cx="9" cy="13" r="1.6" />
      </svg>
    </button>
  );
}

export function PanelSection({ view }: { view: PanelView }) {
  const { source, panel, orders } = view;
  const group = `panels:${source.key}`;
  const panelIds = source.panels.map((p) => p.id);
  const lifted = reordering.value?.group === group && reordering.value.key === panel.id;
  const open = !panelCollapsed(view.key);
  const [refusal, setRefusal] = useState<string | null>(null);
  const total = orders.length;
  const autopilot = panel.autopilot;
  return (
    <section class={lifted ? 'panel lifted' : 'panel'} id={`panel-${view.key}`} aria-label={view.title} data-reorder={group} data-key={panel.id}>
      <div class="panel-head">
        {panelIds.length > 1 && (
          <ReorderGrip label={`Move ${view.title}`} group={group} itemKey={panel.id} keys={panelIds} commit={(ids) => savePanelOrder(source.key, ids, setRefusal)} />
        )}
        <button type="button" class="panel-toggle" aria-expanded={open} onClick={() => setPanelCollapsed(view.key, open)}>
          <Chevron open={open} />
          <SourceIcon source={source} size={20} />
          <span class="panel-title">{view.title}</span>
        </button>
        <span class="count" role="img" aria-label={countLabel(total)}>
          {total}
        </span>
        {autopilot?.enabled && (
          <>
            <AutopilotBadge paused={autopilot.paused} />
            <button
              type="button"
              class="icon-button press"
              aria-label={autopilot.paused ? 'Resume autopilot' : 'Pause autopilot'}
              onClick={async () => {
                const r = await api('PUT', sourcePath(source.key, `/panels/${encodeURIComponent(panel.id)}/autopilot`), { paused: !autopilot.paused });
                setRefusal(r.ok ? null : r.error);
              }}
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="2" stroke-linejoin="round" aria-hidden="true">
                {autopilot.paused ? <path d="M7 4l13 8-13 8z" /> : <path d="M7 4h3v16H7zM14 4h3v16h-3z" />}
              </svg>
            </button>
          </>
        )}
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
      {autopilot && autopilot.waiting_for.length > 0 && <p class="panel-waiting">waiting for {autopilot.waiting_for.join(', ')}</p>}
      {open && (
        <div class="panel-body">
          {source.state === 'auth_required' && source.auth && <SignInCard auth={source.auth} />}
          {(source.state === 'error' || refusal || panel.error) && (
            <SourceProblem
              sourceKey={source.key}
              message={source.state === 'error' ? source.error : null}
              extra={refusal ?? (source.state === 'error' ? undefined : panel.error ?? undefined)}
            />
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
          // Return adds the order; Shift+Return, or Return on a touch keyboard (which has no Shift+Return), starts a new line.
          if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && !touchKeyboard()) {
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
