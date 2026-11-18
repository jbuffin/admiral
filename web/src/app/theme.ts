import { computed, signal } from '@preact/signals';

export type Theme = 'light' | 'dark';

const KEY = 'admiral.theme';
const DARK = '(prefers-color-scheme: dark)';

function stored(): Theme | null {
  try {
    const v = localStorage.getItem(KEY);
    return v === 'light' || v === 'dark' ? v : null;
  } catch {
    return null;
  }
}

const systemQuery = () => (typeof matchMedia === 'function' ? matchMedia(DARK) : null);

/** The explicit choice, or null while the shell follows the system. */
const choice = signal<Theme | null>(stored());
// Held in a const: some engines drop the listeners of a query list nothing references.
const system = systemQuery();
const systemDark = signal(system?.matches ?? false);
system?.addEventListener('change', () => {
  systemDark.value = system.matches;
});

/** The theme showing now. */
export const theme = computed<Theme>(() => choice.value ?? (systemDark.value ? 'dark' : 'light'));

// Each theme-color meta keeps the color index.html gave it, so a choice can pick between them.
function paintChrome(t: Theme) {
  const metas = [...document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')];
  const base = (m: HTMLMetaElement) => (m.dataset.base ??= m.content);
  const chosen = metas.find((m) => (m.getAttribute('media') ?? '').includes(t));
  if (!chosen) return;
  const color = base(chosen);
  for (const m of metas) {
    base(m);
    m.content = color;
  }
}

function apply(t: Theme | null) {
  if (!t) return;
  document.documentElement.setAttribute('data-theme', t);
  paintChrome(t);
}

/** Applies the stored choice; main.tsx calls it before the first render. */
export function applyStoredTheme() {
  apply(stored());
}

export function toggleTheme() {
  const next: Theme = theme.value === 'dark' ? 'light' : 'dark';
  choice.value = next;
  try {
    localStorage.setItem(KEY, next);
  } catch {
    /* blocked storage: the choice lasts for the session */
  }
  apply(next);
}
