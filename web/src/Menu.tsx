import { signal } from '@preact/signals';
import type { ComponentChildren } from 'preact';

// Only one ⋯ menu is open at a time.
const open = signal<string | null>(null);

if (typeof document !== 'undefined') {
  document.addEventListener('click', () => (open.value = null));
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') open.value = null;
  });
}

export const closeMenu = () => (open.value = null);

export function Menu({ id, label, disabled, children }: { id: string; label: string; disabled?: boolean; children: ComponentChildren }) {
  const isOpen = open.value === id;
  return (
    <div class="menu">
      <button
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
      {isOpen && (
        <div class="menu-list" role="menu">
          {children}
        </div>
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
