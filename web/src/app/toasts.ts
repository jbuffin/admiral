// The toast queue: pure, time comes in as `now` and nothing here sets a timer or touches the page.

export const TOAST_MS = 6000;
const MAX_TOASTS = 2;

export interface ToastAlert {
  name: string;
  line: string;
  tag: string;
}

export interface Toast<A extends ToastAlert = ToastAlert> {
  id: number;
  tag: string;
  name: string;
  line: string;
  alert: A;
  at: number;
}

export function createToasts<A extends ToastAlert = ToastAlert>({ now }: { now: () => number }) {
  let list: Toast<A>[] = [];
  let next = 1;
  return {
    /** A toast with the same tag is replaced, and a third toast replaces the oldest. */
    push(alert: A): Toast<A> {
      const toast: Toast<A> = { id: next++, tag: alert.tag, name: alert.name, line: alert.line, alert, at: now() };
      list = [...list.filter((t) => t.tag !== alert.tag), toast].slice(-MAX_TOASTS);
      return toast;
    },
    list: (): Toast<A>[] => list,
    dismiss(id: number) {
      list = list.filter((t) => t.id !== id);
    },
    expire() {
      const t = now();
      list = list.filter((x) => t - x.at < TOAST_MS);
    },
  };
}
