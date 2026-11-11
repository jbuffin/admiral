import { useLayoutEffect, useRef, useState } from 'preact/hooks';
import { startSetup } from './api';
import { activeFleet, chooseFleet, fleetPaint, fleetSearch, fleets, loaded } from './fleets';
import type { Fleet } from './api';
import { Icon } from './Icon';
import { fleetPath, go } from './router';
import { Sheet, useListKeys } from './Sheet';

const LINE = 'The inbox and shared resources span fleets.';

function Dot({ fleet }: { fleet: Fleet }) {
  return <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />;
}

export function FleetSwitcher({ sheet }: { sheet: boolean }) {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const active = activeFleet.value;

  const close = (refocus = true) => {
    setOpen(false);
    if (refocus) trigger.current?.focus();
  };

  useListKeys(panel, open && !sheet, () => close());
  useLayoutEffect(() => {
    if (!open || sheet) return;
    // The desktop menu has no backdrop: a tap anywhere else closes it.
    const onDown = (e: Event) => {
      const t = e.target as Node;
      if (!panel.current?.contains(t) && !trigger.current?.contains(t)) close(false);
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [open, sheet]);

  const pick = (key: string) => {
    chooseFleet(key);
    close();
  };

  const [setup, setSetup] = useState<{ error?: string; started?: boolean } | null>(null);

  const openSettings = () => {
    if (!active) return;
    close(false);
    go(fleetPath(active.key), fleetSearch.value);
  };

  // The new shell has no terminal: setup runs detached and the old dashboard joins it.
  const newFleet = async () => {
    const r = await startSetup();
    if (!r.ok) return setSetup({ error: r.error });
    setSetup({ started: true });
    setTimeout(() => location.assign('/'), 2500);
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
      <button type="button" role="menuitem" class="menu-item" onClick={() => void newFleet()}>
        <Icon name="plus" size={14} />
        <span class="menu-name">New fleet…</span>
      </button>
      {setup?.error ? (
        <p class="menu-note error-text" role="alert">
          {setup.error}
        </p>
      ) : null}
      {setup?.started ? (
        <p class="menu-note" role="status">
          Setup is running. Opening the dashboard: tap the fleet count there to join the setup terminal.
        </p>
      ) : null}
      {active ? (
        <button type="button" role="menuitem" class="menu-item" onClick={openSettings}>
          <Icon name="preferences" size={14} />
          <span class="menu-name">{active.name} fleet settings</span>
        </button>
      ) : null}
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
        <Sheet label="Fleets" onClose={() => close()}>
          {items}
        </Sheet>
      ) : null}
    </div>
  );
}
