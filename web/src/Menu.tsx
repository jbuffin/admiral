import { signal } from '@preact/signals';
import type { ComponentChildren } from 'preact';
import { createPortal } from 'preact/compat';
import { useLayoutEffect, useRef } from 'preact/hooks';

// Only one ⋯ menu is open at a time.
const open = signal<string | null>(null);

if (typeof document !== 'undefined') {
  document.addEventListener('click', () => (open.value = null));
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') open.value = null;
  });
  // The list is placed once from its button, so it closes rather than drift when anything scrolls.
  document.addEventListener('scroll', () => (open.value = null), true);
  window.addEventListener('resize', () => (open.value = null));
}

// Room between the list and its button, and between the list and the screen's edge.
const GAP = 4;
const EDGE = 8;

export const closeMenu = () => (open.value = null);

export function Menu({ id, label, disabled, children }: { id: string; label: string; disabled?: boolean; children: ComponentChildren }) {
  const isOpen = open.value === id;
  const button = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);
  // Below the button and right-aligned to it, or above it when there's no room below.
  useLayoutEffect(() => {
    const b = button.current?.getBoundingClientRect();
    const l = list.current;
    if (!isOpen || !b || !l) return;
    const { offsetWidth: w, offsetHeight: h } = l;
    const top = b.bottom + GAP + h <= innerHeight - EDGE || b.top - GAP - h < EDGE ? b.bottom + GAP : b.top - GAP - h;
    l.style.top = `${Math.max(EDGE, Math.min(top, innerHeight - EDGE - h))}px`;
    l.style.left = `${Math.max(EDGE, Math.min(b.right - w, innerWidth - EDGE - w))}px`;
  }, [isOpen]);
  return (
    <div class="menu">
      <button
        ref={button}
        type="button"
        class="icon-button press"
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={isOpen}
        disabled={disabled}
        onClick={(e) => {
          e.stopPropagation();
          open.value = isOpen ? null : id;
        }}
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <circle cx="5" cy="12" r="2" />
          <circle cx="12" cy="12" r="2" />
          <circle cx="19" cy="12" r="2" />
        </svg>
      </button>
      {isOpen &&
        createPortal(
          <div class="menu-list" role="menu" ref={list}>
            {children}
          </div>,
          document.body,
        )}
    </div>
  );
}

export function MenuItem({ onSelect, disabled, children }: { onSelect: () => void; disabled?: boolean; children: ComponentChildren }) {
  return (
    <button type="button" role="menuitem" class="menu-item" disabled={disabled} onClick={() => (closeMenu(), onSelect())}>
      {children}
    </button>
  );
}
