import type { ComponentChildren, RefObject } from 'preact';
import { useLayoutEffect, useRef } from 'preact/hooks';

const ITEMS = '[role="menuitemradio"], [role="menuitem"], a';

export function useListKeys(ref: RefObject<HTMLElement | null>, active: boolean, onEscape: () => void) {
  useLayoutEffect(() => {
    if (!active) return;
    const items = () => [...(ref.current?.querySelectorAll<HTMLElement>(ITEMS) ?? [])];
    (items().find((i) => i.getAttribute('aria-checked') === 'true') ?? items()[0])?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') return onEscape();
      if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
      e.preventDefault();
      const list = items();
      const i = list.indexOf(document.activeElement as HTMLElement);
      list[(i + (e.key === 'ArrowDown' ? 1 : list.length - 1)) % list.length]?.focus();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [active]);
}

/** Only a click on the backdrop itself closes it: closing on pointerdown would let the click land underneath. */
export function Sheet({ label, onClose, wide, children }: { label: string; onClose: () => void; wide?: boolean; children: ComponentChildren }) {
  const ref = useRef<HTMLDivElement>(null);
  useListKeys(ref, true, onClose);
  return (
    <div class="backdrop" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div ref={ref} class={wide ? 'sheet wide' : 'sheet'} role="dialog" aria-modal="true" aria-label={label}>
        <h2 class="sheet-title">{label}</h2>
        {children}
      </div>
    </div>
  );
}
