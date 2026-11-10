import { useLayoutEffect, useRef, useState } from 'preact/hooks';
import { activeFleet, chooseFleet, fleetPaint, fleets, loaded } from './fleets';
import type { Fleet } from './api';
import { Icon } from './Icon';

const LINE = 'The inbox and shared resources span fleets.';

function Dot({ fleet }: { fleet: Fleet }) {
  return <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />;
}

/**
 * The fleet switcher: a menu under the trigger in the sidebar, a bottom sheet on the phone.
 * Both list the fleets as menuitemradio items.
 */
export function FleetSwitcher({ sheet }: { sheet: boolean }) {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const active = activeFleet.value;

  const close = (refocus = true) => {
    setOpen(false);
    if (refocus) trigger.current?.focus();
  };

  useLayoutEffect(() => {
    if (!open) return;
    const items = () => [...(panel.current?.querySelectorAll<HTMLElement>('[role="menuitemradio"]') ?? [])];
    (items().find((i) => i.getAttribute('aria-checked') === 'true') ?? items()[0])?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') return close();
      if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
      e.preventDefault();
      const list = items();
      const i = list.indexOf(document.activeElement as HTMLElement);
      list[(i + (e.key === 'ArrowDown' ? 1 : list.length - 1)) % list.length]?.focus();
    };
    const onDown = (e: Event) => {
      const t = e.target as Node;
      if (!panel.current?.contains(t) && !trigger.current?.contains(t)) close(false);
    };
    document.addEventListener('keydown', onKey);
    document.addEventListener('pointerdown', onDown);
    return () => {
      document.removeEventListener('keydown', onKey);
      document.removeEventListener('pointerdown', onDown);
    };
  }, [open]);

  const pick = (key: string) => {
    chooseFleet(key);
    close();
  };

  const items = (
    <>
      {fleets.value.map((f) => (
        <button
          key={f.key}
          type="button"
          role="menuitemradio"
          aria-checked={f.key === active?.key}
          class="menu-item"
          onClick={() => pick(f.key)}
        >
          <Dot fleet={f} />
          <span class="menu-name">{f.name}</span>
        </button>
      ))}
      <p class="menu-note">{LINE}</p>
    </>
  );

  return (
    <div class={sheet ? 'switcher switcher-sheet' : 'switcher'}>
      <button
        ref={trigger}
        type="button"
        class="switcher-trigger"
        aria-haspopup={sheet ? 'dialog' : 'menu'}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        {active ? <Dot fleet={active} /> : null}
        <span class="switcher-name">{active ? active.name : loaded.value ? 'No fleets' : ' '}</span>
        <Icon name="chevron" />
      </button>
      {open && !sheet ? (
        <div ref={panel} class="menu" role="menu" aria-label="Switch fleet">
          {items}
        </div>
      ) : null}
      {open && sheet ? (
        <div class="backdrop">
          <div ref={panel} class="sheet" role="dialog" aria-modal="true" aria-label="Fleets">
            <h2 class="sheet-title">Fleets</h2>
            {items}
          </div>
        </div>
      ) : null}
    </div>
  );
}
