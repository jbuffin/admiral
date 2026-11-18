import { useEffect, useRef, useState } from 'preact/hooks';
import { Button } from './Button';
import { renderMarkdown } from './markdown';
import { Sheet } from './Sheet';

type State = { kind: 'loading' } | { kind: 'error' } | { kind: 'ready'; markdown: string };

const UNAVAILABLE = "Couldn't open these findings. Close this and try again.";

/** A ship voyage's kept findings, opened from its card. */
export function FindingsSheet({ ship, firstLaunched, onClose }: { ship: string; firstLaunched?: string; onClose: () => void }) {
  const [state, setState] = useState<State>({ kind: 'loading' });
  const close = useRef<HTMLDivElement>(null);
  useEffect(() => {
    close.current?.querySelector('button')?.focus();
  }, []);
  useEffect(() => {
    let live = true;
    const query = firstLaunched ? `?first_launched=${encodeURIComponent(firstLaunched)}` : '';
    fetch(`/api/v1/ships/${encodeURIComponent(ship)}/findings${query}`)
      .then(async (r) => {
        const body = r.ok ? ((await r.json()) as { findings?: string }) : null;
        return typeof body?.findings === 'string' ? ({ kind: 'ready', markdown: body.findings } as State) : ({ kind: 'error' } as State);
      })
      .catch((): State => ({ kind: 'error' }))
      .then((s) => live && setState(s));
    return () => {
      live = false;
    };
  }, [ship, firstLaunched]);
  return (
    <Sheet label="Findings" kind="order-sheet" onClose={onClose}>
      <div ref={close} class="actions">
        <Button label="Close" onClick={onClose} />
      </div>
      {state.kind === 'loading' ? <p class="sheet-sub">Loading findings…</p> : null}
      {state.kind === 'error' ? <p class="sheet-sub">{UNAVAILABLE}</p> : null}
      {state.kind === 'ready' ? <div class="md" dangerouslySetInnerHTML={{ __html: renderMarkdown(state.markdown) }} /> : null}
    </Sheet>
  );
}
