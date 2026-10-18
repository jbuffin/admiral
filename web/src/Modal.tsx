import type { ComponentChildren } from 'preact';
import { useEffect, useRef } from 'preact/hooks';

// A native modal dialog: Escape closes it and the page behind is inert.
export function Modal({ label, onClose, children }: { label: string; onClose: () => void; children: ComponentChildren }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (d && !d.open) d.showModal();
  }, []);
  return (
    <dialog
      ref={ref}
      class="modal"
      aria-label={label}
      onClose={onClose}
      onClick={(e) => {
        if (e.target === ref.current) onClose();
      }}
    >
      <div class="modal-body">{children}</div>
    </dialog>
  );
}
