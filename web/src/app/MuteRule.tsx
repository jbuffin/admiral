import { useState } from 'preact/hooks';
import { call } from './api';

export interface MuteTarget {
  fleet: string;
  routine: string;
  sender?: string;
  dedupe_key?: string;
  mute_by?: string[];
}

/** Mute rule: skip this routine's events from the sender, or with the key. The server adds the skip entry to the fleet's Limits. */
export function MuteRule({ target, disabled = false }: { target: MuteTarget; disabled?: boolean }) {
  const [open, setOpen] = useState(false);
  const [state, setState] = useState({ busy: false, error: '', done: '' });
  const by = (target.mute_by ?? []).filter((b) => (b === 'sender' ? target.sender : b === 'dedupe_key' ? target.dedupe_key : false));
  if (by.length === 0) return null;
  const mute = async (kind: 'sender' | 'dedupe_key') => {
    const value = kind === 'sender' ? target.sender! : target.dedupe_key!;
    setState({ busy: true, error: '', done: '' });
    const r = await call('POST', `/fleets/${encodeURIComponent(target.fleet)}/routines/${encodeURIComponent(target.routine)}/mute`, { by: kind, value });
    setState(r.ok ? { busy: false, error: '', done: kind === 'sender' ? 'Muted this sender' : 'Muted this key' } : { busy: false, error: r.error, done: '' });
    if (r.ok) setOpen(false);
  };
  return (
    <div class="mute-rule">
      <button type="button" class="btn" disabled={disabled || state.busy} aria-expanded={open} onClick={() => setOpen(!open)}>
        Mute rule
      </button>
      {open ? (
        <div class="peek-actions" role="group" aria-label="Mute rule">
          {by.includes('sender') ? (
            <button type="button" class="btn" disabled={state.busy} onClick={() => void mute('sender')}>
              Mute this sender ({target.sender})
            </button>
          ) : null}
          {by.includes('dedupe_key') ? (
            <button type="button" class="btn" disabled={state.busy} onClick={() => void mute('dedupe_key')}>
              Mute this key ({target.dedupe_key})
            </button>
          ) : null}
        </div>
      ) : null}
      {state.done ? <p class="line">{state.done}</p> : null}
      {state.error ? (
        <p class="inbox-error" role="alert">
          {state.error}
        </p>
      ) : null}
    </div>
  );
}
