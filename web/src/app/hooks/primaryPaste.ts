import type { RefObject } from 'preact';
import { touchKeyboard } from '../../keys';
import { useSubscribe } from './subscribe';

export const editable = (el: Element | null) => el instanceof HTMLElement && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName));

/** A page's primary text box takes text pasted while nothing editable has focus and no dialog is open. `onText` receives the box's new value. */
export function usePrimaryPaste(field: RefObject<HTMLTextAreaElement | null>, primary: boolean, onText: (value: string) => void): void {
  useSubscribe(primary ? document : null, 'paste', (e) => {
    // Another primary box already took it: a paste goes to one box.
    if (e.defaultPrevented) return;
    const pasted = (e as ClipboardEvent).clipboardData?.getData('text/plain');
    const el = field.current;
    if (!pasted || !el || el.disabled || el.readOnly) return;
    if (editable(document.activeElement) || document.querySelector('[aria-modal="true"]')) return;
    e.preventDefault();
    el.setRangeText(pasted, el.selectionStart, el.selectionEnd, 'end');
    onText(el.value);
    if (!touchKeyboard()) el.focus();
  });
}
