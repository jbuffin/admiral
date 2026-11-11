import type { ComponentChildren } from 'preact';
import { useEffect, useRef } from 'preact/hooks';

// onCancel runs first on Escape; it can keep the dialog open with preventDefault.
export function Modal({
  label,
  onClose,
  onCancel,
  variant,
  role,
  children,
}: {
  label: string;
  onClose: () => void;
  onCancel?: (e: Event) => void;
  variant?: string;
  /** `alertdialog` for a question that asks before a destructive step. */
  role?: 'alertdialog';
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
      role={role}
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
