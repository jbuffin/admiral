import type { ComponentChildren } from 'preact';
import { useEffect, useRef } from 'preact/hooks';

// A native modal dialog: Escape closes it and the page behind is inert.
// onCancel runs first on Escape; it can keep the dialog open with preventDefault.
export function Modal({
  label,
  onClose,
  onCancel,
  variant,
  children,
}: {
  label: string;
  onClose: () => void;
  onCancel?: (e: Event) => void;
  variant?: string;
  children: ComponentChildren;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (d && !d.open) d.showModal();
  }, []);
  return (
    <dialog
      ref={ref}
      class={variant ? `modal ${variant}` : 'modal'}
      aria-label={label}
      onCancel={onCancel}
      onClose={onClose}
      onClick={(e) => {
        if (e.target === ref.current) onClose();
      }}
    >
      <div class="modal-body">{children}</div>
    </dialog>
  );
}
