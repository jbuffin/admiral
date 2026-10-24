import { computed, effect, signal } from '@preact/signals';

// The size class, decided in one place (designs/README.md "Size classes"; the prototype's phoneQuery() and
// wideQuery()). A phone is narrow or short, which catches a phone turned sideways; wide is the iPad
// landscape and desktop layout, with the inbox as a drawer. Everything else is the middle class.
const PHONE = '(max-width: 760px), (max-height: 499px)';
const WIDE = '(min-width: 1024px) and (orientation: landscape)';

function watch(query: string) {
  const q = typeof matchMedia === 'function' ? matchMedia(query) : null;
  const on = signal(q?.matches ?? false);
  q?.addEventListener('change', (e) => (on.value = e.matches));
  return on;
}

export const phone = watch(PHONE);
const wideMatch = watch(WIDE);
export const wide = computed(() => wideMatch.value && !phone.value);

// Phone CSS keys off this one class on the root, never off its own media query.
if (typeof document !== 'undefined') {
  effect(() => {
    document.documentElement.classList.toggle('phone', phone.value);
  });
}
