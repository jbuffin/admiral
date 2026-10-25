import { useLayoutEffect, useRef } from 'preact/hooks';
import { config } from './config';
import { harnesses } from './harnesses';
import { dockSheet, forgetOpener, openLaunch, returnFocus } from './launch';
import { LogoFlag } from './Logos';
import { marks, restingStatus, Sign } from './Marina';
import { phone } from './sizeClass';
import { isWebUrl } from './sources';

const FOCUSABLE = 'button:not([disabled]), a[href], input, textarea, [tabindex]:not([tabindex="-1"])';

// The phone's dock sheet (prototype :933-960): a tap on an order lists every dock to launch it on.
// It is not `selected`, so the marinas stay unarmed and show their resting status.
export function DockSheet() {
  const ref = useRef<HTMLDivElement>(null);
  const open = dockSheet.value !== null && phone.value;
  useLayoutEffect(() => {
    if (open) ref.current?.querySelector<HTMLElement>('button')?.focus({ preventScroll: true });
  }, [open]);
  // Leaving the phone layout (a resize, a split view) drops the sheet, so coming back doesn't reopen it.
  useLayoutEffect(() => {
    if (!phone.value && dockSheet.value) {
      dockSheet.value = null;
      forgetOpener();
    }
  }, [phone.value]);
  // Escape closes the sheet wherever focus is (the order's button, say, if a browser leaves it there).
  // It swallows every other Escape handler, so it relies on the sheet being the only thing open: it is
  // removed as soon as a dock is picked, before Launch orders (a native dialog) opens.
  useLayoutEffect(() => {
    if (!open) return;
    const esc = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.stopPropagation();
      dockSheet.value = null;
      returnFocus();
    };
    window.addEventListener('keydown', esc, true);
    return () => window.removeEventListener('keydown', esc, true);
  }, [open]);
  const picked = dockSheet.value;
  if (!picked || !phone.value) return null;
  const { order } = picked;
  const shown = isWebUrl(order.url) ? order.id : '';
  const what = shown ? `Launch ${shown}` : 'Launch order';
  const fleets = config.value?.fleets ?? [];

  const cancel = () => {
    dockSheet.value = null;
    returnFocus();
  };
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Tab') {
      const stops = [...ref.current!.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => el.offsetParent !== null);
      if (stops.length === 0) return;
      e.preventDefault();
      const i = stops.indexOf(document.activeElement as HTMLElement);
      stops[(i + (e.shiftKey ? -1 : 1) + stops.length) % stops.length].focus();
    }
  };

  return (
    <div class="dock-sheet-wrap">
      <div class="dock-sheet-backdrop" aria-hidden="true" onClick={cancel} />
      <div class="inbox-sheet dock-sheet" role="dialog" aria-modal="true" aria-labelledby="dock-sheet-title" tabIndex={-1} ref={ref} onKeyDown={onKeyDown}>
        <div class="dock-sheet-head">
          <div class="dock-sheet-what">
            <h2 id="dock-sheet-title">{what}</h2>
            <span>{order.title.split('\n')[0]}</span>
          </div>
          <button type="button" class="press dock-sheet-cancel" onClick={cancel}>
            Cancel
          </button>
        </div>
        <div class="dock-sheet-body">
          {fleets.length === 0 && (
            <Sign color="#FFF6E3" ink="#14304A">
              <span class="sign-name">Set up a fleet</span>
            </Sign>
          )}
          {fleets.map((f) => (
            <div key={f.key} role="group" aria-label={f.name} class="dock-sheet-fleet">
              <Sign color={f.color} ink={f.ink}>
                <span class="sign-name">{f.name}</span>
              </Sign>
              {f.docks.length === 0 && <p class="no-docks">No harnesses</p>}
              {f.docks.map((d) => {
                const h = harnesses[d.harness];
                const mark = marks(f, d);
                const label = `${what} on ${f.name}, ${h.label} dock`;
                return (
                  <button
                    key={d.harness}
                    type="button"
                    class="press dock-pick"
                    aria-label={mark ? `${label}. ${mark.full.join('. ')}` : label}
                    title={mark ? mark.full.join('\n') : undefined}
                    onClick={() => {
                      dockSheet.value = null;
                      openLaunch(picked, f.key, d.harness);
                    }}
                  >
                    <span class="dock-pick-stripe" style={`background-color:${f.color}`} aria-hidden="true" />
                    <svg width="40" height="28" viewBox="0 0 40 28" aria-hidden="true" class="dock-pick-flag">
                      <LogoFlag harness={d.harness} kind="pier" />
                    </svg>
                    <span class="dock-pick-text">
                      <span class="dock-pick-name">{h.label}</span>
                      <span class={mark ? 'dock-pick-status marked' : 'dock-pick-status'}>{restingStatus(f, d)}</span>
                    </span>
                  </button>
                );
              })}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
